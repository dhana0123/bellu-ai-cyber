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

import asyncio
import json
from typing import TYPE_CHECKING, Any, cast

import httpx
import pytest

from strix.config.bellu import BelluSettings
from strix.core import bellu_runner
from strix.core.bellu_runner import RunDescriptor, RunStatus, TracePayload, run_bellu_scan
from strix.tools.graph_context import GraphContext


if TYPE_CHECKING:
    from pathlib import Path


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
        # The runner now issues a real status write-back through the same
        # injected client (task 10). Serve those PUTs with a plain 200 and only
        # record the graph-context request below.
        if request.url.path != "/api/agent/graph-context":
            return httpx.Response(200, json={"ok": True})
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

    async def fake_run_scan(**kwargs: Any) -> None:
        order.append("scan_started")
        captured.update(kwargs)
        captured["ran"] = True

    # Mark when the context fetch happened relative to the scan start.
    real_fetch = GraphContext.fetch

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
        mp.setattr(GraphContext, "fetch", staticmethod(tracking_fetch))
        mp.setattr(bellu_runner, "get_global_report_state", lambda: report_state)

        await run_bellu_scan(
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
    assert captured["ran"] is True
    assert captured["scan_id"] == "run-1"
    assert captured["image"] == "bellu-sandbox:latest"
    assert captured["max_turns"] == descriptor.max_turns

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


# ---------------------------------------------------------------------------
# Status write-back (task 10): report_status now issues the real PUT.
# ---------------------------------------------------------------------------


def test_report_status_issues_put_write_back() -> None:
    seen: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["method"] = request.method
        seen["path"] = request.url.path
        seen["auth"] = request.headers.get("Authorization", "")
        seen["has_org"] = "org" in request.url.params
        seen["body"] = json.loads(request.content.decode() or "{}")
        return httpx.Response(200, json={"ok": True})

    client = httpx.Client(transport=httpx.MockTransport(handler))
    descriptor = RunDescriptor(
        run_id="run-w", target={"kind": "entity", "canonical_id": "host:aws:i-1"}
    )

    bellu_runner.report_status(
        descriptor, RunStatus.COMPLETED, _settings(), findings_count=5, client=client
    )
    client.close()

    assert seen["method"] == "PUT"
    assert seen["path"] == "/api/agent/runs/run-w/status"
    assert seen["auth"] == f"Bearer {_TOKEN}"
    assert seen["has_org"] is False  # no org ever sent (Req 9.5)
    assert seen["body"] == {"status": "completed", "findings_count": 5}


def test_report_status_omits_findings_count_when_absent() -> None:
    seen: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["body"] = json.loads(request.content.decode() or "{}")
        return httpx.Response(200)

    client = httpx.Client(transport=httpx.MockTransport(handler))
    descriptor = RunDescriptor(
        run_id="run-r", target={"kind": "raw", "raw": "https://x"}
    )
    bellu_runner.report_status(descriptor, RunStatus.RUNNING, _settings(), client=client)
    client.close()

    assert seen["body"] == {"status": "running"}


def test_report_status_never_raises_on_transport_error() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("control plane down")

    client = httpx.Client(transport=httpx.MockTransport(handler))
    descriptor = RunDescriptor(
        run_id="run-e", target={"kind": "raw", "raw": "https://x"}
    )
    # Best-effort: a write-back failure must not propagate (Req 7.4).
    bellu_runner.report_status(descriptor, RunStatus.FAILED, _settings(), client=client)
    client.close()


# ---------------------------------------------------------------------------
# Worker loop (task 10): claim -> run, empty -> backoff, error -> failed+continue.
# ---------------------------------------------------------------------------


def _claim_handler(responses: list[Any]) -> Any:
    """A MockTransport handler serving one queued claim response per POST.

    Each element of ``responses`` is the ``claims`` list to return on that call;
    once exhausted it keeps returning an empty claim so the loop idles.
    """
    calls: dict[str, int] = {"n": 0}

    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == bellu_runner.CLAIM_PATH
        assert request.headers.get("Authorization", "") == f"Bearer {_TOKEN}"
        assert "org" not in request.url.params  # no org ever sent (Req 19.1)
        i = calls["n"]
        calls["n"] += 1
        claims = responses[i] if i < len(responses) else []
        return httpx.Response(200, json={"claims": claims})

    return handler


class _StopLoopError(Exception):
    """Sentinel raised from a patched sleep to break the infinite loop in tests."""


@pytest.mark.asyncio
async def test_worker_loop_claims_and_runs_read_only() -> None:
    # One non-empty claim, then empties; the patched sleep stops the loop.
    claim = {
        "run_id": "run-100",
        "source": "github",
        "delivery_id": "d-1",
        "payload": {"canonical_id": "host:aws:i-9"},
    }
    client = httpx.Client(transport=httpx.MockTransport(_claim_handler([[claim]])))

    ran: list[RunDescriptor] = []

    async def fake_run(descriptor: Any, **_kw: Any) -> None:
        ran.append(descriptor)

    async def stop_sleep(_s: float) -> None:
        raise _StopLoopError

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner, "run_bellu_scan", fake_run)
        mp.setattr(asyncio, "sleep", stop_sleep)
        with pytest.raises(_StopLoopError):
            await bellu_runner.run_worker_loop(_settings(), client=client)

    client.close()

    assert len(ran) == 1
    d = ran[0]
    assert d.run_id == "run-100"
    # Target derived from the claim payload's canonical_id (entity, read-only).
    assert d.target == {"kind": "entity", "canonical_id": "host:aws:i-9"}
    # Auto-triggered runs are strictly read-only (Property P5 / Req 9.1).
    assert d.authz_scope is None


