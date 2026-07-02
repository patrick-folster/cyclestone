package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestPreflightRenderingAndConfirmCancelFlow(t *testing.T) {
	styles := DefaultStyles(true, true)
	state := &config.State{
		MilestoneStatuses:        map[string]string{"0015-cycle-preflight-review": "In Progress"},
		MilestoneCycles:          map[string]int{"0015-cycle-preflight-review": 1},
		MilestoneRecommendations: map[string]int{},
		History:                  map[string][]config.MilestoneCycleLog{},
	}
	req := StartCycleMsg{
		Milestone: config.Milestone{
			ID:     "0015-cycle-preflight-review",
			Title:  "Cycle Preflight Review",
			Goal:   "Add preflight",
			Status: "Todo",
		},
		RunnerLLM:      "manual",
		RunnerMode:     "sandbox",
		NoBranchChange: true,
		Group:          config.AgentGroup{Name: "Solo", AgentIDs: []string{"pm"}},
		Note:           "review this run",
	}
	model := NewPreflightModel(styles)
	model.Width = 100
	model.Height = 30
	model.Load(req, state, ".cyclestone/milestone.yml", ".cyclestone/state.json")
	model.Pipeline = []config.Agent{{ID: "pm", Name: "PM", RunnerBinary: "manual", PromptBody: "prompt"}}
	model.Issues = nil

	view := model.View()
	for _, want := range []string{
		"CYCLE PREFLIGHT REVIEW",
		"0015-cycle-preflight-review - Cycle Preflight Review",
		"Status: In Progress",
		"Next cycle: 002",
		"Agent group: Solo",
		"Runner/model: manual",
		"Branch changes: disabled",
		"0015-cycle-preflight-review-cycle-002.yaml",
		"Cycle note: present",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected preflight view to contain %q, got:\n%s", want, view)
		}
	}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected confirm command")
	}
	msg := cmd()
	start, ok := msg.(StartCycleMsg)
	if !ok {
		t.Fatalf("expected StartCycleMsg, got %#v", msg)
	}
	if start.Note != req.Note || start.Milestone.ID != req.Milestone.ID {
		t.Fatalf("confirm did not preserve request: %#v", start)
	}

	model.FocusIndex = 1
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cancel command")
	}
	change, ok := cmd().(ChangeScreenMsg)
	if !ok || change.Screen != ScreenDetails {
		t.Fatalf("expected cancel to return to details, got %#v", change)
	}
}

func TestPreflightValidationBlocksInvalidGroupAndMissingAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	styles := DefaultStyles(true, true)
	state := &config.State{
		MilestoneStatuses:        map[string]string{},
		MilestoneCycles:          map[string]int{},
		MilestoneRecommendations: map[string]int{},
		History:                  map[string][]config.MilestoneCycleLog{},
	}
	model := NewPreflightModel(styles)
	model.Width = 90
	model.Height = 50
	model.Load(StartCycleMsg{
		Milestone: config.Milestone{ID: "0015-cycle-preflight-review", Title: "Preflight"},
		RunnerLLM: "gemini",
		Group:     config.AgentGroup{Name: "Broken", AgentIDs: []string{"missing-agent"}},
	}, state, ".cyclestone/milestone.yml", ".cyclestone/state.json")

	if !model.HasBlockers() {
		t.Fatal("expected blockers for missing agent and empty pipeline")
	}
	view := model.View()
	if !strings.Contains(view, "Selected group references missing agents: missing-agent") {
		t.Fatalf("expected missing agent blocker, got:\n%s", view)
	}
	if !strings.Contains(view, "Resolved agent pipeline is empty") {
		t.Fatalf("expected empty pipeline blocker, got:\n%s", view)
	}
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("blocked confirm should keep cancel focused and return cancel command")
	}
	change, ok := cmd().(ChangeScreenMsg)
	if !ok || change.Screen != ScreenDetails {
		t.Fatalf("expected blocked enter to cancel from focused cancel button, got %#v", change)
	}
}

func TestPreflightSingleAgentUsesOnlyEffectivePipeline(t *testing.T) {
	styles := DefaultStyles(true, true)
	state := &config.State{
		MilestoneStatuses:        map[string]string{},
		MilestoneCycles:          map[string]int{},
		MilestoneRecommendations: map[string]int{},
		History:                  map[string][]config.MilestoneCycleLog{},
	}
	model := NewPreflightModel(styles)
	model.Width = 100
	model.Height = 40
	model.Load(StartCycleMsg{
		Milestone:     config.Milestone{ID: "0015-cycle-preflight-review", Title: "Preflight"},
		SingleAgentID: "pm",
		RunnerLLM:     "manual",
		Group:         config.AgentGroup{Name: "Default", AgentIDs: []string{"pm", "missing-skipped"}},
	}, state, ".cyclestone/milestone.yml", ".cyclestone/state.json")

	if model.HasBlockers() {
		t.Fatalf("expected skipped missing group agent not to block single-agent preflight, issues=%#v", model.Issues)
	}
	if len(model.Pipeline) != 1 || model.Pipeline[0].ID != "pm" {
		t.Fatalf("expected effective pipeline to contain only pm, got %#v", model.Pipeline)
	}
	view := model.View()
	if !strings.Contains(view, "single agent: pm") || strings.Contains(view, "missing-skipped") {
		t.Fatalf("expected view to show selected single agent without skipped missing agent, got:\n%s", view)
	}
}

