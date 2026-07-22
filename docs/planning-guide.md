# Planning Guide (Optional Planning Layer)

This guide documents Cyclestone's **optional** planning layer: the Milestone Planner, Milestone Plans, and Milestone Briefings that sit *above* the existing core execution model. The planning layer is opt-in. Existing repositories require no migration, and standalone Milestone workflows continue to work with no Plan and no Briefing.

For the full persistence schema, field reference, validation rules, and status lifecycle tables, see [Planning Data Models](planning-data-models.md). For the architecture overview and one-way dependency direction, see [Architecture](architecture.md).

## Two-Layer Model

Cyclestone has two layers. The **core execution model** is unchanged and required:

```text
Milestone -> Milestone Cycles
```

The **optional planning model** sits above the core and depends on it, never the reverse:

```text
Milestone Planner -> Milestone Plan -> Milestone Briefing -> optional relation to one Milestone
```

- The **Milestone Planner** is a virtual UI/navigation root. It has no required persisted identity.
- A **Milestone Plan** groups related Briefings. It does not own Milestone runtime state, reports, branches, or cycles.
- A **Milestone Briefing** captures planning intent for *possible* Milestone work. A Briefing may never create a Milestone, may later create one Milestone, or may reference one existing Milestone.
- A **Milestone** remains the independent execution unit backed by the compact index entry, optional long-form spec, runtime state, and reports.

## Architecture: One-Way Dependency

Dependency direction is strictly one-way. Planning concepts may point to Milestones; Milestone config, runtime state, executor paths, reports, and Cycles must never depend on Plans, Briefings, or Planner state.

```text
+-------------------+      depends on      +------------------+      depends on      +------------------+
|  Planning Layer   |  --------------->   |  Milestone Layer |  --------------->   |   Cycle Engine   |
| Planner/Plan/     |                      | milestone.yml /  |                      | executor.Execute |
| Briefing (opt-in) |                      | milestones/*.md /|                      | reports / state  |
| plans/*.yml       |                      | state.json       |                      | branches         |
+-------------------+                      +------------------+                      +------------------+
        ^                                      ^
        |  never                               |  never
        |  depends on                          |  depends on
        +--------------------------------------+
                    REVERSE DIRECTION IS NEVER ALLOWED
```

Consequences of the one-way rule:

- A missing, deleted, or archived Plan or Briefing must not invalidate a Milestone or any of its reports.
- Removing a Plan or Briefing affects only planning metadata under `.cyclestone/plans/`.
- The executor (`internal/executor`) remains planning-agnostic for ordinary cycle execution. Planning origin metadata stays in the TUI/CLI wrapper and is not passed into `executor.ExecuteCycle` as run state.

## Terminology

| Term | Meaning |
| --- | --- |
| Milestone Planner | Virtual UI/navigation root that presents Plans and standalone Milestones. No required persisted identity. |
| Milestone Plan | Planning intent for related work. Groups Briefings; does not own Milestone execution state or reports. Stored as one `.cyclestone/plans/<plan-id>.yml` file. |
| Milestone Briefing | Actionable preparation for possible Milestone work. Embedded in a Plan. May reference zero or one Milestone. |
| Milestone | The independent execution unit: compact index entry, optional long-form spec, runtime state, and reports. Standalone or planned. |
| Milestone Cycle | One execution pass for a Milestone. Cycles belong to Milestones only, never to Plans or Briefings. |
| Standalone Milestone | A Milestone with no Plan or Briefing relationship. Fully first-class; requires no planning data. |
| Provenance | Optional advisory metadata on a Milestone indicating it came from a Briefing (`source: { type: briefing, plan_id, briefing_id }`). Advisory and backward-compatible; current execution ignores it. |

## Key Invariants

- Plans and Briefings are optional.
- Existing users can continue using only Milestones.
- Milestones remain independently creatable.
- Milestones remain independently executable.
- Briefings can generate Milestones (`briefing generate-milestone`).
- Briefings can link existing Milestones (`briefing link`).
- Links do not imply ownership. A Briefing link is a reference, not lifecycle control.
- Removing Plans or Briefings does not remove Milestones.
- The Milestone Planner is a virtual UI/navigation root with no required persisted identity.
- The planning layer depends on the Milestone layer, not the reverse.
- Planning CLI and TUI surfaces reuse existing Milestone controls/screens wherever practical rather than duplicating the core workflow.

## Working Without Plans (Standalone Milestones)

You are never required to use Plans or Briefings. The standalone path is fully supported:

