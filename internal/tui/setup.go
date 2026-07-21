package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	setupFieldAuthorPrefix
	setupFieldAgentInstructions
	setupFieldAgentInstructionsPreview
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
	ConfigPath string
	StatePath  string
	// GlobalSettings holds the resolved global settings used to preview the
	// effective value of any field set to "inherit" on the init screen.
	GlobalSettings      config.Settings
	AuthorPrefixInput   textinput.Model
	MilestoneIDInput    textinput.Model
	MilestoneTitleInput textinput.Model
	MilestoneGoalInput  textarea.Model
	MilestoneCriteria   textarea.Model
	Runners             []runnerAvailability
	Runner              string
	// RunnerInherit, SafetyInherit, and BranchesInherit mirror the settings
	// screen "inherit" option: when true the corresponding project setting is
	// saved empty/nil so it resolves from the global settings at merge time.
	RunnerInherit           bool
	Unrestricted            bool
	SafetyInherit           bool
	UnrestrictedAck         bool
	AutoBranches            bool
	BranchesInherit         bool
	CreateAgentInstructions bool
	AgentInstructions       textarea.Model
	CreateFirst             bool
	IsGitWorktree           bool
	FocusIndex              int
	Width                   int
	Height                  int
	Styles                  Styles
	ErrorMsg                string
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

	agentInstructions := textarea.New()
	agentInstructions.Placeholder = "Durable agent instructions"
	agentInstructions.SetValue(defaultAgentInstructionsContent())
	agentInstructions.SetWidth(60)
	agentInstructions.SetHeight(5)
	agentInstructions.ShowLineNumbers = false
	agentInstructions.Cursor.Style = styles.AccentText

	runners := detectSetupRunnerAvailability()
	global, _ := config.LoadGlobalSettings()
	authorPrefixVal := global.AuthorPrefix
	if authorPrefixVal == "" {
		authorPrefixVal = config.GetDefaultAuthorPrefix(global)
	}
	m := SetupWizardModel{
		ConfigPath:          configPath,
		StatePath:           statePath,
		GlobalSettings:      global,
		AuthorPrefixInput:   newInput(authorPrefixVal, 16, 20),
		MilestoneIDInput:    newInput("0001-first-milestone", 36, 100),
		MilestoneTitleInput: newInput("First milestone", 56, 160),
		MilestoneGoalInput:  goal,
		MilestoneCriteria:   criteria,
		Runners:             runners,
		// "inherit" is the default for runner, safety, and branches so the
		// project settings defer to the global configuration, matching the
		// settings screen project-scope behavior. Runner is left empty
		// because RunnerInherit is true; the cycling logic discovers the
		// first available runner when the user switches away from inherit.
		Runner:                  "",
		RunnerInherit:           true,
		SafetyInherit:           true,
		AutoBranches:            true,
		BranchesInherit:         true,
		CreateAgentInstructions: true,
		AgentInstructions:       agentInstructions,
		CreateFirst:             false,
		IsGitWorktree:           git.IsGitRepository(),
		FocusIndex:              setupFieldRunner,
		Styles:                  styles,
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
	case setupFieldAuthorPrefix:
		m.AuthorPrefixInput, cmd = m.AuthorPrefixInput.Update(msg)
	case setupFieldAgentInstructionsPreview:
		m.AgentInstructions, cmd = m.AgentInstructions.Update(msg)
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
	m.AgentInstructions.SetWidth(width)
	height := 4
	if m.Height < 24 {
		height = 2
	}
	m.MilestoneGoalInput.SetHeight(height)
	m.MilestoneCriteria.SetHeight(height)
	agentHeight := 5
	if m.Height < 28 {
		agentHeight = 3
	}
	m.AgentInstructions.SetHeight(agentHeight)
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
	if idx == setupFieldAuthorPrefix {
		return m.GlobalSettings.AuthorPrefix == ""
	}
	if idx == setupFieldUnrestrictedAck {
		return m.Unrestricted
	}
	if idx == setupFieldAgentInstructionsPreview {
		return m.CreateAgentInstructions && m.Height >= 24
	}
	if idx == setupFieldMilestoneID || idx == setupFieldMilestoneTitle || idx == setupFieldMilestoneGoal || idx == setupFieldMilestoneCriteria {
		return m.CreateFirst
	}
	return true
}

func (m SetupWizardModel) isChoiceField(idx int) bool {
	return idx == setupFieldRunner || idx == setupFieldSafetyMode || idx == setupFieldUnrestrictedAck || idx == setupFieldBranchBehavior || idx == setupFieldAgentInstructions || idx == setupFieldCreateFirstMilestone
}

