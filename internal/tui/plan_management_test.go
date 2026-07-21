package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/executor"
)


func TestCreatePlanModelKeyboardValidationAndCancellation(t *testing.T) {
	model := NewCreatePlanModel(DefaultStyles(true, true))
	model, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(model.View(), "Objective") {
		t.Fatalf("Objective input is not discoverable:\n%s", model.View())
	}

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if model.FocusIndex != 1 {
		t.Fatalf("Tab did not advance focus: %d", model.FocusIndex)
	}
	if !strings.Contains(model.View(), "Plan ID") {
		t.Fatalf("Plan ID input is not discoverable:\n%s", model.View())
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if model.FocusIndex != 0 {
		t.Fatalf("Shift+Tab did not reverse focus: %d", model.FocusIndex)
	}
	model.ObjectiveInput.SetValue("Test Objective")
	model.FocusIndex = 5
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if _, ok := cmd().(CreatePlanMsg); !ok {
		t.Fatalf("submit did not emit CreatePlanMsg: %#v", cmd())
	}
	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := cmd().(ChangeScreenMsg); got.Screen != ScreenPlans {
		t.Fatalf("Esc returned to %v, want Plans", got.Screen)
	}
}

func TestCreatePlanModelMultiLineObjective(t *testing.T) {
	model := NewCreatePlanModel(DefaultStyles(true, true))
	model.IDInput.SetValue("multiline-plan")
	model.TitleInput.SetValue("Multi-line Plan")
	model.ObjectiveInput.SetValue("First line of objective.\nSecond line of objective.")

	model.FocusIndex = 5 // Submit button
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit command")
	}

	msg := cmd()
	createMsg, ok := msg.(CreatePlanMsg)
	if !ok {
		t.Fatalf("expected CreatePlanMsg, got %T", msg)
	}
	if createMsg.ID != "multiline-plan" {
		t.Errorf("expected ID 'multiline-plan', got %q", createMsg.ID)
	}
	if createMsg.Title != "Multi-line Plan" {
		t.Errorf("expected Title 'Multi-line Plan', got %q", createMsg.Title)
	}
	expectedObj := "First line of objective.\nSecond line of objective."
	if createMsg.Objective != expectedObj {
		t.Errorf("expected multi-line objective %q, got %q", expectedObj, createMsg.Objective)
	}
}

func TestCreatePlanModelArrowKeysOverriddenOnTextArea(t *testing.T) {
	model := NewCreatePlanModel(DefaultStyles(true, true))

	// FocusIndex 0 (Objective textarea): Down arrow moves cursor within textarea and does NOT advance FocusIndex
	model.FocusIndex = 0
	model.ObjectiveInput.SetValue("Line 1\nLine 2\nLine 3")
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if model.FocusIndex != 0 {
		t.Fatalf("expected Down on FocusIndex 0 (textarea) to keep FocusIndex 0, got %d", model.FocusIndex)
	}

	// FocusIndex 1 (Title textinput): Down arrow advances focus to 2 (ID textinput)
	model.FocusIndex = 1
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if model.FocusIndex != 2 {
		t.Fatalf("expected Down on FocusIndex 1 to advance to 2, got %d", model.FocusIndex)
	}

	// FocusIndex 2 (ID textinput): Down arrow advances focus to 3 (Submit button)
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if model.FocusIndex != 3 {
		t.Fatalf("expected Down on FocusIndex 2 to advance to 3, got %d", model.FocusIndex)
	}
}

func TestCreatePlanModelPageKeysAndMouseOnObjective(t *testing.T) {
	model := NewCreatePlanModel(DefaultStyles(true, true))
	model.FocusIndex = 0 // Objective textarea
	model.ObjectiveInput.SetHeight(2)
	model.ObjectiveInput.SetValue("L1\nL2\nL3\nL4\nL5\nL6\nL7\nL8")

	// PgDn on textarea
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if model.FocusIndex != 0 {
		t.Fatalf("expected PgDn to retain FocusIndex 0, got %d", model.FocusIndex)
	}

	// PgUp on textarea
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if model.FocusIndex != 0 {
		t.Fatalf("expected PgUp to retain FocusIndex 0, got %d", model.FocusIndex)
	}

	// Mouse wheel on textarea
	model, _ = model.Update(tea.MouseMsg{Type: tea.MouseWheelDown})
	if model.FocusIndex != 0 {
		t.Fatalf("expected MouseWheelDown to retain FocusIndex 0, got %d", model.FocusIndex)
	}
}