1. Create a Milestone from the TUI (`c` from the dashboard) or by adding an entry to `.cyclestone/milestone.yml` and a spec under `.cyclestone/milestones/`.
2. Run it from the dashboard with `r`, confirm the preflight review, and the normal PM -> Developer -> QA -> Recommender cycle executes.
3. Reports are written under `.cyclestone/reports/milestones/<milestone-id>/`, runtime state under `.cyclestone/state.json`.
4. Delete or archive the Milestone through the existing TUI flows.

**No migration is required.** A repository with no `.cyclestone/plans/` directory is valid. `.cyclestone/milestone.yml`, `.cyclestone/milestones/*.md`, `.cyclestone/state.json`, and `.cyclestone/reports/milestones/<milestone-id>/` remain valid without any planning data.

## Creating a Plan Manually

```bash
cyclestone plan create onboarding-improvements \
  --title "Improve onboarding" \
  --objective "Make first-run setup easier to understand and recover from." \
  --actor patrick
```

Then add Briefings and dependencies:

```bash
cyclestone briefing add onboarding-improvements setup-copy-review \
  --title "Review setup copy" \
  --objective "Clarify first-run setup labels and confirmation text." \
  --intent "Users should understand what files setup will create before confirmation." \
  --completion-signal "Setup copy is reviewed and accepted in the TUI."

cyclestone briefing add onboarding-improvements setup-recovery-paths \
  --title "Define setup recovery paths" \
  --objective "Describe recovery behavior when setup is cancelled or fails." \
  --intent "Users should be able to retry setup without partial configuration surprises." \
  --completion-signal "Recovery paths are documented and validated against current setup flow."

cyclestone briefing dependency add onboarding-improvements setup-recovery-paths setup-copy-review
```

`plan create` creates `.cyclestone/plans/` on first use and writes only `.cyclestone/plans/<plan-id>.yml`. It does not create, mutate, or delete Milestone specs, compact index entries, state, reports, temp files, or branch snapshots.

## Creating a Plan Through AI (`plan generate`)

```bash
cyclestone plan generate --goal "Improve reporting reliability after interrupted runs"
```

Flags:

- `--goal <goal>`: high-level goal for the AI Planner (required).
- `--preview`: print the generated Plan through the same renderer as `plan show` and write nothing.
- `--actor <actor>`: actor recorded in planning metadata (default `ai-plan-generator`).
- `--runner-command <command>`: shell command that returns one structured JSON Plan object.
- `--response-file <path>`: read the structured JSON response from a local file instead of invoking a runner (useful for tests and deterministic workflows).

The generation prompt is bounded and assembled from stable repository context (`AGENTS.md`, `.cyclestone/DECISIONS.md`, `docs/architecture.md`, `docs/planning-data-models.md`, and tracked repository structure). Cyclestone parses the JSON, derives lowercase Plan and Briefing IDs from titles, normalizes same-Plan dependencies, sets Plan and Briefing status to `active`, rejects any generated `milestone_id` (generated Briefings are active, same-Plan only, and cannot carry Milestone links), validates through the same planning validator used by `SavePlan`, and then writes only `.cyclestone/plans/<plan-id>.yml`. Invalid JSON, missing required fields, unmappable dependencies, dependency cycles, generated Milestone links, validation errors, or Plan ID collisions fail before any Plan file is written.

Review-only mode (`--preview`) and response-file usage both bypass runner execution while still using the same parser, converter, validation, collision checks, and preview/save behavior.

## Reviewing Briefings

Briefing review reuses the planning status model:

- `briefing approve <plan-id> <briefing-id>` sets status to `completed`.
- `briefing reject <plan-id> <briefing-id>` sets status to `archived` and removes the Briefing from active `briefing_order` while preserving the embedded record and any `milestone_id`.
- `plan approve <plan-id>` / `plan reject <plan-id>` do the same at the Plan level.

Approve/reject do not execute Plans, create Milestones, start runner cycles, or mutate Milestone specs, compact index entries, state, reports, temp files, cycle artifacts, or branch snapshots.

### Split and Merge (Visual Diff Workflow)

`briefing split` and `briefing merge` rewrite only the containing typed Plan file. They never edit or delete linked or standalone Milestone storage.