@pytest.mark.asyncio
async def test_worker_loop_backs_off_exponentially_on_empty_claims() -> None:
    # Always-empty claims; record the backoff schedule the loop sleeps on.
    client = httpx.Client(transport=httpx.MockTransport(_claim_handler([])))

    slept: list[float] = []

    async def record_sleep(s: float) -> None:
        slept.append(s)
        if len(slept) >= 5:  # let it climb a few times, then stop
            raise _StopLoopError

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(asyncio, "sleep", record_sleep)
        with pytest.raises(_StopLoopError):
            await bellu_runner.run_worker_loop(
                _settings(),
                client=client,
                idle_backoff_s=2.0,
                max_backoff_s=16.0,
            )

    client.close()

    # Doubles from the initial interval, capped at the maximum (Req 6.5).
    assert slept == [2.0, 4.0, 8.0, 16.0, 16.0]


@pytest.mark.asyncio
async def test_worker_loop_resets_backoff_after_nonempty_claim() -> None:
    # empty (sleep 2) -> empty (sleep 4) -> non-empty (reset) -> empty (sleep 2).
    claim = {"run_id": "run-r", "payload": {"url": "https://a"}}
    client = httpx.Client(
        transport=httpx.MockTransport(_claim_handler([[], [], [claim]]))
    )

    slept: list[float] = []

    async def record_sleep(s: float) -> None:
        slept.append(s)
        if len(slept) >= 3:  # stop after the post-reset sleep
            raise _StopLoopError

    async def fake_run(_descriptor: Any, **_kw: Any) -> None:
        return None

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner, "run_bellu_scan", fake_run)
        mp.setattr(asyncio, "sleep", record_sleep)
        with pytest.raises(_StopLoopError):
            await bellu_runner.run_worker_loop(
                _settings(), client=client, idle_backoff_s=2.0, max_backoff_s=30.0
            )

    client.close()

    # 2, 4 (climbing), then reset to 2 after the non-empty claim (Req 6.6).
    assert slept == [2.0, 4.0, 2.0]


