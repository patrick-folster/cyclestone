package tui

import (
	"context"
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

func TestParseMilestoneFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "milestones-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name          string
		filename      string
		content       string
		expectedTitle string
		expectedSlug  string
	}{
		{
			name:     "Standard dash separator",
			filename: "0007-my-optimized-slug.md",
			content: `# Milestone Spec: 0007-my-optimized-slug - My Optimized Title

## Goal
Optimize stuff.`,
			expectedTitle: "My Optimized Title",
			expectedSlug:  "my-optimized-slug",
		},
		{
			name:     "Colon separator",
			filename: "0007-my-optimized-slug.md",
			content: `# Milestone Spec: 0007-my-optimized-slug: My Optimized Title

## Goal
Optimize stuff.`,
			expectedTitle: "My Optimized Title",
			expectedSlug:  "my-optimized-slug",
		},
		{
			name:     "Pipe separator",
			filename: "0007-my-optimized-slug.md",
			content: `# Milestone Spec: 0007-my-optimized-slug | My Optimized Title

## Goal
Optimize stuff.`,
			expectedTitle: "My Optimized Title",
			expectedSlug:  "my-optimized-slug",
		},
		{
			name:     "Slight spacing variations",
			filename: "0007-my-optimized-slug.md",
			content: `  
# Milestone Spec: 0007-my-optimized-slug    -    My Optimized Title

## Goal
Optimize stuff.`,
			expectedTitle: "My Optimized Title",
			expectedSlug:  "my-optimized-slug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write the test file
			err = os.WriteFile(filepath.Join(tempDir, tt.filename), []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("failed to write file: %v", err)
			}

			// Scan the directory
			files, err := os.ReadDir(tempDir)
			if err != nil {
				t.Fatalf("failed to read dir: %v", err)
			}

			prefix := "0007-"
			var matchedFile string
			for _, f := range files {
				if !f.IsDir() && strings.HasPrefix(f.Name(), prefix) && strings.HasSuffix(f.Name(), ".md") {
					matchedFile = f.Name()
					break
				}
			}

			if matchedFile != tt.filename {
				t.Fatalf("expected matched file %q, got %q", tt.filename, matchedFile)
			}

			// Extract slug
			slugPart := strings.TrimPrefix(matchedFile, prefix)
			slugPart = strings.TrimSuffix(slugPart, ".md")
			if slugPart != tt.expectedSlug {
				t.Errorf("expected slug %q, got %q", tt.expectedSlug, slugPart)
			}

			// Read file and parse header
			filePath := filepath.Join(tempDir, matchedFile)
			contentBytes, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read file: %v", err)
			}

			lines := strings.Split(string(contentBytes), "\n")
			var firstLine string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					firstLine = trimmed
					break
				}
			}

			expectedID := "0007-" + slugPart
			idx := strings.Index(firstLine, expectedID)
			if idx == -1 {
				t.Fatalf("expected ID %q not found in first line %q", expectedID, firstLine)
			}

			titlePart := firstLine[idx+len(expectedID):]
			titlePart = strings.TrimSpace(titlePart)
			titlePart = strings.TrimLeft(titlePart, "-:| ")
			titlePart = strings.TrimSpace(titlePart)

			if titlePart != tt.expectedTitle {
				t.Errorf("expected title %q, got %q", tt.expectedTitle, titlePart)
			}

			// Clean up file for next test run
			_ = os.Remove(filepath.Join(tempDir, tt.filename))
		})
	}
}

func TestGenerateNextIDUsesAuthorPrefixedMilestoneID(t *testing.T) {
	// Isolate the global settings directory so LoadMergedSettings resolves a
	// deterministic author_prefix instead of reading the real user config.
	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("USERPROFILE", oldUserProfile)
	})
	root := t.TempDir()
	_ = os.Setenv("HOME", root)
	_ = os.Setenv("USERPROFILE", root)
	globalCfgDir := filepath.Join(root, ".config", "cyclestone")
	if err := os.MkdirAll(globalCfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalCfgDir, "settings.yml"), []byte("author_prefix: pf\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "empty config starts at ms-pf-0001",
			cfg:  &config.Config{},
			want: "ms-pf-0001",
		},
		{
			name: "increments author-prefixed milestone IDs",
			cfg: &config.Config{Milestones: []config.Milestone{
				{ID: "ms-pf-0001-project-setup"},
				{ID: "ms-pf-0009-release-readiness"},
			}},
			want: "ms-pf-0010",
		},
		{
			name: "ignores legacy non-prefixed IDs",
			cfg: &config.Config{Milestones: []config.Milestone{
				{ID: "MS-1"},
				{ID: "0002-current-convention"},
			}},
			want: "ms-pf-0001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateNextID(tt.cfg); got != tt.want {
				t.Fatalf("expected next ID %q, got %q", tt.want, got)
			}
		})
	}
}

