import os
import logging
import threading
import time
from collections import defaultdict

from kubernetes import client, config
from prometheus_client import Gauge

logger = logging.getLogger("kubetasker_frontend.metrics")

# Prometheus Metrics
# Tracks the number of Ktasks by phase in the current namespace
KTASK_STATUS_COUNT = Gauge(
    "ktask_status_count",
    "Number of Ktasks by status phase",
    ["namespace", "phase"]
)

def get_current_namespace():
    """
    Detects the namespace the pod is running in.
    """
    # Standard path for service account namespace
    ns_path = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
    if os.path.exists(ns_path):
        with open(ns_path, "r") as f:
            return f.read().strip()
    # Fallback for local development
    return os.environ.get("KUBERNETES_NAMESPACE", "default")

def collect_metrics_loop():
    """
    Background thread that queries the Kubernetes API for Ktask resources
    and updates the Prometheus metrics.
    """
    # Load Kubernetes configuration
    # We attempt to load config independently here to ensure the thread is self-contained
    try:
        config.load_incluster_config()
    except config.ConfigException:
        try:
            config.load_kube_config()
        except config.ConfigException:
            logger.error("Failed to load Kubernetes configuration. Metrics collection disabled.")
            return

    api = client.CustomObjectsApi()
    namespace = get_current_namespace()
    
    logger.info(f"Starting Ktask metrics collector for namespace: {namespace}")

    while True:
        try:
            # List Ktasks in the current namespace
            # Group: task.ktasker.com, Version: v1, Plural: ktasks
            response = api.list_namespaced_custom_object(
                group="task.ktasker.com",
                version="v1",
                namespace=namespace,
                plural="ktasks"
            )

            # Reset counts for this iteration
            counts = defaultdict(int)
            
            items = response.get("items", [])
            for item in items:
                status = item.get("status", {})
                phase = status.get("phase", "Unknown")
                counts[phase] += 1

            # Update Gauges
            KTASK_STATUS_COUNT.clear()
            for phase, count in counts.items():
                KTASK_STATUS_COUNT.labels(namespace=namespace, phase=phase).set(count)

        except client.exceptions.ApiException as e:
            logger.error(f"Kubernetes API error collecting Ktask metrics: {e}")
        except Exception as e:
            logger.exception("Unexpected error in metrics collector")


        # Scrape interval (30s matches the ServiceMonitor interval)
        time.sleep(30)

def start_collector():
    """Starts the background metrics collector thread."""
    t = threading.Thread(target=collect_metrics_loop, daemon=True)
    t.start()