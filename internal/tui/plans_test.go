package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestPlansModel_RenderingAndNavigation(t *testing.T) {
	planning := &config.PlanningState{
		Plans: []config.Plan{
			{
				ID:     "plan-01",
				Title:  "First Test Plan",
				Status: "active",
				Briefings: []config.Briefing{
					{ID: "b1", Title: "Briefing 1", Status: "completed"},
					{ID: "b2", Title: "Briefing 2", Status: "todo"},
				},
			},
			{
				ID:     "plan-02",
				Title:  "Second Test Plan",
				Status: "draft",
				Briefings: []config.Briefing{
					{ID: "b3", Title: "Briefing 3", Status: "todo"},
				},
			},
		},
	}

	cfg := &config.Config{}
	st := &config.State{}
	styles := DefaultStyles(true, true)

	plansModel := NewPlansModel(cfg, st, planning, styles)
	plansModel.Width = 80
	plansModel.Height = 24
	plansModel, _ = plansModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := plansModel.View()
	if !strings.Contains(view, "Milestone Plans") {
		t.Errorf("expected view to contain header 'Milestone Plans', got:\n%s", view)
	}
	if !strings.Contains(view, "plan-01") || !strings.Contains(view, "First Test Plan") {
		t.Errorf("expected view to contain plan-01 data, got:\n%s", view)
	}
	if !strings.Contains(view, "1/2") {
		t.Errorf("expected briefings progress '1/2', got:\n%s", view)
	}

	// Test Enter key navigation -> ScreenPlanDetails
	_, cmd := plansModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command on Enter key")
	}
	msg := cmd()
	changeMsg, ok := msg.(ChangeScreenMsg)
	if !ok || changeMsg.Screen != ScreenPlanDetails {
		t.Fatalf("expected ChangeScreenMsg to ScreenPlanDetails, got %#v", msg)
	}
	planData, ok := changeMsg.Data.(config.Plan)
	if !ok || planData.ID != "plan-01" {
		t.Fatalf("expected plan-01 data, got %#v", changeMsg.Data)
	}

	// Test Esc key navigation -> ScreenDashboard
	_, cmdEsc := plansModel.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmdEsc == nil {
		t.Fatal("expected command on Esc key")
	}
	msgEsc := cmdEsc()
	changeMsgEsc, ok := msgEsc.(ChangeScreenMsg)
	if !ok || changeMsgEsc.Screen != ScreenDashboard {
		t.Fatalf("expected ChangeScreenMsg to ScreenDashboard, got %#v", msgEsc)
	}
}

func TestPlansModel_NarrowWidth(t *testing.T) {
	planning := &config.PlanningState{
		Plans: []config.Plan{
			{
				ID:     "plan-01",
				Title:  "Very Long Plan Title That Should Be Truncated",
				Status: "active",
				Briefings: []config.Briefing{
					{ID: "b1", Status: "completed"},
				},
			},
		},
	}
	styles := DefaultStyles(true, true)
	plansModel := NewPlansModel(nil, nil, planning, styles)
	plansModel, _ = plansModel.Update(tea.WindowSizeMsg{Width: 40, Height: 15})

	view := plansModel.View()
	if !strings.Contains(view, "plan-01") {
		t.Errorf("expected narrow view to contain plan-01, got:\n%s", view)
	}
}

func TestPlanDetailsModel_RenderingAndNavigation(t *testing.T) {
	plan := config.Plan{
		ID:        "plan-alpha",
		Title:     "Alpha Architecture Plan",
		Status:    "active",
		Objective: "Build core modules and APIs",
		Briefings: []config.Briefing{
			{
				ID:          "b1",
				Title:       "Core Module",
				Status:      "completed",
				MilestoneID: "ms-001",
			},
			{
				ID:          "b2",
				Title:       "API Module",
				Status:      "in_progress",
				DependsOn:   []string{"b1"},
				MilestoneID: "ms-002",
			},
			{
				ID:          "b3",
				Title:       "Unlinked Module",
				Status:      "todo",
				MilestoneID: "",
			},
			{
				ID:          "b4",
				Title:       "Missing Link Module",
				Status:      "todo",
				MilestoneID: "ms-missing",
			},
		},
	}

	cfg := &config.Config{
		Milestones: []config.Milestone{
			{ID: "ms-001", Title: "Core MS", Status: "Done"},
			{ID: "ms-002", Title: "API MS", Status: "In Progress"},
		},
	}
	styles := DefaultStyles(true, true)
	model := NewPlanDetailsModel(cfg, nil, styles)
	model.Plan = plan
	model, _ = model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})

	view := model.View()
	if !strings.Contains(view, "Plan: plan-alpha - Alpha Architecture Plan") {
		t.Errorf("expected plan title in view, got:\n%s", view)
	}
	if !strings.Contains(view, "Plan Briefings") {
		t.Errorf("expected 'Plan Briefings' section, got:\n%s", view)
	}
	if !strings.Contains(view, "[linked: ms-001]") {
		t.Errorf("expected linked tag for ms-001, got:\n%s", view)
	}
	if !strings.Contains(view, "[unlinked]") {
		t.Errorf("expected unlinked tag for b3, got:\n%s", view)
	}
	if !strings.Contains(view, "[missing: ms-missing]") {
		t.Errorf("expected missing tag for b4, got:\n%s", view)
	}

	// Test Enter key navigation -> ScreenBriefingDetails
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command on Enter key")
	}
	msg := cmd()
	changeMsg, ok := msg.(ChangeScreenMsg)
	if !ok || changeMsg.Screen != ScreenBriefingDetails {
		t.Fatalf("expected ChangeScreenMsg to ScreenBriefingDetails, got %#v", msg)
	}
	bData, ok := changeMsg.Data.(BriefingDetailData)
	if !ok || bData.Briefing.ID != "b1" || bData.Plan.ID != "plan-alpha" {
		t.Fatalf("expected b1 briefing data, got %#v", changeMsg.Data)
	}

	// Test Esc key navigation -> ScreenPlans
	_, cmdEsc := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmdEsc == nil {
		t.Fatal("expected command on Esc key")
	}
	msgEsc := cmdEsc()
	changeMsgEsc, ok := msgEsc.(ChangeScreenMsg)
	if !ok || changeMsgEsc.Screen != ScreenPlans {
		t.Fatalf("expected ChangeScreenMsg to ScreenPlans, got %#v", msgEsc)
	}
}

