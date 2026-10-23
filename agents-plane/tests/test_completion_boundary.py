"""Bellu completion-boundary enforcement (spec task 13.1, Requirement 2).

These tests pin the root_complete vs child_complete boundary that the
AgentCoordinator owns authoritatively:

- a non-root agent cannot end the whole scan via ``root_complete``/``finish_scan``
  (Req 2.8);
- the root invoking ``child_complete``/``agent_finish`` is an error and does not
  close the scan (Req 2.9);
- a specialist child's ``child_complete`` returns its result to the parent's
  mailbox, frees its budget, and leaves the scan open (Req 2.7);
- the coordinator's authoritative status vocabulary is exactly the five
  Bellu-required values, with ``waiting`` while awaiting a child/mailbox,
  ``waiting -> running`` on the awaited result, and ``budget_paused`` while the
  budget is exhausted (Req 2.3, 2.4, 2.5, 2.6).
"""

from __future__ import annotations

import json
from typing import TYPE_CHECKING, Any, cast

import pytest
from agents.tool_context import ToolContext

from strix.core.agents import (
    BELLU_STATUSES,
    AgentCoordinator,
    bellu_status,
)
from strix.report.state import ReportState, set_global_report_state
from strix.tools.agents_graph.tools import agent_finish
from strix.tools.finish.tool import finish_scan


if TYPE_CHECKING:
    from collections.abc import Iterator
    from pathlib import Path


