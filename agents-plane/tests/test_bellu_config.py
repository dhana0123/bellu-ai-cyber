"""Tests for the Bellu control-plane config and startup health check.

Covers config defaults/env loading (Requirement 1.3: the plane reaches the
control plane only over authenticated HTTP, and sends no org) and the
health-check logic with the HTTP call mocked via ``httpx.MockTransport``.
"""

from __future__ import annotations

import httpx
import pytest

from strix.config import bellu


@pytest.fixture(autouse=True)
def _reset_bellu_cache(monkeypatch: pytest.MonkeyPatch) -> None:
    """Each test starts from a clean env + memoized settings cache."""
    for name in (
        "BELLU_API_BASE",
        "BELLU_SERVICE_ACCOUNT_TOKEN",
        "BELLU_HEALTH_TIMEOUT",
    ):
        monkeypatch.delenv(name, raising=False)
    bellu.reset_cache()
    yield
    bellu.reset_cache()


def _mock_client(handler: object) -> httpx.Client:
    return httpx.Client(transport=httpx.MockTransport(handler))  # type: ignore[arg-type]


# --- config defaults + env loading ------------------------------------------


def test_defaults_when_env_unset() -> None:
    cfg = bellu.load_bellu_settings()
    assert cfg.api_base == bellu.DEFAULT_API_BASE
    assert cfg.service_account_token is None
    assert cfg.health_timeout == bellu.DEFAULT_HEALTH_TIMEOUT


def test_env_overrides(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("BELLU_API_BASE", "https://bellu.example.com/")
    monkeypatch.setenv("BELLU_SERVICE_ACCOUNT_TOKEN", "sa-token-123")
    monkeypatch.setenv("BELLU_HEALTH_TIMEOUT", "3.5")
    bellu.reset_cache()

    cfg = bellu.load_bellu_settings()
    # Trailing slash stripped by the `base` accessor so path joins are clean.
    assert cfg.base == "https://bellu.example.com"
    assert cfg.service_account_token == "sa-token-123"
    assert cfg.health_timeout == 3.5


def test_token_absent_from_repr(monkeypatch: pytest.MonkeyPatch) -> None:
    """The secret must not leak into logs via repr()."""
    monkeypatch.setenv("BELLU_SERVICE_ACCOUNT_TOKEN", "super-secret")
    bellu.reset_cache()
    assert "super-secret" not in repr(bellu.load_bellu_settings())


def test_auth_header_requires_token() -> None:
    cfg = bellu.load_bellu_settings()
    with pytest.raises(RuntimeError, match="BELLU_SERVICE_ACCOUNT_TOKEN"):
        cfg.auth_header()


def test_auth_header_bearer(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("BELLU_SERVICE_ACCOUNT_TOKEN", "abc")
    bellu.reset_cache()
    assert bellu.load_bellu_settings().auth_header() == {"Authorization": "Bearer abc"}


# --- model route selection ---------------------------------------------------


def test_selected_model_reads_strix_llm(monkeypatch: pytest.MonkeyPatch) -> None:
    """The plane reuses Strix's existing model route (STRIX_LLM), not a new one."""
    from strix.config import loader

    monkeypatch.setenv("STRIX_LLM", "openai/gpt-5")
    monkeypatch.setattr(loader, "_cached", None)
    assert bellu.selected_model() == "openai/gpt-5"


# --- health check ------------------------------------------------------------


def test_health_check_healthy(monkeypatch: pytest.MonkeyPatch) -> None:
    from strix.config import loader

    monkeypatch.setenv("STRIX_LLM", "openai/gpt-5")
    monkeypatch.setattr(loader, "_cached", None)
    monkeypatch.setenv("BELLU_API_BASE", "https://cp.example.com")
    bellu.reset_cache()

    seen: dict[str, str] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["url"] = str(request.url)
        return httpx.Response(200, json={"status": "ok", "graph": True})

    result = bellu.health_check(client=_mock_client(handler))

    assert result.healthy is True
    assert bool(result) is True
    assert result.control_plane_ok is True
    assert result.model_configured is True
    assert result.model == "openai/gpt-5"
    assert result.graph_online is True
    # Hits Bellu's public health endpoint; carries no org query param.
    assert seen["url"] == "https://cp.example.com/api/health"
    assert "org" not in seen["url"]


def test_health_check_unreachable_control_plane(monkeypatch: pytest.MonkeyPatch) -> None:
    from strix.config import loader

    monkeypatch.setenv("STRIX_LLM", "openai/gpt-5")
    monkeypatch.setattr(loader, "_cached", None)
    bellu.reset_cache()

    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    result = bellu.health_check(client=_mock_client(handler))

    assert result.healthy is False
    assert result.control_plane_ok is False
    assert result.model_configured is True
    assert "unreachable" in result.detail


def test_health_check_no_model_route(monkeypatch: pytest.MonkeyPatch) -> None:
    from strix.config import loader

    monkeypatch.delenv("STRIX_LLM", raising=False)
    monkeypatch.setattr(loader, "_cached", None)
    bellu.reset_cache()

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"status": "ok", "graph": False})

    result = bellu.health_check(client=_mock_client(handler))

    # Control plane is reachable but no LLM route => unhealthy.
    assert result.control_plane_ok is True
    assert result.model_configured is False
    assert result.healthy is False
    assert result.graph_online is False
    assert "STRIX_LLM" in result.detail


def test_health_check_non_200(monkeypatch: pytest.MonkeyPatch) -> None:
    from strix.config import loader

    monkeypatch.setenv("STRIX_LLM", "openai/gpt-5")
    monkeypatch.setattr(loader, "_cached", None)
    bellu.reset_cache()

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "down"})

    result = bellu.health_check(client=_mock_client(handler))

    assert result.control_plane_ok is False
    assert result.healthy is False
    assert "503" in result.detail
