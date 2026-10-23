"""Strix application settings.

Public surface:

- :class:`Settings` — composite model. Get via :func:`load_settings`.
- :class:`LlmSettings`, :class:`RuntimeSettings`, :class:`TelemetrySettings`,
  :class:`IntegrationSettings` — sub-models, attribute-accessed off
  ``Settings``.
- :func:`load_settings` — memoized resolve (env > JSON file > defaults).
- :func:`apply_config_override` — switch the JSON source to a custom path.
- :func:`persist_current` — write currently-set env vars to the active file.

Bellu control-plane surface (agents-plane specific):

- :class:`BelluSettings` — control-plane base URL + service-account token
  (env-only). Get via :func:`load_bellu_settings`.
- :func:`selected_model` — the LLM route the plane uses (reuses ``STRIX_LLM``).
- :func:`health_check` / :class:`HealthResult` — startup connectivity + model
  route check against the control plane.
"""

from strix.config.loader import (
    apply_config_override,
    load_settings,
    persist_current,
)
from strix.config.settings import (
    ContextSettings,
    DedupeSettings,
    IntegrationSettings,
    LlmSettings,
    RuntimeSettings,
    Settings,
    TelemetrySettings,
)


__all__ = [
    "BelluSettings",
    "ContextSettings",
    "DedupeSettings",
    "HealthResult",
    "IntegrationSettings",
    "LlmSettings",
    "RuntimeSettings",
    "Settings",
    "TelemetrySettings",
    "apply_config_override",
    "health_check",
    "load_bellu_settings",
    "load_settings",
    "persist_current",
    "selected_model",
]


def __getattr__(name: str) -> object:
    # The Bellu symbols live in ``strix.config.bellu``, imported lazily so the
    # core config package has no new import-time cost or httpx dependency.
    if name in {
        "BelluSettings",
        "HealthResult",
        "health_check",
        "load_bellu_settings",
        "selected_model",
    }:
        from strix.config import bellu

        return getattr(bellu, name)
    msg = f"module {__name__!r} has no attribute {name!r}"
    raise AttributeError(msg)
