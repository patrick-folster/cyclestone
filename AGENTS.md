# AGENTS.md

## Purpose

Cyclestone is a local-first Go CLI/TUI for milestone-driven AI development. It coordinates PM, Developer, QA, and recommender agent cycles, captures auditable reports and branch snapshots, and keeps the user in control of repository changes.

Use this file as the canonical project instruction source for coding agents. Keep it concise and current. Use `.cyclestone/DECISIONS.md` for chronological architectural decisions and durable historical notes.

## Source Map

- `cmd/cyclestone`: CLI entrypoint.
- `internal/config`: settings, milestone state, config loading, and persisted project defaults.
- `internal/executor`: milestone cycle execution, runner orchestration, prompt assembly, reports, handoffs, and git/package checks.
- `internal/tui`: Bubble Tea TUI, setup wizard, settings, preflight, runner views, and terminal compatibility behavior.
- `resources/agents`: built-in PM, Developer, QA, and recommender prompts.
- `resources/update_agent_instructions.md`: built-in prompt for human-reviewed `AGENTS.md` update proposals.
- `docs/`: user-facing and architecture documentation.
- `.cyclestone/milestones/`: milestone specs for this repository.
- `.cyclestone/DECISIONS.md`: durable decision log. Read it before changing architecture, runner behavior, config semantics, or TUI compatibility rules.

## Working Rules

- Keep work inside the repository root.
- Do not change branches when the milestone runner uses `--no-branch-change`.
- Do not load unrelated milestone specs, reports, state entries, or index entries during scoped milestone continuation.
- Preserve behavior for non-target runners unless the active milestone explicitly requires a narrow change.
- Do not depend on live network services, live Ollama, or installed external CLIs in unit tests; use local fakes, fixtures, or `httptest`.
- Treat custom agent prompts and runner commands as trusted workflow inputs. Do not broaden their authority without an explicit milestone requirement.
- Keep generated runtime files separate from source changes. `.cyclestone/reports/*`, `.cyclestone/temp/*`, and `.cyclestone/state.json` may change during runs, but should not be edited as source unless the task specifically targets them.
- Do not commit local debug binaries such as `cmd/cyclestone/__debug_bin*`.

## TUI Compatibility Rules

- TUI startup must issue an initial sizing command that returns a `tea.WindowSizeMsg`; otherwise Bubble Tea can remain stuck on "Loading...".
- Terminal sizing must degrade to a safe fallback, such as `80x24`, in non-TTY, redirected, or test environments.
- VS Code integrated terminal safeguards are intentional:
  - Default bold styling to disabled via `config.DefaultDisableBoldForEnvironment()` when `TERM_PROGRAM == "vscode"`, unless explicitly configured otherwise.
  - Default rounded borders to disabled via `config.DefaultDisableRoundedBordersForEnvironment()` when `TERM_PROGRAM == "vscode"`, unless explicitly configured otherwise.
  - Use ASCII-safe glyphs such as `>` and `*` instead of Unicode glyphs such as `›` and `◆` in VS Code terminal contexts.
- Do not bypass VS Code detection, bold/border defaults, or glyph fallback behavior. These safeguards prevent visual glitches, layout shifts, overlapping text, and cursor bugs.
- Interactive startup with a missing milestone config routes to the first-run setup wizard. Non-interactive startup exits before launching Bubble Tea unless an existing config is provided.

## Agent Instruction Files

- `AGENTS.md` is the current operating instruction file loaded into agent prompts.
- `.cyclestone/DECISIONS.md` remains the chronological decision log and should not be merged wholesale into `AGENTS.md`.
- Agents may propose updates to `AGENTS.md`, but normal milestone cycles must not silently rewrite it. Instruction changes should be visible, reviewable, and explicitly accepted by the user.
- Keep `AGENTS.md` focused on stable guidance: source layout, invariants, checks, compatibility constraints, and project workflow rules. Put transient milestone details in milestone specs or reports instead.

## Checks

Use the narrowest useful checks first, then broaden when the change touches shared behavior.

- Targeted Go tests for touched packages, for example:
  - `go test ./internal/config ./internal/executor -count=1`
  - `go test ./internal/tui -count=1`
- Full Go test suite when behavior spans packages:
  - `go test ./... -count=1`
- Repository hygiene:
  - `git diff --check`
  - `git status --short --branch`

If a check cannot be run, report the reason and the remaining risk.
