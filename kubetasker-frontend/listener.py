
from fastapi import FastAPI, HTTPException, Response
import subprocess
import tempfile
import yaml


app = FastAPI()

# Allow external systems to check if app is running/ working
@app.get("/healthz")
def health_check():
    '''
    Expose a simple Health check endpoint
    '''
    return {"status": "ok"}

# Generate JobRequest YAML and apply via kubectl.
@app.post("/jobrequest")
def create_job_request(job_request: dict):
    '''
    Accept POST /jobrequest to create a new JobRequest CRD.
    '''
    try:
        # Use a temporary file to store the job request YAML.
        with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix=".yaml") as f:
            yaml.dump(job_request, f)
            temp_file_name = f.name

        try:
            # Use check=True to raise an exception on failure.
            result = subprocess.run(
                ["kubectl", "apply", "-f", temp_file_name],
                capture_output=True, text=True, check=True)
            
            return {"message": "JobRequest submitted", "job_request": job_request, "kubectl_output": result.stdout}
        finally:
            # Clean up the temporary file.
            import os
            os.remove(temp_file_name)

    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        # If kubectl fails, return a 500 error with the details
        error_details = e.stderr if hasattr(e, 'stderr') and e.stderr else str(e)
        raise HTTPException(status_code=500, detail={"error": "Failed to apply job to Kubernetes", "details": error_details})

# List/query job statuses.
@app.get("/jobrequest")
def list_job_requests():
    '''
    Provide GET endpoint for querying all JobRequest statuses.
    '''
    try:
        result = subprocess.run(
            ["kubectl", "get", "jobrequests", "-o", "json"],
            capture_output=True, text=True, check=True)
         # If kubectl returns nothing, send back a valid empty JSON list
        if not result.stdout.strip():
            return {"items": []}
        # Return the raw JSON from kubectl as a proper JSON response
        return Response(content=result.stdout, media_type="application/json")
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        error_details = e.stderr if hasattr(e, 'stderr') and e.stderr else str(e)
        raise HTTPException(status_code=500, detail={"error": "Failed to retrieve cluster jobs", "details": error_details})

# Get a single job's status.
@app.get("/jobrequest/{job_name}")
def get_job_request(job_name: str):
    '''
    Provide GET endpoint for querying a single JobRequest's status.
    '''
    try:
        result = subprocess.run(
            ["kubectl", "get", "jobrequest", job_name, "-o", "json"],
            capture_output=True, text=True, check=True)
        return Response(content=result.stdout, media_type="application/json")
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        error_details = e.stderr if hasattr(e, 'stderr') and e.stderr else str(e)
        raise HTTPException(status_code=500, detail={"error": "Failed to retrieve cluster jobs", "details": error_details})