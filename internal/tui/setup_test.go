package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestSetupRunnerDetectionUsesRestrictedPathRunners(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	ollamaPath := filepath.Join(tmp, "ollama")
	if err := os.WriteFile(ollamaPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)

	runners := detectSetupRunnerAvailability()
	available := map[string]bool{}
	seen := map[string]bool{}
	for _, runner := range runners {
		available[runner.ID] = runner.Available
		seen[runner.ID] = true
	}
	if !available["codex"] {
		t.Fatalf("expected codex to be detected from PATH: %#v", runners)
	}
	if !available["ollama-codex"] {
		t.Fatalf("expected ollama-codex to be available through ollama and codex on PATH: %#v", runners)
	}
	if available["agy"] {
		t.Fatalf("unexpected unavailable runner marked available: %#v", runners)
	}
	for _, removed := range []string{"gemini", "openai", "anthropic"} {
		if seen[removed] {
			t.Fatalf("setup should not offer %s: %#v", removed, runners)
		}
	}
	if got := defaultSetupRunner(runners); got != "codex" {
		t.Fatalf("expected first available default runner codex, got %q", got)
	}
}

func TestOllamaCodexSetupRunnerRequiresBothBinaries(t *testing.T) {
	tmp := t.TempDir()
	ollamaPath := filepath.Join(tmp, "ollama")
	if err := os.WriteFile(ollamaPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)

	available, reason := isRunnerAvailable("ollama-codex")
	if available || !strings.Contains(reason, "codex not found on PATH") {
		t.Fatalf("expected missing codex reason, available=%v reason=%q", available, reason)
	}

	if err := os.WriteFile(filepath.Join(tmp, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	available, reason = isRunnerAvailable("ollama-codex")
	if !available || !strings.Contains(reason, "ollama and codex") {
		t.Fatalf("expected ollama-codex availability, available=%v reason=%q", available, reason)
	}
}

func TestSetupWizardDefaultsToInherit(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	if !model.RunnerInherit {
		t.Fatal("expected RunnerInherit to default to true")
	}
	if !model.SafetyInherit {
		t.Fatal("expected SafetyInherit to default to true")
	}
	if !model.BranchesInherit {
		t.Fatal("expected BranchesInherit to default to true")
	}
	if model.Runner != "" {
		t.Fatalf("expected empty Runner when inheriting, got %q", model.Runner)
	}
	if model.Unrestricted {
		t.Fatal("expected Unrestricted=false when safety is inherited")
	}
}

func TestSetupWizardInheritRunnerCycling(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.GlobalSettings = config.Settings{DefaultLLM: "codex"}
	model.Width = 80
	model.Height = 24

	if !model.RunnerInherit {
		t.Fatal("expected inherit as default for runner")
	}

	// Right: inherit -> codex
	model.FocusIndex = setupFieldRunner
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if model.RunnerInherit || model.Runner != "codex" {
		t.Fatalf("expected codex after cycling right from inherit, got inherit=%v runner=%q", model.RunnerInherit, model.Runner)
	}

	// Right: codex -> inherit
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if !model.RunnerInherit || model.Runner != "" {
		t.Fatalf("expected inherit after cycling right from codex, got inherit=%v runner=%q", model.RunnerInherit, model.Runner)
	}

	// Left from inherit wraps to codex
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated
	if model.RunnerInherit || model.Runner != "codex" {
		t.Fatalf("expected codex after cycling left from inherit, got inherit=%v runner=%q", model.RunnerInherit, model.Runner)
	}
}

func TestSetupWizardInheritSafetyCycling(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.GlobalSettings = config.Settings{DefaultMode: "sandbox"}
	model.Width = 80
	model.Height = 24
	model.FocusIndex = setupFieldSafetyMode

	if !model.SafetyInherit {
		t.Fatal("expected inherit as default for safety")
	}

	// Right: inherit -> sandbox
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if model.SafetyInherit || model.Unrestricted {
		t.Fatalf("expected sandbox after cycling right from inherit, got inherit=%v unrestricted=%v", model.SafetyInherit, model.Unrestricted)
	}

	// Right: sandbox -> unrestricted
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if model.SafetyInherit || !model.Unrestricted {
		t.Fatalf("expected unrestricted after cycling right from sandbox, got inherit=%v unrestricted=%v", model.SafetyInherit, model.Unrestricted)
	}

	// Right: unrestricted -> inherit
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if !model.SafetyInherit || model.Unrestricted {
		t.Fatalf("expected inherit after cycling right from unrestricted, got inherit=%v unrestricted=%v", model.SafetyInherit, model.Unrestricted)
	}

	// Left from inherit wraps to unrestricted
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated
	if model.SafetyInherit || !model.Unrestricted {
		t.Fatalf("expected unrestricted after cycling left from inherit, got inherit=%v unrestricted=%v", model.SafetyInherit, model.Unrestricted)
	}
}

func TestSetupWizardInheritBranchesCycling(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.GlobalSettings = config.Settings{}
	model.Width = 80
	model.Height = 24
	model.FocusIndex = setupFieldBranchBehavior

	if !model.BranchesInherit {
		t.Fatal("expected inherit as default for branches")
	}

	// Right: inherit -> automatic
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if model.BranchesInherit || !model.AutoBranches {
		t.Fatalf("expected automatic after cycling right from inherit, got inherit=%v auto=%v", model.BranchesInherit, model.AutoBranches)
	}

	// Right: automatic -> manual
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if model.BranchesInherit || model.AutoBranches {
		t.Fatalf("expected manual after cycling right from automatic, got inherit=%v auto=%v", model.BranchesInherit, model.AutoBranches)
	}

	// Right: manual -> inherit
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated
	if !model.BranchesInherit {
		t.Fatalf("expected inherit after cycling right from manual, got inherit=%v auto=%v", model.BranchesInherit, model.AutoBranches)
	}

	// Left from inherit wraps to manual
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated
	if model.BranchesInherit || model.AutoBranches {
		t.Fatalf("expected manual after cycling left from inherit, got inherit=%v auto=%v", model.BranchesInherit, model.AutoBranches)
	}
}

func TestSetupWizardBlocksUnrestrictedWithoutAcknowledgement(t *testing.T) {
	root := t.TempDir()
	model := NewSetupWizardModel(filepath.Join(root, ".cyclestone", "milestone.yml"), filepath.Join(root, ".cyclestone", "state.json"), DefaultStyles(true, true))
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.RunnerInherit = false
	model.Runner = "codex"
	model.SafetyInherit = false
	model.Unrestricted = true
	model.UnrestrictedAck = false
	model.FocusIndex = setupFieldConfirm

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected unrestricted setup to block until acknowledged")
	}
	if !strings.Contains(updated.ErrorMsg, "Confirm unrestricted mode") {
		t.Fatalf("expected unrestricted acknowledgement error, got %q", updated.ErrorMsg)
	}
	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestone.yml")); !os.IsNotExist(err) {
		t.Fatalf("config was created despite blocked confirmation: %v", err)
	}
}

