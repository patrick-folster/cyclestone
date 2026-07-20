package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/tui"
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
		"- linked-existing | completed | readiness: completed | milestone: linked existing-milestone (standalone) | Linked Existing",
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

func TestPlanningRelationDisplayIncludesCrossPlanLinks(t *testing.T) {
	t.Parallel()

	root, configPath, _ := writePlanningCommandFixture(t)
	writeOtherPlanWithMilestoneLink(t, root, "other-plan", "foreign-link", "existing-milestone", "completed")

	var stdout, stderr bytes.Buffer
	code := runReadOnlyCommand([]string{"briefing", "show", "delivery-plan", "linked-existing"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("briefing show returned %d, stderr:\n%s", code, stderr.String())
	}
	want := "Milestone: linked existing-milestone (also linked by Plan other-plan Briefing foreign-link)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("expected briefing show output to contain %q, got:\n%s", want, stdout.String())
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

func TestPlanGenerateCreatesValidatedPlanOnly(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	writeMainTestFile(t, configPath, `milestones:
  - id: existing-milestone
    title: Existing Milestone
    spec_path: milestones/existing-milestone.md
`)
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"), "# Existing\n")
	writeMainTestFile(t, statePath, `{"active_milestone_id":"existing-milestone","milestone_statuses":{},"milestone_cycles":{"existing-milestone":3},"history":{}}`)
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"), "existing report\n")
	responsePath := filepath.Join(root, "response.json")
	writeMainTestFile(t, responsePath, `{
  "title": "Improve First Run Setup",
  "objective": "Make first run setup dependable and easy to validate.",
  "constraints": ["Do not alter milestone execution."],
  "briefings": [
    {
      "title": "Audit setup flow",
      "objective": "Map current setup states.",
      "intent": "Expose gaps before implementation.",
      "completion_signal": "Setup states are documented."
    },
    {
      "title": "Add setup validation",
      "objective": "Validate setup inputs before save.",
      "intent": "Prevent invalid local configuration.",
      "completion_signal": "Invalid setup input is rejected.",
      "constraints": ["Keep non-interactive startup unchanged."],
      "depends_on": ["Audit setup flow"]
    },
    {
      "title": "Add setup validation",
      "objective": "Cover duplicate-title ID suffixing.",
      "intent": "Keep generated IDs deterministic.",
      "completion_signal": "Duplicate title has a suffixed ID."
    }
  ]
}`)
	before := snapshotFiles(t,
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)

	var stdout, stderr bytes.Buffer
	code := runPlanningCommand([]string{"plan", "generate", "--goal", "Improve first run setup", "--actor", "pm", "--response-file", responsePath}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan generate returned %d, stderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Plan \"improve-first-run-setup\" generated",
		"Plan: improve-first-run-setup",
		"- audit-setup-flow | active | readiness: ready | milestone: none | Audit setup flow",
		"- add-setup-validation | active | readiness: blocked | milestone: none | Add setup validation",
		"- add-setup-validation-2 | active | readiness: ready | milestone: none | Add setup validation",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected generated output to contain %q, got:\n%s", want, out)
		}
	}
	assertFilesUnchanged(t, before)
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "temp"))
	plan := loadMainTestPlan(t, root, "improve-first-run-setup")
	if plan.CreatedBy != "pm" || plan.UpdatedBy != "pm" || plan.Status != "active" {
		t.Fatalf("unexpected generated plan metadata: %+v", plan)
	}
	if strings.Join(plan.BriefingOrder, "|") != "audit-setup-flow|add-setup-validation|add-setup-validation-2" {
		t.Fatalf("unexpected generated briefing order: %+v", plan.BriefingOrder)
	}
	second, ok := findBriefing(plan, "add-setup-validation")
	if !ok || strings.Join(second.DependsOn, "|") != "audit-setup-flow" || second.MilestoneID != "" {
		t.Fatalf("unexpected generated dependency or milestone link: %+v", second)
	}
}

func TestPlanGeneratePreviewDoesNotWritePlan(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	writeMainTestFile(t, configPath, "milestones: []\n")
	responsePath := filepath.Join(root, "response.json")
	writeMainTestFile(t, responsePath, validGeneratedPlanJSON("Preview Plan"))

	var stdout, stderr bytes.Buffer
	code := runPlanningCommand([]string{"plan", "generate", "--goal", "Preview work", "--preview", "--response-file", responsePath}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preview returned %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Generated Plan \"preview-plan\" preview") || !strings.Contains(stdout.String(), "Plan: preview-plan") {
		t.Fatalf("unexpected preview output:\n%s", stdout.String())
	}
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "plans"))
}

