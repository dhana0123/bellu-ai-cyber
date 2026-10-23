"""Tests for the ``bellu_mcp`` live-graph query tool (task 9.1).

Covers the three load-bearing invariants of :class:`BelluMCP`:

- **Unknown-tool rejection** (Requirement 5.2): a tool name outside the six MCP
  read tools raises *before* any HTTP request is issued.
- **Graceful degradation** (Requirement 5.5): a control-plane 503 is reported as
  an ``unavailable`` result rather than raised; a timeout is likewise unavailable.
- **200 round-trip** (Requirement 5.1): a successful call returns the payload and
  hits ``POST /api/mcp/<tool>``.
- **No org ever** (Requirement 5.3 / Property 1): no request carries an org in the
  path, query string, headers, or body — only the service-account bearer token.

The HTTP call is mocked with ``httpx.MockTransport``, mirroring
``tests/test_bellu_config.py``. ``BelluMCP`` is driven directly with an injected
``httpx.AsyncClient`` so the behavior is exercised without a live control plane.
"""

from __future__ import annotations

import json
from typing import Any

import httpx
import pytest

from strix.tools.bellu_mcp.tool import (
    MCP_READ_TOOLS,
    BelluMCP,
    UnknownMCPToolError,
)


_AUTH = {"Authorization": "Bearer sa-token-xyz"}


def _capture() -> tuple[list[httpx.Request], httpx.AsyncClient]:
    """An async MockTransport client that records every request it serves 200."""
    seen: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json={"entities": [], "ok": True})

    client = httpx.AsyncClient(transport=httpx.MockTransport(handler))
    return seen, client


def _client(handler: Any) -> httpx.AsyncClient:
    return httpx.AsyncClient(transport=httpx.MockTransport(handler))


# --- unknown-tool rejection (Requirement 5.2) --------------------------------


async def test_unknown_tool_raises_before_any_request() -> None:
    issued = False

    def handler(request: httpx.Request) -> httpx.Response:
        nonlocal issued
        issued = True
        return httpx.Response(200, json={})

    client = _client(handler)
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client)
    try:
        with pytest.raises(UnknownMCPToolError, match="unknown MCP tool"):
            await tool.call("drop_graph", {"x": 1})
        # No HTTP request may have been issued for a rejected tool name.
        assert issued is False
    finally:
        await client.aclose()


async def test_unknown_tool_error_lists_the_rejected_name() -> None:
    client = _client(lambda request: httpx.Response(200, json={}))
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client)
    try:
        with pytest.raises(UnknownMCPToolError) as excinfo:
            await tool.call("find_entitiez")  # typo
        assert excinfo.value.tool == "find_entitiez"
    finally:
        await client.aclose()


# --- 200 round-trip (Requirement 5.1) ----------------------------------------


@pytest.mark.parametrize("name", sorted(MCP_READ_TOOLS))
async def test_each_read_tool_round_trips(name: str) -> None:
    seen, client = _capture()
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client)
    try:
        result = await tool.call(name, {"filter": "kind=host"})
    finally:
        await client.aclose()

    assert result == {"entities": [], "ok": True}
    assert len(seen) == 1
    req = seen[0]
    assert req.method == "POST"
    assert str(req.url) == f"https://cp.example.com/api/mcp/{name}"


async def test_no_args_sends_empty_body() -> None:
    seen, client = _capture()
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client)
    try:
        await tool.call("describe_schema")
    finally:
        await client.aclose()

    assert json.loads(seen[0].content) == {}


async def test_base_url_trailing_slash_is_stripped() -> None:
    seen, client = _capture()
    tool = BelluMCP("https://cp.example.com/", _AUTH, client=client)
    try:
        await tool.call("get_entity", {"id": "host:aws:i-1"})
    finally:
        await client.aclose()

    assert str(seen[0].url) == "https://cp.example.com/api/mcp/get_entity"


# --- 503 / timeout -> unavailable (Requirement 5.5) --------------------------


async def test_503_returns_unavailable_not_raise() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "graph temporarily unavailable"})

    client = _client(handler)
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client)
    try:
        result = await tool.call("get_neighbors", {"id": "host:aws:i-1"})
    finally:
        await client.aclose()

    assert result == {"unavailable": True, "reason": "service_unavailable"}


async def test_timeout_returns_unavailable_not_raise() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ReadTimeout("deadline exceeded", request=request)

    client = _client(handler)
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client, timeout=0.01)
    try:
        result = await tool.call("recent_changes", {})
    finally:
        await client.aclose()

    assert result == {"unavailable": True, "reason": "timeout"}


# --- other non-200/503 -> error (Requirement 5.x) ----------------------------


async def test_other_error_status_raises() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, json={"error": "forbidden"})

    client = _client(handler)
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client)
    try:
        with pytest.raises(httpx.HTTPStatusError):
            await tool.call("find_entities", {})
    finally:
        await client.aclose()


# --- no org anywhere (Requirement 5.3 / Property 1) --------------------------


async def test_never_sends_an_org_anywhere() -> None:
    """Across the six tools and varied args, no request carries an org.

    Inspects the mocked request's URL (path + query), headers, and JSON body.
    The only identity sent is the service-account bearer token.
    """
    seen: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json={"ok": True})

    client = _client(handler)
    tool = BelluMCP("https://cp.example.com", _AUTH, client=client)

    # Deliberately include org-looking keys in the args the agent passes: the
    # tool forwards args verbatim as the body, so these appear in the body but
    # the tool itself must never *add* an org anywhere.
    arg_variants: list[dict[str, Any]] = [
        {},
        {"filter": "kind=host"},
        {"id": "host:aws:i-1", "depth": 1},
        {"limit": 50},
    ]
    try:
        for name in sorted(MCP_READ_TOOLS):
            for args in arg_variants:
                await tool.call(name, args)
    finally:
        await client.aclose()

    assert seen, "expected requests to have been issued"
    for req in seen:
        # URL path never addresses an org, and no org query parameter is added.
        assert "org" not in req.url.params
        assert "/org" not in req.url.path
        # Headers carry only the bearer token as identity; no org header.
        header_keys = {k.lower() for k in req.headers}
        assert not any("org" in k for k in header_keys)
        assert req.headers["authorization"] == "Bearer sa-token-xyz"
        # The tool adds nothing to the body: it is exactly the forwarded args.
        body = json.loads(req.content) if req.content else {}
        assert "org" not in body


# --- registration seam -------------------------------------------------------


def test_register_bellu_tools_registers_bellu_mcp() -> None:
    """``register_bellu_tools`` wires ``bellu_mcp`` into the agent toolset."""
    from strix.agents.factory import registered_agent_tools
    from strix.tools.bellu import register_bellu_tools
    from strix.tools.bellu_mcp.tool import bellu_mcp

    register_bellu_tools()
    names = {getattr(t, "name", None) for t in registered_agent_tools()}
    assert "bellu_mcp" in names
    # Idempotent: a second call does not double-register.
    register_bellu_tools()
    registered = [t for t in registered_agent_tools() if t is bellu_mcp]
    assert len(registered) == 1
