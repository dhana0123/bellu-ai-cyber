"""Tests for the ``finding_report`` tool (task 11.1).

Covers the client side of the findings write-back seam (Requirement 6):

- ``_to_bellu`` linked mapping: ``asset_id`` + ``asset_kind`` present, no
  ``raw_target`` (Requirement 6.3).
- ``_to_bellu`` raw mapping: ``raw_target`` present, no ``asset_id``
  (Requirement 6.5).
- ``risk_score`` clamp to ``[0, 100]`` at both ends (Requirement 6.9).
- ``report_findings`` POST: payload shape (``run_id`` + mapped ``findings``),
  the service-account bearer header, the ``findings:ingest`` URL, and that **no
  org** is sent anywhere (Requirement 6.1 / Property 1). The HTTP call is mocked
  with ``httpx.MockTransport``.
- ``BelluReporter`` incremental + on-completion wiring: the ReportState
  found-callback reports a finding as it is filed, and ``flush_on_completion``
  does not re-send an already-reported finding.
"""

from __future__ import annotations

import json
from typing import Any

import httpx

from strix.tools.finding_report import tool
from strix.tools.finding_report.tool import (
    BelluReporter,
    StrixFinding,
    report_findings,
)


BASE = "https://bellu.example.com"
TOKEN = "sa-token-xyz"  # test fixture token
RUN_ID = "run-abc123"


def _linked() -> StrixFinding:
    return StrixFinding(
        vuln_id="vuln-0001",
        severity="high",
        risk_score=42.0,
        intel_ref="CVE-2024-1234",
        graph_entity_id="Compute:aws:i-0abc",
        asset_kind="Compute",
    )


def _raw() -> StrixFinding:
    return StrixFinding(
        vuln_id="vuln-0002",
        severity="medium",
        risk_score=10.0,
        intel_ref="",
        raw_target="https://scanme.example.com",
    )


# --- _to_bellu mapping -------------------------------------------------------


def test_to_bellu_linked_emits_asset_id_and_kind_no_raw_target() -> None:
    out = tool._to_bellu(_linked())

    assert out["asset_id"] == "Compute:aws:i-0abc"
    assert out["asset_kind"] == "Compute"
    assert "raw_target" not in out
    assert out["vuln_id"] == "vuln-0001"
    assert out["intel_ref"] == "CVE-2024-1234"
    assert out["severity"] == "high"


def test_to_bellu_raw_emits_raw_target_no_asset_id() -> None:
    out = tool._to_bellu(_raw())

    assert out["raw_target"] == "https://scanme.example.com"
    assert "asset_id" not in out
    assert "asset_kind" not in out
    assert out["vuln_id"] == "vuln-0002"
    # Absent intel_ref maps to an empty string, never omitted.
    assert out["intel_ref"] == ""


def test_to_bellu_graph_entity_wins_over_raw_target() -> None:
    both = StrixFinding(
        vuln_id="v",
        severity="low",
        graph_entity_id="Identity:aws:role/x",
        asset_kind="Identity",
        raw_target="https://ignored.example.com",
    )
    out = tool._to_bellu(both)

    assert out["asset_id"] == "Identity:aws:role/x"
    assert "raw_target" not in out


# --- risk_score clamp (both ends) -------------------------------------------


def test_to_bellu_clamps_risk_above_max() -> None:
    f = StrixFinding(vuln_id="v", severity="critical", risk_score=9999.0, raw_target="t")
    assert tool._to_bellu(f)["risk_score"] == tool.RISK_MAX


def test_to_bellu_clamps_risk_below_min() -> None:
    f = StrixFinding(vuln_id="v", severity="info", risk_score=-5.0, raw_target="t")
    assert tool._to_bellu(f)["risk_score"] == tool.RISK_MIN


def test_to_bellu_keeps_in_range_risk() -> None:
    f = StrixFinding(vuln_id="v", severity="info", risk_score=73.5, raw_target="t")
    assert tool._to_bellu(f)["risk_score"] == 73.5


# --- report_findings POST: payload / header / url / no org ------------------


def _capture() -> tuple[list[httpx.Request], httpx.Client]:
    """A MockTransport httpx.Client that records every request it serves."""
    seen: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(
            200,
            json={"created": 1, "updated": 1, "rejected": 0, "rejections": []},
        )

    client = httpx.Client(transport=httpx.MockTransport(handler))
    return seen, client