func TestRenderCommandHelp(t *testing.T) {
	styles := DefaultStyles(true, true) // disable bold and rounded borders
	commands := []string{
		"a Alpha",
		"b Beta",
		"c Gamma",
	}

	// First let's render with a large width, it should fit on one line.
	resOneLine := renderCommandHelp(styles, commands, 100)
	if strings.Contains(resOneLine, "\n") {
		t.Errorf("expected no newline with large width, got: %q", resOneLine)
	}

	// Now let's render with a very small width (e.g. 10), it should wrap every element to its own line.
	resWrapped := renderCommandHelp(styles, commands, 10)
	lines := strings.Split(resWrapped, "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 wrapped lines, got %d: %q", len(lines), resWrapped)
	}
}

func TestMissingConfigRoutesToSetupWizardAndConfirmCreatesProject(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	state := &config.State{
		MilestoneStatuses:        make(map[string]string),
		MilestoneCycles:          make(map[string]int),
		MilestoneRecommendations: make(map[string]int),
		History:                  make(map[string][]config.MilestoneCycleLog),
	}
	model := NewRootModel(&config.Config{Milestones: []config.Milestone{}}, state, configPath, statePath, true, false, true, true)
	model.MissingConfig = true
	model.ActiveScreen = ScreenSetup
	model.Width = 80
	model.Height = 24
	model.Setup.Width = 80
	model.Setup.Height = 24
	model.Setup.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.Setup.RunnerInherit = false
	model.Setup.Runner = "codex"
	model.Setup.SafetyInherit = false
	model.Setup.BranchesInherit = false
	model.Setup.CreateFirst = true
	model.Setup.MilestoneIDInput.SetValue("0001-first-run")
	model.Setup.MilestoneTitleInput.SetValue("First Run")
	model.Setup.MilestoneGoalInput.SetValue("Create the first setup milestone.")
	model.Setup.MilestoneCriteria.SetValue("Config exists\nDashboard shows milestone")
	model.Setup.FocusIndex = setupFieldConfirm

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected setup completion command")
	}
	updated, _ = updated.Update(cmd())
	got := updated.(RootModel)
	if got.MissingConfig {
		t.Fatal("expected setup wizard to close")
	}
	if got.InitConfigError != "" {
		t.Fatalf("unexpected init error: %s", got.InitConfigError)
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones")); err != nil {
		t.Fatalf("milestones directory was not created: %v", err)
	}
	// The first milestone is saved in folder-per-item layout: milestones/0001-first-run/0001-specification.md
	specDir := filepath.Join(root, ".cyclestone", "milestones", "0001-first-run")
	specData, err := os.ReadFile(filepath.Join(specDir, "0001-specification.md"))
	if err != nil {
		// Try to find the spec by scanning the milestones directory.
		entries, dirErr := os.ReadDir(filepath.Join(root, ".cyclestone", "milestones"))
		if dirErr != nil {
			t.Fatalf("failed to read milestones dir: %v", dirErr)
		}
		found := false
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "0001") {
				specData, err = os.ReadFile(filepath.Join(root, ".cyclestone", "milestones", e.Name(), "0001-specification.md"))
				if err == nil {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("first milestone spec was not created: %v", err)
		}
	}
	specText := string(specData)
	for _, want := range []string{"# Milestone Spec: 0001-first-run - First Run", "Create the first setup milestone.", "- [ ] Config exists", "- [ ] Dashboard shows milestone"} {
		if !strings.Contains(specText, want) {
			t.Fatalf("expected first milestone spec to contain %q, got:\n%s", want, specText)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "settings.yml")); err != nil {
		t.Fatalf("settings.yml was not created: %v", err)
	}
	settingsData, err := os.ReadFile(filepath.Join(root, ".cyclestone", "settings.yml"))
	if err != nil {
		t.Fatalf("failed to read settings.yml: %v", err)
	}
	settingsText := string(settingsData)
	for _, want := range []string{"default_llm: codex", "default_mode: sandbox", "auto_git_branch: true", "create_milestone_branch: true"} {
		if !strings.Contains(settingsText, want) {
			t.Fatalf("expected settings.yml to contain %q, got:\n%s", want, settingsText)
		}
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state.json was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "wrong", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected state file outside constructor path: %v", err)
	}
	if len(got.Config.Milestones) != 1 || got.Config.Milestones[0].ID != "0001-first-run" {
		t.Fatalf("expected first milestone in config, got %#v", got.Config.Milestones)
	}
	if got.State.ActiveMilestoneID != "0001-first-run" {
		t.Fatalf("expected active first milestone, got %q", got.State.ActiveMilestoneID)
	}
	if got.ActiveScreen != ScreenDashboard {
		t.Fatalf("expected dashboard after setup, got %v", got.ActiveScreen)
	}
}

