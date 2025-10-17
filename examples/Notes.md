kubernetes:
- to load local images to kubernetes cluster: 
    `kind load docker-image simple-api:latest`
- once image is loaded, you can forward ports to run the service a kubernetes pod (ports must be configured properly in service YAMLs): 
    `kubectl port-forward deployment/job-listener-deployment 8000:8000`
- to stop the pod, run: `kubectl scale deployment job-listener-deployment --replicas=0`
    to start it again, run: `kubectl scale deployment job-listener-deployment --replicas=1`

To update JobRequests, update `jobrequest_types.go` and:
- `make manifests`: It reads your updated jobrequest_types.go file and regenerates the Custom Resource Definition (CRD) manifest in config/crd/bases/.

- `make generate`: It updates the auto-generated Go code (like the zz_generated.deepcopy.go file) to include methods for your new Phase field.

After `make manifests` generates the CRD (located `config/crd/bases/<domain>_jobrequests.yaml`), then you need to install it (register it with Kubernetes cluster)
- run `make install`

INSTALL CERT-MANAGER:
```
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.5/cert-manager.yaml
```

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