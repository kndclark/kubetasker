
from fastapi import FastAPI, HTTPException, Response, Request, Depends
from fastapi.responses import HTMLResponse
from pydantic import BaseModel, Field, field_validator
import logging
import json
import os
import threading
import asyncio
import httpx
from typing import List, Optional
from contextlib import asynccontextmanager
from prometheus_client import generate_latest, CONTENT_TYPE_LATEST

import metrics

# Global HTTP client for reuse
http_client: Optional[httpx.AsyncClient] = None

# --- Structured Logging Setup ---
class JsonFormatter(logging.Formatter):
    def format(self, record):
        log_record = {
            "timestamp": self.formatTime(record, self.datefmt),
            "level": record.levelname,
            "message": record.getMessage(),
        }
        if record.exc_info:
            log_record["exception"] = self.formatException(record.exc_info)
        return json.dumps(log_record)

log = logging.getLogger("uvicorn")
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
    value_from: Optional[dict] = Field(None, alias="valueFrom")

class KtaskSpec(BaseModel):
    image: str
    command: Optional[List[str]] = None
    env: Optional[List[EnvVar]] = None
    restart_policy: str = Field("OnFailure", alias="restartPolicy")
    resources: Optional[dict] = None

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

CONTROLLER_URL = os.environ["CONTROLLER_URL"]

ktask_queue = asyncio.Queue(maxsize=1000)

# In-memory store for request statuses to handle async feedback
submission_status = {}
status_lock = threading.Lock()
MAX_STATUS_HISTORY = 1000

def update_submission_status(name: str, status: str, message: str = None):
    """Updates the in-memory status of a Ktask submission."""
    with status_lock:
        if len(submission_status) >= MAX_STATUS_HISTORY and name not in submission_status:
            submission_status.pop(next(iter(submission_status)))
        submission_status[name] = {"phase": status, "message": message}

def get_submission_status(name: str):
    """Retrieves the in-memory status of a Ktask submission."""
    with status_lock:
        return submission_status.get(name)

async def process_single_ktask(ktask: KtaskPayload):
    """Processes a single Ktask with retry logic via Controller API."""
    max_retries = 3
    for attempt in range(1, max_retries + 1):
        try:
            log.info(f"Processing Ktask '{ktask.metadata.name}', attempt {attempt}")
            # Reuse global http_client
            # We need to serialize the Pydantic model to JSON for the request
            body = ktask.model_dump(by_alias=True, exclude_none=True)
            resp = await http_client.post(f"{CONTROLLER_URL}/ktask", json=body)
                
            if resp.status_code in [200, 201]:
                log.info(f"Async created Ktask '{ktask.metadata.name}' via controller")
                update_submission_status(ktask.metadata.name, "Created", "Successfully submitted to Kubernetes")
                break
            elif resp.status_code == 409:
                log.warning(f"Ktask '{ktask.metadata.name}' already exists. Skipping.")
                update_submission_status(ktask.metadata.name, "Failed", "Ktask already exists")
                break
            else:
                raise Exception(f"Controller returned status {resp.status_code}: {resp.text}")

        except Exception as e:
            if attempt == max_retries:
                err_msg = f"Failed after {max_retries} attempts: {e}"
                log.error(f"Error processing buffered Ktask '{ktask.metadata.name}': {err_msg}")
                update_submission_status(ktask.metadata.name, "Failed", err_msg)
            else:
                log.warning(f"Error processing Ktask '{ktask.metadata.name}' (attempt {attempt}/{max_retries}): {e}. Retrying...")
                await asyncio.sleep(1 * attempt)


# Limit the number of concurrent worker tasks to avoid overwhelming the controller or exhausting resources
MAX_CONCURRENT_WORKERS = 20
worker_semaphore = asyncio.Semaphore(MAX_CONCURRENT_WORKERS)

async def process_ktasks():
    """Background worker to process buffered Ktask creation requests."""
    log.info("Starting Ktask processing worker")
    
    async def _bounded_processor(ktask):
        async with worker_semaphore:
            await process_single_ktask(ktask)

    while True:
        ktask = await ktask_queue.get()
        # Fire and forget (bounded by semaphore inside the wrapper), 
        # so we can pull the next item from the queue immediately.
        asyncio.create_task(_bounded_processor(ktask))
        ktask_queue.task_done()