func TestPlanGenerateRejectsInvalidResponsesWithoutWrites(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		error string
	}{
		{name: "malformed", body: `{"title":`, error: "response must contain one JSON object"},
		{name: "missing fields", body: `{"title":"Missing Fields","objective":"","briefings":[]}`, error: "objective is required"},
		{name: "unknown dependency", body: `{"title":"Bad Dependency","objective":"Objective.","briefings":[{"title":"One","objective":"Objective.","intent":"Intent.","completion_signal":"Done.","depends_on":["Missing"]}]}`, error: "depends on unknown Briefing"},
		{name: "milestone id", body: `{"title":"Forbidden Link","objective":"Objective.","briefings":[{"title":"One","objective":"Objective.","intent":"Intent.","completion_signal":"Done.","milestone_id":"existing-milestone"}]}`, error: "must not include milestone_id"},
		{name: "cycle", body: `{"title":"Cycle Plan","objective":"Objective.","briefings":[{"title":"One","objective":"Objective.","intent":"Intent.","completion_signal":"Done.","depends_on":["Two"]},{"title":"Two","objective":"Objective.","intent":"Intent.","completion_signal":"Done.","depends_on":["One"]}]}`, error: "generated Plan validation failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, configPath, statePath := writePlanningCommandFixture(t)
			planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
			before := snapshotFiles(t,
				configPath,
				statePath,
				planPath,
				filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
				filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
			)
			responsePath := filepath.Join(root, tc.name+".json")
			writeMainTestFile(t, responsePath, tc.body)

			assertCommandFails(t, configPath,
				[]string{"plan", "generate", "--goal", "Should not write", "--response-file", responsePath},
				tc.error,
			)
			assertFilesUnchanged(t, before)
			assertPathMissing(t, filepath.Join(root, ".cyclestone", "plans", "should-not-write.yml"))
		})
	}
}

func TestPlanGenerateRejectsExistingPlanCollision(t *testing.T) {
	root, configPath, statePath := writePlanningCommandFixture(t)
	before := snapshotFiles(t,
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml"),
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)
	responsePath := filepath.Join(root, "collision.json")
	writeMainTestFile(t, responsePath, validGeneratedPlanJSON("Delivery Plan"))

	assertCommandFails(t, configPath,
		[]string{"plan", "generate", "--goal", "Collision", "--response-file", responsePath},
		"generated Plan \"delivery-plan\" already exists",
	)
	assertFilesUnchanged(t, before)
}

func TestPlanGenerateUsesInjectedRunnerAndBoundedPrompt(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	writeMainTestFile(t, configPath, "milestones: []\n")
	oldRunner := runPlanGenerationRunner
	defer func() { runPlanGenerationRunner = oldRunner }()
	var gotCommand, gotPrompt string
	runPlanGenerationRunner = func(command, prompt string) (string, error) {
		gotCommand, gotPrompt = command, prompt
		return validGeneratedPlanJSON("Runner Plan"), nil
	}

	var stdout, stderr bytes.Buffer
	code := runPlanningCommand([]string{"plan", "generate", "--goal", "Runner goal", "--runner-command", "fake-ai"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runner generation returned %d, stderr:\n%s", code, stderr.String())
	}
	if gotCommand != "fake-ai" {
		t.Fatalf("expected runner command to be passed through, got %q", gotCommand)
	}
	for _, want := range []string{"# Cyclestone Plan Generation", "Runner goal", "Return only one JSON object", "Do not include `milestone_id`"} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, gotPrompt)
		}
	}
	if len([]rune(gotPrompt)) > maxPlanGenerationContextChars {
		t.Fatalf("prompt exceeded context bound")
	}
	plan := loadMainTestPlan(t, root, "runner-plan")
	if plan.ID != "runner-plan" || len(plan.Briefings) != 1 {
		t.Fatalf("unexpected generated plan from runner: %+v", plan)
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

func TestBriefingLinkReplacementRequiresExplicitFlagAndPreservesOldMilestone(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "temp", "runner-note.txt"), "temp artifact\n")
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "branch-snapshots", "existing-milestone.txt"), "snapshot artifact\n")
	before := snapshotFiles(t,
		configPath,
		statePath,
		planPath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
		filepath.Join(root, ".cyclestone", "temp", "runner-note.txt"),
		filepath.Join(root, ".cyclestone", "branch-snapshots", "existing-milestone.txt"),
	)

	assertCommandFails(t, configPath,
		[]string{"briefing", "link", "delivery-plan", "linked-existing", "standalone-milestone"},
		`already linked to Milestone "existing-milestone"; pass --replace-link`,
	)
	assertFilesUnchanged(t, before)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "link", "delivery-plan", "linked-existing", "standalone-milestone", "--replace-link", "--actor", "reviewer"},
		`Briefing "linked-existing" linked to Milestone "standalone-milestone"`,
	)
	plan := loadMainTestPlan(t, root, "delivery-plan")
	briefing, _ := findBriefing(plan, "linked-existing")
	if briefing.MilestoneID != "standalone-milestone" || briefing.UpdatedBy != "reviewer" {
		t.Fatalf("expected replacement link and actor to persist, got %+v", briefing)
	}
	assertFilesUnchanged(t, map[string]string{
		configPath: before[configPath],
		statePath:  before[statePath],
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"):         before[filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md")],
		filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"):       before[filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md")],
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"): before[filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md")],
		filepath.Join(root, ".cyclestone", "temp", "runner-note.txt"):                     before[filepath.Join(root, ".cyclestone", "temp", "runner-note.txt")],
		filepath.Join(root, ".cyclestone", "branch-snapshots", "existing-milestone.txt"):  before[filepath.Join(root, ".cyclestone", "branch-snapshots", "existing-milestone.txt")],
	})
}