- `briefing split <plan-id> <briefing-id> --parts-file <path>`: reads a JSON parts file (`{"parts":[...]}` or a top-level array). Each part supplies `id`, `title`, `objective`, `intent`, and `completion_signal`, with optional `status`, `constraints`, and `depends_on`. The source Briefing is removed and the new part IDs are inserted where the source appeared in `briefing_order`. If the source has `milestone_id`, the command fails unless `--milestone-link <part-id|none>` explicitly selects the part that keeps the link or clears it.
- `briefing merge <plan-id> <target-briefing-id> <merged-briefing-id> [<merged-briefing-id>...] --title ... --objective ... --intent ... --completion-signal ...`: keeps the first ID as the stable target, unions dependencies (excluding merged IDs and self-dependencies), and rewrites external dependents of merged-away Briefings to the target. If multiple merged Briefings have `milestone_id`, the command fails unless `--milestone-link <briefing-id|none>` selects one to preserve or clears the link.

## Generating a Milestone from a Briefing

```bash
cyclestone briefing generate-milestone onboarding-improvements setup-recovery-paths
```

Flags:

- `--milestone-id <id>`: explicit Milestone ID (defaults to `<plan-id>-<briefing-id>`).
- `--preview`: print the proposed Milestone ID, spec, and Briefing link without writing milestone or planning data.
- `--replace-link`: replace an existing Briefing Milestone link without deleting the previously linked Milestone.
- `--actor <actor>`: actor recorded in planning metadata.

The command validates all Plan files before writing, requires the selected Briefing to be `completed`, requires every same-Plan dependency to also be `completed`, refuses an existing Briefing link unless `--replace-link` is supplied, writes a long-form Markdown spec under `.cyclestone/milestones/`, appends a compact entry to `.cyclestone/milestone.yml`, and only then stores the generated `milestone_id` back on the source Briefing. Replacing a Briefing link does not delete or mutate the previously linked Milestone. The generated Milestone is an ordinary, independently executable Milestone.

## Linking an Existing Milestone

```bash
cyclestone briefing link onboarding-improvements setup-copy-review 0008-fix-runner-status
```

Flags: `--replace-link` (replace an existing different Milestone link), `--actor <actor>`.

`briefing link` is strict:

- The Milestone ID must already exist in the compact Milestone index.
- The target Briefing must not already link a different Milestone unless `--replace-link` is supplied.
- No other active or completed Briefing in any valid Plan may already link the same Milestone (single-link cardinality across Plans).
- Replacement changes only the selected Briefing's `milestone_id` and planning metadata; it does not create, delete, or mutate either the previously linked Milestone or the replacement Milestone.

`briefing unlink <plan-id> <briefing-id>` removes only the Briefing's `milestone_id`. It never modifies linked Milestone specs, compact index entries, runtime state, reports, temp files, branch snapshots, or cycles.

## Reviewing and Replacing Links

CLI detail views (`plan show`, `briefing show`) derive relation labels from loaded Plan files plus the compact Milestone ID set:

- `milestone: none` — Briefing has no link.
- `milestone: linked <id> (standalone)` — Briefing links a Milestone not linked by any other Briefing.
- `milestone: linked <id> (also linked by Plan <plan-id> Briefing <briefing-id>)` — cross-Plan duplicate detection for awareness.
- `milestone: missing <id>` — the linked Milestone ID is not in the compact index (dangling link; a warning, not an error).

Link replacement with `briefing link --replace-link` is the explicit repair path: it validates the replacement Milestone exists, blocks active/completed cross-Plan duplicate links, and updates only the containing typed Plan file. Provenance reconciliation is advisory: a Milestone's optional `source: { type: briefing, plan_id, briefing_id }` is metadata only and is ignored by current execution. A dangling `source.plan_id` or `source.briefing_id` never invalidates a Milestone spec or compact index entry.

## Executing One Briefing

```bash
cyclestone briefing execute onboarding-improvements setup-recovery-paths
```

`briefing execute` executes exactly one active or completed Briefing whose same-Plan dependencies are completed. It is the narrow bridge between the planning layer and the core execution model:

- A valid existing `milestone_id` resolves to that ordinary Milestone without rewriting its index entry, spec, state, or reports.
- An unlinked Briefing reuses the same generation preparation path, persists its new Milestone link, reloads normal Milestone config and state, and opens the **existing** preflight and runner workflow.
- The executor receives only the resolved `config.Milestone` and normal run options. Planning origin stays in the TUI wrapper.
- An `approved` terminal result best-effort marks only the selected Briefing `completed` after reloading and revalidating its link. `failed`, `blocked`, executor errors, and cancellation do not advance the Briefing.
- A post-cycle Plan save failure is shown as a warning and never rolls back or invalidates cycle state, reports, branch snapshots, or the generated/linked Milestone.
- **Partial-success behavior:** if Milestone creation succeeds but link persistence fails, the generated standalone Milestone is preserved and execution stops with an explicit partial-success error. A later `plan resume` may reclaim only the deterministic generated ID whose title, spec path, and embedded Plan/Briefing source markers prove it is the interrupted output for that same Briefing; ambiguous collisions stop for explicit repair.