def test_report_findings_posts_expected_payload_and_bearer_no_org() -> None:
    seen, client = _capture()

    result = report_findings(BASE, TOKEN, RUN_ID, [_linked(), _raw()], client=client)

    assert result == {"created": 1, "updated": 1, "rejected": 0, "rejections": []}
    assert len(seen) == 1
    req = seen[0]

    # URL is the findings:ingest seam under the configured base.
    assert str(req.url) == f"{BASE}/api/agent/findings:ingest"
    assert req.method == "POST"

    # Service-account bearer token is sent.
    assert req.headers["Authorization"] == f"Bearer {TOKEN}"

    body = json.loads(req.content.decode())
    assert body["run_id"] == RUN_ID
    assert isinstance(body["findings"], list)
    assert len(body["findings"]) == 2
    # First is the linked mapping, second the raw mapping.
    assert body["findings"][0]["asset_id"] == "Compute:aws:i-0abc"
    assert body["findings"][1]["raw_target"] == "https://scanme.example.com"

    # No org anywhere: not in the body, not in the query string, not a header.
    assert "org" not in body
    for f in body["findings"]:
        assert "org" not in f
    assert "org" not in req.url.params
    assert not any(k.lower() == "org" for k in req.headers)


def test_report_findings_empty_batch_still_posts_empty_findings() -> None:
    seen, client = _capture()

    report_findings(BASE, TOKEN, RUN_ID, [], client=client)

    body = json.loads(seen[0].content.decode())
    assert body == {"run_id": RUN_ID, "findings": []}


# --- strix_report_to_finding adapter ----------------------------------------


def test_report_adapter_linked_from_bellu_entity_fields() -> None:
    report: dict[str, Any] = {
        "id": "vuln-0007",
        "severity": "high",
        "cvss": 7.5,
        "cve": "CVE-2023-9",
        "bellu_entity_id": "DataStore:aws:bucket/x",
        "bellu_asset_kind": "DataStore",
        "target": "https://should-be-ignored.example.com",
    }
    f = tool.strix_report_to_finding(report)

    assert f.is_linked
    assert f.graph_entity_id == "DataStore:aws:bucket/x"
    assert f.asset_kind == "DataStore"
    assert f.raw_target == ""  # linked finding drops raw_target
    assert f.risk_score == 75.0  # cvss 7.5 (0-10) scaled to 0-100
    assert f.intel_ref == "CVE-2023-9"


def test_report_adapter_raw_from_target() -> None:
    report: dict[str, Any] = {
        "id": "vuln-0008",
        "severity": "medium",
        "target": "https://scanme.example.com",
    }
    f = tool.strix_report_to_finding(report)

    assert not f.is_linked
    assert f.raw_target == "https://scanme.example.com"
    assert f.risk_score == 0.0  # no cvss -> 0


# --- BelluReporter incremental + completion wiring --------------------------


class _FakeReportState:
    """Stands in for strix.report.state.ReportState's callback seam."""

    def __init__(self) -> None:
        self.vulnerability_found_callback: Any = None

    def file(self, report: dict[str, Any]) -> None:
        if self.vulnerability_found_callback is not None:
            self.vulnerability_found_callback(report)


def test_reporter_reports_incrementally_on_found_callback() -> None:
    seen, client = _capture()
    reporter = BelluReporter(BASE, TOKEN, RUN_ID, client=client)
    state = _FakeReportState()
    reporter.attach(state)

    state.file({"id": "vuln-0001", "severity": "high", "target": "https://t.example.com"})

    # The incremental hook fires the thin findings:ingest POST for the just-filed
    # finding (it also fires the full report artifact on a separate path; that is
    # asserted in test_reporter_sends_report_artifact_on_found_callback).
    ingest = [r for r in seen if r.url.path == "/api/agent/findings:ingest"]
    assert len(ingest) == 1
    body = json.loads(ingest[0].content.decode())
    assert body["run_id"] == RUN_ID
    assert body["findings"][0]["raw_target"] == "https://t.example.com"


def test_reporter_flush_on_completion_skips_already_reported() -> None:
    seen, client = _capture()
    reporter = BelluReporter(BASE, TOKEN, RUN_ID, client=client)
    state = _FakeReportState()
    reporter.attach(state)

    state.file({"id": "vuln-0001", "severity": "high", "target": "https://t.example.com"})
    # One thin-ingest POST was made incrementally for this finding.
    ingest = [r for r in seen if r.url.path == "/api/agent/findings:ingest"]
    assert len(ingest) == 1
    sent_before = len(seen)

    # Everything filed was already reported incrementally (both the thin ingest
    # and the full report artifact), so the completion flush is a no-op with no
    # additional POST on either seam.
    result = reporter.flush_on_completion()
    assert result == {"created": 0, "updated": 0, "rejected": 0}
    assert len(seen) == sent_before


