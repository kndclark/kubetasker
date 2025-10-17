
from fastapi import FastAPI, HTTPException, Response
import subprocess
import yaml


app = FastAPI()

# Allow external systems to check if app is running/ working
@app.get("/healthz")
def health_check():
    '''
    Expose a simple Health check endpoint
    '''
    return {"status": "ok"}

# Generate JobRequest YAML and apply via kubectl
@app.post("/jobs")
def create_job(job: dict):
    '''
    Accept POST /jobs to create new JobRequest CRD
    '''
    try:
        with open("job.yaml", "w") as f:
            yaml.dump(job, f)
        
        # Use check=True to raise an exception on failure
        # Add --insecure-skip-tls-verify to bypass the certificate hostname mismatch for local development.
        result = subprocess.run(
            ["kubectl", "apply", "-f", "job.yaml", "--insecure-skip-tls-verify=true"],
            capture_output=True, text=True, check=True)
        
        return {"message": "Job submitted", "job": job, "kubectl_output": result.stdout}
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        # If kubectl fails, return a 500 error with the details
        error_details = e.stderr if hasattr(e, 'stderr') and e.stderr else str(e)
        raise HTTPException(status_code=500, detail={"error": "Failed to apply job to Kubernetes", "details": error_details})

# List/ query job statuses
@app.get("/jobs")
def list_jobs():
    '''
    Provide GET endpoint for querying JobRequest statuses
    '''
    try:
        result = subprocess.run(
            ["kubectl", "get", "jobrequests", "-o", "json", "--insecure-skip-tls-verify=true"],
            capture_output=True, text=True, check=True)
         # If kubectl returns nothing, send back a valid empty JSON list
        if not result.stdout.strip():
            return {"items": []}
        # Return the raw JSON from kubectl as a proper JSON response
        return Response(content=result.stdout, media_type="application/json")
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        error_details = e.stderr if hasattr(e, 'stderr') and e.stderr else str(e)
        raise HTTPException(status_code=500, detail={"error": "Failed to retrieve cluster jobs", "details": error_details})