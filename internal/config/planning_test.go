package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPlanningStateEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	state, result := LoadPlanningState(filepath.Join(tmpDir, "plans"))
	if result.HasErrors() || result.HasWarnings() {
		t.Fatalf("expected no validation messages for missing plans dir, got %+v", result.Messages)
	}
	if len(state.Plans) != 0 {
		t.Fatalf("expected empty planning state, got %+v", state.Plans)
	}

	plansDir := filepath.Join(tmpDir, "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		t.Fatalf("failed to create plans dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plansDir, "ignored.yaml"), []byte("not: loaded\n"), 0644); err != nil {
		t.Fatalf("failed to write ignored file: %v", err)
	}
	state, result = LoadPlanningState(plansDir)
	if result.HasErrors() || len(state.Plans) != 0 {
		t.Fatalf("expected empty state for no *.yml files, state=%+v result=%+v", state, result)
	}
}

func TestDeletePlanRemovesOnlyExactPlanningRecord(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	first := representativePlan()
	second := representativePlan()
	second.ID = "second-plan"
	second.Title = "Second Plan"
	if _, err := SavePlan(plansDir, first); err != nil {
		t.Fatal(err)
	}
	if _, err := SavePlan(plansDir, second); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(plansDir), "state.json")
	if err := os.WriteFile(unrelated, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := DeletePlan(plansDir, first.ID); err != nil {
		t.Fatalf("DeletePlan failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plansDir, first.ID+".yml")); !os.IsNotExist(err) {
		t.Fatalf("deleted Plan still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plansDir, second.ID+".yml")); err != nil {
		t.Fatalf("unselected Plan changed: %v", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated artifact changed: %q %v", data, err)
	}
	if err := DeletePlan(plansDir, "../state"); err == nil {
		t.Fatal("expected unsafe Plan ID to be rejected")
	}
	if err := DeletePlan(plansDir, "missing-plan"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing-file error, got %v", err)
	}
}

func TestPlanExecutionRoundTripAndValidation(t *testing.T) {
	t.Parallel()
	plan := representativePlan()
	dir := filepath.Join(t.TempDir(), "plans")
	if result, err := SavePlan(dir, plan); err != nil || result.HasErrors() {
		t.Fatalf("SavePlan() = %v, %+v", err, result)
	}
	// Execution state now lives in State.PlanExecutions, not in the Plan YAML.
	statePath := filepath.Join(t.TempDir(), "state.json")
	st, _ := LoadState(statePath)
	exec := &PlanExecution{
		Mode: PlanExecutionModeContinuous, State: "paused", Checkpoint: "approval-required",
		CurrentBriefingID: plan.Briefings[0].ID, PendingApproval: "before-cycle", UpdatedAt: plan.UpdatedAt,
	}
	st.SetPlanExecution(plan.ID, exec)
	if err := SaveState(statePath, st); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}
	reloaded, _ := LoadState(statePath)
	got := reloaded.GetPlanExecution(plan.ID)
	if got == nil || got.Mode != PlanExecutionModeContinuous || got.State != "paused" {
		t.Fatalf("execution state did not round trip via State: %+v", got)
	}
	// ValidatePlan no longer validates execution (it lives in state.json).
	planResult := ValidatePlan(plan, "plan.yml")
	if planResult.HasErrors() {
		t.Fatalf("expected clean plan validation without execution, got %+v", planResult)
	}
}

func TestPlanExecutionMissingCurrentBriefingIsRepairableWarning(t *testing.T) {
	t.Parallel()
	plan := representativePlan()
	// Execution state is centralized in State.PlanExecutions. The Plan YAML
	// itself should validate cleanly even when execution references a missing
	// briefing, because that condition is surfaced at runtime via State.
	result := ValidatePlan(plan, "plan.yml")
	if result.HasErrors() {
		t.Fatalf("plan should validate cleanly without inline execution: %+v", result.Messages)
	}
}

func TestPlanAndBriefingRoundTrip(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	plan := representativePlan()

	result, err := SavePlan(plansDir, plan, WithKnownMilestoneIDs([]string{"0003-persist-planning-layer"}))
	if err != nil {
		t.Fatalf("SavePlan failed: %v result=%+v", err, result)
	}

	state, result := LoadPlanningState(plansDir, WithKnownMilestoneIDs([]string{"0003-persist-planning-layer"}))
	if result.HasErrors() || result.HasWarnings() {
		t.Fatalf("expected clean reload, got %+v", result.Messages)
	}
	if len(state.Plans) != 1 {
		t.Fatalf("expected one plan, got %d", len(state.Plans))
	}
	got := state.Plans[0]
	if got.SchemaVersion != PlanningSchemaVersion || got.ID != plan.ID || got.Title != plan.Title || got.Objective != plan.Objective || got.Status != plan.Status {
		t.Fatalf("plan identity fields did not round trip: %+v", got)
	}
	if got.CreatedAt != plan.CreatedAt || got.CreatedBy != plan.CreatedBy || got.UpdatedAt != plan.UpdatedAt || got.UpdatedBy != plan.UpdatedBy {
		t.Fatalf("plan metadata did not round trip: %+v", got)
	}
	if strings.Join(got.Constraints, "|") != strings.Join(plan.Constraints, "|") {
		t.Fatalf("plan constraints did not round trip: %+v", got.Constraints)
	}
	if strings.Join(got.BriefingOrder, "|") != strings.Join(plan.BriefingOrder, "|") {
		t.Fatalf("briefing order did not round trip: %+v", got.BriefingOrder)
	}
	if len(got.Briefings) != 3 {
		t.Fatalf("expected three briefings, got %d", len(got.Briefings))
	}
	byID := map[string]Briefing{}
	for _, briefing := range got.Briefings {
		byID[briefing.ID] = briefing
	}
	active := byID["setup-copy-review"]
	if active.Title == "" || active.Objective == "" || active.Intent == "" || active.CompletionSignal == "" {
		t.Fatalf("required briefing text fields did not round trip: %+v", active)
	}
	if active.Status != "active" || active.CreatedAt != "2026-07-20T10:15:00Z" || active.UpdatedBy != "patrick" {
		t.Fatalf("active briefing metadata did not round trip: %+v", active)
	}
	if strings.Join(active.Constraints, "|") != "Do not change runner detection behavior." {
		t.Fatalf("briefing constraints did not round trip: %+v", active.Constraints)
	}
	completed := byID["persist-plan-files"]
	if completed.Status != "completed" || strings.Join(completed.DependsOn, "|") != "setup-copy-review" || completed.MilestoneID != "0003-persist-planning-layer" {
		t.Fatalf("optional briefing fields did not round trip: %+v", completed)
	}
	archived := byID["archived-note"]
	if archived.Status != "archived" {
		t.Fatalf("expected archived briefing to remain parseable, got %+v", archived)
	}
}

func TestLoadPlanningStateInvalidFilesAreScoped(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		t.Fatalf("failed to create plans dir: %v", err)
	}
	writeFile(t, filepath.Join(plansDir, "valid.yml"), validPlanYAML("valid-plan"))
	writeFile(t, filepath.Join(plansDir, "malformed.yml"), "schema_version: 1\nid: [broken\n")
	writeFile(t, filepath.Join(plansDir, "missing-fields.yml"), `id: Bad_ID
title: ""
objective: Missing fields
status: waiting
created_at: not-a-time
updated_at: "2026-07-20T10:00:00Z"
briefing_order:
  - missing
briefings: []
`)
	writeFile(t, filepath.Join(plansDir, "duplicate-a.yml"), validPlanYAML("duplicate-plan"))
	writeFile(t, filepath.Join(plansDir, "duplicate-b.yml"), validPlanYAML("duplicate-plan"))
	writeFile(t, filepath.Join(plansDir, "invalid-briefings.yml"), `schema_version: 1
id: invalid-briefings
title: Invalid Briefings
objective: Exercise validation errors
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: patrick
updated_at: "2026-07-20T11:00:00Z"
updated_by: patrick
briefing_order:
  - first
  - first
  - missing
briefings:
  - id: first
    title: First
    objective: First objective
    intent: First intent
    status: active
    depends_on:
      - second
    completion_signal: Done
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
  - id: first
    title: Duplicate
    objective: Duplicate objective
    intent: Duplicate intent
    status: completed
    completion_signal: Done
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
  - id: second
    title: Second
    objective: Second objective
    intent: Second intent
    status: active
    depends_on:
      - first
    completion_signal: Done
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
`)

	state, result := LoadPlanningState(plansDir)
	if !result.HasErrors() {
		t.Fatalf("expected validation errors")
	}
	if len(state.Plans) != 2 {
		t.Fatalf("expected valid non-duplicate plans to load, got %d: %+v", len(state.Plans), state.Plans)
	}
	messages := planningMessagesText(result)
	for _, want := range []string{
		"malformed YAML",
		"schema_version",
		"Plan ID",
		"invalid status",
		"created_at must be RFC3339",
		"duplicate Plan ID",
		"duplicate Briefing ID",
		"duplicate Briefing ID \"first\" in briefing_order",
		"briefing_order references missing Briefing \"missing\"",
		"dependency cycle",
	} {
		if !strings.Contains(messages, want) {
			t.Fatalf("expected validation messages to contain %q, got:\n%s", want, messages)
		}
	}
}

func TestPlanningWarningsForDanglingOptionalReferences(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		t.Fatalf("failed to create plans dir: %v", err)
	}
	writeFile(t, filepath.Join(plansDir, "references.yml"), `schema_version: 1
id: references
title: References
objective: Exercise warnings
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: patrick
updated_at: "2026-07-20T11:00:00Z"
updated_by: patrick
briefing_order:
  - active-briefing
briefings:
  - id: active-briefing
    title: Active Briefing
    objective: Active objective
    intent: Active intent
    status: active
    milestone_id: missing-milestone
    completion_signal: Done
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
  - id: archived-briefing
    title: Archived Briefing
    objective: Archived objective
    intent: Archived intent
    status: archived
    depends_on:
      - missing-archived-dependency
    completion_signal: Done
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
`)

	state, result := LoadPlanningState(
		plansDir,
		WithKnownMilestoneIDs([]string{"existing-milestone"}),
		WithMilestoneSourceReferences([]MilestoneSourceReference{
			{MilestoneID: "generated-milestone", Type: "briefing", PlanID: "references", BriefingID: "deleted-briefing"},
			{MilestoneID: "orphan-milestone", Type: "briefing", PlanID: "deleted-plan", BriefingID: "anything"},
		}),
	)
	if result.HasErrors() {
		t.Fatalf("expected warnings only, got %+v", result.Messages)
	}
	if !result.HasWarnings() || len(result.UnresolvedReferences) != 3 {
		t.Fatalf("expected dangling references and archived dependency warnings, result=%+v", result)
	}
	if len(state.Plans) != 1 || len(state.Plans[0].Briefings) != 2 {
		t.Fatalf("expected archived briefing to remain addressable, state=%+v", state)
	}
	messages := planningMessagesText(result)
	for _, want := range []string{
		"references missing Milestone",
		"depends on missing Briefing",
		"references missing source Briefing",
		"references missing source Plan",
	} {
		if !strings.Contains(messages, want) {
			t.Fatalf("expected warning %q, got:\n%s", want, messages)
		}
	}
}

