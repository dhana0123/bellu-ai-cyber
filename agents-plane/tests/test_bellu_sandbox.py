"""Tests for the Bellu sandbox gate+resolver front (task 16).

Covers the safety contract that the Docker sandbox can never launch a container
against a target outside the run's authorization scope (Requirements 8.3, 8.5)
and the entity->reachable-target resolution (Requirement 3.2):

* an in-scope target passes the gate and the underlying launch IS invoked;
* an out-of-scope target raises ``PermissionError`` and NO launch occurs
  (asserted via a fake launcher that records every launch);
* an absent/disabled scope refuses the active path entirely (no launch);
* entity->target resolution yields a concrete target for a supported kind and
  ``None`` (reasoning-only) for an unsupported kind;
* an unreachable entity never reaches the gate or the launcher.

The fake launcher stands in for ``session_manager.create_or_reuse`` so these
tests exercise the full guard path without a Docker daemon: every recorded
launch is a container that *would* have been created.
"""

from __future__ import annotations

from typing import Any

import pytest

from strix.runtime.active_testing_gate import ActiveTestingGate
from strix.runtime.bellu_sandbox import (
    BelluSandbox,
    EntityRef,
    ReasoningOnlyError,
)
from strix.runtime.entity_target_resolver import (
    SUPPORTED_KINDS,
    resolve_entity_target,
)


class _RecordingLauncher:
    """Fake sandbox launch that records every (would-be) container launch.

    A recorded entry proves a container launch reached the backend. An empty
    record proves the gate blocked before any launch — the core safety
    assertion.
    """

    def __init__(self) -> None:
        self.launches: list[dict[str, Any]] = []

    async def __call__(self, scan_id: str, **kwargs: Any) -> dict[str, Any]:
        entry = {"scan_id": scan_id, **kwargs}
        self.launches.append(entry)
        return {"client": object(), "session": object(), "launched": entry}


def _enabled_gate(*targets: str) -> ActiveTestingGate:
    return ActiveTestingGate({"active_testing": True, "allow_targets": list(targets)})


# --- in-scope target launches ------------------------------------------------


async def test_in_scope_target_passes_gate_and_launches() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("api.example.com"), launch=launcher)

    bundle = await sandbox.run_active_test(
        "api.example.com",
        scan_id="scan-1",
        image="bellu/sandbox:latest",
    )

    assert len(launcher.launches) == 1
    launch = launcher.launches[0]
    assert launch["scan_id"] == "scan-1"
    assert launch["target"] == "api.example.com"
    assert launch["image"] == "bellu/sandbox:latest"
    assert bundle["launched"]["target"] == "api.example.com"
    assert sandbox.gate.recorded_refusals == []


async def test_in_scope_ip_in_cidr_launches() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("10.0.0.0/24"), launch=launcher)

    await sandbox.run_active_test("10.0.0.42", scan_id="s", image="img")

    assert [launch["target"] for launch in launcher.launches] == ["10.0.0.42"]


# --- out-of-scope target: refused, NO launch --------------------------------


async def test_out_of_scope_target_raises_and_never_launches() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("api.example.com"), launch=launcher)

    with pytest.raises(PermissionError, match="outside the authorized scope"):
        await sandbox.run_active_test("evil.example.com", scan_id="s", image="img")

    # The safety assertion: no container launch happened for the refused target.
    assert launcher.launches == []
    refusals = sandbox.gate.recorded_refusals
    assert len(refusals) == 1
    assert refusals[0].target == "evil.example.com"


async def test_out_of_scope_ip_raises_and_never_launches() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("10.0.0.0/24"), launch=launcher)

    with pytest.raises(PermissionError, match="outside the authorized scope"):
        await sandbox.run_active_test("192.168.1.1", scan_id="s", image="img")

    assert launcher.launches == []


# --- absent / disabled scope: active path refused ---------------------------


async def test_absent_scope_refuses_active_path_no_launch() -> None:
    launcher = _RecordingLauncher()
    # No scope at all => default read-only: the gate refuses every target.
    sandbox = BelluSandbox(ActiveTestingGate(None), launch=launcher)

    with pytest.raises(PermissionError, match="active testing not authorized"):
        await sandbox.run_active_test("api.example.com", scan_id="s", image="img")

    assert launcher.launches == []