func TestCreatePlanModelValidationErrorRendering(t *testing.T) {
	model := NewCreatePlanModel(DefaultStyles(true, true))
	model.ErrorMsg = "Invalid plan configuration error"

	viewStr := model.View()
	if !strings.Contains(viewStr, "Invalid plan configuration error") {
		t.Fatalf("expected view to display validation error, got:\n%s", viewStr)
	}
}

func TestRootDoesNotTreatPlanFormTextAsQuit(t *testing.T) {
	root := NewRootModel(&config.Config{}, &config.State{}, "", "", true, false, true, true)
	root.ActiveScreen = ScreenCreatePlan
	updated, _ := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got := updated.(RootModel)
	if got.CreatePlan.ObjectiveInput.Value() != "q" {
		t.Fatalf("create form did not receive q as text: %q", got.CreatePlan.ObjectiveInput.Value())
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	got = updated.(RootModel)
	if got.CreatePlan.ObjectiveInput.Value() != "" {
		t.Fatalf("Backspace did not edit create input: %q", got.CreatePlan.ObjectiveInput.Value())
	}

	got.ActiveScreen = ScreenDeletePlan
	got.DeletePlan = NewDeletePlanModel(testEmptyPlan("quit-plan", "Quit Plan"), ScreenPlans, got.Styles)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got = updated.(RootModel)
	if got.DeletePlan.ConfirmInput.Value() != "q" {
		t.Fatalf("delete confirmation did not receive q as text: %q", got.DeletePlan.ConfirmInput.Value())
	}
}

func TestRootCreatePlanPersistsRefreshesAndSurvivesRestart(t *testing.T) {
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, ".cyclestone", "milestone.yml")
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(rootDir, ".cyclestone", "state.json")
	unrelated := filepath.Join(rootDir, ".cyclestone", "state-sentinel")
	if err := os.WriteFile(unrelated, []byte("unchanged"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Milestones: []config.Milestone{}}
	model := NewRootModel(cfg, &config.State{}, configPath, statePath, true, false, true, true)
	model.ActiveScreen = ScreenCreatePlan

	updated, _ := model.Update(CreatePlanMsg{ID: "release-plan", Title: "Release Plan", Objective: "Ship safely"})
	got := updated.(RootModel)
	if got.ActiveScreen != ScreenPlans || got.CreatePlan.ErrorMsg != "" {
		t.Fatalf("create did not return to Plans: screen=%v error=%q", got.ActiveScreen, got.CreatePlan.ErrorMsg)
	}
	if len(got.Plans.Planning.Plans) != 1 || len(got.Dashboard.Planning.Plans) != 1 || got.Plans.Table.SelectedRow()[0] != "release-plan" {
		t.Fatalf("current-session planning copies were not synchronized: %+v", got.Plans.Planning)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "unchanged" {
		t.Fatalf("unrelated state artifact changed: %q %v", data, err)
	}

	restarted := NewRootModel(cfg, &config.State{}, configPath, statePath, true, false, true, true)
	if restarted.Plans.Planning == nil || len(restarted.Plans.Planning.Plans) != 1 || restarted.Plans.Planning.Plans[0].ID != "release-plan" {
		t.Fatalf("saved Plan was not loaded after restart: %+v", restarted.Plans.Planning)
	}
}

func TestRootCreatePlanRejectsInvalidDuplicateAndCancelWritesNothing(t *testing.T) {
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, ".cyclestone", "milestone.yml")
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}
	model := NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	model.ActiveScreen = ScreenCreatePlan
	updated, _ := model.Update(CreatePlanMsg{ID: "", Title: "", Objective: ""})
	got := updated.(RootModel)
	if got.ActiveScreen != ScreenCreatePlan || !strings.Contains(got.CreatePlan.ErrorMsg, "id") {
		t.Fatalf("invalid form was not retained with field error: %q", got.CreatePlan.ErrorMsg)
	}
	if entries, err := os.ReadDir(filepath.Join(rootDir, ".cyclestone", "plans")); err == nil && len(entries) != 0 {
		t.Fatalf("invalid form persisted files: %+v", entries)
	}
	updated, _ = got.Update(CreatePlanMsg{ID: "../escape", Title: "Escape", Objective: "Must not escape plans directory"})
	got = updated.(RootModel)
	if !strings.Contains(got.CreatePlan.ErrorMsg, "id") {
		t.Fatalf("unsafe ID did not produce a field error: %q", got.CreatePlan.ErrorMsg)
	}
	if _, err := os.Stat(filepath.Join(rootDir, ".cyclestone", "escape.yml")); !os.IsNotExist(err) {
		t.Fatalf("unsafe ID escaped plans directory: %v", err)
	}

	updated, _ = got.Update(CreatePlanMsg{ID: "same-plan", Title: "Same", Objective: "First"})
	got = updated.(RootModel)
	got.ActiveScreen = ScreenCreatePlan
	updated, _ = got.Update(CreatePlanMsg{ID: "same-plan", Title: "Replacement", Objective: "Must fail"})
	got = updated.(RootModel)
	if got.ActiveScreen != ScreenCreatePlan || !strings.Contains(got.CreatePlan.ErrorMsg, "already exists") {
		t.Fatalf("duplicate was not rejected in form: %q", got.CreatePlan.ErrorMsg)
	}
	loaded, validation := config.LoadPlanningState(filepath.Join(rootDir, ".cyclestone", "plans"))
	if validation.HasErrors() || len(loaded.Plans) != 1 || loaded.Plans[0].Objective != "First" {
		t.Fatalf("duplicate changed persisted Plan: %+v %+v", loaded, validation)
	}

	form := NewCreatePlanModel(DefaultStyles(true, true))
	form.IDInput.SetValue("cancelled-plan")
	_, cmd := form.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := cmd().(ChangeScreenMsg); got.Screen != ScreenPlans {
		t.Fatalf("cancel did not return to Plans: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(rootDir, ".cyclestone", "plans", "cancelled-plan.yml")); !os.IsNotExist(err) {
		t.Fatalf("cancelled Plan was persisted: %v", err)
	}
}

func TestRootCreatePlanWithAIExecution(t *testing.T) {
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, ".cyclestone", "milestone.yml")
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}

	model := NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	model.ActiveScreen = ScreenCreatePlan


	msg := CreatePlanMsg{
		ID:           "ai-plan-1",
		Title:        "AI Plan 1",
		Objective:    "Generate features",
		RunnerType:   "codex",
		CreateBranch: false,
	}

	updated, _ := model.Update(msg)
	got := updated.(RootModel)

	if !got.CreatePlan.Loading {
		t.Fatalf("expected CreatePlan.Loading to be true during AI plan creation")
	}

	updated, _ = got.Update(executor.CreatePlanProgressMsg{LogLine: "Analyzing codebase..."})
	got = updated.(RootModel)
	if len(got.CreatePlan.Logs) != 1 || got.CreatePlan.Logs[0] != "Analyzing codebase..." {
		t.Fatalf("expected progress log to be recorded, got: %v", got.CreatePlan.Logs)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	plan := testEmptyPlan("ai-plan-1", "AI Plan 1")
	plan.Briefings = []config.Briefing{
		{ID: "b1", Title: "Briefing 1", Objective: "First step", Intent: "Setup", Status: "active", CompletionSignal: "Done", CreatedAt: now, CreatedBy: "test", UpdatedAt: now, UpdatedBy: "test"},

	}
	plan.BriefingOrder = []string{"b1"}

	if val, err := config.SavePlan(filepath.Join(rootDir, ".cyclestone", "plans"), plan); err != nil {
		t.Fatalf("failed to save test plan: %v (validation: %+v)", err, val)
	}


	updated, _ = got.Update(executor.CreatePlanFinishedMsg{Error: nil})
	got = updated.(RootModel)

	if got.CreatePlan.Loading {
		t.Fatalf("expected Loading to be false after completion")
	}
	if got.ActiveScreen != ScreenPlans {
		t.Fatalf("expected to navigate to ScreenPlans after finish, got: %v", got.ActiveScreen)
	}
	if got.Plans.Planning == nil || len(got.Plans.Planning.Plans) != 1 || len(got.Plans.Planning.Plans[0].Briefings) != 1 {
		t.Fatalf("expected generated plan with briefing loaded into TUI state: %+v", got.Plans.Planning)
	}
}



