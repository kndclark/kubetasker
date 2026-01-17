import os
import logging
import threading
import time
from collections import defaultdict

from kubernetes import client, config
from prometheus_client import Gauge

# Use uvicorn logger for visibility in pod logs
logger = logging.getLogger("uvicorn.error")

# Prometheus Metrics
# Tracks the number of Ktasks by phase cluster-wide
KTASK_STATUS_COUNT = Gauge(
    "ktask_status_count",
    "Number of Ktasks by status phase",
    ["namespace", "phase"]
)

def collect_metrics_loop():
    """
    Background thread that queries the Kubernetes API for Ktask resources
    across all namespaces and updates the Prometheus metrics.
    """
    logger.info("Initializing metrics collector thread...")
    
    # Load Kubernetes configuration
    try:
        config.load_incluster_config()
    except config.ConfigException:
        try:
            config.load_kube_config()
        except config.ConfigException:
            logger.error("Failed to load Kubernetes configuration. Metrics collection disabled.")
            return

    api = client.CustomObjectsApi()
    logger.info("Ktask metrics collector started successfully")

    while True:
        try:
            # List Ktasks cluster-wide (across all namespaces)
            # Some versions use 'plural', some use 'resource_plural'
            # Using positional arguments to be safe
            response = api.list_custom_object_for_all_namespaces(
                "task.ktasker.com",
                "v1",
                "ktasks"
            )

            # Reset counts for this iteration
            counts = defaultdict(int)
            
            items = response.get("items", [])
            for item in items:
                metadata = item.get("metadata", {})
                ns = metadata.get("namespace", "unknown")
                status = item.get("status", {})
                phase = status.get("phase", "Unknown")
                counts[(ns, phase)] += 1

            # Update Gauges
            KTASK_STATUS_COUNT.clear()
            
            if not counts:
                logger.debug("No Ktasks found in any namespace")
            
            for (ns, phase), count in counts.items():
                KTASK_STATUS_COUNT.labels(namespace=ns, phase=phase).set(count)
                
        except client.exceptions.ApiException as e:
            logger.error(f"Kubernetes API error collecting Ktask metrics: {e}")
        except Exception as e:
            logger.error(f"Unexpected error in metrics collector: {str(e)}")

        # Scrape interval (30s matches the ServiceMonitor interval)
        time.sleep(30)

def start_collector():
    """Starts the background metrics collector thread."""
    t = threading.Thread(target=collect_metrics_loop, daemon=True)
    t.start()