func TestPreflightSingleAgentMissingBlocksConfirm(t *testing.T) {
	styles := DefaultStyles(true, true)
	state := &config.State{
		MilestoneStatuses:        map[string]string{},
		MilestoneCycles:          map[string]int{},
		MilestoneRecommendations: map[string]int{},
		History:                  map[string][]config.MilestoneCycleLog{},
	}
	model := NewPreflightModel(styles)
	model.Width = 100
	model.Height = 40
	model.Load(StartCycleMsg{
		Milestone:     config.Milestone{ID: "0015-cycle-preflight-review", Title: "Preflight"},
		SingleAgentID: "missing-selected",
		RunnerLLM:     "manual",
		Group:         config.AgentGroup{Name: "Default", AgentIDs: []string{"pm", "missing-selected"}},
	}, state, ".cyclestone/milestone.yml", ".cyclestone/state.json")

	if !model.HasBlockers() {
		t.Fatal("expected missing selected single agent to block preflight")
	}
	view := model.View()
	if !strings.Contains(view, "Selected group references missing agents: missing-selected") {
		t.Fatalf("expected missing selected agent blocker, got:\n%s", view)
	}
	if !strings.Contains(view, "Resolved agent pipeline is empty") {
		t.Fatalf("expected empty effective pipeline blocker, got:\n%s", view)
	}
}

func TestStartCyclePipelineResolutionMatchesSingleAgentPreflight(t *testing.T) {
	agents := []config.Agent{
		{ID: "pm", Name: "PM"},
		{ID: "developer", Name: "Developer"},
		{ID: "qa", Name: "QA"},
	}
	group := config.AgentGroup{Name: "Default", AgentIDs: []string{"pm", "developer", "qa"}}

	pipeline, missing := resolveStartCyclePipeline(agents, group, "qa")
	if len(missing) != 0 {
		t.Fatalf("expected no missing agents, got %#v", missing)
	}
	if len(pipeline) != 1 || pipeline[0].ID != "qa" {
		t.Fatalf("expected only selected single agent in startup pipeline, got %#v", pipeline)
	}

	pipeline, missing = resolveStartCyclePipeline(agents, group, "missing-selected")
	if len(pipeline) != 0 {
		t.Fatalf("expected empty pipeline for missing selected agent, got %#v", pipeline)
	}
	if len(missing) != 1 || missing[0] != "missing-selected" {
		t.Fatalf("expected missing selected agent to be reported, got %#v", missing)
	}
}

func TestValidateRunnerAvailabilityWarningsAndBlockers(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	issue, ok := validateRunnerAvailability("openai")
	if !ok || issue.Severity != preflightBlocker || !strings.Contains(issue.Message, "OPENAI_API_KEY") {
		t.Fatalf("expected missing openai API key blocker, got %#v ok=%v", issue, ok)
	}

	tmp := t.TempDir()
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp)
	issue, ok = validateRunnerAvailability("definitely-missing-runner")
	if ok {
		t.Fatalf("custom or unknown runner should not be false-blocked, got %#v", issue)
	}
	issue, ok = validateRunnerAvailability("codex")
	if !ok || issue.Severity != preflightBlocker || !strings.Contains(issue.Message, "codex") {
		t.Fatalf("expected missing codex binary blocker with PATH %q, old PATH %q: %#v ok=%v", tmp, oldPath, issue, ok)
	}
}

func TestPreflightRootRoutingFromNoteForm(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	state := &config.State{
		MilestoneStatuses:        map[string]string{},
		MilestoneCycles:          map[string]int{},
		MilestoneRecommendations: map[string]int{},
		History:                  map[string][]config.MilestoneCycleLog{},
	}
	model := NewRootModel(&config.Config{Milestones: []config.Milestone{}}, state, configPath, statePath, true, false, true, true)
	model.Width = 100
	model.Height = 30

	req := StartCycleMsg{
		Milestone: config.Milestone{ID: "0015-cycle-preflight-review", Title: "Preflight"},
		Group:     config.AgentGroup{Name: "Solo", AgentIDs: []string{"pm"}},
	}
	updated, _ := model.Update(ChangeScreenMsg{Screen: ScreenCreateMilestone, Data: req})
	rootModel := updated.(RootModel)
	rootModel.CreateMilestone.GoalInput.SetValue("note body")
	rootModel.CreateMilestone.FocusIndex = 4

	updated, cmd := rootModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		msg := cmd()
		updated, _ = updated.Update(msg)
	}
	rootModel = updated.(RootModel)
	if rootModel.ActiveScreen != ScreenPreflight {
		t.Fatalf("expected note submit to route to preflight, got screen %v", rootModel.ActiveScreen)
	}
	if rootModel.Preflight.Request.Note != "note body" {
		t.Fatalf("expected note to be preserved, got %q", rootModel.Preflight.Request.Note)
	}
}
