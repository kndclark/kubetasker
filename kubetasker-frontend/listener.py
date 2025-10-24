
from fastapi import FastAPI, HTTPException, Response
from pydantic import BaseModel, Field, field_validator
import logging
import json
from typing import List, Optional
from kubernetes import client, config
from kubernetes.client.exceptions import ApiException

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
def create_job_request(job_request: JobRequestPayload):
    '''
    Accept POST /jobrequest to create a new JobRequest CRD.
    '''
    log.info(f"Received request to create JobRequest '{job_request.metadata.name}' in namespace '{job_request.metadata.namespace}'")
    try:
        # Convert the Pydantic model back to a dict for the k8s client.
        # `by_alias=True` ensures fields like `restartPolicy` use their correct names.
        job_request_dict = job_request.model_dump(by_alias=True, exclude_none=True)
        api_response = custom_objects_api.create_namespaced_custom_object(
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
def list_job_requests(namespace: str = "default"):
    '''
    Provide GET endpoint for querying JobRequest statuses in a given namespace.
    '''
    log.info(f"Received request to list JobRequests in namespace '{namespace}'")
    try:
        api_response = custom_objects_api.list_namespaced_custom_object(
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
def get_job_request(job_name: str, namespace: str = "default"):
    '''
    Provide GET endpoint for querying a single JobRequest's status in a given namespace.
    '''
    log.info(f"Received request to get JobRequest '{job_name}' in namespace '{namespace}'")
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
            log.warning(f"JobRequest '{job_name}' not found in namespace '{namespace}'")
            raise HTTPException(status_code=404, detail=f"JobRequest '{job_name}' not found in namespace '{namespace}'.")
        log.error(f"Failed to retrieve JobRequest '{job_name}'", exc_info=True)
        raise HTTPException(status_code=e.status, detail={"error": f"Failed to retrieve JobRequest '{job_name}'", "details": e.reason, "body": json.loads(e.body)})