package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
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

func TestGenerateNextIDUsesFourDigitPrefix(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "empty config starts at 0001",
			cfg:  &config.Config{},
			want: "0001",
		},
		{
			name: "increments numeric milestone prefixes",
			cfg: &config.Config{Milestones: []config.Milestone{
				{ID: "0001-project-setup"},
				{ID: "0009-release-readiness"},
			}},
			want: "0010",
		},
		{
			name: "ignores legacy nonnumeric prefixes",
			cfg: &config.Config{Milestones: []config.Milestone{
				{ID: "MS-1"},
				{ID: "0002-current-convention"},
			}},
			want: "0003",
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
	model.Setup.Runner = "codex"
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
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("milestone.yml was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones")); err != nil {
		t.Fatalf("milestones directory was not created: %v", err)
	}
	specData, err := os.ReadFile(filepath.Join(root, ".cyclestone", "milestones", "0001-first-run.md"))
	if err != nil {
		t.Fatalf("first milestone spec was not created: %v", err)
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
