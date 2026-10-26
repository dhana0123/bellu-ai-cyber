"""Bellu Scan_Runner: own a single run's lifecycle on the agents plane.

This is task 12 of the agents plane. It implements the **Scan_Runner**
component from the design (``Agents (Py) | Scan Runner``): the thing that owns
one run from launch to a terminal state. It is a thin, Bellu-owned
*orchestrator* that composes the pieces tasks 8-11 already built; it does
**not** reimplement the agent loop and does **not** rewrite
:func:`strix.core.runner.run_strix_scan`.

Lifecycle (design "Scan Lifecycle (Sequence)" + Requirement 7.6):

1. Resolve Bellu settings and the service-account token
   (:mod:`strix.config.bellu`).
2. Encode the target (``entity:<canonical_id>`` or ``raw:<url-or-ip>``) and
   **fetch the bounded graph-context BEFORE the coordinator initializes the
   root agent** (Requirement 7.6) via
   :meth:`strix.tools.graph_context.GraphContext.fetch`.
3. Build ``root_instructions_override`` from the fetched context via
   :func:`strix.tools.graph_context.graph_context_block`, so the graph context
   lands in the root agent's prompt ahead of its first instruction
   (Requirement 10.2).
4. Construct a :class:`strix.tools.finding_report.BelluReporter` and wire it to
   the run's :class:`~strix.report.state.ReportState` (``attach``) for
   incremental findings reporting, arranging ``flush_on_completion`` at the end
   (Requirement 6, task 11).
5. Run the scan through :func:`strix.core.runner.run_strix_scan`, passing
   ``root_instructions_override`` (reusing the engine, not reimplementing it).
6. Report status back to the control plane as the run moves
   ``queued -> running -> completed | failed`` (see the status-write-back gap
   note below).

Tenant isolation (Requirement 9.5): every control-plane call carries only the
service-account bearer token. The org is resolved server-side from the
principal; nothing here sends an org.

The control-plane hand-off
-------------------------------------------------------------------------------
The hand-off is now a durable-first claim queue. The control plane exposes
``POST /api/agent/monitor/claim`` (task 6): it atomically claims routed
``monitor_events`` rows with ``FOR UPDATE SKIP LOCKED``, launches a strictly
read-only run per row, and returns ``{"claims": [...]}`` with each run_id
stamped on. :func:`run_worker_loop` is the long-lived driver that polls this
endpoint, builds a :class:`RunDescriptor` per claim (``authz_scope=None`` --
read-only, Property P5 / Requirement 9.1), and ``await``\\s
:func:`run_bellu_scan` on each. The transport-agnostic :class:`RunDescriptor`
remains the clean interface: a future notify/queue consumer can equally build
one from a delivered message and call :func:`run_bellu_scan` directly.

Status write-back
-------------------------------------------------------------------------------
The control plane implements ``PUT /api/agent/runs/:id/status`` (task 7): it
folds the reported status and findings count onto the ``agent_runs`` row and the
linked ``monitor_events`` row, validating ``status`` in ``{running, completed,
failed}`` (Requirement 7.1-7.3). :func:`report_status` issues that write with
the service-account auth header and a ``{status, findings_count}`` body, keyed by
:data:`STATUS_WRITE_BACK`. It is best-effort and never raises: a status-report
failure (or an unreachable control plane) must not fail the run it describes
(Requirement 7.4). The org is resolved server-side from the principal; nothing
here sends an org (Requirement 9.5).

Trace write-back
-------------------------------------------------------------------------------
After a run completes, the plane reports its agent-tree and handoff telemetry to
the control plane (``POST /api/agent/runs/:id/trace``, task 15), which maps each
agent name to a pipeline stage, upserts the legs/handoffs, and folds
``agents_count``/``handoffs_count`` onto the ``agent_runs`` row (Requirement
12.1). :func:`build_trace_payload` assembles the ``{agents, handoffs}`` batch
from the run's recorded telemetry (:func:`get_global_report_state`); it reads the
coordinator's persisted agent graph (``agents.json``) for the tree -- agent id,
parent, name, and terminal status -- and derives per-agent start/end times from
the ``llm_requests``/``tool_calls`` entries in ``run.json`` that carry that
``agent_id``. The run.json shape records **no explicit handoffs**, so handoffs
are *derived* from the parent->child relationships in the agent tree (one
handoff per registered child, from its parent to it); nothing is fabricated
beyond that documented derivation. :func:`emit_trace` posts the batch with the
service-account auth header, keyed by :data:`TRACE_WRITE_BACK`. Like
:func:`report_status` it is best-effort and NEVER raises: a trace post must not
fail the run it describes (Requirement 12.2). The org is resolved server-side
from the principal; nothing here sends an org (Requirement 9.5).
"""