@pytest.mark.asyncio
async def test_worker_loop_run_error_reports_failed_and_continues() -> None:
    # Two claims in one batch: the first run raises, the second must still run.
    claims = [
        {"run_id": "run-bad", "payload": {"url": "https://bad"}},
        {"run_id": "run-ok", "payload": {"url": "https://ok"}},
    ]
    client = httpx.Client(transport=httpx.MockTransport(_claim_handler([claims])))

    ran: list[str] = []

    async def fake_run(descriptor: Any, **_kw: Any) -> None:
        ran.append(descriptor.run_id)
        if descriptor.run_id == "run-bad":
            raise RuntimeError("engine exploded")

    failed: list[tuple[str, RunStatus]] = []

    def record_status(descriptor: Any, status: RunStatus, *_a: Any, **_k: Any) -> None:
        failed.append((descriptor.run_id, status))

    async def stop_sleep(_s: float) -> None:
        raise _StopLoopError

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner, "run_bellu_scan", fake_run)
        mp.setattr(bellu_runner, "report_status", record_status)
        mp.setattr(asyncio, "sleep", stop_sleep)
        with pytest.raises(_StopLoopError):
            await bellu_runner.run_worker_loop(_settings(), client=client)

    client.close()

    # The erroring run did not crash the loop: the next claim still ran (Req 7.4).
    assert ran == ["run-bad", "run-ok"]
    # A failed status write-back was reported for the erroring run.
    assert ("run-bad", RunStatus.FAILED) in failed


def test_claim_returns_empty_on_transport_error() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("down")

    client = httpx.Client(transport=httpx.MockTransport(handler))
    assert bellu_runner._claim(_settings(), batch=4, client=client) == []
    client.close()


def test_claim_returns_empty_on_non_2xx() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "temporarily unavailable"})

    client = httpx.Client(transport=httpx.MockTransport(handler))
    assert bellu_runner._claim(_settings(), batch=4, client=client) == []
    client.close()


def test_claim_filters_malformed_elements() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            200,
            json={"claims": [{"run_id": "ok"}, {"no_run_id": True}, "garbage"]},
        )

    client = httpx.Client(transport=httpx.MockTransport(handler))
    claims = bellu_runner._claim(_settings(), batch=4, client=client)
    client.close()
    assert claims == [{"run_id": "ok"}]


def test_target_from_claim_prefers_explicit_target() -> None:
    explicit = bellu_runner._target_from_claim(
        {"run_id": "r", "target": {"kind": "entity", "canonical_id": "x"}},
    )
    assert explicit == {"kind": "entity", "canonical_id": "x"}


def test_target_from_claim_derives_from_payload_like_targetfromevent() -> None:
    # canonical_id / asset_id -> entity.
    assert bellu_runner._target_from_claim(
        {"payload": {"canonical_id": "host:aws:i-1"}}
    ) == {"kind": "entity", "canonical_id": "host:aws:i-1"}
    assert bellu_runner._target_from_claim({"payload": {"asset_id": "a-1"}}) == {
        "kind": "entity",
        "canonical_id": "a-1",
    }
    # target / url / host -> raw.
    assert bellu_runner._target_from_claim(
        {"payload": {"url": "https://app"}}
    ) == {"kind": "raw", "raw": "https://app"}
    # Fallback: source:delivery_id raw reference.
    assert bellu_runner._target_from_claim(
        {"source": "github", "delivery_id": "d-9", "payload": {}}
    ) == {"kind": "raw", "raw": "github:d-9"}


# ---------------------------------------------------------------------------
# Trace write-back (task 16): emit_trace posts {agents, handoffs}; the payload
# builder maps a run's ReportState/run_record agent tree to the trace shape.
# ---------------------------------------------------------------------------