def test_reporter_flush_on_completion_sends_outstanding() -> None:
    seen, client = _capture()
    reporter = BelluReporter(BASE, TOKEN, RUN_ID, client=client)

    # Stage a finding without going through the incremental POST path.
    reporter._pending["vuln-0009"] = StrixFinding(
        vuln_id="vuln-0009", severity="low", raw_target="https://late.example.com"
    )

    result = reporter.flush_on_completion()
    assert result["created"] == 1
    assert len(seen) == 1
    body = json.loads(seen[0].content.decode())
    assert body["findings"][0]["raw_target"] == "https://late.example.com"


# --- build_report_payload: team/blue mapping, PoC strip (task 21) -----------


_WEAPONIZED = "import os; os.system('rm -rf /'); exfiltrate(secrets)"


def _red_report() -> dict[str, Any]:
    """A linked EXPLOIT-stage report carrying a weaponized PoC body."""
    return {
        "id": "vuln-0100",
        "title": "SQL injection in login",
        "severity": "critical",
        "cvss": 9.1,
        "cve": "CVE-2024-5555",
        "bellu_entity_id": "Compute:aws:i-0red",
        "bellu_asset_kind": "Compute",
        "agent_name": "exploit-agent",
        "description": "User input reaches the query unescaped.",
        "impact": "Full database read.",
        "technical_analysis": "The parameter is concatenated into SQL.",
        "poc_description": "Send ' OR 1=1-- in the username field.",
        "poc_script_code": _WEAPONIZED,
        "remediation_steps": "Use parameterized queries.",
        "evidence": "Returned all rows.",
        "confidence": "high",
        "fix_effort": "low",
        "code_locations": [
            {"file": "login.py", "start_line": 12, "snippet": "q = 'SELECT ...'"},
        ],
    }


def _blue_report() -> dict[str, Any]:
    """A raw-target REPORT-stage report with only a PoC description (no body)."""
    return {
        "id": "vuln-0101",
        "title": "Missing security headers",
        "severity": "low",
        "target": "https://scanme.example.com",
        "agent_name": "report-writer",
        "description": "No CSP header present.",
        "remediation_steps": "Add a Content-Security-Policy header.",
        "poc_description": "Observe the absent header in the response.",
    }


def _contains(obj: Any, needle: str) -> bool:
    """True when ``needle`` appears anywhere in the JSON serialization of obj."""
    return needle in json.dumps(obj, default=str)


def test_build_report_payload_red_maps_identity_and_sets_poc_available() -> None:
    payload = tool.build_report_payload(_red_report(), RUN_ID)

    # Identified by the SAME dedup key the thin ingest uses (asset_id+kind).
    assert payload["asset_id"] == "Compute:aws:i-0red"
    assert payload["asset_kind"] == "Compute"
    assert payload["vuln_id"] == "vuln-0100"
    assert payload["run_id"] == RUN_ID
    assert payload["title"] == "SQL injection in login"
    assert payload["severity"] == "critical"
    # A PoC exists (description + script), so the flag is set — flag only.
    assert payload["poc_available"] is True
    # Stage signal sent so the server derives the team (prefer server-side).
    assert payload["agent_name"] == "exploit-agent"
    assert "team" not in payload


def test_build_report_payload_blue_maps_raw_target_and_poc_available() -> None:
    payload = tool.build_report_payload(_blue_report(), RUN_ID)

    assert payload["raw_target"] == "https://scanme.example.com"
    assert "asset_id" not in payload
    assert payload["vuln_id"] == "vuln-0101"
    assert payload["agent_name"] == "report-writer"
    # A PoC description (no script) still counts as "a PoC exists".
    assert payload["poc_available"] is True


def test_build_report_payload_explicit_team_is_passed_through() -> None:
    report = {**_red_report(), "team": "red"}
    payload = tool.build_report_payload(report, RUN_ID)

    assert payload["team"] == "red"
    # An explicit team short-circuits the stage signal.
    assert "agent_name" not in payload


def test_build_report_payload_no_poc_sets_flag_false() -> None:
    report = {"id": "vuln-0102", "title": "Info", "severity": "info", "target": "t"}
    payload = tool.build_report_payload(report, RUN_ID)

    assert payload["poc_available"] is False


