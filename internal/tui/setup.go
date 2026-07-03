package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/git"
)

const (
	setupFieldRunner = iota
	setupFieldSafetyMode
	setupFieldUnrestrictedAck
	setupFieldBranchBehavior
	setupFieldCreateFirstMilestone
	setupFieldMilestoneID
	setupFieldMilestoneTitle
	setupFieldMilestoneGoal
	setupFieldMilestoneCriteria
	setupFieldConfirm
	setupFieldCancel
	setupFieldCount
)

type SetupCompletedMsg struct {
	ConfigPath  string
	StatePath   string
	MilestoneID string
}

type SetupWizardModel struct {
	// ConfigPath and StatePath are startup-owned paths displayed as read-only setup details.
	ConfigPath          string
	StatePath           string
	MilestoneIDInput    textinput.Model
	MilestoneTitleInput textinput.Model
	MilestoneGoalInput  textarea.Model
	MilestoneCriteria   textarea.Model
	Runners             []runnerAvailability
	Runner              string
	Unrestricted        bool
	UnrestrictedAck     bool
	AutoBranches        bool
	CreateFirst         bool
	IsGitWorktree       bool
	FocusIndex          int
	Width               int
	Height              int
	Styles              Styles
	ErrorMsg            string
}

func NewSetupWizardModel(configPath, statePath string, styles Styles) SetupWizardModel {
	newInput := func(value string, width int, limit int) textinput.Model {
		ti := textinput.New()
		ti.SetValue(value)
		ti.Width = width
		ti.CharLimit = limit
		ti.TextStyle = styles.BlurredInput
		ti.PlaceholderStyle = styles.SubtleText
		ti.Cursor.Style = styles.AccentText
		return ti
	}

	goal := textarea.New()
	goal.Placeholder = "First milestone goal"
	goal.SetWidth(60)
	goal.SetHeight(4)
	goal.ShowLineNumbers = false
	goal.Cursor.Style = styles.AccentText

	criteria := textarea.New()
	criteria.Placeholder = "One acceptance criterion per line"
	criteria.SetWidth(60)
	criteria.SetHeight(4)
	criteria.ShowLineNumbers = false
	criteria.Cursor.Style = styles.AccentText

	runners := detectSetupRunnerAvailability()
	m := SetupWizardModel{
		ConfigPath:          configPath,
		StatePath:           statePath,
		MilestoneIDInput:    newInput("0001-first-milestone", 36, 100),
		MilestoneTitleInput: newInput("First milestone", 56, 160),
		MilestoneGoalInput:  goal,
		MilestoneCriteria:   criteria,
		Runners:             runners,
		Runner:              defaultSetupRunner(runners),
		AutoBranches:        true,
		CreateFirst:         false,
		IsGitWorktree:       git.IsGitRepository(),
		FocusIndex:          setupFieldRunner,
		Styles:              styles,
	}
	m.updateFocus()
	return m
}

func (m SetupWizardModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, textarea.Blink)
}

func (m SetupWizardModel) Update(msg tea.Msg) (SetupWizardModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		m.resizeInputs()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c", "q":
			return m, tea.Quit
		case "tab", "down":
			m.FocusIndex = m.nextFocusable(1)
			return m, m.updateFocus()
		case "shift+tab", "up":
			m.FocusIndex = m.nextFocusable(-1)
			return m, m.updateFocus()
		case "left", "h":
			m.adjustChoice(-1)
			return m, nil
		case "right", "l":
			m.adjustChoice(1)
			return m, nil
		case "enter":
			if m.FocusIndex == setupFieldConfirm {
				return m.handleConfirm()
			}
			if m.FocusIndex == setupFieldCancel {
				return m, tea.Quit
			}
			if m.isChoiceField(m.FocusIndex) {
				m.adjustChoice(1)
				return m, nil
			}
		}
	}

	switch m.FocusIndex {
	case setupFieldMilestoneID:
		m.MilestoneIDInput, cmd = m.MilestoneIDInput.Update(msg)
	case setupFieldMilestoneTitle:
		m.MilestoneTitleInput, cmd = m.MilestoneTitleInput.Update(msg)
	case setupFieldMilestoneGoal:
		m.MilestoneGoalInput, cmd = m.MilestoneGoalInput.Update(msg)
	case setupFieldMilestoneCriteria:
		m.MilestoneCriteria, cmd = m.MilestoneCriteria.Update(msg)
	}
	return m, cmd
}

