# QA Checklist

## Required Inputs

- Active milestone spec and scoped runtime state are available.
- Current changed-file list is available.
- Required process context is available from `.cyclestone/AI_CONTEXT.md` or `AI_CONTEXT.md`.

## Review Checks

- Validate every active milestone acceptance criterion.
- Inspect all tracked changed files for the active milestone.
- Confirm root repository branch and any nested repository branches without changing branches.
- Confirm no unrelated source files, generated binaries, vendor files, archived paths, or deprecated paths were changed.
- Confirm cross-package contracts remain consistent.
- Confirm non-target runner behavior remains unchanged unless the milestone explicitly requires it.
- Confirm secrets are not added to source, reports, logs, tests, or config.

## Test Checks

- Run focused Go tests for changed packages when applicable.
- Run `go test ./... -count=1`.
- Run `git diff --check`.
- Report exact failing package or test names when any check fails.

## Verdict Rules

- Use `approved` only when acceptance criteria and required checks pass.
- Use `blocked` when required inputs are missing or a deterministic failure prevents verification.
- Use `needs-human-review` when code appears correct but process, artifact, or intermittent-test risk remains.
