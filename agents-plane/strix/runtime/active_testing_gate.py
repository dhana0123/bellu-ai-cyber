"""Authorization-scope gating for the sandbox active-testing path.

This module implements :class:`ActiveTestingGate`, the single enforcement point
that decides whether a sandbox action is permitted for a run. It is the
safety-critical gate that prevents the Docker sandbox from ever reaching a
target outside the run's declared authorization scope.

Default behavior is read-only reasoning. If a run carries no scope, or the
scope does not enable active testing, the gate refuses every sandbox request
(Requirement 8.3). When active testing is enabled, only targets matched by the
run's ``allow_targets`` are permitted; everything else is refused and the
refusal is recorded (Requirements 8.4, 8.5).

An ``allow_targets`` entry is one of:

* a hostname — matched exactly, case-insensitively (e.g. ``api.example.com``);
* a single IPv4 or IPv6 address (e.g. ``10.0.0.5`` or ``2001:db8::1``);
* a CIDR network (e.g. ``10.0.0.0/24`` or ``2001:db8::/32``), matched by
  containment.

Matching is type-aware: an IP target is only ever matched against IP and CIDR
matchers, and a hostname target is only ever matched against hostname matchers.
A hostname never matches a CIDR, and an IP never matches a hostname matcher.

This module is the gate ONLY. Wiring it into ``DockerSandbox`` is task 16: the
sandbox must call :meth:`ActiveTestingGate.ensure_allowed` before launching any
container, so that refused targets never result in a container launch. The gate
depends only on the Python standard library (``ipaddress``); it introduces no
new third-party dependencies.

Correctness Property 4 (Authorization-scope gating):
    ``executed(action on target t) => scope.active_testing and t in allow_targets``;
    absent scope implies no active testing at all.
"""

from __future__ import annotations

import ipaddress
from dataclasses import dataclass, field
from typing import cast


__all__ = [
    "ActiveTestingGate",
    "RecordedRefusal",
]


# An allow_targets entry compiles to one of three matcher shapes:
#   * a hostname string (compared case-insensitively, exact match),
#   * a single IP address (IPv4Address | IPv6Address),
#   * an IP network (IPv4Network | IPv6Network) matched by containment.
_IPAddress = ipaddress.IPv4Address | ipaddress.IPv6Address
_IPNetwork = ipaddress.IPv4Network | ipaddress.IPv6Network


@dataclass(frozen=True)
class _HostnameMatcher:
    """Matches a target hostname exactly, case-insensitively."""

    # Stored already lower-cased so comparison is a plain equality check.
    hostname: str


@dataclass(frozen=True)
class _AddressMatcher:
    """Matches a target IP that equals this single address."""

    address: _IPAddress


@dataclass(frozen=True)
class _NetworkMatcher:
    """Matches a target IP contained in this CIDR network."""

    network: _IPNetwork


_Matcher = _HostnameMatcher | _AddressMatcher | _NetworkMatcher


@dataclass(frozen=True)
class RecordedRefusal:
    """An observable record that the gate refused a target, with the reason.

    ``target`` is the raw value the caller tried to reach; ``reason`` is a
    human-readable explanation (the same text carried by the raised
    :class:`PermissionError`).
    """

    target: str
    reason: str


def _compile_targets(entries: list[str]) -> list[_Matcher]:
    """Compile ``allow_targets`` entries into matchers.

    Each entry is parsed, in order, as:

    1. a CIDR network (contains ``/``) via :func:`ipaddress.ip_network`;
    2. a single IP address via :func:`ipaddress.ip_address`;
    3. otherwise, a hostname (stored lower-cased for case-insensitive compare).

    Blank/whitespace-only entries are skipped. An entry that looks like a
    network or address but does not parse raises :class:`ValueError`, so a
    malformed scope fails loudly at construction rather than silently widening
    or narrowing what the sandbox may reach.
    """
    matchers: list[_Matcher] = []
    for raw in entries:
        entry = raw.strip()
        if not entry:
            continue

        if "/" in entry:
            # A CIDR network. strict=False tolerates host bits being set
            # (e.g. 10.0.0.5/24), normalizing to the containing network.
            network = ipaddress.ip_network(entry, strict=False)
            matchers.append(_NetworkMatcher(network=network))
            continue

        try:
            address = ipaddress.ip_address(entry)
        except ValueError:
            # Not an IP literal: treat as a hostname (exact, case-insensitive).
            matchers.append(_HostnameMatcher(hostname=entry.lower()))
        else:
            matchers.append(_AddressMatcher(address=address))

    return matchers


