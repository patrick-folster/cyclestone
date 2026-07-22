# Planning Data Models

This document defines Cyclestone's optional planning-layer persistence model. Planning remains optional and does not migrate or replace ordinary Milestone runtime data. Explicit planning commands may generate or orchestrate ordinary Milestones through the existing TUI and cycle engine.

User-facing workflows, CLI/TUI reference, troubleshooting, migration, and backward-compatibility guarantees are documented in [Planning Guide](planning-guide.md). This document is the persistence schema and validation reference.

The current execution model remains independent:

```text
Milestone -> Milestone Cycles
```

Planning data may sit above that model:

```text
Milestone Planner -> Plan -> Briefing -> optional Milestone -> Cycles
```

The Milestone Planner is a virtual UI and navigation root. It has no required persisted global identity or metadata in this design.

## File Layout

Planning persistence uses one YAML file per Plan under `.cyclestone/plans/`:

```text
.cyclestone/plans/<plan-id>.yml
```

Each Plan file embeds its Briefing records and keeps a canonical `briefing_order` list. This keeps planning data separate from existing execution data:

- `.cyclestone/milestone.yml` remains the compact Milestone index.
- `.cyclestone/milestones/*.md` remains the long-form Milestone spec location.
- `.cyclestone/state.json` remains runtime state keyed by Milestone ID.
- `.cyclestone/reports/milestones/<milestone-id>/` remains cycle artifacts keyed by Milestone ID.

Old projects with no `.cyclestone/plans/` directory are valid and require no migration.

## Manual CLI Management

The CLI exposes the planning layer without mutating Milestone, state, report, temp, or runner files:

```text
cyclestone plan list
cyclestone plan show <plan-id>
cyclestone plan generate --goal <goal> [--preview] [--actor <actor>] [--runner-command <command>] [--response-file <path>]
cyclestone plan reevaluate <plan-id> [--goal <goal>] [--preview] [--auto-apply] [--actor <actor>] [--runner-command <command>] [--response-file <path>]
cyclestone plan create <plan-id> --title <title> --objective <objective> [--actor <actor>]
cyclestone plan edit <plan-id> [--title <title>] [--objective <objective>] [--actor <actor>]
cyclestone plan approve <plan-id> [--actor <actor>]
cyclestone plan reject <plan-id> [--actor <actor>]
cyclestone plan archive <plan-id> [--actor <actor>]
cyclestone plan restore <plan-id> [--actor <actor>]
cyclestone plan delete <plan-id> --confirm <plan-id>

cyclestone briefing show <plan-id> <briefing-id>
cyclestone briefing add <plan-id> <briefing-id> --title <title> --objective <objective> --intent <intent> --completion-signal <signal> [--actor <actor>]
cyclestone briefing edit <plan-id> <briefing-id> [metadata flags] [--actor <actor>]
cyclestone briefing reorder <plan-id> <briefing-id> [<briefing-id>...] [--actor <actor>]
cyclestone briefing approve <plan-id> <briefing-id> [--actor <actor>]
cyclestone briefing reject <plan-id> <briefing-id> [--actor <actor>]
cyclestone briefing archive <plan-id> <briefing-id> [--actor <actor>]
cyclestone briefing restore <plan-id> <briefing-id> [--actor <actor>]
cyclestone briefing delete <plan-id> <briefing-id> --confirm <briefing-id>
cyclestone briefing split <plan-id> <briefing-id> --parts-file <path> [--milestone-link <part-id|none>] [--actor <actor>]
cyclestone briefing merge <plan-id> <target-briefing-id> <merged-briefing-id> [<merged-briefing-id>...] --title <title> --objective <objective> --intent <intent> --completion-signal <signal> [--status <status>] [--milestone-link <briefing-id|none>] [--actor <actor>]
cyclestone briefing dependency add <plan-id> <briefing-id> <dependency-id> [--actor <actor>]
cyclestone briefing dependency remove <plan-id> <briefing-id> <dependency-id> [--actor <actor>]
cyclestone briefing link <plan-id> <briefing-id> <milestone-id> [--replace-link] [--actor <actor>]
cyclestone briefing unlink <plan-id> <briefing-id> [--actor <actor>]
cyclestone briefing execute <plan-id> <briefing-id>
cyclestone plan start <plan-id> [--mode once|continuous|review]
cyclestone plan resume <plan-id> [--mode once|continuous|review] [--approve]
```

