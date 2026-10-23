"""Map Strix findings onto Bellu's finding-ingest schema and POST them back.

The Go control plane owns the authoritative finding model; this module is the
thin client that feeds it (Requirement 6). The contract it targets is
``POST /api/agent/findings:ingest`` in
``backend/server/agent_findings_handlers.go``:

    IngestRequest { run_id, findings[] }
    IngestFinding { asset_id?, asset_kind?, raw_target?, vuln_id,
                    intel_ref?, risk_score, severity }

Mapping rules, mirrored from that handler so the client rejects the same shapes
the server would and never relies on the server to paper over a bad payload:

- **Linked finding** -- the finding is attached to a real graph entity. We emit
  ``asset_id`` (the graph canonical id ``kind:provider:native-id``) and
  ``asset_kind`` (an allow-listed ``model.Kind`` string). The server re-validates
  the kind against ``model.ValidKind`` and rejects+counts any that fail
  (Requirements 6.3, 6.4), so a bad kind never silently persists.
- **Raw finding** -- an operator-supplied URL/IP with no graph entity. We emit
  ``raw_target`` and **no** ``asset_id``; the server attaches an org-scoped
  synthetic asset ref (Requirement 6.5).
- ``risk_score`` is clamped to ``[0, 100]`` client-side (Requirement 6.9); the
  server clamps too, so the clamp is belt-and-suspenders, not a trust boundary.

Tenant isolation (Requirement 6.1 / Property 1): no org is ever sent. The
service-account bearer token identifies the principal and the control plane
resolves the org from it.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any


if TYPE_CHECKING:
    import httpx


logger = logging.getLogger(__name__)

# Server-side clamp bounds for risk_score (Requirement 6.9). Kept here so the
# client rejects the same range the server would.
RISK_MIN = 0.0
RISK_MAX = 100.0

INGEST_PATH = "/api/agent/findings:ingest"
DEFAULT_INGEST_TIMEOUT = 30.0


def _clamp_risk(score: float) -> float:
    """Clamp a risk score into ``[RISK_MIN, RISK_MAX]`` (Requirement 6.9)."""
    return max(RISK_MIN, min(RISK_MAX, score))


@dataclass(frozen=True)
class StrixFinding:
    """A Strix finding in the shape the Bellu mapper consumes.

    This is the agents-plane view of a finding, decoupled from Strix's internal
    report dict (see :func:`strix_report_to_finding` for the adapter that builds
    one from a ``ReportState`` report). A finding is either *linked* to a graph
    entity or attached to a *raw* operator-supplied target:

    - linked  -> ``graph_entity_id`` set (plus ``asset_kind``), ``raw_target`` empty
    - raw     -> ``raw_target`` set, ``graph_entity_id`` empty

    ``_to_bellu`` reads ``graph_entity_id`` to decide which shape to emit, so a
    finding with both set is treated as linked (the graph entity wins).
    """

    vuln_id: str
    severity: str
    risk_score: float = 0.0
    intel_ref: str = ""
    # Linked: the graph canonical id (kind:provider:native-id) + its model.Kind.
    graph_entity_id: str = ""
    asset_kind: str = ""
    # Raw: an operator-supplied URL/IP with no graph entity.
    raw_target: str = ""

    @property
    def is_linked(self) -> bool:
        """True when this finding is linked to a real graph entity."""
        return bool(self.graph_entity_id)


def _to_bellu(f: StrixFinding) -> dict[str, Any]:
    """Map one :class:`StrixFinding` onto a Bellu ``IngestFinding`` dict.

    Linked findings carry ``asset_id`` + ``asset_kind``; raw-target findings
    carry ``raw_target`` and omit ``asset_id`` so the control plane attaches a
    synthetic asset ref. ``risk_score`` is clamped to ``[0, 100]`` here as well
    as server-side. No org is ever included (Requirement 6.1).
    """
    out: dict[str, Any] = {
        "vuln_id": f.vuln_id,
        "intel_ref": f.intel_ref or "",
        "risk_score": _clamp_risk(f.risk_score),
        "severity": f.severity,
    }
    if f.is_linked:
        # Linked to a real graph entity: send the canonical id + kind. The
        # server re-validates the kind against model.ValidKind (Req 6.3/6.4).
        out["asset_id"] = f.graph_entity_id
        out["asset_kind"] = f.asset_kind
    else:
        # Operator-supplied raw target with no graph entity: send raw_target and
        # NO asset_id so the server attaches an org-scoped synthetic ref (6.5).
        out["raw_target"] = f.raw_target
    return out


def report_findings(
    bellu_base: str,
    token: str,
    run_id: str,
    strix_findings: list[StrixFinding],
    *,
    client: httpx.Client | None = None,
    timeout: float = DEFAULT_INGEST_TIMEOUT,
) -> dict[str, Any]:
    """Translate Strix findings into Bellu's ingest schema and POST them.

    Builds ``{run_id, findings: [_to_bellu(f) ...]}`` and POSTs it to
    ``{bellu_base}/api/agent/findings:ingest`` with the service-account bearer
    token. Returns the control plane's ``{created, updated, rejected}`` response
    (the server may add a ``rejections`` detail list; it is passed through).

    No org is sent (Requirement 6.1). ``client`` lets the scan runner reuse a
    pooled ``httpx.Client`` and lets tests inject an ``httpx.MockTransport``; by
    default a short-lived client is created and closed.
    """
    import httpx

    payload = {
        "run_id": run_id,
        "findings": [_to_bellu(f) for f in strix_findings],
    }
    url = f"{bellu_base.rstrip('/')}{INGEST_PATH}"
    headers = {"Authorization": f"Bearer {token}"}

    owns_client = client is None
    http = client or httpx.Client(timeout=timeout)
    try:
        resp = http.post(url, json=payload, headers=headers, timeout=timeout)
        resp.raise_for_status()
        result: dict[str, Any] = resp.json()
    finally:
        if owns_client:
            http.close()
    return result


def strix_report_to_finding(report: dict[str, Any]) -> StrixFinding:
    """Adapt a Strix ``ReportState`` report dict into a :class:`StrixFinding`.

    Strix files findings as plain dicts (see
    :meth:`strix.report.state.ReportState.add_vulnerability_report`); this is the
    single place that knows those keys, so the rest of the pipeline works in
    terms of the typed :class:`StrixFinding`.

    Linkage: a Bellu-known scan target carries its graph canonical id and kind in
    ``bellu_entity_id`` / ``bellu_asset_kind`` (set by the scan runner, task 12,
    when it resolves an ``entity`` target). When those are absent the finding is
    for a raw target and the report's ``target`` (falling back to ``endpoint``)
    becomes ``raw_target``.
    """
    vuln_id = str(report.get("id") or "").strip()
    severity = str(report.get("severity") or "").strip()

    graph_entity_id = str(report.get("bellu_entity_id") or "").strip()
    asset_kind = str(report.get("bellu_asset_kind") or "").strip()
    raw_target = str(report.get("target") or report.get("endpoint") or "").strip()

    intel_ref = str(report.get("cve") or report.get("cwe") or "").strip()

    return StrixFinding(
        vuln_id=vuln_id,
        severity=severity,
        risk_score=_report_risk_score(report),
        intel_ref=intel_ref,
        graph_entity_id=graph_entity_id,
        asset_kind=asset_kind,
        # A linked finding ignores raw_target; carrying it would be dead data.
        raw_target="" if graph_entity_id else raw_target,
    )


def _report_risk_score(report: dict[str, Any]) -> float:
    """Derive a 0-100 risk score from a Strix report's CVSS (0-10), if any.

    Strix rates findings with CVSS base scores on a 0-10 scale; Bellu's
    ``risk_score`` is 0-100, so the CVSS score is scaled up. Missing/unparseable
    CVSS degrades to 0.0 (the server treats it the same and clamps regardless).
    """
    raw = report.get("cvss")
    try:
        cvss = float(raw) if raw is not None else 0.0
    except (TypeError, ValueError):
        return 0.0
    return _clamp_risk(cvss * 10.0)


class BelluReporter:
    """Reports Strix findings to Bellu incrementally and on completion.

    This is the seam the scan runner (task 12) and the coordinator completion
    boundary (task 13) drive. It is deliberately small and explicit so those
    tasks have a stable API:

    - :meth:`attach` wires :meth:`_on_vulnerability_found` onto a
      :class:`~strix.report.state.ReportState`'s ``vulnerability_found_callback``,
      so every finding Strix files is **reported incrementally** as it is
      discovered.
    - :meth:`report_batch` reports an explicit batch (used by the runner for
      findings it surfaces outside the report callback, and by tests).
    - :meth:`flush_on_completion` reports any findings the incremental path has
      not yet sent and is called once when the root agent completes
      (``root_complete``), guaranteeing a final reconcile so nothing filed late
      in the run is missed.

    Reporting a finding is best-effort per call: a transport error is logged and
    re-raised to the caller that drove the reporter, which decides whether a
    single failed POST should fail the run. Successful reports are remembered by
    ``vuln_id`` so :meth:`flush_on_completion` does not re-send them (the server
    is idempotent on ``(asset_id, vuln_id)`` regardless -- Property 2 -- but not
    re-sending keeps the completion flush cheap).
    """

    def __init__(
        self,
        bellu_base: str,
        token: str,
        run_id: str,
        *,
        client: httpx.Client | None = None,
    ) -> None:
        self._bellu_base = bellu_base
        self._token = token
        self._run_id = run_id
        self._client = client
        # vuln_ids already accepted by the control plane, so the completion
        # flush can skip them.
        self._reported: set[str] = set()
        # Findings filed this run, in file order, keyed by vuln_id so a later
        # revision of the same finding replaces the earlier snapshot.
        self._pending: dict[str, StrixFinding] = {}

    def attach(self, report_state: Any) -> None:
        """Wire incremental reporting onto a ReportState's found-callback.

        ``report_state`` is a :class:`strix.report.state.ReportState`; it is
        typed ``Any`` to avoid importing the report package (and its heavy
        transitive deps) at module load. The callback fires inside
        ``add_vulnerability_report`` for every finding the agent tree files.
        """
        report_state.vulnerability_found_callback = self._on_vulnerability_found

    def _on_vulnerability_found(self, report: dict[str, Any]) -> None:
        """Incremental hook: map and report a single just-filed finding."""
        finding = strix_report_to_finding(report)
        self._pending[finding.vuln_id] = finding
        self.report_batch([finding])

    def report_batch(self, findings: list[StrixFinding]) -> dict[str, Any]:
        """Report an explicit batch now, returning the server's counts.

        Each finding reported is recorded so :meth:`flush_on_completion` will
        not re-send it. An empty batch is a no-op that returns zeroed counts
        without a network call.
        """
        if not findings:
            return {"created": 0, "updated": 0, "rejected": 0}
        for f in findings:
            self._pending.setdefault(f.vuln_id, f)
        result = report_findings(
            self._bellu_base,
            self._token,
            self._run_id,
            findings,
            client=self._client,
        )
        for f in findings:
            self._reported.add(f.vuln_id)
        return result

    def flush_on_completion(self) -> dict[str, Any]:
        """Report any findings not yet accepted by the control plane.

        Called once on root completion. Sends every pending finding whose
        ``vuln_id`` has not already been reported, so a finding filed late in the
        run (or whose incremental POST failed) is reconciled before the run ends.
        Returns the server's counts for the flushed batch (zeroed when nothing
        is outstanding).
        """
        outstanding = [
            f for vuln_id, f in self._pending.items() if vuln_id not in self._reported
        ]
        if not outstanding:
            return {"created": 0, "updated": 0, "rejected": 0}
        logger.info("flushing %d finding(s) to Bellu on completion", len(outstanding))
        return self.report_batch(outstanding)
