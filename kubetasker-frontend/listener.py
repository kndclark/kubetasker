
from fastapi import FastAPI, HTTPException, Response, Depends
from pydantic import BaseModel, Field, field_validator
import logging
import json
import os
from typing import List, Optional
from kubernetes import client, config
from kubernetes.client.exceptions import ApiException
from contextlib import asynccontextmanager

# --- Structured Logging Setup ---
class JsonFormatter(logging.Formatter):
    """Formats log records as a JSON string."""
    def format(self, record):
        log_record = {
            "timestamp": self.formatTime(record, self.datefmt),
            "level": record.levelname,
            "message": record.getMessage(),
        }
        if record.exc_info:
            log_record['exc_info'] = self.formatException(record.exc_info)
        return json.dumps(log_record)

log = logging.getLogger("kubetasker_frontend")
log.setLevel(logging.INFO)
handler = logging.StreamHandler()
handler.setFormatter(JsonFormatter())
log.addHandler(handler)

# Pydantic models for data validation and API documentation
class ObjectMeta(BaseModel):
    name: str
    namespace: str = "default"

class EnvVar(BaseModel):
    """Represents an environment variable in a container."""
    name: str
    value: Optional[str] = None
    # A full implementation would model valueFrom sources (Secret, ConfigMap),
    # but this is a great start for validation.
    value_from: Optional[dict] = Field(None, alias="valueFrom")

class JobRequestSpec(BaseModel):
    image: str
    command: Optional[List[str]] = None
    env: Optional[List[EnvVar]] = None
    restart_policy: str = Field("OnFailure", alias="restartPolicy")

class JobRequestPayload(BaseModel):
    api_version: str = Field(..., alias="apiVersion")
    kind: str
    metadata: ObjectMeta
    spec: JobRequestSpec

    @field_validator('api_version')
    def validate_api_version(cls, v):
        if v != "task.ktasker.com/v1":
            raise ValueError('apiVersion must be "task.ktasker.com/v1"')
        return v
    
    @field_validator('kind')
    def validate_kind(cls, v):
        if v != "JobRequest":
            raise ValueError('kind must be "JobRequest"')
        return v

# This will be initialized once and cached.
custom_objects_api = None

def get_k8s_api():
    """
    FastAPI dependency to get a Kubernetes CustomObjectsApi client.
    It initializes the client on the first call and caches it.
    It handles different configuration strategies for production vs. development.
    """
    global custom_objects_api
    if custom_objects_api:
        return custom_objects_api

    env = os.getenv("KUBETASKER_ENV")
    log.info(f"Initializing Kubernetes client for environment: '{env}'")

    # In production (default), we must load in-cluster config. Fail fast if we can't.
    # In development, we try a sequence of configurations gracefully.
    is_development = env == "development"

    try:
        config.load_incluster_config()
        log.info("Loaded in-cluster Kubernetes configuration.")
    except config.ConfigException:
        if not is_development:
            log.error("Failed to load in-cluster config in a non-development environment.")
            raise

        log.info("In-cluster config failed. Falling back to kube-config for development.")
        try:
            config.load_kube_config()
            log.info("Loaded local kube-config configuration.")
        except config.ConfigException:
            log.warning("Could not load any Kubernetes configuration. API endpoints will fail.")
            # We return None, and endpoints will handle this.
            return None

    custom_objects_api = client.CustomObjectsApi()
    return custom_objects_api

@asynccontextmanager
async def lifespan(app: FastAPI):
    # This is the modern way to handle startup logic.
    # We can pre-warm the Kubernetes client on startup if we want,
    # or just let it initialize on the first request.
    get_k8s_api() # Optional: pre-warm the client at startup
    yield
    # Clean up resources if needed on shutdown
    log.info("Shutting down.")

app = FastAPI(lifespan=lifespan)

# Allow external systems to check if app is running/ working
@app.get("/healthz")
def health_check():
    '''
    Expose a simple Health check endpoint
    '''
    return {"status": "ok"}

# Create a new JobRequest custom resource.
@app.post("/jobrequest")
def create_job_request(
    job_request: JobRequestPayload,
    api: client.CustomObjectsApi = Depends(get_k8s_api)
):
    '''
    Accept POST /jobrequest to create a new JobRequest CRD.
    '''
    if api is None:
        raise HTTPException(status_code=503, detail="Service is unavailable: Cannot connect to Kubernetes cluster.")
    log.info(f"Received request to create JobRequest '{job_request.metadata.name}' in namespace '{job_request.metadata.namespace}'")
    try:
        # Convert the Pydantic model back to a dict for the k8s client.
        # `by_alias=True` ensures fields like `restartPolicy` use their correct names.
        job_request_dict = job_request.model_dump(by_alias=True, exclude_none=True)
        api_response = api.create_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            namespace=job_request.metadata.namespace,
            plural="jobrequests",
            body=job_request_dict,
        )
        log.info(f"Successfully created JobRequest '{job_request.metadata.name}'")
        return {"message": "JobRequest submitted", "job_request": api_response}
    except ApiException as e:
        log.error(f"Failed to create JobRequest '{job_request.metadata.name}'", exc_info=True)
        raise HTTPException(status_code=e.status, detail={"error": "Failed to create JobRequest", "details": e.reason, "body": json.loads(e.body)})

# List/query job statuses.
@app.get("/jobrequest")
def list_job_requests(
    namespace: str = "default",
    api: client.CustomObjectsApi = Depends(get_k8s_api)
):
    '''
    Provide GET endpoint for querying JobRequest statuses in a given namespace.
    '''
    if api is None:
        raise HTTPException(status_code=503, detail="Service is unavailable: Cannot connect to Kubernetes cluster.")
    log.info(f"Received request to list JobRequests in namespace '{namespace}'")
    try:
        api_response = api.list_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            namespace=namespace,
            plural="jobrequests",
        )
        return api_response
    except ApiException as e:
        log.error(f"Failed to list JobRequests in namespace '{namespace}'", exc_info=True)
        raise HTTPException(status_code=e.status, detail={"error": "Failed to list JobRequests", "details": e.reason, "body": json.loads(e.body)})

# Get a single job's status.
@app.get("/jobrequest/{job_name}")
def get_job_request(
    job_name: str,
    namespace: str = "default",
    api: client.CustomObjectsApi = Depends(get_k8s_api)
):
    '''
    Provide GET endpoint for querying a single JobRequest's status in a given namespace.
    '''
    if api is None:
        raise HTTPException(status_code=503, detail="Service is unavailable: Cannot connect to Kubernetes cluster.")
    log.info(f"Received request to get JobRequest '{job_name}' in namespace '{namespace}'")
    try:
        api_response = api.get_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            name=job_name,
            namespace=namespace,
            plural="jobrequests",
        )
        return api_response
    except ApiException as e:
        if e.status == 404:
            log.warning(f"JobRequest '{job_name}' not found in namespace '{namespace}'")
            raise HTTPException(status_code=404, detail=f"JobRequest '{job_name}' not found in namespace '{namespace}'.")
        log.error(f"Failed to retrieve JobRequest '{job_name}'", exc_info=True)
        raise HTTPException(status_code=e.status, detail={"error": f"Failed to retrieve JobRequest '{job_name}'", "details": e.reason, "body": json.loads(e.body)})