These commands load `.cyclestone/plans/*.yml` relative to the configured `-config` file directory. Display commands use the existing milestone index and loaded planning files only to label Briefing relationships as `milestone: none`, `milestone: linked <id> (standalone)`, `milestone: linked <id> (also linked by Plan <plan-id> Briefing <briefing-id>)`, or `milestone: missing <id>`. Missing optional Milestone references remain warnings and do not create Milestone specs, state, reports, or Plan files.

`plan list` prints deterministic Plan rows with ID, title, status, Briefing count, and derived progress. `plan show` prints one Plan with metadata, progress, and Briefings in `briefing_order`, followed by remaining addressable Briefings sorted by ID. `briefing show` prints one Briefing with objective, intent, completion signal, dependencies, constraints, derived readiness, and Milestone relationship.

Mutating commands load and validate all planning files before writing. Planning validation errors block the command and leave files unchanged; warnings are printed and the command may continue. Plan creation creates `.cyclestone/plans/` on first use. Plan deletion removes only `.cyclestone/plans/<plan-id>.yml` and requires an exact `--confirm <plan-id>` token. Briefing deletion removes only the embedded Briefing record and its active order entry and requires `--confirm <briefing-id>`. Archive commands set status to `archived`; they do not delete records or Milestone artifacts.

Review commands are aliases over the same typed planning mutation path. `plan approve` and `briefing approve` set status to `completed`; `plan reject` and `briefing reject` set status to `archived`. Briefing rejection removes the Briefing from active `briefing_order` while preserving the embedded record and any `milestone_id`. Approval and rejection do not execute Plans, create Milestones, start runner cycles, or mutate Milestone specs, compact index entries, state, reports, temp files, cycle artifacts, or branch snapshots.

`briefing split` reads a JSON parts file, either as `{"parts":[...]}` or as a top-level array. Each part supplies `id`, `title`, `objective`, `intent`, and `completion_signal`, with optional `status`, `constraints`, and `depends_on`; omitted status defaults to `active`. Splitting removes the source Briefing record from the Plan, inserts the new non-archived part IDs where the source appeared in `briefing_order`, gives the first part the source dependencies when `depends_on` is omitted, chains later omitted dependencies to the previous part, and rewrites external dependents of the source to the final part. If the source has `milestone_id`, the command fails unless `--milestone-link <part-id|none>` explicitly selects the part that keeps the link or clears it.

`briefing merge` keeps the first Briefing ID as the stable target ID, requires explicit merged metadata flags, removes the other merged Briefing records from the Plan, unions dependencies while excluding merged IDs and self-dependencies, and rewrites external dependents of merged-away Briefings to the target. If multiple merged Briefings have `milestone_id`, the command fails unless `--milestone-link <briefing-id|none>` selects one linked Briefing to preserve or clears the link. Merge and split only rewrite the containing Plan file; linked or standalone Milestone storage is never deleted or edited.

`briefing link` is strict: the Milestone ID must already exist in the compact Milestone index, the target Briefing must not already link a different Milestone unless `--replace-link` is supplied, and no other active or completed Briefing in any valid Plan may already link the same Milestone. Replacement changes only the selected Briefing's `milestone_id` and planning metadata; it does not create, delete, or mutate either the previously linked Milestone or the replacement Milestone. `briefing unlink`, archive, delete, and Plan delete never modify linked Milestone specs, compact index entries, runtime state, reports, temp files, branch snapshots, or cycles.

`briefing generate-milestone <plan-id> <briefing-id>` creates exactly one ordinary Milestone from one completed Briefing. The command validates all Plan files before writing, requires every dependency of the selected Briefing to also be completed, refuses an existing Briefing link unless `--replace-link` is supplied, writes a long-form Markdown spec under `.cyclestone/milestones/`, appends a compact entry to `.cyclestone/milestone.yml`, and then stores the generated Milestone ID back on only the source Briefing. `--preview` prints the proposed Milestone ID, spec, and Briefing link without writing milestone or planning data. Replacing a Briefing link does not delete or mutate the previously linked Milestone.

`briefing execute <plan-id> <briefing-id>` executes exactly one active or completed Briefing whose dependencies are completed. A valid existing `milestone_id` resolves to that ordinary Milestone without rewriting its index entry, spec, state, or reports. An unlinked Briefing reuses the same generation preparation path, persists its new Milestone link, reloads normal Milestone config and state, and opens the existing preflight and runner workflow. If Milestone creation succeeds but link persistence fails, the generated standalone Milestone is preserved and execution stops with an explicit partial-success error. A Plan resume may reclaim only the deterministic generated ID whose title, spec path, and embedded Plan/Briefing source markers prove it is the interrupted output for that same Briefing; ambiguous collisions stop for explicit repair.

