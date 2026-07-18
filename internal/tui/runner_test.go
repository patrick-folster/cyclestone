package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/executor"
)

func TestPadLogLines(t *testing.T) {
	tests := []struct {
		name         string
		lines        []string
		targetHeight int
		expectedLen  int
	}{
		{
			name:         "empty slice, positive target",
			lines:        []string{},
			targetHeight: 5,
			expectedLen:  5,
		},
		{
			name:         "shorter slice than target",
			lines:        []string{"line1", "line2"},
			targetHeight: 4,
			expectedLen:  4,
		},
		{
			name:         "longer slice than target",
			lines:        []string{"line1", "line2", "line3", "line4"},
			targetHeight: 2,
			expectedLen:  4, // should not be truncated by padLogLines
		},
		{
			name:         "equal size",
			lines:        []string{"line1", "line2"},
			targetHeight: 2,
			expectedLen:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := padLogLines(tt.lines, tt.targetHeight)
			if len(res) != tt.expectedLen {
				t.Errorf("expected len %d, got %d", tt.expectedLen, len(res))
			}
			// Check padding values
			for i := len(tt.lines); i < tt.targetHeight; i++ {
				if res[i] != "" {
					t.Errorf("expected empty string at index %d, got %q", i, res[i])
				}
			}
		})
	}
}

func TestRunnerTabSwitching(t *testing.T) {
	styles := DefaultStyles(true, true)

	t.Run("tab switching in small resolution", func(t *testing.T) {
		m := NewRunnerModel(styles)
		m.Height = 15 // Small resolution
		m.Width = 80

		// Active tab starts at RunnerTabLog
		if m.ActiveTab != RunnerTabLog {
			t.Errorf("expected initial tab to be RunnerTabLog, got %v", m.ActiveTab)
		}

		// Tab switches to RunnerTabPlan
		m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		if cmd != nil {
			t.Error("expected cmd to be nil for tab key switch")
		}
		if m2.ActiveTab != RunnerTabPlan {
			t.Errorf("expected tab to switch to RunnerTabPlan, got %v", m2.ActiveTab)
		}

		// Tab switches back to RunnerTabLog
		m3, _ := m2.Update(tea.KeyMsg{Type: tea.KeyTab})
		if m3.ActiveTab != RunnerTabLog {
			t.Errorf("expected tab to switch back to RunnerTabLog, got %v", m3.ActiveTab)
		}

		// Right arrow switches to RunnerTabPlan
		m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		if m4.ActiveTab != RunnerTabPlan {
			t.Errorf("expected right key to switch to RunnerTabPlan, got %v", m4.ActiveTab)
		}

		// Left arrow switches back to RunnerTabLog from RunnerTabPlan
		m5, _ := m4.Update(tea.KeyMsg{Type: tea.KeyLeft})
		if m5.ActiveTab != RunnerTabLog {
			t.Errorf("expected left key to switch to RunnerTabLog, got %v", m5.ActiveTab)
		}

		// Shift+Tab switches to RunnerTabPlan
		m6, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		if m6.ActiveTab != RunnerTabPlan {
			t.Errorf("expected shift+tab to switch to RunnerTabPlan, got %v", m6.ActiveTab)
		}
	})

	t.Run("tab switching ignored in standard resolution", func(t *testing.T) {
		m := NewRunnerModel(styles)
		m.Height = 25 // Standard/large resolution
		m.Width = 80

		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		if m2.ActiveTab != RunnerTabLog {
			t.Errorf("expected tab switch to be ignored in standard resolution, got %v", m2.ActiveTab)
		}
	})
}