func TestPlanningErrorForMalformedBriefingMilestoneID(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		t.Fatalf("failed to create plans dir: %v", err)
	}
	writeFile(t, filepath.Join(plansDir, "malformed-reference.yml"), `schema_version: 1
id: malformed-reference
title: Malformed Reference
objective: Exercise malformed optional milestone IDs
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: patrick
updated_at: "2026-07-20T11:00:00Z"
updated_by: patrick
briefing_order:
  - active-briefing
briefings:
  - id: active-briefing
    title: Active Briefing
    objective: Active objective
    intent: Active intent
    status: active
    milestone_id: Bad_ID
    completion_signal: Done
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
`)

	state, result := LoadPlanningState(plansDir, WithKnownMilestoneIDs([]string{"existing-milestone"}))
	if !result.HasErrors() {
		t.Fatalf("expected malformed milestone_id to be a validation error, got %+v", result.Messages)
	}
	if len(state.Plans) != 0 {
		t.Fatalf("expected invalid Plan file to be excluded, got %+v", state.Plans)
	}
	if len(result.UnresolvedReferences) != 0 {
		t.Fatalf("malformed milestone_id should not be reported as an unresolved reference: %+v", result.UnresolvedReferences)
	}
	messages := planningMessagesText(result)
	for _, want := range []string{
		"briefings.active-briefing.milestone_id",
		"Milestone ID must use lowercase ASCII letters, numbers, and hyphens",
	} {
		if !strings.Contains(messages, want) {
			t.Fatalf("expected validation messages to contain %q, got:\n%s", want, messages)
		}
	}
}