The executor receives only the resolved `config.Milestone` and normal run options. Planning origin stays in the TUI wrapper. An `approved` terminal result marks only the selected Briefing `completed`; `failed`, `blocked`, executor errors, and cancellation do not advance it. Completion reloads the Plan and verifies that the Briefing still links the executed Milestone before saving. A post-cycle Plan save failure is shown as a warning and never rolls back or invalidates cycle state, reports, branch snapshots, or the generated/linked Milestone. No other ready Briefing starts automatically.

## Plan Execution

An approved Plan uses the existing `completed` Plan status. `plan start` creates optional `execution` metadata in that Plan file and selects only from its `briefing_order`. Archived and completed Briefings are skipped; an active Briefing is eligible only when all same-Plan dependencies are completed. If incomplete work remains but none is eligible, execution ends in the distinct `blocked` / `dependency-deadlock` state. If no incomplete work remains, it ends at `completed` / `exhausted`.

Execution modes are:

- `once`: reconcile or execute one eligible Briefing, then pause.
- `continuous`: continue selecting eligible Briefings after each approved result until exhaustion, failure, or dependency deadlock.
- `review`: persist an `approval-required` checkpoint before each Milestone cycle. `plan resume <id> --approve` consumes approval only for the displayed Briefing/Milestone gate.

The command-line `--mode` overrides `default_plan_execution_mode`; the resolved mode is persisted for resume. A resume-time mode changes only the Plan coordinator behavior and never changes runner safety or branch settings.

The optional execution record stores the resolved mode, state, checkpoint, current Briefing and Milestone IDs, pending approval token, stop reason, and update time. Checkpoints are written around selection, durable Milestone linkage, pending launch, active cycle, Briefing completion, review pause, one-item pause, stop, deadlock, and exhaustion. A safe stop at an uncertain launch boundary retains `cycle-pending` or `cycle-running` instead of collapsing it to a generic checkpoint. The TUI preflight and runner show Plan ID, Briefing ID, queue position, dependency readiness, and mode while continuing to execute an ordinary Milestone.

Resume always reloads the selected Plan, compact Milestone index, and `state.json`. Approval consumption and the final preflight-to-runner transition both require the retained Plan to remain approved, the Briefing to remain active and dependency-ready, its exact retained link to remain intact, and the Milestone to remain indexed. A valid existing link is reused. An Approved linked Milestone completes the Briefing without another cycle; Failed or Blocked stops on the current Briefing. A dangling or edited link is preserved and requires explicit repair. If a process ends after `cycle-running` was saved but no terminal Milestone status exists, every resume remains non-launchable and stops for inspection until runtime state is reconciled. Terminal events whose Milestone identity does not match the retained cycle are recorded as actionable stops and never advance planning. Navigation after runner cancellation hides the visible Plan context but retains callback ownership until the delayed terminal event durably stops the selected Briefing.

Archiving or deleting a Plan only changes/removes its planning file. It never cascades to generated or linked Milestone specs, compact index entries, state, cycle history, reports, handoffs, temp data, or branch snapshots.

## AI-Assisted Plan Generation

`cyclestone plan generate --goal <goal>` asks a configured local runner to return one structured JSON object with a Plan title, objective, optional constraints, and ordered Briefings. Cyclestone parses that JSON, derives lowercase Plan and Briefing IDs from titles, normalizes same-Plan dependencies after IDs are fixed, sets Plan and Briefing status to `active`, clears Milestone links by rejecting any generated `milestone_id`, validates the in-memory Plan with the same planning validator used by `SavePlan`, and then writes only `.cyclestone/plans/<plan-id>.yml`.

Generation may also run in review-only mode with `--preview`, which prints the generated Plan through the same renderer as `plan show` and writes nothing. Tests and deterministic local workflows may pass `--response-file <path>` to provide the structured response directly; this bypasses runner execution but still uses the same parser, converter, validation, collision checks, and preview/save behavior.

