package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestGenerateDefaultConfigStartsWithoutMilestones(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")

	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatalf("GenerateDefaultConfig failed: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Milestones) != 0 {
		t.Fatalf("expected no default milestones, got %d", len(cfg.Milestones))
	}

	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones")); !os.IsNotExist(err) {
		t.Fatalf("expected no milestones directory, stat error: %v", err)
	}
}

func TestIsConfigMissing(t *testing.T) {
	t.Run("config already exists", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, []byte("milestones: []\n"), 0644); err != nil {
			t.Fatal(err)
		}

		missing, err := isConfigMissing(configPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if missing {
			t.Fatal("expected missing to be false")
		}
	})

	t.Run("missing config", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, ".cyclestone", "milestone.yml")

		missing, err := isConfigMissing(configPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !missing {
			t.Fatal("expected missing to be true")
		}
	})
}

func TestMissingConfigNonInteractiveErrorMentionsSetupRequirement(t *testing.T) {
	msg := missingConfigNonInteractiveError()
	for _, want := range []string{"milestones configuration not found", "interactive terminal", "existing config"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected non-interactive error to mention %q, got %q", want, msg)
		}
	}
}

func TestVersionFallback(t *testing.T) {
	if Version != "development" {
		t.Errorf("expected default Version to be 'development', got '%s'", Version)
	}
}

func TestPlanListReadOnlyOutput(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	before := snapshotFiles(t,
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml"),
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)

	var stdout, stderr bytes.Buffer
	code := runReadOnlyCommand([]string{"plan", "list"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan list returned %d, stderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Plans:",
		"- id: delivery-plan",
		"title: Delivery Plan",
		"status: active",
		"briefings: 4",
		"progress: 1/3 completed (33%)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected plan list output to contain %q, got:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr.String(), "references missing Milestone \"missing-milestone\"") {
		t.Fatalf("expected dangling milestone warning, got stderr:\n%s", stderr.String())
	}
	assertFilesUnchanged(t, before)
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "milestones", "missing-milestone.md"))
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "reports", "missing-milestone"))
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "plans", "missing-milestone.yml"))
}

func TestPlanListEmptyPlanningDirectorySucceeds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	writeMainTestFile(t, configPath, "milestones: []\n")

	var stdout, stderr bytes.Buffer
	code := runReadOnlyCommand([]string{"plan", "list"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan list returned %d, stderr:\n%s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "Plans: none" {
		t.Fatalf("unexpected empty plan list output:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr for missing plans dir, got:\n%s", stderr.String())
	}
}

func TestPlanShowIncludesOrderedBriefingsAndMilestoneRelationships(t *testing.T) {
	t.Parallel()

	_, configPath, _ := writePlanningCommandFixture(t)

	var stdout, stderr bytes.Buffer
	code := runReadOnlyCommand([]string{"plan", "show", "delivery-plan"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan show returned %d, stderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Plan: delivery-plan",
		"Progress: 1/3 completed (33%)",
		"- no-milestone | active | readiness: ready | milestone: none | No Milestone",
		"- linked-existing | completed | readiness: completed | milestone: linked existing-milestone | Linked Existing",
		"- blocked-missing | active | readiness: blocked | milestone: missing missing-milestone | Blocked Missing",
		"- archived-note | archived | readiness: archived | milestone: none | Archived Note",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected plan show output to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "standalone-milestone") {
		t.Fatalf("standalone milestone should not be listed as a Plan child, got:\n%s", out)
	}
}

func TestBriefingShowIncludesDetailsAndBlockedState(t *testing.T) {
	t.Parallel()

	_, configPath, _ := writePlanningCommandFixture(t)

	var stdout, stderr bytes.Buffer
	code := runReadOnlyCommand([]string{"briefing", "show", "delivery-plan", "blocked-missing"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("briefing show returned %d, stderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Briefing: blocked-missing",
		"Plan: delivery-plan",
		"Status: active",
		"Readiness: blocked",
		"Milestone: missing missing-milestone",
		"Objective: Exercise missing milestone display.",
		"Intent: Keep optional references non-fatal.",
		"Completion Signal: Missing reference is shown safely.",
		"Dependencies:\n- no-milestone",
		"Constraints:\n- Do not create milestone files.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected briefing show output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestReadOnlyCommandFlagValidationAndVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", "", "plan", "list"}, os.Stdin, &stdout, &stderr); code != 1 {
		t.Fatalf("expected empty config path to fail with code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "-config parameter cannot be empty") {
		t.Fatalf("expected empty config error, got:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-version"}, os.Stdin, &stdout, &stderr); code != 0 {
		t.Fatalf("expected version command to succeed, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Cyclestone ") {
		t.Fatalf("expected version output, got:\n%s", stdout.String())
	}
}

func TestPlanAndBriefingManualCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	writeMainTestFile(t, configPath, "milestones: []\n")

	assertCommandSucceeds(t, configPath,
		[]string{"plan", "create", "launch-plan", "--title", "Launch Plan", "--objective", "Coordinate launch.", "--actor", "pm"},
		"Plan \"launch-plan\" created",
	)
	plan := loadMainTestPlan(t, root, "launch-plan")
	if plan.ID != "launch-plan" || plan.Title != "Launch Plan" || plan.Status != "active" || plan.CreatedBy != "pm" || plan.UpdatedBy != "pm" {
		t.Fatalf("unexpected created Plan: %+v", plan)
	}

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "add", "launch-plan", "write-copy", "--title", "Write copy", "--objective", "Draft launch copy.", "--intent", "Clear announcement.", "--completion-signal", "Copy approved.", "--actor", "pm"},
		"Briefing \"write-copy\" added",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "add", "launch-plan", "ship-ui", "--title", "Ship UI", "--objective", "Finish UI.", "--intent", "Feature usable.", "--completion-signal", "UI merged.", "--actor", "pm"},
		"Briefing \"ship-ui\" added",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "dependency", "add", "launch-plan", "ship-ui", "write-copy"},
		"Dependency \"write-copy\" added",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "reorder", "launch-plan", "ship-ui", "write-copy"},
		"Briefing order updated",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"plan", "edit", "launch-plan", "--title", "Launch Plan Updated", "--actor", "pm"},
		"Plan \"launch-plan\" updated",
	)

	plan = loadMainTestPlan(t, root, "launch-plan")
	if plan.Title != "Launch Plan Updated" {
		t.Fatalf("expected plan title edit to persist, got %+v", plan)
	}
	if strings.Join(plan.BriefingOrder, "|") != "ship-ui|write-copy" {
		t.Fatalf("unexpected briefing order: %+v", plan.BriefingOrder)
	}
	shipUI, ok := findBriefing(plan, "ship-ui")
	if !ok || strings.Join(shipUI.DependsOn, "|") != "write-copy" {
		t.Fatalf("expected dependency to persist, briefing=%+v ok=%v", shipUI, ok)
	}

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "dependency", "remove", "launch-plan", "ship-ui", "write-copy"},
		"Dependency \"write-copy\" removed",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "archive", "launch-plan", "write-copy"},
		"Briefing \"write-copy\" archived",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "restore", "launch-plan", "write-copy"},
		"Briefing \"write-copy\" restored",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"plan", "archive", "launch-plan"},
		"Plan \"launch-plan\" archived",
	)
	assertCommandSucceeds(t, configPath,
		[]string{"plan", "restore", "launch-plan"},
		"Plan \"launch-plan\" restored",
	)

	plan = loadMainTestPlan(t, root, "launch-plan")
	writeCopy, ok := findBriefing(plan, "write-copy")
	if !ok || writeCopy.Status != "active" || !containsString(plan.BriefingOrder, "write-copy") {
		t.Fatalf("expected restored briefing to be active and ordered, plan=%+v", plan)
	}
	if plan.Status != "active" {
		t.Fatalf("expected restored plan to be active, got %s", plan.Status)
	}
}

