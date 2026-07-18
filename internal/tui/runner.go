package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/executor"
)

// RunnerTab represents a tab in the runner view when resolution is small.
type RunnerTab int

const (
	RunnerTabLog  RunnerTab = 0
	RunnerTabPlan RunnerTab = 1
)

const (
	maxRunnerLogLines    = 500
	maxRunnerStatusLines = 200
	runnerContextLines   = 5
	runnerBudgetLines    = 2
	runnerSummaryLines   = 7
	runnerProposalLines  = 6
)

// RunnerModel manages the run status screen.
type RunnerModel struct {
	Milestone           config.Milestone
	Pipeline            []config.Agent
	AgentStates         map[string]string // agent.ID -> status: "pending", "running", "success", "failed"
	AgentStartedAt      map[string]time.Time
	AgentElapsed        map[string]time.Duration
	Logs                []string
	StatusEvents        []string
	Spinner             spinner.Model
	Width               int
	Height              int
	Styles              Styles
	Ctx                 context.Context
	CancelFunc          context.CancelFunc
	Status              string
	CycleStatus         string
	CycleNumber         int
	ActiveAgentID       string
	ActivePhase         string
	Runner              string
	Model               string
	Mode                string
	OutputFile          string
	LatestCommand       string
	LatestToolCall      string
	ModelCalls          int
	ToolCalls           int
	EstimatedTokens     int
	PromptTokens        int
	CompletionTokens    int
	MaxModelCalls       int
	MaxTokenBudget      int
	StopOrDoneReason    string
	LastError           string
	NextSuggestedAction string
	FinalVerdict        string
	StartedAt           time.Time
	FinishedAt          time.Time
	Error               error
	Finished            bool
	ReportFile          string
	ActiveTab           RunnerTab
	Workflow            WorkflowKind
	ReturnScreen        Screen
}

// NewRunnerModel instantiates a new RunnerModel.
func NewRunnerModel(styles Styles) RunnerModel {
	s := spinner.New()
	s.Spinner = spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    80 * time.Millisecond,
	}
	s.Style = styles.Spinner

	return RunnerModel{
		AgentStates:    make(map[string]string),
		AgentStartedAt: make(map[string]time.Time),
		AgentElapsed:   make(map[string]time.Duration),
		Spinner:        s,
		Styles:         styles,
		Status:         "Initializing cycle execution...",
		CycleStatus:    "preparing",
		ActiveTab:      RunnerTabLog,
	}
}

// Init triggers the spinner animation.
func (m RunnerModel) Init() tea.Cmd {
	return m.Spinner.Tick
}

