"""Tests for the safety-critical active-testing authorization gate.

This is the gate that stops the Docker sandbox from ever reaching a target
outside the run's declared authorization scope. Coverage (Requirement 8,
correctness Property 4):

* absent scope refuses everything (8.1, 8.3);
* ``active_testing=false`` refuses everything (8.1, 8.3);
* enabled + exact hostname match allowed, non-matching hostname refused
  (8.4, 8.5);
* enabled + IPv4 address-in-CIDR allowed, out-of-CIDR refused (8.4, 8.5);
* IPv6 single address and IPv6 network matching (8.4);
* a hostname must NOT match an IP CIDR, and an IP must NOT match a hostname
  matcher (type-aware matching, 8.5);
* every refusal is recorded with a reason (detailed Requirement 8).
"""

from __future__ import annotations

import pytest

from strix.runtime.active_testing_gate import ActiveTestingGate, RecordedRefusal


# --- default read-only: absent / disabled scope refuses everything ----------


def test_absent_scope_refuses_everything() -> None:
    gate = ActiveTestingGate(None)
    assert gate.enabled is False
    for target in ("example.com", "10.0.0.5", "2001:db8::1"):
        with pytest.raises(PermissionError, match="active testing not authorized"):
            gate.ensure_allowed(target)


def test_active_testing_false_refuses_everything() -> None:
    # Even with a populated allow list, a disabled flag means read-only.
    gate = ActiveTestingGate(
        {"active_testing": False, "allow_targets": ["example.com", "10.0.0.0/24"]},
    )
    assert gate.enabled is False
    with pytest.raises(PermissionError, match="active testing not authorized"):
        gate.ensure_allowed("example.com")
    with pytest.raises(PermissionError, match="active testing not authorized"):
        gate.ensure_allowed("10.0.0.5")


def test_missing_active_testing_key_defaults_to_disabled() -> None:
    gate = ActiveTestingGate({"allow_targets": ["example.com"]})
    assert gate.enabled is False
    with pytest.raises(PermissionError, match="active testing not authorized"):
        gate.ensure_allowed("example.com")


# --- enabled: hostname matching ---------------------------------------------


def test_enabled_exact_hostname_allowed() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["api.example.com"]},
    )
    assert gate.enabled is True
    # No exception == allowed.
    gate.ensure_allowed("api.example.com")
    assert gate.recorded_refusals == []


def test_enabled_hostname_match_is_case_insensitive() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["API.Example.COM"]},
    )
    gate.ensure_allowed("api.example.com")
    gate.ensure_allowed("API.EXAMPLE.COM")


def test_enabled_non_matching_hostname_refused() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["api.example.com"]},
    )
    with pytest.raises(PermissionError, match="outside the authorized scope"):
        gate.ensure_allowed("evil.example.com")


def test_hostname_not_a_substring_match() -> None:
    # "example.com" must not match "notexample.com" nor "example.com.evil.net".
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["example.com"]},
    )
    for bad in ("notexample.com", "example.com.evil.net", "example.co"):
        with pytest.raises(PermissionError, match="outside the authorized scope"):
            gate.ensure_allowed(bad)


# --- enabled: IPv4 address and CIDR matching --------------------------------


def test_enabled_ipv4_in_cidr_allowed() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["10.0.0.0/24"]},
    )
    for target in ("10.0.0.1", "10.0.0.5", "10.0.0.254"):
        gate.ensure_allowed(target)
    assert gate.recorded_refusals == []


def test_enabled_ipv4_out_of_cidr_refused() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["10.0.0.0/24"]},
    )
    for target in ("10.0.1.1", "192.168.0.1", "11.0.0.5"):
        with pytest.raises(PermissionError, match="outside the authorized scope"):
            gate.ensure_allowed(target)


def test_enabled_single_ipv4_address() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["203.0.113.7"]},
    )
    gate.ensure_allowed("203.0.113.7")
    with pytest.raises(PermissionError, match="outside the authorized scope"):
        gate.ensure_allowed("203.0.113.8")


# --- enabled: IPv6 address and network --------------------------------------


def test_enabled_single_ipv6_address() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["2001:db8::1"]},
    )
    # Equivalent compressed/expanded forms of the same address match.
    gate.ensure_allowed("2001:db8::1")
    gate.ensure_allowed("2001:0db8:0000:0000:0000:0000:0000:0001")
    with pytest.raises(PermissionError, match="outside the authorized scope"):
        gate.ensure_allowed("2001:db8::2")


def test_enabled_ipv6_network() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["2001:db8::/32"]},
    )
    gate.ensure_allowed("2001:db8::1")
    gate.ensure_allowed("2001:db8:ffff::abcd")
    with pytest.raises(PermissionError, match="outside the authorized scope"):
        gate.ensure_allowed("2001:db9::1")


# --- type-aware matching: hostname vs IP must not cross ---------------------