func TestInitDetailsHonorsRootNoBranchChange(t *testing.T) {
	state := &config.State{
		MilestoneStatuses:        map[string]string{},
		MilestoneCycles:          map[string]int{},
		MilestoneRecommendations: map[string]int{},
		History:                  map[string][]config.MilestoneCycleLog{},
	}
	model := NewRootModel(&config.Config{Milestones: []config.Milestone{}}, state, ".cyclestone/milestone.yml", ".cyclestone/state.json", true, false, true, true)

	model.initDetailsScreen(config.Milestone{ID: "0015-cycle-preflight-review", Title: "Preflight"})
	if model.Details.BranchChange {
		t.Fatal("expected details branch changes to be disabled when root NoBranchChange is true")
	}
}

func TestSetupWizardCancelLeavesNoPartialFiles(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	state := &config.State{
		MilestoneStatuses:        make(map[string]string),
		MilestoneCycles:          make(map[string]int),
		MilestoneRecommendations: make(map[string]int),
		History:                  make(map[string][]config.MilestoneCycleLog),
	}
	model := NewRootModel(&config.Config{Milestones: []config.Milestone{}}, state, configPath, statePath, true, false, true, true)
	model.MissingConfig = true
	model.ActiveScreen = ScreenSetup

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected quit command after cancelling setup")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("milestone.yml was created but should not have been")
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state.json was created but should not have been")
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "settings.yml")); !os.IsNotExist(err) {
		t.Fatalf("settings.yml was created but should not have been")
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones")); !os.IsNotExist(err) {
		t.Fatalf("milestones directory was created but should not have been")
	}
}

func TestInitialWindowSizeCmdUsesTerminalSize(t *testing.T) {
	oldTerminalSize := terminalSize
	terminalSize = func() (int, int, error) {
		return 132, 43, nil
	}
	defer func() { terminalSize = oldTerminalSize }()

	msg, ok := initialWindowSizeCmd()().(tea.WindowSizeMsg)
	if !ok {
		t.Fatalf("expected WindowSizeMsg, got %#v", msg)
	}
	if msg.Width != 132 || msg.Height != 43 {
		t.Fatalf("expected 132x43 initial size, got %dx%d", msg.Width, msg.Height)
	}
}

func TestInitialWindowSizeCmdFallsBackWhenTerminalSizeFails(t *testing.T) {
	oldTerminalSize := terminalSize
	terminalSize = func() (int, int, error) {
		return 0, 0, errors.New("no tty")
	}
	defer func() { terminalSize = oldTerminalSize }()

	msg, ok := initialWindowSizeCmd()().(tea.WindowSizeMsg)
	if !ok {
		t.Fatalf("expected WindowSizeMsg, got %#v", msg)
	}
	if msg.Width != defaultTerminalWidth || msg.Height != defaultTerminalHeight {
		t.Fatalf("expected fallback %dx%d, got %dx%d", defaultTerminalWidth, defaultTerminalHeight, msg.Width, msg.Height)
	}
}

func TestRootModelInitReturnsInitialWindowSizeCommand(t *testing.T) {
	oldTerminalSize := terminalSize
	terminalSize = func() (int, int, error) {
		return 101, 31, nil
	}
	defer func() { terminalSize = oldTerminalSize }()

	state := &config.State{
		MilestoneStatuses:        make(map[string]string),
		MilestoneCycles:          make(map[string]int),
		MilestoneRecommendations: make(map[string]int),
		History:                  make(map[string][]config.MilestoneCycleLog),
	}
	model := NewRootModel(&config.Config{Milestones: []config.Milestone{}}, state, ".cyclestone/milestone.yml", ".cyclestone/state.json", true, false, true, true)

	cmd := model.Init()
	if cmd == nil {
		t.Fatal("expected init command")
	}

	msg := cmd()
	if sizeMsg, ok := msg.(tea.WindowSizeMsg); ok {
		if sizeMsg.Width != 101 || sizeMsg.Height != 31 {
			t.Fatalf("expected 101x31 initial size, got %dx%d", sizeMsg.Width, sizeMsg.Height)
		}
		return
	}

	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected WindowSizeMsg or BatchMsg, got %#v", msg)
	}
	for _, batchCmd := range batch {
		if sizeMsg, ok := batchCmd().(tea.WindowSizeMsg); ok {
			if sizeMsg.Width != 101 || sizeMsg.Height != 31 {
				t.Fatalf("expected 101x31 initial size, got %dx%d", sizeMsg.Width, sizeMsg.Height)
			}
			return
		}
	}
	t.Fatal("expected init batch to include WindowSizeMsg command")
}