from __future__ import annotations

import asyncio
import ipaddress
import logging
from dataclasses import dataclass, field
from enum import StrEnum
from typing import TYPE_CHECKING, Any, Protocol, TypedDict, cast

from strix.config.bellu import BelluSettings, load_bellu_settings
from strix.core.paths import runtime_state_dir
from strix.report.coverage import read_agent_graph
from strix.report.state import get_global_report_state
from strix.tools.finding_report import BelluReporter
from strix.tools.graph_context import GraphContext, graph_context_block


if TYPE_CHECKING:
    from collections.abc import Awaitable

    import httpx
    from agents.result import RunResultBase

    from strix.report.state import ReportState


logger = logging.getLogger(__name__)

# Control-plane claim endpoint the worker loop polls (task 6). A POST with the
# service-account bearer token returns {"claims": [...]} of claimed, run-stamped
# monitor rows.
CLAIM_PATH = "/api/agent/monitor/claim"

# Timeouts (seconds) for the short control-plane calls the loop makes.
_CLAIM_TIMEOUT = 30.0
_STATUS_TIMEOUT = 10.0
_TRACE_TIMEOUT = 10.0

# HTTP boundary used to treat a response as a failed write-back.
_HTTP_BAD_REQUEST = 400


class _RunScan(Protocol):
    """The scan entry point the runner drives (``run_strix_scan``'s shape).

    Only the keyword arguments the Scan_Runner passes are modeled; the real
    ``run_strix_scan`` accepts more with defaults. Typing it as a Protocol keeps
    the orchestrator honest about what it calls and lets tests inject a stub.
    """

    def __call__(
        self,
        *,
        scan_config: dict[str, Any],
        scan_id: str,
        image: str,
        max_turns: int,
        root_instructions_override: str,
    ) -> Awaitable[RunResultBase | None]: ...


class RunStatus(StrEnum):
    """The run lifecycle states the control plane models (design "RunStatus").

    The plane drives a run ``queued -> running -> (completed | failed)``.
    ``queued`` is the launch state the control plane assigns; the plane reports
    ``running`` when it begins execution and a terminal state at the end.
    """

    QUEUED = "queued"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"


# The control-plane status-write-back target, kept in one place so wiring it to
# a real Go endpoint (once it exists) is a single edit. verb/path are the
# design-consistent shape: a write against the per-run resource. See the
# module docstring's "Status write-back gap".
STATUS_WRITE_BACK: dict[str, str] = {
    "verb": "PUT",
    "path_template": "/api/agent/runs/{run_id}/status",
}


# The control-plane trace-write-back target (task 15's
# ``POST /api/agent/runs/:id/trace``). Same shape as STATUS_WRITE_BACK: a write
# against the per-run resource. See the module docstring's "Trace write-back".
TRACE_WRITE_BACK: dict[str, str] = {
    "verb": "POST",
    "path_template": "/api/agent/runs/{run_id}/trace",
}


class TracePayload(TypedDict):
    """The batched trace write-back body posted to ``POST /api/agent/runs/{id}/trace``.

    Matches the control plane's handler (task 15): ``agents`` is one entry per
    agent leg (``{agent_id, parent_id, agent_name, outcome, started_at,
    ended_at}``) and ``handoffs`` is one entry per recorded handoff
    (``{from_agent_id, to_agent_id, at}``). Timestamps are ISO-8601 strings or
    ``None``; the control plane maps each ``agent_name`` to a pipeline stage.
    """

    agents: list[dict[str, Any]]
    handoffs: list[dict[str, Any]]


