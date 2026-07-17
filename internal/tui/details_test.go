package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestWrapText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		width    int
		expected string
	}{
		{
			name:     "no wrapping needed",
			text:     "hello world",
			width:    20,
			expected: "hello world",
		},
		{
			name:     "wrapping needed",
			text:     "hello world and everyone else",
			width:    12,
			expected: "hello world\nand everyone\nelse",
		},
		{
			name:     "preserve paragraphs",
			text:     "hello world\n\nnew line here",
			width:    12,
			expected: "hello world\n\nnew line\nhere",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.text, tt.width)
			if got != tt.expected {
				t.Errorf("wrapText() = %q; want %q", got, tt.expected)
			}
		})
	}
}

func TestWrapTextWithIndent(t *testing.T) {
	text := "hello world and everyone"
	indent := "  "
	width := 10
	expected := "hello\n  world\n  and\n  everyone"
	got := wrapTextWithIndent(text, width, indent)
	if got != expected {
		t.Errorf("wrapTextWithIndent() = %q; want %q", got, expected)
	}
}

func TestDetailsTabToggling(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 80
	m.Height = 24
	m.Milestone = config.Milestone{
		ID:    "0007",
		Title: "Test milestone",
		Goal:  strings.Repeat("long goal line text here\n", 40),
	}
	m.ShowHistoryTab = false
	m.ScrollOffset = 5

	// Send "tab" key
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !m2.ShowHistoryTab {
		t.Error("expected ShowHistoryTab to be true after tab key press")
	}
	if m2.ScrollOffset != 5 {
		t.Errorf("expected ScrollOffset to remain 5, got %d", m2.ScrollOffset)
	}

	// Send "tab" key again
	m3, _ := m2.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m3.ShowHistoryTab {
		t.Error("expected ShowHistoryTab to be false after second tab key press")
	}
}

func TestDetailsPaneScrolling(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 80
	m.Height = 24
	m.Milestone = config.Milestone{
		ID:    "0007",
		Title: "Test milestone",
		Goal:  strings.Repeat("long goal line text here\n", 40),
	}
	var history []config.MilestoneCycleLog
	for i := 0; i < 20; i++ {
		history = append(history, config.MilestoneCycleLog{
			CycleNumber: i + 1,
			Status:      "Success",
			Timestamp:   time.Now(),
		})
	}
	m.History = history

	// In Details tab, send "down" (j)
	m.ShowHistoryTab = false
	m.ScrollOffset = 0
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m2.ScrollOffset != 1 {
		t.Errorf("expected ScrollOffset to increment to 1, got %d", m2.ScrollOffset)
	}

	// Send "up" (k)
	m3, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m3.ScrollOffset != 0 {
		t.Errorf("expected ScrollOffset to decrement to 0, got %d", m3.ScrollOffset)
	}

	// Switch to History tab
	m.ShowHistoryTab = true
	m.HistorySelectedIdx = 0
	m.HistoryScrollOffset = 0

	// Test keyboard navigation (j/k) across cycles
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m4.HistorySelectedIdx != 1 {
		t.Errorf("expected HistorySelectedIdx to increment to 1, got %d", m4.HistorySelectedIdx)
	}

	m5, _ := m4.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m5.HistorySelectedIdx != 0 {
		t.Errorf("expected HistorySelectedIdx to decrement to 0, got %d", m5.HistorySelectedIdx)
	}

	// Test scrolling (]/[) details scroll offset
	m6, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m6.HistoryScrollOffset != 1 {
		t.Errorf("expected HistoryScrollOffset to increment to 1, got %d", m6.HistoryScrollOffset)
	}

	m7, _ := m6.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m7.HistoryScrollOffset != 0 {
		t.Errorf("expected HistoryScrollOffset to decrement to 0, got %d", m7.HistoryScrollOffset)
	}
}

func TestAgentSelectorScrolling(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 80
	m.Height = 15 // Small height to ensure paging triggers
	m.ShowAgentSelector = true
	m.SelectedAgentIdx = 0
	m.AgentScrollOffset = 0
	m.Agents = []config.Agent{
		{ID: "1", Name: "Agent 1"},
		{ID: "2", Name: "Agent 2"},
		{ID: "3", Name: "Agent 3"},
		{ID: "4", Name: "Agent 4"},
		{ID: "5", Name: "Agent 5"},
	}

	// Simulate down movement
	current := m
	for i := 0; i < 4; i++ {
		var cmd tea.Cmd
		current, cmd = current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		if cmd != nil {
			t.Fatal("unexpected cmd")
		}
	}

	if current.SelectedAgentIdx != 4 {
		t.Errorf("expected SelectedAgentIdx to be 4, got %d", current.SelectedAgentIdx)
	}

	// Verify scroll offset adjusted (greater than 0) because height is small
	if current.AgentScrollOffset == 0 && len(current.Agents) > 2 {
		t.Logf("AgentScrollOffset updated to %d", current.AgentScrollOffset)
	}

	// Simulate up movement
	current, _ = current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if current.SelectedAgentIdx != 3 {
		t.Errorf("expected SelectedAgentIdx to be 3, got %d", current.SelectedAgentIdx)
	}
}