// Update processes execution messages, windows resize, and key cancellations.
func (m RunnerModel) Update(msg tea.Msg) (RunnerModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		return m, nil

	case spinner.TickMsg:
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if m.Height < 20 {
			switch msg.String() {
			case "tab", "right":
				if m.ActiveTab == RunnerTabLog {
					m.ActiveTab = RunnerTabPlan
				} else {
					m.ActiveTab = RunnerTabLog
				}
				return m, nil
			case "shift+tab", "left":
				if m.ActiveTab == RunnerTabPlan {
					m.ActiveTab = RunnerTabLog
				} else {
					m.ActiveTab = RunnerTabPlan
				}
				return m, nil
			}
		}
		switch msg.String() {
		case "y":
			if m.Finished && m.isAgentInstructionsWorkflow() {
				data, err := os.ReadFile(agentInstructionsDraftPath())
				if err != nil || strings.TrimSpace(string(data)) == "" {
					m.LastError = "no AGENTS.md proposal draft available to apply"
					return m, nil
				}
				if err := os.WriteFile("AGENTS.md", data, 0644); err != nil {
					m.LastError = "apply failed: " + err.Error()
				} else {
					m.Status = "Applied AGENTS.md proposal."
					m.NextSuggestedAction = "Review git diff for AGENTS.md before committing."
				}
				return m, nil
			}
		case "i":
			if m.Finished && m.isAgentInstructionsWorkflow() {
				if _, err := os.Stat(agentInstructionsDraftPath()); err != nil {
					m.LastError = "no AGENTS.md proposal draft available"
				} else {
					m.Status = "Saved editable AGENTS.md draft at .cyclestone/temp/AGENTS.md.proposed."
				}
				return m, nil
			}
		case "ctrl+c", "esc", "backspace":
			if !m.Finished {
				if m.CancelFunc != nil {
					m.CancelFunc()
				}
				m.Status = "Execution cancelled by user."
				m.CycleStatus = "cancelled"
				m.FinishedAt = time.Now()
				m.NextSuggestedAction = "Return to details when ready or start another cycle."
				m.Finished = true
			}
			return m, func() tea.Msg {
				if m.Workflow == WorkflowAgentInstructionsRepository && m.Finished {
					return ChangeScreenMsg{Screen: ScreenDashboard}
				}
				return ChangeScreenMsg{
					Screen: ScreenDetails,
					Data:   m.Milestone,
				}
			}
		}

	case executor.AgentStartedMsg:
		m.AgentStates[msg.AgentID] = "running"
		m.ActiveAgentID = msg.AgentID
		m.ActivePhase = msg.AgentID
		if m.StartedAt.IsZero() {
			m.StartedAt = time.Now()
		}
		m.AgentStartedAt[msg.AgentID] = time.Now()
		m.CycleStatus = "running"
		m.Status = fmt.Sprintf("Running %s phase...", msg.AgentID)
		return m, nil

	case executor.AgentProgressMsg:
		m.Logs = appendBounded(m.Logs, redactRunnerText(msg.LogLine), maxRunnerLogLines)
		return m, nil

	case executor.AgentCompletedMsg:
		if startedAt, ok := m.AgentStartedAt[msg.AgentID]; ok && !startedAt.IsZero() {
			m.AgentElapsed[msg.AgentID] = time.Since(startedAt)
		}
		if msg.ExitCode == 0 {
			m.AgentStates[msg.AgentID] = "success"
		} else {
			m.AgentStates[msg.AgentID] = "failed"
			m.CycleStatus = "failed"
			m.LastError = fmt.Sprintf("agent %s failed with exit code %d", msg.AgentID, msg.ExitCode)
		}
		if msg.OutputFile != "" {
			m.OutputFile = redactRunnerText(msg.OutputFile)
		}
		return m, nil

	case executor.RunnerStatusMsg:
		m.applyRunnerStatus(msg)
		return m, nil

	case executor.CycleFinishedMsg:
		m.Finished = true
		m.FinishedAt = time.Now()
		m.ReportFile = redactRunnerText(msg.ReportFile)
		if msg.CycleNumber > 0 {
			m.CycleNumber = msg.CycleNumber
		}
		if msg.Error != nil {
			if msg.Error == context.Canceled {
				m.Status = "Execution cancelled."
				m.CycleStatus = "cancelled"
				if msg.Status != "" {
					m.FinalVerdict = redactRunnerText(msg.Status)
				}
				if m.NextSuggestedAction == "" {
					m.NextSuggestedAction = "Return when ready."
				}
			} else {
				m.Status = fmt.Sprintf("%s failed with error: %v", m.workflowNounTitle(), msg.Error)
				m.CycleStatus = "failed"
				if msg.Status != "" {
					m.FinalVerdict = redactRunnerText(msg.Status)
				}
				m.LastError = redactRunnerText(msg.Error.Error())
				if m.NextSuggestedAction == "" {
					m.NextSuggestedAction = "Review the output log and rerun after fixing the failure."
				}
			}
			m.Error = msg.Error
		} else {
			m.Status = fmt.Sprintf("%s finished. Verdict: %s", m.workflowNounTitle(), strings.ToUpper(msg.Status))
			m.FinalVerdict = redactRunnerText(msg.Status)
			if msg.Status == "failed" {
				m.CycleStatus = "failed"
				if m.LastError == "" {
					m.LastError = "cycle finished with failed verdict"
				}
				if m.NextSuggestedAction == "" {
					m.NextSuggestedAction = "Review the report and fix the failed phase or checks before rerunning."
				}
			} else {
				m.CycleStatus = "finished"
				if m.NextSuggestedAction == "" {
					m.NextSuggestedAction = "Review the report and continue from milestone details."
				}
			}
		}
		return m, nil
	}

	return m, nil
}

