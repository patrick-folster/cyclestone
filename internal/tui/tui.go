package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/executor"
	"github.com/patrick-folster/cyclestone/internal/git"
	"github.com/patrick-folster/cyclestone/resources"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

const (
	defaultTerminalWidth  = 80
	defaultTerminalHeight = 24
)

var terminalSize = func() (int, int, error) {
	return term.GetSize(os.Stdout.Fd())
}

// Screen identifies the currently active UI view.
type Screen int

const (
	ScreenDashboard Screen = iota
	ScreenDetails
	ScreenRunner
	ScreenCreateMilestone
	ScreenPreflight
	ScreenSettings
	ScreenAgentGroups
	ScreenSetup
)

// ChangeScreenMsg is sent to switch views.
type ChangeScreenMsg struct {
	Screen Screen
	Data   interface{}
}

// UpdateMilestoneMsg is sent to log actions or transitions on milestones.
type UpdateMilestoneMsg struct {
	MilestoneID string
	Action      string // e.g., "cycle_logged", "status_changed"
	Status      string // optional
}

type ShowDeleteMilestoneMsg struct {
	Milestone config.Milestone
}

type DeleteMilestoneMsg struct {
	MilestoneID string
}

type DeleteCycleMsg struct {
	MilestoneID string
	CycleNumber int
}

// LogCycleResultMsg is sent asynchronously when a work cycle is completed and Git info is fetched.
type LogCycleResultMsg struct {
	MilestoneID string
	Branch      string
	CommitHash  string
	Timestamp   string
}

// RootModel is the top-level Bubble Tea model containing application state.
type RootModel struct {
	ActiveScreen    Screen
	Dashboard       DashboardModel
	Details         DetailsModel
	Runner          RunnerModel
	CreateMilestone CreateMilestoneModel
	Preflight       PreflightModel
	Settings        SettingsModel
	AgentGroups     AgentGroupsModel
	Setup           SetupWizardModel
	Width           int
	Height          int
	Config          *config.Config
	State           *config.State
	ConfigPath      string
	StatePath       string
	NoBranchChange  bool
	Unrestricted    bool
	MsgChan         chan tea.Msg
	Styles          Styles
	StatusMsg       string
	StatusTime      time.Time
	MissingConfig   bool
	InitConfigError string
}

// NewRootModel constructs the RootModel and initializes its sub-models.
func NewRootModel(cfg *config.Config, state *config.State, configPath, statePath string, noBranchChange, unrestricted, disableBold, disableRoundedBorders bool) RootModel {
	styles := DefaultStyles(disableBold, disableRoundedBorders)

	dashboard := NewDashboardModel(cfg, state, styles)
	details := NewDetailsModel(styles)
	runner := NewRunnerModel(styles)
	createMilestone := NewCreateMilestoneModel(styles)
	createMilestone.NextID = generateNextID(cfg)
	preflight := NewPreflightModel(styles)
	settings := NewSettingsModel(styles)
	agentGroups := NewAgentGroupsModel(styles)
	setup := NewSetupWizardModel(configPath, statePath, styles)

	return RootModel{
		ActiveScreen:    ScreenDashboard,
		Dashboard:       dashboard,
		Details:         details,
		Runner:          runner,
		CreateMilestone: createMilestone,
		Preflight:       preflight,
		Settings:        settings,
		AgentGroups:     agentGroups,
		Setup:           setup,
		Config:          cfg,
		State:           state,
		ConfigPath:      configPath,
		StatePath:       statePath,
		NoBranchChange:  noBranchChange,
		Unrestricted:    unrestricted,
		MsgChan:         make(chan tea.Msg, 100),
		Styles:          styles,
	}
}

// Init initializes the Bubble Tea program.
func (m RootModel) Init() tea.Cmd {
	return tea.Batch(
		initialWindowSizeCmd(),
		m.Dashboard.Init(),
		m.Details.Init(),
		m.Runner.Init(),
		m.CreateMilestone.Init(),
		m.Preflight.Init(),
		m.Settings.Init(),
		m.AgentGroups.Init(),
		m.Setup.Init(),
	)
}

// initialWindowSizeCmd queries the terminal's width and height on startup.
//
// CRITICAL FUNCTIONALITY NOTE:
// In Bubble Tea programs, if the initial window dimensions are not set or received, the TUI
// can hang indefinitely on a blank screen or a "Loading..." placeholder.
// To bypass this initialization hang, we query the terminal dimensions immediately using
// "github.com/charmbracelet/x/term" and immediately return a tea.WindowSizeMsg.
// In environments where the terminal is non-TTY, redirected, or inside a test framework
// where terminal size queries fail, it degrades gracefully to a default fallback layout
// of 80x24 (defaultTerminalWidth x defaultTerminalHeight).
func initialWindowSizeCmd() tea.Cmd {
	return func() tea.Msg {
		width, height, err := terminalSize()
		if err != nil || width <= 0 || height <= 0 {
			width = defaultTerminalWidth
			height = defaultTerminalHeight
		}
		return tea.WindowSizeMsg{Width: width, Height: height}
	}
}

// ListenForExecutorMessages returns a tea.Cmd that blocks until an executor event is ready.
func (m RootModel) ListenForExecutorMessages() tea.Cmd {
	return func() tea.Msg {
		return <-m.MsgChan
	}
}

