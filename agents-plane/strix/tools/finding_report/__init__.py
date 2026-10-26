"""The ``finding_report`` tool: write Strix findings back into Bellu.

This is seam 2 of the agents plane on the Python side (Requirement 6). A Strix
scan produces findings as the agent tree works; this package maps each one onto
Bellu's finding-ingest schema and POSTs it to the control plane's write seam at
``POST {BELLU_API_BASE}/api/agent/findings:ingest``.

Two public entry points:

- :func:`report_findings` -- the stateless core: translate a batch of Strix
  findings into the ingest schema and POST them, returning the control plane's
  ``{created, updated, rejected}`` counts.
- :class:`BelluReporter` -- the stateful seam the scan runner (task 12) and the
  coordinator's completion boundary (task 13) drive. It reports **incrementally**
  as findings are discovered (wired onto
  :attr:`strix.report.state.ReportState.vulnerability_found_callback`) and
  **flushes any stragglers on root completion** via
  :meth:`BelluReporter.flush_on_completion`.

Tenant isolation (Requirement 6.1 / Property 1): nothing here sends an org. The
service-account bearer token identifies the principal and the control plane
resolves the org from it server-side.
"""

from __future__ import annotations

from strix.tools.finding_report.tool import (
    BelluReporter,
    StrixFinding,
    build_report_payload,
    report_findings,
    send_report,
    strix_report_to_finding,
)


__all__ = [
    "BelluReporter",
    "StrixFinding",
    "build_report_payload",
    "report_findings",
    "send_report",
    "strix_report_to_finding",
]
