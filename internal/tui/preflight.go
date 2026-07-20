package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/git"
)

type preflightSeverity int

const (
	preflightWarning preflightSeverity = iota
	preflightBlocker
)

type preflightIssue struct {
	Severity preflightSeverity
	Message  string
}

type instructionSourceStatus struct {
	Label   string
	Path    string
	Present bool
}

// PreflightModel renders the non-mutating workflow review that must be
// confirmed before RootModel receives StartCycleMsg for executor startup.
type PreflightModel struct {
	Request            StartCycleMsg
	Milestone          config.Milestone
	State              *config.State
	ConfigPath         string
	StatePath          string
	Width              int
	Height             int
	FocusIndex         int
	ScrollOffset       int
	Styles             Styles
	Settings           config.Settings
	Pipeline           []config.Agent
	MissingAgents      []string
	Repos              []git.RepoStatusSummary
	Issues             []preflightIssue
	InstructionSources []instructionSourceStatus
}

func NewPreflightModel(styles Styles) PreflightModel {
	return PreflightModel{Styles: styles}
}

func (m PreflightModel) Init() tea.Cmd {
	return nil
}

func (m *PreflightModel) Load(req StartCycleMsg, state *config.State, configPath, statePath string) {
	m.Request = req
	m.Milestone = req.Milestone
	m.State = state
	m.ConfigPath = configPath
	m.StatePath = statePath
	m.Settings = config.LoadMergedSettings()
	m.FocusIndex = 0
	m.ScrollOffset = 0
	m.Pipeline = nil
	m.MissingAgents = nil
	m.Repos = git.SummarizeTrackedRepoStatuses()
	m.Issues = nil
	m.InstructionSources = m.loadInstructionSources()

	agents, err := config.LoadDynamicAgents()
	if err != nil {
		m.Issues = append(m.Issues, preflightIssue{Severity: preflightBlocker, Message: fmt.Sprintf("Unable to load agents: %v", err)})
	}
	m.Pipeline, m.MissingAgents = resolvePreflightPipeline(agents, req.Group, req.SingleAgentID)
	if req.Workflow == WorkflowAgentInstructionsRepository || req.Workflow == WorkflowAgentInstructionsMilestone {
		runner := req.RunnerLLM
		if runner == "" {
			runner = m.Settings.DefaultLLM
		}
		runner = normalizeMilestoneRunner(runner)
		m.Request.RunnerLLM = runner
		m.Pipeline = []config.Agent{{
			ID:           "agent-instructions-updater",
			Name:         "Agent Instructions Updater",
			RunnerBinary: runner,
		}}
		m.MissingAgents = nil
	}
	m.applyRunnerOverride()
	m.validate()
	if m.HasBlockers() {
		m.FocusIndex = 1
	}
}

func (m PreflightModel) loadInstructionSources() []instructionSourceStatus {
	instructionPath := strings.TrimSpace(m.Settings.AgentInstructions.File)
	if instructionPath == "" {
		instructionPath = "AGENTS.md"
	}
	sources := []instructionSourceStatus{
		{Label: "Agent instructions", Path: instructionPath},
		{Label: "Decisions log", Path: filepath.Join(".cyclestone", "DECISIONS.md")},
	}
	for i := range sources {
		_, err := os.Stat(sources[i].Path)
		sources[i].Present = err == nil
	}
	return sources
}

func resolvePreflightPipeline(agents []config.Agent, group config.AgentGroup, singleAgentID string) ([]config.Agent, []string) {
	byID := make(map[string]config.Agent, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}

	if singleAgentID != "" {
		if group.Name == "" {
			agent, ok := byID[singleAgentID]
			if !ok {
				return nil, []string{singleAgentID}
			}
			return []config.Agent{agent}, nil
		}

		for _, id := range group.AgentIDs {
			if id != singleAgentID {
				continue
			}
			agent, ok := byID[id]
			if !ok {
				return nil, []string{id}
			}
			return []config.Agent{agent}, nil
		}
		return nil, []string{singleAgentID}
	}

	if group.Name == "" {
		return append([]config.Agent(nil), agents...), nil
	}
	var pipeline []config.Agent
	var missing []string
	for _, id := range group.AgentIDs {
		agent, ok := byID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		pipeline = append(pipeline, agent)
	}
	return pipeline, missing
}

