from fastapi.testclient import TestClient
from unittest.mock import patch, MagicMock, AsyncMock
import pytest
import json
import asyncio
import httpx

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

    # Patch the config loading functions to do nothing.
    # These patches are active during the import of `listener.py`.
    with patch('os.getenv', return_value='development'), \
         patch('kubernetes.config.load_incluster_config'), \
         patch('kubernetes.config.load_kube_config'), \
         patch.dict('sys.modules', {
             'kubernetes': mock_kubernetes,
             'kubernetes.client': mock_kubernetes.client,
             'kubernetes.config': mock_kubernetes.config,
         }):
        # Import app and the dependency function within the patched context
        import listener
        from listener import app

        # Mock httpx.AsyncClient
        with patch("httpx.AsyncClient") as mock_httpx:
            mock_client_instance = AsyncMock()
            mock_httpx.return_value = mock_client_instance
            mock_client_instance.__aenter__.return_value = mock_client_instance
            mock_client_instance.__aexit__.return_value = None

            # Patch metrics.start_collector to prevent the metrics thread from starting.
            with patch("listener.metrics.start_collector"):
                test_client = TestClient(app)

                # Yield the mock client and test client
                yield mock_client_instance, test_client, listener

def test_health_check(setup_app_and_mock_k8s_client):
    """
    Tests the /healthz endpoint.
    """
    _, client, _ = setup_app_and_mock_k8s_client
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}
    
@pytest.mark.parametrize(
    "input_payload, expected_body",
    [
        pytest.param(
            { # Minimal payload
                "apiVersion": "task.ktasker.com/v1", "kind": "Ktask",
                "metadata": {"name": "test-minimal"},
                "spec": {"image": "busybox"},
            },
            { # Expected body sent to Kubernetes, with defaults applied
                "apiVersion": "task.ktasker.com/v1", "kind": "Ktask",
                "metadata": {"name": "test-minimal", "namespace": "default"},
                "spec": {"image": "busybox", "restartPolicy": "OnFailure"},
            },
            id="minimal_payload_with_defaults"
        ),
        pytest.param(
            { # Full payload with optional fields
                "apiVersion": "task.ktasker.com/v1", "kind": "Ktask",
                "metadata": {"name": "test-full", "namespace": "custom-ns"},
                "spec": {"image": "nginx", "command": ["nginx"], "restartPolicy": "Never", "env": [{"name": "VAR", "value": "VAL"}]},
            },
            { # Expected body is identical to input
                "apiVersion": "task.ktasker.com/v1", "kind": "Ktask",
                "metadata": {"name": "test-full", "namespace": "custom-ns"},
                "spec": {"image": "nginx", "command": ["nginx"], "restartPolicy": "Never", "env": [{"name": "VAR", "value": "VAL"}]},
            },
            id="full_payload_with_env"
        ),
    pytest.param(
        { # Payload with resources
            "apiVersion": "task.ktasker.com/v1", "kind": "Ktask",
            "metadata": {"name": "test-resources", "namespace": "default"},
            "spec": {
                "image": "busybox",
                "resources": {
                    "requests": {"cpu": "100m", "memory": "128Mi"},
                    "limits": {"cpu": "200m", "memory": "256Mi"}
                }
            },
        },
        { # Expected body
            "apiVersion": "task.ktasker.com/v1", "kind": "Ktask",
            "metadata": {"name": "test-resources", "namespace": "default"},
            "spec": {
                "image": "busybox",
                "restartPolicy": "OnFailure",
                "resources": {
                    "requests": {"cpu": "100m", "memory": "128Mi"},
                    "limits": {"cpu": "200m", "memory": "256Mi"}
                }
            },
        },
        id="payload_with_resources"
    ),
    ]
)
def test_create_ktask_success(setup_app_and_mock_k8s_client, input_payload, expected_body):
    """Tests successful POST /ktask calls with various valid payloads."""
    mock_httpx, client, listener_module = setup_app_and_mock_k8s_client

    # Setup mock response from controller
    mock_httpx.post.return_value = httpx.Response(201, json=expected_body)

    response = client.post("/ktask", json=input_payload)

    # Assertions
    assert response.status_code == 201
    assert response.json() == expected_body

    # Verify proxy call
    mock_httpx.post.assert_called_once()
    call_args = mock_httpx.post.call_args
    assert call_args[0][0].endswith("/ktask")
    assert json.loads(call_args[1]['content']) == input_payload