func TestSetupWizardDisplaysPathsReadOnlyAndStartsAtRunner(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Width = 80
	model.Height = 24
	model.GlobalSettings = config.Settings{DefaultLLM: "codex", DefaultMode: "sandbox"}

	if model.FocusIndex != setupFieldRunner {
		t.Fatalf("expected initial focus on runner, got %d", model.FocusIndex)
	}

	view := model.View()
	for _, want := range []string{"Milestone config: .cyclestone/milestone.yml", "State file: .cyclestone/state.json"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected setup view to contain read-only path row %q, got:\n%s", want, view)
		}
	}
	// Inherit is the default and should be visible.
	if !strings.Contains(view, "inherit") {
		t.Fatalf("expected setup view to show inherit option, got:\n%s", view)
	}
}

func TestSetupWizardNavigationSkipsStaticPathRows(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}

	seen := map[int]bool{}
	for i := 0; i < setupFieldCount*2; i++ {
		seen[model.FocusIndex] = true
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
		model = updated
	}
	for _, want := range []int{setupFieldRunner, setupFieldSafetyMode, setupFieldBranchBehavior, setupFieldCreateFirstMilestone, setupFieldConfirm, setupFieldCancel} {
		if !seen[want] {
			t.Fatalf("expected tab navigation to reach field %d, seen %#v", want, seen)
		}
	}

	model.FocusIndex = setupFieldRunner
	seen = map[int]bool{}
	for i := 0; i < setupFieldCount*2; i++ {
		seen[model.FocusIndex] = true
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		model = updated
	}
	for _, want := range []int{setupFieldRunner, setupFieldSafetyMode, setupFieldBranchBehavior, setupFieldCreateFirstMilestone, setupFieldConfirm, setupFieldCancel} {
		if !seen[want] {
			t.Fatalf("expected shift-tab navigation to reach field %d, seen %#v", want, seen)
		}
	}
}

