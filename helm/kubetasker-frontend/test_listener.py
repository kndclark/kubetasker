from fastapi.testclient import TestClient
from unittest.mock import patch, MagicMock
import pytest
import json
import asyncio

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
        import listener
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
        
        # Patch process_ktasks to prevent background execution during tests.
        # This ensures we can inspect the queue and status deterministically.
        async def mock_worker():
            pass
        
        with patch("listener.process_ktasks", side_effect=mock_worker):
            # Reset state before each test
            listener.submission_status.clear()
            listener.ktask_queue = asyncio.Queue(maxsize=1000)
            
            test_client = TestClient(app)

            # Yield the mock, client, helper function, and the listener module
            yield mock_kubernetes, test_client, _create_api_exception_side_effect, listener

def test_health_check(setup_app_and_mock_k8s_client):
    """
    Tests the /healthz endpoint.
    """
    _, client, _, _ = setup_app_and_mock_k8s_client
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
    ]
)
def test_create_ktask_success(setup_app_and_mock_k8s_client, input_payload, expected_body):
    """Tests successful POST /ktask calls with various valid payloads."""
    mock_k8s, client, _, listener_module = setup_app_and_mock_k8s_client

    response = client.post("/ktask", json=input_payload)

    # Assertions
    assert response.status_code == 202
    assert response.json()["message"] == "Ktask buffered"
    assert response.json()["ktask_name"] == input_payload["metadata"]["name"]

    # Verify buffering logic
    ktask_name = input_payload["metadata"]["name"]
    assert listener_module.submission_status[ktask_name]["phase"] == "Pending"
    assert listener_module.ktask_queue.qsize() == 1

    # Verify that the k8s client was NOT called synchronously (worker is mocked)
    mock_k8s.client.CustomObjectsApi.return_value.create_namespaced_custom_object.assert_not_called()

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
            'Value error, apiVersion must be "task.ktasker.com/v1"',
            id="bad_api_version",
        ),
        pytest.param(
            {"kind": "NotKtask"}, # Incorrect kind
            ("body", "kind"),
            'Value error, kind must be "Ktask"',
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
def test_create_ktask_validation_errors(setup_app_and_mock_k8s_client, payload_override, expected_error_loc, expected_error_msg):
    """
    Tests that POST /ktask returns a 422 on various Pydantic validation failures.
    """
    _, client, _, _ = setup_app_and_mock_k8s_client

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
    assert error_details["loc"] == list(expected_error_loc)
    assert expected_error_msg in error_details["msg"]

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
    mock_k8s_client, client, _, _ = setup_app_and_mock_k8s_client
    mock_k8s_response = {"items": [{"metadata": {"name": f"test-job-in-{expected_namespace}"}}]}
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.return_value = mock_k8s_response

    response = client.get(f"/ktask{query_params}")

    assert response.status_code == 200
    assert response.json() == mock_k8s_response

    # Verify that the k8s client was called correctly.
    mock_k8s_client.client.CustomObjectsApi.return_value.list_namespaced_custom_object.assert_called_once_with(
        group="task.ktasker.com",
        version="v1",
        namespace=expected_namespace,
        plural="ktasks",
    )

@pytest.mark.parametrize(
    "api_status, api_reason, api_body, expected_text",
    [
        pytest.param(
            500, "Internal Server Error", {"message": "the server has a problem"}, "the server has a problem",
            id="internal_error_500"
        ),
        # We could add other error codes here, like 403 Forbidden
        pytest.param(
            403, "Forbidden", {"message": "user cannot list resources"}, "user cannot list resources",
            id="forbidden_403"
        ),
    ]
)
def test_list_ktasks_api_errors(setup_app_and_mock_k8s_client, api_status, api_reason, api_body, expected_text):
    """Tests that GET /ktask handles various API errors from Kubernetes."""
    mock_k8s, client, create_api_exception, _ = setup_app_and_mock_k8s_client
    side_effect = create_api_exception(status=api_status, reason=api_reason, body_dict=api_body)
    mock_k8s.client.CustomObjectsApi.return_value.list_namespaced_custom_object.side_effect = side_effect

    response = client.get("/ktask?namespace=test-ns")
    assert response.status_code == api_status
    assert expected_text in response.text

@pytest.mark.parametrize(
    "url, mock_method_name, mock_response, expected_call_kwargs",
    [
        pytest.param(
            "/ktask?namespace=test-ns",
            "list_namespaced_custom_object",
            {"items": [{"metadata": {"name": "job-in-test-ns"}}]},
            {"namespace": "test-ns"},
            id="list_explicit_namespace"
        ),
        pytest.param(
            "/ktask",
            "list_namespaced_custom_object",
            {"items": [{"metadata": {"name": "job-in-default-ns"}}]},
            {"namespace": "default"},
            id="list_default_namespace"
        ),
        pytest.param(
            "/ktask/my-specific-job?namespace=test-ns",
            "get_namespaced_custom_object",
            {"metadata": {"name": "my-specific-job"}},
            {"name": "my-specific-job", "namespace": "test-ns"},
            id="get_single_job"
        ),
    ]
)
def test_get_and_list_ktasks_success(setup_app_and_mock_k8s_client, url, mock_method_name, mock_response, expected_call_kwargs):
    """Tests successful GET requests for listing and retrieving single Ktasks."""
    mock_k8s, client, _, _ = setup_app_and_mock_k8s_client
    mock_method = getattr(mock_k8s.client.CustomObjectsApi.return_value, mock_method_name)
    mock_method.return_value = mock_response

    response = client.get(url)

    assert response.status_code == 200
    assert response.json() == mock_response

    # Verify the correct Kubernetes client method was called with the right arguments
    base_kwargs = {"group": "task.ktasker.com", "version": "v1", "plural": "ktasks"}
    mock_method.assert_called_once_with(**base_kwargs, **expected_call_kwargs)

@pytest.mark.parametrize(
    "api_status, api_reason, api_body, expected_text",
    [
        pytest.param(
            404, "Not Found", {"message": "not found"}, "not found",
            id="not_found_404"
        ),
        pytest.param(
            503, "Service Unavailable", {"message": "etcd is down"}, "etcd is down",
            id="service_unavailable_503"
        ),
    ]
)
def test_get_ktask_api_errors(setup_app_and_mock_k8s_client, api_status, api_reason, api_body, expected_text):
    """
    Tests that GET /ktask/{job_name} handles various API errors from Kubernetes.
    """
    mock_k8s, client, create_api_exception, _ = setup_app_and_mock_k8s_client

    side_effect = create_api_exception(status=api_status, reason=api_reason, body_dict=api_body)
    mock_k8s.client.CustomObjectsApi.return_value.get_namespaced_custom_object.side_effect = side_effect

    response = client.get("/ktask/some-job?namespace=test-ns")
    assert response.status_code == api_status
    assert expected_text in response.text

@pytest.mark.parametrize(
    "status_phase, status_message, expected_reason",
    [
        ("Pending", "In queue", "AsyncSubmissionStatus"),
        ("Created", "Successfully submitted to Kubernetes", "AsyncSubmissionStatus"),
        ("Failed", "Something went wrong", "AsyncSubmissionStatus"),
    ]
)
def test_get_ktask_async_status(setup_app_and_mock_k8s_client, status_phase, status_message, expected_reason):
    """
    Tests that GET /ktask/{name} returns in-memory status when K8s returns 404.
    """
    mock_k8s, client, create_api_exception, listener_module = setup_app_and_mock_k8s_client
    job_name = "async-job"
    namespace = "default"

    # Mock K8s returning 404
    side_effect = create_api_exception(status=404, reason="Not Found", body_dict={})
    mock_k8s.client.CustomObjectsApi.return_value.get_namespaced_custom_object.side_effect = side_effect

    # Set in-memory status
    listener_module.update_submission_status(job_name, status_phase, status_message)
    
    response = client.get(f"/ktask/{job_name}?namespace={namespace}")
    assert response.status_code == 200
    json_resp = response.json()
    assert json_resp["metadata"]["name"] == job_name
    assert json_resp["status"]["phase"] == status_phase
    assert json_resp["status"]["message"] == status_message
    assert json_resp["status"]["reason"] == expected_reason

@pytest.mark.parametrize(
    "method, url",
    [
        ("GET", "/ktask?namespace=test-ns"),
        ("GET", "/ktask/some-job?namespace=test-ns"),
    ],
    ids=["list_ktasks", "get_ktask"]
)
def test_api_unavailable_when_k8s_client_fails(method, url):
    """
    Tests that endpoints return 503 if the Kubernetes client could not be initialized.
    This test runs without the standard fixture to control dependency overrides manually.
    """
    from listener import app, get_k8s_api

    # Override the dependency to simulate a failure to get the k8s client
    app.dependency_overrides[get_k8s_api] = lambda: None
    
    with TestClient(app) as client:
        if method == "POST":
            # POST requires a valid body, even if the endpoint logic fails early
            valid_payload = {"apiVersion": "task.ktasker.com/v1", "kind": "Ktask", "metadata": {"name": "job"}, "spec": {"image": "img"}}
            response = client.post(url, json=valid_payload)
        else:
            response = client.get(url)

        assert response.status_code == 503
        assert "Service is unavailable" in response.json()["detail"]

    # Clear the override for other tests
    app.dependency_overrides.clear()

@pytest.mark.asyncio
async def test_worker_retries_and_failure(setup_app_and_mock_k8s_client):
    """
    Tests that the worker retries on failure and eventually marks the task as Failed.
    """
    mock_k8s, _, create_api_exception, listener_module = setup_app_and_mock_k8s_client
    
    payload_dict = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "retry-job", "namespace": "default"},
        "spec": {"image": "busybox"}
    }
    ktask = listener_module.KtaskPayload(**payload_dict)
    
    # Configure mock to raise exception
    side_effect = create_api_exception(status=500, reason="Internal Error", body_dict={})
    mock_k8s.client.CustomObjectsApi.return_value.create_namespaced_custom_object.side_effect = side_effect
    
    # Patch asyncio.sleep to speed up the test
    with patch("asyncio.sleep", return_value=None) as mock_sleep:
        await listener_module.process_single_ktask(ktask)
        
        # Verify retries occurred (max_retries=3, so 3 calls)
        assert mock_k8s.client.CustomObjectsApi.return_value.create_namespaced_custom_object.call_count == 3
        assert mock_sleep.call_count == 2
        
        # Verify status is Failed
        status = listener_module.get_submission_status("retry-job")
        assert status["phase"] == "Failed"
        assert "Failed after 3 attempts" in status["message"]

@pytest.mark.asyncio
async def test_worker_success(setup_app_and_mock_k8s_client):
    """
    Tests that the worker handles success correctly.
    """
    mock_k8s, _, _, listener_module = setup_app_and_mock_k8s_client
    
    payload_dict = {
        "apiVersion": "task.ktasker.com/v1",
        "kind": "Ktask",
        "metadata": {"name": "success-job", "namespace": "default"},
        "spec": {"image": "busybox"}
    }
    ktask = listener_module.KtaskPayload(**payload_dict)
    
    # Configure mock to succeed
    mock_k8s.client.CustomObjectsApi.return_value.create_namespaced_custom_object.return_value = payload_dict
    
    await listener_module.process_single_ktask(ktask)
    
    # Verify call
    mock_k8s.client.CustomObjectsApi.return_value.create_namespaced_custom_object.assert_called_once()
    
    # Verify status
    status = listener_module.get_submission_status("success-job")
    assert status["phase"] == "Created"

def _raise_exception(exc):
    """Helper function to raise an exception within a lambda."""
    raise exc