func TestDashboardDeleteKeybinding(t *testing.T) {
	styles := DefaultStyles(true, true)
	cfg := &config.Config{
		Milestones: []config.Milestone{
			{ID: "MS-1", Title: "Milestone 1"},
		},
	}
	state := &config.State{
		MilestoneStatuses:        map[string]string{"MS-1": "Todo"},
		MilestoneCycles:          map[string]int{"MS-1": 1},
		MilestoneRecommendations: make(map[string]int),
		History:                  make(map[string][]config.MilestoneCycleLog),
	}
	m := NewDashboardModel(cfg, state, styles)
	m.Width = 80
	m.Height = 24

	// Pressing "d" on dashboard triggers a cmd returning ShowDeleteMilestoneMsg
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("expected command to be returned from dashboard 'd' keypress")
	}

	msg := cmd()
	showMsg, ok := msg.(ShowDeleteMilestoneMsg)
	if !ok {
		t.Fatalf("expected ShowDeleteMilestoneMsg, got %#v", msg)
	}
	if showMsg.Milestone.ID != "MS-1" {
		t.Errorf("expected ShowDeleteMilestoneMsg for MS-1, got milestone %s", showMsg.Milestone.ID)
	}

	// Verify RootModel handles ShowDeleteMilestoneMsg correctly
	root := NewRootModel(cfg, state, "config.yml", "state.json", true, false, true, true)
	root.Width = 80
	root.Height = 24
	root.ActiveScreen = ScreenDashboard

	updatedRoot, rootCmd := root.Update(showMsg)
	if rootCmd != nil {
		t.Fatal("expected no command from ShowDeleteMilestoneMsg handling in RootModel")
	}

	gotRoot := updatedRoot.(RootModel)
	if gotRoot.ActiveScreen != ScreenDetails {
		t.Errorf("expected active screen to transition to ScreenDetails, got %v", gotRoot.ActiveScreen)
	}
	if !gotRoot.Details.ConfirmDeleteMilestone {
		t.Error("expected Details model to have ConfirmDeleteMilestone set to true")
	}
}

func TestDefaultStylesVSCodeFallback(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "vscode")

	styles := DefaultStyles(true, true)
	if styles.GlyphPointer != ">" {
		t.Errorf("expected GlyphPointer to be '>' in VS Code, got %q", styles.GlyphPointer)
	}
	if styles.GlyphDiamond != "*" {
		t.Errorf("expected GlyphDiamond to be '*' in VS Code, got %q", styles.GlyphDiamond)
	}
}

func TestRootModelRoutesBriefingSnapshotIntoFreshCreateMilestoneModel(t *testing.T) {
	cfg := &config.Config{}
	state := &config.State{
		MilestoneStatuses:        make(map[string]string),
		MilestoneCycles:          make(map[string]int),
		MilestoneRecommendations: make(map[string]int),
		History:                  make(map[string][]config.MilestoneCycleLog),
	}
	model := NewRootModel(cfg, state, "config.yml", "state.json", true, false, true, true)
	model.Width = 60
	model.Height = 24
	data := newCreateMilestoneFromBriefingData(
		config.Plan{ID: "plan-one", Title: "One"},
		config.Briefing{ID: "briefing-one", Title: "First", Objective: "Ship it", Status: "active"},
	)

	updated, _ := model.Update(ChangeScreenMsg{Screen: ScreenCreateMilestone, Data: data})
	got := updated.(RootModel)
	if got.ActiveScreen != ScreenCreateMilestone || got.CreateMilestone.Mode != ModeCreateMilestone {
		t.Fatalf("expected create-milestone mode, got screen=%v mode=%v", got.ActiveScreen, got.CreateMilestone.Mode)
	}
	if got.CreateMilestone.BriefingContext != data.ContextText {
		t.Fatalf("expected exact immutable context transfer, got %q", got.CreateMilestone.BriefingContext)
	}
	if got.CreateMilestone.GoalInput.Value() != "" || got.CreateMilestone.TitleInput.Value() != "" {
		t.Fatal("Briefing entry must not prefill existing editable inputs")
	}

	updated, _ = got.Update(ChangeScreenMsg{Screen: ScreenCreateMilestone})
	ordinary := updated.(RootModel)
	if ordinary.CreateMilestone.BriefingContext != "" || ordinary.CreateMilestone.Mode != ModeCreateMilestone {
		t.Fatalf("ordinary entry retained Briefing state: %+v", ordinary.CreateMilestone)
	}
}

