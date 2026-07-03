package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestSetupWizardBlocksUnrestrictedWithoutAcknowledgement(t *testing.T) {
	root := t.TempDir()
	model := NewSetupWizardModel(filepath.Join(root, ".cyclestone", "milestone.yml"), filepath.Join(root, ".cyclestone", "state.json"), DefaultStyles(true, true))
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.Runner = "codex"
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
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.Runner = "codex"

	if model.FocusIndex != setupFieldRunner {
		t.Fatalf("expected initial focus on runner, got %d", model.FocusIndex)
	}

	view := model.View()
	for _, want := range []string{"Milestone config: .cyclestone/milestone.yml", "State file: .cyclestone/state.json"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected setup view to contain read-only path row %q, got:\n%s", want, view)
		}
	}
}

func TestSetupWizardNavigationSkipsStaticPathRows(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.Runner = "codex"

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
	model.Runner = "codex"
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

func TestSetupWizardNarrowRendering(t *testing.T) {
	model := NewSetupWizardModel(".cyclestone/milestone.yml", ".cyclestone/state.json", DefaultStyles(true, true))
	model.Width = 42
	model.Height = 16
	model.Runners = []runnerAvailability{{ID: "codex", Label: "Codex CLI", Available: true}}
	model.Runner = "codex"

	view := model.View()
	for _, want := range []string{"FIRST-RUN SETUP", "Milestone config", "Runner", "Confirm setup"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected narrow setup view to contain %q, got:\n%s", want, view)
		}
	}
}