func TestRunnerViews(t *testing.T) {
	styles := DefaultStyles(true, true)
	milestone := config.Milestone{
		ID:    "0008",
		Title: "Optimize running screen",
	}

	pipeline := []config.Agent{
		{ID: "pm", Name: "Product Manager"},
		{ID: "developer", Name: "Developer"},
	}

	t.Run("standard/large resolution view layout", func(t *testing.T) {
		m := NewRunnerModel(styles)
		m.Milestone = milestone
		m.Pipeline = pipeline
		m.Height = 25
		m.Width = 80
		m.Logs = []string{"initializing...", "step 1 completed"}

		view := m.View()
		if !strings.Contains(view, "Agent Workflow Pipeline:") {
			t.Error("expected standard view to contain 'Agent Workflow Pipeline:'")
		}
		if !strings.Contains(view, "Logs Output (Live Tail):") {
			t.Error("expected standard view to contain 'Logs Output (Live Tail):'")
		}
		if strings.Contains(view, "LOG") && strings.Contains(view, "PLAN") {
			// Tab headers shouldn't be printed in standard view
			if strings.Contains(view, "Active Agent:") {
				t.Error("expected standard view NOT to show active agent block")
			}
		}
	})

	t.Run("small resolution view - LOG tab active", func(t *testing.T) {
		m := NewRunnerModel(styles)
		m.Milestone = milestone
		m.Pipeline = pipeline
		m.Height = 15
		m.Width = 80
		m.ActiveTab = RunnerTabLog
		m.AgentStates["developer"] = "running"
		m.Logs = []string{"running developer task..."}

		view := m.View()
		plainView := stripANSI(view)
		// Tab bar should be visible
		if !strings.Contains(plainView, "LOG") || !strings.Contains(plainView, "PLAN") {
			t.Error("expected tabbed view to display LOG and PLAN tabs")
		}
		// Should show active agent
		if !strings.Contains(plainView, "Active Agent: Developer") {
			t.Error("expected LOG tab to show active agent name")
		}
		// Should show logs
		if !strings.Contains(plainView, "Logs Output (Live Tail):") {
			t.Error("expected LOG tab to show logs section")
		}
		if !strings.Contains(plainView, "running developer task...") {
			t.Error("expected LOG tab to contain actual logs")
		}
		// Should NOT show the full list of agents
		if strings.Contains(plainView, "Agent Workflow Pipeline:") {
			t.Error("expected LOG tab to hide full pipeline list")
		}
		// Help instructions should contain "Tab" and "Switch Tab"
		if !strings.Contains(plainView, "Tab") || !strings.Contains(plainView, "Switch Tab") {
			t.Error("expected small resolution help instructions to include 'Tab' and 'Switch Tab'")
		}
	})

	t.Run("small resolution view - PLAN tab active", func(t *testing.T) {
		m := NewRunnerModel(styles)
		m.Milestone = milestone
		m.Pipeline = pipeline
		m.Height = 15
		m.Width = 80
		m.ActiveTab = RunnerTabPlan
		m.AgentStates["developer"] = "running"
		m.Logs = []string{"running developer task..."}

		view := m.View()
		plainView := stripANSI(view)
		// Tab bar should be visible
		if !strings.Contains(plainView, "LOG") || !strings.Contains(plainView, "PLAN") {
			t.Error("expected tabbed view to display LOG and PLAN tabs")
		}
		// Should show the full list of agents
		if !strings.Contains(plainView, "Agent Workflow Pipeline:") {
			t.Error("expected PLAN tab to show full pipeline list")
		}
		// Should NOT show active agent line
		if strings.Contains(plainView, "Active Agent:") {
			t.Error("expected PLAN tab to hide active agent details")
		}
		// Should NOT show logs
		if strings.Contains(plainView, "Logs Output (Live Tail):") {
			t.Error("expected PLAN tab to hide logs section")
		}
		if strings.Contains(plainView, "running developer task...") {
			t.Error("expected PLAN tab to hide logs content")
		}
	})
}

func TestRunnerStatusRenderingAndElapsed(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewRunnerModel(styles)
	m.Milestone = config.Milestone{ID: "0017", Title: "Live Runner Status"}
	m.Pipeline = []config.Agent{{ID: "developer", Name: "Developer"}}
	m.Width = 100
	m.Height = 28
	m.StartedAt = time.Now().Add(-90 * time.Second)
	m.AgentStartedAt["developer"] = time.Now().Add(-12 * time.Second)

	m, _ = m.Update(executor.AgentStartedMsg{AgentID: "developer"})
	m, _ = m.Update(executor.RunnerStatusMsg{
		CycleNumber:      2,
		CycleStatus:      "running",
		Phase:            "developer",
		AgentID:          "developer",
		Runner:           "ollama",
		Model:            "qwen3-coder:480b-cloud",
		Mode:             "sandbox",
		ReportFile:       ".cyclestone/reports/0017-cycle-002.md",
		OutputFile:       ".cyclestone/reports/0017-cycle-002-developer-output.log",
		LatestCommand:    "aider --model ollama_chat/qwen3-coder:480b-cloud",
		ModelCalls:       3,
		ToolCalls:        2,
		EstimatedTokens:  1200,
		PromptTokens:     700,
		CompletionTokens: 90,
		MaxModelCalls:    50,
		MaxTokenBudget:   1000000,
		LatestToolCall:   "exec_command",
	})

	plainView := stripANSI(m.View())
	for _, want := range []string{
		"running | cycle 002 | phase developer",
		"Runner: ollama | Model: qwen3-coder:480b-cloud | Mode: sandbox",
		"Report: .cyclestone/reports/0017-cycle-002.md",
		"Latest command: aider --model ollama_chat/qwen3-coder:480b-cloud",
		"Latest tool call: exec_command",
		"Budget: model calls 3/50 | tool calls 2 | est tokens 1200/1000000 | actual tokens prompt 700",
		"completion 90",
		"Developer",
	} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("expected runner view to contain %q\nview:\n%s", want, plainView)
		}
	}
}