@pytest.fixture
def report_state(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[ReportState]:
    monkeypatch.chdir(tmp_path)
    state = ReportState(run_name="test-run")
    set_global_report_state(state)
    yield state
    set_global_report_state(None)


async def _graph() -> AgentCoordinator:
    coordinator = AgentCoordinator()
    await coordinator.register("root", "strix", parent_id=None)
    await coordinator.register("child", "Validator", parent_id="root")
    await coordinator.attach_runtime("root", resumable=False)
    await coordinator.attach_runtime("child", resumable=False)
    return coordinator


async def _call(
    tool: Any,
    coordinator: AgentCoordinator,
    agent_id: str,
    args: dict[str, Any],
    **extra: Any,
) -> dict[str, Any]:
    ctx = ToolContext(
        context={"coordinator": coordinator, "agent_id": agent_id, **extra},
        tool_name=tool.name,
        tool_call_id="call-1",
        tool_arguments="{}",
    )
    raw: str = await tool.on_invoke_tool(ctx, json.dumps(args))
    return cast("dict[str, Any]", json.loads(raw))


# --- Req 2.8: non-root root_complete is rejected ---------------------------------


@pytest.mark.asyncio
async def test_non_root_root_complete_is_rejected() -> None:
    coordinator = await _graph()

    result = await _call(
        finish_scan,
        coordinator,
        "child",
        {
            "executive_summary": "x",
            "methodology": "x",
            "technical_analysis": "x",
            "recommendations": "x",
        },
        parent_id="root",
    )

    assert result["success"] is False
    assert result["scan_completed"] is False
    # The scan is not driven to a terminal state by a child's call.
    assert coordinator.statuses["root"] == "running"
    assert coordinator.statuses["child"] == "running"


@pytest.mark.asyncio
async def test_coordinator_rejects_root_complete_from_a_child() -> None:
    coordinator = await _graph()

    allowed, error = await coordinator.authorize_root_complete("child")

    assert allowed is False
    assert error is not None
    assert await coordinator.authorize_root_complete("root") == (True, None)


# --- Req 2.9: root child_complete errors and does not close the scan -------------


@pytest.mark.asyncio
async def test_root_child_complete_errors_without_closing_the_scan() -> None:
    coordinator = await _graph()

    result = await _call(
        agent_finish,
        coordinator,
        "root",
        {"result_summary": "done"},
        parent_id=None,
    )

    assert result["success"] is False
    # The root's misrouted child_complete must not complete anything or close the scan.
    assert coordinator.statuses["root"] == "running"
    assert coordinator.statuses["child"] == "running"
    # No completion report was posted anywhere by this errored call.
    assert coordinator.runtimes["root"].mailbox == []
    assert coordinator.runtimes["child"].mailbox == []


@pytest.mark.asyncio
async def test_coordinator_rejects_child_complete_from_the_root() -> None:
    coordinator = await _graph()

    allowed, error = await coordinator.authorize_child_complete("root")

    assert allowed is False
    assert error is not None
    assert await coordinator.authorize_child_complete("child") == (True, None)
    # complete_child refuses the root too — the boundary is authoritative here.
    assert await coordinator.complete_child("root") is False
    assert coordinator.statuses["root"] == "running"


# --- Req 2.7: child child_complete returns result + frees budget, scan stays open -


@pytest.mark.asyncio
async def test_child_complete_returns_result_to_parent_and_keeps_scan_open(
    report_state: ReportState,
) -> None:
    coordinator = await _graph()
    filed = report_state.add_vulnerability_report(
        title="IDOR on /api/audits", severity="high", agent_id="child", agent_name="Validator"
    )

    result = await _call(
        agent_finish,
        coordinator,
        "child",
        {"result_summary": "confirmed", "findings": ["IDOR confirmed"]},
        parent_id="root",
    )

    assert result["success"] is True
    assert result["agent_completed"] is True
    # The child's result lands in the parent's mailbox.
    delivered = coordinator.runtimes["root"].mailbox
    assert len(delivered) == 1
    assert delivered[0]["filed_report_ids"] == [filed]
    # The child is completed but the scan stays open: the root is still running.
    assert coordinator.statuses["child"] == "completed"
    assert coordinator.statuses["root"] == "running"


@pytest.mark.asyncio
async def test_complete_child_frees_budget_and_wakes_budget_paused_peers() -> None:
    coordinator = AgentCoordinator()
    await coordinator.register("root", "strix", parent_id=None)
    await coordinator.register("child-a", "recon", parent_id="root")
    await coordinator.register("child-b", "recon", parent_id="root")
    # A peer is parked on the shared budget.
    await coordinator.pause_for_budget("child-b")
    assert coordinator.statuses["child-b"] == "budget_paused"
    coordinator.runtimes["child-b"].wake.clear()

    closed = await coordinator.complete_child("child-a")

    assert closed is True
    assert coordinator.statuses["child-a"] == "completed"
    # The completed child no longer holds an active claim on the budget...
    active = await coordinator.active_agents_except("root")
    assert "child-a" not in [a["agent_id"] for a in active]
    # ...and the budget-parked peer was nudged so it can reclaim the headroom.
    assert coordinator.runtimes["child-b"].wake.is_set()
    # Freeing a child's budget never flips the scan-wide budget flags.
    assert coordinator.budget_stopped is False
    assert coordinator.budget_paused is True  # peer still parked; scan not terminated


# --- Req 2.3 / 2.4 / 2.5 / 2.6: status vocabulary and transitions -----------------


def test_bellu_status_vocabulary_is_exactly_the_five_required_values() -> None:
    assert frozenset({"running", "waiting", "completed", "failed", "budget_paused"}) == (
        BELLU_STATUSES
    )
    # Internal-only terminal states project onto the required set.
    assert bellu_status("stopped") == "failed"
    assert bellu_status("crashed") == "failed"
    for status in BELLU_STATUSES:
        assert bellu_status(status) == status
        assert status in BELLU_STATUSES


@pytest.mark.asyncio
async def test_status_is_running_on_registration_and_waiting_while_awaiting() -> None:
    coordinator = await _graph()

    # Req 2.3: a freshly registered agent is running.
    assert await coordinator.bellu_status_of("root") == "running"

    # Req 2.4: awaiting a child/mailbox parks the agent as waiting.
    await coordinator.park_waiting("root", wait_kind="agents")
    assert coordinator.statuses["root"] == "waiting"
    assert await coordinator.bellu_status_of("root") == "waiting"

    # Req 2.5: receiving the awaited result returns the agent to running.
    await coordinator.mark_running("root")
    assert await coordinator.bellu_status_of("root") == "running"


@pytest.mark.asyncio
async def test_budget_paused_while_budget_exhausted() -> None:
    coordinator = await _graph()

    # Req 2.6: budget exhaustion parks the agent as budget_paused.
    await coordinator.pause_for_budget("root")
    assert coordinator.statuses["root"] == "budget_paused"
    assert await coordinator.bellu_status_of("root") == "budget_paused"
    assert coordinator.budget_paused is True


@pytest.mark.asyncio
async def test_every_coordinator_status_projects_into_the_bellu_set() -> None:
    coordinator = await _graph()
    for status in ("running", "waiting", "completed", "stopped", "crashed", "failed",
                   "budget_paused"):
        await coordinator.set_status("child", status)
        projected = await coordinator.bellu_status_of("child")
        assert projected in BELLU_STATUSES
