"""Tests for the Bellu graph-context provider (task 10.1).

Covers:

- ``fetch`` builds the right URL, sends the service-account bearer token and no
  org, and encodes the ``?target=`` value to match the Go ``parseAgentTargetRef``
  (``entity:<canonical_id>`` / ``raw:<url-or-ip>``) — HTTP mocked via
  ``httpx.MockTransport`` (Requirements 4.1, 9.5, 3.2).
- ``as_prompt_block`` renders nodes (with kinds) and edges (with relations) for
  a non-empty subgraph (Requirement 10.1).
- an empty-but-valid online subgraph renders a degraded "empty subgraph" block
  (Requirement 4.6).
- an unavailable projection (``graph: false``) renders the "graph-context
  unavailable" indication and never crashes (Requirement 4.7).
- a non-200 response degrades gracefully instead of raising.
- the ``graph_context_block`` injection helper used by the Scan_Runner (task 12).
"""

from __future__ import annotations

import httpx
import pytest

from strix.tools.graph_context import GraphContext, graph_context_block


_TOKEN = "sa-token-xyz"  # fixture token, not a real secret
_BASE = "https://cp.example.com"


def _client(handler: object) -> httpx.Client:
    return httpx.Client(transport=httpx.MockTransport(handler))  # type: ignore[arg-type]


# --- fetch: URL, target encoding, auth header --------------------------------


def test_fetch_builds_url_encodes_target_and_sends_bearer() -> None:
    seen: dict[str, str] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["url"] = str(request.url)
        seen["path"] = request.url.path
        seen["target"] = request.url.params.get("target", "")
        seen["auth"] = request.headers.get("Authorization", "")
        return httpx.Response(
            200,
            json={
                "target": {"kind": "entity", "canonical_id": "host:aws:i-0abc"},
                "seed": {"canonical_id": "host:aws:i-0abc", "kind": "host", "name": "web-1"},
                "neighbors": [],
                "edges": [],
                "bounded": True,
                "graph": True,
                "truncated": False,
            },
        )

    target = GraphContext.encode_target("entity", "host:aws:i-0abc")
    gc = GraphContext.fetch(_BASE, _TOKEN, target, client=_client(handler))

    assert seen["path"] == "/api/agent/graph-context"
    # Target encoding matches the Go parseAgentTargetRef: entity:<canonical_id>,
    # canonical id keeps its own colons (only the first colon is the scheme).
    assert seen["target"] == "entity:host:aws:i-0abc"
    assert seen["auth"] == f"Bearer {_TOKEN}"
    # Tenant isolation: no org is ever sent (Req 9.5).
    assert "org" not in seen["url"]
    assert gc.graph_online is True
    assert gc.seed is not None


def test_encode_target_raw_and_validation() -> None:
    assert GraphContext.encode_target("raw", "http://10.0.0.5") == "raw:http://10.0.0.5"
    assert GraphContext.encode_target("ENTITY", "host:aws:i-1") == "entity:host:aws:i-1"
    with pytest.raises(ValueError, match="unknown target kind"):
        GraphContext.encode_target("bogus", "x")
    with pytest.raises(ValueError, match="must not be empty"):
        GraphContext.encode_target("raw", "   ")


# --- rendering: non-empty subgraph -------------------------------------------


def test_as_prompt_block_renders_nodes_and_edges() -> None:
    gc = GraphContext(
        target={"kind": "entity", "canonical_id": "host:aws:i-0abc"},
        seed={"canonical_id": "host:aws:i-0abc", "kind": "host", "name": "web-1"},
        neighbors=[
            {"canonical_id": "sg:aws:sg-1", "kind": "security_group", "name": "web-sg"},
            {"canonical_id": "role:aws:r-1", "kind": "iam_role", "name": "web-role"},
        ],
        edges=[
            {"relation": "PROTECTED_BY", "src": "host:aws:i-0abc", "dst": "sg:aws:sg-1"},
            {"relation": "ASSUMES", "src": "host:aws:i-0abc", "dst": "role:aws:r-1"},
        ],
        graph_online=True,
    )

    block = gc.as_prompt_block()

    # Nodes listed with their kind.
    assert "SEED: host:aws:i-0abc [host] web-1" in block
    assert "sg:aws:sg-1 [security_group] web-sg" in block
    assert "role:aws:r-1 [iam_role] web-role" in block
    # Edges listed as src -(relation)-> dst.
    assert "host:aws:i-0abc -(PROTECTED_BY)-> sg:aws:sg-1" in block
    assert "host:aws:i-0abc -(ASSUMES)-> role:aws:r-1" in block
    # Delimited and carries graph-aware citation guidance.
    assert "<bellu_graph_context>" in block
    assert "</bellu_graph_context>" in block
    assert "canonical_id" in block