func (m *PreflightModel) applyRunnerOverride() {
	if m.Request.RunnerLLM == "" {
		return
	}
	for i := range m.Pipeline {
		if m.Pipeline[i].RunnerBinary != "manual" {
			m.Pipeline[i].RunnerBinary = m.Request.RunnerLLM
		}
	}
}

func (m *PreflightModel) validate() {
	unrestricted := m.Request.RunnerMode == "unrestricted"
	if m.Request.RunnerMode == "" {
		unrestricted = m.Settings.DefaultMode == "unrestricted"
	}
	if unrestricted {
		m.Issues = append(m.Issues, preflightIssue{Severity: preflightWarning, Message: "Unrestricted execution mode is enabled."})
	}
	if len(m.MissingAgents) > 0 {
		m.Issues = append(m.Issues, preflightIssue{Severity: preflightBlocker, Message: "Selected group references missing agents: " + strings.Join(m.MissingAgents, ", ")})
	}
	if len(m.Pipeline) == 0 {
		m.Issues = append(m.Issues, preflightIssue{Severity: preflightBlocker, Message: "Resolved agent pipeline is empty."})
	}
	for _, repo := range m.Repos {
		if !repo.IsWorktree {
			m.Issues = append(m.Issues, preflightIssue{Severity: preflightWarning, Message: fmt.Sprintf("%s is not a Git worktree (%s).", repo.Label, repo.Path)})
			continue
		}
		if repo.Dirty {
			m.Issues = append(m.Issues, preflightIssue{Severity: preflightWarning, Message: fmt.Sprintf("%s has %d changed file(s).", repo.Label, repo.ChangedCount)})
		}
		if repo.Detached {
			m.Issues = append(m.Issues, preflightIssue{Severity: preflightWarning, Message: fmt.Sprintf("%s is on detached HEAD.", repo.Label)})
		}
		if repo.Unknown {
			m.Issues = append(m.Issues, preflightIssue{Severity: preflightWarning, Message: fmt.Sprintf("%s branch state is unknown.", repo.Label)})
		}
	}
	m.validateRunners()
}

func (m *PreflightModel) validateRunners() {
	seen := map[string]bool{}
	for _, agent := range m.Pipeline {
		runner := m.runnerForAgent(agent)
		if runner == "" || runner == "manual" {
			continue
		}
		if seen[runner] {
			continue
		}
		seen[runner] = true
		if issue, ok := validateRunnerAvailability(runner); ok {
			m.Issues = append(m.Issues, issue)
		}
	}
}

func (m PreflightModel) runnerForAgent(agent config.Agent) string {
	runner := agent.RunnerBinary
	if runner == "" {
		runner = m.Settings.DefaultLLM
	}
	if runner == "" {
		runner = "codex"
	}
	return runner
}

func validateRunnerAvailability(runner string) (preflightIssue, bool) {
	switch runner {
	case "ollama-codex":
		if ok, reason := checkRunnerAvailable("ollama-codex"); !ok {
			return preflightIssue{Severity: preflightBlocker, Message: fmt.Sprintf("Runner %q is unavailable: %s.", runner, reason)}, true
		}
	case "codex", "agy":
		if ok, reason := checkRunnerAvailable(runner); !ok {
			return preflightIssue{Severity: preflightBlocker, Message: fmt.Sprintf("Runner %q is unavailable: %s.", runner, reason)}, true
		}
	default:
		return preflightIssue{Severity: preflightBlocker, Message: fmt.Sprintf("Runner %q is unsupported. Select codex, agy, or ollama-codex.", runner)}, true
	}
	return preflightIssue{}, false
}

