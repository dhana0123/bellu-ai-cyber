# Bellu Agents Plane

The **agents plane** is the Python subsystem of the Bellu platform that runs
Strix-style autonomous pentest agents. It is a separately deployable
process/container that layers *on top of* Bellu's authoritative Go control
plane, talking to it only over authenticated HTTP + MCP (never directly to
Postgres or Memgraph).

See the spec for the full architecture:
`.kiro/specs/bellu-agents-plane/{requirements,design,tasks}.md`.

## This is a hard fork of Strix

This directory is a **hard fork of the open-source Strix agent engine**, vendored
in place rather than consumed as a dependency.

- **Upstream project:** https://github.com/usestrix/strix
- **License:** Apache-2.0 (preserved in `LICENSE`)
- **Fork point (upstream commit):** `594398876f9cd465f00cce626d0cb98bfa5e1662`
  (upstream `HEAD`, authored 2026-10-22)
- **Vendored at:** task 7.1 of the `bellu-agents-plane` spec

### Hard-fork maintenance tradeoff

Because the engine is forked and **edited in place**, upstream Strix
improvements do **not** arrive automatically — they must be **manually merged**.
We accept this tradeoff deliberately: it gives us the control needed to make the
engine Bellu-graph-aware (the Bellu MCP tool, the graph-context provider, the
findings write-back tool, and the per-run authorization gate for active
testing).

Operational guidance:

- The fork point commit above is the baseline. When reviewing upstream for
  security-relevant fixes, diff against that commit and cherry-pick
  deliberately.
- Keep Bellu-specific edits isolated and clearly marked so future merges are
  tractable.

## What was vendored

The full upstream `strix/` package tree is vendored so the engine is
import-clean and runnable standalone:

| Package | Role in the design |
|---------|--------------------|
| `strix/agents/` | Agent definitions, factory, prompts |
| `strix/core/` | Coordinator / agent-tree ownership, runner, execution, sessions, hooks |
| `strix/llm/` | LLM provider route, context compaction, context budget |
| `strix/runtime/` | Docker sandbox backend + session management |
| `strix/report/` | Finding/report state, dedupe, writers, usage |
| `strix/tools/` | Agent tools (shell, proxy, mcp, reporting, finish, …) |
| `strix/config/` | Settings, model config, tool-call limits/ids/arguments |

Supporting packages the engine imports are also vendored: `strix/skills/`
(skill data), `strix/utils/`, `strix/telemetry/`.

The upstream **CLI-scanner skin** (`strix/interface/`) is kept intentionally:
the design notes it may stay for standalone / debug use. The `strix` console
entry point (`strix.interface.main:main`) therefore still works. Bellu
deployment does not depend on it.

> Note on the coordinator: the design refers to an "AgentCoordinator" under
> `core/coordinator`. In this upstream snapshot the coordinator / agent-tree
> ownership logic lives in `strix/core/` (see `core/agents.py`, `core/runner.py`,
> and the `agents_graph` tool). Later tasks (e.g. task 13) layer Bellu's
> completion-boundary enforcement on top of that code; task 7.2 runs the
> vendored coordination tests (`tests/test_agent_graph_coordination.py`) to
> establish a known-good baseline.

## Dependencies & environment

- Python **>= 3.12** (upstream pins 3.12; 3.12–3.14 supported).
- Managed with [`uv`](https://docs.astral.sh/uv/). Dependencies are pinned in
  `pyproject.toml` and locked in `uv.lock` (upstream lock preserved, including
  the supply-chain `exclude-newer` cooldown).

### Setup

```bash
cd agents-plane
uv sync            # create the venv and install the locked dependency set
uv run python -c "import strix; print('strix import OK')"
```

### Tests

```bash
uv run pytest tests/            # full vendored suite
uv run pytest tests/test_agent_graph_coordination.py   # coordinator baseline (task 7.2)
```

## Bellu-specific additions (later tasks)

These are *not* part of task 7.1 (which only establishes the fork). They land in
later spec tasks:

- Bellu control-plane config + startup health check (task 8) — **landed**, see
  `strix/config/bellu.py`:
  - `BELLU_API_BASE` — control-plane base URL (default `http://localhost:8080`).
  - `BELLU_SERVICE_ACCOUNT_TOKEN` — bearer token for the `agent_service_account`
    principal; **env-only, never hardcoded**. The control plane resolves the org
    from this principal server-side, so the plane sends no org anywhere.
  - `BELLU_HEALTH_TIMEOUT` — timeout (seconds) for the startup health check.
  - The **LLM model route is not reinvented**: the plane reuses Strix's existing
    model config (`STRIX_LLM` via `LlmSettings` / `StrixProvider`).
    `strix.config.selected_model()` surfaces the configured id.
  - `strix.config.health_check()` verifies connectivity to the control plane
    (`GET {BELLU_API_BASE}/api/health`, Bellu's public health endpoint) and that
    a model route is configured, returning a `HealthResult` healthy/unhealthy
    verdict.
- `bellu_mcp` tool — the six read-only MCP tools at `POST /api/mcp/*` (task 9)
- `graph_context` provider — inject the bounded initial subgraph (task 10)
- `finding_report` tool — write findings back via `findings:ingest` (task 11)
- Scan runner dispatch + coordinator completion boundary (tasks 12–13)
- Authorization-gated sandbox active testing (tasks 15–16)
