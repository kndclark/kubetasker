# KubeTasker Prometheus & Grafana Setup Guide

This guide walks you through setting up Prometheus monitoring and Grafana dashboards for KubeTasker.

## Prerequisites

- Kind cluster running (or any Kubernetes cluster)
- Helm 3.x installed
- kubectl configured
- KubeTasker charts built

## Step 1: Install Prometheus Operator

```bash
make install-prometheus
```

This installs the `kube-prometheus-stack` which includes:
- Prometheus Operator
- Prometheus server
- Grafana
- AlertManager
- Node exporters
- Kube-state-metrics

**Wait for installation** (takes 2-3 minutes):
```bash
kubectl get pods -n monitoring -w
```

## Step 2: Deploy KubeTasker with Monitoring Enabled

```bash
make deploy-with-monitoring
```

This deploys KubeTasker with:
- `serviceMonitor.enabled=true` for both controller and frontend
- ServiceMonitor resources created in the `monitoring` namespace

**Verify ServiceMonitors created**:
```bash
kubectl get servicemonitor -n monitoring | grep kubetasker
```

Expected output:
```
kubetasker-controller   5m
kubetasker-frontend     5m
```

## Step 3: Verify Metrics Endpoints

### Controller Metrics

The controller exposes metrics on HTTPS port 8443 at `/metrics`.

```bash
# Port-forward controller metrics
kubectl port-forward -n kubetasker svc/kubetasker-umbrella-kubetasker-controller-metrics-service 8443:8443
```

In another terminal:
```bash
# Get service account token
TOKEN=$(kubectl create token default -n kubetasker --duration=1h)

# Query metrics
curl -k -H "Authorization: Bearer $TOKEN" https://localhost:8443/metrics
```

**Key metrics to look for**:
- `kubetasker_controller_reconcile_duration_seconds` - Reconciliation time histogram
- `kubetasker_controller_reconcile_errors_total` - Error counter
- `kubetasker_controller_active_reconciles` - Active reconciliations gauge

### Frontend Metrics

The frontend exposes metrics on HTTP port 8000 at `/metrics`.

```bash
# Port-forward frontend metrics
kubectl port-forward -n kubetasker svc/kubetasker-umbrella-kubetasker-frontend 8000:8000
```

In another terminal:
```bash
curl http://localhost:8000/metrics
```

**Key metrics to look for**:
- `ktask_status_count{namespace="...",phase="..."}` - Number of Ktasks by phase

## Step 4: Access Grafana

### Get Grafana Credentials

```bash
# Default username is 'admin'
# Get the password:
kubectl get secret -n monitoring kube-prometheus-stack-grafana -o jsonpath="{.data.admin-password}" | base64 --decode ; echo
```

### Port-Forward Grafana

```bash
make dashboard-prometheus
# Or manually:
kubectl port-forward -n monitoring svc/kube-prometheus-stack-grafana 3000:80
```

Access Grafana at: **http://localhost:3000**

## Step 5: Verify Prometheus Targets

1. In Grafana, go to **Connections** → **Data Sources**
2. Click on **Prometheus**
3. Scroll down and click **"Explore"**
4. Or navigate to **Status** → **Targets** in Prometheus UI:

```bash
kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090
```

Access Prometheus at: **http://localhost:9090**

Go to **Status → Targets** and verify you see:
- `serviceMonitor/monitoring/kubetasker-controller/0`
- `serviceMonitor/monitoring/kubetasker-frontend/0`

Both should show status: **UP** (green)

## Step 6: Create KubeTasker Dashboard

### Create a New Dashboard

1. In Grafana, click **+** → **Create Dashboard**
2. Click **Add visualization**
3. Select **Prometheus** as data source

### Panel 1: Ktasks by Phase

**Panel Title**: "Ktasks by Status (Current)"

**Query**:
```promql
sum by (phase) (ktask_status_count)
```

**Visualization**: Pie Chart or Stat

**Description**: Shows the current distribution of Ktasks across different phases (Pending, Processing, Succeeded, Failed)

---

### Panel 2: Reconciliation Duration

**Panel Title**: "Controller Reconciliation Duration (p95)"