func (m PreflightModel) HasBlockers() bool {
	for _, issue := range m.Issues {
		if issue.Severity == preflightBlocker {
			return true
		}
	}
	return false
}

func (m PreflightModel) Update(msg tea.Msg) (PreflightModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		m.clampScroll()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.ScrollOffset > 0 {
				m.ScrollOffset--
			}
			return m, nil
		case "down", "j":
			m.ScrollOffset++
			m.clampScroll()
			return m, nil
		case "tab", "left", "right", "h", "l":
			m.FocusIndex = 1 - m.FocusIndex
			if m.HasBlockers() {
				m.FocusIndex = 1
			}
			return m, nil
		case "esc":
			return m, m.cancelCmd()
		case "enter":
			if m.FocusIndex == 0 && !m.HasBlockers() {
				return m, func() tea.Msg {
					return m.Request
				}
			}
			if m.FocusIndex == 1 {
				return m, m.cancelCmd()
			}
		}
	}
	return m, nil
}

func (m PreflightModel) cancelCmd() tea.Cmd {
	return func() tea.Msg {
		if m.Request.Workflow == WorkflowAgentInstructionsRepository {
			return ChangeScreenMsg{Screen: ScreenDashboard}
		}
		return ChangeScreenMsg{Screen: ScreenDetails, Data: m.Milestone}
	}
}

func (m *PreflightModel) clampScroll() {
	max := m.contentLineCount() - m.visibleContentHeight()
	if max < 0 {
		max = 0
	}
	if m.ScrollOffset > max {
		m.ScrollOffset = max
	}
}

func (m PreflightModel) visibleContentHeight() int {
	h := m.Height - 8
	if h < 4 {
		h = 4
	}
	return h
}

func (m PreflightModel) contentLineCount() int {
	return len(strings.Split(m.content(), "\n"))
}

func (m PreflightModel) View() string {
	width := m.Width - 4
	if width < 24 {
		width = 24
	}
	height := m.Height - 5
	if height < 8 {
		height = 8
	}
	bodyHeight := height - 3
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	lines := strings.Split(m.content(), "\n")
	if m.ScrollOffset > len(lines) {
		m.ScrollOffset = len(lines)
	}
	end := m.ScrollOffset + bodyHeight
	if end > len(lines) {
		end = len(lines)
	}
	if len(lines) == 0 {
		lines = []string{""}
		end = 1
	}

	var sb strings.Builder
	sb.WriteString(m.Styles.DetailHeader.Render(strings.ToUpper(m.workflowLabel())+" PREFLIGHT REVIEW") + "\n")
	sb.WriteString(strings.Join(lines[m.ScrollOffset:end], "\n") + "\n")
	sb.WriteString(m.renderButtons(width) + "\n")
	sb.WriteString(renderCommandHelp(m.Styles, []string{"↑/↓ Scroll", "Tab Toggle", "Enter Confirm", "Esc Cancel"}, width))

	return m.Styles.ActiveBorder.
		Width(width).
		Height(height).
		Render(truncateLines(sb.String(), height))
}

