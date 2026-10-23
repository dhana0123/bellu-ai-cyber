"""Bellu control-plane configuration for the agents plane.

The agents plane is a Python subsystem that layers on top of Bellu's
authoritative Go control plane. Per Requirement 1.3 it reaches the control
plane **only** over authenticated HTTP and MCP calls -- never Postgres or
Memgraph directly. This module holds the configuration for that single
boundary:

- :class:`BelluSettings` -- pydantic-settings model carrying the control-plane
  base URL and the service-account bearer token. Both come from the
  environment; the token is a secret and is never hardcoded.
- :func:`load_bellu_settings` -- memoized resolver, mirroring
  :func:`strix.config.load_settings`.
- :func:`selected_model` -- surfaces which LLM the plane routes to, reusing
  Strix's existing model config (``STRIX_LLM`` via :class:`LlmSettings`) rather
  than reinventing a second route.
- :func:`health_check` / :class:`HealthResult` -- a startup check that verifies
  connectivity to the control plane (``GET {BELLU_API_BASE}/api/health``) and
  that an LLM model route is configured.

Tenant isolation (Requirement 9.5): the service-account token identifies the
``agent_service_account`` principal, and the control plane resolves the org from
that principal server-side. Nothing here sends an org anywhere.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING, cast

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from strix.config import load_settings


if TYPE_CHECKING:
    import httpx


# Bellu's public, unauthenticated health endpoint (see backend/server/server.go:
# api.GET("/health", s.handleHealth) -> {"status": "ok", "graph": bool}).
HEALTH_PATH = "/api/health"

DEFAULT_API_BASE = "http://localhost:8080"
DEFAULT_HEALTH_TIMEOUT = 10.0

_HTTP_OK = 200


_BELLU_CONFIG = SettingsConfigDict(
    case_sensitive=False,
    populate_by_name=True,
    extra="ignore",
)


class BelluSettings(BaseSettings):
    """Connection settings for the Bellu control plane.

    Loaded from the environment, consistent with the rest of
    :mod:`strix.config`. The service-account token is a secret: it is read only
    from the environment (``repr=False`` keeps it out of logs) and is never
    written into source.
    """

    model_config = _BELLU_CONFIG

    api_base: str = Field(
        default=DEFAULT_API_BASE,
        alias="BELLU_API_BASE",
        description="Base URL of the Bellu Go control plane, e.g. https://bellu.example.com",
    )
    service_account_token: str | None = Field(
        default=None,
        alias="BELLU_SERVICE_ACCOUNT_TOKEN",
        repr=False,
        description=(
            "Bearer token for the agent_service_account principal. The control "
            "plane resolves the org from this principal server-side; no org is "
            "ever sent by the plane."
        ),
    )
    health_timeout: float = Field(
        default=DEFAULT_HEALTH_TIMEOUT,
        gt=0,
        alias="BELLU_HEALTH_TIMEOUT",
        description="Timeout (seconds) for the startup health check request.",
    )

    @field_validator("api_base", mode="before")
    @classmethod
    def _strip(cls, value: object) -> object:
        return value.strip() if isinstance(value, str) else value

    @property
    def base(self) -> str:
        """The API base with any trailing slash removed, ready to join paths."""
        return self.api_base.rstrip("/")

    def auth_header(self) -> dict[str, str]:
        """The Authorization header for the service account.

        Raises if no token is configured so callers fail loudly rather than
        issuing an unauthenticated request.
        """
        if not self.service_account_token:
            msg = "BELLU_SERVICE_ACCOUNT_TOKEN is not set; cannot authenticate to the control plane"
            raise RuntimeError(msg)
        return {"Authorization": f"Bearer {self.service_account_token}"}


_cached: BelluSettings | None = None


def load_bellu_settings() -> BelluSettings:
    """Resolve Bellu control-plane settings from the environment. Memoized."""
    global _cached  # noqa: PLW0603
    if _cached is None:
        _cached = BelluSettings()
    return _cached


def reset_cache() -> None:
    """Drop the memoized settings (used by tests that mutate the environment)."""
    global _cached  # noqa: PLW0603
    _cached = None


def selected_model() -> str | None:
    """The LLM model route the plane uses.

    The agents plane does **not** define its own model route. It reuses Strix's
    existing LLM configuration (:class:`strix.config.LlmSettings`, driven by the
    ``STRIX_LLM`` env var and resolved through ``StrixProvider`` in
    :mod:`strix.config.models`). This accessor simply surfaces the configured
    model id so the health check can confirm a route exists, and so operators
    can see which model is selected without reaching into Strix internals.

    Returns the model id string (e.g. ``"openai/gpt-5"``,
    ``"anthropic/claude-sonnet-4-6"``) or ``None`` when unset.
    """
    return load_settings().llm.model


def _extract_graph_online(body: object) -> bool:
    """Read the ``graph`` boolean from the health response body, if present."""
    if isinstance(body, dict):
        typed = cast("dict[str, object]", body)
        return bool(typed.get("graph", False))
    return False


@dataclass(frozen=True)
class HealthResult:
    """Outcome of the startup health check.

    ``healthy`` is true only when both the control plane answered the health
    endpoint and an LLM model route is configured.
    """

    healthy: bool
    control_plane_ok: bool
    model_configured: bool
    model: str | None
    graph_online: bool
    detail: str

    def __bool__(self) -> bool:
        return self.healthy


def health_check(
    settings: BelluSettings | None = None,
    *,
    client: httpx.Client | None = None,
) -> HealthResult:
    """Verify the plane can reach the control plane and has a model route.

    Performs ``GET {BELLU_API_BASE}/api/health`` (Bellu's public health
    endpoint) and checks that :func:`selected_model` returns a configured model.
    Returns a :class:`HealthResult` with a clear healthy/unhealthy verdict;
    never raises for an unreachable control plane -- the failure is reported in
    the result so a caller can decide how to react at startup.

    ``client`` lets callers (and tests) inject an ``httpx.Client``; by default a
    short-lived client is created and closed. The health endpoint is public, so
    no Authorization header is required here.
    """
    import httpx

    cfg = settings or load_bellu_settings()
    model = selected_model()
    model_configured = bool(model)

    url = f"{cfg.base}{HEALTH_PATH}"
    control_plane_ok = False
    graph_online = False
    detail_parts: list[str] = []

    owns_client = client is None
    http = client or httpx.Client(timeout=cfg.health_timeout)
    try:
        resp = http.get(url, timeout=cfg.health_timeout)
        if resp.status_code == _HTTP_OK:
            control_plane_ok = True
            body: object
            try:
                body = resp.json()
            except ValueError:
                body = {}
            graph_online = _extract_graph_online(body)
            detail_parts.append(f"control plane healthy at {url} (graph={graph_online})")
        else:
            detail_parts.append(f"control plane returned HTTP {resp.status_code} from {url}")
    except httpx.HTTPError as exc:
        detail_parts.append(f"control plane unreachable at {url}: {exc}")
    finally:
        if owns_client:
            http.close()

    if model_configured:
        detail_parts.append(f"model route configured: {model}")
    else:
        detail_parts.append("no LLM model route configured (set STRIX_LLM)")

    healthy = control_plane_ok and model_configured
    return HealthResult(
        healthy=healthy,
        control_plane_ok=control_plane_ok,
        model_configured=model_configured,
        model=model,
        graph_online=graph_online,
        detail="; ".join(detail_parts),
    )
