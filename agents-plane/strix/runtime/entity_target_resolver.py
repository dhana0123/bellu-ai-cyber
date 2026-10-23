"""Bellu entity -> reachable-target resolution for the sandbox active path.

This is a Bellu-owned addition to the forked Strix runtime (task 16,
Requirement 3.2). It is a small, pure, side-effect-free resolver: given a
Bellu graph entity (a canonical id ``kind:provider:native-id`` plus its
attributes), it returns a single concrete, network-reachable target string
(host / IP / URL) that the Docker sandbox could point an active test at, or
``None`` when the entity has no such reachable target.

Why a separate module (upstream-merge isolation):
    The resolver is pure Bellu domain knowledge about Bellu's canonical graph
    kinds; it does not belong in upstream Strix. Keeping it in its own module
    (imported by :mod:`strix.runtime.bellu_sandbox`) keeps the Bellu edit
    surface small and clearly marked for future upstream merges.

Safety role (why ``None`` matters):
    Returning ``None`` is the signal that an entity is *reasoning-only*: there
    is no reachable target, so the sandbox active path must not run for it. The
    resolver NEVER invents or guesses a target — it only reports an address the
    entity actually carries. The authorization gate
    (:class:`strix.runtime.active_testing_gate.ActiveTestingGate`) is still the
    sole enforcement point; this resolver only decides *what*, if anything, the
    concrete target would be. A resolved target is handed to the gate, which may
    still refuse it if it is outside the run's authorization scope.

Supported kinds (kinds that can map to a reachable network target):
    * ``NetworkEndpoint`` — a concrete host/port the account exposes;
    * ``EntryPoint``      — a resource that accepts outside traffic (e.g. an
      API Gateway endpoint);
    * ``ExternalService`` — a third-party service addressed by host/URL;
    * ``Compute``         — a VM / instance with a public address, *only* when
      it carries one (a private-only instance resolves to ``None``).

Every other kind (``Identity``, ``Package``, ``Vulnerability``, ``Prompt``,
``CloudAccount``, ``DataStore``, ``Cluster``, ``Workload``, ``ContainerImage``,
``Repository``, ``AIAgent``, ``AIModel``, ``MCPServer``, ``AIService``,
``Network``) is reasoning-only and resolves to ``None``.

Attribute probing:
    A reachable target may be recorded under any of several attribute names
    depending on the collector, so the resolver probes a small ordered list of
    well-known keys (``url``, ``public_url``, ``endpoint``, ``public_ip``,
    ``public_dns``, ``dns_name``, ``hostname``, ``host``, ``fqdn``,
    ``ip_address``, ``address``). The first non-empty, usable value wins. For
    ``Compute`` only public/reachable keys are consulted so a private-only
    instance is never surfaced as an active-test target.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any


__all__ = [
    "SUPPORTED_KINDS",
    "ResolvedEntityTarget",
    "resolve_entity_target",
    "split_canonical_id",
]


# Graph kinds that can carry a concrete, network-reachable target. Mirrors the
# Go model.Kind vocabulary (internal/model/model.go). Kept as a frozenset so a
# membership test is O(1) and the set is immutable.
SUPPORTED_KINDS: frozenset[str] = frozenset(
    {
        "NetworkEndpoint",
        "EntryPoint",
        "ExternalService",
        "Compute",
    }
)

# Attribute keys that may hold a reachable address, in priority order. A URL is
# preferred over a bare host so the sandbox keeps any scheme/port the graph
# recorded; an explicit public address is preferred over a generic one.
_GENERAL_TARGET_KEYS: tuple[str, ...] = (
    "url",
    "public_url",
    "endpoint",
    "public_ip",
    "public_dns",
    "dns_name",
    "hostname",
    "host",
    "fqdn",
    "ip_address",
    "address",
)

# For Compute, only keys that denote a PUBLIC / externally reachable address are
# consulted. A private-only instance (only private_ip) must resolve to None so
# the sandbox never targets an address it could not legitimately reach from
# outside the account boundary.
_COMPUTE_PUBLIC_KEYS: tuple[str, ...] = (
    "public_url",
    "url",
    "public_ip",
    "public_dns",
    "public_hostname",
)


@dataclass(frozen=True)
class ResolvedEntityTarget:
    """The outcome of resolving an entity to a reachable target.

    ``target`` is the concrete host/IP/URL the sandbox would point at, or
    ``None`` when the entity is reasoning-only (unsupported kind, or a supported
    kind with no reachable address recorded). ``kind`` and ``source_attr`` are
    carried for observability: ``source_attr`` names the attribute the target
    came from (``None`` when unresolved).
    """

    kind: str
    target: str | None
    source_attr: str | None = None

    @property
    def reachable(self) -> bool:
        """True when a concrete reachable target was resolved."""
        return self.target is not None


def split_canonical_id(canonical_id: str) -> tuple[str, str, str] | None:
    """Split a canonical id ``kind:provider:native-id`` into its three parts.

    The native id itself may contain colons (e.g. an ARN), so only the first
    two colons are significant: the result is ``(kind, provider, native_id)``
    where ``native_id`` is the remainder verbatim. Returns ``None`` when the
    value does not have at least a ``kind:provider:native-id`` shape or any of
    the three leading parts is empty.
    """
    parts = canonical_id.strip().split(":", 2)
    if len(parts) < 3:
        return None
    kind, provider, native_id = parts[0].strip(), parts[1].strip(), parts[2].strip()
    if not kind or not provider or not native_id:
        return None
    return kind, provider, native_id


def _first_usable(attributes: dict[str, Any], keys: tuple[str, ...]) -> tuple[str, str] | None:
    """Return the first ``(key, value)`` whose attribute holds a usable target.

    A usable value is a non-empty string once stripped. Non-string values
    (numbers, nested objects) are ignored: a reachable target is always a
    textual host/IP/URL. Returns ``None`` when no key yields a usable value.
    """
    for key in keys:
        if key not in attributes:
            continue
        raw = attributes[key]
        if not isinstance(raw, str):
            continue
        value = raw.strip()
        if value:
            return key, value
    return None


def resolve_entity_target(
    kind: str,
    attributes: dict[str, Any] | None = None,
) -> ResolvedEntityTarget:
    """Resolve a Bellu graph entity to an optional reachable target.

    Pure and side-effect-free. Given the entity ``kind`` (a ``model.Kind``
    value such as ``"NetworkEndpoint"``) and its ``attributes`` map, return a
    :class:`ResolvedEntityTarget`:

    * For an unsupported kind (anything not in :data:`SUPPORTED_KINDS`), the
      result has ``target=None`` (reasoning-only). The gate/sandbox must not run
      an active test for it.
    * For ``Compute``, only PUBLIC/reachable attribute keys are consulted, so a
      private-only instance resolves to ``target=None``.
    * For every other supported kind, the first non-empty well-known address
      attribute is used.

    The resolver never fabricates a target: a supported kind with no usable
    address attribute also resolves to ``target=None``.
    """
    kind = kind.strip()
    attrs = attributes or {}

    if kind not in SUPPORTED_KINDS:
        return ResolvedEntityTarget(kind=kind, target=None)

    keys = _COMPUTE_PUBLIC_KEYS if kind == "Compute" else _GENERAL_TARGET_KEYS
    found = _first_usable(attrs, keys)
    if found is None:
        return ResolvedEntityTarget(kind=kind, target=None)

    source_attr, value = found
    return ResolvedEntityTarget(kind=kind, target=value, source_attr=source_attr)


def resolve_canonical_entity_target(
    canonical_id: str,
    attributes: dict[str, Any] | None = None,
) -> ResolvedEntityTarget:
    """Resolve from a full canonical id, extracting the kind for the caller.

    Convenience wrapper over :func:`resolve_entity_target` for callers that hold
    a canonical id (``kind:provider:native-id``) rather than a separate kind. A
    malformed canonical id resolves to ``target=None`` under an empty kind.
    """
    split = split_canonical_id(canonical_id)
    if split is None:
        return ResolvedEntityTarget(kind="", target=None)
    kind, _provider, _native = split
    return resolve_entity_target(kind, attributes)