func (m PreflightModel) content() string {
	var sb strings.Builder
	cycle := 1
	status := m.Milestone.Status
	if status == "" {
		status = "Todo"
	}
	if m.State != nil {
		cycle = m.State.GetMilestoneCycles(m.Milestone.ID) + 1
		status = m.State.GetMilestoneStatus(m.Milestone.ID)
	}
	cyclePadded := fmt.Sprintf("%03d", cycle)
	group := m.Request.Group.Name
	if group == "" {
		group = "All discovered agents"
	}
	if m.Request.SingleAgentID != "" {
		group += fmt.Sprintf(" (single agent: %s)", m.Request.SingleAgentID)
	}
	runner := m.Request.RunnerLLM
	if runner == "" {
		runner = m.Settings.DefaultLLM
	}
	if runner == "" {
		runner = "codex"
	}
	mode := m.Request.RunnerMode
	if mode == "" {
		mode = m.Settings.DefaultMode
	}
	if mode == "" {
		mode = "sandbox"
	}
	prefix := m.Settings.DefaultGitBranchPrefix
	if prefix == "" {
		prefix = "cyclestone/milestones/"
	}
	expectedBranch := prefix + m.Milestone.ID
	branchSetting := "enabled"
	if m.Request.NoBranchChange {
		branchSetting = "disabled (--no-branch-change)"
		expectedBranch = currentBranchFallback()
	}

	if m.Request.Workflow == WorkflowAgentInstructionsRepository {
		sb.WriteString("Workflow: Repository AGENTS.md update\n")
	} else if m.Request.Workflow == WorkflowAgentInstructionsMilestone {
		sb.WriteString(fmt.Sprintf("Workflow: Milestone-scoped AGENTS.md update for %s\n", m.Milestone.ID))
	} else {
		sb.WriteString(fmt.Sprintf("Milestone: %s - %s\n", m.Milestone.ID, emptyFallback(m.Milestone.Title, "(untitled)")))
		if origin := m.Request.BriefingOrigin; origin.PlanRun {
			sb.WriteString(fmt.Sprintf("Plan: %s | Briefing: %s | Queue: %d/%d\n", origin.PlanID, origin.BriefingID, origin.QueuePosition, origin.QueueTotal))
			sb.WriteString(fmt.Sprintf("Plan execution: %s | Dependencies: %s\n", origin.Mode, emptyFallback(origin.DependencyState, "ready")))
		}
		sb.WriteString(fmt.Sprintf("Status: %s\n", status))
		sb.WriteString(fmt.Sprintf("Next cycle: %s\n", cyclePadded))
		sb.WriteString(fmt.Sprintf("Agent group: %s\n", group))
	}
	sb.WriteString(fmt.Sprintf("Pipeline: %s\n", m.pipelineText()))
	sb.WriteString(fmt.Sprintf("Runner/model: %s / %s\n", runner, m.modelForRunner(runner)))
	sb.WriteString(fmt.Sprintf("Mode: %s\n", mode))
	sb.WriteString(fmt.Sprintf("Branch changes: %s\n", branchSetting))
	sb.WriteString(fmt.Sprintf("Expected branch: %s\n", expectedBranch))
	if m.Request.Workflow == WorkflowCycle {
		cycleDir := filepath.Join(".cyclestone", "reports", m.Milestone.ID, "cycle-"+cyclePadded)
		sb.WriteString(fmt.Sprintf("Reports: %s\n", filepath.Join(cycleDir, "report.yaml")))
		sb.WriteString(fmt.Sprintf("Metadata: %s\n", filepath.Join(cycleDir, "metadata.json")))
	} else {
		sb.WriteString(fmt.Sprintf("Proposal draft: %s\n", filepath.Join(".cyclestone", "temp", "AGENTS.md.proposed")))
	}
	sb.WriteString(fmt.Sprintf("State: %s\n", emptyFallback(m.StatePath, filepath.Join(".cyclestone", "state.json"))))
	sb.WriteString(fmt.Sprintf("Config: %s\n", emptyFallback(m.ConfigPath, filepath.Join(".cyclestone", "milestone.yml"))))
	sb.WriteString(fmt.Sprintf("Context size: %s\n", m.contextSizeText()))
	sb.WriteString("Instruction sources:\n")
	for _, source := range m.InstructionSources {
		status := "missing"
		if source.Present {
			status = "present"
		}
		sb.WriteString(fmt.Sprintf("  %s: %s (%s)\n", source.Label, source.Path, status))
	}
	if strings.TrimSpace(m.Request.Note) != "" {
		if m.Request.Workflow == WorkflowCycle {
			sb.WriteString("Cycle note: present\n")
		} else {
			sb.WriteString("Human message: present\n")
		}
	}

	sb.WriteString("\nRepositories:\n")
	for _, repo := range m.Repos {
		sb.WriteString("  " + formatRepoSummary(repo) + "\n")
	}

	sb.WriteString("\nValidation:\n")
	if len(m.Issues) == 0 {
		sb.WriteString("  No warnings or blockers.\n")
	} else {
		for _, issue := range m.Issues {
			label := "WARN"
			if issue.Severity == preflightBlocker {
				label = "BLOCK"
			}
			sb.WriteString(fmt.Sprintf("  %s: %s\n", label, issue.Message))
		}
	}
	return sb.String()
}