func TestBriefingLinkReplacementRejectsMissingAndCrossPlanMilestones(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
	writeOtherPlanWithMilestoneLink(t, root, "other-plan", "foreign-link", "standalone-milestone", "active")
	before := snapshotFiles(t,
		configPath,
		statePath,
		planPath,
		filepath.Join(root, ".cyclestone", "plans", "other-plan.yml"),
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)

	assertCommandFails(t, configPath,
		[]string{"briefing", "link", "delivery-plan", "linked-existing", "missing-milestone", "--replace-link"},
		`Milestone "missing-milestone" not found`,
	)
	assertCommandFails(t, configPath,
		[]string{"briefing", "link", "delivery-plan", "linked-existing", "standalone-milestone", "--replace-link"},
		`Milestone "standalone-milestone" is already linked by Briefing "foreign-link" in Plan "other-plan"`,
	)
	assertFilesUnchanged(t, before)
}

func TestBriefingGenerateMilestoneCreatesOrdinaryMilestoneAndLink(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	assertCommandSucceeds(t, configPath, []string{"briefing", "approve", "delivery-plan", "no-milestone", "--actor", "reviewer"}, "approved")
	before := snapshotFiles(t,
		statePath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)

	var stdout, stderr bytes.Buffer
	code := runPlanningCommand([]string{"briefing", "generate-milestone", "delivery-plan", "no-milestone", "--actor", "generator"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("briefing generate-milestone returned %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `Milestone "delivery-plan-no-milestone" generated`) {
		t.Fatalf("unexpected stdout:\n%s", stdout.String())
	}
	assertFilesUnchanged(t, before)

	plan := loadMainTestPlan(t, root, "delivery-plan")
	briefing, ok := findBriefing(plan, "no-milestone")
	if !ok || briefing.MilestoneID != "delivery-plan-no-milestone" || briefing.UpdatedBy != "generator" {
		t.Fatalf("expected source Briefing to persist generated link, got %+v ok=%v", briefing, ok)
	}

	specPath := filepath.Join(root, ".cyclestone", "milestones", "delivery-plan-no-milestone.md")
	specBytes, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("expected generated spec: %v", err)
	}
	spec := string(specBytes)
	for _, want := range []string{
		"# Milestone Spec: delivery-plan-no-milestone - No Milestone",
		"## Goal",
		"## Implementation Prompt",
		"## Explicit Exclusions",
		"## Acceptance Criteria",
		"## Repository Context",
		"## Testing Expectations",
		"Plan `delivery-plan`",
		"Briefing `no-milestone`",
		"cmd/cyclestone/main_test.go",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("expected generated spec to contain %q, got:\n%s", want, spec)
		}
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	var generated config.Milestone
	var foundStandalone bool
	for _, milestone := range cfg.Milestones {
		if milestone.ID == "delivery-plan-no-milestone" {
			generated = milestone
		}
		if milestone.ID == "standalone-milestone" {
			foundStandalone = true
		}
	}
	if generated.ID == "" || generated.SpecPath != filepath.Join("milestones", "delivery-plan-no-milestone.md") || !strings.Contains(generated.Goal, "Exercise no milestone display.") {
		t.Fatalf("expected generated ordinary milestone to hydrate from spec, got %+v", generated)
	}
	if !foundStandalone {
		t.Fatalf("expected standalone milestone to remain indexed, cfg=%+v", cfg.Milestones)
	}

	if err := os.Remove(filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")); err != nil {
		t.Fatalf("failed to remove source Plan: %v", err)
	}
	cfgAfterPlanRemoval, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig should not require source Plan: %v", err)
	}
	if len(cfgAfterPlanRemoval.Milestones) != len(cfg.Milestones) {
		t.Fatalf("expected milestone index to survive Plan removal, got %+v", cfgAfterPlanRemoval.Milestones)
	}
}

