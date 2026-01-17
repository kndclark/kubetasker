from fastapi.testclient import TestClient
from unittest.mock import patch, MagicMock, AsyncMock
import pytest
import json
import asyncio
import httpx
import os
import metrics

@pytest.fixture(autouse=True)
def setup_app_and_mock_client():
    """
    Pytest fixture to set up the FastAPI app and mock dependencies.
    """
    with patch.dict(os.environ, {'KUBETASKER_ENV': 'development', 'CONTROLLER_URL': 'http://localhost:8090'}):
        import listener
        from listener import app

        # Mock httpx.AsyncClient
        with patch("httpx.AsyncClient") as mock_httpx:
            mock_client_instance = AsyncMock()
            mock_httpx.return_value = mock_client_instance
            mock_client_instance.__aenter__.return_value = mock_client_instance
            mock_client_instance.__aexit__.return_value = None

            # Patch metrics.start_collector
            with patch("listener.metrics.start_collector"):
                # Patch process_ktasks to prevent background execution during tests
                async def mock_worker():
                    pass
                
                with patch("listener.process_ktasks", side_effect=mock_worker):
                    # Reset state
                    listener.submission_status.clear()
                    listener.ktask_queue = asyncio.Queue(maxsize=1000)
                    
                    with TestClient(app) as test_client:
                        yield mock_client_instance, test_client, listener

def test_health_check(setup_app_and_mock_client):
    _, client, _ = setup_app_and_mock_client
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}

def test_create_ktask_success(setup_app_and_mock_client):
    """Tests that POST /ktask buffers the request and returns 202."""
    mock_httpx, client, listener_module = setup_app_and_mock_client
    
    payload = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "test-job", "namespace": "default"},
        "spec": {"image": "busybox"}
    }

    response = client.post("/ktask", json=payload)

    # Assertions
    assert response.status_code == 202
    assert response.json()["message"] == "Ktask buffered"
    assert response.json()["ktask_name"] == "test-job"

    # Verify buffering logic
    assert listener_module.submission_status["test-job"]["phase"] == "Pending"
    assert listener_module.ktask_queue.qsize() == 1

    # Verify NO synchronous call to controller
    mock_httpx.post.assert_not_called()

@pytest.mark.parametrize(
    "payload_override, expected_error_loc, expected_error_msg",
    [
        pytest.param(
            {"spec": {"command": ["echo", "test"]}}, # Missing 'image'
            ("body", "spec", "image"),
            "Field required",
            id="missing_image",
        ),
        pytest.param(
            {"apiVersion": "task.ktasker.com/v2"}, # Incorrect version
            ("body", "apiVersion"),
            'apiVersion must be "task.ktasker.com/v1"',
            id="bad_api_version",
        ),
        pytest.param(
            {"kind": "NotKtask"}, # Incorrect kind
            ("body", "kind"),
            'kind must be "Ktask"',
            id="bad_kind",
        ),
        pytest.param(
            {"spec": {"image": "busybox", "command": "not-a-list"}}, # Invalid type
            ("body", "spec", "command"),
            "Input should be a valid list",
            id="bad_command_type",
        ),
    ],
)
def test_create_ktask_validation_errors(setup_app_and_mock_client, payload_override, expected_error_loc, expected_error_msg):
    """
    Tests that POST /ktask returns a 422 on various Pydantic validation failures.
    """
    _, client, _, = setup_app_and_mock_client

    base_payload = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "test-job-invalid", "namespace": "default"},
        "spec": {"image": "busybox"},
    }
    # The override payload replaces keys in the base payload.
    invalid_payload = {**base_payload, **payload_override}

    response = client.post("/ktask", json=invalid_payload)

    assert response.status_code == 422
    response_json = response.json()
    assert "detail" in response_json
    error_details = response_json["detail"][0]
    # Check if the location matches (handling the fact Pydantic v2 might differ slightly in loc structure)
    assert error_details["loc"][-1] == expected_error_loc[-1]
    assert expected_error_msg in error_details["msg"]

@pytest.mark.parametrize(
    "status_phase, status_message",
    [
        ("Pending", "Request buffered in queue"),
        ("Failed", "Something went wrong"),
    ]
)
def test_get_ktask_local_status(setup_app_and_mock_client, status_phase, status_message):
    """Tests that Pending/Failed statuses are retrieved from local buffer."""
    mock_httpx, client, listener_module = setup_app_and_mock_client
    
    # Manually add to status
    listener_module.update_submission_status("test-job", status_phase, status_message)

    response = client.get("/ktask/test-job?namespace=default")
    assert response.status_code == 200
    data = response.json()
    assert data["metadata"]["name"] == "test-job"
    assert data["status"]["phase"] == status_phase
    assert data["status"]["message"] == status_message
    assert data["status"]["reason"] == "AsyncSubmissionStatus"

    # Verify we didn't call upstream
    mock_httpx.get.assert_not_called()

