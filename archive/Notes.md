kubernetes:
- to load local images to kubernetes cluster: 
    `kind load docker-image simple-api:latest`
- once image is loaded, you can forward ports to run the service a kubernetes pod (ports must be configured properly in service YAMLs): 
    `kubectl port-forward deployment/job-listener-deployment 8000:8000`
- to stop the pod, run: `kubectl scale deployment job-listener-deployment --replicas=0`
    to start it again, run: `kubectl scale deployment job-listener-deployment --replicas=1`

example `create_job` POST request:
```
curl -X POST http://localhost:8000/jobs \
-H "Content-Type: application/json" \
-d '{
  "apiVersion": "custom.io/v1",
  "kind": "JobRequest",
  "metadata": { "name": "test-job-from-curl" },
  "spec": { "image": "busybox", "command": ["echo", "Hello from a test job"] }
}'
```