package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestAgentGroupsLayoutClamping(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewAgentGroupsModel(styles)

	m.Groups = []config.AgentGroup{
		{Name: "default", AgentIDs: []string{"a1", "a2", "a3"}},
		{Name: "group1", AgentIDs: []string{"a4", "a5"}},
	}
	m.AvailableAgents = []config.Agent{
		{ID: "a1", Name: "Agent 1"},
		{ID: "a2", Name: "Agent 2"},
		{ID: "a3", Name: "Agent 3"},
		{ID: "a4", Name: "Agent 4"},
		{ID: "a5", Name: "Agent 5"},
	}

	// Normal size terminal
	m.Width = 80
	m.Height = 20
	m.FocusCol = 0

	layout := m.calculateLayout()
	if layout.boxHeight < 5 {
		t.Errorf("Expected boxHeight >= 5, got %d", layout.boxHeight)
	}
	if !layout.showHelp {
		t.Error("Expected help text to be visible on large height")
	}

	// Small height terminal (Height < 15)
	m.Height = 12
	layoutSmall := m.calculateLayout()
	if layoutSmall.showHelp {
		t.Error("Expected help text to be hidden when Height < 15")
	}
	if layoutSmall.showSpacers {
		t.Error("Expected spacers to be hidden when Height < 15")
	}
	if layoutSmall.boxHeight < 1 {
		t.Errorf("Expected boxHeight to be at least 1, got %d", layoutSmall.boxHeight)
	}

	// Verify viewport clamping
	viewport0, _, viewport1, _, viewport2, _ := m.getViewports(layoutSmall.boxHeight)
	if viewport0 < 1 || viewport1 < 1 || viewport2 < 1 {
		t.Errorf("Expected viewports to be at least 1, got v0=%d, v1=%d, v2=%d", viewport0, viewport1, viewport2)
	}
}

func TestAgentGroupsTabNavigation(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewAgentGroupsModel(styles)
	m.Width = 80
	m.Height = 24
	m.FocusCol = 0

	// Tab navigates right: 0 -> 1 -> 2 -> 0
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.FocusCol != 1 {
		t.Errorf("Expected FocusCol = 1 after Tab, got %d", m.FocusCol)
	}
	if cmd != nil {
		t.Error("Unexpected non-nil cmd")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.FocusCol != 2 {
		t.Errorf("Expected FocusCol = 2 after Tab, got %d", m.FocusCol)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.FocusCol != 0 {
		t.Errorf("Expected FocusCol = 0 after Tab, got %d", m.FocusCol)
	}

	// Shift+Tab navigates left: 0 -> 2 -> 1 -> 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.FocusCol != 2 {
		t.Errorf("Expected FocusCol = 2 after Shift+Tab, got %d", m.FocusCol)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.FocusCol != 1 {
		t.Errorf("Expected FocusCol = 1 after Shift+Tab, got %d", m.FocusCol)
	}

	// Numeric keys focus columns directly
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.FocusCol != 2 {
		t.Errorf("Expected FocusCol = 2 after '3', got %d", m.FocusCol)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if m.FocusCol != 0 {
		t.Errorf("Expected FocusCol = 0 after '1', got %d", m.FocusCol)
	}
}

func TestAgentGroupsModifications(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewAgentGroupsModel(styles)
	m.Width = 80
	m.Height = 24

	// Populate groups
	m.Groups = []config.AgentGroup{
		{Name: "default", AgentIDs: []string{"a1"}},
		{Name: "custom", AgentIDs: []string{"a2", "a3"}},
	}
	m.AvailableAgents = []config.Agent{
		{ID: "a1", Name: "Agent 1"},
		{ID: "a2", Name: "Agent 2"},
		{ID: "a3", Name: "Agent 3"},
		{ID: "a4", Name: "Agent 4"},
	}

	// 1. Try to modify predefined "default" group - should fail
	m.SelectedGroupIdx = 0
	m.FocusCol = 1 // Pipeline agents
	m.SelectedAgentIdx = 0

	// Try removing agent from default group
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.ErrorMsg == "" {
		t.Error("Expected error when trying to remove agent from default group")
	}
	if len(m.Groups[0].AgentIDs) != 1 {
		t.Error("Default group should not have been modified")
	}

	m.ErrorMsg = "" // Reset error

	// Try reordering in default group
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if m.ErrorMsg == "" {
		t.Error("Expected error when trying to reorder agent in default group")
	}

	m.ErrorMsg = ""

	// Try deleting default group
	m.FocusCol = 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.ErrorMsg == "" {
		t.Error("Expected error when trying to delete default group")
	}

	m.ErrorMsg = ""

	// 2. Modify custom group
	m.SelectedGroupIdx = 1

	// Add an agent from available list (FocusCol = 2)
	m.FocusCol = 2
	m.AvailableAgentIdx = 3 // select "a4"
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.ErrorMsg != "" {
		t.Errorf("Unexpected error: %s", m.ErrorMsg)
	}
	if !m.HasChanges {
		t.Error("Expected HasChanges to be true")
	}
	if len(m.Groups[1].AgentIDs) != 3 || m.Groups[1].AgentIDs[2] != "a4" {
		t.Errorf("Expected agent a4 to be added to group, group is now: %v", m.Groups[1].AgentIDs)
	}

	// Reorder agent in custom group (FocusCol = 1)
	m.FocusCol = 1
	m.SelectedAgentIdx = 2 // "a4"
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	if m.Groups[1].AgentIDs[1] != "a4" || m.Groups[1].AgentIDs[2] != "a3" {
		t.Errorf("Expected swap, got pipeline: %v", m.Groups[1].AgentIDs)
	}

	// Remove agent from custom group (FocusCol = 1)
	m.SelectedAgentIdx = 1 // "a4"
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if len(m.Groups[1].AgentIDs) != 2 || m.Groups[1].AgentIDs[1] != "a3" {
		t.Errorf("Expected agent removed, got pipeline: %v", m.Groups[1].AgentIDs)
	}

	// Delete custom group
	m.FocusCol = 0
	m.SelectedGroupIdx = 1
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if len(m.Groups) != 1 {
		t.Errorf("Expected custom group deleted, groups: %v", m.Groups)
	}
}

func TestAgentGroupsPaginationDisplay(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewAgentGroupsModel(styles)
	m.Width = 80
	m.Height = 12

	// Setup data such that item count exceeds base viewport height
	// boxHeight for Height 12 is calculated to be around 4-5.
	// Let's populate 10 groups
	for i := 0; i < 10; i++ {
		m.Groups = append(m.Groups, config.AgentGroup{Name: "group", AgentIDs: []string{}})
	}

	layout := m.calculateLayout()
	_, showPagination0, _, _, _, _ := m.getViewports(layout.boxHeight)

	if !showPagination0 {
		t.Logf("boxHeight: %d", layout.boxHeight)
		t.Error("Expected showPagination0 to be true when total items exceed base viewport height")
	}

	// Render view and check if 'Showing' text is present
	viewStr := m.View()
	if !strings.Contains(viewStr, "Showing") {
		t.Error("Expected pagination text in the rendered view")
	}
}
