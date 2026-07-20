# Changelog

All notable changes to this project should be documented in this file.

This project follows tagged releases in the form `vMAJOR.MINOR.PATCH`.

## Unreleased

### Added
- Optional planning layer documentation: new `docs/planning-guide.md` covering the conceptual overview, terminology table, one-way dependency architecture diagram, CLI/TUI reference, step-by-step workflows, hierarchy visualization, archiving/deletion behavior, migration guidance, backward-compatibility guarantees, and troubleshooting for the Milestone Planner/Plan/Briefing layer.
- `docs/architecture.md` now includes an explicit one-way dependency diagram and terminology table for the planning layer, with cross-links to the planning guide and data-model reference.
- `docs/planning-data-models.md` now includes an enumerated backward-compatibility guarantee list and a troubleshooting summary.
- `README.md` now points to the optional planning-layer documentation without changing the standalone Milestone onboarding flow.
- `-version` / `--version` CLI flag to check the current version of the tool.
- Root `AGENTS.md` support as the canonical current instruction source for agent prompts, replacing the older `.cyclestone/AI_CONTEXT.md` context file.
- First-run setup can create an editable starter `AGENTS.md`, and runtime settings now support `agent_instructions.file`, `agent_instructions.propose_updates`, and `agent_instructions.auto_apply_updates`.
- Human-reviewed `AGENTS.md` update workflow from the dashboard or milestone details. The updater runner produces a proposal draft at `.cyclestone/temp/AGENTS.md.proposed`, leaving the root file unchanged until the user applies it.
- Separate recommender `agent_instructions_update_score` values for deciding whether a durable `AGENTS.md` update should be reviewed, independent from the normal next-cycle recommendation score.
- Informational warnings for untracked embedded Git repositories in cycle reports and metadata.

### Changed
- Milestone runtime artifacts now use hierarchical report paths under `.cyclestone/reports/<milestone-id>/`, with `summary.md` at the milestone root and each `cycle-NNN/` directory containing `report.yaml`, `metadata.json`, `codex-thread.json` when present, and per-agent phase directories.
- Normal milestone cycles protect root `AGENTS.md` from direct runner edits. Any attempted create, modify, or delete is restored and captured as proposed instruction content for explicit human review.
- Agent prompts now read optional root `AGENTS.md`, keep `.cyclestone/DECISIONS.md` as the chronological decision log, and treat embedded-repository warnings as human-awareness notes unless repository topology is explicitly in scope.
- Runner and milestone views keep live log frames stable as status text, proposal previews, and terminal dimensions change.

### Removed
- `.cyclestone/AI_CONTEXT.md` is no longer the project instruction source.

## v0.0.2 - 2026-07-03

### Added

- Agents now write their structured YAML handoff directly to a dedicated temp file under `.cyclestone/temp/` (path injected into the prompt via the `{{HANDOFF_YAML_PATH}}` placeholder) instead of emitting it inline in the console output. Cyclestone reads clean YAML from the file, avoiding the brittle console-log extraction and normalization pipeline. The log-based extraction remains as a fallback for manual mode, custom agents without the placeholder, and older runners.
- Security policy.
- Release checklist.
- Architecture documentation.
- GitHub issue and pull request templates.

### Changed

- The Aider-based runners (`aider` and `ollama`/Ollama via Aider) are no longer offered in the TUI or documented as supported runners. They remain supported by the executor for non-interactive/manual configuration, but the first-run setup wizard, milestone creation, settings screen, and preflight checks no longer list or accept them. Existing configs using `default_llm: aider` or `default_llm: ollama` should be migrated to `codex`, `agy`, or `ollama-codex`. The `ollama-codex` runner (Codex CLI launched through Ollama) remains the recommended Ollama integration.

### Fixed

- Inline YAML extraction from Aider/Ollama runner logs no longer splits block-scalar documents (e.g. `reason: |`) when Aider's CLI display flattens block-scalar content to column 0. The scanner and normalizer now track block-scalar indicators and re-indent flattened content, so fields like the recommender's `score`, `verdict`, and `reason` are captured instead of being silently discarded.
- Nested block-scalar content (e.g. `notes: |` inside `criteria_results` list items) is now correctly re-indented when Aider's CLI display flattens it to the same indentation as the key. Previously, only column-0 content was re-indented; content at the key's indentation was left untouched, causing a YAML parse failure that silently discarded the entire QA handoff (`verdict`, `criteria_results`, `reviewed_files`, `failing_checks`, `required_fixes`) and replaced it with unrelated fields scraped from the model's thinking section.
- Inline YAML extraction now prefers the answer region (after the last `► ANSWER` marker) over the model's thinking/reasoning section, preventing handoff keys quoted out of context in the thinking section from being mistaken for the agent's structured output.

## v0.0.1 - 2026-07-01

Initial open-source release.
