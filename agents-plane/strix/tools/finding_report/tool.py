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

# The control-plane report write-back target (task 20's
# ``POST /api/agent/findings/reports``). This is the FULL-artifact companion to
# the thin ``findings:ingest`` seam: the agent's actual generated report (long-
# form sections + the plane's rendered markdown) flows back here so the Finding
# Detail view can render it. The verb/path are kept in one dict so wiring stays
# a single edit.
REPORT_WRITE_BACK: dict[str, str] = {
    "verb": "POST",
    "path": "/api/agent/findings/reports",
}
DEFAULT_REPORT_TIMEOUT = 30.0

# HTTP boundary at/above which a response is treated as a failed write-back.
_HTTP_BAD_REQUEST = 400

# The weaponized exploit body. It is NEVER transported, stored, or served (Req
# 16.1 / Property 6): report payloads carry only a ``poc_available`` flag and the
# PoC *description*. Named here so the strip is a single source of truth shared
# by the payload builder and the markdown renderer.
POC_SCRIPT_KEY = "poc_script_code"


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


def _strip_poc_script(value: Any) -> Any:
    """Return ``value`` with every ``poc_script_code`` key removed, recursively.

    The weaponized PoC body can be nested anywhere a report dict is embedded
    (top level, inside ``code_locations`` entries, inside an update-history
    revision), so the strip walks dicts and lists at every depth. The result is
    a fresh structure; the input is never mutated (Req 16.1 / Property 6).
    """
    if isinstance(value, dict):
        return {
            k: _strip_poc_script(v) for k, v in value.items() if k != POC_SCRIPT_KEY
        }
    if isinstance(value, list):
        return [_strip_poc_script(item) for item in value]
    return value


def _report_team(report: dict[str, Any]) -> str:
    """Return an explicit ``red``/``blue`` team only when the report states one.

    Req 15.3 asks the agents plane to PREFER sending the reporting agent's
    ``agent_name``/``stage`` and let the control plane derive the team (so the
    EXPLOIT->red, VERIFY/STABILITY/REPORT->blue mapping lives in one place). We
    therefore pass through an explicit ``team`` only when the report clearly
    carries a valid one, and otherwise leave it empty so the server derives it
    from the stage signal.
    """
    team = str(report.get("team") or "").strip().lower()
    return team if team in {"red", "blue"} else ""


def build_report_payload(report: dict[str, Any], run_id: str) -> dict[str, Any]:
    """Build the finding-report write-back body from a Strix vulnerability report.

    This is the FULL-artifact companion to :func:`_to_bellu` (the thin ingest
    mapping). It targets ``POST /api/agent/findings/reports`` (task 20), which
    accepts either a numeric ``finding_id`` or the ``(asset_id, vuln_id)`` dedup
    key the thin ingest already used — so the control plane resolves the finding
    without a round-trip. We reuse :func:`strix_report_to_finding` +
    :func:`_to_bellu` to compute that same key (``asset_id``+``asset_kind`` for a
    linked finding, ``raw_target`` for a raw one), keeping the two seams in
    agreement about how a finding is identified.

    Team (Req 15.2/15.3): an explicit ``red``/``blue`` on the report is passed
    through; otherwise we send the reporting agent's ``agent_name`` (and
    ``stage`` when present) and let the server derive the team from the stage
    (EXPLOIT->red; VERIFY/STABILITY/REPORT->blue). No team is guessed here.

    PoC redaction (Req 16.1 / Property 6): the weaponized ``poc_script_code``
    body is NEVER included. ``poc_available`` is a flag derived from the presence
    of a PoC description or script; ``sections`` is the long-form content with
    every ``poc_script_code`` key stripped at every nesting level; and
    ``rendered_markdown`` is produced from a PoC-stripped COPY of the report so
    the renderer (which would otherwise embed the script body under "Proof of
    Concept") emits only the PoC description and presence — never the body.
    """
    # Import here (not at module load) so the thin-ingest path never pulls in the
    # report writer and its pygments dependency.
    from strix.report.writer import render_vulnerability_md

    finding = strix_report_to_finding(report)

    payload: dict[str, Any] = {
        "run_id": run_id,
        "vuln_id": finding.vuln_id,
        "title": str(report.get("title") or ""),
        "severity": str(report.get("severity") or ""),
        # Flag only — presence of a description OR a script counts as "a PoC
        # exists", but the script body itself is never transported (Req 16.1).
        "poc_available": bool(
            report.get("poc_description") or report.get(POC_SCRIPT_KEY)
        ),
    }

    # Identify the finding by the SAME dedup key the thin ingest used, so the
    # control plane can resolve finding_id without a round-trip. Linked ->
    # asset_id+asset_kind; raw -> raw_target.
    if finding.is_linked:
        payload["asset_id"] = finding.graph_entity_id
        payload["asset_kind"] = finding.asset_kind
    else:
        payload["raw_target"] = finding.raw_target

    # Team: honor an explicit red/blue, else send the stage signal and let the
    # server derive it (Req 15.3).
    explicit_team = _report_team(report)
    if explicit_team:
        payload["team"] = explicit_team
    else:
        agent_name = str(report.get("agent_name") or "").strip()
        if agent_name:
            payload["agent_name"] = agent_name
        stage = str(report.get("stage") or "").strip()
        if stage:
            payload["stage"] = stage

    # Long-form sections: everything on the report EXCEPT the weaponized body,
    # stripped at every nesting level (Req 16.1/16.3). The server strips again
    # defensively (Req 16.2), so this is belt-and-suspenders.
    payload["sections"] = _strip_poc_script(report)

    # Rendered artifact: render from a PoC-stripped copy so the "Proof of
    # Concept" section carries only the description + presence, never the script
    # body (Req 16.1 / Property 6). render_vulnerability_md embeds poc_script_code
    # as a fenced block when present, which is exactly what must not leak.
    stripped_report = {k: v for k, v in report.items() if k != POC_SCRIPT_KEY}
    payload["rendered_markdown"] = render_vulnerability_md(stripped_report)

    return payload