// ResolveMilestoneTemplate resolves the template for milestone spec markdown.
func ResolveMilestoneTemplate() (string, error) {
	// 1. Local Project Folder
	localPath := filepath.Join(".cyclestone", "templates", "milestone.md")
	if data, err := os.ReadFile(localPath); err == nil {
		return string(data), nil
	}

	// 2. Global Config Folder
	globalDir := config.GetGlobalConfigDir()
	if globalDir != "" {
		globalPath := filepath.Join(globalDir, "templates", "milestone.md")
		if data, err := os.ReadFile(globalPath); err == nil {
			return string(data), nil
		}
	}

	// 3. Embedded Fallback
	defaultTemplate := `# Milestone Spec: {{ID}} - {{TITLE}}

## Goal
{{GOAL}}

## Acceptance Criteria
{{ACCEPTANCE_CRITERIA}}

## Likely Areas to Inspect
- [ ] Root repository and configured/discovered repositories relevant to this milestone.

## Risks & Unknowns
- None identified.
`
	return defaultTemplate, nil
}

// Update routes messages to the active sub-model and processes global/async actions.
func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.MissingConfig {
			var setupCmd tea.Cmd
			m.Setup, setupCmd = m.Setup.Update(msg)
			return m, setupCmd
		}

		switch msg.String() {
		case "ctrl+c":
			if m.ActiveScreen == ScreenRunner && !m.Runner.Finished {
				// Forward ctrl+c to the runner model instead of quitting immediately
				var rCmd tea.Cmd
				m.Runner, rCmd = m.Runner.Update(msg)
				return m, rCmd
			}
			return m, tea.Quit
		case "q":
			if m.ActiveScreen == ScreenRunner && !m.Runner.Finished {
				break
			}
			if m.ActiveScreen == ScreenCreateMilestone && (m.CreateMilestone.Loading || m.CreateMilestone.FocusIndex == 0 || m.CreateMilestone.FocusIndex == 1) {
				break
			}
			if m.ActiveScreen == ScreenSettings {
				if m.Settings.IsTextInputFocused() {
					break
				}
				if m.Settings.HasUnsavedChanges() {
					m.Settings.ShowDiscardPrompt = true
					m.Settings.DiscardQuit = true
					return m, nil
				}
			}
			if m.ActiveScreen == ScreenAgentGroups && m.AgentGroups.FocusCol == 3 {
				break
			}
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height

		// Propagate window resize to submodels
		m.Dashboard, cmd = m.Dashboard.Update(msg)
		cmds = append(cmds, cmd)

		m.Details, cmd = m.Details.Update(msg)
		cmds = append(cmds, cmd)

		m.Runner, cmd = m.Runner.Update(msg)
		cmds = append(cmds, cmd)

		m.CreateMilestone, cmd = m.CreateMilestone.Update(msg)
		cmds = append(cmds, cmd)

		m.Preflight, cmd = m.Preflight.Update(msg)
		cmds = append(cmds, cmd)

		m.Settings, cmd = m.Settings.Update(msg)
		cmds = append(cmds, cmd)

		m.AgentGroups, cmd = m.AgentGroups.Update(msg)
		cmds = append(cmds, cmd)
		m.Setup, cmd = m.Setup.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case ShowDeleteMilestoneMsg:
		m.ActiveScreen = ScreenDetails
		m.initDetailsScreen(msg.Milestone)
		m.Details.ConfirmDeleteMilestone = true
		return m, nil

	case ChangeScreenMsg:
		m.ActiveScreen = msg.Screen
		if msg.Screen == ScreenDetails {
			if ms, ok := msg.Data.(config.Milestone); ok {
				m.initDetailsScreen(ms)
			}
		} else if msg.Screen == ScreenDashboard {
			// Sync any changes back to the dashboard state
			m.Dashboard.State = m.State
			m.Dashboard.updateTableRows()
		} else if msg.Screen == ScreenCreateMilestone {
			m.CreateMilestone = NewCreateMilestoneModel(m.Styles)
			m.CreateMilestone.NextID = generateNextID(m.Config)
			m.CreateMilestone.Width = m.Width
			m.CreateMilestone.Height = m.Height
			settings := config.LoadMergedSettings()
			defaultRunner := normalizeMilestoneRunner(settings.DefaultLLM)
			m.CreateMilestone.DefaultLLM = settings.DefaultLLM
			m.CreateMilestone.RunnerType = defaultRunner
			createBranch := false
			if settings.CreateMilestoneBranch != nil {
				createBranch = *settings.CreateMilestoneBranch
			}
			m.CreateMilestone.CreateBranch = createBranch

			if cycleMsg, ok := msg.Data.(StartCycleMsg); ok {
				m.CreateMilestone.Mode = ModeCycleNote
				m.CreateMilestone.RunMilestone = cycleMsg.Milestone
				m.CreateMilestone.RunRunnerLLM = cycleMsg.RunnerLLM
				m.CreateMilestone.RunRunnerMode = cycleMsg.RunnerMode
				m.CreateMilestone.RunNoBranch = cycleMsg.NoBranchChange
				m.CreateMilestone.RunGroup = cycleMsg.Group
				m.CreateMilestone.RunSingleID = cycleMsg.SingleAgentID
				m.CreateMilestone.GoalInput.Placeholder = "Enter optional cycle note / comment here..."
				m.CreateMilestone.GoalInput.SetValue("")
				m.CreateMilestone.FocusIndex = 0
			} else {
				m.CreateMilestone.Mode = ModeCreateMilestone
			}
			m.CreateMilestone.recalcHeights()
		} else if msg.Screen == ScreenPreflight {
			if cycleMsg, ok := msg.Data.(StartCycleMsg); ok {
				m.Preflight = NewPreflightModel(m.Styles)
				m.Preflight.Width = m.Width
				m.Preflight.Height = m.Height
				m.Preflight.Load(cycleMsg, m.State, m.ConfigPath, m.StatePath)
			}
		} else if msg.Screen == ScreenSettings {
			m.Settings.loadSettingsDrafts()
			m.Settings.Width = m.Width
			m.Settings.Height = m.Height
			m.Settings.FocusIndex = 0
			m.Settings.ActiveGroup = -1
			m.Settings.SelectedGroup = 0
			m.Settings.GroupScrollOffset = 0
			m.Settings.DetailScrollOffset = 0
			m.Settings.ErrorMsg = ""
			m.Settings.SuccessMsg = ""
			m.Settings.ShowDiscardPrompt = false
			m.Settings.DiscardQuit = false
		} else if msg.Screen == ScreenAgentGroups {
			m.AgentGroups.loadAgentGroups()
			m.AgentGroups.Width = m.Width
			m.AgentGroups.Height = m.Height
			m.AgentGroups.SelectedGroupIdx = 0
			m.AgentGroups.SelectedAgentIdx = 0
			m.AgentGroups.AvailableAgentIdx = 0
			m.AgentGroups.FocusCol = 0
			m.AgentGroups.NewGroupName = ""
			m.AgentGroups.SavePrompt = false
			m.AgentGroups.ErrorMsg = ""
			m.AgentGroups.SuccessMsg = ""
			m.AgentGroups.HasChanges = false
			m.AgentGroups.ClampScrollOffsets()
		} else if msg.Screen == ScreenSetup {
			m.Setup = NewSetupWizardModel(m.ConfigPath, m.StatePath, m.Styles)
			m.Setup.Width = m.Width
			m.Setup.Height = m.Height
			m.Setup.resizeInputs()
		}
		return m, nil

	case CreateMilestoneMsg:
		newMilestone := config.Milestone{
			ID:                 msg.ID,
			Title:              msg.Title,
			Goal:               msg.Goal,
			AcceptanceCriteria: msg.AcceptanceCriteria,
			Status:             "Todo",
			Cycles:             0,
			Checks:             msg.Checks,
		}

		// Handle CreateMilestoneBranch option
		if msg.CreateBranch {
			settings := config.LoadMergedSettings()
			prefix := settings.DefaultGitBranchPrefix
			if prefix == "" {
				prefix = "cyclestone/milestones/"
			}
			branchName := prefix + msg.ID
			repos := git.GetTrackedRepos()
			for _, repo := range repos {
				_ = git.CheckoutOrCreateBranchInDir(repo.Path, branchName)
			}
		}

		if msg.RunnerType != "template" {
			// Ensure milestones directory exists
			milestonesDir := filepath.Join(".cyclestone", "milestones")
			if err := os.MkdirAll(milestonesDir, 0755); err != nil {
				m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error creating milestones directory: %v", err)
				return m, nil
			}

			m.CreateMilestone.Loading = true
			m.CreateMilestone.Logs = nil

			absRoot, err := filepath.Abs(".")
			if err != nil {
				absRoot = "."
			}

			idParts := strings.Split(msg.ID, "-")
			idPrefix := idParts[0]

			prompt := resources.CreatorPrompt
			prompt = strings.ReplaceAll(prompt, "{{ID_PREFIX}}", idPrefix)
			prompt = strings.ReplaceAll(prompt, "{{TITLE}}", msg.Title)
			prompt = strings.ReplaceAll(prompt, "{{GOAL}}", msg.Goal)
			prompt = strings.ReplaceAll(prompt, "`.cyclestone/milestones/", fmt.Sprintf("`%s/.cyclestone/milestones/", absRoot))

			// Inject absolute repository root info
			rootInfo := fmt.Sprintf("\n## Repository Information\n\nRepository root: %s\n\n", absRoot)
			prompt = rootInfo + prompt

			safetyRules := strings.ReplaceAll(resources.SafetyRules, "root (`.`)", fmt.Sprintf("root (`%s`)", absRoot))
			safetyRules = strings.ReplaceAll(safetyRules, "outside the root `.`", fmt.Sprintf("outside the root `%s`", absRoot))
			safetyRules = strings.ReplaceAll(safetyRules, "workspace root (`.`)", fmt.Sprintf("workspace root (`%s`)", absRoot))
			prompt += "\n\n## Workspace Safety Rules\n\n" + safetyRules + "\n\n"

			ctx, cancel := context.WithCancel(context.Background())

			opts := executor.RunOptions{
				ConfigPath:     m.ConfigPath,
				StatePath:      m.StatePath,
				NoBranchChange: m.NoBranchChange,
				Unrestricted:   m.Unrestricted,
			}

			// Clear executor message queue
			for len(m.MsgChan) > 0 {
				<-m.MsgChan
			}

			go func() {
				defer cancel()
				executor.ExecuteMilestoneCreation(ctx, msg.RunnerType, prompt, opts, m.MsgChan, idPrefix, msg.Title)
			}()

			return m, tea.Batch(
				m.CreateMilestone.Spinner.Tick,
				m.ListenForExecutorMessages(),
			)
		}

		// 1. Resolve template content.
		tpl, err := ResolveMilestoneTemplate()
		if err != nil {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error resolving template: %v", err)
			return m, nil
		}

		// 2. Perform placeholder replacement.
		var acLines []string
		for _, ac := range msg.AcceptanceCriteria {
			acLines = append(acLines, fmt.Sprintf("- [ ] %s", ac))
		}
		acFormatted := strings.Join(acLines, "\n")
		if acFormatted == "" {
			acFormatted = "- None defined."
		}

		replaced := strings.ReplaceAll(tpl, "{{ID}}", msg.ID)
		replaced = strings.ReplaceAll(replaced, "{{TITLE}}", msg.Title)
		replaced = strings.ReplaceAll(replaced, "{{GOAL}}", msg.Goal)
		replaced = strings.ReplaceAll(replaced, "{{ACCEPTANCE_CRITERIA}}", acFormatted)
		replaced = strings.ReplaceAll(replaced, "{{DATE}}", time.Now().Format("2006-01-02"))

		// 3. Write markdown file to .cyclestone/milestones/<id>.md.
		milestonesDir := filepath.Join(".cyclestone", "milestones")
		if err := os.MkdirAll(milestonesDir, 0755); err != nil {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error creating milestones directory: %v", err)
			return m, nil
		}

		mdPath := filepath.Join(milestonesDir, fmt.Sprintf("%s.md", msg.ID))
		if err := os.WriteFile(mdPath, []byte(replaced), 0644); err != nil {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error writing markdown file: %v", err)
			return m, nil
		}

		// 4. Call config.AddMilestone.
		if err := config.AddMilestone(m.ConfigPath, newMilestone); err != nil {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error adding milestone: %v", err)
			return m, nil
		}

		// 5. Reload config (LoadConfig) and update table rows on Dashboard.
		cfg, err := config.LoadConfig(m.ConfigPath)
		if err != nil {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error reloading config: %v", err)
			return m, nil
		}
		m.Config = cfg
		m.Dashboard.Config = cfg
		m.Dashboard.updateTableRows()

		// Select the new milestone on the Dashboard.
		m.Dashboard.SelectMilestone(msg.ID)

		m.ActiveScreen = ScreenDashboard
		m.StatusMsg = fmt.Sprintf("Created milestone %s successfully.", msg.ID)
		m.StatusTime = time.Now()
		return m, nil

	case UpdateMilestoneMsg:
		if msg.Action == "cycle_logged" {
			// Trigger an asynchronous git task so UI remains responsive
			m.StatusMsg = fmt.Sprintf("Querying Git for %s...", msg.MilestoneID)
			m.StatusTime = time.Now()
			return m, m.asyncLogCycle(msg.MilestoneID)
		} else if msg.Action == "status_changed" {
			m.handleStatusChange(msg.MilestoneID, msg.Status)
			m.refreshUI(msg.MilestoneID)
		}
		return m, nil

	case DeleteMilestoneMsg:
		err := config.DeleteMilestone(m.ConfigPath, m.StatePath, msg.MilestoneID)
		if err != nil {
			m.StatusMsg = fmt.Sprintf("Error deleting milestone: %v", err)
			m.StatusTime = time.Now()
			return m, nil
		}
		cfg, err := config.LoadConfig(m.ConfigPath)
		if err == nil {
			m.Config = cfg
			m.Dashboard.Config = cfg
		}
		st, err := config.LoadState(m.StatePath)
		if err == nil {
			m.State = st
			m.Dashboard.State = st
		}
		m.Dashboard.updateTableRows()
		m.ActiveScreen = ScreenDashboard
		m.StatusMsg = fmt.Sprintf("Deleted milestone %s successfully.", msg.MilestoneID)
		m.StatusTime = time.Now()
		return m, nil

	case DeleteCycleMsg:
		err := config.DeleteMilestoneCycle(m.ConfigPath, m.StatePath, msg.MilestoneID, msg.CycleNumber)
		if err != nil {
			m.StatusMsg = fmt.Sprintf("Error deleting cycle: %v", err)
			m.StatusTime = time.Now()
			return m, nil
		}
		st, err := config.LoadState(m.StatePath)
		if err == nil {
			m.State = st
		}
		m.refreshUI(msg.MilestoneID)
		m.StatusMsg = fmt.Sprintf("Deleted Cycle %d of milestone %s successfully.", msg.CycleNumber, msg.MilestoneID)
		m.StatusTime = time.Now()
		return m, nil

	case LogCycleResultMsg:
		m.handleLogCycleResult(msg)
		m.refreshUI(msg.MilestoneID)
		return m, nil

	case StartCycleMsg:
		agents, err := config.LoadDynamicAgents()
		if err != nil {
			m.StatusMsg = fmt.Sprintf("Error loading agents: %v", err)
			m.StatusTime = time.Now()
			return m, nil
		}
		if len(agents) == 0 {
			m.StatusMsg = "Error: no dynamic agents discovered."
			m.StatusTime = time.Now()
			return m, nil
		}

		pipeline, missingAgents := resolveStartCyclePipeline(agents, msg.Group, msg.SingleAgentID)
		if len(missingAgents) > 0 {
			m.StatusMsg = "Error: selected agent group references missing agents: " + strings.Join(missingAgents, ", ")
			m.StatusTime = time.Now()
			return m, nil
		}

		if len(pipeline) == 0 {
			m.StatusMsg = "Error: selected agent group contains no valid agents on disk."
			m.StatusTime = time.Now()
			return m, nil
		}

		if msg.RunnerLLM != "" {
			for i := range pipeline {
				if pipeline[i].RunnerBinary != "manual" {
					pipeline[i].RunnerBinary = msg.RunnerLLM
				}
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		m.Runner.Ctx = ctx
		m.Runner.CancelFunc = cancel
		m.Runner.Milestone = msg.Milestone
		m.Runner.Pipeline = pipeline
		m.Runner.AgentStates = make(map[string]string)
		m.Runner.AgentStartedAt = make(map[string]time.Time)
		m.Runner.AgentElapsed = make(map[string]time.Duration)
		m.Runner.Logs = nil
		m.Runner.StatusEvents = nil
		m.Runner.Finished = false
		m.Runner.Status = "Initializing workflow pipeline..."
		m.Runner.CycleStatus = "preparing"
		m.Runner.CycleNumber = 0
		m.Runner.ActiveAgentID = ""
		m.Runner.ActivePhase = "preparing"
		m.Runner.Runner = ""
		m.Runner.Model = ""
		m.Runner.Mode = ""
		m.Runner.OutputFile = ""
		m.Runner.LatestCommand = ""
		m.Runner.LatestToolCall = ""
		m.Runner.ModelCalls = 0
		m.Runner.ToolCalls = 0
		m.Runner.EstimatedTokens = 0
		m.Runner.PromptTokens = 0
		m.Runner.CompletionTokens = 0
		m.Runner.MaxModelCalls = 0
		m.Runner.MaxTokenBudget = 0
		m.Runner.StopOrDoneReason = ""
		m.Runner.LastError = ""
		m.Runner.NextSuggestedAction = ""
		m.Runner.FinalVerdict = ""
		m.Runner.StartedAt = time.Now()
		m.Runner.FinishedAt = time.Time{}
		m.Runner.Error = nil
		m.Runner.ReportFile = ""
		m.Runner.ActiveTab = RunnerTabLog

		// Flush pipeline events channel
		for len(m.MsgChan) > 0 {
			<-m.MsgChan
		}

		unrestricted := m.Unrestricted
		if msg.RunnerMode != "" {
			unrestricted = msg.RunnerMode == "unrestricted"
		}

		opts := executor.RunOptions{
			ConfigPath:     m.ConfigPath,
			StatePath:      m.StatePath,
			NoBranchChange: msg.NoBranchChange,
			Unrestricted:   unrestricted,
			SingleAgentID:  msg.SingleAgentID,
			CycleNote:      msg.Note,
		}

		// Run executing routine in background
		go executor.ExecuteCycle(ctx, msg.Milestone, pipeline, opts, m.State, m.MsgChan)

		m.ActiveScreen = ScreenRunner
		return m, tea.Batch(
			m.Runner.Init(),
			m.ListenForExecutorMessages(),
		)

	case executor.AgentStartedMsg:
		var rCmd tea.Cmd
		m.Runner, rCmd = m.Runner.Update(msg)
		return m, tea.Batch(rCmd, m.ListenForExecutorMessages())

	case executor.AgentProgressMsg:
		var rCmd tea.Cmd
		m.Runner, rCmd = m.Runner.Update(msg)
		return m, tea.Batch(rCmd, m.ListenForExecutorMessages())

	case executor.AgentCompletedMsg:
		var rCmd tea.Cmd
		m.Runner, rCmd = m.Runner.Update(msg)
		return m, tea.Batch(rCmd, m.ListenForExecutorMessages())

	case executor.RunnerStatusMsg:
		var rCmd tea.Cmd
		m.Runner, rCmd = m.Runner.Update(msg)
		return m, tea.Batch(rCmd, m.ListenForExecutorMessages())

	case executor.CycleFinishedMsg:
		var rCmd tea.Cmd
		m.Runner, rCmd = m.Runner.Update(msg)
		m.refreshUI(msg.MilestoneID)
		return m, rCmd

	case executor.CreateMilestoneProgressMsg:
		m.CreateMilestone.Logs = append(m.CreateMilestone.Logs, msg.LogLine)
		return m, m.ListenForExecutorMessages()

	case executor.CreateMilestoneFinishedMsg:
		m.CreateMilestone.Loading = false
		if msg.Error != nil {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error creating milestone: %v", msg.Error)
			return m, nil
		}

		title := m.CreateMilestone.TitleInput.Value()
		goal := m.CreateMilestone.GoalInput.Value()
		// Auto-generate title if empty
		if title == "" {
			firstLine := strings.Split(goal, "\n")[0]
			firstLine = cleanAutoTitle(firstLine)
			if len(firstLine) > 50 {
				title = firstLine[:50] + "..."
			} else if firstLine != "" {
				title = firstLine
			} else {
				title = "Milestone " + m.CreateMilestone.NextID
			}
		}

		finalID := m.CreateMilestone.NextID
		slug := slugifyTitle(title)
		if slug != "" {
			finalID = fmt.Sprintf("%s-%s", m.CreateMilestone.NextID, slug)
		}

		// Try to scan for optimized slug and title from the generated file
		milestonesDir := filepath.Join(".cyclestone", "milestones")
		files, err := os.ReadDir(milestonesDir)
		if err == nil {
			prefix := m.CreateMilestone.NextID + "-"
			var matchedFile string
			for _, f := range files {
				if !f.IsDir() && strings.HasPrefix(f.Name(), prefix) && strings.HasSuffix(f.Name(), ".md") {
					matchedFile = f.Name()
					break
				}
			}
			if matchedFile != "" {
				slugPart := strings.TrimPrefix(matchedFile, prefix)
				slugPart = strings.TrimSuffix(slugPart, ".md")
				if slugPart != "" {
					filePath := filepath.Join(milestonesDir, matchedFile)
					contentBytes, err := os.ReadFile(filePath)
					if err == nil {
						lines := strings.Split(string(contentBytes), "\n")
						var firstLine string
						for _, line := range lines {
							trimmed := strings.TrimSpace(line)
							if trimmed != "" {
								firstLine = trimmed
								break
							}
						}
						if firstLine != "" {
							expectedID := m.CreateMilestone.NextID + "-" + slugPart
							idx := strings.Index(firstLine, expectedID)
							if idx != -1 {
								titlePart := firstLine[idx+len(expectedID):]
								titlePart = strings.TrimSpace(titlePart)
								titlePart = strings.TrimLeft(titlePart, "-:| ")
								titlePart = strings.TrimSpace(titlePart)
								if titlePart != "" {
									title = titlePart
									finalID = expectedID
								}
							}
						}
					}
				}
			}
		}

		// On success, add to config
		newMilestone := config.Milestone{
			ID:                 finalID,
			Title:              title,
			Goal:               goal,
			AcceptanceCriteria: nil,
			Status:             "Todo",
			Cycles:             0,
			Checks:             nil,
		}

		if err := config.AddMilestone(m.ConfigPath, newMilestone); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error adding milestone: %v", err)
				return m, nil
			}
		}

		// Reload config and update dashboard
		cfg, err := config.LoadConfig(m.ConfigPath)
		if err != nil {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error reloading config: %v", err)
			return m, nil
		}
		m.Config = cfg
		m.Dashboard.Config = cfg
		m.Dashboard.updateTableRows()

		// Select new milestone
		m.Dashboard.SelectMilestone(newMilestone.ID)

		m.ActiveScreen = ScreenDashboard
		m.StatusMsg = fmt.Sprintf("Created milestone %s successfully with agent.", newMilestone.ID)
		m.StatusTime = time.Now()
		return m, nil

	case SettingsSavedMsg:
		// Reload current settings into memory to update program options
		settings := config.LoadMergedSettings()
		autoGit := true
		if settings.AutoGitBranch != nil {
			autoGit = *settings.AutoGitBranch
		}
		// NoBranchChange reflects AutoGitBranch only. CreateMilestoneBranch is a separate
		// setting that controls branch creation at milestone-creation time, not during cycles.
		m.NoBranchChange = !autoGit
		m.Unrestricted = settings.DefaultMode == "unrestricted"

		m.Styles = DefaultStyles(config.DefaultDisableBoldForEnvironment(), config.DefaultDisableRoundedBordersForEnvironment())
		m.Dashboard.Styles = m.Styles
		m.Details.Styles = m.Styles
		m.Runner.Styles = m.Styles
		m.CreateMilestone.Styles = m.Styles
		m.Preflight.Styles = m.Styles
		m.Settings.Styles = m.Styles
		m.AgentGroups.Styles = m.Styles
		m.Setup.Styles = m.Styles

		m.StatusMsg = fmt.Sprintf("Saved %s settings successfully.", msg.Scope)
		m.StatusTime = time.Now()
		m.ActiveScreen = ScreenDashboard
		m.Dashboard.State = m.State
		m.Dashboard.updateTableRows()
		return m, nil

	case SetupCompletedMsg:
		cfg, err := config.LoadConfig(msg.ConfigPath)
		if err != nil {
			m.Setup.ErrorMsg = fmt.Sprintf("Error loading setup config: %v", err)
			return m, nil
		}
		st, err := config.LoadState(msg.StatePath)
		if err != nil {
			m.Setup.ErrorMsg = fmt.Sprintf("Error loading setup state: %v", err)
			return m, nil
		}
		m.ConfigPath = msg.ConfigPath
		m.StatePath = msg.StatePath
		m.Config = cfg
		m.State = st
		// Derive NoBranchChange and Unrestricted from the merged settings so
		// that inherited (empty/nil) project values resolve correctly from
		// the global configuration, matching the SettingsSavedMsg behaviour.
		mergedSettings := config.LoadMergedSettings()
		autoGit := true
		if mergedSettings.AutoGitBranch != nil {
			autoGit = *mergedSettings.AutoGitBranch
		}
		m.NoBranchChange = !autoGit
		m.Unrestricted = mergedSettings.DefaultMode == "unrestricted"
		m.Dashboard.Config = cfg
		m.Dashboard.State = st
		m.Dashboard.updateTableRows()
		if msg.MilestoneID != "" {
			m.Dashboard.SelectMilestone(msg.MilestoneID)
		}
		m.MissingConfig = false
		m.InitConfigError = ""
		m.ActiveScreen = ScreenDashboard
		m.StatusMsg = "Setup complete."
		m.StatusTime = time.Now()
		return m, nil
	}

	// Route msg to the active screen
	switch m.ActiveScreen {
	case ScreenDashboard:
		var dModel DashboardModel
		dModel, cmd = m.Dashboard.Update(msg)
		m.Dashboard = dModel
		cmds = append(cmds, cmd)
	case ScreenDetails:
		var detModel DetailsModel
		detModel, cmd = m.Details.Update(msg)
		m.Details = detModel
		cmds = append(cmds, cmd)
	case ScreenRunner:
		var runModel RunnerModel
		runModel, cmd = m.Runner.Update(msg)
		m.Runner = runModel
		cmds = append(cmds, cmd)
	case ScreenCreateMilestone:
		var createModel CreateMilestoneModel
		createModel, cmd = m.CreateMilestone.Update(msg)
		m.CreateMilestone = createModel
		cmds = append(cmds, cmd)
	case ScreenPreflight:
		var preflightModel PreflightModel
		preflightModel, cmd = m.Preflight.Update(msg)
		m.Preflight = preflightModel
		cmds = append(cmds, cmd)
	case ScreenSettings:
		var settingsModel SettingsModel
		settingsModel, cmd = m.Settings.Update(msg)
		m.Settings = settingsModel
		cmds = append(cmds, cmd)
	case ScreenAgentGroups:
		var groupsModel AgentGroupsModel
		groupsModel, cmd = m.AgentGroups.Update(msg)
		m.AgentGroups = groupsModel
		cmds = append(cmds, cmd)
	case ScreenSetup:
		var setupModel SetupWizardModel
		setupModel, cmd = m.Setup.Update(msg)
		m.Setup = setupModel
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View constructs the overall user interface layout.
func (m RootModel) View() string {
	if m.Width == 0 || m.Height == 0 {
		return "Loading..."
	}
	if m.MissingConfig {
		m.ActiveScreen = ScreenSetup
		return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), m.Setup.View())
	}
	var activeView string
	switch m.ActiveScreen {
	case ScreenDashboard:
		activeView = m.Dashboard.View()
	case ScreenDetails:
		activeView = m.Details.View()
	case ScreenRunner:
		activeView = m.Runner.View()
	case ScreenCreateMilestone:
		activeView = m.CreateMilestone.View()
	case ScreenPreflight:
		activeView = m.Preflight.View()
	case ScreenSettings:
		activeView = m.Settings.View()
	case ScreenAgentGroups:
		activeView = m.AgentGroups.View()
	case ScreenSetup:
		activeView = m.Setup.View()
	default:
		activeView = "Unknown Screen"
	}

	header := m.renderHeader()

	var footer string
	if m.StatusMsg != "" && time.Since(m.StatusTime) < 3*time.Second {
		maxLen := m.Width - 4
		if maxLen < 10 {
			maxLen = 10
		}
		dispMsg := m.StatusMsg
		if len(dispMsg) > maxLen {
			dispMsg = dispMsg[:maxLen-3] + "..."
		}

		var formattedStatus string
		lowerStatus := strings.ToLower(dispMsg)
		if strings.Contains(lowerStatus, "error") || strings.Contains(lowerStatus, "failed") {
			formattedStatus = m.Styles.RenderError(dispMsg)
		} else if strings.Contains(lowerStatus, "success") {
			formattedStatus = m.Styles.RenderSuccess(dispMsg)
		} else {
			formattedStatus = m.Styles.RenderInfo(dispMsg)
		}

		footer = m.Styles.Footer.
			Width(m.Width - 2).
			Render(formattedStatus)
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		activeView,
		footer,
	)
}