func TestWindowSizeSafetyChecks(t *testing.T) {
	cfg := &config.Config{}
	state := &config.State{
		MilestoneStatuses:        make(map[string]string),
		MilestoneCycles:          make(map[string]int),
		MilestoneRecommendations: make(map[string]int),
		History:                  make(map[string][]config.MilestoneCycleLog),
	}
	model := NewRootModel(cfg, state, "config.yml", "state.json", true, false, true, true)
	model.Width = 80
	model.Height = 24

	// Send negative window size - should be ignored and preserve original sizes
	updated, _ := model.Update(tea.WindowSizeMsg{Width: -10, Height: 0})
	got := updated.(RootModel)
	if got.Width != 80 || got.Height != 24 {
		t.Errorf("expected negative WindowSizeMsg to be ignored, got %dx%d", got.Width, got.Height)
	}

	// Send 0 size - should be ignored
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 0, Height: 24})
	got = updated.(RootModel)
	if got.Width != 80 || got.Height != 24 {
		t.Errorf("expected zero width WindowSizeMsg to be ignored, got %dx%d", got.Width, got.Height)
	}
}

func TestBriefingExecutionApprovedCompletesOnlyOriginatingBriefing(t *testing.T) {
	root, model := writeBriefingExecutionTUIFixture(t)
	model.BriefingOrigin = BriefingOrigin{PlanID: "plan-one", BriefingID: "selected"}

	model.finishBriefingExecution(executor.CycleFinishedMsg{MilestoneID: "linked-milestone", Status: "approved"})

	planning, validation := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"linked-milestone"}))
	if validation.HasErrors() || len(planning.Plans) != 1 {
		t.Fatalf("failed to reload planning state: %+v", validation)
	}
	selected := planning.Plans[0].Briefings[0]
	other := planning.Plans[0].Briefings[1]
	if selected.Status != "completed" || selected.UpdatedBy != "briefing-executor" {
		t.Fatalf("expected selected Briefing completed, got %+v", selected)
	}
	if other.Status != "active" {
		t.Fatalf("expected other ready Briefing to remain active, got %+v", other)
	}
	if model.BriefingOrigin != (BriefingOrigin{}) {
		t.Fatalf("expected origin context cleared, got %+v", model.BriefingOrigin)
	}
}

