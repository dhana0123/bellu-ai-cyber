"""Tests for the Bellu Scan_Runner (task 12).

Drive :func:`strix.core.bellu_runner.run_bellu_scan` with the engine entry
point (``run_strix_scan``) and every HTTP call mocked via
``httpx.MockTransport`` / injected seams. Asserts the lifecycle the design and
Requirement 7.6 require:

- the bounded graph-context is fetched **before** the scan starts, and the
  rendered block is injected as ``root_instructions_override`` (Req 7.6, 10.2);
- the :class:`BelluReporter` is attached to the run's ``ReportState`` for
  incremental reporting and flushed on completion (task 11 / Req 6);
- the run reports status (``running`` then a terminal state);
- a raw target is encoded ``raw:<url>`` and an entity target
  ``entity:<canonical_id>`` for the fetch (matching the Go parser);
- no org is ever sent (Req 9.5);
- an engine failure reports ``failed`` and re-raises.
"""

from __future__ import annotations

from typing import Any

import httpx
import pytest

from strix.config.bellu import BelluSettings
from strix.core import bellu_runner
from strix.core.bellu_runner import RunDescriptor, RunStatus, run_bellu_scan


_TOKEN = "sa-token-xyz"  # fixture token, not a real secret
_BASE = "https://cp.example.com"


def _settings() -> BelluSettings:
    return BelluSettings(
        BELLU_API_BASE=_BASE,
        BELLU_SERVICE_ACCOUNT_TOKEN=_TOKEN,
    )


class _FakeReportState:
    """Minimal stand-in for ReportState: records the attached callback."""

    def __init__(self) -> None:
        self.vulnerability_found_callback: Any = None


def _graph_context_handler(seen: dict[str, Any]) -> Any:
    """A MockTransport handler that records the graph-context request."""

    def handler(request: httpx.Request) -> httpx.Response:
        seen["graph_context_called"] = True
        seen["target"] = request.url.params.get("target", "")
        seen["path"] = request.url.path
        seen["auth"] = request.headers.get("Authorization", "")
        # No org must ever be sent (Req 9.5).
        seen["has_org"] = "org" in request.url.params
        return httpx.Response(
            200,
            json={
                "target": {"kind": "entity", "canonical_id": "host:aws:i-0abc"},
                "seed": {
                    "canonical_id": "host:aws:i-0abc",
                    "kind": "host",
                    "name": "web-1",
                },
                "neighbors": [
                    {"canonical_id": "sg:aws:sg-1", "kind": "security_group", "name": "sg"},
                ],
                "edges": [
                    {"relation": "protected_by", "src": "host:aws:i-0abc", "dst": "sg:aws:sg-1"},
                ],
                "bounded": True,
                "graph": True,
                "truncated": False,
            },
        )

    return handler


@pytest.mark.asyncio
async def test_run_bellu_scan_fetches_context_before_scan_and_injects_override() -> None:
    seen: dict[str, Any] = {}
    client = httpx.Client(transport=httpx.MockTransport(_graph_context_handler(seen)))

    order: list[str] = []
    captured: dict[str, Any] = {}

    async def fake_run_scan(**kwargs: Any) -> str:
        order.append("scan_started")
        captured.update(kwargs)
        return "engine-result"

    # Mark when the context fetch happened relative to the scan start.
    real_fetch = bellu_runner.GraphContext.fetch

    def tracking_fetch(*args: Any, **kw: Any) -> Any:
        order.append("context_fetched")
        return real_fetch(*args, **kw)

    report_state = _FakeReportState()

    descriptor = RunDescriptor(
        run_id="run-1",
        target={"kind": "entity", "canonical_id": "host:aws:i-0abc"},
        instructions="investigate lateral movement",
    )

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner.GraphContext, "fetch", staticmethod(tracking_fetch))
        mp.setattr(bellu_runner, "get_global_report_state", lambda: report_state)

        result = await run_bellu_scan(
            descriptor,
            settings=_settings(),
            client=client,
            run_scan=fake_run_scan,
            image="bellu-sandbox:latest",
        )

    # Graph-context was fetched BEFORE the scan started (Requirement 7.6).
    assert order == ["context_fetched", "scan_started"]
    assert seen["graph_context_called"] is True
    assert seen["path"] == "/api/agent/graph-context"
    assert seen["target"] == "entity:host:aws:i-0abc"
    assert seen["auth"] == f"Bearer {_TOKEN}"
    assert seen["has_org"] is False  # no org ever sent (Req 9.5)

    # The fetched context was injected as root_instructions_override, and
    # operator instructions follow the graph block.
    override = captured["root_instructions_override"]
    assert "<bellu_graph_context>" in override
    assert "host:aws:i-0abc" in override
    assert "sg:aws:sg-1" in override
    assert "investigate lateral movement" in override
    assert override.index("<bellu_graph_context>") < override.index(
        "investigate lateral movement"
    )

    # The engine was driven with the descriptor's run id and image.
    assert captured["scan_id"] == "run-1"
    assert captured["image"] == "bellu-sandbox:latest"
    assert result == "engine-result"

    # The BelluReporter was attached to the ReportState for incremental
    # reporting (task 11).
    assert callable(report_state.vulnerability_found_callback)

    client.close()