func TestDeletePlanConfirmationAndRootCleanup(t *testing.T) {
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, ".cyclestone", "milestone.yml")
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}
	plansDir := filepath.Join(rootDir, ".cyclestone", "plans")
	deleteMe := testEmptyPlan("delete-me", "Delete Me")
	keepMe := testEmptyPlan("keep-me", "Keep Me")
	if _, err := config.SavePlan(plansDir, deleteMe); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SavePlan(plansDir, keepMe); err != nil {
		t.Fatal(err)
	}

	confirmation := NewDeletePlanModel(deleteMe, ScreenPlanDetails, DefaultStyles(true, true))
	confirmation.ConfirmInput.SetValue("wrong")
	confirmation, cmd := confirmation.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !strings.Contains(confirmation.ErrorMsg, deleteMe.ID) {
		t.Fatalf("wrong confirmation was accepted or not explained: %q", confirmation.ErrorMsg)
	}
	confirmation.ConfirmInput.SetValue(" " + deleteMe.ID + " ")
	confirmation, cmd = confirmation.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("confirmation with surrounding whitespace was not exact")
	}
	confirmation.ConfirmInput.SetValue(deleteMe.ID)
	_, cmd = confirmation.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if msg, ok := cmd().(DeletePlanMsg); !ok || msg.Plan.ID != deleteMe.ID {
		t.Fatalf("exact confirmation did not emit delete: %#v", cmd())
	}
	_, cmd = confirmation.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if msg := cmd().(ChangeScreenMsg); msg.Screen != ScreenPlanDetails {
		t.Fatalf("cancel did not restore detail screen: %#v", msg)
	}

	model := NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	model.ActiveScreen = ScreenDeletePlan
	model.DeletePlan = confirmation
	model.PlanDetails.Plan = deleteMe
	model.BriefingDetails.Plan = deleteMe
	model.BriefingOrigin = BriefingOrigin{PlanID: deleteMe.ID, PlanRun: true}
	model.activePlanOrigin = BriefingOrigin{PlanID: deleteMe.ID, PlanRun: true}
	updated, _ := model.Update(DeletePlanMsg{Plan: deleteMe, ReturnScreen: ScreenPlanDetails})
	got := updated.(RootModel)
	if got.ActiveScreen != ScreenPlans || len(got.Plans.Planning.Plans) != 1 || got.Plans.Planning.Plans[0].ID != keepMe.ID {
		t.Fatalf("delete did not refresh surviving selection: screen=%v planning=%+v", got.ActiveScreen, got.Plans.Planning)
	}
	if got.PlanDetails.Plan.ID != "" || got.BriefingDetails.Plan.ID != "" || got.BriefingOrigin.PlanRun || got.activePlanOrigin.PlanRun {
		t.Fatalf("deleted Plan references were retained: details=%q briefing=%q origins=%+v/%+v", got.PlanDetails.Plan.ID, got.BriefingDetails.Plan.ID, got.BriefingOrigin, got.activePlanOrigin)
	}
	if _, err := os.Stat(filepath.Join(plansDir, keepMe.ID+".yml")); err != nil {
		t.Fatalf("surviving Plan was changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plansDir, deleteMe.ID+".yml")); !os.IsNotExist(err) {
		t.Fatalf("deleted Plan still exists: %v", err)
	}
}

