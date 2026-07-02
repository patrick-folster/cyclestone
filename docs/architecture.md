# Architecture

Cyclestone is a local-first Go CLI/TUI for running milestone-oriented agent cycles against a repository.

## Milestone Flow

1. The user opens the TUI with `cyclestone`.
2. If the configured milestone file is missing and stdin is interactive, RootModel opens the first-run setup wizard. If stdin is non-interactive, startup exits before launching Bubble Tea and reports that setup requires an interactive terminal or an existing config.
3. Setup remains non-mutating until final confirmation. It reviews config and state paths, detects Git worktree status, detects supported runners, collects sandbox and branch behavior, and can optionally collect the first milestone.
4. Confirmed setup creates the compact milestone index, project settings, state file, milestones directory, and optional first milestone spec, then reloads config/state and returns to the dashboard.
5. Existing projects skip setup. The project config is loaded from `.cyclestone/milestone.yml` or the configured path.
6. Long-form milestone specs are read from `.cyclestone/milestones/*.md`.
7. Pressing run opens a cycle preflight review. The review shows the next cycle number, selected agent group and pipeline, runner/model, sandbox mode, branch behavior, report paths, tracked repository Git status, and warnings or blockers.
8. The user explicitly confirms the preflight review before cycle execution starts, or cancels back to milestone details without preparing the executor, writing reports or metadata, saving state, or changing branches.
9. A confirmed cycle runs the selected agent group, usually PM -> Developer -> QA -> Recommender.
10. Each agent receives milestone context, recent reports, git context, and handoff data from earlier phases.
11. During execution the executor streams log lines and focused structured runner status events to the TUI. The runner screen shows cycle status, active phase, per-agent state and elapsed time, report/output paths, runner/model/mode, usage ceilings, available token/tool/model-call metrics, and final success/failure/cancellation summaries. Streamed and structured fields are redacted before TUI rendering.
12. Reports and handoffs are written under `.cyclestone/reports/`.
13. Runtime state is updated in `.cyclestone/state.json`.

## Agents

Default agents are embedded from `resources/agents/`.

Users can override or add agents with Markdown prompt files in:

- Global config: `~/.config/cyclestone/agents/`
- Project config: `.cyclestone/agents/`

Agent frontmatter selects the runner and ordering. Custom agents should be treated as trusted workflow code.

Built-in Developer, QA, and Recommender agents opt in to structured output contracts with `output_contract` frontmatter. Custom agents are not assigned a contract by default; a custom prompt can opt in with one of the known values:

- `developer`: final fenced `json` block with `changed_files`, `implemented_behavior`, `checks_run`, `decisions`, and `risks`, all arrays of strings.
- `qa`: final fenced `json` block with string `verdict`, `criteria_results` objects containing string `criterion` and `result`, plus `reviewed_files`, `failing_checks`, and `required_fixes` arrays of strings.
- `recommender`: final fenced `json` block with integer `score` from 0 to 10, string `verdict`, string `reason`, and `next_cycle_focus` array of strings.

For contracted agents, the executor validates the final fenced `json` block only. Explicit contracts are still validated and persisted when `EnableCompactPhaseHandoffs` is false; that setting disables compact phase-input summaries and uncontracted fallback handoff persistence. Older custom or uncontracted outputs continue through the legacy handoff fallback path.

## Runners

Supported runners:

- `codex`
- `agy`
- `aider`
- `gemini`
- `openai`
- `anthropic`
- `ollama`
- Custom executable paths

CLI runners execute external programs (including Codex, Agy, Aider, and Ollama via Aider). Direct API runners call provider APIs.

First-run setup detects `codex`, `agy`, and `aider` with `PATH` lookups. It detects direct API runners from `GEMINI_API_KEY`, `OPENAI_API_KEY`, and `ANTHROPIC_API_KEY`. The default setup runner is chosen only from available supported options; unavailable API runners are not selectable until their environment variable exists.

## Files

Project config:

- `.cyclestone/milestone.yml`: compact milestone index.
- `.cyclestone/milestones/*.md`: milestone specs.
- `.cyclestone/settings.yml`: local project runner/settings overrides.

Runtime output:

- `.cyclestone/state.json`: active milestone, status, cycles, and history.
- `.cyclestone/reports/*.md`: cycle reports and phase excerpts.
- `.cyclestone/reports/*handoff.json`: structured phase handoffs. Contracted handoffs include `output_contract`, `validation_status`, `validation_errors`, `source_log`, and the parsed contract object under `summary`. Legacy handoffs may omit these fields and are treated as compatible.
- `.cyclestone/reports/*metadata.json`: cycle metadata.

Malformed JSON, missing required fields, or wrong field types are written to `validation_errors` and surfaced in reports and TUI history. Invalid Developer output marks the cycle failed. Invalid QA output, or a QA verdict of `blocked` or `needs-human-review`, maps to the existing blocked cycle status. Recommender score loading prefers validated structured handoff data and falls back to the legacy `RECOMMENDATION_SCORE:` marker for older logs.

## Branch Behavior

By default, Cyclestone creates or switches to milestone branches using the configured prefix.

Default prefix:

```yaml
default_git_branch_prefix: cyclestone/milestones/
```

Disable branch changes with:

```bash
cyclestone --no-branch-change
```

When branch changes are disabled, the current branch is used and branch snapshots are recorded in report metadata.

First-run setup defaults branch behavior to automatic milestone branches and persists that choice through the existing project settings fields.

## Sandbox Boundary

Default mode is `sandbox`. The exact restrictions depend on the selected runner. The application still coordinates prompts, writes runtime files, and invokes external runners or provider APIs.

`--unrestricted` removes the intended sandbox boundary for agent execution. Use it only when the runner and repository are trusted.

The first-run wizard defaults to sandbox mode. Persisting `default_mode: unrestricted` requires an explicit acknowledgement in the wizard before setup can be confirmed.

Custom runner scripts and custom agents are trusted inputs. Review them before use.

## Terminal Compatibility

Cyclestone is optimized for high terminal compatibility, but certain environments like the VS Code integrated terminal introduce specific rendering limitations:

### VS Code Integrated Terminal Constraints & Safeguards
- **Font & Style Rendering (No-Bold)**: The VS Code integrated terminal has known style bugs where bold fonts can cause character overlapping, layout shifts, or cursor tracking bugs. To safeguard the layout, Cyclestone defaults bold styling to disabled (`disable_bold: true`) when running inside VS Code (`TERM_PROGRAM == "vscode"`).
- **Border Rendering (No-Rounded-Borders)**: Unicode rounded border characters often trigger rendering and alignment defects in VS Code's terminal pane, leading to double-width spaces or gaps between borders. Cyclestone defaults to rectangular normal borders when `TERM_PROGRAM == "vscode"` to ensure card and pane integrity.
- **Glyph Fallbacks**: Unicode glyphs such as pointers (`›`) and diamonds (`◆`) can fail to display correctly or disrupt spacing depending on the user's terminal font. Cyclestone falls back to ASCII-safe pointer (`>`) and diamond (`*`) characters when VS Code is detected.
- **Initialization & Layout Sizing**: In Bubble Tea, initial terminal size querying is critical. If the program starts up without a known terminal size, it can hang indefinitely in a "Loading..." state. To prevent this, Cyclestone queries terminal size at startup using `github.com/charmbracelet/x/term` and returns a `tea.WindowSizeMsg`. If the query fails (e.g. non-TTY, redirected, or test context), it safely defaults to `80x24` instead of hanging.

Developers must not bypass these environment detection checks or default settings to prevent visual regressions in VS Code terminal environments.