func TestSetupWizardConfirmUsesConstructorPaths(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	model := NewSetupWizardModel(configPath, statePath, DefaultStyles(true, true))
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.RunnerInherit = false
	model.Runner = "codex"
	model.SafetyInherit = false
	model.BranchesInherit = false
	model.FocusIndex = setupFieldConfirm

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected setup completion command, error=%q", updated.ErrorMsg)
	}
	msg, ok := cmd().(SetupCompletedMsg)
	if !ok {
		t.Fatalf("expected SetupCompletedMsg")
	}
	if msg.ConfigPath != configPath || msg.StatePath != statePath {
		t.Fatalf("expected static paths in completion, got config=%q state=%q", msg.ConfigPath, msg.StatePath)
	}
	for _, path := range []string{
		configPath,
		statePath,
		filepath.Join(root, ".cyclestone", "settings.yml"),
		filepath.Join(root, ".cyclestone", "milestones"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected setup artifact %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "wrong", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected state file outside constructor path: %v", err)
	}
}

func TestSetupWizardConfirmWithInheritSavesEmptySettings(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
	statePath := filepath.Join(root, ".cyclestone", "state.json")
	model := NewSetupWizardModel(configPath, statePath, DefaultStyles(true, true))
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	// Leave RunnerInherit, SafetyInherit, and BranchesInherit at their default true.
	model.FocusIndex = setupFieldConfirm

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected setup completion command with inherit defaults, error=%q", updated.ErrorMsg)
	}
	msg, ok := cmd().(SetupCompletedMsg)
	if !ok {
		t.Fatalf("expected SetupCompletedMsg")
	}
	_ = msg

	settingsPath := filepath.Join(root, ".cyclestone", "settings.yml")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.yml: %v", err)
	}
	settingsText := string(data)
	// Inherited fields should NOT appear with explicit values in settings.yml.
	// default_llm, default_mode, auto_git_branch, and create_milestone_branch
	// should be absent or empty so the project defers to the global config.
	for _, absent := range []string{"default_llm: codex", "default_mode: sandbox", "default_mode: unrestricted", "auto_git_branch: true", "create_milestone_branch: true"} {
		if strings.Contains(settingsText, absent) {
			t.Fatalf("expected inherited field to be absent from settings.yml, but found %q in:\n%s", absent, settingsText)
		}
	}
	// The branch prefix is always saved (not an inheritable field in the setup screen).
	if !strings.Contains(settingsText, "cyclestone/milestones/") {
		t.Fatalf("expected default_git_branch_prefix in settings.yml, got:\n%s", settingsText)
	}
}

func TestSetupWizardNarrowRendering(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Width = 42
	model.Height = 16
	model.GlobalSettings = config.Settings{DefaultLLM: "codex", DefaultMode: "sandbox"}

	view := model.View()
	for _, want := range []string{"FIRST-RUN SETUP", "Milestone config", "Runner", "Confirm setup"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected narrow setup view to contain %q, got:\n%s", want, view)
		}
	}
}

func TestSetupWizardShowsAllRunnerSafetyBranchesOptionsWide(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Width = 80
	model.Height = 24
	model.GlobalSettings = config.Settings{DefaultLLM: "codex", DefaultMode: "sandbox"}
	model.Runners = []runnerAvailability{
		{ID: "codex", Label: "Codex CLI", Available: true},
		{ID: "agy", Label: "Agy CLI", Available: false},
		{ID: "ollama-codex", Label: "Ollama via Codex", Available: false},
	}
	model.RunnerInherit = false
	model.Runner = "codex"
	model.SafetyInherit = false
	model.BranchesInherit = false

	view := model.View()
	for _, want := range []string{"codex", "agy", "ollama-codex"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected wide view to show runner %q, got:\n%s", want, view)
		}
	}
	for _, want := range []string{"Sandbox", "Unrestricted"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected wide Safety view to show %q, got:\n%s", want, view)
		}
	}
	for _, want := range []string{"Automatic", "No branch changes"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected wide Branches view to show %q, got:\n%s", want, view)
		}
	}
	// Inherit option should be visible even when not selected.
	if !strings.Contains(view, "inherit") {
		t.Fatalf("expected wide view to show inherit option, got:\n%s", view)
	}
}