@dataclass(frozen=True)
class RunDescriptor:
    """A single run to execute, independent of how it reached the plane.

    This is the clean, transport-agnostic hand-off interface. A notify/queue
    consumer or a polling driver builds one of these and passes it to
    :func:`run_bellu_scan`; the runner never assumes a particular delivery
    mechanism.

    ``target`` mirrors the Go ``TargetRef``:
    ``{"kind": "entity", "canonical_id": "..."}`` or
    ``{"kind": "raw", "raw": "https://..."}``. ``authz_scope`` mirrors the Go
    ``AuthzScope`` and is carried through for the active-testing gate (task 15);
    absent/None means read-only graph reasoning only (Requirement 8.1).
    ``instructions`` is optional operator direction composed after the graph
    block. ``scan_mode`` and ``max_turns`` tune the underlying engine run.
    """

    run_id: str
    target: dict[str, Any]
    authz_scope: dict[str, Any] | None = None
    instructions: str | None = None
    scan_mode: str = "deep"
    max_turns: int = 50
    extra: dict[str, Any] = field(default_factory=dict)

    @property
    def target_kind(self) -> str:
        return str(self.target.get("kind", "")).strip().lower()

    def encoded_target(self) -> str:
        """The scheme-prefixed ``?target=`` value for the graph-context fetch.

        ``entity`` targets encode their ``canonical_id``; ``raw`` targets encode
        their ``raw`` URL/IP. Matches the Go ``parseAgentTargetRef`` via
        :meth:`GraphContext.encode_target`.
        """
        kind = self.target_kind
        if kind == "entity":
            value = str(self.target.get("canonical_id", "")).strip()
        elif kind == "raw":
            value = str(self.target.get("raw", "")).strip()
        else:
            raise ValueError(f"unknown target kind: {self.target.get('kind')!r}")
        return GraphContext.encode_target(kind, value)


def report_status(
    descriptor: RunDescriptor,
    status: RunStatus,
    settings: BelluSettings,
    *,
    findings_count: int | None = None,
    client: httpx.Client | None = None,
    detail: str | None = None,
) -> None:
    """Report a run status transition to the control plane.

    **Status write-back.** The control plane now exposes the write-back endpoint
    (``PUT /api/agent/runs/:id/status``, task 7), so this issues the real write:
    it posts ``{"status", "findings_count"}`` to :data:`STATUS_WRITE_BACK` with
    the service-account auth header, folding the transition onto both the
    ``agent_runs`` row and the linked ``monitor_events`` row (Requirement 7.1,
    7.2). The org is resolved server-side from the principal; nothing here sends
    an org (Requirement 9.5).

    It is best-effort and never raises: a status report (or an unreachable
    control plane) must not fail the run it describes (Requirement 7.4). The
    transition is also logged at INFO so the plane's view stays observable even
    when the write-back cannot be delivered.
    """
    path = STATUS_WRITE_BACK["path_template"].format(run_id=descriptor.run_id)
    url = f"{settings.base}{path}"
    logger.info(
        "run %s -> %s (findings=%s)%s [status write-back: %s %s]",
        descriptor.run_id,
        status.value,
        findings_count if findings_count is not None else "?",
        f" {detail}" if detail else "",
        STATUS_WRITE_BACK["verb"],
        url,
    )

    import httpx

    body: dict[str, Any] = {"status": status.value}
    if findings_count is not None:
        body["findings_count"] = findings_count

    owns_client = client is None
    http = client or httpx.Client(timeout=_STATUS_TIMEOUT)
    try:
        resp = http.request(
            STATUS_WRITE_BACK["verb"],
            url,
            headers=settings.auth_header(),
            json=body,
            timeout=_STATUS_TIMEOUT,
        )
        if resp.status_code >= _HTTP_BAD_REQUEST:
            logger.warning(
                "run %s: status write-back %s returned HTTP %s",
                descriptor.run_id,
                status.value,
                resp.status_code,
            )
    except Exception:
        # Best-effort: an unreachable control plane or any transport/auth error
        # must never fail the run it describes (Requirement 7.4).
        logger.exception(
            "run %s: status write-back %s failed", descriptor.run_id, status.value
        )
    finally:
        if owns_client:
            http.close()


