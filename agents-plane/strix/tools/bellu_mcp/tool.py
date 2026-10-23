"""``bellu_mcp`` — the live-graph query tool for scan agents.

A scan agent reasons over Bellu's canonical security graph by calling the six
read-only MCP tools the control plane already exposes at ``POST /api/mcp/*``:

- ``find_entities`` — search the graph for entities matching a filter.
- ``get_entity`` — fetch one entity by its canonical id.
- ``get_neighbors`` — walk one hop out from an entity.
- ``entity_history`` — the change history of a single entity.
- ``recent_changes`` — recently changed entities across the graph.
- ``describe_schema`` — the entity/edge kinds and their shape.

:class:`BelluMCP` is a thin async client over exactly those six tools. Three
invariants are load-bearing and verified by tests:

* **Read-only by construction** (Requirement 5.1): the tool maps one-to-one onto
  the six read tools and exposes no write path. An unknown tool name is rejected
  *before* any HTTP request is issued (Requirement 5.2), so a typo or a made-up
  tool can never reach the network.
* **No org, ever** (Requirement 5.3 / Property 1 "tenant isolation"): every
  request carries only the service-account bearer token. The tool never adds an
  org to the path, the query string, a header, or the body — the control plane
  resolves the tenant from the authenticated principal. The agent's ``args`` are
  forwarded as the JSON body unchanged; the tool adds nothing of its own.
* **Graceful degradation** (Requirement 5.5): a control-plane 503 (graph
  projection temporarily unavailable) is reported back as
  ``{"unavailable": True, ...}`` rather than raised, mirroring the graph-context
  seam's degradation. A per-call timeout cancels the in-flight request and is
  likewise reported as unavailable. Any other non-2xx status is an error.
"""

from __future__ import annotations

import json
import logging
from typing import TYPE_CHECKING, Any, cast

import httpx
from agents import function_tool

from strix.config.bellu import load_bellu_settings


if TYPE_CHECKING:
    from collections.abc import Mapping


logger = logging.getLogger(__name__)


# The six read-only MCP tools. This frozenset is the authoritative allow-list:
# any name outside it is rejected before a request is issued (Requirement 5.2),
# which is also what keeps the tool read-only by construction (Requirement 5.1).
MCP_READ_TOOLS: frozenset[str] = frozenset(
    {
        "find_entities",
        "get_entity",
        "get_neighbors",
        "entity_history",
        "recent_changes",
        "describe_schema",
    }
)

# Per-call timeout. A request still outstanding at the deadline is cancelled and
# reported as unavailable rather than raised (Requirement 5.5).
CALL_TIMEOUT_SECONDS = 15.0

_HTTP_SERVICE_UNAVAILABLE = 503


class UnknownMCPToolError(ValueError):
    """Raised when a tool name is not one of the six MCP read tools.

    Raised *before* any HTTP request is issued, so an unknown tool name never
    reaches the network (Requirement 5.2).
    """

    def __init__(self, tool: str) -> None:
        self.tool = tool
        allowed = ", ".join(sorted(MCP_READ_TOOLS))
        super().__init__(f"unknown MCP tool: {tool!r} (allowed: {allowed})")


class BelluMCP:
    """Async client over Bellu's six read-only MCP tools at ``POST /api/mcp/*``.

    Read-only by construction: it maps one-to-one onto the six read tools and has
    no write path. It never sends an org — the control plane resolves the tenant
    from the service-account principal carried by the bearer token.

    Args:
        base_url: The control-plane base URL (e.g. ``https://bellu.example.com``).
            A trailing slash is stripped so path joins stay clean.
        auth_header: The ``Authorization`` header for the service account, i.e.
            ``{"Authorization": "Bearer <token>"}``. This is the *only* identity
            the tool sends; no org is added anywhere.
        client: An injected :class:`httpx.AsyncClient` (used by tests via
            ``httpx.MockTransport``). When omitted, a short-lived client is
            created per call and closed afterward.
        timeout: Per-call timeout in seconds.
    """

    def __init__(
        self,
        base_url: str,
        auth_header: Mapping[str, str],
        *,
        client: httpx.AsyncClient | None = None,
        timeout: float = CALL_TIMEOUT_SECONDS,
    ) -> None:
        self._base = base_url.rstrip("/")
        # Copy so a caller mutating their dict later can't change what we send,
        # and so there is exactly one place the outgoing headers come from.
        self._auth_header = dict(auth_header)
        self._client = client
        self._timeout = timeout

    async def call(self, tool: str, args: dict[str, Any] | None = None) -> dict[str, Any]:
        """Call one MCP read tool and return its JSON payload.

        Rejects an unknown tool name before issuing any request. On HTTP 200
        returns the decoded payload; on HTTP 503 (or a timeout) returns an
        ``unavailable`` result rather than raising; any other non-2xx status
        raises :class:`httpx.HTTPStatusError`.

        The tool never sends an org: only the service-account ``Authorization``
        header is attached, and ``args`` is forwarded as the JSON body unchanged.
        """
        if tool not in MCP_READ_TOOLS:
            # Raise before any network activity (Requirement 5.2).
            raise UnknownMCPToolError(tool)

        url = f"{self._base}/api/mcp/{tool}"
        body: dict[str, Any] = args or {}

        try:
            resp = await self._post(url, body)
        except httpx.TimeoutException:
            # The per-call timeout cancels the in-flight request; report it as
            # unavailable, mirroring the 503 degradation (Requirement 5.5).
            logger.warning("bellu_mcp %s timed out after %.0fs", tool, self._timeout)
            return {"unavailable": True, "reason": "timeout"}

        if resp.status_code == _HTTP_SERVICE_UNAVAILABLE:
            # The control plane returns 503 when the graph projection is down;
            # degrade instead of raising so the agent can keep reasoning.
            logger.info("bellu_mcp %s unavailable (HTTP 503)", tool)
            return {"unavailable": True, "reason": "service_unavailable"}

        # Any other non-2xx (401/403/404/5xx…) is a real error for the agent.
        resp.raise_for_status()

        payload: Any = resp.json()
        if not isinstance(payload, dict):
            # The six MCP read tools return JSON objects; wrap anything else so
            # the return type stays a dict for callers.
            return {"result": payload}
        return cast("dict[str, Any]", payload)

    async def _post(self, url: str, body: dict[str, Any]) -> httpx.Response:
        """Issue the POST, using the injected client or a short-lived one.

        Only the service-account ``Authorization`` header is sent — no org in the
        path, query, headers, or body.
        """
        timeout = httpx.Timeout(self._timeout)
        if self._client is not None:
            return await self._client.post(
                url, json=body, headers=self._auth_header, timeout=timeout
            )
        async with httpx.AsyncClient(timeout=timeout) as client:
            return await client.post(url, json=body, headers=self._auth_header)