@asynccontextmanager
async def lifespan(app: FastAPI):
    global http_client
    # Initialize shared client with limits to prevent exhaustion
    limits = httpx.Limits(max_keepalive_connections=50, max_connections=100)
    http_client = httpx.AsyncClient(limits=limits)
    
    metrics.start_collector() # Start the background metrics collector
    task = asyncio.create_task(process_ktasks())
    yield
    task.cancel()
    if http_client:
        await http_client.aclose()
    log.info("Shutting down.")

app = FastAPI(lifespan=lifespan)

# Expose Prometheus metrics
@app.get("/metrics")
async def metrics_handler():
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)

# Serve the GUI
@app.get("/", response_class=HTMLResponse)
async def get_gui():
    try:
        with open("static/index.html", "r") as f:
            return f.read()
    except FileNotFoundError:
        return "<h1>KubeTasker Dashboard (Development Mode)</h1><p>index.html not found.</p>"

@app.get("/healthz")
def health_check():
    return {"status": "ok"}

# Create a new Ktask custom resource.
@app.post("/ktask", status_code=202)
async def create_ktask(ktask: KtaskPayload):
    '''
    Accept POST /ktask to queue a new Ktask CRD creation to the Controller.
    '''
    log.info(f"Buffering request to create Ktask '{ktask.metadata.name}' in namespace '{ktask.metadata.namespace}'")
    update_submission_status(ktask.metadata.name, "Pending", "Request buffered in queue")
    await ktask_queue.put(ktask)
    return {"message": "Ktask buffered", "ktask_name": ktask.metadata.name}

# List/query job statuses.
@app.get("/ktask")
async def list_ktasks(namespace: str = "default"):
    '''
    Proxy GET /ktask to the controller API.
    '''
    log.info(f"Proxying list Ktasks request for namespace '{namespace}'")
    # Use global http_client directly
    try:
        resp = await http_client.get(f"{CONTROLLER_URL}/ktask", params={"namespace": namespace})
        return Response(content=resp.content, status_code=resp.status_code, media_type="application/json")
    except httpx.RequestError as exc:
        log.error(f"An error occurred while requesting {exc.request.url!r}.")
        raise HTTPException(status_code=503, detail="Controller unavailable")

# Get a single job's status.
@app.get("/ktask/{job_name}")
async def get_ktask(job_name: str, namespace: str = "default"):
    """
    Proxy GET /ktask/{name} to the controller API, checking local buffer first.
    """
    # Check in-memory status for async failures or pending state
    local_status = get_submission_status(job_name)
    # Only return local status if it's Pending or Failed. 
    # If it's "Created", we should try to get the authoritative status from the controller.
    if local_status and local_status["phase"] in ["Pending", "Failed"]:
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

    # Use global http_client directly.
    # Note: We do not use 'async with' on the shared client here.
    # Correct usage: just use http_client directy.
    try:
        resp = await http_client.get(f"{CONTROLLER_URL}/ktask/{job_name}", params={"namespace": namespace})
        return Response(content=resp.content, status_code=resp.status_code, media_type="application/json")
    except httpx.RequestError as exc:
        log.error(f"An error occurred while requesting {exc.request.url!r}.")
        raise HTTPException(status_code=503, detail="Controller unavailable")

@app.delete("/ktask/{job_name}")
async def delete_ktask(job_name: str, namespace: str = "default"):
    '''
    Proxy DELETE /ktask/{name} to the controller API.
    '''
    log.info(f"Proxying delete Ktask '{job_name}' in namespace '{namespace}'")
    # Use global http_client directly
    try:
        resp = await http_client.delete(f"{CONTROLLER_URL}/ktask/{job_name}", params={"namespace": namespace})
        return Response(content=resp.content, status_code=resp.status_code)
    except httpx.RequestError as exc:
        log.error(f"An error occurred while requesting {exc.request.url!r}.")
        raise HTTPException(status_code=503, detail="Controller unavailable")