**Query**:
```promql
histogram_quantile(0.95, 
  sum by (le, result) (
    rate(kubetasker_controller_reconcile_duration_seconds_bucket[5m])
  )
)
```

**Visualization**: Time Series (Line chart)

**Description**: Shows 95th percentile of reconciliation duration, split by success/error

---

### Panel 3: Reconciliation Errors

**Panel Title**: "Controller Errors (Rate)"

**Query**:
```promql
rate(kubetasker_controller_reconcile_errors_total[5m])
```

**Visualization**: Time Series

**Description**: Error rate over time

---

### Panel 4: Active Reconciliations

**Panel Title**: "Active Reconciles"

**Query**:
```promql
kubetasker_controller_active_reconciles
```

**Visualization**: Stat or Gauge

**Description**: Current number of active reconciliations

---

### Panel 5: Ktask Status Count Over Time

**Panel Title**: "Ktasks Over Time by Phase"

**Query**:
```promql
ktask_status_count
```

**Visualization**: Time Series (stacked)

**Group by**: `phase`

**Description**: Shows how Ktask counts change over time by phase

---

### Panel 6: Controller Pod Resource Usage (Optional)

**Panel Title**: "Controller Memory Usage"

**Query**:
```promql
container_memory_working_set_bytes{
  namespace="kubetasker",
  pod=~".*controller.*"
}
```

**Visualization**: Time Series

---

## Step 7: Test the Dashboard

### Create Test Ktasks

```bash
# Create some test Ktasks
for i in {1..5}; do
  kubectl apply -f - <<EOF
apiVersion: task.ktasker.com/v1
kind: Ktask
metadata:
  name: test-ktask-$i
  namespace: kubetasker
spec:
  image: busybox
  command: ["/bin/sh", "-c", "echo 'Hello from test $i'; sleep 30"]
EOF
done
```

### Watch Metrics Update

1. In Grafana dashboard, set refresh to **5s** or **10s**
2. Watch the panels update as Ktasks transition through phases:
   - **Pending** → **Processing** → **Succeeded**
3. Check the reconciliation duration increases as more Ktasks are processed
4. Verify `ktask_status_count` shows the changing numbers

### Clean Up Test Ktasks

```bash
kubectl delete ktask -n kubetasker --all
```

## Step 8: Save & Export Dashboard

1. Click **Save dashboard** (disk icon)
2. Give it a name: "KubeTasker Monitoring"
3. Click **Save**
4. To export as JSON: **Share** → **Export** → **Save to file**

## Taking Screenshots for Documentation

Take screenshots showing:

1. **Prometheus Targets page** - Both kubetasker targets showing UP
2. **Grafana dashboard overview** - All 5-6 panels showing metrics
3. **Ktask Status panel** - Showing distribution of Ktasks by phase
4. **Reconciliation duration chart** - Showing controller performance
5. **Metrics endpoint raw output** - `curl` output showing `ktask_status_count`

## Troubleshooting

### ServiceMonitors not appearing

Check if they're in the correct namespace:
```bash
kubectl get servicemonitor -A | grep kubetasker
```

They should be in the `monitoring` namespace, not the `kubetasker` namespace.

### Metrics not showing in Prometheus

1. Check Prometheus logs:
```bash
kubectl logs -n monitoring -l app.kubernetes.io/name=prometheus
```

2. Verify ServiceMonitor selector matches your services:
```bash
kubectl get svc -n kubetasker -o yaml | grep -A5 labels
kubectl get servicemonitor -n monitoring kubetasker-controller -o yaml | grep -A5 selector
```

### Frontend ktask_status_count always 0

The frontend metrics collector runs every 30 seconds. Wait at least 30s after creating Ktasks, then check:
```bash
kubectl logs -n kubetasker -l app.kubernetes.io/name=kubetasker-frontend | grep metrics
```

## Additional Resources

- [Prometheus Query Examples](https://prometheus.io/docs/prometheus/latest/querying/examples/)
- [Grafana Dashboard Best Practices](https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/best-practices/)
- [kube-prometheus-stack Documentation](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack)