The generation prompt is bounded and assembled from stable repository context: `AGENTS.md`, `.cyclestone/DECISIONS.md`, `docs/architecture.md`, `docs/planning-data-models.md`, and tracked repository structure when available. It does not load unrelated milestone specs, reports, state entries, or temp artifacts. Invalid JSON, missing required generated fields, dependency references that cannot be mapped to generated Briefings, dependency cycles, generated Milestone links, validation errors, or Plan ID collisions fail before any Plan file is written.

## AI-Assisted Plan Re-Evaluation

`cyclestone plan reevaluate <plan-id>` triggers an AI Planner re-evaluation of remaining incomplete Briefings in an active Plan. It may be run explicitly from the CLI or triggered post-execution after a Briefing's Milestone execution completes when `enable_plan_reevaluation` is enabled in configuration.

The Planner agent assesses repository context, completed Milestones, cycle reports, QA findings, updated architecture, and documentation to synthesize proposed updates to remaining Briefings. Proposals may include adding new Briefings, removing obsolete Briefings, reordering, splitting, merging, updating properties (objectives, constraints, dependencies), or blocking Briefings.

### Safety Invariants

Replanning enforces strict safety boundaries:
1. Replanning modifies only planning-layer entities (`.cyclestone/plans/*.yml`).
2. Existing or completed Milestones, compact index entries, state, reports, and branch snapshots are never modified, rewritten, or deleted.
3. Removing or merging a Briefing preserves all linked Milestones and execution history intact on disk.
4. Standalone Milestones remain outside Plans unless explicitly linked through user-approved suggestions; silent linking is strictly prohibited.
5. Proposed plan modifications require explicit user approval before applying changes to disk, unless `auto-apply` or `auto_apply_plan_reevaluation` is explicitly configured.

### Visual Diff & Review

Proposed changes are computed by `config.ComputePlanDiff` and rendered as a structured visual diff displaying additions (`+`), removals (`-`), property updates (`~`), reordering (`^`), blockages (`!`), splits (`/`), merges (`+`), link suggestions (`*`), and warnings. Invariant validation runs before presenting the diff; invalid proposals block execution and leave files unchanged.


## Plan Record

A Milestone Plan groups related Briefings. It does not own Milestone runtime state, reports, branches, or cycle history.

Required fields:

- `schema_version`: planning schema version. Initial value: `1`.
- `id`: stable Plan identifier. It is immutable after creation.
- `title`: human-readable Plan title.
- `objective`: concise outcome or description for the Plan.
- `status`: one of `active`, `completed`, or `archived`.
- `created_at`: RFC 3339 timestamp.
- `created_by`: user or agent identity that created the Plan.
- `updated_at`: RFC 3339 timestamp.
- `updated_by`: user or agent identity that last changed the Plan.
- `briefing_order`: ordered list of Briefing IDs.
- `briefings`: Briefing records for this Plan.

Optional fields:

- `constraints`: project-level or Plan-level constraints that apply to Briefings unless overridden.

Derived fields:

- Progress is derived from non-archived Briefing statuses. It is not persisted as canonical state.
- A Plan with no non-archived Briefings has no completed work and no required derived completion percentage.
- Archived Briefings remain addressable but are excluded from derived progress.
- A Plan can be marked `completed` only when every non-archived Briefing is `completed`.

## Briefing Record

A Milestone Briefing captures planning intent for possible Milestone work. A Briefing may never generate a Milestone, may later create one Milestone, or may reference one existing Milestone.

Required fields:

- `id`: stable Briefing identifier unique within the parent Plan. It is immutable after creation.
- `title`: human-readable Briefing title.
- `objective`: concrete outcome for the possible Milestone work.
- `intent`: rationale, context, or problem statement.
- `status`: one of `active`, `completed`, or `archived`.
- `completion_signal`: text describing how a future user or validator can tell the Briefing is complete.
- `created_at`: RFC 3339 timestamp.
- `created_by`: user or agent identity that created the Briefing.
- `updated_at`: RFC 3339 timestamp.
- `updated_by`: user or agent identity that last changed the Briefing.

Optional fields:

- `constraints`: Briefing-specific constraints.
- `depends_on`: Briefing IDs in the same Plan that must be complete before this Briefing is ready.
- `milestone_id`: zero or one generated or linked Milestone ID.

Ordering is stored by the parent Plan's `briefing_order` list, not by a mutable numeric field inside each Briefing.

## Status Lifecycles

Plans and Briefings share the same status values:

- `active`: visible and available for continued planning work or review.
- `completed`: approved or finished, retained for context, and normally read-only except metadata corrections or explicit reopen actions.
- `archived`: rejected or hidden from active planning views by default, retained for history, and excluded from derived progress.