func TestBriefingDetailsModel_RenderingAndScrolling(t *testing.T) {
	plan := config.Plan{
		ID:    "plan-alpha",
		Title: "Alpha Plan",
	}
	briefing := config.Briefing{
		ID:               "b2",
		Title:            "API Module",
		Status:           "in_progress",
		Objective:        "Build HTTP APIs for frontend",
		Intent:           "ExposeRESTEndpoints",
		CompletionSignal: "All endpoints passing tests",
		Constraints:      []string{"No external cloud services", "Use standard library"},
		DependsOn:        []string{"b1"},
		MilestoneID:      "ms-002",
	}
	linkedMS := &config.Milestone{
		ID:     "ms-002",
		Title:  "API Milestone",
		Status: "In Progress",
		Cycles: 2,
		Goal:   "Implement REST API endpoints",
	}
	history := []config.MilestoneCycleLog{
		{
			CycleNumber: 1,
			Status:      "approved",
			Duration:    "2m00s",
			UserNote:    "Initial API draft",
			Timestamp:   time.Now(),
		},
	}

	styles := DefaultStyles(true, true)
	model := NewBriefingDetailsModel(styles)
	model.Plan = plan
	model.Briefing = briefing
	model.LinkedMS = linkedMS
	model.History = history
	model, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	view := model.View()
	if !strings.Contains(view, "Briefing: b2 - API Module") {
		t.Errorf("expected briefing title, got:\n%s", view)
	}
	if !strings.Contains(view, "Plan: plan-alpha") {
		t.Errorf("expected plan context, got:\n%s", view)
	}
	if !strings.Contains(view, "Build HTTP APIs for frontend") {
		t.Errorf("expected objective text, got:\n%s", view)
	}
	if !strings.Contains(view, "ExposeRESTEndpoints") {
		t.Errorf("expected intent text, got:\n%s", view)
	}
	if !strings.Contains(view, "No external cloud services") {
		t.Errorf("expected constraint text, got:\n%s", view)
	}
	if !strings.Contains(view, "Linked Milestone Details") {
		t.Errorf("expected linked milestone section, got:\n%s", view)
	}
	if !strings.Contains(view, "Cycle 1: [approved]") {
		t.Errorf("expected cycle log text, got:\n%s", view)
	}

	// Test Esc key -> ScreenPlanDetails
	_, cmdEsc := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmdEsc == nil {
		t.Fatal("expected command on Esc key")
	}
	msgEsc := cmdEsc()
	changeMsgEsc, ok := msgEsc.(ChangeScreenMsg)
	if !ok || changeMsgEsc.Screen != ScreenPlanDetails {
		t.Fatalf("expected ChangeScreenMsg to ScreenPlanDetails, got %#v", msgEsc)
	}
}