func TestBriefingExecuteGeneratesAndQueuesOneOrdinaryMilestone(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	stateBefore := snapshotFiles(t,
		statePath,
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)
	var launched tui.RootModel
	var stdout, stderr bytes.Buffer
	code := runBriefingExecute([]string{"delivery-plan", "no-milestone"}, briefingExecutionOptions{
		configPath: configPath, statePath: statePath, noBranchChange: true,
	}, &stdout, &stderr, func(model tui.RootModel) error {
		launched = model
		return nil
	})
	if code != 0 {
		t.Fatalf("briefing execute returned %d, stderr:\n%s", code, stderr.String())
	}
	if launched.PendingCycle == nil {
		t.Fatal("expected generated Milestone to be queued for preflight")
	}
	request := *launched.PendingCycle
	if request.Milestone.ID != "delivery-plan-no-milestone" || !request.NoBranchChange {
		t.Fatalf("unexpected queued cycle: %+v", request)
	}
	if request.BriefingOrigin.PlanID != "delivery-plan" || request.BriefingOrigin.BriefingID != "no-milestone" {
		t.Fatalf("unexpected planning origin: %+v", request.BriefingOrigin)
	}
	plan := loadMainTestPlan(t, root, "delivery-plan")
	briefing, _ := findBriefing(plan, "no-milestone")
	if briefing.MilestoneID != request.Milestone.ID {
		t.Fatalf("expected link to persist before launch, got %+v", briefing)
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones", request.Milestone.ID+".md")); err != nil {
		t.Fatalf("expected generated Milestone spec: %v", err)
	}
	assertFilesUnchanged(t, stateBefore)
}

func TestBriefingExecuteLinkedMilestoneDoesNotRewriteArtifacts(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	tracked := snapshotFiles(t,
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml"),
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)
	var launched tui.RootModel
	code := runBriefingExecute([]string{"delivery-plan", "linked-existing"}, briefingExecutionOptions{
		configPath: configPath, statePath: statePath,
	}, io.Discard, io.Discard, func(model tui.RootModel) error {
		launched = model
		return nil
	})
	if code != 0 || launched.PendingCycle == nil || launched.PendingCycle.Milestone.ID != "existing-milestone" {
		t.Fatalf("expected linked Milestone to be queued unchanged, code=%d request=%+v", code, launched.PendingCycle)
	}
	assertFilesUnchanged(t, tracked)
}

func TestBriefingExecuteRejectsInvalidSelections(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
	base := snapshotFiles(t, configPath, statePath, planPath)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"missing-plan", "anything"}, `Plan "missing-plan" not found`},
		{[]string{"delivery-plan", "missing"}, `Briefing "missing" not found`},
		{[]string{"delivery-plan", "archived-note"}, `not eligible for execution`},
		{[]string{"delivery-plan", "blocked-missing"}, `incomplete dependencies: no-milestone`},
	}
	for _, tc := range tests {
		var stderr bytes.Buffer
		launched := false
		code := runBriefingExecute(tc.args, briefingExecutionOptions{configPath: configPath, statePath: statePath}, io.Discard, &stderr, func(tui.RootModel) error {
			launched = true
			return nil
		})
		if code == 0 || launched || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("args %v: code=%d launched=%v stderr=%q, want %q", tc.args, code, launched, stderr.String(), tc.want)
		}
		assertFilesUnchanged(t, base)
	}
}

func TestBriefingExecuteTreatsDanglingMilestoneWarningAsFatalForSelectedBriefing(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "id: no-milestone\n    title: No Milestone\n    objective: Exercise no milestone display.\n    intent: Show standalone planning work.\n    status: active", "id: no-milestone\n    title: No Milestone\n    objective: Exercise no milestone display.\n    intent: Show standalone planning work.\n    status: completed", 1)
	writeMainTestFile(t, planPath, updated)
	var stderr bytes.Buffer
	launched := false
	code := runBriefingExecute([]string{"delivery-plan", "blocked-missing"}, briefingExecutionOptions{configPath: configPath, statePath: statePath}, io.Discard, &stderr, func(tui.RootModel) error {
		launched = true
		return nil
	})
	if code == 0 || launched || !strings.Contains(stderr.String(), `links missing Milestone "missing-milestone"`) {
		t.Fatalf("expected selected dangling link to be fatal, code=%d launched=%v stderr=%q", code, launched, stderr.String())
	}
}