// runnerCycleOptions builds the ordered list of selectable runner options for
// the init screen: "inherit" first, then every available runner. Inherit lets
// the project setting resolve from the global configuration at merge time.
func (m SetupWizardModel) runnerCycleOptions() []struct {
	inherit bool
	id      string
} {
	var opts []struct {
		inherit bool
		id      string
	}
	opts = append(opts, struct {
		inherit bool
		id      string
	}{inherit: true})
	for _, r := range m.Runners {
		if r.Available {
			opts = append(opts, struct {
				inherit bool
				id      string
			}{id: r.ID})
		}
	}
	return opts
}

func (m *SetupWizardModel) adjustChoice(delta int) {
	switch m.FocusIndex {
	case setupFieldRunner:
		opts := m.runnerCycleOptions()
		if len(opts) <= 1 {
			m.RunnerInherit = true
			m.Runner = ""
			return
		}
		cur := 0
		if !m.RunnerInherit {
			for i, opt := range opts {
				if !opt.inherit && opt.id == m.Runner {
					cur = i
					break
				}
			}
		}
		next := (cur + delta + len(opts)) % len(opts)
		m.RunnerInherit = opts[next].inherit
		if !opts[next].inherit {
			m.Runner = opts[next].id
		} else {
			m.Runner = ""
		}
	case setupFieldSafetyMode:
		// Cycle: inherit -> sandbox -> unrestricted -> inherit.
		switch {
		case m.SafetyInherit:
			m.SafetyInherit = false
			if delta > 0 {
				m.Unrestricted = false
			} else {
				m.Unrestricted = true
			}
		case !m.Unrestricted:
			if delta > 0 {
				m.Unrestricted = true
			} else {
				m.SafetyInherit = true
				m.Unrestricted = false
			}
		default:
			if delta > 0 {
				m.SafetyInherit = true
				m.Unrestricted = false
			} else {
				m.Unrestricted = false
			}
		}
		if m.SafetyInherit {
			m.UnrestrictedAck = false
		}
	case setupFieldUnrestrictedAck:
		m.UnrestrictedAck = !m.UnrestrictedAck
	case setupFieldBranchBehavior:
		// Cycle: inherit -> automatic -> manual -> inherit.
		switch {
		case m.BranchesInherit:
			m.BranchesInherit = false
			if delta > 0 {
				m.AutoBranches = true
			} else {
				m.AutoBranches = false
			}
		case m.AutoBranches:
			if delta > 0 {
				m.AutoBranches = false
			} else {
				m.BranchesInherit = true
			}
		default:
			if delta > 0 {
				m.BranchesInherit = true
			} else {
				m.AutoBranches = true
			}
		}
	case setupFieldAgentInstructions:
		m.CreateAgentInstructions = !m.CreateAgentInstructions
	case setupFieldCreateFirstMilestone:
		m.CreateFirst = !m.CreateFirst
	}
}

func (m *SetupWizardModel) updateFocus() tea.Cmd {
	var cmds []tea.Cmd
	if !m.fieldVisible(m.FocusIndex) {
		m.FocusIndex = m.nextFocusable(1)
	}
	for _, input := range []*textinput.Model{&m.MilestoneIDInput, &m.MilestoneTitleInput, &m.AuthorPrefixInput} {
		input.Blur()
		input.TextStyle = m.Styles.BlurredInput
	}
	m.MilestoneGoalInput.Blur()
	m.MilestoneCriteria.Blur()
	m.AgentInstructions.Blur()

	switch m.FocusIndex {
	case setupFieldAuthorPrefix:
		cmds = append(cmds, m.AuthorPrefixInput.Focus())
		m.AuthorPrefixInput.TextStyle = m.Styles.FocusedInput
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
	case setupFieldAgentInstructionsPreview:
		cmds = append(cmds, m.AgentInstructions.Focus())
	}
	return tea.Batch(cmds...)
}