def test_as_prompt_block_marks_truncation() -> None:
    gc = GraphContext(
        target={"kind": "entity", "canonical_id": "host:aws:i-0abc"},
        seed={"canonical_id": "host:aws:i-0abc", "kind": "host", "name": "web-1"},
        neighbors=[{"canonical_id": "sg:aws:sg-1", "kind": "security_group", "name": "sg"}],
        edges=[],
        graph_online=True,
        truncated=True,
    )
    assert "TRUNCATED" in gc.as_prompt_block()


# --- rendering: empty-but-valid online subgraph (raw target) -----------------


def test_as_prompt_block_empty_online_is_valid_degraded_block() -> None:
    gc = GraphContext(
        target={"kind": "raw", "raw": "http://10.0.0.5"},
        seed=None,
        neighbors=[],
        edges=[],
        graph_online=True,
    )
    block = gc.as_prompt_block()
    assert "Empty subgraph" in block
    assert "TARGET: raw http://10.0.0.5" in block
    assert block.count("<bellu_graph_context>") == 1
    assert "</bellu_graph_context>" in block


# --- rendering: projection unavailable (graph: false) ------------------------


def test_as_prompt_block_unavailable_projection() -> None:
    gc = GraphContext(
        target={"kind": "entity", "canonical_id": "host:aws:i-0abc"},
        seed=None,
        neighbors=[],
        edges=[],
        graph_online=False,
    )
    block = gc.as_prompt_block()
    assert "Graph-context unavailable" in block
    assert "<bellu_graph_context>" in block


def test_fetch_graph_false_renders_unavailable() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={
                "target": {"kind": "entity", "canonical_id": "host:aws:i-0abc"},
                "neighbors": [],
                "edges": [],
                "bounded": True,
                "graph": False,
            },
        )

    target = GraphContext.encode_target("entity", "host:aws:i-0abc")
    gc = GraphContext.fetch(_BASE, _TOKEN, target, client=_client(handler))
    assert gc.graph_online is False
    assert "Graph-context unavailable" in gc.as_prompt_block()


# --- fetch: graceful degradation on non-200 ----------------------------------


def test_fetch_non_200_degrades_gracefully() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "temporarily unavailable"})

    target = GraphContext.encode_target("entity", "host:aws:i-0abc")
    gc = GraphContext.fetch(_BASE, _TOKEN, target, client=_client(handler))

    # No raise; a valid empty offline context the root can still start from.
    assert gc.graph_online is False
    assert gc.is_empty()
    assert gc.target == {"kind": "entity", "canonical_id": "host:aws:i-0abc"}
    assert "Graph-context unavailable" in gc.as_prompt_block()


def test_fetch_transport_error_degrades_gracefully() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    target = GraphContext.encode_target("raw", "http://10.0.0.5")
    gc = GraphContext.fetch(_BASE, _TOKEN, target, client=_client(handler))
    assert gc.graph_online is False
    assert gc.target == {"kind": "raw", "raw": "http://10.0.0.5"}


# --- injection API for task 12 -----------------------------------------------


def test_graph_context_block_prepends_before_instructions() -> None:
    gc = GraphContext(
        target={"kind": "entity", "canonical_id": "host:aws:i-0abc"},
        seed={"canonical_id": "host:aws:i-0abc", "kind": "host", "name": "web-1"},
        neighbors=[],
        edges=[],
        graph_online=True,
    )
    merged = graph_context_block(gc, "Scan this host for SSRF.")
    block = gc.as_prompt_block()
    assert merged.startswith(block)
    assert merged.endswith("Scan this host for SSRF.")


def test_graph_context_block_standalone() -> None:
    gc = GraphContext(
        target={"kind": "raw", "raw": "http://10.0.0.5"},
        seed=None,
        neighbors=[],
        edges=[],
        graph_online=True,
    )
    assert graph_context_block(gc) == gc.as_prompt_block()
    assert graph_context_block(gc, "   ") == gc.as_prompt_block()
