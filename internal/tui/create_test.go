package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestGetCreateRunnerOptions(t *testing.T) {
	tests := []struct {
		name       string
		defaultLLM string
		expected   []string
	}{
		{
			name:       "empty defaultLLM",
			defaultLLM: "",
			expected:   []string{"template", "codex", "agy", "aider", "gemini", "openai", "anthropic", "ollama"},
		},
		{
			name:       "standard defaultLLM",
			defaultLLM: "gemini",
			expected:   []string{"template", "codex", "agy", "aider", "gemini", "openai", "anthropic", "ollama"},
		},
		{
			name:       "custom defaultLLM",
			defaultLLM: "custom-runner",
			expected:   []string{"template", "codex", "agy", "aider", "gemini", "openai", "anthropic", "ollama", "custom-runner"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getCreateRunnerOptions(tt.defaultLLM)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getCreateRunnerOptions(%q) = %v; want %v", tt.defaultLLM, got, tt.expected)
			}
		})
	}
}

func TestSlugifyTitle(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		expected string
	}{
		{
			name:     "simple title",
			title:    "Implement caching controls",
			expected: "implement-caching-controls",
		},
		{
			name:     "title with stop words",
			title:    "Implement the caching and controls for a project",
			expected: "implement-caching-controls-project",
		},
		{
			name:     "title with special characters",
			title:    "LLM Caching API (v1beta) - Refactor!",
			expected: "llm-caching-api-v1beta",
		},
		{
			name:     "excessively long title",
			title:    "This is a very long title with many words that will be trimmed to four words",
			expected: "this-very-long-title",
		},
		{
			name:     "only stop words",
			title:    "the a is with to",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugifyTitle(tt.title)
			if got != tt.expected {
				t.Errorf("slugifyTitle(%q) = %q; want %q", tt.title, got, tt.expected)
			}
		})
	}
}

func TestCreateMilestoneModel_CycleNoteMode(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewCreateMilestoneModel(styles)
	m.Mode = ModeCycleNote
	m.Width = 80
	m.Height = 24
	m.RunMilestone = config.Milestone{ID: "0010", Title: "My Milestone"}
	m.RunRunnerLLM = "gemini"
	m.RunRunnerMode = "sandbox"
	m.RunNoBranch = true
	m.RunSingleID = "qa"

	// Verify initial focus is 0 (note textarea)
	if m.FocusIndex != 0 {
		t.Errorf("expected initial FocusIndex to be 0, got %d", m.FocusIndex)
	}

	// 1. Tab Focus Cycling
	// Tab -> 4 (Submit)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.FocusIndex != 4 {
		t.Errorf("expected FocusIndex to be 4 after Tab, got %d", m.FocusIndex)
	}

	// Tab -> 5 (Cancel)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.FocusIndex != 5 {
		t.Errorf("expected FocusIndex to be 5 after Tab, got %d", m.FocusIndex)
	}

	// Tab -> 0 (Note textarea)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.FocusIndex != 0 {
		t.Errorf("expected FocusIndex to be 0 after Tab, got %d", m.FocusIndex)
	}

	// 2. Shift+Tab Focus Cycling
	// Shift+Tab -> 5 (Cancel)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.FocusIndex != 5 {
		t.Errorf("expected FocusIndex to be 5 after Shift+Tab, got %d", m.FocusIndex)
	}

	// Shift+Tab -> 4 (Submit)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.FocusIndex != 4 {
		t.Errorf("expected FocusIndex to be 4 after Shift+Tab, got %d", m.FocusIndex)
	}

	// Shift+Tab -> 0 (Note textarea)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.FocusIndex != 0 {
		t.Errorf("expected FocusIndex to be 0 after Shift+Tab, got %d", m.FocusIndex)
	}

	// 3. Arrow Down/Up Focus Cycling (when FocusIndex != 0)
	// Go to 4 first
	m.FocusIndex = 4
	// Down -> 5
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.FocusIndex != 5 {
		t.Errorf("expected FocusIndex to be 5 after Down on 4, got %d", m.FocusIndex)
	}
	// Down -> 4 (Since only 4 and 5 are selectable when not on 0)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.FocusIndex != 4 {
		t.Errorf("expected FocusIndex to be 4 after Down on 5, got %d", m.FocusIndex)
	}
	// Up -> 5
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.FocusIndex != 5 {
		t.Errorf("expected FocusIndex to be 5 after Up on 4, got %d", m.FocusIndex)
	}

	// 4. View rendering does not crash and contains the header
	viewStr := m.View()
	if !strings.Contains(viewStr, "ADD OPTIONAL CYCLE NOTE / COMMENT") {
		t.Errorf("expected view to contain header, got: %s", viewStr)
	}
	if !strings.Contains(viewStr, "Milestone: 0010") {
		t.Errorf("expected view to contain milestone ID, got: %s", viewStr)
	}

	// 5. Submit note routes to preflight with the StartCycleMsg payload
	m.FocusIndex = 4 // Focus on Submit
	m.GoalInput.SetValue("This is a test cycle note")
	_, cmdSubmit := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdSubmit == nil {
		t.Fatal("expected submit action to return a command")
	}
	msgSubmit := cmdSubmit()
	changeScreenMsg, ok := msgSubmit.(ChangeScreenMsg)
	if !ok {
		t.Fatalf("expected command to return ChangeScreenMsg, got %T", msgSubmit)
	}
	if changeScreenMsg.Screen != ScreenPreflight {
		t.Fatalf("expected screen to transition to ScreenPreflight, got %v", changeScreenMsg.Screen)
	}
	startCycleMsg, ok := changeScreenMsg.Data.(StartCycleMsg)
	if !ok {
		t.Fatalf("expected preflight payload to be StartCycleMsg, got %T", changeScreenMsg.Data)
	}
	if startCycleMsg.Note != "This is a test cycle note" {
		t.Errorf("expected note to be 'This is a test cycle note', got %q", startCycleMsg.Note)
	}
	if startCycleMsg.Milestone.ID != "0010" {
		t.Errorf("expected Milestone ID to be '0010', got %q", startCycleMsg.Milestone.ID)
	}
	if startCycleMsg.SingleAgentID != "qa" {
		t.Errorf("expected SingleAgentID to be preserved, got %q", startCycleMsg.SingleAgentID)
	}
	if !startCycleMsg.NoBranchChange {
		t.Error("expected NoBranchChange to be true")
	}

	// 6. Cancel note triggers transition back to ScreenDetails
	m.FocusIndex = 5 // Focus on Cancel
	_, cmdCancel := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdCancel == nil {
		t.Fatal("expected cancel action to return a command")
	}
	msgCancel := cmdCancel()
	changeScreenMsg, ok = msgCancel.(ChangeScreenMsg)
	if !ok {
		t.Fatalf("expected command to return ChangeScreenMsg, got %T", msgCancel)
	}
	if changeScreenMsg.Screen != ScreenDetails {
		t.Errorf("expected screen to transition to ScreenDetails, got %v", changeScreenMsg.Screen)
	}

	// 7. Esc triggers transition back to ScreenDetails
	m.FocusIndex = 0
	_, cmdEsc := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmdEsc == nil {
		t.Fatal("expected esc action to return a command")
	}
	msgEsc := cmdEsc()
	changeScreenMsgEsc, ok := msgEsc.(ChangeScreenMsg)
	if !ok {
		t.Fatalf("expected command to return ChangeScreenMsg, got %T", msgEsc)
	}
	if changeScreenMsgEsc.Screen != ScreenDetails {
		t.Errorf("expected screen to transition to ScreenDetails, got %v", changeScreenMsgEsc.Screen)
	}
}