def test_hostname_does_not_match_ip_cidr() -> None:
    # A hostname target must never be considered "inside" an IP CIDR.
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["10.0.0.0/8"]},
    )
    with pytest.raises(PermissionError, match="outside the authorized scope"):
        gate.ensure_allowed("example.com")


def test_ip_does_not_match_hostname_allow_entry() -> None:
    # An IP target must never match a hostname matcher.
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["10.0.0.5"]},
    )
    # A hostname that textually resembles nothing; the allow entry is an IP.
    with pytest.raises(PermissionError, match="outside the authorized scope"):
        gate.ensure_allowed("host-named-10.0.0.5")


def test_mixed_allow_list_matches_each_kind() -> None:
    gate = ActiveTestingGate(
        {
            "active_testing": True,
            "allow_targets": ["api.example.com", "10.0.0.0/24", "2001:db8::5"],
        },
    )
    gate.ensure_allowed("api.example.com")
    gate.ensure_allowed("10.0.0.42")
    gate.ensure_allowed("2001:db8::5")
    with pytest.raises(PermissionError):
        gate.ensure_allowed("other.example.com")
    with pytest.raises(PermissionError):
        gate.ensure_allowed("10.0.1.1")


# --- refusals are observable with reasons -----------------------------------


def test_refusals_recorded_with_reason_when_disabled() -> None:
    gate = ActiveTestingGate(None)
    with pytest.raises(PermissionError):
        gate.ensure_allowed("example.com")
    refusals = gate.recorded_refusals
    assert refusals == [
        RecordedRefusal(
            target="example.com",
            reason="active testing not authorized for this run",
        ),
    ]


def test_refusals_recorded_with_reason_when_out_of_scope() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["api.example.com"]},
    )
    with pytest.raises(PermissionError):
        gate.ensure_allowed("evil.example.com")
    refusals = gate.recorded_refusals
    assert len(refusals) == 1
    assert refusals[0].target == "evil.example.com"
    assert "outside the authorized scope" in refusals[0].reason


def test_allowed_targets_record_no_refusal() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["api.example.com"]},
    )
    gate.ensure_allowed("api.example.com")
    assert gate.recorded_refusals == []


def test_recorded_refusals_accumulate_in_order() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["ok.example.com"]},
    )
    for bad in ("a.example.com", "b.example.com"):
        with pytest.raises(PermissionError):
            gate.ensure_allowed(bad)
    gate.ensure_allowed("ok.example.com")  # allowed, records nothing
    targets = [r.target for r in gate.recorded_refusals]
    assert targets == ["a.example.com", "b.example.com"]


def test_recorded_refusals_returns_defensive_copy() -> None:
    gate = ActiveTestingGate(None)
    with pytest.raises(PermissionError):
        gate.ensure_allowed("example.com")
    snapshot = gate.recorded_refusals
    snapshot.clear()
    # Mutating the returned list must not affect the gate's record.
    assert len(gate.recorded_refusals) == 1


# --- is_allowed predicate is side-effect-free -------------------------------


def test_is_allowed_predicate_no_side_effects() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["api.example.com"]},
    )
    assert gate.is_allowed("api.example.com") is True
    assert gate.is_allowed("evil.example.com") is False
    # Predicate must not record refusals.
    assert gate.recorded_refusals == []


def test_is_allowed_false_when_disabled() -> None:
    gate = ActiveTestingGate({"active_testing": False, "allow_targets": ["x"]})
    assert gate.is_allowed("x") is False


# --- scope compilation edge cases -------------------------------------------


def test_blank_allow_entries_skipped() -> None:
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["", "  ", "api.example.com"]},
    )
    gate.ensure_allowed("api.example.com")
    with pytest.raises(PermissionError):
        gate.ensure_allowed("")


def test_enabled_with_empty_allow_list_refuses_everything() -> None:
    # The control plane rejects enabling active testing with an empty list
    # (Requirement 8.2), but the gate must still fail safe if it ever sees one.
    gate = ActiveTestingGate({"active_testing": True, "allow_targets": []})
    with pytest.raises(PermissionError, match="outside the authorized scope"):
        gate.ensure_allowed("api.example.com")


def test_malformed_cidr_raises_at_construction() -> None:
    with pytest.raises(ValueError, match=r"10\.0\.0\.0/33"):
        ActiveTestingGate({"active_testing": True, "allow_targets": ["10.0.0.0/33"]})


def test_cidr_host_bits_tolerated() -> None:
    # 10.0.0.5/24 normalizes to the 10.0.0.0/24 network.
    gate = ActiveTestingGate(
        {"active_testing": True, "allow_targets": ["10.0.0.5/24"]},
    )
    gate.ensure_allowed("10.0.0.200")
    with pytest.raises(PermissionError):
        gate.ensure_allowed("10.0.1.1")


def test_non_list_allow_targets_raises() -> None:
    with pytest.raises(TypeError, match="allow_targets must be a list"):
        ActiveTestingGate({"active_testing": True, "allow_targets": "example.com"})