func TestBriefingStartUsesExistingCycleEngineWithNormalMilestoneOptions(t *testing.T) {
	_, model := writeBriefingExecutionTUIFixture(t)
	type call struct {
		milestone config.Milestone
		opts      executor.RunOptions
	}
	calls := make(chan call, 1)
	original := executeCycle
	executeCycle = func(_ context.Context, milestone config.Milestone, _ []config.Agent, opts executor.RunOptions, _ *config.State, _ chan tea.Msg) {
		calls <- call{milestone: milestone, opts: opts}
	}
	t.Cleanup(func() { executeCycle = original })

	request := StartCycleMsg{
		Milestone:      config.Milestone{ID: "linked-milestone", Title: "Linked", SpecPath: "milestones/linked-milestone.md"},
		NoBranchChange: true,
		BriefingOrigin: BriefingOrigin{PlanID: "plan-one", BriefingID: "selected"},
	}
	updated, _ := model.Update(request)
	gotModel := updated.(RootModel)
	if gotModel.ActiveScreen != ScreenRunner || gotModel.BriefingOrigin != request.BriefingOrigin {
		t.Fatalf("expected ordinary runner with one planning origin, screen=%v origin=%+v", gotModel.ActiveScreen, gotModel.BriefingOrigin)
	}
	select {
	case got := <-calls:
		if got.milestone.ID != request.Milestone.ID || got.milestone.SpecPath != request.Milestone.SpecPath || got.opts.ConfigPath != model.ConfigPath || got.opts.StatePath != model.StatePath || !got.opts.NoBranchChange {
			t.Fatalf("unexpected ExecuteCycle handoff: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("existing ExecuteCycle seam was not invoked")
	}
}

func TestQueuedBriefingCycleRoutesResolvedSafetyModeThroughPreflight(t *testing.T) {
	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("USERPROFILE", oldUserProfile)
	})

	oldRunnerCheck := checkRunnerAvailable
	checkRunnerAvailable = func(string) (bool, string) { return true, "available in test" }
	t.Cleanup(func() { checkRunnerAvailable = oldRunnerCheck })

	oldExecuteCycle := executeCycle
	t.Cleanup(func() { executeCycle = oldExecuteCycle })

	for _, tc := range []struct {
		name             string
		unrestricted     bool
		settingsMode     string
		wantMode         string
		wantUnrestricted bool
	}{
		{name: "CLI unrestricted overrides sandbox settings", unrestricted: true, settingsMode: "sandbox", wantMode: "unrestricted", wantUnrestricted: true},
		{name: "CLI sandbox overrides unrestricted settings", unrestricted: false, settingsMode: "unrestricted", wantMode: "sandbox", wantUnrestricted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Setenv("HOME", root); err != nil {
				t.Fatal(err)
			}
			if err := os.Setenv("USERPROFILE", root); err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(root); err != nil {
				t.Fatal(err)
			}
			cyclestoneDir := filepath.Join(root, ".cyclestone")
			if err := os.MkdirAll(cyclestoneDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cyclestoneDir, "settings.yml"), []byte("default_llm: codex\ndefault_mode: "+tc.settingsMode+"\n"), 0644); err != nil {
				t.Fatal(err)
			}

			configPath := filepath.Join(cyclestoneDir, "milestone.yml")
			statePath := filepath.Join(cyclestoneDir, "state.json")
			milestone := config.Milestone{ID: "linked-milestone", Title: "Linked", SpecPath: "milestones/linked-milestone.md"}
			state := &config.State{
				MilestoneStatuses:        map[string]string{},
				MilestoneCycles:          map[string]int{},
				MilestoneRecommendations: map[string]int{},
				History:                  map[string][]config.MilestoneCycleLog{},
			}
			origin := BriefingOrigin{PlanID: "plan-one", BriefingID: "selected"}
			model := NewRootModel(&config.Config{Milestones: []config.Milestone{milestone}}, state, configPath, statePath, true, tc.unrestricted, true, true)
			model.QueueBriefingCycle(milestone, origin)

			batch, ok := model.Init()().(tea.BatchMsg)
			if !ok {
				t.Fatalf("expected Init to return tea.BatchMsg")
			}
			var route ChangeScreenMsg
			for _, cmd := range batch {
				if msg, ok := cmd().(ChangeScreenMsg); ok {
					route = msg
					break
				}
			}
			if route.Screen != ScreenPreflight {
				t.Fatalf("expected queued briefing to route to preflight, got %#v", route)
			}

			updated, _ := model.Update(route)
			routed := updated.(RootModel)
			request := routed.Preflight.Request
			if routed.ActiveScreen != ScreenPreflight || request.Milestone.ID != milestone.ID || request.Milestone.Title != milestone.Title || request.Milestone.SpecPath != milestone.SpecPath || request.BriefingOrigin != origin {
				t.Fatalf("unexpected queued preflight request: screen=%v request=%+v", routed.ActiveScreen, request)
			}
			if request.RunnerMode != tc.wantMode || !request.NoBranchChange || routed.Preflight.ConfigPath != configPath || routed.Preflight.StatePath != statePath {
				t.Fatalf("preflight lost execution options: request=%+v config=%q state=%q", request, routed.Preflight.ConfigPath, routed.Preflight.StatePath)
			}

			routed.Preflight.Issues = nil
			_, confirmCmd := routed.Preflight.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if confirmCmd == nil {
				t.Fatal("expected preflight confirmation command")
			}
			confirmed, ok := confirmCmd().(StartCycleMsg)
			if !ok || confirmed.RunnerMode != tc.wantMode || confirmed.BriefingOrigin != origin {
				t.Fatalf("preflight did not preserve queued request: %#v", confirmed)
			}

			calls := make(chan executor.RunOptions, 1)
			executeCycle = func(_ context.Context, _ config.Milestone, _ []config.Agent, opts executor.RunOptions, _ *config.State, _ chan tea.Msg) {
				calls <- opts
			}
			updated, _ = routed.Update(confirmed)
			running := updated.(RootModel)
			if running.ActiveScreen != ScreenRunner || running.BriefingOrigin != origin {
				t.Fatalf("runner lost briefing origin: screen=%v origin=%+v", running.ActiveScreen, running.BriefingOrigin)
			}
			select {
			case opts := <-calls:
				if opts.Unrestricted != tc.wantUnrestricted || !opts.NoBranchChange || opts.ConfigPath != configPath || opts.StatePath != statePath {
					t.Fatalf("executor received mismatched options: %+v", opts)
				}
			case <-time.After(time.Second):
				t.Fatal("existing ExecuteCycle seam was not invoked")
			}
		})
	}
}

