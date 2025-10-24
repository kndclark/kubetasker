from fastapi.testclient import TestClient
from unittest.mock import patch, MagicMock
import pytest

# We need to import the real kubernetes.client.exceptions here
# to create a mock ApiException that behaves like the real one.
# This import must happen outside any patching of `sys.modules['kubernetes']`
# if we want to use the real ApiException type.
from kubernetes.client.exceptions import ApiException as RealApiException

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
    mock_kubernetes.client.exceptions.ApiException = RealApiException

    # Patch the config loading functions to do nothing.
    # These patches are active during the import of `listener.py`.
    with patch('kubernetes.config.load_incluster_config'), \
         patch('kubernetes.config.load_kube_config'):
        # Patch sys.modules to ensure listener.py gets our mock for the client API
        with patch.dict('sys.modules', {
            'kubernetes': mock_kubernetes,
            'kubernetes.client': mock_kubernetes.client,
            'kubernetes.config': mock_kubernetes.config,
            'kubernetes.client.exceptions': mock_kubernetes.client.exceptions,
        }):
            # Import app *within* the patched context
            from listener import app
            # Initialize client *within* the patched context
            test_client = TestClient(app)

            # Yield the mock_kubernetes object and the test_client
            yield mock_kubernetes, test_client

def test_health_check(setup_app_and_mock_k8s_client):
    """
    Tests the /healthz endpoint.
    """
    _, client = setup_app_and_mock_k8s_client
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}

def test_create_job_request(setup_app_and_mock_k8s_client):
    mock_k8s_client, client = setup_app_and_mock_k8s_client
    """
    Tests the POST /jobrequest endpoint, mocking the Kubernetes client call.
    """
    job_request_payload = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "JobRequest",
        "metadata": {"name": "test-job-1", "namespace": "test-ns"},
        "spec": {"image": "busybox", "command": ["echo", "test"], 
                 'env': None, 'restartPolicy': 'OnFailure'},
    }
    # Mock the API response from the Kubernetes client
    mock_k8s_client.client.CustomObjectsApi.return_value.create_namespaced_custom_object.return_value = job_request_payload.copy()

    response = client.post("/jobrequest", json=job_request_payload)

    # Assertions
    assert response.status_code == 200
    assert response.json()["message"] == "JobRequest submitted"
    assert response.json()["job_request"] == job_request_payload # The mock returns the payload

    # Verify that the k8s client was called correctly
    mock_k8s_client.client.CustomObjectsApi.return_value.create_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        namespace="test-ns",
        plural="jobrequests",
        body=job_request_payload,
    )


def test_list_job_requests(setup_app_and_mock_k8s_client):
    mock_k8s_client, client = setup_app_and_mock_k8s_client
    """
    Tests the GET /jobrequest endpoint, mocking the Kubernetes client call.
    """
    mock_k8s_response = {"items": [{"metadata": {"name": "test-job-1"}}]} #.copy()
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.return_value = mock_k8s_response

    response = client.get("/jobrequest?namespace=test-ns")

    assert response.status_code == 200
    assert response.json() == mock_k8s_response

    # Verify that the k8s client was called correctly.
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        namespace="test-ns",
        plural="jobrequests",
    )


def test_get_job_request(setup_app_and_mock_k8s_client):
    mock_k8s_client, client = setup_app_and_mock_k8s_client
    """Tests the GET /jobrequest/{job_name} endpoint."""
    mock_k8s_response = {"metadata": {"name": "test-job-1"}} #.copy()
    mock_k8s_client.client.CustomObjectsApi.return_value.get_namespaced_custom_object.return_value = mock_k8s_response.copy()

    response = client.get("/jobrequest/test-job-1?namespace=test-ns")

    assert response.status_code == 200
    assert response.json() == mock_k8s_response

    # Verify that the k8s client was called correctly.
    mock_k8s_client.client.CustomObjectsApi.return_value.get_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        name="test-job-1",
        namespace="test-ns",
        plural="jobrequests",
    )

def test_get_job_request_not_found(setup_app_and_mock_k8s_client):
    mock_k8s_client, client = setup_app_and_mock_k8s_client
    """Tests the GET /jobrequest/{job_name} endpoint when the resource is not found."""
    # Configure the mock to raise an ApiException when called
    # Use the real ApiException type for the side_effect
    mock_k8s_client.client.CustomObjectsApi.return_value.get_namespaced_custom_object.side_effect = RealApiException(status=404)

    response = client.get("/jobrequest/not-found-job?namespace=test-ns")

    assert response.status_code == 404
    assert "not found" in response.json()["detail"]