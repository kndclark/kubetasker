from fastapi.testclient import TestClient
from unittest.mock import patch, MagicMock
import pytest
import json

# We need to import the real kubernetes.client.exceptions here
# to create a mock ApiException that behaves like the real one.
# This import must happen outside any patching of `sys.modules['kubernetes']`
# if we want to use the real ApiException type.
from kubernetes.client.exceptions import ApiException as RealApiException
from pydantic import ValidationError
@pytest.fixture(autouse=True)
def setup_app_and_mock_k8s_client():
    """
    Pytest fixture to set up the FastAPI app and mock the Kubernetes client.
    This fixture ensures that `listener.py` is imported within a patched context,
    preventing module-level errors related to Kubernetes config loading.
    `autouse=True` ensures it's used for every test in this module.
    """
    # Create a mock structure that mimics the real kubernetes library's layout
    mock_kubernetes = MagicMock()
    mock_kubernetes.client = MagicMock()
    mock_kubernetes.config = MagicMock()
    # Assign the real ApiException type to the mock's exceptions module
    # We need to mock the body attribute to be a JSON string
    class MockApiException(RealApiException):
        def __init__(self, status=0, reason=None, http_resp=None, body="{}"):
            # In Python 3.12, the body attribute is expected to be bytes.
            if isinstance(body, str):
                body = body.encode('utf-8')
            super().__init__(status, reason, http_resp)
            self.body = body
    mock_kubernetes.client.exceptions.ApiException = MockApiException

    # Patch the config loading functions to do nothing.
    # These patches are active during the import of `listener.py`.
    with patch('os.getenv', return_value='development'), \
         patch('kubernetes.config.load_incluster_config'), \
         patch('kubernetes.config.load_kube_config'), \
         patch.dict('sys.modules', {
             'kubernetes': mock_kubernetes,
             'kubernetes.client': mock_kubernetes.client,
             'kubernetes.config': mock_kubernetes.config,
             'kubernetes.client.exceptions': mock_kubernetes.client.exceptions,
         }):
        # Import app and the dependency function within the patched context
        from listener import app, get_k8s_api

        def _create_api_exception_side_effect(status, reason, body_dict):
            """
            Factory to create a side_effect function that raises an ApiException.
            This simplifies mocking API errors in tests.
            """
            exception_body = json.dumps(body_dict)
            mock_resp = MagicMock()
            mock_resp.status = status
            mock_resp.reason = reason
            
            # The lambda ensures a fresh exception is raised on each call.
            return lambda *args, **kwargs: _raise_exception(
                mock_kubernetes.client.exceptions.ApiException(
                    status=status, body=exception_body, http_resp=mock_resp
                )
            )

        # Override the dependency with our mock client
        app.dependency_overrides[get_k8s_api] = lambda: mock_kubernetes.client.CustomObjectsApi()
        
        test_client = TestClient(app)

        # Yield the mock, client, and the helper function
        yield mock_kubernetes, test_client, _create_api_exception_side_effect

def test_health_check(setup_app_and_mock_k8s_client):
    """
    Tests the /healthz endpoint.
    """
    _, client, _ = setup_app_and_mock_k8s_client
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}

def test_create_ktask(setup_app_and_mock_k8s_client):
    """
    Tests the POST /ktask endpoint, mocking the Kubernetes client call.
    """
    mock_k8s_client, client, _ = setup_app_and_mock_k8s_client
    ktask_payload = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "test-job-1", "namespace": "test-ns"},
        "spec": {"image": "busybox", "command": ["echo", "test"], 
                 'restartPolicy': 'OnFailure'},
    }
    # Mock the API response from the Kubernetes client
    mock_k8s_client.client.CustomObjectsApi.return_value.create_namespaced_custom_object.return_value = ktask_payload.copy()

    response = client.post("/ktask", json=ktask_payload)

    # Assertions
    assert response.status_code == 200
    assert response.json()["message"] == "Ktask submitted"
    assert response.json()["ktask"] == ktask_payload # The mock returns the payload

    # Verify that the k8s client was called correctly
    mock_k8s_client.client.CustomObjectsApi.return_value.create_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        namespace="test-ns",
        plural="ktasks",
        body=ktask_payload,
    )

def test_create_ktask_validation_error(setup_app_and_mock_k8s_client):
    """
    Tests that the POST /ktask endpoint returns a 422 on validation failure.
    """
    _, client, _ = setup_app_and_mock_k8s_client

    # Payload missing the required 'image' field in spec
    invalid_payload = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "test-job-invalid"},
        "spec": {"command": ["echo", "test"]},
    }

    response = client.post("/ktask", json=invalid_payload)

    assert response.status_code == 422
    response_json = response.json()
    assert "detail" in response_json
    assert response_json["detail"][0]["msg"] == "Field required"
    assert response_json["detail"][0]["loc"] == ["body", "spec", "image"]

def test_create_ktask_bad_api_version(setup_app_and_mock_k8s_client):
    """
    Tests that the custom apiVersion validator rejects incorrect versions.
    """
    _, client, _ = setup_app_and_mock_k8s_client

    invalid_payload = {
        "apiVersion": "task.ktasker.com/v2", # Incorrect version
        "kind": "Ktask",
        "metadata": {"name": "test-job-invalid-api"},
        "spec": {"image": "busybox"},
    }

    response = client.post("/ktask", json=invalid_payload)

    assert response.status_code == 422
    assert 'apiVersion must be' in response.text and 'task.ktasker.com/v1' in response.text

