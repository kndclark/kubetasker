from fastapi.testclient import TestClient
from unittest.mock import patch, MagicMock
import json

from listener import app

client = TestClient(app)


def test_health_check():
    """
    Tests the /healthz endpoint.
    """
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}


@patch("subprocess.run")
@patch("builtins.open")
def test_create_job(mock_open, mock_subprocess_run):
    """
    Tests the POST /jobs endpoint, mocking the file write and kubectl call.
    """
    job_payload = {
        "apiVersion": "custom.io/v1",
        "kind": "JobRequest",
        "metadata": {"name": "test-job-1"},
        "spec": {"image": "busybox", "command": ["echo", "test"]},
    }

    response = client.post("/jobs", json=job_payload)

    # Assertions
    assert response.status_code == 200
    assert response.json() == {"message": "Job submitted", "job": job_payload}

    # Verify that open was called to write the YAML file
    mock_open.assert_called_once_with("job.yaml", "w")

    # Verify that kubectl apply was called
    mock_subprocess_run.assert_called_once_with(
        ["kubectl", "apply", "-f", "job.yaml"]
    )


@patch("subprocess.run")
def test_list_jobs(mock_subprocess_run):
    """
    Tests the GET /jobs endpoint, mocking the kubectl call.
    """
    # Create a mock return value for the subprocess call
    mock_kubectl_output = '{"items": [{"metadata": {"name": "test-job-1"}}]}'
    mock_process = MagicMock()
    mock_process.stdout = mock_kubectl_output
    mock_subprocess_run.return_value = mock_process

    response = client.get("/jobs")

    assert response.status_code == 200
    assert response.text == mock_kubectl_output

    # Verify that kubectl get was called
    mock_subprocess_run.assert_called_once_with(
        ["kubectl", "get", "jobrequests", "-o", "json"], capture_output=True, text=True
    )