func (m *SetupWizardModel) resizeInputs() {
	width := m.Width - 10
	if width < 24 {
		width = 24
	}
	if width > 70 {
		width = 70
	}
	m.MilestoneTitleInput.Width = width
	m.MilestoneGoalInput.SetWidth(width)
	m.MilestoneCriteria.SetWidth(width)
	height := 4
	if m.Height < 24 {
		height = 2
	}
	m.MilestoneGoalInput.SetHeight(height)
	m.MilestoneCriteria.SetHeight(height)
}

func (m SetupWizardModel) nextFocusable(delta int) int {
	idx := m.FocusIndex
	for {
		idx = (idx + delta + setupFieldCount) % setupFieldCount
		if m.fieldVisible(idx) {
			return idx
		}
	}
}

func (m SetupWizardModel) fieldVisible(idx int) bool {
	if idx == setupFieldUnrestrictedAck {
		return m.Unrestricted
	}
	if idx == setupFieldMilestoneID || idx == setupFieldMilestoneTitle || idx == setupFieldMilestoneGoal || idx == setupFieldMilestoneCriteria {
		return m.CreateFirst
	}
	return true
}

func (m SetupWizardModel) isChoiceField(idx int) bool {
	return idx == setupFieldRunner || idx == setupFieldSafetyMode || idx == setupFieldUnrestrictedAck || idx == setupFieldBranchBehavior || idx == setupFieldCreateFirstMilestone
}

func (m *SetupWizardModel) adjustChoice(delta int) {
	switch m.FocusIndex {
	case setupFieldRunner:
		var available []runnerAvailability
		for _, runner := range m.Runners {
			if runner.Available {
				available = append(available, runner)
			}
		}
		if len(available) == 0 {
			m.Runner = ""
			return
		}
		cur := 0
		for i, runner := range available {
			if runner.ID == m.Runner {
				cur = i
				break
			}
		}
		m.Runner = available[(cur+delta+len(available))%len(available)].ID
	case setupFieldSafetyMode:
		m.Unrestricted = !m.Unrestricted
		if !m.Unrestricted {
			m.UnrestrictedAck = false
		}
	case setupFieldUnrestrictedAck:
		m.UnrestrictedAck = !m.UnrestrictedAck
	case setupFieldBranchBehavior:
		m.AutoBranches = !m.AutoBranches
	case setupFieldCreateFirstMilestone:
		m.CreateFirst = !m.CreateFirst
	}
}

func (m *SetupWizardModel) updateFocus() tea.Cmd {
	var cmds []tea.Cmd
	if !m.fieldVisible(m.FocusIndex) {
		m.FocusIndex = m.nextFocusable(1)
	}
	for _, input := range []*textinput.Model{&m.MilestoneIDInput, &m.MilestoneTitleInput} {
		input.Blur()
		input.TextStyle = m.Styles.BlurredInput
	}
	m.MilestoneGoalInput.Blur()
	m.MilestoneCriteria.Blur()

	switch m.FocusIndex {
	case setupFieldMilestoneID:
		cmds = append(cmds, m.MilestoneIDInput.Focus())
		m.MilestoneIDInput.TextStyle = m.Styles.FocusedInput
	case setupFieldMilestoneTitle:
		cmds = append(cmds, m.MilestoneTitleInput.Focus())
		m.MilestoneTitleInput.TextStyle = m.Styles.FocusedInput
	case setupFieldMilestoneGoal:
		cmds = append(cmds, m.MilestoneGoalInput.Focus())
	case setupFieldMilestoneCriteria:
		cmds = append(cmds, m.MilestoneCriteria.Focus())
	}
	return tea.Batch(cmds...)
}