def test_create_ktask_conflict(setup_app_and_mock_k8s_client):
    """
    Tests that POST /ktask returns a 409 on conflict (resource already exists).
    """
    mock_k8s_client, client, create_api_exception = setup_app_and_mock_k8s_client
    ktask_payload = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "test-job-conflict", "namespace": "test-ns"},
        "spec": {"image": "busybox", "command": ["echo", "test"], 'restartPolicy': 'OnFailure'},
    }
    
    # Use the helper to configure the mock exception
    side_effect = create_api_exception(
        status=409, reason="Conflict", 
        body_dict={"message": "ktasks.task.ktasker.com \"test-job-conflict\" already exists"}
    )
    mock_k8s_client.client.CustomObjectsApi.return_value.create_namespaced_custom_object.side_effect = side_effect

    response = client.post("/ktask", json=ktask_payload)
    assert response.status_code == 409
    assert "already exists" in response.text

def test_list_ktasks(setup_app_and_mock_k8s_client):
    """
    Tests the GET /ktask endpoint, mocking the Kubernetes client call.
    """
    mock_k8s_client, client, _ = setup_app_and_mock_k8s_client
    mock_k8s_response = {"items": [{"metadata": {"name": "test-job-1"}}]} #.copy()
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.return_value = mock_k8s_response

    response = client.get("/ktask?namespace=test-ns")

    assert response.status_code == 200
    assert response.json() == mock_k8s_response

    # Verify that the k8s client was called correctly.
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        namespace="test-ns",
        plural="ktasks",
    )

def test_list_ktasks_default_namespace(setup_app_and_mock_k8s_client):
    """
    Tests that GET /ktask uses the 'default' namespace if none is provided.
    """
    mock_k8s_client, client, _ = setup_app_and_mock_k8s_client
    mock_k8s_response = {"items": [{"metadata": {"name": "test-job-default-ns"}}]}
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.return_value = mock_k8s_response

    # Note: No '?namespace=' query parameter is sent
    response = client.get("/ktask")

    assert response.status_code == 200
    assert response.json() == mock_k8s_response

    # Verify that the k8s client was called with the 'default' namespace
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        namespace="default",
        plural="ktasks",
    )

def test_list_ktasks_api_error(setup_app_and_mock_k8s_client):
    """
    Tests that GET /ktask handles generic API errors from Kubernetes.
    """
    mock_k8s_client, client, create_api_exception = setup_app_and_mock_k8s_client

    side_effect = create_api_exception(
        status=500, reason="Internal Server Error", 
        body_dict={"message": "the server has a problem"}
    )
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.side_effect = side_effect

    response = client.get("/ktask?namespace=test-ns")
    assert response.status_code == 500
    assert "the server has a problem" in response.text

def test_get_ktask(setup_app_and_mock_k8s_client):
    """Tests the GET /ktask/{job_name} endpoint."""
    mock_k8s_client, client, _ = setup_app_and_mock_k8s_client
    mock_k8s_response = {"metadata": {"name": "test-job-1"}} #.copy()
    mock_k8s_client.client.CustomObjectsApi.return_value.get_namespaced_custom_object.return_value = mock_k8s_response.copy()

    response = client.get("/ktask/test-job-1?namespace=test-ns")

    assert response.status_code == 200
    assert response.json() == mock_k8s_response

    # Verify that the k8s client was called correctly.
    mock_k8s_client.client.CustomObjectsApi.return_value.get_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        name="test-job-1",
        namespace="test-ns",
        plural="ktasks",
    )

def test_get_ktask_not_found(setup_app_and_mock_k8s_client):
    """Tests the GET /ktask/{job_name} endpoint when the resource is not found."""
    mock_k8s_client, client, create_api_exception = setup_app_and_mock_k8s_client

    side_effect = create_api_exception(
        status=404, 
        reason="Not Found", 
        body_dict={"message": "not found"}
    )
    mock_k8s_client.client.CustomObjectsApi.return_value.get_namespaced_custom_object.side_effect = side_effect

    response = client.get("/ktask/not-found-job?namespace=test-ns")

    assert response.status_code == 404
    assert "not found" in response.json()["detail"]

def test_get_ktask_api_error(setup_app_and_mock_k8s_client):
    """
    Tests that GET /ktask/{job_name} handles generic API errors.
    """
    mock_k8s_client, client, create_api_exception = setup_app_and_mock_k8s_client

    side_effect = create_api_exception(
        status=503, 
        reason="Service Unavailable", 
        body_dict={"message": "etcd is down"}
    )
    mock_k8s_client.client.CustomObjectsApi.return_value.get_namespaced_custom_object.side_effect = side_effect

    response = client.get("/ktask/some-job?namespace=test-ns")
    assert response.status_code == 503
    assert "etcd is down" in response.text

def test_api_unavailable_when_k8s_client_fails(setup_app_and_mock_k8s_client):
    """
    Tests that endpoints return 503 if the Kubernetes client could not be initialized.
    """
    from listener import app, get_k8s_api

    # Override the dependency to simulate a failure to get the k8s client
    app.dependency_overrides[get_k8s_api] = lambda: None
    client = TestClient(app)

    # Test one of the endpoints
    response = client.get("/ktask?namespace=test-ns")

    assert response.status_code == 503
    assert "Service is unavailable" in response.json()["detail"]

    # Clear the override for other tests
    app.dependency_overrides.clear()

def _raise_exception(exc):
    """Helper function to raise an exception within a lambda."""
    raise exc