func (m SetupWizardModel) handleConfirm() (SetupWizardModel, tea.Cmd) {
	configPath := m.ConfigPath
	statePath := m.StatePath
	if !m.RunnerInherit {
		if m.Runner == "" || !isSetupRunnerSelectable(m.Runners, m.Runner) {
			m.ErrorMsg = "Select an available runner before confirming setup."
			return m, nil
		}
	}
	if m.Unrestricted && !m.UnrestrictedAck {
		m.ErrorMsg = "Confirm unrestricted mode before saving that setting."
		return m, nil
	}

	var first config.Milestone
	if m.CreateFirst {
		now := time.Now().UTC().Format(time.RFC3339)
		first = config.Milestone{
			ID:                 strings.TrimSpace(m.MilestoneIDInput.Value()),
			Title:              strings.TrimSpace(m.MilestoneTitleInput.Value()),
			Goal:               strings.TrimSpace(m.MilestoneGoalInput.Value()),
			AcceptanceCriteria: splitCriteria(m.MilestoneCriteria.Value()),
			Status:             "Todo",
			CreatedBy:          "tui",
			UpdatedBy:          "tui",
			CreatedAt:          now,
			UpdatedAt:          now,
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

	settings := config.Settings{
		DefaultGitBranchPrefix: "cyclestone/milestones/",
	}
	proposeUpdates := true
	autoApplyUpdates := false
	settings.AgentInstructions = config.AgentInstructionsSettings{
		File:             "AGENTS.md",
		ProposeUpdates:   &proposeUpdates,
		AutoApplyUpdates: &autoApplyUpdates,
	}
	// Save explicit values only for non-inherited fields so the project
	// settings defer to the global configuration for inherited ones.
	if !m.RunnerInherit {
		settings.DefaultLLM = m.Runner
	}
	if !m.SafetyInherit {
		settings.DefaultMode = "sandbox"
		if m.Unrestricted {
			settings.DefaultMode = "unrestricted"
		}
	}
	if !m.BranchesInherit {
		autoBranches := m.AutoBranches
		settings.AutoGitBranch = &autoBranches
		settings.CreateMilestoneBranch = &autoBranches
	}

	settingsPath := filepath.Join(filepath.Dir(configPath), "settings.yml")
	if err := config.SaveProjectSettingsAt(settingsPath, settings); err != nil {
		m.ErrorMsg = fmt.Sprintf("Error saving settings: %v", err)
		return m, nil
	}

	authorPrefix := strings.TrimSpace(m.AuthorPrefixInput.Value())
	if authorPrefix == "" {
		authorPrefix = config.GetDefaultAuthorPrefix(m.GlobalSettings)
	}
	globalSettings, err := config.LoadGlobalSettings()
	if err != nil {
		globalSettings = config.LoadDefaultSettings()
	}
	globalSettings.AuthorPrefix = authorPrefix
	if err := config.SaveGlobalSettings(globalSettings); err != nil {
		m.ErrorMsg = fmt.Sprintf("Error saving global author prefix: %v", err)
		return m, nil
	}
	if m.CreateAgentInstructions {
		rootDir := filepath.Dir(filepath.Dir(configPath))
		agentsPath := filepath.Join(rootDir, "AGENTS.md")
		if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
			if err := os.WriteFile(agentsPath, []byte(strings.TrimSpace(m.AgentInstructions.Value())+"\n"), 0644); err != nil {
				m.ErrorMsg = fmt.Sprintf("Error creating AGENTS.md: %v", err)
				return m, nil
			}
		}
	}

	milestoneID := ""
	state := &config.State{
		MilestoneStatuses:                     map[string]string{},
		MilestoneCycles:                       map[string]int{},
		MilestoneRecommendations:              map[string]int{},
		MilestoneAgentInstructionUpdateScores: map[string]int{},
		History:                               map[string][]config.MilestoneCycleLog{},
	}
	if m.CreateFirst {
		milestonesDir := filepath.Join(filepath.Dir(configPath), "milestones")
		if _, err := config.SaveMilestoneToFolder(milestonesDir, first, ""); err != nil {
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

func defaultAgentInstructionsContent() string {
	return strings.TrimSpace(`# Agent Instructions

- Keep current operating instructions concise and durable in this file.
- Keep chronological decisions in .cyclestone/DECISIONS.md.
- Propose instruction updates in cycle reports or handoffs for human review instead of editing this file automatically.`)
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
	sb.WriteString(m.renderChoice(setupFieldSafetyMode, "Safety", m.safetySummary()) + "\n")
	if m.Unrestricted {
		sb.WriteString(m.renderChoice(setupFieldUnrestrictedAck, "Confirm", boolLabel(m.UnrestrictedAck, "I understand unrestricted mode", "Required before save")) + "\n")
	}
	sb.WriteString(m.renderChoice(setupFieldBranchBehavior, "Branches", m.branchesSummary()) + "\n")
	if m.fieldVisible(setupFieldAuthorPrefix) {
		sb.WriteString(m.renderInput(setupFieldAuthorPrefix, "Author prefix (global setting)", m.AuthorPrefixInput.View()) + "\n")
	}
	sb.WriteString(m.renderChoice(setupFieldAgentInstructions, "AGENTS.md", boolLabel(m.CreateAgentInstructions, "Create from preview", "Skip")) + "\n")
	if m.CreateAgentInstructions && m.Height >= 24 {
		sb.WriteString(m.renderInput(setupFieldAgentInstructionsPreview, "AGENTS.md preview", m.AgentInstructions.View()) + "\n")
	}
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

// setupOptionLabel represents one selectable option in a multi-option setup
// choice field.
type setupOptionLabel struct {
	Label    string
	Selected bool
}

// renderSetupOptions renders multiple choice options with "(*)" for the
// selected option and "( )" for unselected ones, matching the existing setup
// screen convention used by the setup choice fields.
func renderSetupOptions(options []setupOptionLabel) string {
	parts := make([]string, 0, len(options))
	for _, opt := range options {
		if opt.Selected {
			parts = append(parts, "(*) "+opt.Label)
		} else {
			parts = append(parts, "( ) "+opt.Label)
		}
	}
	return strings.Join(parts, "  ")
}

// setupInheritLabel renders the "inherit" option label, showing the effective
// global value in wide terminals and a compact "inherit" label in narrow ones.
func (m SetupWizardModel) setupInheritLabel(globalValue, fallback string) string {
	resolved := defaultString(globalValue, fallback)
	if m.Width < 60 {
		return "inherit"
	}
	return fmt.Sprintf("inherit (global: %s)", resolved)
}

// safetySummary renders the Safety field with three options: inherit, Sandbox,
// and Unrestricted, mirroring the settings screen project-scope "inherit" option.
func (m SetupWizardModel) safetySummary() string {
	globalMode := defaultString(m.GlobalSettings.DefaultMode, "sandbox")
	return renderSetupOptions([]setupOptionLabel{
		{Label: m.setupInheritLabel(globalMode, "sandbox"), Selected: m.SafetyInherit},
		{Label: "Sandbox", Selected: !m.SafetyInherit && !m.Unrestricted},
		{Label: "Unrestricted", Selected: !m.SafetyInherit && m.Unrestricted},
	})
}

// branchesSummary renders the Branches field with three options: inherit,
// Automatic, and No branch changes (abbreviated to Auto/Manual in narrow
// terminals). The compact labels keep all three options visible on one line
// even at 80-column widths alongside the inherit (global: ...) preview.
func (m SetupWizardModel) branchesSummary() string {
	globalBranches := boolValue(m.GlobalSettings.AutoGitBranch, true)
	branchLeft, branchRight := "Automatic", "No branch changes"
	if m.Width < 60 {
		branchLeft, branchRight = "Auto", "Manual"
	}
	return renderSetupOptions([]setupOptionLabel{
		{Label: m.setupInheritLabel(yesNo(globalBranches), "yes"), Selected: m.BranchesInherit},
		{Label: branchLeft, Selected: !m.BranchesInherit && m.AutoBranches},
		{Label: branchRight, Selected: !m.BranchesInherit && !m.AutoBranches},
	})
}

// runnerSummary renders every supported runner plus a leading "inherit" option
// so all runner choices stay visible regardless of terminal width. The
// "inherit" option lets the project defer runner selection to the global
// configuration. Unavailable runners are shown with their ID only in narrow
// terminals to keep the line compact; wider terminals append an "unavailable"
// suffix for clarity. The currently selected runner (or inherit) is wrapped in
// parentheses, matching the existing selection indicator.
func (m SetupWizardModel) runnerSummary() string {
	var parts []string
	globalRunner := defaultString(m.GlobalSettings.DefaultLLM, "codex")
	inheritLabel := "inherit"
	if m.Width >= 70 {
		inheritLabel = fmt.Sprintf("inherit (global: %s)", globalRunner)
	}
	if m.RunnerInherit {
		parts = append(parts, m.Styles.SuccessText.Render("("+inheritLabel+")"))
	} else {
		parts = append(parts, m.Styles.HelpStyle.Render(inheritLabel))
	}
	for _, runner := range m.Runners {
		name := runner.ID
		if runner.ID == m.Runner && !m.RunnerInherit {
			name = "(" + runner.ID + ")"
		}
		if runner.Available {
			parts = append(parts, m.Styles.SuccessText.Render(name))
			continue
		}
		label := runner.ID
		if m.Width >= 70 {
			label = runner.ID + " unavailable"
		}
		parts = append(parts, m.Styles.SubtleText.Render(label))
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