func TestRunnerAgentInstructionsProposalReviewActions(t *testing.T) {
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "temp"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("AGENTS.md", []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentInstructionsDraftPath(), []byte("proposed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewRunnerModel(DefaultStyles(true, true))
	m.Width = 100
	m.Height = 28
	m.Finished = true
	m.Workflow = WorkflowAgentInstructionsRepository

	view := stripANSI(m.View())
	for _, want := range []string{"Proposal Draft: .cyclestone/temp/AGENTS.md.proposed", "proposed", "Apply-AGENTS", "Save-Draft"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected runner proposal view to contain %q, got:\n%s", want, view)
		}
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if !strings.Contains(m.Status, "Saved editable AGENTS.md draft") {
		t.Fatalf("expected save-draft status, got %q", m.Status)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	data, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "proposed\n" {
		t.Fatalf("expected explicit apply to write proposal, got %q", string(data))
	}
	if !strings.Contains(m.Status, "Applied AGENTS.md proposal") {
		t.Fatalf("expected apply status, got %q", m.Status)
	}
}

func TestRunnerStandardViewKeepsLiveLogFrameStable(t *testing.T) {
	m := NewRunnerModel(DefaultStyles(true, true))
	m.Milestone = config.Milestone{ID: "0007", Title: "Stabilize Live Output"}
	m.Pipeline = []config.Agent{{ID: "pm", Name: "PM"}, {ID: "developer", Name: "Developer"}, {ID: "qa", Name: "QA"}}
	m.Width = 104
	m.Height = 32
	m.CycleStatus = "failed"
	m.ActivePhase = "developer"
	m.ActiveAgentID = "developer"
	m.Runner = "codex"
	m.Model = "gpt-5"
	m.Mode = "sandbox"
	m.ReportFile = ".cyclestone/reports/report.md"
	m.OutputFile = ".cyclestone/reports/output.log"
	m.LatestCommand = "codex run"
	m.LatestToolCall = "exec_command"
	m.ModelCalls = 1
	m.ToolCalls = 1
	m.EstimatedTokens = 10
	m.LastError = "failed"
	m.NextSuggestedAction = "retry"
	m.Logs = []string{"baseline log"}

	baseView := stripANSI(m.View())
	baseWidth, baseHeight := renderedSize(baseView)
	baseLogWidth, baseLogHeight := logFrameSize(t, baseView)

	m.ReportFile = ".cyclestone/reports/" + strings.Repeat("very-long-report-path-", 8) + "report.md"
	m.OutputFile = ".cyclestone/reports/" + strings.Repeat("very-long-output-path-", 8) + "output.log"
	m.LatestCommand = strings.Repeat("codex exec with many arguments ", 8)
	m.LatestToolCall = strings.Repeat("execute_document_command ", 6)
	m.ModelCalls = 123
	m.ToolCalls = 456
	m.EstimatedTokens = 789012
	m.PromptTokens = 345678
	m.CompletionTokens = 901234
	m.MaxModelCalls = 999
	m.MaxTokenBudget = 2000000
	m.StopOrDoneReason = strings.Repeat("budget-check ", 8)
	m.LastError = strings.Repeat("long failure reason ", 10)
	m.NextSuggestedAction = strings.Repeat("review logs and retry ", 8)
	for i := 0; i < 80; i++ {
		m.Logs = append(m.Logs, strings.Repeat("live output line ", 12)+fmt.Sprintf("%02d", i))
	}

	grownView := stripANSI(m.View())
	grownWidth, grownHeight := renderedSize(grownView)
	grownLogWidth, grownLogHeight := logFrameSize(t, grownView)
	if grownWidth != baseWidth || grownHeight != baseHeight {
		t.Fatalf("runner view dimensions changed from %dx%d to %dx%d", baseWidth, baseHeight, grownWidth, grownHeight)
	}
	if grownLogWidth != baseLogWidth || grownLogHeight != baseLogHeight {
		t.Fatalf("runner log frame changed from %dx%d to %dx%d", baseLogWidth, baseLogHeight, grownLogWidth, grownLogHeight)
	}
	for _, want := range []string{"Status:", "Logs Output (Live Tail):", "Esc", "Cancel"} {
		if !strings.Contains(grownView, want) {
			t.Fatalf("expected grown runner view to keep %q visible\n%s", want, grownView)
		}
	}
}

func TestRunnerStandardViewKeepsLiveLogFrameStableAcrossAbsentToPresentStatus(t *testing.T) {
	m := NewRunnerModel(DefaultStyles(true, true))
	m.Milestone = config.Milestone{ID: "0007", Title: "Stabilize Live Output"}
	m.Pipeline = []config.Agent{{ID: "pm", Name: "PM"}, {ID: "developer", Name: "Developer"}, {ID: "qa", Name: "QA"}}
	m.Width = 112
	m.Height = 34

	baseView := stripANSI(m.View())
	baseWidth, baseHeight := renderedSize(baseView)
	baseLogWidth, baseLogHeight := logFrameSize(t, baseView)

	m, _ = m.Update(executor.RunnerStatusMsg{
		CycleNumber:         2,
		CycleStatus:         "running",
		Phase:               "developer",
		AgentID:             "developer",
		Runner:              "codex",
		Model:               "gpt-5",
		Mode:                "workspace-write",
		ReportFile:          ".cyclestone/reports/" + strings.Repeat("long-report-path-", 8) + "report.md",
		OutputFile:          ".cyclestone/reports/" + strings.Repeat("long-output-path-", 8) + "developer-output.log",
		LatestCommand:       strings.Repeat("codex exec --json with wrapped command text ", 5),
		LatestToolCall:      strings.Repeat("execute_document_command ", 5),
		ModelCalls:          12,
		ToolCalls:           34,
		EstimatedTokens:     56789,
		PromptTokens:        12345,
		CompletionTokens:    6789,
		MaxModelCalls:       50,
		MaxTokenBudget:      1000000,
		StopOrDoneReason:    strings.Repeat("budget ", 10),
		NextSuggestedAction: strings.Repeat("continue after reviewing the generated report ", 4),
	})
	m, _ = m.Update(executor.CycleFinishedMsg{
		MilestoneID: "0007",
		CycleNumber: 2,
		Status:      "passed",
		ReportFile:  ".cyclestone/reports/" + strings.Repeat("finished-report-path-", 8) + "report.md",
	})
	m.LastError = strings.Repeat("historical failure summary ", 8)
	for i := 0; i < 90; i++ {
		m.Logs = append(m.Logs, strings.Repeat("live lifecycle log line ", 9)+fmt.Sprintf("%02d", i))
	}

	grownView := stripANSI(m.View())
	grownWidth, grownHeight := renderedSize(grownView)
	grownLogWidth, grownLogHeight := logFrameSize(t, grownView)
	if grownWidth != baseWidth || grownHeight != baseHeight {
		t.Fatalf("runner absent-to-present dimensions changed from %dx%d to %dx%d", baseWidth, baseHeight, grownWidth, grownHeight)
	}
	if grownLogWidth != baseLogWidth || grownLogHeight != baseLogHeight {
		t.Fatalf("runner absent-to-present log frame changed from %dx%d to %dx%d", baseLogWidth, baseLogHeight, grownLogWidth, grownLogHeight)
	}
	for _, want := range []string{"Status:", "Logs Output (Live Tail):", "Esc", "Backspace", "q", "Quit"} {
		if !strings.Contains(grownView, want) {
			t.Fatalf("expected completed runner view to keep %q visible\n%s", want, grownView)
		}
	}
}

func TestRunnerSmallLogTabKeepsLiveLogFrameStable(t *testing.T) {
	m := NewRunnerModel(DefaultStyles(true, true))
	m.Milestone = config.Milestone{ID: "0007", Title: "Small Runner"}
	m.Pipeline = []config.Agent{{ID: "developer", Name: "Developer"}}
	m.Width = 76
	m.Height = 16
	m.ActiveTab = RunnerTabLog
	m.AgentStates["developer"] = "running"
	m.ActiveAgentID = "developer"
	m.ActivePhase = "developer"
	m.Logs = []string{"short log"}

	baseView := stripANSI(m.View())
	baseWidth, baseHeight := renderedSize(baseView)
	baseLogWidth, baseLogHeight := logFrameSize(t, baseView)

	m.Status = strings.Repeat("very long status ", 12)
	for i := 0; i < 50; i++ {
		m.Logs = append(m.Logs, strings.Repeat("overflow small log ", 8)+fmt.Sprintf("%02d", i))
	}
	grownView := stripANSI(m.View())
	grownWidth, grownHeight := renderedSize(grownView)
	grownLogWidth, grownLogHeight := logFrameSize(t, grownView)
	if grownWidth != baseWidth || grownHeight != baseHeight {
		t.Fatalf("small runner dimensions changed from %dx%d to %dx%d", baseWidth, baseHeight, grownWidth, grownHeight)
	}
	if grownLogWidth != baseLogWidth || grownLogHeight != baseLogHeight {
		t.Fatalf("small runner log frame changed from %dx%d to %dx%d", baseLogWidth, baseLogHeight, grownLogWidth, grownLogHeight)
	}
	for _, want := range []string{"Active Agent: Developer", "Logs Output (Live Tail):", "Tab", "Switch Tab"} {
		if !strings.Contains(grownView, want) {
			t.Fatalf("expected small runner LOG tab to keep %q visible\n%s", want, grownView)
		}
	}
}

func TestRunnerAgentInstructionsProposalViewKeepsLiveLogFrameStable(t *testing.T) {
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "temp"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentInstructionsDraftPath(), []byte("short proposal\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewRunnerModel(DefaultStyles(true, true))
	m.Width = 104
	m.Height = 34
	m.Finished = true
	m.Workflow = WorkflowAgentInstructionsRepository
	m.Milestone = config.Milestone{ID: "AGENTS.md", Title: "Repository update"}
	m.Pipeline = []config.Agent{{ID: "updater", Name: "Updater"}}
	m.CycleStatus = "finished"
	m.FinalVerdict = "passed"
	m.Logs = []string{"proposal generated"}

	baseView := stripANSI(m.View())
	baseWidth, baseHeight := renderedSize(baseView)
	baseLogWidth, baseLogHeight := logFrameSize(t, baseView)

	longProposal := strings.Repeat("## Section\nLong proposed AGENTS.md guidance with enough words to wrap across the proposal preview budget.\n", 80)
	if err := os.WriteFile(agentInstructionsDraftPath(), []byte(longProposal), 0644); err != nil {
		t.Fatal(err)
	}
	m.Status = strings.Repeat("Saved editable AGENTS.md draft status ", 8)
	m.NextSuggestedAction = strings.Repeat("Review git diff before committing ", 6)
	for i := 0; i < 40; i++ {
		m.Logs = append(m.Logs, strings.Repeat("agents update live log ", 10)+fmt.Sprintf("%02d", i))
	}

	grownView := stripANSI(m.View())
	grownWidth, grownHeight := renderedSize(grownView)
	grownLogWidth, grownLogHeight := logFrameSize(t, grownView)
	if grownWidth != baseWidth || grownHeight != baseHeight {
		t.Fatalf("AGENTS proposal view dimensions changed from %dx%d to %dx%d", baseWidth, baseHeight, grownWidth, grownHeight)
	}
	if grownLogWidth != baseLogWidth || grownLogHeight != baseLogHeight {
		t.Fatalf("AGENTS proposal log frame changed from %dx%d to %dx%d", baseLogWidth, baseLogHeight, grownLogWidth, grownLogHeight)
	}
	for _, want := range []string{"Proposal Draft: .cyclestone/temp/AGENTS.md.proposed", "Apply-AGENTS", "Save-Draft", "Logs Output (Live Tail):"} {
		if !strings.Contains(grownView, want) {
			t.Fatalf("expected AGENTS proposal view to keep %q visible\n%s", want, grownView)
		}
	}
}

func TestRunnerAgentInstructionsTransitionKeepsLiveLogFrameStable(t *testing.T) {
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "temp"), 0755); err != nil {
		t.Fatal(err)
	}

	m := NewRunnerModel(DefaultStyles(true, true))
	m.Width = 108
	m.Height = 35
	m.Workflow = WorkflowAgentInstructionsRepository
	m.Milestone = config.Milestone{ID: "AGENTS.md", Title: "Repository update"}
	m.Pipeline = []config.Agent{{ID: "updater", Name: "Updater"}}
	m.Logs = []string{"starting AGENTS.md update"}

	baseView := stripANSI(m.View())
	baseWidth, baseHeight := renderedSize(baseView)
	baseLogWidth, baseLogHeight := logFrameSize(t, baseView)

	longProposal := strings.Repeat("# Guidance\nUse bounded runner output sections with enough text to wrap through the proposal preview.\n", 80)
	if err := os.WriteFile(agentInstructionsDraftPath(), []byte(longProposal), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ = m.Update(executor.RunnerStatusMsg{
		CycleStatus:         "running",
		Phase:               "updater",
		AgentID:             "updater",
		Runner:              "codex",
		Model:               "gpt-5",
		Mode:                "workspace-write",
		ReportFile:          ".cyclestone/reports/" + strings.Repeat("agents-report-", 7) + "report.md",
		OutputFile:          ".cyclestone/reports/" + strings.Repeat("agents-output-", 7) + "output.log",
		LatestCommand:       strings.Repeat("codex run update agent instructions ", 5),
		ModelCalls:          3,
		ToolCalls:           7,
		EstimatedTokens:     22222,
		MaxModelCalls:       10,
		MaxTokenBudget:      100000,
		NextSuggestedAction: strings.Repeat("review and apply the AGENTS.md proposal ", 5),
	})
	m.Finished = true
	m.CycleStatus = "finished"
	m.FinalVerdict = "passed"
	m.Status = strings.Repeat("AGENTS.md proposal generated and ready for review ", 4)
	for i := 0; i < 70; i++ {
		m.Logs = append(m.Logs, strings.Repeat("agents proposal live log ", 9)+fmt.Sprintf("%02d", i))
	}

	finishedView := stripANSI(m.View())
	finishedWidth, finishedHeight := renderedSize(finishedView)
	finishedLogWidth, finishedLogHeight := logFrameSize(t, finishedView)
	if finishedWidth != baseWidth || finishedHeight != baseHeight {
		t.Fatalf("AGENTS absent-to-proposal dimensions changed from %dx%d to %dx%d", baseWidth, baseHeight, finishedWidth, finishedHeight)
	}
	if finishedLogWidth != baseLogWidth || finishedLogHeight != baseLogHeight {
		t.Fatalf("AGENTS absent-to-proposal log frame changed from %dx%d to %dx%d", baseLogWidth, baseLogHeight, finishedLogWidth, finishedLogHeight)
	}
	for _, want := range []string{"Proposal Draft: .cyclestone/temp/AGENTS.md.proposed", "Apply-AGENTS", "Save-Draft", "Logs Output (Live Tail):"} {
		if !strings.Contains(finishedView, want) {
			t.Fatalf("expected finished AGENTS view to keep %q visible\n%s", want, finishedView)
		}
	}
}

func TestRunnerFailureSummaryRendering(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewRunnerModel(styles)
	m.Milestone = config.Milestone{ID: "0017", Title: "Live Runner Status"}
	m.Pipeline = []config.Agent{{ID: "qa", Name: "QA"}}
	m.Width = 100
	m.Height = 28
	m.StartedAt = time.Now().Add(-10 * time.Second)

	m, _ = m.Update(executor.RunnerStatusMsg{
		CycleNumber:         1,
		CycleStatus:         "failed",
		Phase:               "qa",
		AgentID:             "qa",
		OutputFile:          ".cyclestone/reports/0017-cycle-001-qa-output.log",
		ReportFile:          ".cyclestone/reports/0017-cycle-001.md",
		LastError:           "agent QA failed with exit code 1",
		NextSuggestedAction: "Review the output log and rerun the cycle after fixing the failure.",
	})

	plainView := stripANSI(m.View())
	for _, want := range []string{
		"Summary: failed",
		"Agent: qa",
		"Duration:",
		"Reason: agent QA failed with exit code 1",
		"Output: .cyclestone/reports/0017-cycle-001-qa-output.log",
		"Next: Review the output log and rerun the cycle after fixing the failure.",
	} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("expected failure summary to contain %q\nview:\n%s", want, plainView)
		}
	}
}

func TestRunnerRedactsSecretsAndBoundsRetention(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewRunnerModel(styles)

	for i := 0; i < maxRunnerLogLines+25; i++ {
		line := "line"
		if i == maxRunnerLogLines+24 {
			line = "Authorization: Bearer sk-secretVALUE1234567890 OPENAI_API_KEY=sk-anotherSECRET1234567890"
		}
		m, _ = m.Update(executor.AgentProgressMsg{AgentID: "developer", LogLine: line})
	}
	if len(m.Logs) != maxRunnerLogLines {
		t.Fatalf("expected %d retained logs, got %d", maxRunnerLogLines, len(m.Logs))
	}
	last := m.Logs[len(m.Logs)-1]
	if strings.Contains(last, "secretVALUE") || strings.Contains(last, "anotherSECRET") {
		t.Fatalf("expected secrets to be redacted, got %q", last)
	}
	if !strings.Contains(last, "[REDACTED]") {
		t.Fatalf("expected redaction marker, got %q", last)
	}

	for i := 0; i < maxRunnerStatusLines+5; i++ {
		m, _ = m.Update(executor.RunnerStatusMsg{CycleStatus: "running", Phase: "developer", OutputFile: "OPENAI_API_KEY: secretvalue"})
	}
	if len(m.StatusEvents) != maxRunnerStatusLines {
		t.Fatalf("expected %d retained status events, got %d", maxRunnerStatusLines, len(m.StatusEvents))
	}
	if strings.Contains(m.OutputFile, "secretvalue") {
		t.Fatalf("expected structured output field to be redacted, got %q", m.OutputFile)
	}
}

func TestRunnerFinishedSummaryRendering(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewRunnerModel(styles)
	m.Milestone = config.Milestone{ID: "0017", Title: "Live Runner Status"}
	m.Width = 100
	m.Height = 28
	m.StartedAt = time.Now().Add(-75 * time.Second)

	m, _ = m.Update(executor.CycleFinishedMsg{
		MilestoneID: "0017",
		CycleNumber: 2,
		Status:      "passed",
		ReportFile:  ".cyclestone/reports/0017-cycle-002.md",
	})

	plainView := stripANSI(m.View())
	for _, want := range []string{
		"Summary: finished",
		"Verdict: passed",
		"Duration:",
		"Report: .cyclestone/reports/0017-cycle-002.md",
		"Next: Review the report and continue from milestone details.",
	} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("expected finished summary to contain %q\nview:\n%s", want, plainView)
		}
	}
}