func TestBriefingDetailsModelCreateMilestoneActionSnapshotsCompleteContext(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "vscode")
	styles := DefaultStyles(true, true)
	model := NewBriefingDetailsModel(styles)
	model.Plan = config.Plan{ID: "plan-alpha", Title: "Alpha Plan"}
	model.Briefing = config.Briefing{
		ID:               "briefing-api",
		Title:            "API Module",
		Status:           "active",
		Objective:        "Build HTTP APIs for the frontend.",
		Intent:           "Expose stable endpoints.",
		CompletionSignal: "All endpoint tests pass.",
		Constraints:      []string{"Use the standard library", "No cloud dependency"},
		DependsOn:        []string{"briefing-core"},
		MilestoneID:      "ms-existing",
	}
	model, _ = model.Update(tea.WindowSizeMsg{Width: 38, Height: 18})

	view := stripANSI(model.View())
	if !strings.Contains(view, "Create Milestone") {
		t.Fatalf("expected narrow detail help to advertise Create Milestone, got:\n%s", view)
	}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("expected Create Milestone action command")
	}
	change, ok := cmd().(ChangeScreenMsg)
	if !ok || change.Screen != ScreenCreateMilestone {
		t.Fatalf("expected create-milestone navigation, got %#v", change)
	}
	data, ok := change.Data.(CreateMilestoneFromBriefingData)
	if !ok {
		t.Fatalf("expected typed Briefing payload, got %#v", change.Data)
	}
	for _, want := range []string{
		"Plan: plan-alpha - Alpha Plan",
		"Briefing ID: briefing-api",
		"Title: API Module",
		"Status: active",
		"Objective: Build HTTP APIs for the frontend.",
		"Intent: Expose stable endpoints.",
		"Completion Signal: All endpoint tests pass.",
		"Constraints: - Use the standard library",
		"No cloud dependency",
		"Dependencies: - briefing-core",
		"Milestone Link: ms-existing",
	} {
		if !strings.Contains(data.ContextText, want) {
			t.Errorf("expected snapshot context to contain %q, got:\n%s", want, data.ContextText)
		}
	}

	model.Briefing.Constraints[0] = "mutated after navigation"
	if data.Briefing.Constraints[0] != "Use the standard library" {
		t.Fatalf("expected immutable Briefing slice snapshot, got %#v", data.Briefing.Constraints)
	}
}

func TestMultiLevelNavigationFlow(t *testing.T) {
	cfg := &config.Config{
		Milestones: []config.Milestone{
			{ID: "ms-001", Title: "Core Module", Status: "Done", Cycles: 1},
		},
	}
	st := &config.State{
		MilestoneStatuses: map[string]string{"ms-001": "Done"},
		MilestoneCycles:   map[string]int{"ms-001": 1},
	}
	planning := &config.PlanningState{
		Plans: []config.Plan{
			{
				ID:     "plan-01",
				Title:  "Test Plan 1",
				Status: "active",
				Briefings: []config.Briefing{
					{ID: "b1", Title: "Briefing 1", Status: "completed", MilestoneID: "ms-001"},
				},
			},
		},
	}

	root := NewRootModel(cfg, st, "", "", true, false, true, true)
	root.Plans.Planning = planning
	root.ActiveScreen = ScreenDashboard

	// Propagate window size
	mWin, _ := root.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	root = mWin.(RootModel)

	// Step 1: Dashboard -> press "p" -> ScreenPlans
	var m tea.Model
	var cmd tea.Cmd
	m, cmd = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	root = m.(RootModel)
	if cmd != nil {
		msg := cmd()
		m, _ = root.Update(msg)
		root = m.(RootModel)
	}
	if root.ActiveScreen != ScreenPlans {
		t.Fatalf("expected ScreenPlans after pressing 'p', got %v", root.ActiveScreen)
	}

	// Step 2: ScreenPlans -> press Enter -> ScreenPlanDetails
	m, cmd = root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	root = m.(RootModel)
	if cmd != nil {
		msg := cmd()
		m, _ = root.Update(msg)
		root = m.(RootModel)
	}
	if root.ActiveScreen != ScreenPlanDetails {
		t.Fatalf("expected ScreenPlanDetails after pressing Enter on plan row, got %v", root.ActiveScreen)
	}

	// Step 3: ScreenPlanDetails -> press Enter -> ScreenBriefingDetails
	m, cmd = root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	root = m.(RootModel)
	if cmd != nil {
		msg := cmd()
		m, _ = root.Update(msg)
		root = m.(RootModel)
	}
	if root.ActiveScreen != ScreenBriefingDetails {
		t.Fatalf("expected ScreenBriefingDetails after pressing Enter on briefing row, got %v", root.ActiveScreen)
	}

	// Step 4: ScreenBriefingDetails -> press Esc -> ScreenPlanDetails
	m, cmd = root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	root = m.(RootModel)
	if cmd != nil {
		msg := cmd()
		m, _ = root.Update(msg)
		root = m.(RootModel)
	}
	if root.ActiveScreen != ScreenPlanDetails {
		t.Fatalf("expected ScreenPlanDetails after Esc from BriefingDetails, got %v", root.ActiveScreen)
	}

	// Step 5: ScreenPlanDetails -> press Esc -> ScreenPlans
	m, cmd = root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	root = m.(RootModel)
	if cmd != nil {
		msg := cmd()
		m, _ = root.Update(msg)
		root = m.(RootModel)
	}
	if root.ActiveScreen != ScreenPlans {
		t.Fatalf("expected ScreenPlans after Esc from PlanDetails, got %v", root.ActiveScreen)
	}

	// Step 6: ScreenPlans -> press Esc -> ScreenDashboard
	m, cmd = root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	root = m.(RootModel)
	if cmd != nil {
		msg := cmd()
		m, _ = root.Update(msg)
		root = m.(RootModel)
	}
	if root.ActiveScreen != ScreenDashboard {
		t.Fatalf("expected ScreenDashboard after Esc from ScreenPlans, got %v", root.ActiveScreen)
	}
}