func TestDeleteLastPlanLeavesUsableEmptyList(t *testing.T) {
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, ".cyclestone", "milestone.yml")
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}
	plan := testEmptyPlan("only-plan", "Only Plan")
	if _, err := config.SavePlan(filepath.Join(rootDir, ".cyclestone", "plans"), plan); err != nil {
		t.Fatal(err)
	}
	model := NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	model.ActiveScreen = ScreenDeletePlan
	updated, _ := model.Update(DeletePlanMsg{Plan: plan})
	got := updated.(RootModel)
	if got.ActiveScreen != ScreenPlans || len(got.Plans.Table.Rows()) != 0 || !strings.Contains(got.Plans.View(), "Milestone Plans") {
		t.Fatalf("last-Plan deletion left unusable list: screen=%v rows=%d", got.ActiveScreen, len(got.Plans.Table.Rows()))
	}
}

func TestPlanPersistenceFailuresRemainInMutationScreen(t *testing.T) {
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, ".cyclestone", "milestone.yml")
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}
	model := NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)

	originalSave := savePlanningPlan
	savePlanningPlan = func(string, config.Plan, ...config.PlanningValidationOption) (string, config.PlanningValidationResult, error) {
		return "", config.PlanningValidationResult{}, errors.New("injected save failure")
	}
	model.ActiveScreen = ScreenCreatePlan
	updated, _ := model.Update(CreatePlanMsg{ID: "failed-plan", Title: "Failed", Objective: "Do not write"})
	got := updated.(RootModel)
	savePlanningPlan = originalSave
	if got.ActiveScreen != ScreenCreatePlan || !strings.Contains(got.CreatePlan.ErrorMsg, "injected save failure") {
		t.Fatalf("save failure did not remain actionable: %q", got.CreatePlan.ErrorMsg)
	}
	if _, err := os.Stat(filepath.Join(rootDir, ".cyclestone", "plans", "failed-plan.yml")); !os.IsNotExist(err) {
		t.Fatalf("failed save created a Plan: %v", err)
	}

	plan := testEmptyPlan("delete-failure", "Delete Failure")
	if _, err := config.SavePlan(filepath.Join(rootDir, ".cyclestone", "plans"), plan); err != nil {
		t.Fatal(err)
	}
	model = NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	originalDelete := deletePlanningPlan
	deletePlanningPlan = func(string, string) error { return errors.New("injected delete failure") }
	model.ActiveScreen = ScreenDeletePlan
	updated, _ = model.Update(DeletePlanMsg{Plan: plan})
	got = updated.(RootModel)
	deletePlanningPlan = originalDelete
	if got.ActiveScreen != ScreenDeletePlan || !strings.Contains(got.DeletePlan.ErrorMsg, "injected delete failure") {
		t.Fatalf("delete failure did not retain confirmation: %q", got.DeletePlan.ErrorMsg)
	}
	if _, err := os.Stat(filepath.Join(rootDir, ".cyclestone", "plans", plan.ID+".yml")); err != nil {
		t.Fatalf("failed delete changed persisted Plan: %v", err)
	}
}

