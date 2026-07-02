# AI Context

## Project Purpose

AiCoordinator coordinates local PM, Developer, QA, and recommender agent cycles for milestone-driven work. It captures cycle reports, branch snapshots, and repository state while preserving the active repository boundaries.

## Active Source Roots

- `cmd/cyclestone`: CLI entrypoint.
- `internal/config`: persisted settings, milestone state, and configuration loading.
- `internal/executor`: milestone cycle execution, API/CLI runner orchestration, reports, and git/package checks.
- `internal/tui`: interactive Bubble Tea TUI.
- `resources/agents`: built-in agent prompts.

## Standard Checks

- `go test ./internal/config ./internal/executor -count=1`
- `go test ./... -count=1`
- `git diff --check`
- `git status --short --branch`

## Repository Constraints

- Keep work inside `{{WORKSPACE_ROOT}}`.
- Do not change branches when the milestone runner uses `--no-branch-change`.
- Do not load unrelated milestone specs, reports, state entries, or index entries during scoped milestone continuation.
- Do not depend on live network or live Ollama for unit tests; use local unit tests or `httptest`.
- Preserve behavior for non-target runners unless the active milestone explicitly requires a narrow change.

## TUI Constraints

- Ensure the TUI startup issues a sizing command (returning `tea.WindowSizeMsg`) to prevent "Loading..." hangs.
- Sizing queries must degrade gracefully to a default fallback (e.g. 80x24) in non-TTY, redirected, or test environments.
- Default the bold style to disabled when running inside the VS Code integrated terminal (`TERM_PROGRAM == "vscode"`) via `config.DefaultDisableBoldForEnvironment()`, unless explicitly configured otherwise.
- Default rounded borders to disabled (using normal rectangular borders instead) when running inside the VS Code integrated terminal (`TERM_PROGRAM == "vscode"`) via `config.DefaultDisableRoundedBordersForEnvironment()`, unless explicitly configured otherwise.
- Select ASCII-safe glyphs (such as `>` and `*`) instead of Unicode characters (`›` and `◆`) when inside the VS Code integrated terminal to avoid font rendering, spacing, and double-width representation bugs.
- WARNING: Modifying or bypassing VS Code detection logic, default bold/border settings, or Unicode glyph overrides will trigger visual glitches, layout misalignments, overlapping text, or cursor bugs in the VS Code terminal. These safeguards must remain in place to protect against regressions.
- Interactive startup with a missing milestone config routes to the first-run setup wizard; non-interactive startup exits before launching Bubble Tea unless an existing config is provided.

## Generated Or Local Artifacts

- `.cyclestone/reports/*` and `.cyclestone/state.json` can be changed by milestone runs.
- `cmd/cyclestone/__debug_bin*` files are local debug binaries and should not be committed.