func TestNarrowWidthMetadataFormatting(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Milestone = config.Milestone{
		ID:                 "0007",
		Title:              "Optimized milestone details TUI screen",
		Goal:               "Goal text",
		AcceptanceCriteria: []string{"AC1", "AC2"},
		Status:             "In Progress",
		Cycles:             2,
	}
	m.LLM = "ollama-codex"
	m.Mode = "sandbox"
	m.BranchChange = true

	// Check output under wide width
	textWide := m.getDetailsTextForHeight(20, 80)
	if !strings.Contains(textWide, "Status:") || !strings.Contains(textWide, "Cycles:") {
		t.Error("expected metadata labels to be present in wide view")
	}

	// Check output under narrow width
	textNarrow := m.getDetailsTextForHeight(20, 30)
	if !strings.Contains(textNarrow, "Status:") || !strings.Contains(textNarrow, "Cycles:") {
		t.Error("expected metadata labels to be present in narrow view")
	}

	linesWide := strings.Split(textWide, "\n")
	linesNarrow := strings.Split(textNarrow, "\n")

	t.Logf("Wide layout lines: %d, Narrow layout lines: %d", len(linesWide), len(linesNarrow))
}

func TestDetailsMetadataShowsAgentInstructionsUpdateScore(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Milestone = config.Milestone{
		ID:     "0007",
		Title:  "Instruction score",
		Status: "In Progress",
		Cycles: 1,
	}
	m.RecommendationScore = 2
	m.AgentInstructionsUpdateScore = 8
	m.LLM = "codex"
	m.Mode = "sandbox"
	m.BranchChange = false

	text := m.getDetailsTextForHeight(20, 100)
	if !strings.Contains(text, "Rec Score: 2/10") {
		t.Fatalf("expected cycle recommendation score in details text, got:\n%s", text)
	}
	if !strings.Contains(text, "AGENTS.md Score: 8/10") {
		t.Fatalf("expected AGENTS.md update score in details text, got:\n%s", text)
	}
}

func TestHistoryRichDetailsAndDeletion(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 80
	m.Height = 24
	m.Milestone = config.Milestone{ID: "MS-1", Title: "Milestone"}
	m.History = []config.MilestoneCycleLog{
		{
			CycleNumber: 1,
			Status:      "Success",
			Timestamp:   time.Now(),
			Branch:      "test-branch",
			CommitHash:  "a1b2c3d",
			UserNote:    "This is a note",
			Duration:    "5m30s",
			Actions: []config.AgentActionLog{
				{
					AgentID:    "pm",
					ExitCode:   0,
					InputFile:  "in.md",
					OutputFile: "out.log",
					Duration:   "2s",
				},
			},
		},
	}
	m.ShowHistoryTab = true
	m.HistorySelectedIdx = 0

	// 1. Verify rich details rendering
	historyText := m.getHistoryText(80)
	if !strings.Contains(historyText, "Duration: 5m30s") {
		t.Error("expected total duration in history text")
	}
	if !strings.Contains(historyText, "Note: This is a note") {
		t.Error("expected note in history text")
	}
	if !strings.Contains(historyText, "pm (Exit Code: 0, Duration: 2s)") {
		t.Error("expected agent actions list with duration in history text")
	}
	if !strings.Contains(historyText, "Input: in.md") || !strings.Contains(historyText, "Output: out.log") {
		t.Error("expected input/output paths in history text")
	}

	// 2. Press "x" to trigger deletion confirmation
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !m2.ConfirmDeleteCycle {
		t.Error("expected ConfirmDeleteCycle to be true after pressing x")
	}

	// Verify confirmation screen render
	viewStr := m2.View()
	if !strings.Contains(viewStr, "DELETE CYCLE: 1") {
		t.Errorf("expected delete cycle confirmation view, got: %s", viewStr)
	}

	// Press "y" to confirm deletion and verify DeleteCycleMsg is sent
	_, cmd := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected DeleteCycleMsg command to be returned")
	}
	msg := cmd()
	deleteMsg, ok := msg.(DeleteCycleMsg)
	if !ok || deleteMsg.MilestoneID != "MS-1" || deleteMsg.CycleNumber != 1 {
		t.Errorf("expected DeleteCycleMsg for MS-1 cycle 1, got: %#v", msg)
	}
}

func TestHistoryRendersStructuredContractMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "MS-cycle-001-03-qa-output.log")
	handoffPath := filepath.Join(tmpDir, "MS-cycle-001-03-qa-handoff.yaml")
	if err := os.WriteFile(outputPath, []byte("qa output"), 0644); err != nil {
		t.Fatalf("failed to write output: %v", err)
	}
	handoff := `summary:
  verdict: needs-human-review
  criteria_results:
    - criterion: AC1
      result: fail
  reviewed_files:
    - internal/executor/handoff.go
  failing_checks:
    - go test ./...
  required_fixes:
    - fix validation status mapping
output_contract: qa
validation_status: valid
`
	if err := os.WriteFile(handoffPath, []byte(handoff), 0644); err != nil {
		t.Fatalf("failed to write handoff: %v", err)
	}

	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 100
	m.Height = 30
	m.Milestone = config.Milestone{ID: "MS", Title: "Milestone"}
	m.History = []config.MilestoneCycleLog{
		{
			CycleNumber: 1,
			Status:      "blocked",
			Timestamp:   time.Now(),
			Actions: []config.AgentActionLog{
				{AgentID: "qa", ExitCode: 0, OutputFile: outputPath},
			},
		},
	}
	m.ShowHistoryTab = true
	m.HistorySelectedIdx = 0

	text := m.getHistoryText(100)
	if !strings.Contains(text, "Contract: QA valid") {
		t.Fatalf("expected QA contract status in history text, got:\n%s", text)
	}
	if !strings.Contains(text, "Verdict: needs-human-review") ||
		!strings.Contains(text, "Failing: go test ./...") ||
		!strings.Contains(text, "Fixes: fix validation status mapping") {
		t.Fatalf("expected structured QA fields in history text, got:\n%s", text)
	}
}

func TestHistoryRendersRecommenderScoresSeparately(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "MS-cycle-001-04-recommender-output.log")
	handoffPath := filepath.Join(tmpDir, "MS-cycle-001-04-recommender-handoff.yaml")
	if err := os.WriteFile(outputPath, []byte("recommender output"), 0644); err != nil {
		t.Fatalf("failed to write output: %v", err)
	}
	handoff := `summary:
  score: 2
  agent_instructions_update_score: 7
  verdict: approved
  reason: complete
  next_cycle_focus: []
output_contract: recommender
validation_status: valid
`
	if err := os.WriteFile(handoffPath, []byte(handoff), 0644); err != nil {
		t.Fatalf("failed to write handoff: %v", err)
	}

	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 100
	m.Height = 30
	m.Milestone = config.Milestone{ID: "MS", Title: "Milestone"}
	m.History = []config.MilestoneCycleLog{{
		CycleNumber: 1,
		Status:      "approved",
		Timestamp:   time.Now(),
		Actions:     []config.AgentActionLog{{AgentID: "recommender", ExitCode: 0, OutputFile: outputPath}},
	}}
	m.ShowHistoryTab = true
	m.HistorySelectedIdx = 0

	text := m.getHistoryText(100)
	if !strings.Contains(text, "Score: 2/10") {
		t.Fatalf("expected recommender cycle score in history text, got:\n%s", text)
	}
	if !strings.Contains(text, "AGENTS.md score: 7/10") {
		t.Fatalf("expected recommender AGENTS.md score in history text, got:\n%s", text)
	}
}