func (m SetupWizardModel) handleConfirm() (SetupWizardModel, tea.Cmd) {
	configPath := m.ConfigPath
	statePath := m.StatePath
	if m.Runner == "" || !isSetupRunnerSelectable(m.Runners, m.Runner) {
		m.ErrorMsg = "Select an available runner before confirming setup."
		return m, nil
	}
	if m.Unrestricted && !m.UnrestrictedAck {
		m.ErrorMsg = "Confirm unrestricted mode before saving that setting."
		return m, nil
	}

	var first config.Milestone
	if m.CreateFirst {
		first = config.Milestone{
			ID:                 strings.TrimSpace(m.MilestoneIDInput.Value()),
			Title:              strings.TrimSpace(m.MilestoneTitleInput.Value()),
			Goal:               strings.TrimSpace(m.MilestoneGoalInput.Value()),
			AcceptanceCriteria: splitCriteria(m.MilestoneCriteria.Value()),
			Status:             "Todo",
		}
		if first.ID == "" || first.Title == "" || first.Goal == "" {
			m.ErrorMsg = "First milestone ID, title, and goal are required when first milestone creation is enabled."
			return m, nil
		}
	}

	if err := config.InitializeMilestonesConfig(configPath); err != nil {
		m.ErrorMsg = fmt.Sprintf("Error creating milestone config: %v", err)
		return m, nil
	}

	autoBranches := m.AutoBranches
	settings := config.Settings{
		DefaultLLM:             m.Runner,
		DefaultMode:            "sandbox",
		AutoGitBranch:          &autoBranches,
		CreateMilestoneBranch:  &autoBranches,
		DefaultGitBranchPrefix: "cyclestone/milestones/",
	}
	if m.Unrestricted {
		settings.DefaultMode = "unrestricted"
	}
	settingsPath := filepath.Join(filepath.Dir(configPath), "settings.yml")
	if err := config.SaveProjectSettingsAt(settingsPath, settings); err != nil {
		m.ErrorMsg = fmt.Sprintf("Error saving settings: %v", err)
		return m, nil
	}

	milestoneID := ""
	state := &config.State{
		MilestoneStatuses:        map[string]string{},
		MilestoneCycles:          map[string]int{},
		MilestoneRecommendations: map[string]int{},
		History:                  map[string][]config.MilestoneCycleLog{},
	}
	if m.CreateFirst {
		if err := config.AddMilestone(configPath, first); err != nil {
			m.ErrorMsg = fmt.Sprintf("Error creating first milestone: %v", err)
			return m, nil
		}
		milestoneID = first.ID
		state.ActiveMilestoneID = first.ID
		state.MilestoneStatuses[first.ID] = "Todo"
		state.MilestoneCycles[first.ID] = 0
	}
	if err := config.SaveState(statePath, state); err != nil {
		m.ErrorMsg = fmt.Sprintf("Error saving state: %v", err)
		return m, nil
	}

	return m, func() tea.Msg {
		return SetupCompletedMsg{ConfigPath: configPath, StatePath: statePath, MilestoneID: milestoneID}
	}
}

func splitCriteria(value string) []string {
	var criteria []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- [ ] ")
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		if line != "" {
			criteria = append(criteria, line)
		}
	}
	return criteria
}