def test_emit_trace_posts_shape_auth_and_no_org() -> None:
    seen: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["method"] = request.method
        seen["path"] = request.url.path
        seen["auth"] = request.headers.get("Authorization", "")
        seen["has_org"] = "org" in request.url.params
        seen["body"] = json.loads(request.content.decode() or "{}")
        return httpx.Response(200, json={"ok": True})

    client = httpx.Client(transport=httpx.MockTransport(handler))
    descriptor = RunDescriptor(
        run_id="run-t", target={"kind": "entity", "canonical_id": "host:aws:i-1"}
    )

    agents = [
        {
            "agent_id": "a-root",
            "parent_id": None,
            "agent_name": "Recon",
            "outcome": "completed",
            "started_at": "2024-01-01T00:00:00+00:00",
            "ended_at": "2024-01-01T00:05:00+00:00",
        }
    ]
    handoffs = [
        {"from_agent_id": "a-root", "to_agent_id": "a-child", "at": "2024-01-01T00:01:00+00:00"}
    ]

    bellu_runner.emit_trace(
        descriptor, _settings(), agents=agents, handoffs=handoffs, client=client
    )
    client.close()

    assert seen["method"] == "POST"
    assert seen["path"] == "/api/agent/runs/run-t/trace"
    assert seen["auth"] == f"Bearer {_TOKEN}"
    assert seen["has_org"] is False  # no org ever sent (Req 9.5)
    assert seen["body"] == {"agents": agents, "handoffs": handoffs}


def test_emit_trace_accepts_bare_run_id() -> None:
    seen: dict[str, Any] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["path"] = request.url.path
        return httpx.Response(200)

    client = httpx.Client(transport=httpx.MockTransport(handler))
    bellu_runner.emit_trace("run-bare", _settings(), agents=[], handoffs=[], client=client)
    client.close()

    assert seen["path"] == "/api/agent/runs/run-bare/trace"


def test_emit_trace_never_raises_on_transport_error() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("control plane down")

    client = httpx.Client(transport=httpx.MockTransport(handler))
    descriptor = RunDescriptor(
        run_id="run-e", target={"kind": "raw", "raw": "https://x"}
    )
    # Best-effort: a trace write-back failure must not propagate (Req 12.2).
    bellu_runner.emit_trace(descriptor, _settings(), agents=[], handoffs=[], client=client)
    client.close()


def test_emit_trace_never_raises_on_non_2xx() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, json={"error": "temporarily unavailable"})

    client = httpx.Client(transport=httpx.MockTransport(handler))
    # A non-2xx is logged but never raised (Req 12.2).
    bellu_runner.emit_trace("run-5xx", _settings(), agents=[], handoffs=[], client=client)
    client.close()


class _FakeReportStateWithGraph:
    """A ReportState stand-in exposing get_run_dir() + a run_record.

    build_trace_payload reads the coordinator's persisted agent graph
    (``.state/agents.json``) under the run dir and derives per-agent activity
    bounds from the run_record's llm_requests/tool_calls.
    """

    def __init__(self, run_dir: Path, run_record: dict[str, Any]) -> None:
        self._run_dir = run_dir
        self.run_record = run_record

    def get_run_dir(self) -> Path:
        return self._run_dir


def _write_agent_graph(run_dir: Path, graph: dict[str, Any]) -> None:
    from strix.core.paths import runtime_state_dir

    state_dir = runtime_state_dir(run_dir)
    state_dir.mkdir(parents=True, exist_ok=True)
    (state_dir / "agents.json").write_text(json.dumps(graph), encoding="utf-8")