func (m *RunnerModel) applyRunnerStatus(msg executor.RunnerStatusMsg) {
	if msg.CycleNumber > 0 {
		m.CycleNumber = msg.CycleNumber
	}
	if msg.CycleStatus != "" {
		m.CycleStatus = msg.CycleStatus
	}
	if msg.Phase != "" {
		m.ActivePhase = redactRunnerText(msg.Phase)
	}
	if msg.AgentID != "" {
		m.ActiveAgentID = msg.AgentID
	}
	if msg.Runner != "" {
		m.Runner = redactRunnerText(msg.Runner)
	}
	if msg.Model != "" {
		m.Model = redactRunnerText(msg.Model)
	}
	if msg.Mode != "" {
		m.Mode = redactRunnerText(msg.Mode)
	}
	if msg.ReportFile != "" {
		m.ReportFile = redactRunnerText(msg.ReportFile)
	}
	if msg.OutputFile != "" {
		m.OutputFile = redactRunnerText(msg.OutputFile)
	}
	if msg.LatestCommand != "" {
		m.LatestCommand = redactRunnerText(msg.LatestCommand)
	}
	if msg.LatestToolCall != "" {
		m.LatestToolCall = redactRunnerText(msg.LatestToolCall)
	}
	if msg.ModelCalls > 0 {
		m.ModelCalls = msg.ModelCalls
	}
	if msg.ToolCalls > 0 {
		m.ToolCalls = msg.ToolCalls
	}
	if msg.EstimatedTokens > 0 {
		m.EstimatedTokens = msg.EstimatedTokens
	}
	if msg.PromptTokens > 0 {
		m.PromptTokens = msg.PromptTokens
	}
	if msg.CompletionTokens > 0 {
		m.CompletionTokens = msg.CompletionTokens
	}
	if msg.MaxModelCalls > 0 {
		m.MaxModelCalls = msg.MaxModelCalls
	}
	if msg.MaxTokenBudget > 0 {
		m.MaxTokenBudget = msg.MaxTokenBudget
	}
	if msg.StopOrDoneReason != "" && msg.StopOrDoneReason != "n/a" {
		m.StopOrDoneReason = redactRunnerText(msg.StopOrDoneReason)
	}
	if msg.LastError != "" {
		m.LastError = redactRunnerText(msg.LastError)
	}
	if msg.NextSuggestedAction != "" {
		m.NextSuggestedAction = redactRunnerText(msg.NextSuggestedAction)
	}
	statusLine := m.statusEventLine()
	if statusLine != "" {
		m.StatusEvents = appendBounded(m.StatusEvents, statusLine, maxRunnerStatusLines)
	}
}