def emit_trace(
    descriptor_or_run_id: RunDescriptor | str,
    settings: BelluSettings,
    *,
    agents: list[dict[str, Any]],
    handoffs: list[dict[str, Any]],
    client: httpx.Client | None = None,
) -> None:
    """Report a run's agent-tree + handoff trace to the control plane.

    **Trace write-back.** Posts the ``{agents, handoffs}`` batch to
    :data:`TRACE_WRITE_BACK` (``POST /api/agent/runs/:id/trace``, task 15) with
    the service-account auth header. The control plane maps each agent name to a
    pipeline stage, upserts the legs/handoffs, and folds
    ``agents_count``/``handoffs_count`` onto the ``agent_runs`` row (Requirement
    12.1). The org is resolved server-side from the principal; nothing here sends
    an org (Requirement 9.5).

    Mirrors :func:`report_status`' signature/shape and its best-effort contract:
    it is best-effort and **NEVER raises**. A trace post (or an unreachable
    control plane) must not fail the run it describes and must change neither the
    run's status nor its terminal outcome, whether the write-back succeeds or
    fails (Requirement 12.2). ``descriptor_or_run_id`` accepts either a
    :class:`RunDescriptor` (its ``run_id`` is used) or a bare run id string.
    """
    run_id = (
        descriptor_or_run_id.run_id
        if isinstance(descriptor_or_run_id, RunDescriptor)
        else str(descriptor_or_run_id)
    )
    path = TRACE_WRITE_BACK["path_template"].format(run_id=run_id)
    url = f"{settings.base}{path}"
    logger.info(
        "run %s: trace write-back %s %s (agents=%d, handoffs=%d)",
        run_id,
        TRACE_WRITE_BACK["verb"],
        url,
        len(agents),
        len(handoffs),
    )

    import httpx

    body: TracePayload = {"agents": agents, "handoffs": handoffs}

    owns_client = client is None
    http = client or httpx.Client(timeout=_TRACE_TIMEOUT)
    try:
        resp = http.request(
            TRACE_WRITE_BACK["verb"],
            url,
            headers=settings.auth_header(),
            json=body,
            timeout=_TRACE_TIMEOUT,
        )
        if resp.status_code >= _HTTP_BAD_REQUEST:
            logger.warning(
                "run %s: trace write-back returned HTTP %s", run_id, resp.status_code
            )
    except Exception:
        # Best-effort: a trace post must not fail the run it describes
        # (Requirement 12.2). Any transport/auth error is swallowed.
        logger.exception("run %s: trace write-back failed", run_id)
    finally:
        if owns_client:
            http.close()


# Engine agent statuses the plane treats as terminal for a leg's outcome, so a
# leg that ran but was cut short still reports what it was doing. A running or
# waiting agent at trace time has no terminal outcome yet.
_TERMINAL_LEG_STATUSES: frozenset[str] = frozenset(
    {"completed", "failed", "stopped", "crashed"}
)


def _agent_activity_bounds(
    run_record: dict[str, Any],
) -> dict[str, tuple[str, str]]:
    """Per-agent ``(earliest, latest)`` activity timestamps from ``run.json``.

    The run.json shape records no per-agent start/end directly, but it records
    every LLM request (``llm_requests``) and tool call (``tool_calls``) with the
    acting ``agent_id`` and a ``started_at`` ISO timestamp. An agent's start is
    its earliest such timestamp and its end is its latest; this is the plane's
    best local view of when each leg was active. Agents with no recorded
    activity are simply absent and fall back to ``None`` start/end.
    """
    bounds: dict[str, tuple[str, str]] = {}
    for key in ("llm_requests", "tool_calls"):
        events = run_record.get(key)
        if not isinstance(events, list):
            continue
        for event in events:
            if not isinstance(event, dict):
                continue
            agent_id = event.get("agent_id")
            started_at = event.get("started_at")
            if not isinstance(agent_id, str) or not agent_id:
                continue
            if not isinstance(started_at, str) or not started_at:
                continue
            existing = bounds.get(agent_id)
            if existing is None:
                bounds[agent_id] = (started_at, started_at)
            else:
                lo, hi = existing
                bounds[agent_id] = (min(lo, started_at), max(hi, started_at))
    return bounds