func TestBriefingExecuteRejectsMalformedPlanningBeforeLaunch(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
	writeMainTestFile(t, planPath, "schema_version: 1\nid: INVALID\n")
	launched := false
	var stderr bytes.Buffer
	code := runBriefingExecute([]string{"delivery-plan", "no-milestone"}, briefingExecutionOptions{configPath: configPath, statePath: statePath}, io.Discard, &stderr, func(tui.RootModel) error {
		launched = true
		return nil
	})
	if code == 0 || launched || !strings.Contains(stderr.String(), "planning files contain validation errors") {
		t.Fatalf("expected malformed planning to block launch, code=%d launched=%v stderr=%q", code, launched, stderr.String())
	}
}

func TestBriefingExecutePreservesGeneratedMilestoneWhenLinkSaveFails(t *testing.T) {
	root, configPath, statePath := writePlanningCommandFixture(t)
	plansDir := filepath.Join(root, ".cyclestone", "plans")
	if err := os.Chmod(plansDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansDir, 0755) })
	launched := false
	var stderr bytes.Buffer
	code := runBriefingExecute([]string{"delivery-plan", "no-milestone"}, briefingExecutionOptions{configPath: configPath, statePath: statePath}, io.Discard, &stderr, func(tui.RootModel) error {
		launched = true
		return nil
	})
	if code == 0 || launched || !strings.Contains(stderr.String(), "was created, but its Briefing link could not be persisted; execution was not started") {
		t.Fatalf("expected explicit partial-success failure, code=%d launched=%v stderr=%q", code, launched, stderr.String())
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, milestone := range cfg.Milestones {
		found = found || milestone.ID == "delivery-plan-no-milestone"
	}
	if !found {
		t.Fatal("generated Milestone index entry was rolled back")
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones", "delivery-plan-no-milestone.md")); err != nil {
		t.Fatalf("generated Milestone spec was not preserved: %v", err)
	}
}

func TestBriefingPreparationReportsSpecOnlyPartialSuccess(t *testing.T) {
	root, configPath, _ := writePlanningCommandFixture(t)
	ctx, err := loadPlanningCommandContext(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(configPath, 0644) })
	_, err = prepareBriefingMilestone(ctx, configPath, briefingMilestoneRequest{
		planID: "delivery-plan", briefingID: "no-milestone", actor: "test", allowActive: true,
	})
	if err == nil || !strings.Contains(err.Error(), "was written, but the compact index update failed") {
		t.Fatalf("expected spec-only partial-success error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones", "delivery-plan-no-milestone.md")); err != nil {
		t.Fatalf("expected orphan spec to remain for human recovery: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, milestone := range cfg.Milestones {
		if milestone.ID == "delivery-plan-no-milestone" {
			t.Fatal("compact index unexpectedly changed despite forced write failure")
		}
	}
}

func TestBriefingGenerateMilestonePreviewDoesNotWrite(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	assertCommandSucceeds(t, configPath, []string{"briefing", "approve", "delivery-plan", "no-milestone"}, "approved")
	before := snapshotFiles(t,
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml"),
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"),
	)

	var stdout, stderr bytes.Buffer
	code := runPlanningCommand([]string{"briefing", "generate-milestone", "delivery-plan", "no-milestone", "--preview"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preview returned %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Generated Milestone \"delivery-plan-no-milestone\" preview") || !strings.Contains(stdout.String(), "Briefing Link: Plan \"delivery-plan\" Briefing \"no-milestone\"") {
		t.Fatalf("unexpected preview stdout:\n%s", stdout.String())
	}
	assertFilesUnchanged(t, before)
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "milestones", "delivery-plan-no-milestone.md"))
}

