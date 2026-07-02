# Decisions

## Cycle Preflight Boundary

- Date: 2026-06-29
- Milestone: 0015-cycle-preflight-review

Cycle preflight review is a TUI-only, non-mutating step. It may read settings,
agents, tracked repository Git status, and current state, but it must not call
executor cycle preparation, write reports or metadata, save state, or change Git
branches. Confirming preflight emits the existing `StartCycleMsg` so RootModel's
current cycle execution path remains the single executor startup path.

## First-Run Setup Persistence Boundary

- Date: 2026-06-29
- Milestone: 0016-first-run-setup-wizard

First-run setup is owned by the TUI RootModel missing-config path and remains
non-mutating until final confirmation. Confirmed setup writes the milestone
index, path-adjacent project settings, state file, milestones directory, and
optional first milestone through existing config/state persistence helpers, then
reloads config and state before returning to the dashboard.

## Agent Output Contract Compatibility

- Date: 2026-06-29
- Milestone: 0018-agent-output-contracts

Built-in developer, QA, and recommender agents opt in to structured output
contracts through agent frontmatter. Contract validation uses the final fenced
`json` block only and persists validation metadata on existing phase handoff
JSON artifacts. Legacy and custom agents without `output_contract` continue to
use the generic handoff fallback path. Invalid developer output maps to failed;
invalid QA output or QA verdicts requiring review map to blocked; recommender
scores prefer structured handoff data and retain the legacy text marker
fallback for older logs.

Cycle 2 clarification: `EnableCompactPhaseHandoffs=false` disables compact
phase-input summaries and uncontracted fallback handoff persistence, but does
not disable validation or handoff persistence for agents with explicit
`output_contract` frontmatter. The recommender contract requires string
`verdict` alongside integer `score`, string `reason`, and `next_cycle_focus`.

## VS Code Integrated Terminal Compatibility Safeguards

- Date: 2026-06-30
- Milestone: 0019-vscode-tui-compatibility-safeguards

VS Code integrated terminal has specific rendering limitations and font quirks that trigger glitches in Bubble Tea TUIs. To safeguard rendering and prevent future regressions, Cyclestone defaults to:
1. Disabling bold styles (`TERM_PROGRAM == "vscode"` auto-detected via `DefaultDisableBoldForEnvironment`).
2. Disabling rounded borders in favor of normal rectangular borders (`TERM_PROGRAM == "vscode"` auto-detected via `DefaultDisableRoundedBordersForEnvironment`).
3. Selecting ASCII-safe glyphs (such as `>` and `*`) instead of Unicode characters (`›` and `◆`).
4. Returning a `WindowSizeMsg` immediately on startup using `initialWindowSizeCmd` (and falling back to a default `80x24` size if querying fails) to prevent layout initialization hangs.

Developers must preserve these safeguards and explanatory documentation to protect against rendering/alignment regressions.

## Workspace Root Portability and Absolute Path Prevention

- Date: 2026-07-01
- Milestone: 0021-prevent-absolute-paths

To guarantee environment independence and portability of the project configuration files, hardcoded absolute paths are prohibited in AI_CONTEXT.md and DECISIONS.md. A portable placeholder `{{WORKSPACE_ROOT}}` is used instead, which the prompt constructor dynamically replaces with the absolute runtime workspace root. In addition, Project Manager and Developer agent prompts have explicit rules instructing them to avoid hardcoding absolute paths and to use relative paths or `{{WORKSPACE_ROOT}}` instead.

## Test Milestone Completion

- Date: 2026-07-01
- Milestone: 0022-just-create-test-milestone

The test milestone "0022-just-create-test-milestone" has been completed. Its goal was simply to create a test milestone without content, which has been achieved. The invalid QA output in cycle 1 doesn't indicate incomplete work on the milestone itself, but rather an issue with the QA agent's output format. Since the milestone has no acceptance criteria to verify, no additional cycles are needed for this milestone.

## YAML Agent Cycle Reports

- Date: 2026-07-02
- Milestone: 0023-transition-cycle-reports-to-yaml

Built-in PM, Developer, QA, and Recommender agents now emit final structured
YAML documents for their output contracts. Contract extraction accepts raw YAML
documents and fenced `yaml`/`yml` blocks, with fenced `json` retained only as a
YAML-compatible legacy input. Main cycle report files are generated with the
`.yaml` extension and store cycle metadata as YAML fields with detailed phase
logs nested under a block scalar. Internal phase handoff artifacts and
`.cyclestone/state.json` remain JSON for compatibility with existing state and
TUI integration.

## Supported Runner Boundary

- Date: 2026-07-02
- Milestone: runner-selection-consolidation

Cyclestone exposes and supports only four LLM runners in user-facing selection
surfaces and executor dispatch: `codex`, `agy`, `aider`, and `ollama`.
Milestone creation, milestone cycle details, settings, first-run setup, runner
availability checks, and preflight validation must use the same supported set.

`ollama` is executed through the Aider CLI, not through a native Ollama API
client. Plain Ollama model names are converted to Aider's `ollama_chat/<model>`
form, `OLLAMA_API_BASE` is set from `ollama_host`, and `ollama_num_ctx` /
`ollama_num_predict` are written to temporary Aider model settings as
`num_ctx` / `num_predict`. The default Ollama model is
`qwen3-coder:480b-cloud`.

Direct provider API runners (`gemini`, `openai`, `anthropic`, `ollama_api`) and
custom runner script paths are intentionally unsupported. They may remain in
negative tests only to verify rejection and migration behavior.
