# Changelog

All notable changes to this project should be documented in this file.

This project follows tagged releases in the form `vMAJOR.MINOR.PATCH`.

## Unreleased

### Added

- Agents now write their structured YAML handoff directly to a dedicated temp file under `.cyclestone/temp/` (path injected into the prompt via the `{{HANDOFF_YAML_PATH}}` placeholder) instead of emitting it inline in the console output. Cyclestone reads clean YAML from the file, avoiding the brittle console-log extraction and normalization pipeline. The log-based extraction remains as a fallback for manual mode, custom agents without the placeholder, and older runners.
- Security policy.
- Release checklist.
- Architecture documentation.
- GitHub issue and pull request templates.

### Fixed

- Inline YAML extraction from Aider/Ollama runner logs no longer splits block-scalar documents (e.g. `reason: |`) when Aider's CLI display flattens block-scalar content to column 0. The scanner and normalizer now track block-scalar indicators and re-indent flattened content, so fields like the recommender's `score`, `verdict`, and `reason` are captured instead of being silently discarded.
- Nested block-scalar content (e.g. `notes: |` inside `criteria_results` list items) is now correctly re-indented when Aider's CLI display flattens it to the same indentation as the key. Previously, only column-0 content was re-indented; content at the key's indentation was left untouched, causing a YAML parse failure that silently discarded the entire QA handoff (`verdict`, `criteria_results`, `reviewed_files`, `failing_checks`, `required_fixes`) and replaced it with unrelated fields scraped from the model's thinking section.
- Inline YAML extraction now prefers the answer region (after the last `► ANSWER` marker) over the model's thinking/reasoning section, preventing handoff keys quoted out of context in the thinking section from being mistaken for the agent's structured output.

## v0.1.0 - TBD

Initial open-source release.