## Executing a Complete Plan

```bash
cyclestone plan start onboarding-improvements --mode continuous
cyclestone plan resume onboarding-improvements --approve
```

`plan start` and `plan resume` run in the pre-TUI CLI layer and persist optional `execution` checkpoints in the selected typed Plan. The coordinator selects only same-Plan dependency-ready Briefings (archived and completed Briefings are skipped; an active Briefing is eligible only when all same-Plan dependencies are completed), resolves or generates one ordinary linked Milestone at a time, and carries immutable queue context through the TUI while `internal/executor` remains planning-agnostic.

Execution modes (`--mode once|continuous|review`, overriding `default_plan_execution_mode`):

- `once`: reconcile or execute one eligible Briefing, then pause (`paused` / `one-complete`).
- `continuous`: continue selecting eligible Briefings after each approved result until exhaustion, failure, or dependency deadlock.
- `review`: persist an `approval-required` checkpoint before each Milestone cycle. `plan resume <id> --approve` consumes approval only for the displayed Briefing/Milestone gate.

Terminal states:

- `completed` / `exhausted`: no incomplete work remains.
- `blocked` / `dependency-deadlock`: incomplete work remains but none is eligible.
- `stopped`: an actionable stop (identity mismatch, dangling/edited link, uncertain launch boundary).

Resume always reloads the Plan, compact Milestone index, and `state.json`. Approval consumption and the final preflight-to-runner transition both require the retained Plan to remain approved, the Briefing to remain active and dependency-ready, its exact retained link to remain intact, and the Milestone to remain indexed. A dangling or edited link is preserved and requires explicit repair. If a process ends after `cycle-running` was saved but no terminal Milestone status exists, every resume remains non-launchable and stops for inspection until runtime state is reconciled. Navigation after runner cancellation hides visible Plan context but retains callback ownership until the delayed terminal event durably stops the selected Briefing.

The TUI preflight and runner show Plan ID, Briefing ID, queue position, dependency readiness, and mode while continuing to execute an ordinary Milestone. Standalone Milestones are never selected by a Plan run merely because they exist in the compact index. Plan lifecycle operations never cascade into Milestone artifacts.

## Replanning (`plan reevaluate`)

```bash
cyclestone plan reevaluate onboarding-improvements --goal "Re-scope after setup rework"
cyclestone plan reevaluate onboarding-improvements --preview
cyclestone plan reevaluate onboarding-improvements --auto-apply
```

`plan reevaluate` triggers an AI Planner re-evaluation of remaining incomplete Briefings in an active Plan. It may also be triggered post-execution when `enable_plan_reevaluation` is enabled in configuration. Proposals may add, remove, reorder, split, merge, update, or block Briefings.

Flags: `--goal <goal>`, `--preview`, `--auto-apply`, `--actor <actor>` (default `ai-planner`), `--runner-command <command>`, `--response-file <path>`.

### Safety Invariants

1. Replanning modifies only planning-layer entities (`.cyclestone/plans/*.yml`).
2. Existing or completed Milestones, compact index entries, state, reports, and branch snapshots are never modified, rewritten, or deleted.
3. Removing or merging a Briefing preserves all linked Milestones and execution history intact on disk.
4. Standalone Milestones remain outside Plans unless explicitly linked through user-approved suggestions; silent linking is strictly prohibited.
5. Proposed Plan modifications require explicit user approval before applying changes to disk, unless `--auto-apply` or `auto_apply_plan_reevaluation` is explicitly configured.

### Visual Diff Symbols

Proposed changes are computed by `config.ComputePlanDiff` and rendered as a structured visual diff in `internal/tui/details.go` (`RenderPlanDiff`). Invariant validation runs before presenting the diff; invalid proposals block execution and leave files unchanged.

| Symbol | Kind | Meaning |
| --- | --- | --- |
| `+ [ADD]` | added | New Briefing added. |
| `- [REMOVE]` | removed | Briefing removed. |
| `~ [UPDATE]` | modified | Briefing property updated (objective, constraints, dependencies, etc.). |
| `^ [REORDER]` | reordered | Briefing position in `briefing_order` changed. |
| `! [BLOCKED]` | blocked | Briefing blocked from execution. |
| `/ [SPLIT]` | split | Briefing split into parts. |
| `+ [MERGE]` | merge | Briefings merged into one. |
| `* [CHANGE]` | default / other | Default or fallback change indicator. |