def build_trace_payload(report_state: ReportState | None = None) -> TracePayload:
    """Build the ``{agents, handoffs}`` trace batch from a run's telemetry.

    Reads the global :class:`~strix.report.state.ReportState` (or the one passed
    in) and maps the engine's recorded agent lifecycle onto the control plane's
    trace shape (task 15):

    - **Agents** come from the coordinator's persisted agent graph
      (``agents.json`` under the run's ``.state`` dir, via
      :func:`strix.report.coverage.read_agent_graph`): one leg per agent carrying
      ``agent_id``, ``parent_id``, ``agent_name``, and ``outcome`` (the agent's
      terminal status, or ``""`` when still running). ``started_at``/``ended_at``
      are derived from the agent's earliest/latest recorded activity in
      ``run.json`` (:func:`_agent_activity_bounds`), or ``None`` when the agent
      logged no activity.
    - **Handoffs** are **derived**, not recorded: the run.json shape exposes no
      explicit handoff events, so one handoff is emitted per registered child
      (``from = parent_id``, ``to = agent_id``), reflecting the parent->child
      delegation in the agent tree. A root agent (no parent) emits no handoff.
      Nothing beyond this documented derivation is fabricated; if the tree is
      empty the batch is empty.

    Returns an empty batch (``{"agents": [], "handoffs": []}``) when no report
    state or no agent graph is available, so the caller can post harmlessly.
    """
    state = report_state if report_state is not None else get_global_report_state()
    if state is None:
        return {"agents": [], "handoffs": []}

    try:
        graph = read_agent_graph(runtime_state_dir(state.get_run_dir()))
    except Exception:
        logger.exception("trace: could not read agent graph; emitting empty trace")
        return {"agents": [], "handoffs": []}

    statuses = graph.get("statuses")
    if not isinstance(statuses, dict):
        return {"agents": [], "handoffs": []}

    raw_names = graph.get("names")
    names: dict[str, Any] = raw_names if isinstance(raw_names, dict) else {}
    raw_parents = graph.get("parent_of")
    parents: dict[str, Any] = raw_parents if isinstance(raw_parents, dict) else {}

    run_record = state.run_record if isinstance(state.run_record, dict) else {}
    bounds = _agent_activity_bounds(run_record)

    agents: list[dict[str, Any]] = []
    handoffs: list[dict[str, Any]] = []
    for agent_id, status in statuses.items():
        parent_id = parents.get(agent_id)
        started_at, ended_at = bounds.get(agent_id, (None, None))
        status_str = str(status)
        agents.append(
            {
                "agent_id": agent_id,
                "parent_id": parent_id if isinstance(parent_id, str) and parent_id else None,
                "agent_name": names.get(agent_id) or agent_id,
                # A leg's outcome is its terminal status; a still-active agent
                # has none yet, reported as "".
                "outcome": status_str if status_str in _TERMINAL_LEG_STATUSES else "",
                "started_at": started_at,
                "ended_at": ended_at,
            }
        )
        # Derived handoff: the parent delegated to this child. Run.json records
        # no explicit handoffs, so this parent->child edge is the derivation.
        if isinstance(parent_id, str) and parent_id:
            handoffs.append(
                {
                    "from_agent_id": parent_id,
                    "to_agent_id": agent_id,
                    # Best local view of when the handoff happened: the child's
                    # first recorded activity. None when it logged none.
                    "at": started_at,
                }
            )

    # Deterministic order (by agent id) so repeated emits are stable.
    agents.sort(key=lambda a: str(a["agent_id"]))
    handoffs.sort(key=lambda h: (str(h["from_agent_id"]), str(h["to_agent_id"])))
    return {"agents": agents, "handoffs": handoffs}


def _build_scan_config(descriptor: RunDescriptor) -> dict[str, Any]:
    """Build the engine ``scan_config`` for a descriptor.

    A ``raw`` target becomes an authorized engine target (a URL -> a
    ``web_application``, an IP -> an ``ip_address``) so the sandbox/active-testing
    path (task 15/16), when authorized, has a concrete target. An ``entity``
    target has no raw host to hand the engine: the run is read-only graph
    reasoning and the target lives entirely in the injected graph-context block,
    so no engine target is added. ``user_instructions`` carries operator
    direction; the graph block is composed separately via
    ``root_instructions_override``.
    """
    targets: list[dict[str, Any]] = []
    if descriptor.target_kind == "raw":
        raw = str(descriptor.target.get("raw", "")).strip()
        if raw:
            if _looks_like_ip(raw):
                targets.append(
                    {"type": "ip_address", "details": {"target_ip": raw}},
                )
            else:
                targets.append(
                    {"type": "web_application", "details": {"target_url": raw}},
                )

    scan_config: dict[str, Any] = {
        "targets": targets,
        "scan_mode": descriptor.scan_mode,
    }
    if descriptor.instructions:
        scan_config["user_instructions"] = descriptor.instructions
    return scan_config