def test_build_report_payload_never_includes_poc_script_code() -> None:
    payload = tool.build_report_payload(_red_report(), RUN_ID)

    # Not at the top level, not in sections, not in the rendered markdown, and
    # the weaponized body string appears nowhere in the serialized payload (P6).
    assert "poc_script_code" not in payload
    assert "poc_script_code" not in payload["sections"]
    assert _WEAPONIZED not in payload["rendered_markdown"]
    assert not _contains(payload, _WEAPONIZED)
    assert not _contains(payload, "poc_script_code")
    # The PoC *description* and presence flag survive — only the body is gone.
    assert payload["poc_available"] is True
    assert "' OR 1=1--" in payload["rendered_markdown"]


def test_build_report_payload_strips_nested_poc_script_code() -> None:
    report = {
        "id": "vuln-0103",
        "title": "Nested",
        "severity": "high",
        "target": "t",
        "code_locations": [
            {"file": "a.py", "poc_script_code": _WEAPONIZED},
        ],
        "extra": {"deep": {"poc_script_code": _WEAPONIZED}},
    }
    payload = tool.build_report_payload(report, RUN_ID)

    # The weaponized body value is gone everywhere (Req 16.1 / Property 6),
    # including inside list entries and arbitrarily deep nested dicts.
    assert not _contains(payload, _WEAPONIZED)
    # The poc_script_code KEY is removed at every nesting level in sections.
    sections = payload["sections"]
    assert "poc_script_code" not in sections["code_locations"][0]
    assert "poc_script_code" not in sections["extra"]["deep"]
    # Siblings around the stripped key are preserved.
    assert sections["code_locations"][0]["file"] == "a.py"


# --- send_report: best-effort write-back ------------------------------------


def test_send_report_posts_payload_with_bearer_no_org() -> None:
    seen, client = _capture()

    tool.send_report(BASE, TOKEN, RUN_ID, _red_report(), client=client)

    assert len(seen) == 1
    req = seen[0]
    assert str(req.url) == f"{BASE}/api/agent/findings/reports"
    assert req.method == "POST"
    assert req.headers["Authorization"] == f"Bearer {TOKEN}"

    body = json.loads(req.content.decode())
    assert body["run_id"] == RUN_ID
    assert body["asset_id"] == "Compute:aws:i-0red"
    # No org anywhere, and no weaponized body on the wire (P6).
    assert "org" not in body
    assert not _contains(body, _WEAPONIZED)
    assert "poc_script_code" not in req.content.decode()


def test_send_report_swallows_transport_error() -> None:
    def boom(_request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("control plane unreachable")

    client = httpx.Client(transport=httpx.MockTransport(boom))

    # Best-effort: a transport error must never propagate out of the sender.
    tool.send_report(BASE, TOKEN, RUN_ID, _red_report(), client=client)


def test_send_report_swallows_http_error_status() -> None:
    def server_error(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "temporarily unavailable"})

    client = httpx.Client(transport=httpx.MockTransport(server_error))

    # A 5xx is logged and swallowed, never raised.
    tool.send_report(BASE, TOKEN, RUN_ID, _blue_report(), client=client)


# --- BelluReporter: report artifact flows alongside thin ingest -------------


def test_reporter_sends_report_artifact_on_found_callback() -> None:
    seen, client = _capture()
    reporter = BelluReporter(BASE, TOKEN, RUN_ID, client=client)
    state = _FakeReportState()
    reporter.attach(state)

    state.file(_red_report())

    # Two POSTs: the thin findings:ingest AND the full report artifact.
    paths = sorted(req.url.path for req in seen)
    assert "/api/agent/findings:ingest" in paths
    assert "/api/agent/findings/reports" in paths

    report_req = next(r for r in seen if r.url.path == "/api/agent/findings/reports")
    body = report_req.content.decode()
    # The weaponized body never reaches the wire on the report seam (P6).
    assert _WEAPONIZED not in body
    assert "poc_script_code" not in body


def test_reporter_report_send_error_does_not_break_incremental() -> None:
    # The report seam fails; the thin ingest seam succeeds. The found-callback
    # must not raise (best-effort report write-back, Req 15.1).
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/agent/findings/reports":
            raise httpx.ConnectError("report seam down")
        return httpx.Response(200, json={"created": 1, "updated": 0, "rejected": 0})

    client = httpx.Client(transport=httpx.MockTransport(handler))
    reporter = BelluReporter(BASE, TOKEN, RUN_ID, client=client)
    state = _FakeReportState()
    reporter.attach(state)

    state.file(_red_report())  # must not raise
