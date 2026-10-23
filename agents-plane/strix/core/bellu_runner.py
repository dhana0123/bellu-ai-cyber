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
The design's Runs seam (``POST /api/agent/runs`` -> 202 ``{run_id, queued}``,
``GET /api/agent/runs/{id}`` -> status) persists the run and hands off to the
agents plane **out-of-band ("queue/notify")**. There is no implemented
push/queue yet. So this module is written against a clean, transport-agnostic
interface -- a :class:`RunDescriptor` -- and :func:`run_bellu_scan` executes one
such descriptor. How a descriptor reaches the plane is left to the caller:

- a future notify/queue consumer can build a :class:`RunDescriptor` from a
  delivered message and ``await run_bellu_scan(descriptor)``;
- until that exists, :func:`poll_launched_runs` documents (and implements, over
  the existing ``GET`` status endpoint) a polling driver that turns launched
  runs into descriptors. Note it can only observe status the control plane
  already exposes; it does not invent a queue.

Status write-back gap (identified, not fabricated)
-------------------------------------------------------------------------------
The Go side (design "Seam 3") implements ``POST /api/agent/runs`` (launch, 202)
and ``GET /api/agent/runs/{id}`` (read status). It persists the run with
``store.CreateAgentRun`` / reads it with ``store.GetAgentRun``, but there is
**no endpoint for the plane to WRITE a status transition back**
(``running``/``completed``/``failed``) -- ``handleAgentRunStatus`` is read-only.
So :func:`report_status` is implemented as a clear seam that logs the transition
and is ready to ``PUT``/``POST`` it the moment a Go write-back endpoint exists;
the target path/verb are captured in :data:`STATUS_WRITE_BACK` so wiring it is a
one-line change. The gap is documented rather than papered over with a
fabricated Go endpoint.
"""

from __future__ import annotations

import ipaddress
import logging
from collections.abc import Awaitable
from dataclasses import dataclass, field
from enum import StrEnum
from typing import TYPE_CHECKING, Any, Protocol, cast

from strix.config.bellu import BelluSettings, load_bellu_settings
from strix.report.state import get_global_report_state
from strix.tools.finding_report import BelluReporter
from strix.tools.graph_context import GraphContext, graph_context_block


if TYPE_CHECKING:
    import httpx
    from agents.result import RunResultBase


logger = logging.getLogger(__name__)


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
    extra: dict[str, Any] = field(default_factory=lambda: dict[str, Any]())

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

    **Status write-back seam.** The control plane does not yet expose an
    endpoint to write a run's status back (see the module docstring); this
    function is the single place that will issue that write once it does. Today
    it logs the transition at INFO so the plane's view of the run is observable,
    and is structured so the network call is a drop-in: uncomment-free wiring to
    :data:`STATUS_WRITE_BACK` is all that remains.

    It never raises: a status report must not fail the run it describes.
    """
    path = STATUS_WRITE_BACK["path_template"].format(run_id=descriptor.run_id)
    url = f"{settings.base}{path}"
    logger.info(
        "run %s -> %s (findings=%s)%s [status write-back target: %s %s]",
        descriptor.run_id,
        status.value,
        findings_count if findings_count is not None else "?",
        f" {detail}" if detail else "",
        STATUS_WRITE_BACK["verb"],
        url,
    )
    # Ready to issue the write the moment a Go write-back endpoint lands:
    # request STATUS_WRITE_BACK["verb"] against ``url`` with the service-account
    # auth header and a {"status", "findings_count"} body, reusing ``client``.
    # Intentionally NOT calling the network today -- there is no such endpoint
    # yet, and fabricating one would hide the gap (see the module docstring).
    _ = client  # accepted for a stable signature once wiring lands


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

        run_scan = run_strix_scan  # type: ignore[assignment]

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


def poll_launched_runs(
    settings: BelluSettings | None = None,
    *,
    client: httpx.Client | None = None,
) -> list[RunDescriptor]:
    """Documented polling driver stub for the control-plane hand-off.

    The design hands launched runs off "out-of-band (queue/notify)", which is
    **not implemented** on the Go side yet; the only run surface the plane can
    observe is ``GET /api/agent/runs/{id}`` for a run it already knows. There is
    therefore no "list launched runs" endpoint to poll, so this cannot discover
    runs on its own. It exists to make the hand-off explicit and to give a
    future queue/notify consumer a single place to build :class:`RunDescriptor`
    objects from delivered messages, then call :func:`run_bellu_scan` on each.

    Returns an empty list today (no discovery endpoint); documented so the gap
    is visible rather than silently absent.
    """
    _ = settings, client
    logger.info(
        "poll_launched_runs: no launched-runs discovery endpoint exists on the "
        "control plane yet (hand-off is out-of-band queue/notify, unimplemented). "
        "A future consumer should build RunDescriptors and call run_bellu_scan().",
    )
    return []