Milestone link relationships and link suggestions are rendered as indented sub-lines under Briefing diffs rather than top-level symbols:
- `Link Suggestion: <milestone-id> (requires user approval)` (when `IsLinkSuggested` is true)
- `Linked Milestone: <milestone-id>` (when an existing milestone link is present)

## Hierarchy Visualization (`plan tree`)

```bash
cyclestone plan tree
cyclestone plan tree onboarding-improvements
cyclestone plan tree --ascii
```

`plan tree` renders the planning hierarchy from the virtual root "Milestone Planner" down through Plans, Briefings, linked Milestones, and Milestone Cycles. Standalone Milestones (those with no Briefing link) are strictly excluded. An optional `<plan-id>` argument filters to one Plan. `--ascii` forces ASCII-safe branch glyphs (`|--`, `\--`, `|`); otherwise the renderer falls back to ASCII glyphs in the VS Code integrated terminal or when explicit styling is disabled. Terminal width truncation is enforced. The renderer is a shared component in `internal/tui/tree.go` and is reused by the TUI dashboard's planner hierarchy view; it never mutates planning files or Milestone artifacts.

## Archiving and Deletion

Archiving and deletion affect **only** planning metadata under `.cyclestone/plans/`. They never cascade to Milestone specs, compact index entries, `state.json`, reports, temp files, or branch snapshots.

- `plan archive <plan-id>` / `briefing archive <plan-id> <briefing-id>`: set status to `archived`; records are retained for history and excluded from derived progress.
- `plan restore <plan-id>` / `briefing restore <plan-id> <briefing-id>`: restore archived records to `active`.
- `plan delete <plan-id> --confirm <plan-id>`: removes only `.cyclestone/plans/<plan-id>.yml`. Requires an exact confirmation token matching the Plan ID.
- `briefing delete <plan-id> <briefing-id> --confirm <briefing-id>`: removes only the embedded Briefing record and its active `briefing_order` entry. Requires an exact confirmation token matching the Briefing ID.
- `briefing unlink`, archive, delete, and Plan delete never modify linked Milestone specs, compact index entries, runtime state, reports, temp files, branch snapshots, or cycles.
- Deleting a Milestone leaves referring Briefings in a safe `milestone: missing <id>` state without corrupting Plan validation or blocking unrelated Briefing execution.

`Planning operations affect planning metadata only. Linked milestone specs, compact index entries, runtime state, reports, and branch snapshots are never modified or deleted.`

## CLI Reference

All planning commands run before TUI startup and load `.cyclestone/plans/*.yml` relative to the configured `-config` file directory. Display commands use the existing milestone index and loaded planning files only to label Briefing relationships. Mutating commands validate all Plan files before writing and save only typed `.cyclestone/plans/<plan-id>.yml` records through `config.SavePlan`. No planning command mutates Milestone specs, compact index entries, state, reports, temp files, cycle artifacts, or branch snapshots (the narrow exception is `briefing generate-milestone`, `briefing execute`, and `plan start`/`plan resume`, which create or execute ordinary Milestones through the existing Milestone paths).

### Read-only navigation

| Command | Description |
| --- | --- |
| `plan list` | Deterministic Plan rows: ID, title, status, Briefing count, derived progress, and execution state. |
| `plan show <plan-id>` | One Plan with metadata, progress, and Briefings in `briefing_order`, plus remaining addressable Briefings sorted by ID. |
| `briefing show <plan-id> <briefing-id>` | One Briefing with objective, intent, completion signal, dependencies, constraints, derived readiness, and Milestone relationship. |
| `plan tree [--ascii] [<plan-id>]` | Planning hierarchy tree; excludes standalone Milestones; optional Plan filter and ASCII glyph fallback. |

### Plan management

