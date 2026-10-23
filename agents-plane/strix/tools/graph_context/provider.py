"""Bellu graph-context provider for the agents plane.

This module implements seam 1 of the agents plane on the Python side (task
10.1). It fetches the bounded scoped subgraph the Go control plane exposes at
``GET /api/agent/graph-context`` and renders it as a prompt block for injection
into the root agent's context, so the agent reasons over Bellu's canonical
security graph from the very first turn.

Design references:

- Requirement 4 (graph-context seam) and Requirement 10 (Bellu-graph-aware
  prompting): the provider surfaces the injected bounded subgraph to the agent.
- Requirement 7.6: the Scan_Runner (task 12) fetches and injects graph-context
  *before* the coordinator initializes the root agent.
- The Go contract is :file:`backend/server/agent_graph_context_handlers.go`:
  the endpoint returns ``{target, seed, neighbors[], edges[], bounded, graph,
  truncated}``; node fields are ``canonical_id`` / ``kind`` / ``name`` /
  ``attributes``; edge fields are ``relation`` / ``src`` / ``dst``. The
  ``?target=`` query is a scheme-prefixed string (``parseAgentTargetRef``):
  ``entity:<canonical_id>`` or ``raw:<url-or-ip>``.

Tenant isolation (Requirement 9.5): the provider sends only the service-account
bearer token. It never sends an org; the control plane resolves the org from
the authenticated principal server-side.

Graceful degradation (Requirements 4.6, 4.7): an empty subgraph renders a valid
"empty subgraph" block and an unavailable projection (``graph: false``) renders
an explicit "graph-context unavailable" indication. Rendering never raises.

Injection API for task 12 (the Scan_Runner):

    from strix.tools.graph_context import GraphContext, graph_context_block

    gc = GraphContext.fetch(settings.base, token, "entity:host:aws:i-0abc")
    block = gc.as_prompt_block()                 # raw block, or
    override = graph_context_block(gc, run_root_instructions)  # merged override

The Scan_Runner passes the rendered block to ``run_strix_scan`` as part of
``root_instructions_override`` (the runner composes it into the root prompt via
``_compose_root_instructions_override`` before the first instruction). See
:func:`graph_context_block` for the clean merge helper.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any, cast


if TYPE_CHECKING:
    import httpx


# Timeout for the one graph-context fetch at run start. Matches the design's
# 20s and the ``bellu_mcp`` tool's bounded-request convention.
_FETCH_TIMEOUT = 20.0

_GRAPH_CONTEXT_PATH = "/api/agent/graph-context"

_HTTP_OK = 200

# The prompt block is delimited so the agent (and the runner's prompt composer)
# can recognize it as an injected, authoritative context region.
_BLOCK_OPEN = "<bellu_graph_context>"
_BLOCK_CLOSE = "</bellu_graph_context>"

# Guidance adapted from the Strix prompting guide, specialized for Bellu's
# canonical graph. Kept as a module constant so the empty / unavailable /
# populated renderings all share one voice and the agent always learns how to
# cite graph entities.
_GUIDANCE = (
    "The block above is a BOUNDED initial slice of Bellu's canonical security "
    "graph for this run's target, resolved server-side for your org. It is not "
    "exhaustive: use the bellu_mcp read tools (get_entity, get_neighbors, "
    "find_entities, entity_history, recent_changes, describe_schema) to expand "
    "from any entity below when you need more. When you reason about or report "
    "on an entity, cite it by its canonical_id (kind:provider:native-id) so the "
    "finding links back to the graph."
)


def _empty_rows() -> list[dict[str, Any]]:
    """Typed default factory for the neighbors/edges fields."""
    return []


@dataclass(frozen=True)
class GraphContext:
    """An immutable bounded scoped subgraph handed to a run.

    Mirrors the Go ``agentGraphContext`` wire type. The payload is bounded by
    construction server-side (neighbor count <= maxContextNodes, depth <=
    maxContextDepth), so this object's size never scales with total graph size.

    ``graph_online`` is the decoded ``graph`` field: ``False`` means the
    Memgraph projection was unavailable and the subgraph is empty-but-valid
    (Requirement 4.7). ``truncated`` means server-side caps clipped the result
    (Requirement 4.4).
    """

    target: dict[str, Any]
    seed: dict[str, Any] | None
    neighbors: list[dict[str, Any]] = field(default_factory=_empty_rows)
    edges: list[dict[str, Any]] = field(default_factory=_empty_rows)
    bounded: bool = True
    graph_online: bool = False
    truncated: bool = False

    @classmethod
    def encode_target(cls, kind: str, value: str) -> str:
        """Encode a target into the scheme-prefixed ``?target=`` query value.

        The encoding MUST match the Go ``parseAgentTargetRef``:
        ``entity:<canonical_id>`` or ``raw:<url-or-ip>``. The canonical id
        itself contains colons (kind:provider:native-id); the Go parser splits
        only on the FIRST colon, so the value is appended verbatim.
        """
        scheme = kind.strip().lower()
        if scheme not in {"entity", "raw"}:
            raise ValueError(f"unknown target kind: {kind!r} (want 'entity' or 'raw')")
        value = value.strip()
        if not value:
            raise ValueError("target value must not be empty")
        return f"{scheme}:{value}"

    @classmethod
    def from_payload(cls, payload: dict[str, Any]) -> GraphContext:
        """Build a :class:`GraphContext` from a decoded response body.

        Tolerant of missing optional fields so a degraded control-plane
        response (empty neighbors, ``graph: false``) still decodes cleanly.
        """
        target_raw = payload.get("target")
        target = cast("dict[str, Any]", target_raw) if isinstance(target_raw, dict) else {}

        seed_raw = payload.get("seed")
        seed = cast("dict[str, Any]", seed_raw) if isinstance(seed_raw, dict) else None

        return cls(
            target=target,
            seed=seed,
            neighbors=_dict_list(payload.get("neighbors")),
            edges=_dict_list(payload.get("edges")),
            bounded=bool(payload.get("bounded", True)),
            graph_online=bool(payload.get("graph", False)),
            truncated=bool(payload.get("truncated", False)),
        )

    @classmethod
    def fetch(
        cls,
        bellu_base: str,
        token: str,
        target: str,
        *,
        client: httpx.Client | None = None,
    ) -> GraphContext:
        """Fetch the bounded subgraph for ``target`` from the control plane.

        Issues ``GET {bellu_base}/api/agent/graph-context?target=<target>`` with
        the service-account bearer token and no org (Requirement 9.5). ``target``
        is the scheme-prefixed encoding produced by :meth:`encode_target`
        (``entity:<canonical_id>`` or ``raw:<url-or-ip>``).

        A non-200 response is handled gracefully: rather than raising, it yields
        a valid degraded context (empty subgraph, ``graph_online`` reflecting a
        503) so the run can still start. ``client`` lets callers (and tests)
        inject an ``httpx.Client`` for transport mocking.
        """
        import httpx

        base = bellu_base.rstrip("/")
        url = f"{base}{_GRAPH_CONTEXT_PATH}"
        headers = {"Authorization": f"Bearer {token}"}

        owns_client = client is None
        http = client or httpx.Client(timeout=_FETCH_TIMEOUT)
        try:
            resp = http.get(
                url,
                params={"target": target},
                headers=headers,
                timeout=_FETCH_TIMEOUT,
            )
        except httpx.HTTPError:
            # Unreachable control plane at run start degrades to an empty,
            # offline context rather than aborting the run.
            return cls._degraded(target)
        finally:
            if owns_client:
                http.close()

        if resp.status_code != _HTTP_OK:
            # 503 (graph read failure) and any other non-200 degrade to an
            # empty offline context; the run proceeds read-only without a seed.
            return cls._degraded(target)

        try:
            body = resp.json()
        except ValueError:
            return cls._degraded(target)
        if not isinstance(body, dict):
            return cls._degraded(target)
        return cls.from_payload(cast("dict[str, Any]", body))

    @classmethod
    def _degraded(cls, target: str) -> GraphContext:
        """A valid empty, offline context used when the fetch cannot succeed."""
        kind, _, rest = target.partition(":")
        ref: dict[str, Any] = {"kind": kind}
        if kind == "entity":
            ref["canonical_id"] = rest
        elif kind == "raw":
            ref["raw"] = rest
        return cls(target=ref, seed=None, graph_online=False)

    # --- rendering -----------------------------------------------------------

    def is_empty(self) -> bool:
        """True when there is no seed and no neighbors to render."""
        return self.seed is None and not self.neighbors

    def as_prompt_block(self) -> str:
        """Render the bounded subgraph as a prompt block for the root agent.

        Three shapes, all valid and none ever raising (Requirements 10.1, 4.6,
        4.7):

        - projection unavailable (``graph_online`` False and empty) -> an
          explicit "graph-context unavailable" degraded block;
        - online but empty (raw target, or a seed not yet in the projection) ->
          a valid "empty subgraph" block;
        - populated -> the seed, neighbors listed with their kind, and edges
          listed as ``src -(relation)-> dst``.
        """
        lines: list[str] = [_BLOCK_OPEN]

        if not self.graph_online and self.is_empty():
            lines.append(
                "Graph-context unavailable: the security-graph projection could "
                "not be reached for this target. Proceed with live bellu_mcp "
                "queries; the initial subgraph is empty for this run.",
            )
            lines.append(_target_line(self.target))
            lines.append(_BLOCK_CLOSE)
            lines.append("")
            lines.append(_GUIDANCE)
            return "\n".join(lines)

        lines.append(_target_line(self.target))

        if self.is_empty():
            lines.append(
                "Empty subgraph: no graph entity is linked to this target yet "
                "(a raw target, or a seed not present in the projection). Start "
                "from the target itself and use bellu_mcp to discover related "
                "entities.",
            )
            lines.append(_BLOCK_CLOSE)
            lines.append("")
            lines.append(_GUIDANCE)
            return "\n".join(lines)

        if self.seed is not None:
            lines.append("")
            lines.append(f"SEED: {_node_line(self.seed)}")

        lines.append("")
        lines.append(f"NEIGHBORS ({len(self.neighbors)}):")
        if self.neighbors:
            lines.extend(f"- {_node_line(n)}" for n in self.neighbors)
        else:
            lines.append("- (none within the bounded slice)")

        lines.append("")
        lines.append(f"EDGES ({len(self.edges)}):")
        if self.edges:
            lines.extend(f"- {_edge_line(e)}" for e in self.edges)
        else:
            lines.append("- (none within the bounded slice)")

        if self.truncated:
            lines.append("")
            lines.append(
                "NOTE: this slice was TRUNCATED by server-side caps; the full "
                "neighborhood is larger. Expand with bellu_mcp get_neighbors.",
            )

        lines.append(_BLOCK_CLOSE)
        lines.append("")
        lines.append(_GUIDANCE)
        return "\n".join(lines)


def _dict_list(value: object) -> list[dict[str, Any]]:
    """Coerce a decoded JSON value into a list of dict rows, dropping non-dicts.

    A missing or malformed field (not a list, or list with stray scalars) still
    yields a valid ``list[dict[str, Any]]`` so a degraded response decodes
    cleanly without raising.
    """
    if not isinstance(value, list):
        return []
    items = cast("list[object]", value)
    rows: list[dict[str, Any]] = [
        cast("dict[str, Any]", item) for item in items if isinstance(item, dict)
    ]
    return rows


def _target_line(target: dict[str, Any]) -> str:
    """One line naming the scan target from a decoded target ref."""
    kind = str(target.get("kind", "")) or "unknown"
    if kind == "entity":
        return f"TARGET: entity {target.get('canonical_id', '') or '(unknown)'}"
    if kind == "raw":
        return f"TARGET: raw {target.get('raw', '') or '(unknown)'}"
    return f"TARGET: {kind}"


def _node_line(node: dict[str, Any]) -> str:
    """Render one node as ``canonical_id [kind] name``."""
    canonical = str(node.get("canonical_id", "") or "(unknown)")
    kind = str(node.get("kind", "") or "?")
    name = str(node.get("name", "") or "")
    line = f"{canonical} [{kind}]"
    if name:
        line += f" {name}"
    return line


def _edge_line(edge: dict[str, Any]) -> str:
    """Render one edge as ``src -(relation)-> dst``."""
    src = str(edge.get("src", "") or "(unknown)")
    dst = str(edge.get("dst", "") or "(unknown)")
    relation = str(edge.get("relation", "") or "related")
    return f"{src} -({relation})-> {dst}"


def graph_context_block(
    graph_context: GraphContext,
    root_instructions: str | None = None,
) -> str:
    """Compose the injection payload the Scan_Runner (task 12) hands the runner.

    This is the clean injection API for task 12. The Scan_Runner fetches a
    :class:`GraphContext` *before* the coordinator initializes the root agent
    (Requirement 7.6), then calls this to build the ``root_instructions_override``
    string it passes to :func:`strix.core.runner.run_strix_scan`::

        gc = GraphContext.fetch(settings.base, token, target)
        override = graph_context_block(gc, run.get("instructions"))
        await run_strix_scan(..., root_instructions_override=override)

    The runner composes ``root_instructions_override`` into the root system
    prompt (after the base instructions, before the scope block) via
    ``_compose_root_instructions_override``, so the graph context lands in the
    root agent's context ahead of its first instruction. When the run carries
    its own root instructions, the graph block is prepended so context comes
    before task direction; otherwise the block stands alone.
    """
    block = graph_context.as_prompt_block()
    extra = (root_instructions or "").strip()
    if extra:
        return f"{block}\n\n{extra}"
    return block