def _settings_client() -> BelluMCP:
    """Build a :class:`BelluMCP` from the plane's Bellu settings.

    Resolves the control-plane base URL and the service-account bearer token
    from the environment-backed :func:`load_bellu_settings`. Raises loudly (via
    ``auth_header``) if no token is configured, so the tool never issues an
    unauthenticated request.
    """
    cfg = load_bellu_settings()
    return BelluMCP(cfg.base, cfg.auth_header())


def _invalid_args(tool: str) -> str:
    """The sanitized error for a non-object ``args`` value."""
    return json.dumps(
        {
            "success": False,
            "error": (
                f"Invalid args for {tool}: expected a JSON object of argument "
                "names to values, or none"
            ),
        },
        ensure_ascii=False,
    )


@function_tool(timeout=30, strict_mode=False)
async def bellu_mcp(tool: str, args: Any = None) -> str:
    """Query Bellu's live security graph through its read-only MCP tools.

    Use this to reason over the *current* graph state during a scan — it reads
    the same canonical graph the initial graph-context block was drawn from, but
    live, so you can follow edges and look up entities you discover as you go.
    It is strictly read-only: it cannot change the graph.

    Pick exactly one of the six tools and pass its arguments in ``args``:

    - ``find_entities`` — search for entities (e.g. by kind or a name/text
      filter). Start here when you don't yet have a canonical id.
    - ``get_entity`` — fetch one entity by its canonical id
      (``kind:provider:native-id``).
    - ``get_neighbors`` — one hop out from an entity to see what it connects to
      (the primary way to walk an attack path).
    - ``entity_history`` — how a single entity changed over time.
    - ``recent_changes`` — entities that changed recently across the graph.
    - ``describe_schema`` — the entity and edge kinds and their shape; call this
      first if you're unsure what to query for.

    The tenant (org) is resolved server-side from the scan's service account —
    you never pass it and cannot query another tenant's graph.

    If the graph projection is temporarily unavailable the result is
    ``{"unavailable": true, ...}`` instead of an error; treat that as "the live
    graph is down right now" and fall back to the injected graph-context block
    or retry later rather than giving up.

    Args:
        tool: One of ``find_entities``, ``get_entity``, ``get_neighbors``,
            ``entity_history``, ``recent_changes``, ``describe_schema``. Any
            other name is rejected without issuing a request.
        args: The arguments for the chosen tool, forwarded as the request body
            as a JSON object of names to values. Omit or pass ``{}`` for tools
            that take no arguments (e.g. ``describe_schema``). Pass an object,
            not a stringified object.
    """
    # ``args`` is schema-less (an open object is not expressible as a strict tool
    # schema, so this tool is strict_mode=False), which means some models send it
    # as a JSON string. Accept a string that decodes to an object so a correct
    # call is not rejected over its encoding (mirrors ``call_mcp``).
    decoded: Any = args
    if isinstance(decoded, str):
        stripped = decoded.strip()
        try:
            decoded = json.loads(stripped) if stripped else {}
        except json.JSONDecodeError:
            return _invalid_args(tool)
    if decoded is not None and not isinstance(decoded, dict):
        return _invalid_args(tool)
    call_args: dict[str, Any] | None = cast("dict[str, Any] | None", decoded)

    client = _settings_client()
    try:
        result = await client.call(tool, call_args)
    except UnknownMCPToolError as exc:
        return json.dumps({"success": False, "error": str(exc)}, ensure_ascii=False)
    except httpx.HTTPStatusError as exc:
        status = exc.response.status_code
        logger.warning("bellu_mcp %s failed: HTTP %s", tool, status)
        return json.dumps(
            {"success": False, "error": f"MCP tool {tool} returned HTTP {status}"},
            ensure_ascii=False,
        )
    except httpx.HTTPError as exc:
        logger.warning("bellu_mcp %s network error: %s", tool, exc)
        return json.dumps(
            {"success": False, "error": f"MCP tool {tool} is unreachable"},
            ensure_ascii=False,
        )
    return json.dumps({"success": True, "tool": tool, "result": result}, ensure_ascii=False)
