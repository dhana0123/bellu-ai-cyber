"""Bellu-owned gate+resolver front for the forked Strix Docker sandbox.

This module (task 16, Requirements 8.3, 8.5, 3.2) is the safety seam that
guarantees the Docker sandbox can NEVER launch a container against a target
outside a run's declared authorization scope. It layers *on top of* the
existing forked-Strix sandbox lifecycle
(:func:`strix.runtime.session_manager.create_or_reuse`) rather than editing it:
every active-testing container launch is routed through
:class:`BelluSandbox`, which calls
:meth:`strix.runtime.active_testing_gate.ActiveTestingGate.ensure_allowed`
*before* delegating to the underlying launch.

Why a wrapper, not an edit to the upstream sandbox:
    The real Strix sandbox bring-up (``session_manager.create_or_reuse`` ->
    ``backends._docker_backend`` -> ``StrixDockerSandboxClient._create_container``
    -> ``docker_client.containers.create``) is a vendored copy of upstream
    internals we must manually re-merge on each Strix bump (see
    ``docker_client.py``'s header). Enforcing the gate by editing those
    internals would deepen the merge conflict surface and scatter the
    safety-critical check across copied code. Instead ``BelluSandbox`` *fronts*
    that machinery: it owns the gate check and the entity->target resolution,
    and only once a target is authorized does it call the existing launch. This
    keeps the Bellu edit isolated in one clearly-marked module.

Shape (from the design's ``DockerSandbox``):
    The design sketches::

        class DockerSandbox:
            def __init__(self, gate): self._gate = gate
            def run_active_test(self, target, plan):
                self._gate.ensure_allowed(target)   # refuses out-of-scope
                # ... spin up the container only against `target`

    :class:`BelluSandbox` adapts that shape to the forked sandbox: the launch
    step is the existing ``session_manager.create_or_reuse`` coroutine (injected
    so it stays testable without a Docker daemon), and ``run_active_test``
    additionally accepts a Bellu graph entity as the target, resolving it to a
    concrete reachable address via
    :mod:`strix.runtime.entity_target_resolver` before gating.

Default read-only (Requirement 8.1):
    ``BelluSandbox`` requires an :class:`ActiveTestingGate`. When the run has no
    scope / active testing disabled, the gate refuses every target, so the
    active path never launches a container. A caller that wants the default
    read-only behavior simply constructs the gate from a ``None``/disabled
    scope — there is no code path that launches a container without the gate
    permitting the concrete target first.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from strix.runtime.entity_target_resolver import (
    ResolvedEntityTarget,
    resolve_entity_target,
)


if TYPE_CHECKING:
    from strix.runtime.active_testing_gate import ActiveTestingGate


__all__ = [
    "BelluSandbox",
    "EntityRef",
    "ReasoningOnlyError",
    "SandboxLaunch",
]


# A launch is any coroutine that materializes a sandbox container for a scan and
# returns the session bundle. The default is
# ``session_manager.create_or_reuse``; tests inject a fake that records calls
# instead of talking to Docker. Keyword-only to match create_or_reuse's shape.
SandboxLaunch = Callable[..., Awaitable[dict[str, Any]]]


class ReasoningOnlyError(RuntimeError):
    """Raised when an entity target has no reachable address (reasoning-only).

    This is distinct from :class:`PermissionError`: a reasoning-only entity is
    not *refused* by the authorization gate, it simply has no network target an
    active test could run against (Requirement 3.2 fall-back). The caller should
    continue with read-only reasoning for that entity rather than treat it as a
    scope violation.
    """


@dataclass(frozen=True)
class EntityRef:
    """A Bellu graph entity presented as a scan target.

    ``kind`` is a ``model.Kind`` value (e.g. ``"NetworkEndpoint"``);
    ``attributes`` is the entity's attribute map, from which a reachable address
    may be resolved. ``canonical_id`` is carried for diagnostics/logging only.
    """

    kind: str
    attributes: dict[str, Any]
    canonical_id: str | None = None


class BelluSandbox:
    """Gate-enforcing, entity-aware front for the forked Strix Docker sandbox.

    Construct with an :class:`ActiveTestingGate` built from the run's
    authorization scope. Every active-testing launch goes through
    :meth:`run_active_test`, which calls ``gate.ensure_allowed(target)`` before
    the underlying launch, so a refused target never reaches
    ``docker ... containers.create``.

    The actual container bring-up is delegated to ``launch`` (defaults to
    :func:`strix.runtime.session_manager.create_or_reuse`), injected so the
    guard is unit-testable without a Docker daemon.
    """

    def __init__(
        self,
        gate: ActiveTestingGate,
        *,
        launch: SandboxLaunch | None = None,
    ) -> None:
        self._gate = gate
        self._launch = launch if launch is not None else _default_launch

    @property
    def gate(self) -> ActiveTestingGate:
        """The authorization gate enforced before every launch."""
        return self._gate

    def resolve_target(self, entity: EntityRef) -> ResolvedEntityTarget:
        """Resolve a Bellu entity to an optional reachable target (Req 3.2).

        Pure delegation to :func:`resolve_entity_target`; no gate check and no
        launch. A ``target`` of ``None`` means the entity is reasoning-only.
        """
        return resolve_entity_target(entity.kind, entity.attributes)

    async def run_active_test(
        self,
        target: str | EntityRef,
        *,
        scan_id: str,
        image: str,
        **launch_kwargs: Any,
    ) -> dict[str, Any]:
        """Launch the sandbox against ``target`` only if the gate permits it.

        ``target`` is either a concrete address string (raw target) or an
        :class:`EntityRef` (a Bellu graph entity). For an entity, the reachable
        address is resolved first (Req 3.2); an entity with no reachable target
        raises :class:`ReasoningOnlyError` and NO container is launched.

        The concrete target is then passed to ``gate.ensure_allowed``: if the
        run is not authorized for active testing (Req 8.3) or the target is
        outside the authorized scope (Req 8.5), the gate raises
        :class:`PermissionError` and this method does NOT call the underlying
        launch — no container is created for that target.

        Only after the gate permits the target is the underlying launch invoked
        and its session bundle returned.
        """
        concrete = self._concrete_target(target)

        # Safety-critical pre-check: the gate is the single enforcement point.
        # If it raises, we return before touching the launch, so no container
        # is ever created for a refused target (Req 8.3, 8.5).
        self._gate.ensure_allowed(concrete)

        return await self._launch(
            scan_id,
            image=image,
            target=concrete,
            **launch_kwargs,
        )

    def _concrete_target(self, target: str | EntityRef) -> str:
        """Reduce a target (raw string or entity) to a concrete address.

        A raw string is used verbatim (after a whitespace check). An entity is
        resolved via the resolver; an unreachable (reasoning-only) entity raises
        :class:`ReasoningOnlyError` so the caller falls back to reasoning only.
        """
        if isinstance(target, EntityRef):
            resolved = self.resolve_target(target)
            if resolved.target is None:
                ref = target.canonical_id or target.kind
                raise ReasoningOnlyError(
                    f"entity {ref!r} (kind {target.kind!r}) has no reachable "
                    "target; active testing not applicable, reasoning only",
                )
            return resolved.target

        concrete = target.strip()
        if not concrete:
            raise ValueError("active-test target must be a non-empty address")
        return concrete


async def _default_launch(scan_id: str, **kwargs: Any) -> dict[str, Any]:
    """Default launch: the forked Strix per-scan sandbox bring-up.

    Imported lazily so merely importing :mod:`strix.runtime.bellu_sandbox` does
    not pull in the Docker/session machinery (mirrors the lazy-import
    convention used across the runtime). ``target`` is accepted and dropped
    here: ``create_or_reuse`` keys its container on ``scan_id`` and does not
    take a per-target argument — the gate has already constrained which target
    the agent may exercise inside that sandbox. Only ``create_or_reuse``'s real
    parameters are forwarded.
    """
    from strix.runtime import session_manager

    kwargs.pop("target", None)
    return await session_manager.create_or_reuse(scan_id, **kwargs)