def _target_matches(target: str, allow: list[_Matcher]) -> bool:
    """Return whether ``target`` is covered by any matcher in ``allow``.

    Matching is type-aware:

    * If ``target`` parses as an IP address, it is checked against address
      matchers (exact equality) and network matchers (containment). It is
      never matched against hostname matchers.
    * Otherwise ``target`` is treated as a hostname and checked against
      hostname matchers only (exact, case-insensitive). A hostname is never
      matched against an address or CIDR matcher.
    """
    stripped = target.strip()

    try:
        target_ip: _IPAddress | None = ipaddress.ip_address(stripped)
    except ValueError:
        target_ip = None

    if target_ip is not None:
        for matcher in allow:
            if isinstance(matcher, _AddressMatcher) and matcher.address == target_ip:
                return True
            if isinstance(matcher, _NetworkMatcher) and target_ip in matcher.network:
                return True
        return False

    target_host = stripped.lower()
    return any(
        isinstance(matcher, _HostnameMatcher) and matcher.hostname == target_host
        for matcher in allow
    )


@dataclass
class ActiveTestingGate:
    """Enforces per-run authorization scope before any sandbox action.

    Construct with the run's authorization scope (the ``AuthzScope`` object
    carried by the launch request, as a plain ``dict``), or ``None`` when the
    run carries no scope. The scope shape is::

        {"active_testing": bool, "allow_targets": ["host-or-ip-or-cidr", ...]}

    Task 16 wires this gate into ``DockerSandbox``: the sandbox MUST call
    :meth:`ensure_allowed` with the concrete target before launching any
    container, and must not launch a container when ``ensure_allowed`` raises.
    """

    _enabled: bool = field(init=False)
    _allow: list[_Matcher] = field(init=False)
    _recorded_refusals: list[RecordedRefusal] = field(init=False)

    def __init__(self, scope: dict[str, object] | None) -> None:
        self._enabled = bool(scope and scope.get("active_testing"))
        raw_targets: object = scope.get("allow_targets", []) if scope else []
        if not isinstance(raw_targets, list):
            raise TypeError("allow_targets must be a list of strings")
        items = cast("list[object]", raw_targets)
        entries: list[str] = [str(t) for t in items]
        self._allow = _compile_targets(entries)
        self._recorded_refusals = []

    @property
    def enabled(self) -> bool:
        """Whether active testing is enabled for this run."""
        return self._enabled

    def is_allowed(self, target: str) -> bool:
        """Return whether ``target`` would be permitted, without recording.

        This is a side-effect-free predicate: it neither raises nor records a
        refusal. Use :meth:`ensure_allowed` at the enforcement point.
        """
        return self._enabled and _target_matches(target, self._allow)

    def ensure_allowed(self, target: str) -> None:
        """Enforce the scope for ``target``; raise and record on refusal.

        Raises :class:`PermissionError` when the run is not authorized for
        active testing at all (Requirement 8.3) or when ``target`` is outside
        the authorized scope (Requirement 8.5). Every refusal is appended to
        :attr:`recorded_refusals` so refusals are observable.
        """
        if not self._enabled:
            reason = "active testing not authorized for this run"
            self._recorded_refusals.append(RecordedRefusal(target=target, reason=reason))
            raise PermissionError(reason)
        if not _target_matches(target, self._allow):
            reason = f"target {target!r} is outside the authorized scope"
            self._recorded_refusals.append(RecordedRefusal(target=target, reason=reason))
            raise PermissionError(reason)

    @property
    def recorded_refusals(self) -> list[RecordedRefusal]:
        """The refusals recorded so far, in the order they occurred.

        Returns a copy so callers cannot mutate the gate's internal record.
        """
        return list(self._recorded_refusals)
