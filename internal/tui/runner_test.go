package tui

import (
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
		Runner:           "openai",
		Model:            "gpt-test",
		Mode:             "sandbox",
		ReportFile:       ".cyclestone/reports/0017-cycle-002.md",
		OutputFile:       ".cyclestone/reports/0017-cycle-002-developer-output.log",
		LatestCommand:    "openai API call",
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
		"Runner: openai | Model: gpt-test | Mode: sandbox",
		"Report: .cyclestone/reports/0017-cycle-002.md",
		"Latest command: openai API call",
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