func TestPlanningSaveDoesNotRewriteMilestoneStorage(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	specPath := filepath.Join(tmpDir, "milestones", "standalone.md")
	reportPath := filepath.Join(tmpDir, "reports", "standalone", "summary.md")
	writeFile(t, configPath, `milestones:
  - id: standalone
    title: Standalone
    spec_path: milestones/standalone.md
    source:
      type: briefing
      plan_id: deleted-plan
      briefing_id: deleted-briefing
`)
	writeFile(t, specPath, "# Milestone Spec: standalone - Standalone\n\n## Goal\nStay independent.\n")
	writeFile(t, statePath, `{"active_milestone_id":"standalone","milestone_statuses":{},"milestone_cycles":{},"history":{}}`)
	writeFile(t, reportPath, "existing report\n")

	before := readFiles(t, configPath, statePath, specPath, reportPath)
	if _, err := SavePlan(filepath.Join(tmpDir, "plans"), representativePlan(), WithKnownMilestoneIDs([]string{"0003-persist-planning-layer"})); err != nil {
		t.Fatalf("SavePlan failed: %v", err)
	}
	after := readFiles(t, configPath, statePath, specPath, reportPath)
	for path, beforeContent := range before {
		if after[path] != beforeContent {
			t.Fatalf("expected %s to remain unchanged", path)
		}
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "plans", "onboarding-improvements.yml")); err != nil {
		t.Fatalf("expected plan file to be written: %v", err)
	}
}