def _looks_like_ip(value: str) -> bool:
    """True when ``value`` is a bare IP (no scheme), else treat it as a URL.

    A raw target with a scheme (``http://``, ``https://``) or a path is a web
    application; a dotted/colon host with no scheme is treated as an IP. This is
    a routing hint for the engine target type only; the control plane does the
    authoritative target parsing.
    """
    v = value.strip()
    if "://" in v or "/" in v:
        return False
    try:
        ipaddress.ip_address(v)
    except ValueError:
        return False
    return True


async def run_bellu_scan(
    descriptor: RunDescriptor,
    *,
    settings: BelluSettings | None = None,
    client: httpx.Client | None = None,
    run_scan: _RunScan | None = None,
    image: str | None = None,
) -> RunResultBase | None:
    """Execute one Bellu run end-to-end (the Scan_Runner entry point).

    Orchestrates the lifecycle described in the module docstring. Returns the
    underlying engine result (or ``None`` when the engine returns none, e.g. a
    budget stop). Raises only if the engine itself raises after status has been
    reported ``failed``.

    Parameters let tests inject seams without touching the network or the
    sandbox:

    - ``settings`` -- Bellu settings (defaults to :func:`load_bellu_settings`).
    - ``client`` -- a shared ``httpx.Client`` for the graph-context fetch and
      the (future) status write-back; tests pass an ``httpx.MockTransport``
      client.
    - ``run_scan`` -- the scan entry point; defaults to
      :func:`strix.core.runner.run_strix_scan`. Injectable so a test can assert
      ordering (graph-context fetched *before* the scan starts) and the
      ``root_instructions_override`` passed in, without spinning a sandbox.
    - ``image`` -- the sandbox image passed to the engine; defaults to the
      configured Strix image.
    """
    cfg = settings or load_bellu_settings()
    token = cfg.service_account_token
    if not token:
        # Fail loudly rather than issuing unauthenticated calls (mirrors
        # BelluSettings.auth_header()).
        cfg.auth_header()
        raise AssertionError("unreachable")  # pragma: no cover

    if run_scan is None:
        from strix.core.runner import run_strix_scan

        run_scan = run_strix_scan

    # (1-2) Fetch the bounded graph-context BEFORE the coordinator initializes
    # the root agent (Requirement 7.6). encode the target the same way the Go
    # parser expects.
    encoded = descriptor.encoded_target()
    logger.info("run %s: fetching graph-context for target %s", descriptor.run_id, encoded)
    graph_context = GraphContext.fetch(cfg.base, token, encoded, client=client)

    # (3) Compose the root prompt override from the fetched context. Operator
    # instructions, if any, follow the graph block.
    root_override = graph_context_block(graph_context, descriptor.instructions)

    # (4) Wire the BelluReporter to the run's ReportState for incremental
    # reporting; flush_on_completion is arranged after the scan returns.
    reporter = BelluReporter(cfg.base, token, descriptor.run_id, client=client)
    report_state = get_global_report_state()
    if report_state is not None:
        reporter.attach(report_state)
    else:
        logger.warning(
            "run %s: no global ReportState at scan start; incremental finding "
            "reporting will rely on the completion flush",
            descriptor.run_id,
        )

    scan_config = _build_scan_config(descriptor)
    resolved_image = image or _resolve_image()

    # Report the transition out of queued before the engine runs.
    report_status(descriptor, RunStatus.RUNNING, cfg, client=client)

    # (5) Run the scan through the engine, injecting the graph context as the
    # root instructions override. The engine owns the agent loop; we compose it.
    try:
        result = await run_scan(
            scan_config=scan_config,
            scan_id=descriptor.run_id,
            image=resolved_image,
            max_turns=descriptor.max_turns,
            root_instructions_override=root_override,
        )
    except BaseException as exc:
        report_status(
            descriptor,
            RunStatus.FAILED,
            cfg,
            client=client,
            detail=f"{type(exc).__name__}: {exc}",
        )
        raise

    # (4 cont.) Reconcile any findings the incremental path has not sent, once,
    # on completion (task 11's completion flush). Best-effort: a flush failure
    # must not flip a completed run to failed.
    flushed = {"created": 0, "updated": 0, "rejected": 0}
    try:
        flushed = reporter.flush_on_completion()
    except Exception:
        logger.exception("run %s: completion flush failed", descriptor.run_id)

    # (6) Terminal status. findings_count is the plane's best local view: what
    # the reporter has accepted plus the completion flush.
    findings_count = _reporter_findings_count(reporter, flushed)
    report_status(
        descriptor,
        RunStatus.COMPLETED,
        cfg,
        findings_count=findings_count,
        client=client,
    )

    # (7) Trace write-back. Report the run's agent-tree + handoff telemetry
    # best-effort AFTER the terminal status: a trace post must never change the
    # run's outcome or fail the run it describes (Requirement 12.2), so the whole
    # thing is wrapped and emit_trace itself never raises.
    try:
        payload = build_trace_payload(report_state)
        emit_trace(
            descriptor,
            cfg,
            agents=payload["agents"],
            handoffs=payload["handoffs"],
            client=client,
        )
    except Exception:
        logger.exception("run %s: trace write-back step failed (non-fatal)", descriptor.run_id)

    return result