func TestPlanMutationsBlockInvalidExistingFilesAndReportReloadFailures(t *testing.T) {
	rootDir := t.TempDir()
	configPath := filepath.Join(rootDir, ".cyclestone", "milestone.yml")
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}
	plansDir := filepath.Join(rootDir, ".cyclestone", "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(plansDir, "bad.yml")
	if err := os.WriteFile(badPath, []byte("id: [broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	model := NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	model.ActiveScreen = ScreenCreatePlan
	updated, _ := model.Update(CreatePlanMsg{ID: "blocked-plan", Title: "Blocked", Objective: "Blocked by invalid file"})
	got := updated.(RootModel)
	if !strings.Contains(got.CreatePlan.ErrorMsg, "Fix existing Plan files") {
		t.Fatalf("invalid existing file did not block mutation: %q", got.CreatePlan.ErrorMsg)
	}
	if _, err := os.Stat(filepath.Join(plansDir, "blocked-plan.yml")); !os.IsNotExist(err) {
		t.Fatalf("blocked mutation wrote a Plan: %v", err)
	}
	if err := os.Remove(badPath); err != nil {
		t.Fatal(err)
	}

	originalLoad := loadPlanningState
	loadCalls := 0
	loadPlanningState = func(dir string, options ...config.PlanningValidationOption) (*config.PlanningState, config.PlanningValidationResult) {
		loadCalls++
		if loadCalls == 2 {
			return &config.PlanningState{}, config.PlanningValidationResult{Messages: []config.PlanningValidationMessage{{Severity: "error", Field: "reload", Message: "injected reload failure"}}}
		}
		return originalLoad(dir, options...)
	}
	model = NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	loadCalls = 0
	model.ActiveScreen = ScreenCreatePlan
	updated, _ = model.Update(CreatePlanMsg{ID: "saved-plan", Title: "Saved", Objective: "Persist before reload"})
	got = updated.(RootModel)
	loadPlanningState = originalLoad
	if got.ActiveScreen != ScreenCreatePlan || !strings.Contains(got.CreatePlan.ErrorMsg, "Plan was saved") {
		t.Fatalf("post-save reload failure was not reported truthfully: %q", got.CreatePlan.ErrorMsg)
	}
	if _, err := os.Stat(filepath.Join(plansDir, "saved-plan", "saved-plan.yml")); err != nil {
		t.Fatalf("successfully saved Plan disappeared after reload failure: %v", err)
	}

	deleteTarget := testEmptyPlan("reload-delete", "Reload Delete")
	if _, _, err := config.SavePlanToFolder(plansDir, deleteTarget); err != nil {
		t.Fatal(err)
	}
	model = NewRootModel(&config.Config{}, &config.State{}, configPath, "", true, false, true, true)
	loadCalls = 0
	loadPlanningState = func(dir string, options ...config.PlanningValidationOption) (*config.PlanningState, config.PlanningValidationResult) {
		loadCalls++
		if loadCalls == 2 {
			return &config.PlanningState{}, config.PlanningValidationResult{Messages: []config.PlanningValidationMessage{{Severity: "error", Field: "reload", Message: "injected reload failure"}}}
		}
		return originalLoad(dir, options...)
	}
	model.ActiveScreen = ScreenDeletePlan
	updated, _ = model.Update(DeletePlanMsg{Plan: deleteTarget})
	got = updated.(RootModel)
	loadPlanningState = originalLoad
	if got.ActiveScreen != ScreenDeletePlan || !strings.Contains(got.DeletePlan.ErrorMsg, "Plan was deleted") {
		t.Fatalf("post-delete reload failure was not reported truthfully: %q", got.DeletePlan.ErrorMsg)
	}
	if _, err := os.Stat(filepath.Join(plansDir, deleteTarget.ID+".yml")); !os.IsNotExist(err) {
		t.Fatalf("deleted Plan unexpectedly remains after reload failure: %v", err)
	}
}

func TestPlanListAndDetailsExposeCreateDeleteKeys(t *testing.T) {
	planning := &config.PlanningState{Plans: []config.Plan{testEmptyPlan("keys-plan", "Keys Plan")}}
	plans := NewPlansModel(&config.Config{}, &config.State{}, planning, DefaultStyles(true, true))
	_, cmd := plans.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if msg := cmd().(ChangeScreenMsg); msg.Screen != ScreenCreatePlan {
		t.Fatalf("c opened %v", msg.Screen)
	}
	_, cmd = plans.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if msg := cmd().(ShowDeletePlanMsg); msg.Plan.ID != "keys-plan" || msg.ReturnScreen != ScreenPlans {
		t.Fatalf("d emitted %#v", msg)
	}
	details := NewPlanDetailsModel(&config.Config{}, &config.State{}, DefaultStyles(true, true))
	details.Plan = planning.Plans[0]
	_, cmd = details.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if msg := cmd().(ShowDeletePlanMsg); msg.ReturnScreen != ScreenPlanDetails {
		t.Fatalf("detail d emitted %#v", msg)
	}
}

func TestCreatePlanModelLayoutAndHeader(t *testing.T) {
	styles := DefaultStyles(true, true)
	model := NewCreatePlanModel(styles)
	model.Width = 80
	model.Height = 24

	viewStr := model.View()
	if !strings.Contains(viewStr, "CREATE NEW PLAN") {
		t.Fatalf("expected header 'CREATE NEW PLAN' in view:\n%s", viewStr)
	}

	// Update ID field and verify header updates dynamically
	model.IDInput.SetValue("my-awesome-plan")
	viewStr = model.View()
	if !strings.Contains(viewStr, "CREATE NEW PLAN (ID: my-awesome-plan)") {
		t.Fatalf("expected dynamic header with ID in view:\n%s", viewStr)
	}

	// Test field focus updates and submit/cancel button layout
	model.FocusIndex = 5 // Submit button
	viewStr = model.View()
	if !strings.Contains(viewStr, "[ Submit ]") || !strings.Contains(viewStr, "[ Cancel ]") {
		t.Fatalf("expected styled action buttons in view:\n%s", viewStr)
	}
}

func testEmptyPlan(id, title string) config.Plan {
	now := time.Now().UTC().Format(time.RFC3339)
	return config.Plan{
		SchemaVersion: config.PlanningSchemaVersion,
		ID:            id, Title: title, Objective: "Objective", Status: "active",
		CreatedAt: now, CreatedBy: "test", UpdatedAt: now, UpdatedBy: "test",
		BriefingOrder: []string{}, Briefings: []config.Briefing{},
	}
}