func TestBriefingLinkUnlinkAndDeletesPreserveMilestoneStorage(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	milestonePaths := []string{
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	}
	beforeMilestones := snapshotFiles(t, milestonePaths...)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "link", "delivery-plan", "no-milestone", "standalone-milestone"},
		"linked to Milestone \"standalone-milestone\"",
	)
	plan := loadMainTestPlan(t, root, "delivery-plan")
	noMilestone, _ := findBriefing(plan, "no-milestone")
	if noMilestone.MilestoneID != "standalone-milestone" {
		t.Fatalf("expected milestone link to persist, got %+v", noMilestone)
	}
	assertFilesUnchanged(t, beforeMilestones)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "unlink", "delivery-plan", "no-milestone"},
		"Briefing \"no-milestone\" unlinked",
	)
	plan = loadMainTestPlan(t, root, "delivery-plan")
	noMilestone, _ = findBriefing(plan, "no-milestone")
	if noMilestone.MilestoneID != "" {
		t.Fatalf("expected milestone link to be cleared, got %+v", noMilestone)
	}
	assertFilesUnchanged(t, beforeMilestones)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "delete", "delivery-plan", "linked-existing", "--confirm", "linked-existing"},
		"Briefing \"linked-existing\" deleted",
	)
	plan = loadMainTestPlan(t, root, "delivery-plan")
	if _, ok := findBriefing(plan, "linked-existing"); ok || containsString(plan.BriefingOrder, "linked-existing") {
		t.Fatalf("expected deleted briefing to be removed from record and order, plan=%+v", plan)
	}
	assertFilesUnchanged(t, beforeMilestones)

	assertCommandSucceeds(t, configPath,
		[]string{"plan", "delete", "delivery-plan", "--confirm", "delivery-plan"},
		"Plan \"delivery-plan\" deleted",
	)
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml"))
	assertFilesUnchanged(t, beforeMilestones)
	if _, err := config.LoadConfig(configPath); err != nil {
		t.Fatalf("LoadConfig should still succeed after Plan delete: %v", err)
	}
	if _, err := config.LoadState(statePath); err != nil {
		t.Fatalf("LoadState should still succeed after Plan delete: %v", err)
	}
}