async def test_disabled_scope_refuses_active_path_no_launch() -> None:
    launcher = _RecordingLauncher()
    gate = ActiveTestingGate({"active_testing": False, "allow_targets": ["api.example.com"]})
    sandbox = BelluSandbox(gate, launch=launcher)

    with pytest.raises(PermissionError, match="active testing not authorized"):
        await sandbox.run_active_test("api.example.com", scan_id="s", image="img")

    assert launcher.launches == []


# --- entity -> target resolution (Requirement 3.2) --------------------------


def test_supported_kind_resolves_to_target() -> None:
    resolved = resolve_entity_target(
        "NetworkEndpoint",
        {"host": "db.internal.example.com"},
    )
    assert resolved.reachable is True
    assert resolved.target == "db.internal.example.com"
    assert resolved.source_attr == "host"


def test_entrypoint_prefers_url_attribute() -> None:
    resolved = resolve_entity_target(
        "EntryPoint",
        {"host": "ignored.example.com", "url": "https://api.example.com/v1"},
    )
    # url is higher priority than host.
    assert resolved.target == "https://api.example.com/v1"
    assert resolved.source_attr == "url"


def test_unsupported_kind_resolves_to_none_reasoning_only() -> None:
    for kind in ("Identity", "Package", "Vulnerability", "Prompt", "DataStore"):
        resolved = resolve_entity_target(kind, {"host": "should.be.ignored"})
        assert resolved.reachable is False
        assert resolved.target is None


def test_compute_private_only_is_reasoning_only() -> None:
    # A Compute instance with only a private IP has no reachable public target.
    resolved = resolve_entity_target("Compute", {"private_ip": "10.0.0.5"})
    assert resolved.target is None


def test_compute_public_ip_resolves() -> None:
    resolved = resolve_entity_target(
        "Compute",
        {"private_ip": "10.0.0.5", "public_ip": "203.0.113.9"},
    )
    assert resolved.target == "203.0.113.9"
    assert resolved.source_attr == "public_ip"


def test_supported_kind_without_address_is_reasoning_only() -> None:
    resolved = resolve_entity_target("ExternalService", {"name": "stripe"})
    assert resolved.target is None


def test_supported_kinds_membership() -> None:
    assert "NetworkEndpoint" in SUPPORTED_KINDS
    assert "EntryPoint" in SUPPORTED_KINDS
    assert "ExternalService" in SUPPORTED_KINDS
    assert "Compute" in SUPPORTED_KINDS
    assert "Identity" not in SUPPORTED_KINDS


# --- entity target end-to-end through the sandbox ---------------------------


async def test_entity_target_in_scope_resolves_and_launches() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("api.example.com"), launch=launcher)
    entity = EntityRef(
        kind="EntryPoint",
        attributes={"url": "api.example.com"},
        canonical_id="EntryPoint:aws:apigw-123",
    )

    await sandbox.run_active_test(entity, scan_id="s", image="img")

    assert [launch["target"] for launch in launcher.launches] == ["api.example.com"]


async def test_entity_target_out_of_scope_resolves_but_refused() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("allowed.example.com"), launch=launcher)
    entity = EntityRef(
        kind="NetworkEndpoint",
        attributes={"host": "forbidden.example.com"},
    )

    with pytest.raises(PermissionError, match="outside the authorized scope"):
        await sandbox.run_active_test(entity, scan_id="s", image="img")

    # Resolution succeeded, but the gate still refused and nothing launched.
    assert launcher.launches == []


async def test_reasoning_only_entity_never_reaches_gate_or_launch() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("anything.example.com"), launch=launcher)
    entity = EntityRef(
        kind="Identity",
        attributes={"arn": "arn:aws:iam::123:user/alice"},
        canonical_id="Identity:aws:AIDA123",
    )

    with pytest.raises(ReasoningOnlyError, match="no reachable target"):
        await sandbox.run_active_test(entity, scan_id="s", image="img")

    # No launch, and the gate recorded nothing (we never even asked it).
    assert launcher.launches == []
    assert sandbox.gate.recorded_refusals == []


async def test_blank_raw_target_rejected_before_gate() -> None:
    launcher = _RecordingLauncher()
    sandbox = BelluSandbox(_enabled_gate("api.example.com"), launch=launcher)

    with pytest.raises(ValueError, match="non-empty address"):
        await sandbox.run_active_test("   ", scan_id="s", image="img")

    assert launcher.launches == []