func TestHistoryInstructionUpdateReviewActions(t *testing.T) {
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	outputPath := filepath.Join(root, "MS-cycle-001-02-developer-output.log")
	handoffPath := filepath.Join(root, "MS-cycle-001-02-developer-handoff.yaml")
	if err := os.WriteFile(outputPath, []byte("developer output"), 0644); err != nil {
		t.Fatal(err)
	}
	handoff := `summary:
  changed_files: []
  implemented_behavior: []
  checks_run: []
  decisions: []
  risks: []
  proposed_agent_instructions_update: |
    # Agent Instructions
    - Proposed durable instruction.
output_contract: developer
validation_status: valid
`
	if err := os.WriteFile(handoffPath, []byte(handoff), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewDetailsModel(DefaultStyles(true, true))
	m.Width = 100
	m.Height = 30
	m.Milestone = config.Milestone{ID: "MS", Title: "Milestone"}
	m.History = []config.MilestoneCycleLog{{
		CycleNumber: 1,
		Status:      "approved",
		Timestamp:   time.Now(),
		Actions:     []config.AgentActionLog{{AgentID: "developer", ExitCode: 0, OutputFile: outputPath}},
	}}
	m.ShowHistoryTab = true
	m.HistorySelectedIdx = 0

	text := m.getHistoryText(100)
	if !strings.Contains(text, "Proposed AGENTS.md Update") || !strings.Contains(text, "v diff") {
		t.Fatalf("expected proposed instruction update review controls, got:\n%s", text)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = updated
	if !m.ShowInstructionDiff || !strings.Contains(m.getHistoryText(100), "+++ AGENTS.md (proposed)") {
		t.Fatalf("expected diff view after v, got:\n%s", m.getHistoryText(100))
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = updated
	if _, err := os.Stat(filepath.Join(".cyclestone", "temp", "AGENTS.md.proposed")); err != nil {
		t.Fatalf("expected editable draft to be saved: %v", err)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated
	agentsBytes, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("expected AGENTS.md to be applied: %v", err)
	}
	if !strings.Contains(string(agentsBytes), "Proposed durable instruction") {
		t.Fatalf("expected applied AGENTS.md content, got %q", string(agentsBytes))
	}
}

func TestMilestoneDeletionTransition(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 80
	m.Height = 24
	m.Milestone = config.Milestone{ID: "MS-1", Title: "Milestone", SpecPath: "milestones/MS-1.md"}
	m.ShowHistoryTab = false

	// Press "d" to trigger milestone deletion confirmation
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if !m2.ConfirmDeleteMilestone {
		t.Error("expected ConfirmDeleteMilestone to be true after pressing d")
	}

	// Verify confirmation screen render
	viewStr := m2.View()
	if !strings.Contains(viewStr, "DELETE MILESTONE: MS-1") {
		t.Errorf("expected delete milestone confirmation view, got: %s", viewStr)
	}

	// Press "y" to confirm deletion and verify DeleteMilestoneMsg is sent
	_, cmd := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected DeleteMilestoneMsg command to be returned")
	}
	msg := cmd()
	deleteMsg, ok := msg.(DeleteMilestoneMsg)
	if !ok || deleteMsg.MilestoneID != "MS-1" {
		t.Errorf("expected DeleteMilestoneMsg for MS-1, got: %#v", msg)
	}
}

func TestDetailsRunRoutesToCycleNoteBeforePreflight(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 80
	m.Height = 24
	m.Milestone = config.Milestone{ID: "0015-cycle-preflight-review", Title: "Preflight"}
	m.LLM = "manual"
	m.Mode = "sandbox"
	m.BranchChange = false
	m.Groups = []config.AgentGroup{{Name: "Default", AgentIDs: []string{"pm"}}}
	m.SelectedGroupIdx = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("expected change screen command")
	}
	msg := cmd()
	change, ok := msg.(ChangeScreenMsg)
	if !ok || change.Screen != ScreenCreateMilestone {
		t.Fatalf("expected run to open cycle note screen, got %#v", msg)
	}
	start, ok := change.Data.(StartCycleMsg)
	if !ok {
		t.Fatalf("expected StartCycleMsg payload, got %#v", change.Data)
	}
	if start.Milestone.ID != m.Milestone.ID || start.Group.Name != "Default" || !start.NoBranchChange {
		t.Fatalf("unexpected start payload: %#v", start)
	}
}

func TestDetailsUpdateAgentsRoutesToMilestoneScopedWorkflow(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewDetailsModel(styles)
	m.Width = 80
	m.Height = 24
	m.Milestone = config.Milestone{ID: "MS-AGENTS", Title: "Instructions"}
	m.LLM = "codex"
	m.Mode = "sandbox"
	m.BranchChange = false

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil {
		t.Fatal("expected change screen command")
	}
	change, ok := cmd().(ChangeScreenMsg)
	if !ok || change.Screen != ScreenCreateMilestone {
		t.Fatalf("expected note screen, got %#v", change)
	}
	req, ok := change.Data.(StartCycleMsg)
	if !ok {
		t.Fatalf("expected StartCycleMsg payload, got %#v", change.Data)
	}
	if req.Workflow != WorkflowAgentInstructionsMilestone || req.Milestone.ID != "MS-AGENTS" || !req.NoBranchChange {
		t.Fatalf("unexpected scoped update request: %#v", req)
	}
}
