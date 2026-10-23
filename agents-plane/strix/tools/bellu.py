"""Register the Bellu-specific agent tools for every scan agent.

The forked Strix engine gains a small set of Bellu tools that talk to the Go
control plane over the authenticated HTTP/MCP seams. They are registered the
same way any extra scan-agent tool is: through
:func:`strix.agents.factory.register_agent_tools`, which adds them to both the
root and child agents after the base toolset and before the lifecycle tool.

:func:`register_bellu_tools` is the single entry point the plane calls once at
run startup, before the first ``build_strix_agent`` call (mirroring how
``strix.runtime.backends.register_backend`` is expected to run before any agent
is built). It is idempotent: ``register_agent_tools`` ignores a tool object it
has already seen, so calling this twice is harmless.

Today this registers ``bellu_mcp`` (task 9). The ``graph_context`` provider and
the ``finding_report`` tool (tasks 10 and 11) are deliberately *not* registered
here: ``graph_context`` is injected into the root agent's prompt rather than
exposed as a callable tool, and ``finding_report`` is wired separately. As each
lands it joins this one call site if it is a callable agent tool.
"""

from __future__ import annotations

import logging

from strix.agents.factory import register_agent_tools
from strix.tools.bellu_mcp.tool import bellu_mcp


logger = logging.getLogger(__name__)


def register_bellu_tools() -> None:
    """Register the Bellu scan-agent tools. Idempotent.

    Call once at run startup, before the first agent is built. Every scan agent
    built afterwards (root and children) gets the Bellu tools.
    """
    register_agent_tools(bellu_mcp)
    logger.info("Registered Bellu agent tools: bellu_mcp")
