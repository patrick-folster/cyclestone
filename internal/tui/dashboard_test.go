package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestDashboardUpdateAgentsRoutesToRepositoryWorkflow(t *testing.T) {
	model := NewDashboardModel(&config.Config{}, &config.State{}, DefaultStyles(true, true))

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("expected update AGENTS command")
	}
	change, ok := cmd().(ChangeScreenMsg)
	if !ok || change.Screen != ScreenCreateMilestone {
		t.Fatalf("expected create/note screen change, got %#v", change)
	}
	req, ok := change.Data.(StartCycleMsg)
	if !ok {
		t.Fatalf("expected StartCycleMsg payload, got %#v", change.Data)
	}
	if req.Workflow != WorkflowAgentInstructionsRepository || req.Milestone.ID != "AGENTS.md" || !req.NoBranchChange {
		t.Fatalf("unexpected repository update request: %#v", req)
	}
}