| Command | Description |
| --- | --- |
| `plan create <plan-id> --title <title> --objective <objective> [--actor <actor>]` | Create a new active Plan file. |
| `plan edit <plan-id> [--title <title>] [--objective <objective>] [--actor <actor>]` | Edit Plan metadata (requires at least one field). |
| `plan approve <plan-id> [--actor <actor>]` | Set Plan status to `completed`. |
| `plan reject <plan-id> [--actor <actor>]` | Set Plan status to `archived`. |
| `plan archive <plan-id> [--actor <actor>]` | Set Plan status to `archived`. |
| `plan restore <plan-id> [--actor <actor>]` | Restore an archived Plan to `active`. |
| `plan delete <plan-id> --confirm <plan-id>` | Delete the Plan file (exact confirmation token). |
| `plan generate --goal <goal> [--preview] [--actor <actor>] [--runner-command <command>] [--response-file <path>]` | AI-assisted Plan generation. |
| `plan reevaluate <plan-id> [--goal <goal>] [--preview] [--auto-apply] [--actor <actor>] [--runner-command <command>] [--response-file <path>]` | AI-assisted Plan re-evaluation with visual diff. |
| `plan start <plan-id> [--mode once\|continuous\|review]` | Start Plan orchestration. |
| `plan resume <plan-id> [--mode once\|continuous\|review] [--approve]` | Resume Plan orchestration; `--approve` consumes a review gate. |

### Briefing management

| Command | Description |
| --- | --- |
| `briefing add <plan-id> <briefing-id> --title <title> --objective <objective> --intent <intent> --completion-signal <signal> [--actor <actor>]` | Add a new active Briefing to a Plan. |
| `briefing edit <plan-id> <briefing-id> [metadata flags] [--actor <actor>]` | Edit Briefing metadata (requires at least one field). |
| `briefing reorder <plan-id> <briefing-id> [<briefing-id>...] [--actor <actor>]` | Reorder Briefings starting at the first given ID. |
| `briefing approve <plan-id> <briefing-id> [--actor <actor>]` | Set Briefing status to `completed`. |
| `briefing reject <plan-id> <briefing-id> [--actor <actor>]` | Set Briefing status to `archived` and remove from active order. |
| `briefing archive <plan-id> <briefing-id> [--actor <actor>]` | Set Briefing status to `archived`. |
| `briefing restore <plan-id> <briefing-id> [--actor <actor>]` | Restore an archived Briefing to `active`. |
| `briefing delete <plan-id> <briefing-id> --confirm <briefing-id>` | Delete the embedded Briefing record (exact confirmation token). |
| `briefing split <plan-id> <briefing-id> --parts-file <path> [--milestone-link <part-id\|none>] [--actor <actor>]` | Split a Briefing into parts from a JSON file. |
| `briefing merge <plan-id> <target-briefing-id> <merged-briefing-id> [<merged-briefing-id>...] --title <title> --objective <objective> --intent <intent> --completion-signal <signal> [--status <status>] [--milestone-link <briefing-id\|none>] [--actor <actor>]` | Merge Briefings into the target ID. |
| `briefing dependency add <plan-id> <briefing-id> <dependency-id> [--actor <actor>]` | Add a same-Plan dependency. |
| `briefing dependency remove <plan-id> <briefing-id> <dependency-id> [--actor <actor>]` | Remove a same-Plan dependency. |
| `briefing link <plan-id> <briefing-id> <milestone-id> [--replace-link] [--actor <actor>]` | Link an existing Milestone (strict single-link cardinality). |
| `briefing unlink <plan-id> <briefing-id> [--actor <actor>]` | Remove a Briefing's Milestone link. |
| `briefing generate-milestone <plan-id> <briefing-id> [--milestone-id <id>] [--preview] [--replace-link] [--actor <actor>]` | Generate one ordinary Milestone from a completed Briefing. |
| `briefing execute <plan-id> <briefing-id>` | Execute one Briefing through the existing preflight and cycle engine. |

## TUI Reference

TUI planning navigation is a multi-level flat-table and detail hierarchy. It reuses the existing milestone preflight and runner screens rather than duplicating them. There are no planning-specific runner controls; Briefing and Plan execution launch the ordinary preflight (`internal/tui/preflight.go`) and runner (`internal/tui/runner.go`) workflow.

### Screen hierarchy

```text
Dashboard (p) -> ScreenPlans -> ScreenPlanDetails -> ScreenBriefingDetails
                         |-> Create Plan / Delete Plan
```