Allowed transitions:

| From | To | Rule |
| --- | --- | --- |
| `active` | `completed` | Valid when completion requirements are met. |
| `active` | `archived` | Valid at any time. |
| `completed` | `active` | Valid as an explicit reopen action. |
| `completed` | `archived` | Valid at any time. |
| `archived` | `active` | Valid as an explicit restore action. |
| `archived` | `completed` | Invalid directly; restore to `active` first, then complete. |

Future validators should report invalid transitions as validation errors and leave persisted status unchanged.

## Dependencies

Briefing dependencies are same-Plan only and reference Briefing IDs from the parent Plan.

Rules:

- `depends_on` must not reference Briefings from another Plan.
- Dependency graphs must be acyclic.
- A missing dependency ID is a validation error for active or completed Briefings.
- A missing dependency ID on an archived Briefing is a recoverable warning because archived records remain historical.
- An active Briefing is ready only when every dependency has `completed` status.
- Completed dependencies satisfy readiness; dependencies in any other status block readiness.
- An archived dependency is not completed, blocks Plan execution, and requires explicit Plan repair before its dependent Briefing can run. Validators warn when active work depends on archived work.
- Ordering and dependencies are separate. `briefing_order` controls presentation; `depends_on` controls readiness.
- If order places a dependent Briefing before an incomplete dependency, the data remains parseable but future validators should warn because the display order conflicts with readiness.

## Ordering

The canonical order for Briefings is the Plan-level `briefing_order` list.

Rules:

- Every non-archived Briefing must appear exactly once in `briefing_order`.
- Archived Briefings may remain in `briefing_order` to preserve historical placement, or may be omitted from active views while remaining addressable by ID.
- Duplicate IDs in `briefing_order` are validation errors.
- Missing active Briefing IDs in `briefing_order` are validation errors.
- IDs in `briefing_order` that do not exist in `briefings` are validation errors.
- Insertion adds a new Briefing ID at the selected position.
- Removal archives or deletes the Briefing record and removes its ID from active ordering unless history preservation requires keeping the archived position.
- Reordering changes only `briefing_order`; Briefing IDs remain stable.

## Optional Milestone Relationship

The Briefing-to-Milestone relationship is optional:

- A Briefing may reference zero or one Milestone with `milestone_id`.
- A Milestone may exist without any Briefing.
- Plans are not required to index all Milestones.
- A dangling `milestone_id` reference is a warning, not a Milestone validity error.
- A generated Milestone remains valid if its source Plan or Briefing is later archived or deleted.

This design includes optional Milestone provenance metadata as a documented future-compatible shape:

```yaml
source:
  type: briefing
  plan_id: onboarding-improvements
  briefing_id: first-run-setup-validation
```

This provenance is advisory and backward-compatible. Current Milestone execution, status loading, report paths, deletion flows, cycle flows, and prompt assembly must ignore it until a later implementation milestone deliberately changes code. A dangling `source.plan_id` or `source.briefing_id` must not invalidate a Milestone spec or compact index entry.

Current compact Milestone writers do not persist this provenance shape. Until a future implementation adds explicit support, examples with `source` are illustrative model examples rather than a supported write path.

## Validation

Future validators should use these severities:

- Error: malformed YAML, unknown required schema version, missing required fields, duplicate Plan IDs, duplicate Briefing IDs within a Plan, invalid status, invalid transition, invalid timestamp, duplicate or missing active order entries, cross-Plan dependency, dependency cycle, missing active dependency, or active Briefing with an invalid `milestone_id` format.
- Warning: unknown optional fields, dangling optional Milestone reference, dangling optional provenance, archived Briefing with missing dependency, archived Plan whose generated Milestones still exist, or display ordering that conflicts with readiness.

Rules:

- Plan IDs and Briefing IDs should use lowercase ASCII letters, numbers, and hyphens.
- IDs are stable and immutable after creation.
- Duplicate Plan IDs across `.cyclestone/plans/*.yml` are errors.
- Duplicate Briefing IDs within one Plan are errors.
- The same Briefing ID may appear in different Plans because dependency scope is same-Plan only.
- Timestamps use RFC 3339. `updated_at` should be equal to or later than `created_at`.
- Missing optional `constraints`, `depends_on`, `milestone_id`, or `source` fields are valid.
- Archived records remain parseable and addressable.
- Malformed YAML makes only the affected Plan file invalid; it must not invalidate existing Milestones or cycle reports.