def _reporter_findings_count(reporter: BelluReporter, flushed: dict[str, Any]) -> int:
    """Best-effort count of findings this run reported to the control plane."""
    # BelluReporter remembers reported vuln_ids in a private set; read it
    # defensively so a refactor there cannot break status reporting.
    reported: object = getattr(reporter, "_reported", None)
    if isinstance(reported, set):
        return len(cast("set[object]", reported))
    created = int(flushed.get("created", 0) or 0)
    updated = int(flushed.get("updated", 0) or 0)
    return created + updated


def _resolve_image() -> str:
    """The sandbox image for the engine run, from Strix runtime settings."""
    from strix.config import load_settings

    image = load_settings().runtime.image
    if not image:
        raise RuntimeError(
            "No sandbox image configured (strix runtime.image). Set it before "
            "launching a Bellu scan.",
        )
    return image


def _claim(
    settings: BelluSettings,
    *,
    batch: int,
    client: httpx.Client | None = None,
) -> list[dict[str, Any]]:
    """Claim up to ``batch`` routed monitor rows from the control plane.

    POSTs to :data:`CLAIM_PATH` with the service-account bearer token and the
    batch limit, and returns the ``claims`` list from the ``{"claims": [...]}``
    response. The control plane atomically claims rows via ``FOR UPDATE SKIP
    LOCKED`` and launches a read-only run per row, stamping each ``run_id`` onto
    the returned claim (task 6). The org is resolved server-side from the
    principal; nothing here sends an org (Requirement 19.1).

    Best-effort: any transport error, non-2xx response, or malformed body
    yields ``[]`` so the loop simply backs off rather than crashing.
    """
    import httpx

    url = f"{settings.base}{CLAIM_PATH}"
    owns_client = client is None
    http = client or httpx.Client(timeout=_CLAIM_TIMEOUT)
    try:
        resp = http.post(
            url,
            headers=settings.auth_header(),
            json={"limit": batch},
            timeout=_CLAIM_TIMEOUT,
        )
        if resp.status_code >= _HTTP_BAD_REQUEST:
            logger.warning("claim: %s returned HTTP %s", url, resp.status_code)
            return []
        body = resp.json()
    except Exception:
        logger.exception("claim: request to %s failed", url)
        return []
    finally:
        if owns_client:
            http.close()

    if not isinstance(body, dict):
        return []
    claims = cast("dict[str, Any]", body).get("claims")
    if not isinstance(claims, list):
        return []
    # Keep only dict claims carrying a run_id; a malformed element is skipped
    # rather than allowed to crash the loop.
    return [c for c in claims if isinstance(c, dict) and c.get("run_id")]


