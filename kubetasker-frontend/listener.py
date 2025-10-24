
from fastapi import FastAPI, HTTPException, Response
from kubernetes import client, config
from kubernetes.client.exceptions import ApiException


app = FastAPI()

# Load Kubernetes configuration
try:
    # Try to load in-cluster configuration
    config.load_incluster_config()
except config.ConfigException:
    # Fallback to kube-config for local development
    config.load_kube_config()

# API clients
custom_objects_api = client.CustomObjectsApi()

# Allow external systems to check if app is running/ working
@app.get("/healthz")
def health_check():
    '''
    Expose a simple Health check endpoint
    '''
    return {"status": "ok"}

# Create a new JobRequest custom resource.
@app.post("/jobrequest")
def create_job_request(job_request: dict):
    '''
    Accept POST /jobrequest to create a new JobRequest CRD.
    '''
    try:
        namespace = job_request.get("metadata", {}).get("namespace", "default")
        api_response = custom_objects_api.create_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            namespace=namespace,
            plural="jobrequests",
            body=job_request,
        )
        return {"message": "JobRequest submitted", "job_request": api_response}
    except ApiException as e:
        raise HTTPException(status_code=e.status, detail={"error": "Failed to create JobRequest", "details": e.reason, "body": e.body})

# List/query job statuses.
@app.get("/jobrequest")
def list_job_requests(namespace: str = "default"):
    '''
    Provide GET endpoint for querying JobRequest statuses in a given namespace.
    '''
    try:
        api_response = custom_objects_api.list_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            namespace=namespace,
            plural="jobrequests",
        )
        return api_response
    except ApiException as e:
        raise HTTPException(status_code=e.status, detail={"error": "Failed to list JobRequests", "details": e.reason, "body": e.body})

# Get a single job's status.
@app.get("/jobrequest/{job_name}")
def get_job_request(job_name: str, namespace: str = "default"):
    '''
    Provide GET endpoint for querying a single JobRequest's status in a given namespace.
    '''
    try:
        api_response = custom_objects_api.get_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            name=job_name,
            namespace=namespace,
            plural="jobrequests",
        )
        return api_response
    except ApiException as e:
        if e.status == 404:
            raise HTTPException(status_code=404, detail=f"JobRequest '{job_name}' not found in namespace '{namespace}'.")
        raise HTTPException(status_code=e.status, detail={"error": f"Failed to retrieve JobRequest '{job_name}'", "details": e.reason, "body": e.body})