func TestMilestoneStorageIndependentFromPlanning(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	writeFile(t, configPath, "milestones:\n  - id: standalone\n    title: Standalone\n    spec_path: milestones/standalone.md\n")
	writeFile(t, filepath.Join(tmpDir, "milestones", "standalone.md"), "# Milestone Spec: standalone - Standalone\n\n## Goal\nHydrated.\n\n## Acceptance Criteria\n- [ ] Works\n")
	writeFile(t, filepath.Join(tmpDir, "plans", "broken.yml"), "schema_version: [broken\n")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig should ignore planning files: %v", err)
	}
	if len(cfg.Milestones) != 1 || cfg.Milestones[0].Goal != "Hydrated." {
		t.Fatalf("expected standalone milestone to load and hydrate, got %+v", cfg.Milestones)
	}

	if err := AddMilestone(configPath, Milestone{ID: "second", Title: "Second"}); err != nil {
		t.Fatalf("AddMilestone should not require planning: %v", err)
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState should not require planning: %v", err)
	}
	if err := SaveState(statePath, state); err != nil {
		t.Fatalf("SaveState should not require planning: %v", err)
	}
	if err := DeleteMilestone(configPath, statePath, "second"); err != nil {
		t.Fatalf("DeleteMilestone should not require planning: %v", err)
	}
}

func representativePlan() Plan {
	return Plan{
		SchemaVersion: PlanningSchemaVersion,
		ID:            "onboarding-improvements",
		Title:         "Improve onboarding",
		Objective:     "Make first-run setup easier to understand and recover from.",
		Status:        "active",
		CreatedAt:     "2026-07-20T10:15:00Z",
		CreatedBy:     "patrick",
		UpdatedAt:     "2026-07-20T12:30:00Z",
		UpdatedBy:     "developer-agent",
		Constraints:   []string{"Keep setup usable in non-TTY environments."},
		BriefingOrder: []string{"setup-copy-review", "persist-plan-files"},
		Briefings: []Briefing{
			{
				ID:               "setup-copy-review",
				Title:            "Review setup copy",
				Objective:        "Clarify first-run setup labels and confirmation text.",
				Intent:           "Users should understand what files setup will create before confirmation.",
				Status:           "active",
				CompletionSignal: "Setup copy is reviewed and accepted in the TUI.",
				Constraints:      []string{"Do not change runner detection behavior."},
				CreatedAt:        "2026-07-20T10:15:00Z",
				CreatedBy:        "patrick",
				UpdatedAt:        "2026-07-20T10:15:00Z",
				UpdatedBy:        "patrick",
			},
			{
				ID:               "persist-plan-files",
				Title:            "Persist plan files",
				Objective:        "Save and reload planning data.",
				Intent:           "Planning state should survive local CLI runs.",
				Status:           "completed",
				DependsOn:        []string{"setup-copy-review"},
				MilestoneID:      "0003-persist-planning-layer",
				CompletionSignal: "Plan files round trip in tests.",
				CreatedAt:        "2026-07-20T10:18:00Z",
				CreatedBy:        "patrick",
				UpdatedAt:        "2026-07-20T12:30:00Z",
				UpdatedBy:        "developer-agent",
			},
			{
				ID:               "archived-note",
				Title:            "Archived note",
				Objective:        "Keep historical context addressable.",
				Intent:           "Archived briefings remain parseable outside active ordering.",
				Status:           "archived",
				CompletionSignal: "Archived record reloads.",
				CreatedAt:        "2026-07-20T10:20:00Z",
				CreatedBy:        "patrick",
				UpdatedAt:        "2026-07-20T10:20:00Z",
				UpdatedBy:        "patrick",
			},
		},
	}
}

func validPlanYAML(id string) string {
	return `schema_version: 1
id: ` + id + `
title: Valid Plan
objective: Valid objective
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: patrick
updated_at: "2026-07-20T11:00:00Z"
updated_by: patrick
briefing_order:
  - valid-briefing
briefings:
  - id: valid-briefing
    title: Valid Briefing
    objective: Valid objective
    intent: Valid intent
    status: active
    completion_signal: Done
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
`
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func readFiles(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	files := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", path, err)
		}
		files[path] = string(data)
	}
	return files
}

func planningMessagesText(result PlanningValidationResult) string {
	var parts []string
	for _, msg := range result.Messages {
		parts = append(parts, msg.Severity+" "+msg.File+" "+msg.Field+" "+msg.Message)
	}
	return strings.Join(parts, "\n")
}

