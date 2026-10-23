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

    # The incremental hook fired exactly one POST for the just-filed finding.
    assert len(seen) == 1
    body = json.loads(seen[0].content.decode())
    assert body["run_id"] == RUN_ID
    assert body["findings"][0]["raw_target"] == "https://t.example.com"


def test_reporter_flush_on_completion_skips_already_reported() -> None:
    seen, client = _capture()
    reporter = BelluReporter(BASE, TOKEN, RUN_ID, client=client)
    state = _FakeReportState()
    reporter.attach(state)

    state.file({"id": "vuln-0001", "severity": "high", "target": "https://t.example.com"})
    assert len(seen) == 1

    # Everything filed was already reported incrementally, so the completion
    # flush is a no-op with no additional POST.
    result = reporter.flush_on_completion()
    assert result == {"created": 0, "updated": 0, "rejected": 0}
    assert len(seen) == 1


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