## Versioning

Planning files use `schema_version`.

Versioning rules:

- Version `1` is the initial planned schema.
- Additive optional fields may be introduced without changing existing files.
- New required fields require a new schema version or a migration plan.
- Unknown fields should be preserved by future planning editors when practical and reported as warnings by validators. The current manual CLI uses the typed Plan model for writes, so editing a Plan file rewrites known schema fields and does not preserve unknown YAML fields.
- Unknown future `schema_version` values are validation errors for planning files only.
- Projects without planning files remain valid for every planning schema version.
- Migrations must not rewrite `.cyclestone/milestone.yml`, `.cyclestone/milestones/*.md`, `.cyclestone/state.json`, or `.cyclestone/reports/` unless a later milestone explicitly targets those files.

## Migration And Compatibility

No migration is required for this design.

Existing data remains readable without modification:

- Compact Milestone index entries continue to require only current Milestone fields.
- Long-form Milestone specs continue to use Markdown sections such as `## Goal` and `## Acceptance Criteria`.
- Runtime status, cycle counts, recommendations, and history remain in `.cyclestone/state.json`.
- Cycle artifacts remain under `.cyclestone/reports/milestones/<milestone-id>/`.

Planning files may be added, removed, archived, or malformed without changing Milestone execution validity. Future planning features should isolate planning validation failures to planning views and planning commands.

### Backward Compatibility Guarantees

- `.cyclestone/milestone.yml` remains the compact Milestone index and remains valid without any Plan or Briefing data.
- `.cyclestone/milestones/*.md` remains the long-form Milestone spec location and remains valid without planning provenance.
- `.cyclestone/state.json` remains keyed by Milestone runtime progress and remains valid without planning state.
- `.cyclestone/reports/milestones/<milestone-id>/` remains keyed by Milestone ID. Existing report directories are not migrated when planning metadata is added.
- `.cyclestone/plans/*.yml` is optional and additive. Adding planning files never rewrites Milestone specs, compact index entries, state, reports, temp files, or branch snapshots.
- Existing projects require no migration for the optional planning layer.
- Standalone and generated Milestones function as fully independent first-class entities regardless of source Plan or Briefing archival, deletion, or missing status.

### Troubleshooting