def test_get_ktask_created_fallthrough(setup_app_and_mock_client):
    """Tests that 'Created' status in local buffer falls through to upstream controller."""
    mock_httpx, client, listener_module = setup_app_and_mock_client
    
    # Manually add to status as Created
    listener_module.update_submission_status("fallthrough-job", "Created", "Submitted")

    # Mock upstream response
    mock_httpx.get.return_value = httpx.Response(200, json={
        "kind": "Ktask", 
        "metadata": {"name": "fallthrough-job"},
        "status": {"phase": "Succeeded"} 
    })

    response = client.get("/ktask/fallthrough-job?namespace=default")
    assert response.status_code == 200
    assert response.json()["status"]["phase"] == "Succeeded"

    # Verify we DID call upstream
    mock_httpx.get.assert_called_once()

def test_get_ktask_proxy_upstream(setup_app_and_mock_client):
    """Tests proxying GET if not in buffer."""
    mock_httpx, client, _ = setup_app_and_mock_client
    
    # Mock upstream response
    mock_httpx.get.return_value = httpx.Response(200, json={"kind": "Ktask", "metadata": {"name": "upstream-job"}})
    
    response = client.get("/ktask/upstream-job?namespace=default")
    assert response.status_code == 200
    assert response.json()["metadata"]["name"] == "upstream-job"
    
    mock_httpx.get.assert_called_once()

@pytest.mark.parametrize(
    "api_status, api_body",
    [
        pytest.param(500, {"message": "the server has a problem"}, id="internal_error_500"),
        pytest.param(403, {"message": "user cannot list resources"}, id="forbidden_403"),
        pytest.param(404, {"message": "not found"}, id="not_found_404"),
    ]
)
def test_proxy_api_errors(setup_app_and_mock_client, api_status, api_body):
    """Tests that GET /ktask proxies various error codes from the controller."""
    mock_httpx, client, _ = setup_app_and_mock_client
    
    mock_httpx.get.return_value = httpx.Response(api_status, json=api_body)

    response = client.get("/ktask?namespace=test-ns")
    assert response.status_code == api_status
    assert response.json() == api_body

@pytest.mark.parametrize(
    "method, endpoint, kwargs",
    [
        ("get", "/ktask", {}),
        ("delete", "/ktask/t", {}),
    ]
)
def test_controller_connection_failure(setup_app_and_mock_client, method, endpoint, kwargs):
    """Tests that the frontend returns 503 when the controller is unreachable (RequestError)."""
    mock_httpx, client, _ = setup_app_and_mock_client
    
    # Simulate a connection error (RequestError)
    getattr(mock_httpx, method).side_effect = httpx.RequestError("Connection refused", request=MagicMock())

    response = getattr(client, method)(endpoint, **kwargs)
    
    assert response.status_code == 503
    assert response.json() == {"detail": "Controller unavailable"}

@pytest.mark.asyncio
async def test_worker_processing_success(setup_app_and_mock_client):
    """Tests the worker logic sending request to controller."""
    mock_httpx, _, listener_module = setup_app_and_mock_client
    
    payload_dict = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "worker-job", "namespace": "default"},
        "spec": {"image": "busybox"}
    }
    ktask_model = listener_module.KtaskPayload(**payload_dict)
    
    # Mock success response from controller
    mock_httpx.post.return_value = httpx.Response(201, json=payload_dict)

    await listener_module.process_single_ktask(ktask_model)

    # Check update status
    status = listener_module.get_submission_status("worker-job")
    assert status["phase"] == "Created"
    
    # Check Controller call
    mock_httpx.post.assert_called_once()
    assert mock_httpx.post.call_args[0][0].endswith("/ktask")
    assert mock_httpx.post.call_args[1]["json"]["metadata"]["name"] == "worker-job"

@pytest.mark.asyncio
@pytest.mark.parametrize(
    "api_status, side_effect, expected_call_count, expected_phase, expected_message_part",
    [
        pytest.param(500, None, 3, "Failed", "Failed after 3 attempts", id="retry_failure_500"),
        pytest.param(422, None, 3, "Failed", "Failed after 3 attempts", id="retry_failure_422"),
        pytest.param(409, None, 1, "Failed", "Ktask already exists", id="conflict_no_retry_409"),
        pytest.param(None, httpx.RequestError("Connection refused", request=MagicMock()), 3, "Failed", "Failed after 3 attempts", id="connection_error_retry"),
    ]
)
async def test_worker_processing_logic_errors(
    setup_app_and_mock_client,
    api_status,
    side_effect,
    expected_call_count,
    expected_phase,
    expected_message_part
):
    """Tests worker retries and error handling."""
    mock_httpx, _, listener_module = setup_app_and_mock_client
    
    payload_dict = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "retry-job", "namespace": "default"},
        "spec": {"image": "busybox"}
    }
    ktask_model = listener_module.KtaskPayload(**payload_dict)
    
    # Configure mock behavior
    if side_effect:
        mock_httpx.post.side_effect = side_effect
    else:
        mock_httpx.post.return_value = httpx.Response(api_status, text="Error")

    # Patch sleep to be instant
    with patch("asyncio.sleep", return_value=None):
        await listener_module.process_single_ktask(ktask_model)

    # Verify calls
    assert mock_httpx.post.call_count == expected_call_count
    
    # Status check
    status = listener_module.get_submission_status("retry-job")
    assert status["phase"] == expected_phase
    assert expected_message_part in status["message"]
