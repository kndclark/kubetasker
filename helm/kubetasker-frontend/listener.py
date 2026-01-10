
from fastapi import FastAPI, HTTPException, Response, Depends
from pydantic import BaseModel, Field, field_validator
import logging
import json
import os
import threading
import asyncio
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

class KtaskSpec(BaseModel):
    image: str
    command: Optional[List[str]] = None
    env: Optional[List[EnvVar]] = None
    restart_policy: str = Field("OnFailure", alias="restartPolicy")

class KtaskPayload(BaseModel):
    api_version: str = Field(..., alias="apiVersion")
    kind: str
    metadata: ObjectMeta
    spec: KtaskSpec

    @field_validator('api_version')
    def validate_api_version(cls, v):
        if v != "task.ktasker.com/v1":
            raise ValueError('apiVersion must be "task.ktasker.com/v1"')
        return v
    
    @field_validator('kind')
    def validate_kind(cls, v):
        if v != "Ktask":
            raise ValueError('kind must be "Ktask"')
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

ktask_queue = asyncio.Queue(maxsize=1000)

# In-memory store for request statuses to handle async feedback
submission_status = {}
status_lock = threading.Lock()
MAX_STATUS_HISTORY = 1000

def update_submission_status(name: str, status: str, message: str = None):
    """Updates the in-memory status of a Ktask submission."""
    with status_lock:
        # Simple LRU-like eviction: remove oldest if full
        if len(submission_status) >= MAX_STATUS_HISTORY and name not in submission_status:
            submission_status.pop(next(iter(submission_status)))
        submission_status[name] = {"phase": status, "message": message}

def get_submission_status(name: str):
    """Retrieves the in-memory status of a Ktask submission."""
    with status_lock:
        return submission_status.get(name)

async def process_single_ktask(ktask):
    """Processes a single Ktask with retry logic."""
    loop = asyncio.get_running_loop()
    max_retries = 3
    for attempt in range(1, max_retries + 1):
        try:
            api = get_k8s_api()
            if api:
                # Run blocking K8s call in thread pool
                await loop.run_in_executor(
                    None,
                    lambda: api.create_namespaced_custom_object(
                        group="task.ktasker.com",
                        version="v1",
                        namespace=ktask.metadata.namespace,
                        plural="ktasks",
                        body=ktask.model_dump(by_alias=True, exclude_none=True),
                    )
                )
                log.info(f"Async created Ktask '{ktask.metadata.name}'")
                update_submission_status(ktask.metadata.name, "Created", "Successfully submitted to Kubernetes")
                break
            else:
                log.error(f"K8s API unavailable, dropping Ktask '{ktask.metadata.name}'")
                update_submission_status(ktask.metadata.name, "Failed", "Kubernetes API unavailable")
                break
        except Exception as e:
            if attempt == max_retries:
                err_msg = f"Failed after {max_retries} attempts: {e}"
                log.error(f"Error processing buffered Ktask '{ktask.metadata.name}': {err_msg}")
                update_submission_status(ktask.metadata.name, "Failed", err_msg)
            else:
                log.warning(f"Error processing Ktask '{ktask.metadata.name}' (attempt {attempt}/{max_retries}): {e}. Retrying...")
                await asyncio.sleep(1 * attempt)

async def process_ktasks():
    """Background worker to process buffered Ktask creation requests."""
    log.info("Starting Ktask processing worker")
    while True:
        ktask = await ktask_queue.get()
        await process_single_ktask(ktask)
        ktask_queue.task_done()

@asynccontextmanager
async def lifespan(app: FastAPI):
    get_k8s_api() # Optional: pre-warm the client at startup
    task = asyncio.create_task(process_ktasks())
    yield
    task.cancel()
    log.info("Shutting down.")

app = FastAPI(lifespan=lifespan)

# Allow external systems to check if app is running/ working
@app.get("/healthz")
def health_check():
    '''
    Expose a simple Health check endpoint
    '''
    return {"status": "ok"}

# Create a new Ktask custom resource.
@app.post("/ktask", status_code=202)
async def create_ktask(
    ktask: KtaskPayload,
):
    '''
    Accept POST /ktask to queue a new Ktask CRD creation.
    '''
    log.info(f"Buffering request to create Ktask '{ktask.metadata.name}' in namespace '{ktask.metadata.namespace}'")
    update_submission_status(ktask.metadata.name, "Pending", "Request buffered in queue")
    await ktask_queue.put(ktask)
    return {"message": "Ktask buffered", "ktask_name": ktask.metadata.name}

# List/query job statuses.
@app.get("/ktask")
def list_ktasks(
    namespace: str = "default",
    api: client.CustomObjectsApi = Depends(get_k8s_api)
):
    '''
    Provide GET endpoint for querying Ktask statuses in a given namespace.
    '''
    if api is None:
        raise HTTPException(status_code=503, detail="Service is unavailable: Cannot connect to Kubernetes cluster.")
    log.info(f"Received request to list Ktasks in namespace '{namespace}'")
    try:
        api_response = api.list_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            namespace=namespace,
            plural="ktasks",
        )
        return api_response
    except ApiException as e:
        log.error(f"Failed to list Ktasks in namespace '{namespace}'", exc_info=True)
        raise HTTPException(status_code=e.status, detail={"error": "Failed to list Ktasks", "details": e.reason, "body": json.loads(e.body)})

# Get a single job's status.
@app.get("/ktask/{job_name}")
def get_ktask(
    job_name: str,
    namespace: str = "default",
    api: client.CustomObjectsApi = Depends(get_k8s_api)
):
    '''
    Provide GET endpoint for querying a single Ktask's status in a given namespace.
    '''
    if api is None:
        raise HTTPException(status_code=503, detail="Service is unavailable: Cannot connect to Kubernetes cluster.")
    log.info(f"Received request to get Ktask '{job_name}' in namespace '{namespace}'")
    try:
        api_response = api.get_namespaced_custom_object(
            group="task.ktasker.com",
            version="v1",
            name=job_name,
            namespace=namespace,
            plural="ktasks",
        )
        return api_response
    except ApiException as e:
        if e.status == 404:
            # Check in-memory status for async failures or pending state
            local_status = get_submission_status(job_name)
            if local_status:
                return {
                    "apiVersion": "task.ktasker.com/v1",
                    "kind": "Ktask",
                    "metadata": {"name": job_name, "namespace": namespace},
                    "status": {
                        "phase": local_status["phase"],
                        "message": local_status["message"],
                        "reason": "AsyncSubmissionStatus" 
                    }
                }
            log.warning(f"Ktask '{job_name}' not found in namespace '{namespace}'")
            raise HTTPException(status_code=404, detail=f"Ktask '{job_name}' not found in namespace '{namespace}'.")
        log.error(f"Failed to retrieve Ktask '{job_name}'", exc_info=True)
        raise HTTPException(status_code=e.status, detail={"error": f"Failed to retrieve Ktask '{job_name}'", "details": e.reason, "body": json.loads(e.body)})