func TestBriefingGenerateMilestoneRefusesInvalidInputsWithoutWrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		prepare   func(t *testing.T, root, configPath string)
		args      []string
		wantError string
	}{
		{
			name:      "active briefing",
			args:      []string{"briefing", "generate-milestone", "delivery-plan", "no-milestone"},
			wantError: "must be completed",
		},
		{
			name: "incomplete dependency",
			prepare: func(t *testing.T, root, configPath string) {
				assertCommandSucceeds(t, configPath, []string{"briefing", "approve", "delivery-plan", "blocked-missing"}, "approved")
			},
			args:      []string{"briefing", "generate-milestone", "delivery-plan", "blocked-missing", "--replace-link"},
			wantError: "incomplete dependencies: no-milestone",
		},
		{
			name:      "missing briefing",
			args:      []string{"briefing", "generate-milestone", "delivery-plan", "missing"},
			wantError: "not found",
		},
		{
			name:      "existing link",
			args:      []string{"briefing", "generate-milestone", "delivery-plan", "linked-existing"},
			wantError: "already linked",
		},
		{
			name: "foreign active link to generated id",
			prepare: func(t *testing.T, root, configPath string) {
				assertCommandSucceeds(t, configPath, []string{"briefing", "approve", "delivery-plan", "no-milestone"}, "approved")
				writeMainTestFile(t, filepath.Join(root, ".cyclestone", "plans", "other-plan.yml"), `schema_version: 1
id: other-plan
title: Other Plan
objective: Keep duplicate links guarded.
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: patrick
updated_at: "2026-07-20T11:00:00Z"
updated_by: patrick
briefing_order:
  - foreign-link
briefings:
  - id: foreign-link
    title: Foreign Link
    objective: Hold a duplicate generated milestone ID.
    intent: Validate link uniqueness.
    status: active
    milestone_id: delivery-plan-no-milestone
    completion_signal: Duplicate link is rejected.
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
`)
			},
			args:      []string{"briefing", "generate-milestone", "delivery-plan", "no-milestone"},
			wantError: "already linked by Briefing",
		},
		{
			name: "malformed planning data",
			prepare: func(t *testing.T, root, configPath string) {
				writeMainTestFile(t, filepath.Join(root, ".cyclestone", "plans", "bad.yml"), "schema_version: [\n")
			},
			args:      []string{"briefing", "generate-milestone", "delivery-plan", "linked-existing", "--replace-link"},
			wantError: "planning files contain validation errors",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, configPath, statePath := writePlanningCommandFixture(t)
			if tc.prepare != nil {
				tc.prepare(t, root, configPath)
			}
			before := snapshotFiles(t,
				configPath,
				statePath,
				filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml"),
				filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
				filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"),
			)
			var stdout, stderr bytes.Buffer
			code := runPlanningCommand(tc.args, configPath, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("command unexpectedly succeeded, stdout:\n%s", stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.wantError) {
				t.Fatalf("expected stderr to contain %q, got:\n%s", tc.wantError, stderr.String())
			}
			assertFilesUnchanged(t, before)
			assertPathMissing(t, filepath.Join(root, ".cyclestone", "milestones", "delivery-plan-no-milestone.md"))
			assertPathMissing(t, filepath.Join(root, ".cyclestone", "milestones", "delivery-plan-blocked-missing.md"))
		})
	}
}

func TestBriefingGenerateMilestoneReplaceLinkDoesNotDeleteOldMilestone(t *testing.T) {
	t.Parallel()

	root, configPath, _ := writePlanningCommandFixture(t)
	var stdout, stderr bytes.Buffer
	code := runPlanningCommand([]string{"briefing", "generate-milestone", "delivery-plan", "linked-existing", "--replace-link", "--milestone-id", "replacement-milestone"}, configPath, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("replace-link generate returned %d, stderr:\n%s", code, stderr.String())
	}
	plan := loadMainTestPlan(t, root, "delivery-plan")
	briefing, _ := findBriefing(plan, "linked-existing")
	if briefing.MilestoneID != "replacement-milestone" {
		t.Fatalf("expected Briefing link to be replaced, got %+v", briefing)
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md")); err != nil {
		t.Fatalf("expected old linked Milestone spec to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones", "replacement-milestone.md")); err != nil {
		t.Fatalf("expected replacement Milestone spec: %v", err)
	}
}