@pytest.mark.parametrize(
    "query_params, expected_namespace",
    [
        pytest.param("?namespace=test-ns", "test-ns", id="explicit_namespace"),
        pytest.param("", "default", id="default_namespace"),
    ],
)
def test_list_ktasks(setup_app_and_mock_k8s_client, query_params, expected_namespace):
    """
    Tests GET /ktask with and without an explicit namespace parameter.
    """
    mock_httpx, client, _ = setup_app_and_mock_k8s_client
    mock_response = {"items": [{"metadata": {"name": f"test-job-in-{expected_namespace}"}}]}
    mock_httpx.get.return_value = httpx.Response(200, json=mock_response)

    response = client.get(f"/ktask{query_params}")

    assert response.status_code == 200
    assert response.json() == mock_response

    # Verify proxy call
    mock_httpx.get.assert_called_once()
    call_args = mock_httpx.get.call_args
    assert call_args[0][0].endswith("/ktask")
    assert call_args[1]['params'] == {"namespace": expected_namespace}

def test_get_gui(setup_app_and_mock_k8s_client):
    """Tests that the GUI endpoint returns HTML."""
    _, client, _ = setup_app_and_mock_k8s_client
    
    # Mock opening the index.html file
    mock_html = "<html><head><title>KubeTasker Dashboard</title></head><body></body></html>"
    with patch("builtins.open", new_callable=MagicMock) as mock_file:
        # Configure the mock to return a file object whose read() returns our HTML
        mock_file.return_value.__enter__.return_value.read.return_value = mock_html
        
        response = client.get("/")
        
        assert response.status_code == 200
        assert "text/html" in response.headers["content-type"]
        assert "<title>KubeTasker Dashboard</title>" in response.text

def test_delete_ktask(setup_app_and_mock_k8s_client):
    """Tests deletion of a Ktask."""
    mock_httpx, client, _ = setup_app_and_mock_k8s_client
    job_name = "test-job"
    namespace = "default"

    mock_httpx.delete.return_value = httpx.Response(204)

    response = client.delete(f"/ktask/{job_name}?namespace={namespace}")

    assert response.status_code == 204

    # Verify proxy call
    mock_httpx.delete.assert_called_once()
    call_args = mock_httpx.delete.call_args
    assert call_args[0][0].endswith(f"/ktask/{job_name}")
    assert call_args[1]['params'] == {"namespace": namespace}

def test_get_single_ktask_not_implemented(setup_app_and_mock_k8s_client):
    """Tests that GET single Ktask returns 501."""
    _, client, _ = setup_app_and_mock_k8s_client
    response = client.get("/ktask/some-job")
    assert response.status_code == 501

@pytest.mark.parametrize(
    "status_code, response_body",
    [
        (400, {"detail": "Bad Request"}),
        (403, {"detail": "Forbidden"}),
        (404, {"detail": "Not Found"}),
        (422, {"detail": "Validation Error"}),
        (500, {"detail": "Internal Server Error"}),
    ],
)
def test_proxy_upstream_errors(setup_app_and_mock_k8s_client, status_code, response_body):
    """
    Tests that the frontend correctly proxies various error status codes 
    and bodies from the controller for different methods.
    """
    mock_httpx, client, _ = setup_app_and_mock_k8s_client
    
    # Configure mock to return the specific error for all methods
    mock_httpx.post.return_value = httpx.Response(status_code, json=response_body)
    mock_httpx.get.return_value = httpx.Response(status_code, json=response_body)
    mock_httpx.delete.return_value = httpx.Response(status_code, json=response_body)

    # Test POST /ktask
    resp = client.post("/ktask", json={"metadata": {"name": "test"}})
    assert resp.status_code == status_code
    assert resp.json() == response_body

    # Test GET /ktask
    resp = client.get("/ktask")
    assert resp.status_code == status_code
    assert resp.json() == response_body

    # Test DELETE /ktask/{name}
    resp = client.delete("/ktask/job")
    assert resp.status_code == status_code
    assert resp.json() == response_body

@pytest.mark.parametrize(
    "method, endpoint, kwargs",
    [
        ("post", "/ktask", {"json": {"metadata": {"name": "t"}}}),
        ("get", "/ktask", {}),
        ("delete", "/ktask/t", {}),
    ]
)
def test_controller_connection_failure(setup_app_and_mock_k8s_client, method, endpoint, kwargs):
    """Tests that the frontend returns 503 when the controller is unreachable."""
    mock_httpx, client, _ = setup_app_and_mock_k8s_client
    
    # Simulate a connection error (RequestError)
    getattr(mock_httpx, method).side_effect = httpx.RequestError("Connection refused", request=MagicMock())

    response = getattr(client, method)(endpoint, **kwargs)
    
    assert response.status_code == 503
    assert response.json() == {"detail": "Controller unavailable"}