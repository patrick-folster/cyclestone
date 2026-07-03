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
	if !available["aider"] || !available["ollama"] {
		t.Fatalf("expected aider and ollama to be available through aider on PATH: %#v", runners)
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