func TestPlanReevaluationProposalValidationAndDiff(t *testing.T) {
	t.Parallel()

	oldPlan := representativePlan()

	// Proposal: keeps completed persist-plan-files, edits setup-copy-review objective, adds new-briefing
	proposal := PlanReevaluationProposal{
		PlanID:        oldPlan.ID,
		Rationale:     "Adjusting plan based on cycle 1 completion.",
		BriefingOrder: []string{"persist-plan-files", "new-briefing", "setup-copy-review"},
		Briefings: []Briefing{
			oldPlan.Briefings[1], // completed briefing persist-plan-files
			{
				ID:               "new-briefing",
				Title:            "New Briefing",
				Objective:        "Newly discovered task",
				Intent:           "Add missing validation step",
				Status:           "active",
				CompletionSignal: "Validation passes",
				CreatedAt:        "2026-07-20T10:00:00Z",
				CreatedBy:        "ai-planner",
				UpdatedAt:        "2026-07-20T11:00:00Z",
				UpdatedBy:        "ai-planner",
			},
			{
				ID:               "setup-copy-review",
				Title:            "Review setup copy",
				Objective:        "Updated objective for review",
				Intent:           "Users should understand what files setup will create before confirmation.",
				Status:           "active",
				CompletionSignal: "Setup copy is reviewed and accepted in the TUI.",
				Constraints:      []string{"Do not change runner detection behavior."},
				CreatedAt:        "2026-07-20T10:15:00Z",
				CreatedBy:        "patrick",
				UpdatedAt:        "2026-07-20T11:00:00Z",
				UpdatedBy:        "ai-planner",
			},
			oldPlan.Briefings[2], // archived-note
		},
	}

	validation := ValidatePlanReevaluationProposal(oldPlan, proposal, []string{"0003-persist-planning-layer"})
	if validation.HasErrors() {
		t.Fatalf("expected valid proposal, got errors: %+v", validation.Messages)
	}

	diff := ComputePlanDiff(oldPlan, proposal)
	if !diff.HasChanges {
		t.Fatal("expected diff to have changes")
	}

	addedCount, modifiedCount := 0, 0
	for _, bd := range diff.BriefingDiffs {
		switch bd.Kind {
		case DiffKindAdded:
			addedCount++
		case DiffKindModified:
			modifiedCount++
		}
	}

	if addedCount != 1 || modifiedCount != 1 {
		t.Fatalf("expected 1 added, 1 modified; got added=%d, modified=%d", addedCount, modifiedCount)
	}

	// Test invariant violation: trying to revert completed briefing status
	invalidProposal := proposal
	invalidProposal.Briefings[0].Status = "active"
	invValidation := ValidatePlanReevaluationProposal(oldPlan, invalidProposal, []string{"0003-persist-planning-layer"})
	if !invValidation.HasErrors() {
		t.Fatal("expected error when reverting completed briefing status")
	}

	// Test applying proposal
	applied := ApplyPlanReevaluationProposal(oldPlan, proposal, "ai-planner", "2026-07-20T12:00:00Z")
	if len(applied.Briefings) != 4 || applied.UpdatedBy != "ai-planner" {
		t.Fatalf("failed to apply proposal cleanly: %+v", applied)
	}
}

func TestLifecycleSafetyPlanDeletionPreservesMilestones(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	plansDir := filepath.Join(tmpDir, "plans")
	specPath := filepath.Join(tmpDir, "milestones", "linked-ms.md")
	reportPath := filepath.Join(tmpDir, "reports", "linked-ms", "summary.md")

	writeFile(t, configPath, "milestones:\n  - id: linked-ms\n    title: Linked MS\n    spec_path: milestones/linked-ms.md\n")
	writeFile(t, specPath, "# Milestone Spec: linked-ms - Linked MS\n\n## Goal\nLinked milestone goal.\n")
	writeFile(t, statePath, `{"active_milestone_id":"linked-ms","milestone_statuses":{"linked-ms":"Approved"},"milestone_cycles":{"linked-ms":1},"history":{}}`)
	writeFile(t, reportPath, "cycle 1 summary report\n")

	plan := representativePlan()
	plan.Briefings[1].MilestoneID = "linked-ms"
	if validation, err := SavePlan(plansDir, plan, WithKnownMilestoneIDs([]string{"linked-ms"})); err != nil || validation.HasErrors() {
		t.Fatalf("SavePlan failed: %v %+v", err, validation)
	}

	beforeStorage := readFiles(t, configPath, statePath, specPath, reportPath)

	// Simulate plan file deletion
	planPath := filepath.Join(plansDir, plan.ID+".yml")
	if err := os.Remove(planPath); err != nil {
		t.Fatalf("failed to remove plan file: %v", err)
	}

	// Verify milestone storage remains 100% intact
	afterStorage := readFiles(t, configPath, statePath, specPath, reportPath)
	for path, beforeContent := range beforeStorage {
		if afterStorage[path] != beforeContent {
			t.Fatalf("expected %s to remain unchanged after Plan deletion", path)
		}
	}

	// Verify config and state load cleanly without planning files
	cfg, err := LoadConfig(configPath)
	if err != nil || len(cfg.Milestones) != 1 || cfg.Milestones[0].ID != "linked-ms" {
		t.Fatalf("LoadConfig failed after Plan deletion: %v %+v", err, cfg)
	}
	st, err := LoadState(statePath)
	if err != nil || st.GetMilestoneStatus("linked-ms") != "Approved" {
		t.Fatalf("LoadState failed after Plan deletion: %v %+v", err, st)
	}
}