func (m RootModel) renderMissingConfigPrompt() string {
	header := m.renderHeader()
	title := m.Styles.SectionTitle.Render("Initialize milestones")
	bodyWidth := m.Width - 4
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	bodyHeight := m.Height - 6
	if bodyHeight < 8 {
		bodyHeight = 8
	}
	bodyLines := []string{
		title,
		"",
		fmt.Sprintf("No milestones configuration found at %q.", m.ConfigPath),
		"Initialize this directory with a default milestones configuration?",
		"",
		renderCommandHelp(m.Styles, []string{"y Yes", "n No"}, bodyWidth),
	}
	if m.InitConfigError != "" {
		bodyLines = append(bodyLines, "", m.Styles.RenderError(m.InitConfigError))
	}
	body := m.Styles.Box.
		Width(bodyWidth).
		Height(bodyHeight).
		Render(strings.Join(bodyLines, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

func (m RootModel) renderHeader() string {
	title := m.Styles.AppTitle.Render("CYCLESTONE")
	subtitle := m.Styles.AppSubtitle.Render("agent command deck")
	screen := m.Styles.CommandKey.Render(screenName(m.ActiveScreen))

	line := lipgloss.JoinHorizontal(lipgloss.Center, title, subtitle)
	if m.Width > 52 {
		spacerWidth := m.Width - lipgloss.Width(line) - lipgloss.Width(screen) - 2
		if spacerWidth < 1 {
			spacerWidth = 1
		}
		line = lipgloss.JoinHorizontal(lipgloss.Center, line, strings.Repeat(" ", spacerWidth), screen)
	}

	return m.Styles.Hero.Width(m.Width - 2).Render(line)
}

func screenName(screen Screen) string {
	switch screen {
	case ScreenDashboard:
		return "DASHBOARD"
	case ScreenDetails:
		return "DETAILS"
	case ScreenRunner:
		return "RUNNER"
	case ScreenCreateMilestone:
		return "CREATE"
	case ScreenPreflight:
		return "PREFLIGHT"
	case ScreenSettings:
		return "SETTINGS"
	case ScreenAgentGroups:
		return "AGENT GROUPS"
	case ScreenSetup:
		return "SETUP"
	default:
		return "UNKNOWN"
	}
}

// refreshUI updates tables and sub-model structures on state change.
func (m *RootModel) refreshUI(milestoneID string) {
	m.Dashboard.State = m.State
	m.Dashboard.updateTableRows()

	if m.ActiveScreen == ScreenDetails && m.Details.Milestone.ID == milestoneID {
		for i, ms := range m.Config.Milestones {
			if ms.ID == milestoneID {
				m.Config.Milestones[i].Status = m.State.GetMilestoneStatus(milestoneID)
				m.Config.Milestones[i].Cycles = m.State.GetMilestoneCycles(milestoneID)
				m.Details.Milestone = m.Config.Milestones[i]
				break
			}
		}
		m.Details.History = m.getHistoryForMilestone(milestoneID)
		m.Details.RecommendationScore = m.State.GetMilestoneRecommendation(milestoneID)
		m.Details.clampHistorySelection()
	}
}

// getHistoryForMilestone gathers logged cycles from State for a specific milestone.
func (m *RootModel) getHistoryForMilestone(id string) []config.MilestoneCycleLog {
	logs := m.State.GetHistory(id)
	// Return in reverse chronological order (newest first)
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}
	return logs
}

func (m *RootModel) initDetailsScreen(ms config.Milestone) {
	m.Details.Milestone = ms
	m.Details.History = m.getHistoryForMilestone(ms.ID)
	m.Details.RecommendationScore = m.State.GetMilestoneRecommendation(ms.ID)
	settings := config.LoadMergedSettings()
	m.Details.LLM = normalizeMilestoneRunner(settings.DefaultLLM)
	if m.Unrestricted {
		m.Details.Mode = "unrestricted"
	} else {
		m.Details.Mode = settings.DefaultMode
	}
	autoGitForDetails := true
	if settings.AutoGitBranch != nil {
		autoGitForDetails = *settings.AutoGitBranch
	}
	if m.NoBranchChange {
		autoGitForDetails = false
	}
	m.Details.BranchChange = autoGitForDetails
	m.Details.Groups = settings.AgentGroups
	m.Details.SelectedGroupIdx = 0
	m.Details.ScrollOffset = 0
	m.Details.HistoryScrollOffset = 0
	m.Details.AgentScrollOffset = 0
	m.Details.ConfirmDeleteMilestone = false
	m.Details.ConfirmDeleteCycle = false
	m.Details.clampHistorySelection()
}

// asyncLogCycle returns a tea.Cmd executing Git checks in the background.
func (m *RootModel) asyncLogCycle(id string) tea.Cmd {
	return func() tea.Msg {
		timestamp := time.Now().Format("2006-01-02 15:04:05")

		branch, err := git.GetCurrentBranch()
		if err != nil {
			branch = "non-git"
		}

		commitHash, err := git.GetLatestCommitHash()
		if err != nil {
			commitHash = "none"
		}

		return LogCycleResultMsg{
			MilestoneID: id,
			Branch:      branch,
			CommitHash:  commitHash,
			Timestamp:   timestamp,
		}
	}
}

// handleStatusChange processes milestone status transitions.
func (m *RootModel) handleStatusChange(id, status string) {
	m.State.SetMilestoneStatus(id, status)

	m.StatusMsg = fmt.Sprintf("Updated %s status to %s", id, status)
	m.StatusTime = time.Now()

	_ = config.SaveState(m.StatePath, m.State)
}

// handleLogCycleResult updates cycles count and logs details from the git process.
func (m *RootModel) handleLogCycleResult(msg LogCycleResultMsg) {
	cycleNum := m.State.IncrementMilestoneCycles(msg.MilestoneID)

	t, err := time.Parse("2006-01-02 15:04:05", msg.Timestamp)
	if err != nil {
		t = time.Now()
	}

	m.State.AddCycleLog(msg.MilestoneID, config.MilestoneCycleLog{
		CycleNumber: cycleNum,
		Timestamp:   t,
		Branch:      msg.Branch,
		CommitHash:  msg.CommitHash,
		Status:      "approved",
		Actions:     []config.AgentActionLog{},
	})

	m.StatusMsg = fmt.Sprintf("Logged manual cycle for %s (branch: %s)", msg.MilestoneID, msg.Branch)
	m.StatusTime = time.Now()

	_ = config.SaveState(m.StatePath, m.State)
}

func generateNextID(cfg *config.Config) string {
	maxVal := 0
	if cfg != nil {
		for _, ms := range cfg.Milestones {
			parts := strings.Split(ms.ID, "-")
			if len(parts) > 0 {
				var val int
				_, err := fmt.Sscanf(parts[0], "%d", &val)
				if err == nil {
					if val > maxVal {
						maxVal = val
					}
				}
			}
		}
	}
	return fmt.Sprintf("%04d", maxVal+1)
}

func resolveStartCyclePipeline(agents []config.Agent, group config.AgentGroup, singleAgentID string) ([]config.Agent, []string) {
	return resolvePreflightPipeline(agents, group, singleAgentID)
}

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		return strings.Join(lines[:maxLines], "\n")
	}
	return s
}

func renderCommandHelp(styles Styles, commands []string, maxWidth int) string {
	if len(commands) == 0 {
		return ""
	}
	sep := "  "
	renderedSep := styles.HelpStyle.Render(sep)
	sepWidth := lipgloss.Width(renderedSep)

	var lines []string
	var currentLine string
	var currentWidth int

	for _, command := range commands {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			continue
		}
		key := styles.CommandKey.Render(fields[0])
		label := ""
		if len(fields) > 1 {
			label = " " + styles.HelpStyle.Render(strings.Join(fields[1:], " "))
		}
		renderedBlock := key + label
		blockWidth := lipgloss.Width(renderedBlock)

		if currentLine == "" {
			currentLine = renderedBlock
			currentWidth = blockWidth
		} else {
			if currentWidth+sepWidth+blockWidth <= maxWidth {
				currentLine += renderedSep + renderedBlock
				currentWidth += sepWidth + blockWidth
			} else {
				lines = append(lines, currentLine)
				currentLine = renderedBlock
				currentWidth = blockWidth
			}
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	return strings.Join(lines, "\n")
}