func TestRunnerCancellationSetsCancelledStatus(t *testing.T) {
	styles := DefaultStyles(true, true)
	m := NewRunnerModel(styles)
	m.Milestone = config.Milestone{ID: "0017", Title: "Live Runner Status"}
	cancelled := false
	m.CancelFunc = func() { cancelled = true }
	m.Finished = false

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled {
		t.Fatal("expected cancel func to be called")
	}
	if !updated.Finished {
		t.Fatal("expected runner to be marked finished after cancellation")
	}
	if updated.CycleStatus != "cancelled" {
		t.Fatalf("expected cancelled status, got %q", updated.CycleStatus)
	}
	if cmd == nil {
		t.Fatal("expected details navigation command after cancellation")
	}
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

func renderedSize(s string) (int, int) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	maxWidth := 0
	for _, line := range lines {
		if len([]rune(line)) > maxWidth {
			maxWidth = len([]rune(line))
		}
	}
	return maxWidth, len(lines)
}

func logFrameSize(t *testing.T, view string) (int, int) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "Logs Output (Live Tail):") {
			start = i + 1
			break
		}
	}
	if start < 0 || start >= len(lines) {
		t.Fatalf("log frame not found in view:\n%s", view)
	}
	end := start
	for end < len(lines) {
		if strings.Contains(lines[end], "┘") || strings.Contains(lines[end], "+") {
			end++
			break
		}
		end++
	}
	maxWidth := 0
	for _, line := range lines[start:end] {
		if len([]rune(line)) > maxWidth {
			maxWidth = len([]rune(line))
		}
	}
	return maxWidth, end - start
}