func TestLifecycleSafetyBriefingUnlinkPreservesMilestones(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	plansDir := filepath.Join(tmpDir, "plans")
	specPath := filepath.Join(tmpDir, "milestones", "linked-ms.md")
	reportPath := filepath.Join(tmpDir, "reports", "linked-ms", "summary.md")

	writeFile(t, configPath, "milestones:\n  - id: linked-ms\n    title: Linked MS\n    spec_path: milestones/linked-ms.md\n")
	writeFile(t, specPath, "# Milestone Spec: linked-ms - Linked MS\n\n## Goal\nGoal.\n")
	writeFile(t, statePath, `{"active_milestone_id":"linked-ms","milestone_statuses":{},"milestone_cycles":{},"history":{}}`)
	writeFile(t, reportPath, "report\n")

	plan := representativePlan()
	plan.Briefings[1].MilestoneID = "linked-ms"
	if validation, err := SavePlan(plansDir, plan, WithKnownMilestoneIDs([]string{"linked-ms"})); err != nil || validation.HasErrors() {
		t.Fatalf("SavePlan failed: %v %+v", err, validation)
	}

	beforeStorage := readFiles(t, configPath, statePath, specPath, reportPath)

	// Unlink Briefing
	plan.Briefings[1].MilestoneID = ""
	if validation, err := SavePlan(plansDir, plan, WithKnownMilestoneIDs([]string{"linked-ms"})); err != nil || validation.HasErrors() {
		t.Fatalf("SavePlan unlinking failed: %v %+v", err, validation)
	}

	// Verify Briefing milestone_id is cleared
	reloaded, val := LoadPlanningState(plansDir, WithKnownMilestoneIDs([]string{"linked-ms"}))
	if val.HasErrors() || len(reloaded.Plans) != 1 || reloaded.Plans[0].Briefings[1].MilestoneID != "" {
		t.Fatalf("expected unlinked briefing in reloaded plan: %+v", reloaded)
	}

	// Verify Milestone storage remains untouched
	afterStorage := readFiles(t, configPath, statePath, specPath, reportPath)
	for path, beforeContent := range beforeStorage {
		if afterStorage[path] != beforeContent {
			t.Fatalf("expected %s to remain unchanged after Briefing unlink", path)
		}
	}
}

func TestLifecycleSafetyMilestoneDeletionLeavesBriefingInMissingState(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	plansDir := filepath.Join(tmpDir, "plans")

	plan := representativePlan()
	plan.Briefings[1].MilestoneID = "deleted-ms"
	if validation, err := SavePlan(plansDir, plan, WithKnownMilestoneIDs([]string{"deleted-ms"})); err != nil || validation.HasErrors() {
		t.Fatalf("SavePlan failed: %v %+v", err, validation)
	}

	// Load planning state with NO known milestone IDs (simulating milestone deletion)
	state, result := LoadPlanningState(plansDir, WithKnownMilestoneIDs([]string{}))

	if result.HasErrors() {
		t.Fatalf("expected non-fatal warnings for missing milestone reference, got errors: %+v", result.Messages)
	}
	if !result.HasWarnings() {
		t.Fatal("expected warning for missing milestone reference")
	}
	if len(result.UnresolvedReferences) != 1 || result.UnresolvedReferences[0].Kind != "milestone" || result.UnresolvedReferences[0].MilestoneID != "deleted-ms" {
		t.Fatalf("unexpected unresolved references: %+v", result.UnresolvedReferences)
	}
	if len(state.Plans) != 1 || state.Plans[0].Briefings[1].MilestoneID != "deleted-ms" {
		t.Fatalf("plan should remain loaded with briefing pointing to missing milestone: %+v", state)
	}
}

