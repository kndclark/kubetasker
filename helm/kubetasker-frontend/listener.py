
from fastapi import FastAPI, HTTPException, Response, Request
from fastapi.responses import HTMLResponse
import logging
import json
import os
import httpx
from contextlib import asynccontextmanager
from prometheus_client import make_asgi_app

import metrics

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

CONTROLLER_URL = os.environ["CONTROLLER_URL"]

@asynccontextmanager
async def lifespan(app: FastAPI):
    metrics.start_collector() # Start the background metrics collector
    yield
    log.info("Shutting down.")

app = FastAPI(lifespan=lifespan)

# Expose Prometheus metrics
metrics_app = make_asgi_app()
app.mount("/metrics", metrics_app)

# Serve the GUI
@app.get("/", response_class=HTMLResponse)
async def get_gui():
    """Serves the KubeTasker Dashboard."""
    with open(os.path.join(os.path.dirname(__file__), "static/index.html")) as f:
        return f.read()

# Allow external systems to check if app is running/ working
@app.get("/healthz")
def health_check():
    '''
    Expose a simple Health check endpoint
    '''
    return {"status": "ok"}

# Create a new Ktask custom resource.
@app.post("/ktask")
async def create_ktask(request: Request):
    '''
    Proxy POST /ktask to the controller API.
    '''
    body = await request.body()
    log.info("Proxying Ktask creation to controller")
    async with httpx.AsyncClient() as client:
        try:
            resp = await client.post(f"{CONTROLLER_URL}/ktask", content=body, headers=request.headers)
            return Response(content=resp.content, status_code=resp.status_code, media_type="application/json")
        except httpx.RequestError as exc:
            log.error(f"An error occurred while requesting {exc.request.url!r}.")
            raise HTTPException(status_code=503, detail="Controller unavailable")

# List/query job statuses.
@app.get("/ktask")
async def list_ktasks(namespace: str = "default"):
    '''
    Proxy GET /ktask to the controller API.
    '''
    log.info(f"Proxying list Ktasks request for namespace '{namespace}'")
    async with httpx.AsyncClient() as client:
        try:
            resp = await client.get(f"{CONTROLLER_URL}/ktask", params={"namespace": namespace})
            return Response(content=resp.content, status_code=resp.status_code, media_type="application/json")
        except httpx.RequestError as exc:
            log.error(f"An error occurred while requesting {exc.request.url!r}.")
            raise HTTPException(status_code=503, detail="Controller unavailable")

# Get a single job's status.
@app.get("/ktask/{job_name}")
async def get_ktask(job_name: str, namespace: str = "default"):
    '''
    Proxy GET /ktask/{name} to the controller API.
    Note: The controller API currently implements list (GET /ktask) and delete (DELETE /ktask/{name}).
    For getting a single item, we might need to filter the list or add a specific endpoint in the controller.
    For now, we will assume the controller might support it or we rely on list filtering if implemented.
    However, based on the provided controller code, only List and Delete are implemented.
    This endpoint might need to be adjusted to filter client-side or update controller.
    '''
    # Implementation omitted as controller doesn't explicitly support GET single item in the provided snippet.
    # But we can try to proxy it anyway if the controller is updated later.
    raise HTTPException(status_code=501, detail="Get single Ktask not implemented in controller API yet")

@app.delete("/ktask/{job_name}")
async def delete_ktask(job_name: str, namespace: str = "default"):
    '''
    Proxy DELETE /ktask/{name} to the controller API.
    '''
    log.info(f"Proxying delete Ktask '{job_name}' in namespace '{namespace}'")
    async with httpx.AsyncClient() as client:
        try:
            resp = await client.delete(f"{CONTROLLER_URL}/ktask/{job_name}", params={"namespace": namespace})
            return Response(content=resp.content, status_code=resp.status_code)
        except httpx.RequestError as exc:
            log.error(f"An error occurred while requesting {exc.request.url!r}.")
            raise HTTPException(status_code=503, detail="Controller unavailable")