- **ScreenPlans** (`internal/tui/plans.go`): flat table of Plans with ID, Title, Status, Briefings progress (`completed/total`), and Execution state. `c` opens Plan creation, `d` deletes the selected Plan after confirmation, `Enter` opens Plan details, and `Esc`/`Backspace`/`p` returns to the dashboard.
- **ScreenPlanDetails** (`internal/tui/plans.go`): Plan metadata plus a flat Briefings table with ID, Title, Status, Dependencies, and Milestone Link (`[unlinked]`, `[linked: <id>]`, or `[missing: <id>]`). `d` opens deletion confirmation for this Plan, `Enter` opens Briefing details, and `Esc`/`Backspace` steps back to the Plans list.
- **ScreenBriefingDetails** (`internal/tui/plans.go`): Briefing details styled to match Milestone details, showing Objective, Intent, Completion Signal, Constraints, and linked Milestone status with recent cycle logs. `Esc`/`Backspace` steps back to Plan details. `↑/↓`/`j`/`k`/`pgup`/`pgdn` scroll.

Plan creation prompts for the required lowercase-hyphenated Plan ID, title, and objective. Cyclestone supplies active status, timestamps, the local `tui` actor, and empty Briefing collections, validates all existing Plan files, and saves only `.cyclestone/plans/<plan-id>.yml`. `Tab`/`Shift+Tab` move between fields and actions; `Esc` or Cancel returns without writing.

Plan deletion visibly identifies the selected ID and title and requires entering the exact Plan ID. Cancelling changes nothing. A confirmed deletion removes only that Plan YAML record and never deletes or changes linked Milestone specs, compact index entries, runtime state, reports, cycles, temp artifacts, branches, or snapshots. The Plans list is reloaded and remains usable when the last Plan is removed.

Multi-level `Esc`/`Backspace` step-back navigation is strictly enforced: Briefing Details -> Plan Details -> Plans -> Dashboard.

### Planner hierarchy tree view

`internal/tui/tree.go` exposes a shared `RenderTree` component that builds the planning hierarchy from the virtual "Milestone Planner" root down through Plans, Briefings, linked Milestones, and Milestone Cycles. Standalone Milestones are excluded. The same component powers the CLI `plan tree` command and the TUI dashboard's planner hierarchy tree view. Terminal width truncation and VS Code integrated terminal / explicit ASCII branch glyph fallbacks (`|--`, `\--`, `|`) are enforced. Tree rendering never mutates planning files or Milestone artifacts.

### Reused preflight and runner screens

When a Briefing or Plan execution launches a Milestone cycle, the TUI reuses the existing preflight and runner screens:

- `internal/tui/preflight.go` shows Plan ID, Briefing ID, queue position (`Queue: N/M`), Plan execution mode, and dependency readiness alongside the normal preflight review (cycle number, agent group, runner/model, sandbox mode, branch behavior, report paths, Git status, warnings/blockers).
- `internal/tui/runner.go` shows the immutable Plan/Briefing origin context via the runner's Plan context header and `PLAN` tab while continuing to execute an ordinary Milestone with the standard cycle status, per-agent state, usage metrics, and final summaries.

The executor remains planning-agnostic; planning origin metadata is carried by the TUI wrapper (`BriefingOrigin`) and never injected into `executor.ExecuteCycle` as run state.

## Data Examples

The full schema, field reference, and additional examples (including an archived Plan whose generated Milestones still exist) are in [Planning Data Models](planning-data-models.md). Two illustrative end-to-end examples follow.

### Standalone Milestone (no Plan, no Briefing)

`.cyclestone/milestone.yml`:

```yaml
milestones:
  - id: 0008-fix-runner-status
    title: Fix Runner Status
    spec_path: milestones/0008-fix-runner-status.md
```

`.cyclestone/milestones/0008-fix-runner-status.md`:

```markdown
# Milestone Spec: 0008-fix-runner-status - Fix Runner Status

## Goal
Correct stale runner status rendering after a cycle finishes.

## Acceptance Criteria
- [ ] The runner view shows the final cycle status after completion.
- [ ] Existing cycle reports remain readable.
```

`.cyclestone/state.json` (illustrative fragment):

```json
{
  "active_milestone_id": "0008-fix-runner-status",
  "milestone_statuses": { "0008-fix-runner-status": "In Progress" },
  "milestone_cycles": { "0008-fix-runner-status": 2 }
}
```

Reports land under `.cyclestone/reports/milestones/0008-fix-runner-status/`. No `.cyclestone/plans/` entry is needed; provenance is absent.

### Planned Milestone (Plan, Briefing, link, provenance)

`.cyclestone/plans/reporting-reliability.yml`:

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

`.cyclestone/milestone.yml` (with optional advisory provenance — a documented future-compatible shape; current execution ignores it):

```yaml
milestones:
  - id: 0007-preserve-cycle-metadata
    title: Preserve Cycle Metadata
    spec_path: milestones/0007-preserve-cycle-metadata.md
    source:
      type: briefing
      plan_id: reporting-reliability
      briefing_id: preserve-cycle-metadata
```

