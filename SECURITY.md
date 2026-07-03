# Security Policy

## Supported Versions

Security fixes are provided for the latest released version. Before the first stable release, fixes are provided on `main` and in the next tagged release.

## Reporting a Vulnerability

Report vulnerabilities through GitHub private vulnerability reporting for this repository when available.

Do not include exploitable details in a public issue. If private reporting is not enabled yet, open a public issue that says a private security report is needed, but do not include reproduction steps, payloads, credentials, or sensitive logs.

Include:

- Affected version or commit.
- Runner used (`codex`, `agy`, or `ollama-codex`).
- Operating system.
- Impact summary.
- Minimal reproduction steps.
- Whether the issue requires `--unrestricted`.

## Threat Model

Cyclestone orchestrates AI runners that can inspect project files and may modify repository content. Treat prompts and custom agents as executable workflow inputs.

Primary risks:

- Prompt injection through repository content or milestone specs.
- Unsafe tool or shell execution through a selected runner.
- File modification outside the intended task scope.
- Credential exposure through environment variables, logs, prompts, or reports.
- Network calls made by external CLIs.

Default mode is `sandbox`. Use `--unrestricted` only for trusted projects, trusted prompts, and trusted runners.

## User Safety Guidance

- Review local agents before running them.
- Keep credentials out of milestone specs, reports, and prompts.
- Run from a clean git worktree when possible.
- Review diffs and reports before merging agent output.
- Avoid `--unrestricted` unless the milestone requires it and you accept the risk.
