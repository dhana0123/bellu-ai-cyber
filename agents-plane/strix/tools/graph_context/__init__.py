"""The ``graph_context`` provider package.

Surfaces the bounded scoped subgraph the control plane returns at
``GET /api/agent/graph-context`` and renders it for injection into the root
agent's context (Requirements 10.1, 10.2, 7.6). See :mod:`provider` for the
``GraphContext`` dataclass, ``fetch``/``as_prompt_block``, and the
``graph_context_block`` injection helper the Scan_Runner (task 12) calls.
"""

from __future__ import annotations

from strix.tools.graph_context.provider import GraphContext, graph_context_block


__all__ = ["GraphContext", "graph_context_block"]