def send_report(
    bellu_base: str,
    token: str,
    run_id: str,
    report: dict[str, Any],
    *,
    client: httpx.Client | None = None,
    timeout: float = DEFAULT_REPORT_TIMEOUT,
) -> None:
    """POST one report artifact to ``/api/agent/findings/reports``, best-effort.

    Builds the payload via :func:`build_report_payload` and POSTs it with the
    service-account bearer token. This is a best-effort write-back and NEVER
    raises: the full-artifact report is a complement to the thin
    ``findings:ingest`` seam, so a failed (or unreachable) report post must never
    fail the run it describes. Any transport/auth/build error is logged and
    swallowed (Req 15.1, mirroring the status/trace write-backs).

    No org is ever sent (Requirement 19.1): the service-account token identifies
    the principal and the control plane resolves the org from it.
    """
    import httpx

    try:
        payload = build_report_payload(report, run_id)
    except Exception:
        logger.exception("failed to build report write-back payload; skipping")
        return

    url = f"{bellu_base.rstrip('/')}{REPORT_WRITE_BACK['path']}"
    headers = {"Authorization": f"Bearer {token}"}

    owns_client = client is None
    http = client or httpx.Client(timeout=timeout)
    try:
        resp = http.request(
            REPORT_WRITE_BACK["verb"],
            url,
            json=payload,
            headers=headers,
            timeout=timeout,
        )
        if resp.status_code >= _HTTP_BAD_REQUEST:
            logger.warning(
                "report write-back for %s returned HTTP %s",
                payload.get("vuln_id"),
                resp.status_code,
            )
    except Exception:
        # Best-effort: a report post must never fail the run it describes.
        logger.exception("report write-back failed; the thin ingest still stands")
    finally:
        if owns_client:
            http.close()


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
        # Raw Strix report dicts seen via the incremental callback, keyed by
        # vuln_id, so the FULL-artifact report write-back (sections + rendered
        # markdown) can be sent alongside the thin ingest and reconciled on
        # completion. vuln_ids whose report artifact the control plane has
        # accepted, so the completion flush skips them.
        self._report_payloads: dict[str, dict[str, Any]] = {}
        self._reports_sent: set[str] = set()

    def attach(self, report_state: Any) -> None:
        """Wire incremental reporting onto a ReportState's found-callback.

        ``report_state`` is a :class:`strix.report.state.ReportState`; it is
        typed ``Any`` to avoid importing the report package (and its heavy
        transitive deps) at module load. The callback fires inside
        ``add_vulnerability_report`` for every finding the agent tree files.
        """
        report_state.vulnerability_found_callback = self._on_vulnerability_found

    def _on_vulnerability_found(self, report: dict[str, Any]) -> None:
        """Incremental hook: map and report a single just-filed finding.

        Two write-backs flow from each filed finding: the thin ``findings:ingest``
        (via :meth:`report_batch`) and the FULL-artifact report (sections +
        rendered markdown) via :meth:`_send_report`. The artifact send is
        best-effort and never raises, so a failed report post cannot sink the
        incremental ingest path.
        """
        finding = strix_report_to_finding(report)
        self._pending[finding.vuln_id] = finding
        # Keep the raw report so the completion flush can reconcile the artifact.
        self._report_payloads[finding.vuln_id] = report
        self.report_batch([finding])
        self._send_report(finding.vuln_id, report)

    def _send_report(self, vuln_id: str, report: dict[str, Any]) -> None:
        """Send the full report artifact for ``vuln_id``, best-effort.

        Delegates to :func:`send_report`, which strips the weaponized PoC body
        (Req 16.1) and never raises. On a successful attempt the ``vuln_id`` is
        remembered so :meth:`flush_on_completion` does not re-send the artifact.
        """
        send_report(
            self._bellu_base,
            self._token,
            self._run_id,
            report,
            client=self._client,
        )
        self._reports_sent.add(vuln_id)

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
        # Reconcile the FULL-artifact report for any finding whose raw report we
        # captured incrementally but have not yet sent the artifact for. This is
        # best-effort and never raises, so it cannot affect the thin-ingest flush
        # below or the run's outcome (Req 15.1).
        for vuln_id, report in self._report_payloads.items():
            if vuln_id not in self._reports_sent:
                self._send_report(vuln_id, report)

        outstanding = [
            f for vuln_id, f in self._pending.items() if vuln_id not in self._reported
        ]
        if not outstanding:
            return {"created": 0, "updated": 0, "rejected": 0}
        logger.info("flushing %d finding(s) to Bellu on completion", len(outstanding))
        return self.report_batch(outstanding)