func (m RunnerModel) statusEventLine() string {
	var parts []string
	if m.CycleStatus != "" {
		parts = append(parts, "status="+m.CycleStatus)
	}
	if m.ActivePhase != "" {
		parts = append(parts, "phase="+m.ActivePhase)
	}
	if m.ActiveAgentID != "" {
		parts = append(parts, "agent="+m.ActiveAgentID)
	}
	if m.OutputFile != "" {
		parts = append(parts, "output="+m.OutputFile)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func appendBounded(lines []string, line string, max int) []string {
	lines = append(lines, line)
	if max > 0 && len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines
}

var runnerRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)([A-Z0-9_]*API[_-]?KEY|[A-Z0-9_]*TOKEN|[A-Z0-9_]*SECRET)\s*[:=]\s*['"]?[^'"\s]+`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret)["']?\s*[:=]\s*["'][^"']+["']`),
	regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{12,}|xox[baprs]-[A-Za-z0-9-]{12,}|AIza[0-9A-Za-z_-]{12,})\b`),
}

func redactRunnerText(text string) string {
	redacted := text
	for _, re := range runnerRedactors {
		redacted = re.ReplaceAllStringFunc(redacted, func(match string) string {
			if strings.Contains(strings.ToLower(match), "bearer ") {
				return "Bearer [REDACTED]"
			}
			if idx := strings.IndexAny(match, "=:"); idx >= 0 {
				return match[:idx+1] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return redacted
}

func formatRunnerDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return "0s"
	}
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return d.String()
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (m RunnerModel) totalElapsed() time.Duration {
	if m.StartedAt.IsZero() {
		return 0
	}
	if !m.FinishedAt.IsZero() {
		return m.FinishedAt.Sub(m.StartedAt)
	}
	return time.Since(m.StartedAt)
}

func (m RunnerModel) agentElapsed(agentID string) time.Duration {
	if d := m.AgentElapsed[agentID]; d > 0 {
		return d
	}
	if startedAt := m.AgentStartedAt[agentID]; !startedAt.IsZero() {
		return time.Since(startedAt)
	}
	return 0
}

// View draws the spinner, agent status lists, and console log output tail.
func (m RunnerModel) View() string {
	var sb strings.Builder

	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	var helpCommands []string
	if m.Height < 20 {
		if m.Finished {
			helpCommands = []string{"Tab Switch Tab", "Esc Details", "Backspace Details", "q Quit", "Ctrl+C Quit"}
		} else {
			helpCommands = []string{"Tab Switch Tab", "Esc Cancel", "Ctrl+C Cancel"}
		}
	} else {
		if m.Finished {
			helpCommands = []string{"Esc Details", "Backspace Details", "q Quit", "Ctrl+C Quit"}
		} else {
			helpCommands = []string{"Esc Cancel", "Ctrl+C Cancel"}
		}
	}
	if m.Finished && m.isAgentInstructionsWorkflow() {
		helpCommands = append([]string{"y Apply-AGENTS", "i Save-Draft"}, helpCommands...)
	}
	layoutHelpCommands := helpCommands
	if !m.Finished && m.isAgentInstructionsWorkflow() {
		layoutHelpCommands = append([]string{"y Apply-AGENTS", "i Save-Draft"}, helpCommands...)
	}
	helpText := renderCommandHelp(m.Styles, helpCommands, helpWidth)
	helpLines := renderedLineCount(renderCommandHelp(m.Styles, layoutHelpCommands, helpWidth))
	helpText = padRenderedText(helpText, helpLines)

	if m.Height < 20 {
		// Tabbed view layout for small resolution
		sb.WriteString(m.Styles.DetailHeader.Render(fmt.Sprintf("RUNNER: %s - %s", m.Milestone.ID, m.Milestone.Title)) + "\n")
		sb.WriteString(fmt.Sprintf("%s %s\n", m.Styles.DetailLabel.Render("Status:"), m.Styles.DetailValue.Render(m.compactStatusLine())))

		tabLine := lipgloss.JoinHorizontal(
			lipgloss.Top,
			tabStyle(m.Styles, "LOG", m.ActiveTab == RunnerTabLog),
			"  ",
			tabStyle(m.Styles, "PLAN", m.ActiveTab == RunnerTabPlan),
		)
		sb.WriteString(tabLine + "\n")

		if m.ActiveTab == RunnerTabLog {
			var activeAgent *config.Agent
			for i := range m.Pipeline {
				if m.AgentStates[m.Pipeline[i].ID] == "running" {
					activeAgent = &m.Pipeline[i]
					break
				}
			}
			if activeAgent == nil && m.AgentStates["recommender"] == "running" {
				activeAgent = &config.Agent{
					ID:   "recommender",
					Name: "Recommender",
				}
			}

			var activeAgentStr string
			if activeAgent != nil {
				activeAgentStr = fmt.Sprintf("Active Agent: %s %s (%s)\n", activeAgent.Name, m.Spinner.View(), formatRunnerDuration(m.agentElapsed(activeAgent.ID)))
			} else {
				activeAgentStr = "Active Agent: -\n"
			}
			sb.WriteString(activeAgentStr)

			sb.WriteString(m.Styles.DetailLabel.Render("Logs Output (Live Tail):") + "\n")
			logBoxHeight := remainingRunnerLogBoxHeight(m.Height, sb.String(), helpLines)

			logContentHeight := logBoxHeight
			if logContentHeight < 1 {
				logContentHeight = 1
			}
			logContentWidth := m.Width - 8
			if logContentWidth < 10 {
				logContentWidth = 10
			}
			logContent := renderBoundedLines(m.Logs, logContentWidth, logContentHeight, "Preparing execution environment...")
			logBox := m.Styles.InactiveBorder.
				Width(m.Width - 6).
				Height(logBoxHeight).
				Render(logContent)

			sb.WriteString(logBox + "\n")
		} else {
			// PLAN Tab
			var pipelineBuilder strings.Builder
			pipelineBuilder.WriteString(m.renderRunnerContext(runnerContextLines))
			pipelineBuilder.WriteString(m.renderBudgetLine(runnerBudgetLines))
			pipelineBuilder.WriteString(m.renderSummaryLine(runnerSummaryLines))
			pipelineBuilder.WriteString(m.Styles.DetailLabel.Render("Agent Workflow Pipeline:") + "\n")
			for _, agent := range m.Pipeline {
				state := m.AgentStates[agent.ID]
				if state == "" {
					state = "pending"
				}

				var icon string
				switch state {
				case "pending":
					icon = m.Styles.HelpStyle.Render("○ Pending")
				case "running":
					icon = m.Spinner.View() + " " + m.Styles.WarningText.Render("Running")
				case "success":
					icon = m.Styles.SuccessText.Render(m.Styles.GlyphCheck + " Success")
				case "failed":
					icon = m.Styles.ErrorText.Render(m.Styles.GlyphCross + " Failed")
				}

				pipelineBuilder.WriteString(fmt.Sprintf("  %s %-15s %s %s\n", m.Styles.AccentText.Render("│"), agent.Name, icon, formatRunnerDuration(m.agentElapsed(agent.ID))))
			}
			hasRecommenderInPipeline := false
			for _, agent := range m.Pipeline {
				if agent.ID == "recommender" {
					hasRecommenderInPipeline = true
					break
				}
			}
			if !hasRecommenderInPipeline {
				recState := m.AgentStates["recommender"]
				if recState == "" {
					recState = "pending"
				}
				var recIcon string
				switch recState {
				case "pending":
					recIcon = m.Styles.HelpStyle.Render("○ Pending")
				case "running":
					recIcon = m.Spinner.View() + " " + m.Styles.WarningText.Render("Running")
				case "success":
					recIcon = m.Styles.SuccessText.Render(m.Styles.GlyphCheck + " Success")
				case "failed":
					recIcon = m.Styles.ErrorText.Render(m.Styles.GlyphCross + " Failed")
				}
				pipelineBuilder.WriteString(fmt.Sprintf("  %s %-15s %s %s\n", m.Styles.AccentText.Render("│"), "Recommender", recIcon, formatRunnerDuration(m.agentElapsed("recommender"))))
			}
			pipelineBuilder.WriteString("\n")
			sb.WriteString(pipelineBuilder.String())
		}
	} else {
		// Default standard resolution layout
		sb.WriteString(m.Styles.DetailHeader.Render(fmt.Sprintf("RUNNER: %s - %s", m.Milestone.ID, m.Milestone.Title)) + "\n")
		sb.WriteString(fmt.Sprintf("%s %s\n", m.Styles.DetailLabel.Render("Status:"), m.Styles.DetailValue.Render(m.compactStatusLine())))
		sb.WriteString(m.renderRunnerContext(runnerContextLines))
		sb.WriteString(m.renderBudgetLine(runnerBudgetLines))
		sb.WriteString(m.renderSummaryLine(runnerSummaryLines))
		if m.isAgentInstructionsWorkflow() {
			sb.WriteString(m.renderAgentInstructionsProposal(m.Width-6, runnerProposalLines))
		}
		sb.WriteString("\n")

		pipelineStr := m.renderStandardPipeline(false)
		candidatePrefix := sb.String() + pipelineStr + m.Styles.DetailLabel.Render("Logs Output (Live Tail):") + "\n"
		if m.Height < 25 && remainingRunnerLogBoxHeight(m.Height, candidatePrefix, helpLines) <= 1 {
			pipelineStr = m.renderStandardPipeline(true)
		}
		sb.WriteString(pipelineStr)

		sb.WriteString(m.Styles.DetailLabel.Render("Logs Output (Live Tail):") + "\n")
		logBoxHeight := remainingRunnerLogBoxHeight(m.Height, sb.String(), helpLines)

		logContentHeight := logBoxHeight
		if logContentHeight < 1 {
			logContentHeight = 1
		}
		logContentWidth := m.Width - 8
		if logContentWidth < 10 {
			logContentWidth = 10
		}
		logContent := renderBoundedLines(m.Logs, logContentWidth, logContentHeight, "Preparing execution environment...")
		logBox := m.Styles.InactiveBorder.
			Width(m.Width - 6).
			Height(logBoxHeight).
			Render(logContent)

		sb.WriteString(logBox + "\n")
	}

	sb.WriteString(helpText)
	return sb.String()
}

func (m RunnerModel) workflowNounTitle() string {
	if m.Workflow == WorkflowAgentInstructionsRepository || m.Workflow == WorkflowAgentInstructionsMilestone {
		return "AGENTS.md update"
	}
	return "Cycle"
}

func (m RunnerModel) isAgentInstructionsWorkflow() bool {
	return m.Workflow == WorkflowAgentInstructionsRepository || m.Workflow == WorkflowAgentInstructionsMilestone
}

func (m RunnerModel) renderStandardPipeline(compact bool) string {
	if compact {
		var states []string
		for _, agent := range m.Pipeline {
			state := m.AgentStates[agent.ID]
			if state == "" {
				state = "pending"
			}
			var statusShort string
			switch state {
			case "pending":
				statusShort = "pending"
			case "running":
				statusShort = "running"
			case "success":
				statusShort = "success"
			case "failed":
				statusShort = "failed"
			}
			states = append(states, fmt.Sprintf("%s: %s %s", agent.Name, statusShort, formatRunnerDuration(m.agentElapsed(agent.ID))))
		}
		if !m.pipelineHasRecommender() {
			recState := m.AgentStates["recommender"]
			if recState == "" {
				recState = "pending"
			}
			var recShort string
			switch recState {
			case "pending":
				recShort = "pending"
			case "running":
				recShort = "running"
			case "success":
				recShort = "success"
			case "failed":
				recShort = "failed"
			}
			states = append(states, fmt.Sprintf("Recommender: %s %s", recShort, formatRunnerDuration(m.agentElapsed("recommender"))))
		}
		return "Pipeline: " + strings.Join(states, " | ") + "\n"
	}

	var pipelineBuilder strings.Builder
	pipelineBuilder.WriteString(m.Styles.DetailLabel.Render("Agent Workflow Pipeline:") + "\n")
	for _, agent := range m.Pipeline {
		state := m.AgentStates[agent.ID]
		if state == "" {
			state = "pending"
		}

		var icon string
		switch state {
		case "pending":
			icon = m.Styles.HelpStyle.Render("○ Pending")
		case "running":
			icon = m.Spinner.View() + " " + m.Styles.WarningText.Render("Running")
		case "success":
			icon = m.Styles.SuccessText.Render(m.Styles.GlyphCheck + " Success")
		case "failed":
			icon = m.Styles.ErrorText.Render(m.Styles.GlyphCross + " Failed")
		}

		pipelineBuilder.WriteString(fmt.Sprintf("  %s %-15s %s %s\n", m.Styles.AccentText.Render("│"), agent.Name, icon, formatRunnerDuration(m.agentElapsed(agent.ID))))
	}
	if !m.pipelineHasRecommender() {
		recState := m.AgentStates["recommender"]
		if recState == "" {
			recState = "pending"
		}
		var recIcon string
		switch recState {
		case "pending":
			recIcon = m.Styles.HelpStyle.Render("○ Pending")
		case "running":
			recIcon = m.Spinner.View() + " " + m.Styles.WarningText.Render("Running")
		case "success":
			recIcon = m.Styles.SuccessText.Render(m.Styles.GlyphCheck + " Success")
		case "failed":
			recIcon = m.Styles.ErrorText.Render(m.Styles.GlyphCross + " Failed")
		}
		pipelineBuilder.WriteString(fmt.Sprintf("  %s %-15s %s %s\n", m.Styles.AccentText.Render("│"), "Recommender", recIcon, formatRunnerDuration(m.agentElapsed("recommender"))))
	}
	pipelineBuilder.WriteString("\n")
	return pipelineBuilder.String()
}

func (m RunnerModel) pipelineHasRecommender() bool {
	for _, agent := range m.Pipeline {
		if agent.ID == "recommender" {
			return true
		}
	}
	return false
}

func remainingRunnerLogBoxHeight(totalHeight int, renderedBeforeLogBox string, helpLines int) int {
	prefixLines := strings.Count(renderedBeforeLogBox, "\n")
	if renderedBeforeLogBox != "" && !strings.HasSuffix(renderedBeforeLogBox, "\n") {
		prefixLines++
	}
	height := totalHeight - prefixLines - helpLines - 2
	if height < 1 {
		height = 1
	}
	return height
}

func agentInstructionsDraftPath() string {
	return filepath.Join(".cyclestone", "temp", "AGENTS.md.proposed")
}

func (m RunnerModel) renderAgentInstructionsProposal(width int, maxLines int) string {
	if !m.Finished {
		return m.renderBoundedDetailBlock([]string{"Proposal draft: waiting for runner to finish"}, width, maxLines)
	}
	data, err := os.ReadFile(agentInstructionsDraftPath())
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return m.renderBoundedDetailBlock([]string{"Proposal draft: not available yet"}, width, maxLines)
	}
	text := limitStringForView(string(data), 1600)
	lines := []string{"Proposal Draft: .cyclestone/temp/AGENTS.md.proposed"}
	lines = append(lines, strings.Split(text, "\n")...)
	return m.renderBoundedDetailBlock(lines, width, maxLines)
}

func limitStringForView(text string, max int) string {
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "\n[truncated in view]"
}

func (m RunnerModel) compactStatusLine() string {
	status := m.CycleStatus
	if status == "" {
		status = "preparing"
	}
	cycle := "-"
	if m.CycleNumber > 0 {
		cycle = fmt.Sprintf("%03d", m.CycleNumber)
	}
	phase := m.ActivePhase
	if phase == "" {
		phase = "preparing"
	}
	return fmt.Sprintf("%s | cycle %s | phase %s | elapsed %s", status, cycle, phase, formatRunnerDuration(m.totalElapsed()))
}

func (m RunnerModel) renderRunnerContext(maxLines int) string {
	var lines []string
	width := m.Width - 6
	if width < 20 {
		width = 20
	}
	if m.Runner != "" || m.Model != "" || m.Mode != "" {
		runner := valueOrDash(m.Runner)
		model := valueOrDash(m.Model)
		mode := valueOrDash(m.Mode)
		lines = append(lines, fmt.Sprintf("Runner: %s | Model: %s | Mode: %s", runner, model, mode))
	}
	if m.ReportFile != "" || m.OutputFile != "" {
		lines = append(lines, fmt.Sprintf("Report: %s", valueOrDash(m.ReportFile)))
		if m.OutputFile != "" {
			lines = append(lines, fmt.Sprintf("Output: %s", m.OutputFile))
		}
	}
	if m.LatestCommand != "" {
		lines = append(lines, "Latest command: "+m.LatestCommand)
	}
	if m.LatestToolCall != "" {
		lines = append(lines, "Latest tool call: "+m.LatestToolCall)
	}
	if len(lines) == 0 {
		return m.renderBoundedDetailBlock(nil, width, maxLines)
	}
	return m.renderBoundedDetailBlock(lines, width, maxLines)
}

func (m RunnerModel) renderBudgetLine(maxLines int) string {
	if m.ModelCalls == 0 && m.ToolCalls == 0 && m.EstimatedTokens == 0 && m.PromptTokens == 0 && m.CompletionTokens == 0 && m.MaxModelCalls == 0 && m.MaxTokenBudget == 0 {
		width := m.Width - 6
		if width < 20 {
			width = 20
		}
		return m.renderBoundedDetailBlock(nil, width, maxLines)
	}
	modelCalls := fmt.Sprintf("%d", m.ModelCalls)
	if m.MaxModelCalls > 0 {
		modelCalls = fmt.Sprintf("%d/%d", m.ModelCalls, m.MaxModelCalls)
	}
	estTokens := fmt.Sprintf("%d", m.EstimatedTokens)
	if m.MaxTokenBudget > 0 {
		estTokens = fmt.Sprintf("%d/%d", m.EstimatedTokens, m.MaxTokenBudget)
	}
	actual := "unavailable"
	if m.PromptTokens > 0 || m.CompletionTokens > 0 {
		actual = fmt.Sprintf("prompt %d, completion %d", m.PromptTokens, m.CompletionTokens)
	}
	line := fmt.Sprintf("Budget: model calls %s | tool calls %d | est tokens %s | actual tokens %s", modelCalls, m.ToolCalls, estTokens, actual)
	if m.StopOrDoneReason != "" {
		line += " | stop " + m.StopOrDoneReason
	}
	width := m.Width - 6
	if width < 20 {
		width = 20
	}
	return m.renderBoundedDetailBlock([]string{line}, width, maxLines)
}

func (m RunnerModel) renderSummaryLine(maxLines int) string {
	if !m.Finished && m.CycleStatus != "failed" && m.CycleStatus != "cancelled" {
		width := m.Width - 6
		if width < 20 {
			width = 20
		}
		return m.renderBoundedDetailBlock(nil, width, maxLines)
	}
	var parts []string
	if m.CycleStatus != "" {
		parts = append(parts, "Summary: "+m.CycleStatus)
	}
	if m.FinalVerdict != "" {
		parts = append(parts, "Verdict: "+m.FinalVerdict)
	}
	if m.ActiveAgentID != "" && (m.CycleStatus == "failed" || m.CycleStatus == "cancelled") {
		parts = append(parts, "Agent: "+m.ActiveAgentID)
	}
	parts = append(parts, "Duration: "+formatRunnerDuration(m.totalElapsed()))
	if m.LastError != "" {
		parts = append(parts, "Reason: "+m.LastError)
	}
	if m.OutputFile != "" {
		parts = append(parts, "Output: "+m.OutputFile)
	}
	if m.ReportFile != "" {
		parts = append(parts, "Report: "+m.ReportFile)
	}
	if m.NextSuggestedAction != "" {
		parts = append(parts, "Next: "+m.NextSuggestedAction)
	}
	if len(parts) == 0 {
		return ""
	}
	width := m.Width - 6
	if width < 20 {
		width = 20
	}
	return m.renderBoundedDetailBlock(parts, width, maxLines)
}

func (m RunnerModel) renderBoundedDetailBlock(lines []string, width int, maxLines int) string {
	if maxLines < 1 {
		maxLines = 1
	}
	content := renderBoundedLinesFromStart(lines, width, maxLines, "")
	return m.Styles.DetailValue.Render(content) + "\n"
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func padRenderedText(s string, targetLines int) string {
	if targetLines <= 0 {
		return s
	}
	currentLines := renderedLineCount(s)
	for currentLines < targetLines {
		if s != "" {
			s += "\n"
		}
		currentLines++
	}
	return s
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// padLogLines pads a string slice with empty strings up to targetHeight.
func padLogLines(lines []string, targetHeight int) []string {
	if len(lines) >= targetHeight {
		return lines
	}
	padded := make([]string, targetHeight)
	copy(padded, lines)
	for i := len(lines); i < targetHeight; i++ {
		padded[i] = ""
	}
	return padded
}