func TestLifecycleSafetyStaleProvenanceResolution(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	plansDir := filepath.Join(tmpDir, "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(plansDir, "existing-plan.yml"), validPlanYAML("existing-plan"))

	// Advisory provenance referencing non-existent plan and non-existent briefing
	state, result := LoadPlanningState(plansDir, WithMilestoneSourceReferences([]MilestoneSourceReference{
		{MilestoneID: "ms-1", Type: "briefing", PlanID: "ghost-plan", BriefingID: "ghost-briefing"},
		{MilestoneID: "ms-2", Type: "briefing", PlanID: "existing-plan", BriefingID: "ghost-briefing"},
	}))

	if result.HasErrors() {
		t.Fatalf("stale provenance should yield non-fatal warnings, got errors: %+v", result.Messages)
	}
	if !result.HasWarnings() || len(result.UnresolvedReferences) != 2 {
		t.Fatalf("expected 2 unresolved provenance references, got result=%+v", result)
	}
	if len(state.Plans) != 1 {
		t.Fatalf("plan loading should succeed despite stale provenance: %+v", state.Plans)
	}
}

func TestLifecycleSafetyGeneratedMilestoneStandaloneExecutionAfterSourceRemoval(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	plansDir := filepath.Join(tmpDir, "plans")

	writeFile(t, configPath, "milestones: []\n")
	writeFile(t, statePath, `{"active_milestone_id":"","milestone_statuses":{},"milestone_cycles":{},"history":{}}`)

	// Create a Plan and Briefing
	plan := representativePlan()
	plan.Briefings[0].Status = "completed"
	if validation, err := SavePlan(plansDir, plan); err != nil || validation.HasErrors() {
		t.Fatalf("SavePlan failed: %v %+v", err, validation)
	}

	// Add milestone generated from briefing
	genMS := Milestone{
		ID:       "onboarding-improvements-setup-copy-review",
		Title:    "Review setup copy",
		SpecPath: "milestones/onboarding-improvements-setup-copy-review.md",
	}
	spec := "# Milestone Spec: onboarding-improvements-setup-copy-review - Review setup copy\n\n## Goal\nGenerated goal.\n"
	if err := AddMilestoneWithSpec(configPath, genMS, spec); err != nil {
		t.Fatalf("AddMilestoneWithSpec failed: %v", err)
	}

	// Remove source Plan file
	if err := os.Remove(filepath.Join(plansDir, plan.ID+".yml")); err != nil {
		t.Fatalf("failed to remove plan file: %v", err)
	}

	// Verify generated milestone functions as first-class entity
	cfg, err := LoadConfig(configPath)
	if err != nil || len(cfg.Milestones) != 1 || cfg.Milestones[0].ID != genMS.ID {
		t.Fatalf("generated milestone should be readable from index: %v %+v", err, cfg)
	}

	st, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	st.SetMilestoneStatus(genMS.ID, "Approved")
	st.SetMilestoneCycles(genMS.ID, 1)
	if err := SaveState(statePath, st); err != nil {
		t.Fatalf("SaveState failed for generated milestone: %v", err)
	}
}

func TestAuthorPrefixAndIDAllocation(t *testing.T) {
	t.Parallel()

	// Test ExtractAuthorPrefix
	if prefix := ExtractAuthorPrefix("p-pf-0001"); prefix != "pf" {
		t.Fatalf("expected 'pf', got %q", prefix)
	}
	if prefix := ExtractAuthorPrefix("b-js-0003"); prefix != "js" {
		t.Fatalf("expected 'js', got %q", prefix)
	}
	if prefix := ExtractAuthorPrefix("ms-al-0012-user-model"); prefix != "al" {
		t.Fatalf("expected 'al', got %q", prefix)
	}

	// Test AllocatePlanID
	existingPlans := []string{"p-pf-0001", "p-pf-0002", "p-js-0001"}
	planID := AllocatePlanID("pf", existingPlans)
	if planID != "p-pf-0003" {
		t.Fatalf("expected 'p-pf-0003', got %q", planID)
	}

	// Test AllocateBriefingID inheriting parent Plan's author prefix ("js")
	existingBriefings := []string{"b-js-0001"}
	briefingID := AllocateBriefingID("p-js-0001", "pf", existingBriefings)
	if briefingID != "b-js-0002" {
		t.Fatalf("expected 'b-js-0002' inheriting parent namespace, got %q", briefingID)
	}

	// Test AllocateMilestoneID inheriting parent Plan's author prefix ("js")
	existingMilestones := []string{"ms-js-0001"}
	msID := AllocateMilestoneID("p-js-0001", "pf", existingMilestones)
	if msID != "ms-js-0002" {
		t.Fatalf("expected 'ms-js-0002' inheriting parent namespace, got %q", msID)
	}

	// Test standalone AllocateMilestoneID using default author prefix ("pf")
	standaloneMSID := AllocateMilestoneID("", "pf", []string{"ms-pf-0001"})
	if standaloneMSID != "ms-pf-0002" {
		t.Fatalf("expected 'ms-pf-0002', got %q", standaloneMSID)
	}
}

func TestParseGeneratedPlanResponseRobustness(t *testing.T) {
	t.Parallel()

	// Test output containing both prompt template (with placeholders) and real response (JSON)
	mergedOutput := `
Some headers or prompt echoes:
{
  "title": "<optimized_plan_title>",
  "objective": "<detailed_plan_objective>",
  "constraints": ["<optional plan constraint>"],
  "briefings": [
    {
      "title": "<briefing title 1>",
      "objective": "<briefing objective 1>",
      "intent": "<briefing intent 1>",
      "completion_signal": "<how to verify completion>"
    }
  ]
}

actual codex response:
{
  "title": "Real Plan",
  "objective": "Real Objective",
  "briefings": [
    {
      "title": "Real Briefing",
      "objective": "Real Briefing Objective",
      "intent": "Real Intent",
      "completion_signal": "Verify real"
    }
  ]
}
`
	parsed, err := ParseGeneratedPlanResponse(mergedOutput)
	if err != nil {
		t.Fatalf("unexpected error parsing robustly: %v", err)
	}
	if parsed.Title != "Real Plan" {
		t.Fatalf("expected robust parser to select 'Real Plan', got %q", parsed.Title)
	}
	if parsed.Objective != "Real Objective" {
		t.Fatalf("expected robust parser to select 'Real Objective', got %q", parsed.Objective)
	}
}

func TestSavePlanToFolderPrefixAndMigration(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	plan := Plan{
		SchemaVersion: PlanningSchemaVersion,
		ID:            "p-pf-0003-four-briefing-read-only-repository-inspection-test-plan",
		Title:         "Four briefing plan",
		Objective:     "Plan objective",
		Status:        "active",
		CreatedAt:     "2026-07-20T10:15:00Z",
		CreatedBy:     "patrick",
		UpdatedAt:     "2026-07-20T12:30:00Z",
		UpdatedBy:     "developer-agent",
		BriefingOrder: []string{"b-pf-0001-survey-repository-layout-and-planning-layer"},
		Briefings: []Briefing{
			{
				ID:               "b-pf-0001-survey-repository-layout-and-planning-layer",
				Title:            "Survey repository layout and planning layer",
				Objective:        "Briefing objective",
				Intent:           "Briefing intent",
				Status:           "active",
				CompletionSignal: "Briefing completion",
				CreatedAt:        "2026-07-20T10:15:00Z",
				CreatedBy:        "patrick",
				UpdatedAt:        "2026-07-20T10:15:00Z",
				UpdatedBy:        "patrick",
			},
		},
	}

	// 1. Save using SavePlanToFolder
	planDir, result, err := SavePlanToFolder(plansDir, plan)
	if err != nil {
		t.Fatalf("SavePlanToFolder failed: %v, result: %+v", err, result)
	}

	// Verify plan files are named after prefix
	expectedPlanMeta := filepath.Join(planDir, "p-pf-0003-metadata.yml")
	expectedPlanSpec := filepath.Join(planDir, "p-pf-0003-spec.md")
	if _, err := os.Stat(expectedPlanMeta); err != nil {
		t.Errorf("expected plan metadata at %s, got err: %v", expectedPlanMeta, err)
	}
	if _, err := os.Stat(expectedPlanSpec); err != nil {
		t.Errorf("expected plan spec at %s, got err: %v", expectedPlanSpec, err)
	}

	// Verify briefing files are named after prefix
	briefingDir := filepath.Join(planDir, "briefings", "b-pf-0001-survey-repository-layout-and-planning-layer")
	expectedBriefingMeta := filepath.Join(briefingDir, "b-pf-0001-metadata.yml")
	expectedBriefingSpec := filepath.Join(briefingDir, "b-pf-0001-spec.md")
	if _, err := os.Stat(expectedBriefingMeta); err != nil {
		t.Errorf("expected briefing metadata at %s, got err: %v", expectedBriefingMeta, err)
	}
	if _, err := os.Stat(expectedBriefingSpec); err != nil {
		t.Errorf("expected briefing spec at %s, got err: %v", expectedBriefingSpec, err)
	}

	// Verify old names do not exist
	oldPlanMeta := filepath.Join(planDir, "p-pf-0003-four-briefing-read-only-repository-inspection-test-plan.yml")
	oldPlanSpec := filepath.Join(planDir, "p-pf-0003-four-briefing-read-only-repository-inspection-test-plan.md")
	if _, err := os.Stat(oldPlanMeta); !os.IsNotExist(err) {
		t.Errorf("old plan metadata still exists: %s", oldPlanMeta)
	}
	if _, err := os.Stat(oldPlanSpec); !os.IsNotExist(err) {
		t.Errorf("old plan spec still exists: %s", oldPlanSpec)
	}

	// 2. Test fallback and auto-migration
	// Rename files back to legacy format manually
	err = os.Rename(expectedPlanMeta, oldPlanMeta)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Rename(expectedPlanSpec, oldPlanSpec)
	if err != nil {
		t.Fatal(err)
	}

	oldBriefingMeta := filepath.Join(briefingDir, "b-pf-0001-survey-repository-layout-and-planning-layer.yml")
	oldBriefingSpec := filepath.Join(briefingDir, "b-pf-0001-survey-repository-layout-and-planning-layer.md")
	err = os.Rename(expectedBriefingMeta, oldBriefingMeta)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Rename(expectedBriefingSpec, oldBriefingSpec)
	if err != nil {
		t.Fatal(err)
	}

	// Load should find them via fallback and auto-migrate them
	state, loadRes := LoadPlanningState(plansDir)
	if loadRes.HasErrors() {
		t.Fatalf("LoadPlanningState failed during fallback check: %+v", loadRes.Messages)
	}
	if len(state.Plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(state.Plans))
	}

	// Check if they were auto-migrated
	if _, err := os.Stat(expectedPlanMeta); err != nil {
		t.Errorf("expected plan metadata to be auto-migrated back to %s, got err: %v", expectedPlanMeta, err)
	}
	if _, err := os.Stat(expectedPlanSpec); err != nil {
		t.Errorf("expected plan spec to be auto-migrated back to %s, got err: %v", expectedPlanSpec, err)
	}
	if _, err := os.Stat(expectedBriefingMeta); err != nil {
		t.Errorf("expected briefing metadata to be auto-migrated back to %s, got err: %v", expectedBriefingMeta, err)
	}
	if _, err := os.Stat(expectedBriefingSpec); err != nil {
		t.Errorf("expected briefing spec to be auto-migrated back to %s, got err: %v", expectedBriefingSpec, err)
	}

	// And old files deleted
	if _, err := os.Stat(oldPlanMeta); !os.IsNotExist(err) {
		t.Errorf("old plan metadata was not deleted after migration")
	}
}
