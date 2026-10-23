"""``bellu_mcp`` — live, read-only graph queries over Bellu's MCP tools.

The package exposes two things:

- :class:`~strix.tools.bellu_mcp.tool.BelluMCP` — the testable async client that
  maps one-to-one onto Bellu's six read-only MCP tools, sends the service-account
  bearer token and never an org, rejects unknown tool names before issuing a
  request, and degrades a 503 (or a timeout) to an ``unavailable`` result rather
  than raising.
- :func:`~strix.tools.bellu_mcp.tool.bellu_mcp` — the SDK ``function_tool``
  wrapper registered for every scan agent via
  :func:`strix.agents.factory.register_agent_tools`.

Import deeply (``from strix.tools.bellu_mcp.tool import bellu_mcp``) so importing
``strix.tools`` does not pull httpx in eagerly.
"""

from __future__ import annotations