`.cyclestone/state.json` (illustrative fragment):

```json
{
  "milestone_statuses": { "0007-preserve-cycle-metadata": "Done" },
  "milestone_cycles": { "0007-preserve-cycle-metadata": 1 }
}
```

If the `reporting-reliability` Plan is later archived or deleted, `0007-preserve-cycle-metadata`, its state, and its reports under `.cyclestone/reports/milestones/0007-preserve-cycle-metadata/` remain valid and untouched.

## Migration Guidance

No migration is required to adopt the planning layer.

- `.cyclestone/plans/` is optional. Old projects with no `.cyclestone/plans/` directory are valid.
- No existing file needs to move. `.cyclestone/milestone.yml`, `.cyclestone/milestones/*.md`, `.cyclestone/state.json`, and `.cyclestone/reports/milestones/<milestone-id>/` remain in their existing locations and remain valid without planning data.
- Planning files may be added, removed, archived, or malformed without changing Milestone execution validity.
- Existing Milestones do not need to be migrated into Plans. The planning layer is not required for Milestone creation or execution.
- Planning validation failures are isolated to planning views and planning commands.

## Backward Compatibility Guarantees

- `.cyclestone/milestone.yml` remains the compact Milestone index and remains valid without any Plan or Briefing data.
- `.cyclestone/milestones/*.md` remains the long-form Milestone spec location and remains valid without planning provenance.
- `.cyclestone/state.json` remains keyed by Milestone runtime progress and remains valid without planning state.
- `.cyclestone/reports/milestones/<milestone-id>/` remains keyed by Milestone ID. Existing report directories are not migrated when planning metadata is added.
- `.cyclestone/plans/*.yml` is optional and additive. Adding planning files never rewrites Milestone specs, compact index entries, state, reports, temp files, or branch snapshots.
- Existing projects require no migration for the optional planning layer.
- Standalone and generated Milestones function as fully independent first-class entities regardless of source Plan or Briefing archival, deletion, or missing status.

## Troubleshooting

### `milestone: missing <id>` warnings

A Briefing carries a `milestone_id` that is not present in the compact Milestone index. This is a **warning**, not an error: the Plan remains valid and unrelated Briefings or Milestones are not blocked. Common causes: the linked Milestone was deleted, or the Briefing references a Milestone ID that was never created.

Repair path:

- Re-create the Milestone, or
- Replace the link with `briefing link --replace-link <plan-id> <briefing-id> <existing-milestone-id>`, or
- Clear the link with `briefing unlink <plan-id> <briefing-id>`.

The repair mutates only the containing typed Plan file. It never deletes or alters the previously linked (or missing) Milestone's specs, index entries, state, reports, or branch snapshots.

### Dangling Briefing links

A Briefing points at a Milestone that no longer exists (same root cause as `missing <id>`). The link is preserved and surfaced as a warning so history is not silently lost. `plan start`/`plan resume` treat a dangling or edited link as an actionable stop and require explicit repair before the Briefing can advance; they never auto-create or auto-relink a Milestone.

### Stale provenance

A Milestone carries optional `source: { plan_id, briefing_id }` pointing at a Plan or Briefing that was archived or deleted. Provenance is advisory metadata only and is ignored by current execution, status loading, report paths, deletion flows, cycle flows, and prompt assembly. A dangling `source.plan_id` or `source.briefing_id` never invalidates a Milestone spec or compact index entry. No repair is required for the Milestone to remain executable.

### Cross-Plan duplicate links

`briefing link` blocks an active or completed Briefing from linking a Milestone already linked by an active or completed Briefing in another Plan. Detail views surface `milestone: linked <id> (also linked by Plan <plan-id> Briefing <briefing-id>)` for awareness. Use `briefing unlink` on one Briefing or `briefing link --replace-link` to reconcile.

### Uncertain active-cycle checkpoint

If a `plan start`/`plan resume` process ends after a `cycle-running` checkpoint was saved but no terminal Milestone status exists, every resume remains non-launchable and stops for inspection until the runtime state in `.cyclestone/state.json` is reconciled to a terminal status. This protects against double-launching the same cycle.

### Repair does not block unrelated work

None of the above conditions block unrelated Briefing execution, standalone Milestone creation/execution, or unrelated Plan operations. Planning warnings are isolated to the affected Plan/Briefing and never invalidate Milestone specs, compact index entries, state, reports, temp files, or branch snapshots.