def test_build_trace_payload_maps_tree_and_derives_handoffs(tmp_path: Path) -> None:
    run_dir = tmp_path / "strix_runs" / "run-1"
    run_dir.mkdir(parents=True)
    _write_agent_graph(
        run_dir,
        {
            "statuses": {"a-root": "completed", "a-child": "running"},
            "parent_of": {"a-root": None, "a-child": "a-root"},
            "names": {"a-root": "Recon", "a-child": "Exploit"},
        },
    )
    run_record = {
        "run_id": "run-1",
        "llm_requests": [
            {"agent_id": "a-root", "started_at": "2024-01-01T00:00:05+00:00"},
            {"agent_id": "a-root", "started_at": "2024-01-01T00:00:01+00:00"},
            {"agent_id": "a-child", "started_at": "2024-01-01T00:02:00+00:00"},
        ],
        "tool_calls": [
            {"agent_id": "a-root", "started_at": "2024-01-01T00:10:00+00:00"},
            {"agent_id": "a-child", "started_at": "2024-01-01T00:03:00+00:00"},
        ],
    }
    state = _FakeReportStateWithGraph(run_dir, run_record)

    payload = bellu_runner.build_trace_payload(cast("Any", state))

    # One leg per agent, sorted by agent id.
    assert [a["agent_id"] for a in payload["agents"]] == ["a-child", "a-root"]
    by_id = {a["agent_id"]: a for a in payload["agents"]}

    root = by_id["a-root"]
    assert root["parent_id"] is None
    assert root["agent_name"] == "Recon"
    assert root["outcome"] == "completed"  # terminal status becomes the outcome
    # Start/end derived from earliest/latest recorded activity across both logs.
    assert root["started_at"] == "2024-01-01T00:00:01+00:00"
    assert root["ended_at"] == "2024-01-01T00:10:00+00:00"

    child = by_id["a-child"]
    assert child["parent_id"] == "a-root"
    assert child["agent_name"] == "Exploit"
    assert child["outcome"] == ""  # still running => no terminal outcome
    assert child["started_at"] == "2024-01-01T00:02:00+00:00"
    assert child["ended_at"] == "2024-01-01T00:03:00+00:00"

    # Handoff derived from the parent->child relationship (run.json has none
    # explicit); the root agent (no parent) emits none.
    assert payload["handoffs"] == [
        {
            "from_agent_id": "a-root",
            "to_agent_id": "a-child",
            "at": "2024-01-01T00:02:00+00:00",
        }
    ]


def test_build_trace_payload_empty_without_graph(tmp_path: Path) -> None:
    run_dir = tmp_path / "strix_runs" / "run-empty"
    run_dir.mkdir(parents=True)
    # No agents.json written => empty batch, nothing fabricated.
    state = _FakeReportStateWithGraph(run_dir, {"run_id": "run-empty"})
    assert bellu_runner.build_trace_payload(cast("Any", state)) == {
        "agents": [],
        "handoffs": [],
    }


def test_build_trace_payload_none_state_returns_empty() -> None:
    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner, "get_global_report_state", lambda: None)
        assert bellu_runner.build_trace_payload() == {"agents": [], "handoffs": []}


@pytest.mark.asyncio
async def test_run_bellu_scan_emits_trace_after_completion() -> None:
    # The scan completes, then a trace write-back is posted to the trace path.
    trace_posts: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/agent/runs/run-tr/trace":
            trace_posts.append(json.loads(request.content.decode() or "{}"))
        return httpx.Response(200, json={"ok": True})

    client = httpx.Client(transport=httpx.MockTransport(handler))

    async def fake_run_scan(**_kwargs: Any) -> None:
        return None

    descriptor = RunDescriptor(
        run_id="run-tr", target={"kind": "raw", "raw": "https://app.example.com"}
    )

    captured_payload: TracePayload = {"agents": [{"agent_id": "a1"}], "handoffs": []}

    with pytest.MonkeyPatch.context() as mp:
        mp.setattr(bellu_runner, "get_global_report_state", lambda: None)
        mp.setattr(bellu_runner, "build_trace_payload", lambda *_a, **_k: captured_payload)

        await run_bellu_scan(
            descriptor,
            settings=_settings(),
            client=client,
            run_scan=fake_run_scan,
            image="img",
        )

    client.close()

    assert trace_posts == [{"agents": [{"agent_id": "a1"}], "handoffs": []}]