func (m SetupWizardModel) View() string {
	(&m).resizeInputs()
	var sb strings.Builder
	spacing := "\n"
	if m.Height >= 26 {
		spacing = "\n\n"
	}
	sb.WriteString(m.Styles.DetailHeader.Render("FIRST-RUN SETUP") + "\n")
	if m.ErrorMsg != "" {
		sb.WriteString(m.Styles.RenderError(m.ErrorMsg) + "\n")
	}
	if !m.IsGitWorktree {
		sb.WriteString(m.Styles.RenderInfo("This directory is not a Git worktree; setup can continue, but branch automation will be skipped.") + "\n")
	}
	sb.WriteString(spacing)
	sb.WriteString(m.renderStaticValue("Milestone config", m.ConfigPath) + "\n")
	sb.WriteString(m.renderStaticValue("State file", m.StatePath) + "\n")
	sb.WriteString(m.renderChoice(setupFieldRunner, "Runner", m.runnerSummary()) + "\n")
	sb.WriteString(m.renderChoice(setupFieldSafetyMode, "Safety", boolLabel(!m.Unrestricted, "Sandbox", "Unrestricted")) + "\n")
	if m.Unrestricted {
		sb.WriteString(m.renderChoice(setupFieldUnrestrictedAck, "Confirm", boolLabel(m.UnrestrictedAck, "I understand unrestricted mode", "Required before save")) + "\n")
	}
	sb.WriteString(m.renderChoice(setupFieldBranchBehavior, "Branches", boolLabel(m.AutoBranches, "Automatic milestone branches", "No branch changes")) + "\n")
	sb.WriteString(m.renderChoice(setupFieldCreateFirstMilestone, "First milestone", boolLabel(m.CreateFirst, "Create now", "Skip")) + "\n")
	if m.CreateFirst {
		sb.WriteString(m.renderInput(setupFieldMilestoneID, "Milestone ID", m.MilestoneIDInput.View()) + "\n")
		sb.WriteString(m.renderInput(setupFieldMilestoneTitle, "Title", m.MilestoneTitleInput.View()) + "\n")
		sb.WriteString(m.renderInput(setupFieldMilestoneGoal, "Goal", m.MilestoneGoalInput.View()) + "\n")
		sb.WriteString(m.renderInput(setupFieldMilestoneCriteria, "Criteria", m.MilestoneCriteria.View()) + "\n")
	}
	sb.WriteString(spacing)
	sb.WriteString(m.renderButtons() + "\n")
	sb.WriteString(renderCommandHelp(m.Styles, []string{"Tab Next", "Shift+Tab Back", "Left/Right Change", "Enter Select", "Esc Cancel"}, setupMaxInt(m.Width-4, 20)))

	bodyHeight := m.Height - 2
	if bodyHeight < 8 {
		bodyHeight = 8
	}
	bodyWidth := m.Width - 4
	if bodyWidth < 24 {
		bodyWidth = 24
	}
	return m.Styles.ActiveBorder.Width(bodyWidth).Height(bodyHeight).Render(truncateLines(sb.String(), bodyHeight))
}

func (m SetupWizardModel) renderInput(idx int, label, value string) string {
	style := m.Styles.DetailValue
	if m.FocusIndex == idx {
		style = m.Styles.DetailLabel.Underline(true)
	}
	return style.Render(label) + "\n" + value
}

func (m SetupWizardModel) renderStaticValue(label, value string) string {
	return m.Styles.DetailValue.Render(label) + ": " + m.Styles.SubtleText.Render(value)
}

func (m SetupWizardModel) renderChoice(idx int, label, value string) string {
	style := m.Styles.DetailValue
	if m.FocusIndex == idx {
		style = m.Styles.DetailLabel.Underline(true)
	}
	return style.Render(label) + ": " + value
}

func (m SetupWizardModel) renderButtons() string {
	confirm := m.Styles.SuccessText.Render(" [ Confirm setup ] ")
	cancel := m.Styles.HelpStyle.Render(" [ Cancel ] ")
	if m.FocusIndex == setupFieldConfirm {
		confirm = m.Styles.TableSelectedRow.Render(" [ Confirm setup ] ")
	}
	if m.FocusIndex == setupFieldCancel {
		cancel = m.Styles.TableSelectedRow.Render(" [ Cancel ] ")
	}
	return confirm + "  " + cancel
}

func (m SetupWizardModel) runnerSummary() string {
	var parts []string
	for _, runner := range m.Runners {
		name := runner.ID
		if runner.ID == m.Runner {
			name = "(" + runner.ID + ")"
		}
		if runner.Available {
			parts = append(parts, m.Styles.SuccessText.Render(name))
		} else if m.Width >= 70 {
			parts = append(parts, m.Styles.SubtleText.Render(runner.ID+" unavailable"))
		}
	}
	if len(parts) == 0 {
		return m.Styles.RenderError("No supported runner detected")
	}
	return strings.Join(parts, " ")
}

func boolLabel(enabled bool, on, off string) string {
	if enabled {
		return "(*) " + on
	}
	return "( ) " + off
}

func setupMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