func TestSetupWizardShowsAllRunnerSafetyBranchesOptionsNarrow(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Width = 50
	model.Height = 16
	model.GlobalSettings = config.Settings{DefaultLLM: "codex", DefaultMode: "sandbox"}
	model.Runners = []runnerAvailability{
		{ID: "codex", Label: "Codex CLI", Available: true},
		{ID: "agy", Label: "Agy CLI", Available: false},
		{ID: "ollama-codex", Label: "Ollama via Codex", Available: false},
	}
	model.RunnerInherit = false
	model.Runner = "codex"
	model.SafetyInherit = false
	model.BranchesInherit = false

	view := model.View()
	for _, want := range []string{"codex", "agy"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected narrow view to show runner %q, got:\n%s", want, view)
		}
	}
	for _, want := range []string{"Sandbox", "Unrestricted"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected narrow Safety view to show %q, got:\n%s", want, view)
		}
	}
	for _, want := range []string{"Auto", "Manual"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected narrow Branches view to show %q, got:\n%s", want, view)
		}
	}
	// Inherit option should be visible even in narrow mode.
	if !strings.Contains(view, "inherit") {
		t.Fatalf("expected narrow view to show inherit option, got:\n%s", view)
	}
}

func TestSetupWizardMarksSelectedOption(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Width = 80
	model.Height = 24
	model.GlobalSettings = config.Settings{DefaultLLM: "codex", DefaultMode: "sandbox"}
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.RunnerInherit = false
	model.Runner = "codex"
	model.SafetyInherit = false
	model.Unrestricted = false
	model.BranchesInherit = false
	model.AutoBranches = true

	view := model.View()
	// Sandbox selected (SafetyInherit=false, Unrestricted=false).
	if !strings.Contains(view, "(*) Sandbox") {
		t.Fatalf("expected Sandbox marked selected, got:\n%s", view)
	}
	if !strings.Contains(view, "( ) Unrestricted") {
		t.Fatalf("expected Unrestricted marked unselected, got:\n%s", view)
	}
	// Automatic selected (BranchesInherit=false, AutoBranches=true).
	if !strings.Contains(view, "(*) Automatic") {
		t.Fatalf("expected Automatic branches marked selected, got:\n%s", view)
	}
	if !strings.Contains(view, "( ) No branch changes") {
		t.Fatalf("expected No branch changes marked unselected, got:\n%s", view)
	}
	// Inherit should be unselected for both.
	if !strings.Contains(view, "( ) inherit") {
		t.Fatalf("expected inherit marked unselected for safety, got:\n%s", view)
	}

	// Flip both and re-check markers.
	model.Unrestricted = true
	model.AutoBranches = false
	view = model.View()
	if !strings.Contains(view, "( ) Sandbox") {
		t.Fatalf("expected Sandbox marked unselected after flip, got:\n%s", view)
	}
	if !strings.Contains(view, "(*) Unrestricted") {
		t.Fatalf("expected Unrestricted marked selected after flip, got:\n%s", view)
	}
	if !strings.Contains(view, "( ) Automatic") {
		t.Fatalf("expected Automatic marked unselected after flip, got:\n%s", view)
	}
	if !strings.Contains(view, "(*) No branch changes") {
		t.Fatalf("expected No branch changes marked selected after flip, got:\n%s", view)
	}
}

func TestSetupWizardInheritSelectedMarkers(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Width = 80
	model.Height = 24
	model.GlobalSettings = config.Settings{DefaultLLM: "codex", DefaultMode: "sandbox"}
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	// All three inherit flags default to true.
	model.RunnerInherit = true
	model.SafetyInherit = true
	model.BranchesInherit = true

	view := model.View()
	// Safety: inherit should be selected.
	if !strings.Contains(view, "(*) inherit") {
		t.Fatalf("expected inherit marked selected for safety, got:\n%s", view)
	}
	if !strings.Contains(view, "( ) Sandbox") {
		t.Fatalf("expected Sandbox marked unselected when inheriting, got:\n%s", view)
	}
	// Branches: inherit should be selected.
	// Both inherit markers appear; verify branches inherit is selected by
	// checking that Automatic is NOT selected.
	if strings.Contains(view, "(*) Automatic") {
		t.Fatalf("expected Automatic NOT selected when branches inherited, got:\n%s", view)
	}
}
