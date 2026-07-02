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
	aiderPath := filepath.Join(tmp, "aider")
	if err := os.WriteFile(aiderPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

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
	if !available["aider"] || !available["ollama"] {
		t.Fatalf("expected aider and ollama to be available through aider on PATH: %#v", runners)
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