@pytest.mark.asyncio
async def test_run_bellu_scan_reports_status_running_then_completed() -> None:
    seen: dict[str, Any] = {}
    client = httpx.Client(transport=httpx.MockTransport(_graph_context_handler(seen)))

    statuses: list[RunStatus] = []

    def record_status(descriptor: Any, status: RunStatus, *_a: Any, **_k: Any) -> None:
        statuses.append(status)

    async def fake_run_scan(**_kwargs: Any) -> None:
        return None

    descriptor = RunDescriptor(
        run_id="run-2",
        target={"kind": "raw", "raw": "https://app.example.com"},
    )

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner, "report_status", record_status)
        mp.setattr(bellu_runner, "get_global_report_state", lambda: None)

        await run_bellu_scan(
            descriptor,
            settings=_settings(),
            client=client,
            run_scan=fake_run_scan,
            image="img",
        )

    assert statuses == [RunStatus.RUNNING, RunStatus.COMPLETED]
    # A raw target is encoded with the raw scheme for the fetch.
    assert seen["target"] == "raw:https://app.example.com"

    client.close()


@pytest.mark.asyncio
async def test_run_bellu_scan_reports_failed_and_reraises_on_engine_error() -> None:
    seen: dict[str, Any] = {}
    client = httpx.Client(transport=httpx.MockTransport(_graph_context_handler(seen)))

    statuses: list[RunStatus] = []

    def record_status(descriptor: Any, status: RunStatus, *_a: Any, **_k: Any) -> None:
        statuses.append(status)

    async def boom(**_kwargs: Any) -> None:
        raise RuntimeError("engine exploded")

    descriptor = RunDescriptor(
        run_id="run-3",
        target={"kind": "entity", "canonical_id": "host:aws:i-0abc"},
    )

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner, "report_status", record_status)
        mp.setattr(bellu_runner, "get_global_report_state", lambda: None)

        with pytest.raises(RuntimeError, match="engine exploded"):
            await run_bellu_scan(
                descriptor,
                settings=_settings(),
                client=client,
                run_scan=boom,
                image="img",
            )

    assert statuses == [RunStatus.RUNNING, RunStatus.FAILED]

    client.close()


def test_descriptor_encodes_entity_and_raw_targets() -> None:
    entity = RunDescriptor(
        run_id="r", target={"kind": "entity", "canonical_id": "host:aws:i-1"}
    )
    assert entity.encoded_target() == "entity:host:aws:i-1"

    raw = RunDescriptor(run_id="r", target={"kind": "raw", "raw": "10.0.0.5"})
    assert raw.encoded_target() == "raw:10.0.0.5"

    bad = RunDescriptor(run_id="r", target={"kind": "nonsense"})
    with pytest.raises(ValueError, match="unknown target kind"):
        bad.encoded_target()


def test_build_scan_config_routes_raw_url_and_ip() -> None:
    url_cfg = bellu_runner._build_scan_config(
        RunDescriptor(run_id="r", target={"kind": "raw", "raw": "https://x.example"}),
    )
    assert url_cfg["targets"] == [
        {"type": "web_application", "details": {"target_url": "https://x.example"}},
    ]

    ip_cfg = bellu_runner._build_scan_config(
        RunDescriptor(run_id="r", target={"kind": "raw", "raw": "10.0.0.5"}),
    )
    assert ip_cfg["targets"] == [
        {"type": "ip_address", "details": {"target_ip": "10.0.0.5"}},
    ]

    # An entity target adds no engine target: the run is read-only graph
    # reasoning and the target lives in the injected graph-context block.
    entity_cfg = bellu_runner._build_scan_config(
        RunDescriptor(run_id="r", target={"kind": "entity", "canonical_id": "host:aws:i-1"}),
    )
    assert entity_cfg["targets"] == []


def test_report_status_never_raises_and_logs_write_back_target(
    caplog: pytest.LogCaptureFixture,
) -> None:
    descriptor = RunDescriptor(
        run_id="run-x", target={"kind": "entity", "canonical_id": "host:aws:i-1"}
    )
    with caplog.at_level("INFO"):
        bellu_runner.report_status(
            descriptor, RunStatus.COMPLETED, _settings(), findings_count=3
        )
    # The transition and the (not-yet-wired) write-back target are observable.
    joined = " ".join(r.message for r in caplog.records)
    assert "run-x -> completed" in joined
    assert "/api/agent/runs/run-x/status" in joined