def _target_from_claim(claim: dict[str, Any]) -> dict[str, Any]:
    """Derive the :class:`RunDescriptor` target from a claim.

    A claim may carry an explicit ``target`` (``{"kind": ..., ...}``); when it
    does, use it verbatim. Otherwise derive one from the claim's ``payload`` the
    same way the control plane's ``targetFromEvent`` does: a ``canonical_id`` or
    ``asset_id`` becomes an ``entity`` target, a ``target``/``url``/``host``
    becomes a ``raw`` target, and the stable fallback is a ``raw`` reference
    built from ``source:delivery_id`` so a run is never launched with an empty
    target.
    """
    target = claim.get("target")
    if isinstance(target, dict) and target.get("kind"):
        return cast("dict[str, Any]", target)

    payload = claim.get("payload")
    p: dict[str, Any] = payload if isinstance(payload, dict) else {}

    for key in ("canonical_id", "asset_id"):
        value = str(p.get(key, "")).strip()
        if value:
            return {"kind": "entity", "canonical_id": value}
    for key in ("target", "url", "host"):
        value = str(p.get(key, "")).strip()
        if value:
            return {"kind": "raw", "raw": value}

    source = str(claim.get("source", "")).strip()
    delivery_id = str(claim.get("delivery_id", "")).strip()
    return {"kind": "raw", "raw": f"{source}:{delivery_id}"}


async def run_worker_loop(
    settings: BelluSettings | None = None,
    *,
    client: httpx.Client | None = None,
    claim_batch: int = 4,
    idle_backoff_s: float = 2.0,
    max_backoff_s: float = 30.0,
) -> None:
    """Claim assigned monitor rows and execute them as READ-ONLY runs.

    Replaces the former ``poll_launched_runs`` stub: the control plane now
    exposes the claim endpoint (``POST /api/agent/monitor/claim``, task 6) and
    the status write-back endpoint (``PUT /api/agent/runs/:id/status``, task 7),
    so the trigger -> run -> status loop closes here.

    Each iteration claims up to ``claim_batch`` rows via :func:`_claim`. For each
    claim it builds a :class:`RunDescriptor` with ``authz_scope=None`` (strictly
    read-only, Property P5 / Requirement 9.1) and awaits :func:`run_bellu_scan`,
    which drives the engine and reports the terminal status via
    :func:`report_status`.

    Backoff (Requirement 6.5, 6.6): an empty claim sleeps for the current
    backoff, then doubles it up to ``max_backoff_s``; the next non-empty claim
    resets the backoff to ``idle_backoff_s``.

    Resilience (Requirement 7.4): a per-claim error never crashes the loop. The
    error is reported as a ``failed`` status write-back (so both the
    ``agent_runs`` and linked ``monitor_events`` rows reflect the terminal
    state) and the loop continues with the next claim.

    Runs forever; intended to be launched as a long-lived task and cancelled by
    the caller. ``client`` lets callers (and tests) inject a shared
    ``httpx.Client`` (e.g. an ``httpx.MockTransport`` client).
    """
    cfg = settings or load_bellu_settings()
    # Fail loudly at startup rather than issuing unauthenticated claims.
    cfg.auth_header()

    backoff = idle_backoff_s
    while True:
        claims = _claim(cfg, batch=claim_batch, client=client)
        if not claims:
            await asyncio.sleep(backoff)
            backoff = min(backoff * 2, max_backoff_s)
            continue

        # A non-empty claim after an idle period resets the backoff (Req 6.6).
        backoff = idle_backoff_s
        for claim in claims:
            run_id = str(claim.get("run_id", ""))
            descriptor = RunDescriptor(
                run_id=run_id,
                target=_target_from_claim(claim),
                authz_scope=None,  # auto-triggered runs are never active-testing (P5)
            )
            try:
                await run_bellu_scan(descriptor, settings=cfg, client=client)
            except Exception as exc:
                # A run error must not crash the loop. run_bellu_scan already
                # reports FAILED before re-raising; report again here defensively
                # (best-effort, never raises) so the terminal state is folded
                # onto both rows even if the earlier write-back was lost, then
                # continue with the next claim (Requirement 7.4).
                logger.exception("worker loop: run %s failed", run_id)
                report_status(
                    descriptor,
                    RunStatus.FAILED,
                    cfg,
                    client=client,
                    detail=f"{type(exc).__name__}: {exc}",
                )