func (m PreflightModel) workflowLabel() string {
	switch m.Request.Workflow {
	case WorkflowAgentInstructionsRepository, WorkflowAgentInstructionsMilestone:
		return "AGENTS.md update"
	default:
		return "cycle"
	}
}

func (m PreflightModel) pipelineText() string {
	if len(m.Pipeline) == 0 {
		return "(empty)"
	}
	parts := make([]string, 0, len(m.Pipeline))
	for _, agent := range m.Pipeline {
		name := agent.ID
		if agent.Name != "" {
			name = agent.Name
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", name, m.runnerForAgent(agent)))
	}
	return strings.Join(parts, " -> ")
}

func (m PreflightModel) modelForRunner(runner string) string {
	switch runner {
	case "ollama-codex":
		return emptyFallback(m.Settings.OllamaCodexModel, "(default)")
	default:
		return "(runner default)"
	}
}

func (m PreflightModel) contextSizeText() string {
	size := 0
	size += len(m.Milestone.ID) + len(m.Milestone.Title) + len(m.Milestone.Goal)
	for _, ac := range m.Milestone.AcceptanceCriteria {
		size += len(ac)
	}
	for _, agent := range m.Pipeline {
		size += len(agent.PromptBody)
	}
	if strings.TrimSpace(m.Request.Note) != "" {
		size += len(m.Request.Note)
	}
	if size == 0 {
		return "unavailable"
	}
	return fmt.Sprintf("~%d chars (~%d tokens)", size, (size+3)/4)
}

func (m PreflightModel) renderButtons(width int) string {
	confirmLabel := " [ Confirm Run ] "
	if m.Request.Workflow != WorkflowCycle {
		confirmLabel = " [ Generate Proposal ] "
	}
	if m.HasBlockers() {
		confirmLabel = " [ Confirm Disabled ] "
	}
	cancelLabel := " [ Cancel ] "
	var confirm, cancel string
	if m.FocusIndex == 0 && !m.HasBlockers() {
		confirm = m.Styles.TableSelectedRow.Render(confirmLabel)
	} else if m.HasBlockers() {
		confirm = m.Styles.HelpStyle.Render(confirmLabel)
	} else {
		confirm = m.Styles.SuccessText.Render(confirmLabel)
	}
	if m.FocusIndex == 1 || m.HasBlockers() {
		cancel = m.Styles.TableSelectedRow.Render(cancelLabel)
	} else {
		cancel = m.Styles.HelpStyle.Render(cancelLabel)
	}
	return truncatePlain(fmt.Sprintf("%s  %s", confirm, cancel), width)
}

func formatRepoSummary(repo git.RepoStatusSummary) string {
	if !repo.IsWorktree {
		return fmt.Sprintf("%s (%s): not a git worktree", repo.Label, repo.Path)
	}
	branch := repo.Branch
	if branch == "" {
		branch = "unknown"
	}
	if repo.Detached {
		branch = "detached:" + branch
	}
	dirty := "clean"
	if repo.Dirty {
		dirty = fmt.Sprintf("dirty (%d changed)", repo.ChangedCount)
	}
	return fmt.Sprintf("%s (%s): git, %s, %s", repo.Label, repo.Path, branch, dirty)
}

func currentBranchFallback() string {
	branch, err := git.GetCurrentBranch()
	if err != nil || strings.TrimSpace(branch) == "" {
		return "current branch"
	}
	return branch
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func truncatePlain(s string, width int) string {
	if width <= 3 || len(s) <= width {
		return s
	}
	return s[:width-3] + "..."
}