func TestPlanAndBriefingReviewAliasesOnlyUpdatePlanFile(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	milestonePaths := []string{
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "milestones", "standalone-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
		filepath.Join(root, ".cyclestone", "temp", "runner-note.txt"),
		filepath.Join(root, ".cyclestone", "branch-snapshots", "existing-milestone.txt"),
	}
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "temp", "runner-note.txt"), "temp artifact\n")
	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "branch-snapshots", "existing-milestone.txt"), "snapshot artifact\n")
	beforeMilestones := snapshotFiles(t, milestonePaths...)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "approve", "delivery-plan", "no-milestone", "--actor", "reviewer"},
		"Briefing \"no-milestone\" approved",
	)
	plan := loadMainTestPlan(t, root, "delivery-plan")
	noMilestone, _ := findBriefing(plan, "no-milestone")
	if noMilestone.Status != "completed" || noMilestone.UpdatedBy != "reviewer" {
		t.Fatalf("expected briefing approval to update planning status, got %+v", noMilestone)
	}
	assertFilesUnchanged(t, beforeMilestones)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "reject", "delivery-plan", "blocked-missing", "--actor", "reviewer"},
		"Briefing \"blocked-missing\" rejected",
	)
	plan = loadMainTestPlan(t, root, "delivery-plan")
	blocked, _ := findBriefing(plan, "blocked-missing")
	if blocked.Status != "archived" || containsString(plan.BriefingOrder, "blocked-missing") {
		t.Fatalf("expected rejected briefing to be archived and removed from active order, plan=%+v", plan)
	}
	assertFilesUnchanged(t, beforeMilestones)

	assertCommandSucceeds(t, configPath,
		[]string{"plan", "approve", "delivery-plan", "--actor", "reviewer"},
		"Plan \"delivery-plan\" approved",
	)
	plan = loadMainTestPlan(t, root, "delivery-plan")
	if plan.Status != "completed" || plan.UpdatedBy != "reviewer" {
		t.Fatalf("expected plan approval to update planning status, got %+v", plan)
	}
	assertFilesUnchanged(t, beforeMilestones)

	assertCommandSucceeds(t, configPath,
		[]string{"plan", "reject", "delivery-plan", "--actor", "reviewer"},
		"Plan \"delivery-plan\" rejected",
	)
	plan = loadMainTestPlan(t, root, "delivery-plan")
	if plan.Status != "archived" {
		t.Fatalf("expected plan rejection to archive planning status, got %+v", plan)
	}
	assertFilesUnchanged(t, beforeMilestones)
}

func TestBriefingSplitRewritesOrderDependenciesAndPreservesMilestoneStorage(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	partsPath := filepath.Join(root, "split-parts.json")
	writeMainTestFile(t, partsPath, `{
  "parts": [
    {
      "id": "scope-copy",
      "title": "Scope Copy",
      "objective": "Scope copy changes.",
      "intent": "Keep the first split part focused.",
      "completion_signal": "Copy scope is clear."
    },
    {
      "id": "ship-copy",
      "title": "Ship Copy",
      "objective": "Ship copy changes.",
      "intent": "Keep delivery separate.",
      "completion_signal": "Copy ships."
    }
  ]
}`)
	milestonePaths := []string{
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	}
	beforeMilestones := snapshotFiles(t, milestonePaths...)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "split", "delivery-plan", "no-milestone", "--parts-file", partsPath, "--actor", "reviewer"},
		"Briefing \"no-milestone\" split into 2 Briefings",
	)
	plan := loadMainTestPlan(t, root, "delivery-plan")
	if _, ok := findBriefing(plan, "no-milestone"); ok {
		t.Fatalf("expected source briefing to be removed after split, plan=%+v", plan)
	}
	if strings.Join(plan.BriefingOrder, "|") != "scope-copy|ship-copy|linked-existing|blocked-missing" {
		t.Fatalf("unexpected split briefing order: %+v", plan.BriefingOrder)
	}
	shipCopy, _ := findBriefing(plan, "ship-copy")
	if strings.Join(shipCopy.DependsOn, "|") != "scope-copy" {
		t.Fatalf("expected second split part to depend on first by default, got %+v", shipCopy)
	}
	blocked, _ := findBriefing(plan, "blocked-missing")
	if strings.Join(blocked.DependsOn, "|") != "ship-copy" {
		t.Fatalf("expected external dependent to point at final split part, got %+v", blocked)
	}
	assertFilesUnchanged(t, beforeMilestones)
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "milestones", "scope-copy.md"))
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "milestones", "ship-copy.md"))
}

func TestBriefingSplitLinkedSourceRequiresExplicitMilestoneChoice(t *testing.T) {
	t.Parallel()

	root, configPath, statePath := writePlanningCommandFixture(t)
	partsPath := filepath.Join(root, "linked-split-parts.json")
	writeMainTestFile(t, partsPath, `[
  {
    "id": "linked-scope",
    "title": "Linked Scope",
    "objective": "Scope linked work.",
    "intent": "Separate linked scope.",
    "completion_signal": "Linked scope is clear."
  },
  {
    "id": "linked-ship",
    "title": "Linked Ship",
    "objective": "Ship linked work.",
    "intent": "Separate linked delivery.",
    "completion_signal": "Linked delivery ships."
  }
]`)
	planPath := filepath.Join(root, ".cyclestone", "plans", "delivery-plan.yml")
	before := snapshotFiles(t,
		configPath,
		statePath,
		planPath,
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"),
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"),
	)

	assertCommandFails(t, configPath,
		[]string{"briefing", "split", "delivery-plan", "linked-existing", "--parts-file", partsPath},
		"requires --milestone-link",
	)
	assertFilesUnchanged(t, before)

	assertCommandSucceeds(t, configPath,
		[]string{"briefing", "split", "delivery-plan", "linked-existing", "--parts-file", partsPath, "--milestone-link", "linked-ship"},
		"Briefing \"linked-existing\" split into 2 Briefings",
	)
	plan := loadMainTestPlan(t, root, "delivery-plan")
	linkedScope, _ := findBriefing(plan, "linked-scope")
	linkedShip, _ := findBriefing(plan, "linked-ship")
	if linkedScope.MilestoneID != "" || linkedShip.MilestoneID != "existing-milestone" {
		t.Fatalf("expected explicit split part to keep milestone link, scope=%+v ship=%+v", linkedScope, linkedShip)
	}
	assertFilesUnchanged(t, map[string]string{
		configPath: before[configPath],
		statePath:  before[statePath],
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"):         before[filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md")],
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"): before[filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md")],
	})
}