See [Planning Guide](planning-guide.md#troubleshooting) for the full troubleshooting workflow. Summary:

- `milestone: missing <id>` warnings: a Briefing's `milestone_id` is not in the compact index. Warning, not an error; unrelated Briefings/Milestones are not blocked. Repair with `briefing link --replace-link`, `briefing unlink`, or by re-creating the Milestone. Repair mutates only the containing Plan file.
- Dangling Briefing links: preserved and surfaced as warnings. `plan start`/`plan resume` treat them as actionable stops and never auto-create or auto-relink.
- Stale provenance: a Milestone's optional `source` points at an archived/deleted Plan or Briefing. Provenance is advisory; current execution ignores it. No repair required for the Milestone to remain executable.
- Cross-Plan duplicate links: `briefing link` blocks active/completed duplicate links across Plans; detail views surface them for awareness. Reconcile with `briefing unlink` or `briefing link --replace-link`.
- Planning warnings never invalidate Milestone specs, compact index entries, state, reports, temp files, or branch snapshots, and never block unrelated Briefing or Milestone operations.

## Examples

### Plan With Ungenerated Briefings

File: `.cyclestone/plans/onboarding-improvements.yml`

```yaml
schema_version: 1
id: onboarding-improvements
title: Improve onboarding
objective: Make first-run setup easier to understand and recover from.
status: active
created_at: "2026-07-20T10:15:00Z"
created_by: "patrick"
updated_at: "2026-07-20T10:15:00Z"
updated_by: "patrick"
constraints:
  - Keep setup usable in non-TTY environments.
  - Preserve existing VS Code terminal safeguards.
briefing_order:
  - setup-copy-review
  - setup-recovery-paths
briefings:
  - id: setup-copy-review
    title: Review setup copy
    objective: Clarify first-run setup labels and confirmation text.
    intent: Users should understand what files setup will create before confirmation.
    status: active
    completion_signal: Setup copy is reviewed and accepted in the TUI.
    constraints:
      - Do not change runner detection behavior.
    created_at: "2026-07-20T10:15:00Z"
    created_by: "patrick"
    updated_at: "2026-07-20T10:15:00Z"
    updated_by: "patrick"
  - id: setup-recovery-paths
    title: Define setup recovery paths
    objective: Describe recovery behavior when setup is cancelled or fails.
    intent: Users should be able to retry setup without partial configuration surprises.
    status: active
    depends_on:
      - setup-copy-review
    completion_signal: Recovery paths are documented and validated against current setup flow.
    created_at: "2026-07-20T10:18:00Z"
    created_by: "patrick"
    updated_at: "2026-07-20T10:18:00Z"
    updated_by: "patrick"
```

### Plan With A Briefing Linked To A Milestone

File: `.cyclestone/plans/reporting-reliability.yml`

```yaml
schema_version: 1
id: reporting-reliability
title: Improve reporting reliability
objective: Make cycle artifacts easier to audit after interrupted runs.
status: active
created_at: "2026-07-20T11:00:00Z"
created_by: "patrick"
updated_at: "2026-07-20T12:30:00Z"
updated_by: "developer-agent"
briefing_order:
  - preserve-cycle-metadata
briefings:
  - id: preserve-cycle-metadata
    title: Preserve cycle metadata
    objective: Ensure report metadata survives failed runner phases.
    intent: QA should be able to inspect branch and artifact context even after failures.
    status: completed
    milestone_id: 0007-preserve-cycle-metadata
    completion_signal: Milestone 0007 has an approved cycle report.
    created_at: "2026-07-20T11:05:00Z"
    created_by: "patrick"
    updated_at: "2026-07-20T12:30:00Z"
    updated_by: "developer-agent"
```

### Standalone Milestone With No Planning Metadata

File: `.cyclestone/milestone.yml`

```yaml
milestones:
  - id: 0008-fix-runner-status
    title: Fix Runner Status
    spec_path: milestones/0008-fix-runner-status.md
```

File: `.cyclestone/milestones/0008-fix-runner-status.md`

```markdown
# Milestone Spec: 0008-fix-runner-status - Fix Runner Status

## Goal
Correct stale runner status rendering after a cycle finishes.

## Acceptance Criteria
- [ ] The runner view shows the final cycle status after completion.
- [ ] Existing cycle reports remain readable.
```

### Milestone With Optional Briefing Provenance

This example shows the documented future shape. It is advisory metadata and ignored by current execution.

File: `.cyclestone/milestone.yml`

```yaml
milestones:
  - id: 0009-setup-validation
    title: Setup Validation
    spec_path: milestones/0009-setup-validation.md
    source:
      type: briefing
      plan_id: onboarding-improvements
      briefing_id: setup-recovery-paths
```

File: `.cyclestone/milestones/0009-setup-validation.md`

```markdown
# Milestone Spec: 0009-setup-validation - Setup Validation

## Goal
Validate first-run setup recovery behavior.

## Acceptance Criteria
- [ ] Cancelled setup does not create partial milestone configuration.
- [ ] Failed setup can be retried from the TUI.
```

### Archived Plan Whose Generated Milestones Still Exist

File: `.cyclestone/plans/legacy-reporting-cleanup.yml`

```yaml
schema_version: 1
id: legacy-reporting-cleanup
title: Legacy reporting cleanup
objective: Retain historical planning context for reporting milestones that still exist.
status: archived
created_at: "2026-06-01T09:00:00Z"
created_by: "patrick"
updated_at: "2026-07-20T13:00:00Z"
updated_by: "patrick"
briefing_order:
  - migrate-report-layout
briefings:
  - id: migrate-report-layout
    title: Migrate report layout
    objective: Move cycle artifacts to hierarchical report directories.
    intent: Report files should be grouped by milestone and cycle for easier review.
    status: archived
    milestone_id: 0005-hierarchical-reports
    completion_signal: Existing reports are readable from the hierarchical paths.
    created_at: "2026-06-01T09:10:00Z"
    created_by: "patrick"
    updated_at: "2026-07-20T13:00:00Z"
    updated_by: "patrick"
```

File: `.cyclestone/milestone.yml`

```yaml
milestones:
  - id: 0005-hierarchical-reports
    title: Hierarchical Reports
    spec_path: milestones/0005-hierarchical-reports.md
```

The archived Plan and Briefing do not invalidate `0005-hierarchical-reports`, its runtime state, or any reports under `.cyclestone/reports/milestones/0005-hierarchical-reports/`.