func TestMutatingPlanningCommandFailuresLeaveFilesUnchanged(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
	before := snapshotFiles(t,
		configPath,
		statePath,
		planPath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)

	assertCommandFails(t, configPath,
		[]string{"plan", "delete", "delivery-plan"},
		"requires --confirm delivery-plan",
	)
	assertCommandFails(t, configPath,
		[]string{"briefing", "delete", "delivery-plan", "no-milestone"},
		"requires --confirm no-milestone",
	)
	assertCommandFails(t, configPath,
		[]string{"briefing", "link", "delivery-plan", "no-milestone", "missing-milestone"},
		"Milestone \"missing-milestone\" not found",
	)
	assertCommandFails(t, configPath,
		[]string{"briefing", "link", "delivery-plan", "no-milestone", "existing-milestone"},
		"already linked by Briefing \"linked-existing\"",
	)
	assertCommandFails(t, configPath,
		[]string{"briefing", "dependency", "add", "delivery-plan", "no-milestone", "blocked-missing"},
		"plan validation failed",
	)
	assertCommandFails(t, configPath,
		[]string{"briefing", "dependency", "remove", "delivery-plan", "no-milestone", "blocked-missing"},
		"does not depend",
	)

	assertFilesUnchanged(t, before)
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("expected Plan file to remain after failed destructive commands: %v", err)
	}
}

func writePlanningCommandFixture(t *testing.T) (root, configPath, statePath string) {
	t.Helper()

	root = t.TempDir()
	configPath = filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath = filepath.Join(root, ".cyclestone", "state.json")
	writeMainTestFile(t, configPath, `milestones:
  - id: existing-milestone
    title: Existing Milestone
    spec_path: milestones/existing-milestone.md
  - id: standalone-milestone
    title: Standalone Milestone
    spec_path: milestones/standalone-milestone.md
`)
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"), "# Milestone Spec: existing-milestone - Existing Milestone\n\n## Goal\nExisting.\n")
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"), "# Milestone Spec: standalone-milestone - Standalone Milestone\n\n## Goal\nStandalone.\n")
	writeMainTestFile(t, statePath, `{"active_milestone_id":"existing-milestone","milestone_statuses":{},"milestone_cycles":{},"history":{}}`)
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"), "existing report\n")
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml"), `schema_version: 1
id: delivery-plan
title: Delivery Plan
objective: Navigate planning records without writes.
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: patrick
updated_at: "2026-07-20T11:00:00Z"
updated_by: patrick
constraints:
  - Keep commands read-only.
briefing_order:
  - no-milestone
  - linked-existing
  - blocked-missing
briefings:
  - id: no-milestone
    title: No Milestone
    objective: Exercise no milestone display.
    intent: Show standalone planning work.
    status: active
    completion_signal: No milestone label is visible.
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
  - id: linked-existing
    title: Linked Existing
    objective: Exercise linked milestone display.
    intent: Show a valid relationship.
    status: completed
    milestone_id: existing-milestone
    completion_signal: Linked milestone label is visible.
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
  - id: blocked-missing
    title: Blocked Missing
    objective: Exercise missing milestone display.
    intent: Keep optional references non-fatal.
    status: active
    depends_on:
      - no-milestone
    milestone_id: missing-milestone
    completion_signal: Missing reference is shown safely.
    constraints:
      - Do not create milestone files.
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
  - id: archived-note
    title: Archived Note
    objective: Remain addressable after active order.
    intent: Preserve archived context.
    status: archived
    completion_signal: Archived briefing remains visible in detail output.
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
`)
	return root, configPath, statePath
}

func assertCommandSucceeds(t *testing.T, configPath string, args []string, wantOutput string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runPlanningCommand(args, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("command %q returned %d, stderr:\n%s", strings.Join(args, " "), code, stderr.String())
	}
	if !strings.Contains(stdout.String(), wantOutput) {
		t.Fatalf("expected stdout to contain %q, got:\n%s", wantOutput, stdout.String())
	}
}

func assertCommandFails(t *testing.T, configPath string, args []string, wantError string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runPlanningCommand(args, configPath, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("command %q unexpectedly succeeded, stdout:\n%s", strings.Join(args, " "), stdout.String())
	}
	if !strings.Contains(stderr.String(), wantError) {
		t.Fatalf("expected stderr to contain %q, got:\n%s", wantError, stderr.String())
	}
}

func loadMainTestPlan(t *testing.T, root, planID string) config.Plan {
	t.Helper()
	state, result := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"))
	if result.HasErrors() {
		t.Fatalf("failed to load planning state: %+v", result.Messages)
	}
	plan, ok := findPlan(state, planID)
	if !ok {
		t.Fatalf("Plan %q not found in %+v", planID, state.Plans)
	}
	return plan
}

func writeMainTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func snapshotFiles(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read %s: %v", path, err)
		}
		snapshot[path] = string(data)
	}
	return snapshot
}

func assertFilesUnchanged(t *testing.T, before map[string]string) {
	t.Helper()
	for path, want := range before {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read %s after command: %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("expected %s to remain unchanged", path)
		}
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be missing, stat error: %v", path, err)
	}
}