func TestBriefingMergeRequiresExplicitLinkChoiceForMultipleLinks(t *testing.T) {
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
	mergeArgs := []string{
		"briefing", "merge", "delivery-plan", "linked-existing", "blocked-missing",
		"--title", "Merged Review Work",
		"--objective", "Merge review work.",
		"--intent", "Review as one item.",
		"--completion-signal", "Merged item is reviewable.",
		"--status", "active",
	}

	assertCommandFails(t, configPath, mergeArgs, "requires --milestone-link")
	assertFilesUnchanged(t, before)

	assertCommandSucceeds(t, configPath,
		append(mergeArgs, "--milestone-link", "linked-existing", "--actor", "reviewer"),
		"Merged 2 Briefings into \"linked-existing\"",
	)
	plan := loadMainTestPlan(t, root, "delivery-plan")
	merged, _ := findBriefing(plan, "linked-existing")
	if merged.MilestoneID != "existing-milestone" || merged.Status != "active" || merged.UpdatedBy != "reviewer" {
		t.Fatalf("expected merged briefing to keep selected link and metadata, got %+v", merged)
	}
	if strings.Join(merged.DependsOn, "|") != "no-milestone" {
		t.Fatalf("expected merged dependencies to exclude merged IDs and keep external deps, got %+v", merged.DependsOn)
	}
	if strings.Join(merged.Constraints, "|") != "Do not create milestone files." {
		t.Fatalf("expected merged constraints to be preserved, got %+v", merged.Constraints)
	}
	if _, ok := findBriefing(plan, "blocked-missing"); ok || containsString(plan.BriefingOrder, "blocked-missing") {
		t.Fatalf("expected merged-away briefing to be removed from Plan and order, plan=%+v", plan)
	}
	assertFilesUnchanged(t, map[string]string{
		configPath: before[configPath],
		statePath:  before[statePath],
		filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md"):         before[filepath.Join(root, ".cyclestone", "milestones", "existing-milestone.md")],
		filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md"): before[filepath.Join(root, ".cyclestone", "reports", "existing-milestone", "summary.md")],
	})
	assertPathMissing(t, filepath.Join(root, ".cyclestone", "milestones", "blocked-missing.md"))
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
	writeMainTestFile(t, statePath, `{"active_milestone_id":"existing-milestone","milestone_statuses":{},"milestone_cycles":{"existing-milestone":3},"history":{}}`)
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

func writeOtherPlanWithMilestoneLink(t *testing.T, root, planID, briefingID, milestoneID, status string) {
	t.Helper()

	writeMainTestFile(t, filepath.Join(root, ".cyclestone", "plans", planID+".yml"), fmt.Sprintf(`schema_version: 1
id: %s
title: Other Plan
objective: Exercise cross-plan relations.
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: patrick
updated_at: "2026-07-20T11:00:00Z"
updated_by: patrick
briefing_order:
  - %s
briefings:
  - id: %s
    title: Foreign Link
    objective: Reference a milestone from another Plan.
    intent: Exercise duplicate relationship detection.
    status: %s
    milestone_id: %s
    completion_signal: Cross-plan relation is visible.
    created_at: "2026-07-20T10:00:00Z"
    created_by: patrick
    updated_at: "2026-07-20T11:00:00Z"
    updated_by: patrick
`, planID, briefingID, briefingID, status, milestoneID))
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

func validGeneratedPlanJSON(title string) string {
	return `{
  "title": "` + title + `",
  "objective": "Deliver a generated planning outcome.",
  "briefings": [
    {
      "title": "Define generated work",
      "objective": "Describe the work.",
      "intent": "Keep generation reviewable.",
      "completion_signal": "The generated work is clear."
    }
  ]
}`
}