func TestBriefingExecutionNonApprovedResultsAndCancellationDoNotAdvanceStatus(t *testing.T) {
	for _, result := range []executor.CycleFinishedMsg{
		{MilestoneID: "linked-milestone", Status: "failed"},
		{MilestoneID: "linked-milestone", Status: "blocked"},
		{MilestoneID: "linked-milestone", Status: "failed", Error: errors.New("executor failed")},
		{MilestoneID: "linked-milestone", Status: "failed", Error: context.Canceled},
	} {
		root, model := writeBriefingExecutionTUIFixture(t)
		model.BriefingOrigin = BriefingOrigin{PlanID: "plan-one", BriefingID: "selected"}
		model.finishBriefingExecution(result)
		planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"linked-milestone"}))
		if got := planning.Plans[0].Briefings[0].Status; got != "active" {
			t.Errorf("result %+v advanced Briefing to %q", result, got)
		}
	}
}

func TestStandaloneApprovedMilestoneDoesNotUpdatePlanning(t *testing.T) {
	root, model := writeBriefingExecutionTUIFixture(t)
	model.finishBriefingExecution(executor.CycleFinishedMsg{MilestoneID: "linked-milestone", Status: "approved"})
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"linked-milestone"}))
	if got := planning.Plans[0].Briefings[0].Status; got != "active" {
		t.Fatalf("standalone execution changed planning status to %q", got)
	}
}

func TestPlanTerminalCallbackReceivesActualMilestoneID(t *testing.T) {
	model := RootModel{BriefingOrigin: BriefingOrigin{PlanID: "plan-one", BriefingID: "selected", MilestoneID: "expected", PlanRun: true}}
	var got string
	model.PlanCycleFinished = func(_ BriefingOrigin, milestoneID, _ string, _ error) (PlanContinuation, error) {
		got = milestoneID
		return PlanContinuation{}, nil
	}
	model.finishBriefingExecution(executor.CycleFinishedMsg{MilestoneID: "actual", Status: "approved"})
	if got != "actual" {
		t.Fatalf("Plan callback received Milestone ID %q, want terminal event ID", got)
	}
}

func TestPlanCancellationRetainsOriginUntilDelayedTerminalMessage(t *testing.T) {
	origin := BriefingOrigin{PlanID: "plan-one", BriefingID: "selected", MilestoneID: "linked-milestone", PlanRun: true}
	state := &config.State{}
	milestone := config.Milestone{ID: "linked-milestone", Title: "Linked"}
	model := NewRootModel(&config.Config{Milestones: []config.Milestone{milestone}}, state, "milestone.yml", "state.json", true, false, true, true)
	model.ActiveScreen = ScreenRunner
	model.BriefingOrigin = origin
	model.activePlanOrigin = origin
	model.Runner.Milestone = config.Milestone{ID: "linked-milestone", Title: "Linked"}
	model.Runner.Workflow = WorkflowCycle
	var gotOrigin BriefingOrigin
	var gotErr error
	model.PlanCycleFinished = func(callbackOrigin BriefingOrigin, _ string, _ string, cycleErr error) (PlanContinuation, error) {
		gotOrigin, gotErr = callbackOrigin, cycleErr
		return PlanContinuation{}, nil
	}

	updated, routeCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if routeCmd == nil {
		t.Fatal("cancel key did not return navigation command")
	}
	routed, _ := updated.(RootModel).Update(routeCmd())
	afterNavigation := routed.(RootModel)
	if afterNavigation.BriefingOrigin != (BriefingOrigin{}) || afterNavigation.activePlanOrigin != origin {
		t.Fatalf("navigation did not preserve only active callback ownership: visible=%+v active=%+v", afterNavigation.BriefingOrigin, afterNavigation.activePlanOrigin)
	}
	afterNavigation.Update(executor.CycleFinishedMsg{MilestoneID: "linked-milestone", Status: "failed", Error: context.Canceled})
	if gotOrigin != origin || gotErr != context.Canceled {
		t.Fatalf("delayed cancellation lost Plan ownership: origin=%+v err=%v", gotOrigin, gotErr)
	}
}

