from fastapi.testclient import TestClient
from unittest.mock import patch, MagicMock
import os
import tempfile

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
@patch("os.remove")
@patch("tempfile.NamedTemporaryFile")
def test_create_job_request(mock_tempfile, mock_os_remove, mock_subprocess_run):
    """
    Tests the POST /jobrequest endpoint, mocking the file write and kubectl call.
    """
    # Mock the temporary file
    mock_file = MagicMock()
    mock_file.__enter__.return_value.name = "temp_job.yaml"
    mock_tempfile.return_value = mock_file

    # Mock the subprocess call
    mock_process = MagicMock()
    mock_process.stdout = "jobrequest.task.ktasker.com/test-job-1 created"
    mock_process.returncode = 0  # Simulate a successful command execution
    mock_process.check_returncode.return_value = None # Mock the check for `check=True`
    mock_subprocess_run.return_value = mock_process

    job_request_payload = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "JobRequest",
        "metadata": {"name": "test-job-1"},
        "spec": {"image": "busybox", "command": ["echo", "test"]},
    }

    response = client.post("/jobrequest", json=job_request_payload)

    # Assertions
    assert response.status_code == 200
    assert response.json()["message"] == "JobRequest submitted"
    assert response.json()["job_request"] == job_request_payload

    # Verify that a temporary file was created
    mock_tempfile.assert_called_once()

    # Verify that kubectl apply was called
    mock_subprocess_run.assert_called_once_with(
        ["kubectl", "apply", "-f", "temp_job.yaml"],
        capture_output=True, text=True, check=True
    )

    # Verify that os.remove was called to clean up the temp file
    mock_os_remove.assert_called_once_with("temp_job.yaml")


@patch("subprocess.run")
def test_list_job_requests(mock_subprocess_run):
    """
    Tests the GET /jobrequest endpoint, mocking the kubectl call.
    """
    # Create a mock return value for the subprocess call
    mock_kubectl_output = '{"items": [{"metadata": {"name": "test-job-1"}}]}'
    mock_process = MagicMock()
    mock_process.stdout = mock_kubectl_output 
    mock_process.check_returncode.return_value = None
    mock_subprocess_run.return_value = mock_process

    response = client.get("/jobrequest")

    assert response.status_code == 200
    assert response.text == mock_kubectl_output

    # Verify that kubectl get was called
    mock_subprocess_run.assert_called_once_with(
        ["kubectl", "get", "jobrequests", "-o", "json"], capture_output=True, text=True, check=True
    )

@patch("subprocess.run")
def test_get_job_request(mock_subprocess_run):
    """Tests the GET /jobrequest/{job_name} endpoint."""
    mock_kubectl_output = '{"metadata": {"name": "test-job-1"}}'
    mock_subprocess_run.return_value.check_returncode.return_value = None
    mock_subprocess_run.return_value.stdout = mock_kubectl_output

    response = client.get("/jobrequest/test-job-1")

    assert response.status_code == 200
    assert response.text == mock_kubectl_output
    mock_subprocess_run.assert_called_once_with(
        ["kubectl", "get", "jobrequest", "test-job-1", "-o", "json"],
        capture_output=True, text=True, check=True
    )