func TestBriefingCompletionSaveFailureIsVisibleAndDoesNotChangeMilestoneArtifacts(t *testing.T) {
	root, model := writeBriefingExecutionTUIFixture(t)
	model.BriefingOrigin = BriefingOrigin{PlanID: "plan-one", BriefingID: "selected"}
	milestonePath := filepath.Join(root, ".cyclestone", "milestones", "linked-milestone.md")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	beforeMilestone, _ := os.ReadFile(milestonePath)
	beforeState, _ := os.ReadFile(statePath)
	plansDir := filepath.Join(root, ".cyclestone", "plans")
	if err := os.Chmod(plansDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansDir, 0755) })

	model.finishBriefingExecution(executor.CycleFinishedMsg{MilestoneID: "linked-milestone", Status: "approved", ReportFile: "report.yaml"})

	if !strings.Contains(model.Runner.LastError, "Planning update warning") || !strings.Contains(model.Runner.Status, "cycle remains valid") {
		t.Fatalf("expected visible planning save warning, status=%q error=%q", model.Runner.Status, model.Runner.LastError)
	}
	afterMilestone, _ := os.ReadFile(milestonePath)
	afterState, _ := os.ReadFile(statePath)
	if string(afterMilestone) != string(beforeMilestone) || string(afterState) != string(beforeState) {
		t.Fatal("planning save failure changed Milestone spec or runtime state")
	}
}

func writeBriefingExecutionTUIFixture(t *testing.T) (string, RootModel) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	plansDir := filepath.Join(root, ".cyclestone", "plans")
	if err := os.MkdirAll(filepath.Join(root, ".cyclestone", "milestones"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("milestones:\n  - id: linked-milestone\n    title: Linked\n    spec_path: milestones/linked-milestone.md\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cyclestone", "milestones", "linked-milestone.md"), []byte("# Linked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"milestone_statuses":{},"milestone_cycles":{},"history":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	plan := `schema_version: 1
id: plan-one
title: Plan One
objective: Execute one Briefing.
status: active
created_at: "2026-07-20T10:00:00Z"
created_by: pm
updated_at: "2026-07-20T10:00:00Z"
updated_by: pm
briefing_order: [selected, other]
briefings:
  - id: selected
    title: Selected
    objective: Execute selected work.
    intent: Run one Milestone.
    status: active
    milestone_id: linked-milestone
    completion_signal: Selected work completes.
    created_at: "2026-07-20T10:00:00Z"
    created_by: pm
    updated_at: "2026-07-20T10:00:00Z"
    updated_by: pm
  - id: other
    title: Other
    objective: Remain ready.
    intent: Do not auto-start.
    status: active
    completion_signal: Other stays active.
    created_at: "2026-07-20T10:00:00Z"
    created_by: pm
    updated_at: "2026-07-20T10:00:00Z"
    updated_by: pm
`
	if err := os.WriteFile(filepath.Join(plansDir, "plan-one.yml"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state, err := config.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	return root, NewRootModel(cfg, state, configPath, statePath, true, false, true, true)
}

func TestCreateMilestoneFinishedMsgScansDirectoryMilestones(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	milestonesDir := filepath.Join(root, ".cyclestone", "milestones")
	if err := os.MkdirAll(milestonesDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg, _ := config.LoadConfig(configPath)
	state := &config.State{
		MilestoneStatuses: make(map[string]string),
		MilestoneCycles:   make(map[string]int),
		History:           make(map[string][]config.MilestoneCycleLog),
	}
	model := NewRootModel(cfg, state, configPath, statePath, true, false, true, true)
	model.CreateMilestone.NextID = "ms-pf-0001"
	model.CreateMilestone.RunnerType = "agy"

	// Simulate AI milestone runner creating a folder-per-item milestone directory
	targetDir := filepath.Join(root, ".cyclestone", "milestones", "ms-pf-0001-test-milestone-without-changes")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	metaData := `id: ms-pf-0001-test-milestone-without-changes
title: Test Milestone Without Changes
goal: Test goal
status: Todo
cycles: 0
created_by: tui
updated_by: tui
`
	if err := os.WriteFile(filepath.Join(targetDir, "ms-pf-0001-metadata.yml"), []byte(metaData), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "ms-pf-0001-specification.md"), []byte("# Milestone Spec: Test Milestone Without Changes\n\n## Goal\nTest goal\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change working directory to root for relative path scanning
	oldWd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer func() { _ = os.Chdir(oldWd) }()

	updated, _ := model.Update(executor.CreateMilestoneFinishedMsg{Error: nil})
	got := updated.(RootModel)

	if got.CreateMilestone.ErrorMsg != "" {
		t.Fatalf("unexpected error message: %s", got.CreateMilestone.ErrorMsg)
	}
	if got.ActiveScreen != ScreenDashboard {
		t.Fatalf("expected ActiveScreen ScreenDashboard, got %v", got.ActiveScreen)
	}
	found := false
	for _, ms := range got.Config.Milestones {
		if ms.ID == "ms-pf-0001-test-milestone-without-changes" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected milestone ms-pf-0001-test-milestone-without-changes to be loaded into Config.Milestones")
	}
}
