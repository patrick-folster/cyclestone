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

var executeCycle = executor.ExecuteCycle
var loadPlanningState = config.LoadPlanningState
var savePlanningPlan = config.SavePlanToFolder
var deletePlanningPlan = config.DeletePlan

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
	ScreenPlans
	ScreenPlanDetails
	ScreenBriefingDetails
	ScreenCreatePlan
	ScreenDeletePlan
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
	ActiveScreen      Screen
	Dashboard         DashboardModel
	Details           DetailsModel
	Runner            RunnerModel
	CreateMilestone   CreateMilestoneModel
	Preflight         PreflightModel
	Settings          SettingsModel
	AgentGroups       AgentGroupsModel
	Setup             SetupWizardModel
	Plans             PlansModel
	PlanDetails       PlanDetailsModel
	BriefingDetails   BriefingDetailsModel
	CreatePlan        CreatePlanModel
	DeletePlan        DeletePlanModel
	Width             int
	Height            int
	Config            *config.Config
	State             *config.State
	ConfigPath        string
	StatePath         string
	NoBranchChange    bool
	Unrestricted      bool
	MsgChan           chan tea.Msg
	Styles            Styles
	StatusMsg         string
	StatusTime        time.Time
	MissingConfig     bool
	InitConfigError   string
	PendingCycle      *StartCycleMsg
	BriefingOrigin    BriefingOrigin
	activePlanOrigin  BriefingOrigin
	PlanCycleStarted  func(BriefingOrigin) error
	PlanCycleFinished func(BriefingOrigin, string, string, error) (PlanContinuation, error)
	// CycleExecutor runs an ordinary Milestone cycle. It defaults to the shared
	// executor and permits deterministic local integration tests without a live runner.
	CycleExecutor func(context.Context, config.Milestone, []config.Agent, executor.RunOptions, *config.State, chan tea.Msg)
}

// PlanContinuation describes the next durable Plan-run transition after a cycle.
type PlanContinuation struct {
	NextMilestone *config.Milestone
	NextOrigin    BriefingOrigin
	Message       string
}

// NewRootModel constructs the RootModel and initializes its sub-models.
func NewRootModel(cfg *config.Config, state *config.State, configPath, statePath string, noBranchChange, unrestricted, disableBold, disableRoundedBorders bool) RootModel {
	styles := DefaultStyles(disableBold, disableRoundedBorders)

	dashboard := NewDashboardModel(cfg, state, styles)
	if configPath != "" {
		plansDir := filepath.Join(filepath.Dir(configPath), "plans")
		var msIDs []string
		if cfg != nil {
			for _, ms := range cfg.Milestones {
				msIDs = append(msIDs, ms.ID)
			}
		}
		planning, _ := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs(msIDs))
		dashboard.Planning = planning
	}
	details := NewDetailsModel(styles)
	runner := NewRunnerModel(styles)
	createMilestone := NewCreateMilestoneModel(styles)
	createMilestone.NextID = generateNextID(cfg)
	preflight := NewPreflightModel(styles)
	settings := NewSettingsModel(styles)
	agentGroups := NewAgentGroupsModel(styles)
	setup := NewSetupWizardModel(configPath, statePath, styles)

	plansDir := ""
	if configPath != "" {
		plansDir = filepath.Join(filepath.Dir(configPath), "plans")
	}
	var msIDs []string
	if cfg != nil {
		for _, ms := range cfg.Milestones {
			msIDs = append(msIDs, ms.ID)
		}
	}
	var planning *config.PlanningState
	if plansDir != "" {
		planning, _ = config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs(msIDs))
	}

	plansModel := NewPlansModel(cfg, state, planning, styles)
	planDetailsModel := NewPlanDetailsModel(cfg, state, styles)
	briefingDetailsModel := NewBriefingDetailsModel(styles)
	createPlanModel := NewCreatePlanModel(styles)

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
		Plans:           plansModel,
		PlanDetails:     planDetailsModel,
		BriefingDetails: briefingDetailsModel,
		CreatePlan:      createPlanModel,
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

// QueueBriefingCycle opens the existing preflight for one resolved ordinary
// Milestone when the TUI starts from `briefing execute`.
func (m *RootModel) QueueBriefingCycle(milestone config.Milestone, origin BriefingOrigin) {
	runnerMode := "sandbox"
	if m.Unrestricted {
		runnerMode = "unrestricted"
	}
	req := StartCycleMsg{
		Milestone:      milestone,
		RunnerMode:     runnerMode,
		NoBranchChange: m.NoBranchChange,
		BriefingOrigin: origin,
	}
	m.PendingCycle = &req
}

// QueuePlanCycle queues an ordinary Milestone while retaining immutable Plan context.
func (m *RootModel) QueuePlanCycle(milestone config.Milestone, origin BriefingOrigin) {
	origin.PlanRun = true
	origin.MilestoneID = milestone.ID
	m.QueueBriefingCycle(milestone, origin)
}

// Init initializes the Bubble Tea program.
func (m RootModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		initialWindowSizeCmd(),
		m.Dashboard.Init(),
		m.Details.Init(),
		m.Runner.Init(),
		m.CreateMilestone.Init(),
		m.Preflight.Init(),
		m.Settings.Init(),
		m.AgentGroups.Init(),
		m.Setup.Init(),
		m.Plans.Init(),
		m.PlanDetails.Init(),
		m.BriefingDetails.Init(),
		m.CreatePlan.Init(),
	}
	if m.PendingCycle != nil {
		req := *m.PendingCycle
		cmds = append(cmds, func() tea.Msg {
			return ChangeScreenMsg{Screen: ScreenPreflight, Data: req}
		})
	}
	return tea.Batch(cmds...)
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
			if m.ActiveScreen == ScreenCreatePlan || m.ActiveScreen == ScreenDeletePlan {
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

		m.Plans, cmd = m.Plans.Update(msg)
		cmds = append(cmds, cmd)
		m.PlanDetails, cmd = m.PlanDetails.Update(msg)
		cmds = append(cmds, cmd)
		m.BriefingDetails, cmd = m.BriefingDetails.Update(msg)
		cmds = append(cmds, cmd)
		m.CreatePlan, cmd = m.CreatePlan.Update(msg)
		cmds = append(cmds, cmd)
		m.DeletePlan, cmd = m.DeletePlan.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case ShowDeleteMilestoneMsg:
		m.ActiveScreen = ScreenDetails
		m.initDetailsScreen(msg.Milestone)
		m.Details.ConfirmDeleteMilestone = true
		return m, nil

	case ShowDeletePlanMsg:
		m.DeletePlan = NewDeletePlanModel(msg.Plan, msg.ReturnScreen, m.Styles)
		m.DeletePlan.Width, m.DeletePlan.Height = m.Width, m.Height
		m.ActiveScreen = ScreenDeletePlan
		return m, m.DeletePlan.Init()

	case ChangeScreenMsg:
		if (m.ActiveScreen == ScreenPreflight || m.ActiveScreen == ScreenRunner) && msg.Screen != ScreenRunner && msg.Screen != ScreenPreflight {
			m.BriefingOrigin = BriefingOrigin{}
		}
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
				m.CreateMilestone.RunWorkflow = cycleMsg.Workflow
				if cycleMsg.Workflow == WorkflowAgentInstructionsRepository || cycleMsg.Workflow == WorkflowAgentInstructionsMilestone {
					m.CreateMilestone.GoalInput.Placeholder = "Enter optional AGENTS.md update guidance here..."
				} else {
					m.CreateMilestone.GoalInput.Placeholder = "Enter optional cycle note / comment here..."
				}
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
		} else if msg.Screen == ScreenPlans {
			m.Plans.Config = m.Config
			m.Plans.State = m.State
			if m.ConfigPath != "" {
				plansDir := filepath.Join(filepath.Dir(m.ConfigPath), "plans")
				var msIDs []string
				if m.Config != nil {
					for _, ms := range m.Config.Milestones {
						msIDs = append(msIDs, ms.ID)
					}
				}
				planning, _ := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs(msIDs))
				m.Plans.Planning = planning
			}
			m.Plans.Width = m.Width
			m.Plans.Height = m.Height
			m.Plans.UpdateTableRows()
		} else if msg.Screen == ScreenPlanDetails {
			if plan, ok := msg.Data.(config.Plan); ok {
				m.PlanDetails.Plan = plan
				m.PlanDetails.Config = m.Config
				m.PlanDetails.State = m.State
				m.PlanDetails.Width = m.Width
				m.PlanDetails.Height = m.Height
				m.PlanDetails.UpdateTableRows()
			}
		} else if msg.Screen == ScreenBriefingDetails {
			if data, ok := msg.Data.(BriefingDetailData); ok {
				m.BriefingDetails.Plan = data.Plan
				m.BriefingDetails.Briefing = data.Briefing
				m.BriefingDetails.Width = m.Width
				m.BriefingDetails.Height = m.Height
				m.BriefingDetails.ScrollOffset = 0
				m.BriefingDetails.LinkedMS = nil
				m.BriefingDetails.History = nil
				if data.Briefing.MilestoneID != "" && m.Config != nil {
					for _, ms := range m.Config.Milestones {
						if ms.ID == data.Briefing.MilestoneID {
							msCopy := ms
							if st, ok := m.State.MilestoneStatuses[ms.ID]; ok {
								msCopy.Status = st
							}
							if cyc, ok := m.State.MilestoneCycles[ms.ID]; ok {
								msCopy.Cycles = cyc
							}
							m.BriefingDetails.LinkedMS = &msCopy
							break
						}
					}
					if m.State != nil {
						if h, ok := m.State.History[data.Briefing.MilestoneID]; ok {
							m.BriefingDetails.History = h
						}
					}
				}
			}
		} else if msg.Screen == ScreenCreatePlan {
			m.CreatePlan = NewCreatePlanModel(m.Styles)
			m.CreatePlan.NextID = generateNextPlanID(m.Plans.Planning)
			m.CreatePlan.Width, m.CreatePlan.Height = m.Width, m.Height
			settings := config.LoadMergedSettings()
			defaultRunner := normalizeMilestoneRunner(settings.DefaultLLM)
			m.CreatePlan.DefaultLLM = settings.DefaultLLM
			m.CreatePlan.RunnerType = defaultRunner
			createBranch := false
			if settings.CreateMilestoneBranch != nil {
				createBranch = *settings.CreateMilestoneBranch
			}
			m.CreatePlan.CreateBranch = createBranch
			m.CreatePlan.recalcHeights()
			return m, m.CreatePlan.Init()
		}
		return m, nil

	case CreatePlanMsg:
		plansDir, ok := m.plansDirectory()
		if !ok {
			m.CreatePlan.ErrorMsg = "Cannot create a Plan without a project configuration path."
			return m, nil
		}

		if msg.CreateBranch {
			settings := config.LoadMergedSettings()
			prefix := settings.DefaultGitBranchPrefix
			if prefix == "" {
				prefix = "cyclestone/plans/"
			}
			branchName := prefix + msg.ID
			repos := git.GetTrackedRepos()
			for _, repo := range repos {
				_ = git.CheckoutOrCreateBranchInDir(repo.Path, branchName)
			}
		}

		if msg.RunnerType != "" && msg.RunnerType != "template" && msg.RunnerType != "manual" {

			if err := os.MkdirAll(plansDir, 0755); err != nil {
				m.CreatePlan.ErrorMsg = fmt.Sprintf("Error creating plans directory: %v", err)
				return m, nil
			}

			m.CreatePlan.Loading = true
			m.CreatePlan.Logs = nil

			absRoot, err := filepath.Abs(".")
			if err != nil {
				absRoot = "."
			}

			prompt := resources.PlanCreatorPrompt
			prompt = strings.ReplaceAll(prompt, "{{PLAN_ID}}", msg.ID)
			prompt = strings.ReplaceAll(prompt, "{{TITLE}}", msg.Title)
			prompt = strings.ReplaceAll(prompt, "{{GOAL}}", msg.Objective)

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

			for len(m.MsgChan) > 0 {
				<-m.MsgChan
			}

			go func() {
				defer cancel()
				executor.ExecutePlanCreation(ctx, msg.RunnerType, prompt, opts, m.MsgChan, msg.ID, msg.Title)
			}()

			return m, tea.Batch(
				m.CreatePlan.Spinner.Tick,
				m.ListenForExecutorMessages(),
			)
		}

		now := time.Now().UTC().Format(time.RFC3339)
		plan := config.Plan{
			SchemaVersion: config.PlanningSchemaVersion,
			ID:            msg.ID, Title: msg.Title, Objective: msg.Objective,
			Status: "active", CreatedAt: now, CreatedBy: "tui", UpdatedAt: now, UpdatedBy: "tui",
			BriefingOrder: []string{}, Briefings: []config.Briefing{},
		}
		formValidation := config.ValidatePlan(plan, "")
		if formValidation.HasErrors() {
			m.CreatePlan.ErrorMsg = firstPlanningError(formValidation)
			return m, nil
		}
		_, existingValidation := loadPlanningState(plansDir, config.WithKnownMilestoneIDs(m.planningMilestoneIDs()))
		if existingValidation.HasErrors() {
			m.CreatePlan.ErrorMsg = "Fix existing Plan files before creating another Plan: " + firstPlanningError(existingValidation)
			return m, nil
		}
		// Check for duplicate Plan ID in both flat .yml and folder-per-item layouts.
		planPath := filepath.Join(plansDir, msg.ID+".yml")
		if _, err := os.Stat(planPath); err == nil {
			m.CreatePlan.ErrorMsg = fmt.Sprintf("Plan ID %q already exists.", msg.ID)
			return m, nil
		} else if !os.IsNotExist(err) {
			m.CreatePlan.ErrorMsg = fmt.Sprintf("Cannot inspect Plan target: %v", err)
			return m, nil
		}
		if entries, err := os.ReadDir(plansDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), msg.ID) {
					m.CreatePlan.ErrorMsg = fmt.Sprintf("Plan ID %q already exists.", msg.ID)
					return m, nil
				}
			}
		}
		_, validation, err := savePlanningPlan(plansDir, plan, config.WithKnownMilestoneIDs(m.planningMilestoneIDs()))
		if err != nil {
			if validation.HasErrors() {
				m.CreatePlan.ErrorMsg = firstPlanningError(validation)
			} else {
				m.CreatePlan.ErrorMsg = fmt.Sprintf("Failed to save Plan: %v", err)
			}
			return m, nil
		}

		planning, reloadValidation := loadPlanningState(plansDir, config.WithKnownMilestoneIDs(m.planningMilestoneIDs()))
		if reloadValidation.HasErrors() {
			m.CreatePlan.ErrorMsg = "Plan was saved, but reloading planning data failed: " + firstPlanningError(reloadValidation)
			return m, nil
		}
		m.syncPlanning(planning)
		m.Plans.SelectPlan(plan.ID)
		m.ActiveScreen = ScreenPlans
		m.StatusMsg = fmt.Sprintf("Created Plan %s successfully.", plan.ID)
		m.StatusTime = time.Now()
		return m, nil


	case DeletePlanMsg:
		plansDir, ok := m.plansDirectory()
		if !ok {
			m.DeletePlan.ErrorMsg = "Cannot delete a Plan without a project configuration path."
			return m, nil
		}
		planningBefore, existingValidation := loadPlanningState(plansDir, config.WithKnownMilestoneIDs(m.planningMilestoneIDs()))
		if existingValidation.HasErrors() {
			m.DeletePlan.ErrorMsg = "Fix existing Plan files before deleting a Plan: " + firstPlanningError(existingValidation)
			return m, nil
		}
		found := false
		for _, plan := range planningBefore.Plans {
			if plan.ID == msg.Plan.ID {
				found = true
				break
			}
		}
		if !found {
			m.DeletePlan.ErrorMsg = fmt.Sprintf("Plan %q no longer exists.", msg.Plan.ID)
			return m, nil
		}
		if err := deletePlanningPlan(plansDir, msg.Plan.ID); err != nil {
			m.DeletePlan.ErrorMsg = fmt.Sprintf("Failed to delete Plan: %v", err)
			return m, nil
		}
		planning, reloadValidation := loadPlanningState(plansDir, config.WithKnownMilestoneIDs(m.planningMilestoneIDs()))
		if reloadValidation.HasErrors() {
			m.DeletePlan.ErrorMsg = "Plan was deleted, but reloading planning data failed: " + firstPlanningError(reloadValidation)
			return m, nil
		}
		m.syncPlanning(planning)
		m.clearDeletedPlanReferences(msg.Plan.ID)
		m.ActiveScreen = ScreenPlans
		m.StatusMsg = fmt.Sprintf("Deleted Plan %s successfully.", msg.Plan.ID)
		m.StatusTime = time.Now()
		return m, nil

	case CreateMilestoneMsg:
		now := time.Now().UTC().Format(time.RFC3339)
		newMilestone := config.Milestone{
			ID:                 msg.ID,
			Title:              msg.Title,
			Goal:               msg.Goal,
			AcceptanceCriteria: msg.AcceptanceCriteria,
			Status:             "Todo",
			Cycles:             0,
			Checks:             msg.Checks,
			CreatedBy:          "tui",
			UpdatedBy:          "tui",
			CreatedAt:          now,
			UpdatedAt:          now,
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

			idPrefix := m.CreateMilestone.NextID
			if idPrefix == "" {
				idPrefix = msg.ID
			}

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

		// 3. Persist milestone via folder-per-item layout.
		milestonesDir := filepath.Join(filepath.Dir(m.ConfigPath), "milestones")
		if _, err := config.SaveMilestoneToFolder(milestonesDir, newMilestone, replaced); err != nil {
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
		if msg.Workflow == WorkflowAgentInstructionsRepository || msg.Workflow == WorkflowAgentInstructionsMilestone {
			runner := msg.RunnerLLM
			if runner == "" {
				runner = config.LoadMergedSettings().DefaultLLM
			}
			runner = normalizeMilestoneRunner(runner)
			ctx, cancel := context.WithCancel(context.Background())
			pipeline := []config.Agent{{ID: "agent-instructions-updater", Name: "Agent Instructions Updater", RunnerBinary: runner}}
			m.Runner = NewRunnerModel(m.Styles)
			m.Runner.Ctx = ctx
			m.Runner.CancelFunc = cancel
			m.Runner.Milestone = msg.Milestone
			m.Runner.Pipeline = pipeline
			m.Runner.Workflow = msg.Workflow
			m.Runner.Status = "Initializing AGENTS.md update workflow..."
			m.Runner.CycleStatus = "preparing"
			m.Runner.ActivePhase = "preparing"
			m.Runner.StartedAt = time.Now()
			m.Runner.Width = m.Width
			m.Runner.Height = m.Height
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
				CycleNote:      msg.Note,
			}
			go executor.ExecuteAgentInstructionsUpdate(ctx, msg.Milestone, msg.Workflow == WorkflowAgentInstructionsMilestone, runner, opts, m.MsgChan)
			m.ActiveScreen = ScreenRunner
			return m, tea.Batch(m.Runner.Init(), m.ListenForExecutorMessages())
		}
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

		m.BriefingOrigin = msg.BriefingOrigin
		if msg.BriefingOrigin.PlanRun {
			m.activePlanOrigin = msg.BriefingOrigin
		} else {
			m.activePlanOrigin = BriefingOrigin{}
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
		m.Runner.Workflow = WorkflowCycle
		m.Runner.PlanContext = ""
		if origin := msg.BriefingOrigin; origin.PlanRun {
			m.Runner.PlanContext = fmt.Sprintf("Plan %s | Briefing %s | Queue %d/%d | Mode %s", origin.PlanID, origin.BriefingID, origin.QueuePosition, origin.QueueTotal, origin.Mode)
		}

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
		if msg.BriefingOrigin.PlanRun && m.PlanCycleStarted != nil {
			if err := m.PlanCycleStarted(msg.BriefingOrigin); err != nil {
				m.StatusMsg = "Plan execution stopped before cycle launch: " + err.Error()
				m.StatusTime = time.Now()
				return m, nil
			}
		}

		// Run executing routine in background
		cycleExecutor := m.CycleExecutor
		if cycleExecutor == nil {
			cycleExecutor = executeCycle
		}
		go cycleExecutor(ctx, msg.Milestone, pipeline, opts, m.State, m.MsgChan)

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
		continuationCmd := m.finishBriefingExecution(msg)
		return m, tea.Batch(rCmd, continuationCmd)

	case executor.CreatePlanProgressMsg:
		m.CreatePlan.Logs = append(m.CreatePlan.Logs, msg.LogLine)
		return m, m.ListenForExecutorMessages()

	case executor.CreatePlanFinishedMsg:
		m.CreatePlan.Loading = false
		if msg.Error != nil {
			m.CreatePlan.ErrorMsg = fmt.Sprintf("Error creating plan: %v", msg.Error)
			return m, nil
		}
		plansDir, ok := m.plansDirectory()
		if !ok {
			m.CreatePlan.ErrorMsg = "Cannot locate plans directory after creation."
			return m, nil
		}

		targetPlanID := m.CreatePlan.effectivePlanID()
		finalPlanID := targetPlanID
		planning, _ := loadPlanningState(plansDir, config.WithKnownMilestoneIDs(m.planningMilestoneIDs()))
		var loadedPlan *config.Plan
		for i := range planning.Plans {
			if targetPlanID != "" && planning.Plans[i].ID == targetPlanID {
				loadedPlan = &planning.Plans[i]
				break
			}
		}

		if (loadedPlan == nil || len(loadedPlan.Briefings) == 0) && strings.TrimSpace(msg.Output) != "" {
			generated, err := config.ParseGeneratedPlanResponse(msg.Output)
			if err == nil {
				now := time.Now().UTC().Format(time.RFC3339)
				goal := m.CreatePlan.ObjectiveInput.Value()
				plan, err2 := config.ConvertGeneratedPlan(goal, generated, "ai-plan-generator", now)
				if err2 == nil {
					if strings.TrimSpace(m.CreatePlan.IDInput.Value()) == "" {
						slug := config.PlanningSlug(plan.Title)
						if slug != "" {
							plan.ID = targetPlanID + "-" + slug
						} else {
							plan.ID = targetPlanID
						}
					} else {
						plan.ID = targetPlanID
					}
					finalPlanID = plan.ID
					// Rewrite generated briefing IDs from title slugs to the
					// b-<author>-NNNN form, mirroring the CLI plan generate path.
					authorPref := config.GetDefaultAuthorPrefix(config.LoadMergedSettings())
					// Briefing IDs are plan-scoped, starting at 0001 for each plan.
					existingBriefingIDs := make([]string, 0)
					oldToNew := map[string]string{}
					for i := range plan.Briefings {
						bSlug := config.PlanningSlug(plan.Briefings[i].Title)
						newBID := config.AllocateBriefingID(plan.ID, authorPref, existingBriefingIDs)
						if bSlug != "" {
							newBID = newBID + "-" + bSlug
						}
						oldToNew[plan.Briefings[i].ID] = newBID
						existingBriefingIDs = append(existingBriefingIDs, newBID)
						plan.Briefings[i].ID = newBID
					}
					// Update BriefingOrder and DependsOn to reference the new IDs.
					plan.BriefingOrder = make([]string, 0, len(plan.Briefings))
					for i := range plan.Briefings {
						plan.BriefingOrder = append(plan.BriefingOrder, plan.Briefings[i].ID)
						updatedDeps := make([]string, 0, len(plan.Briefings[i].DependsOn))
						for _, dep := range plan.Briefings[i].DependsOn {
							if newID, ok := oldToNew[dep]; ok {
								updatedDeps = append(updatedDeps, newID)
							} else {
								updatedDeps = append(updatedDeps, dep)
							}
						}
						plan.Briefings[i].DependsOn = updatedDeps
					}
					savePlanningPlan(plansDir, plan, config.WithKnownMilestoneIDs(m.planningMilestoneIDs())) // returns (_, _, _) – best-effort
					planning, _ = loadPlanningState(plansDir, config.WithKnownMilestoneIDs(m.planningMilestoneIDs()))
				}
			}
		}


		m.syncPlanning(planning)
		if len(planning.Plans) > 0 {
			if finalPlanID != "" {
				m.Plans.SelectPlan(finalPlanID)
			} else {
				m.Plans.SelectPlan(planning.Plans[len(planning.Plans)-1].ID)
			}
		}
		m.ActiveScreen = ScreenPlans
		m.StatusMsg = "Plan generated successfully."
		m.StatusTime = time.Now()
		return m, nil


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

		var matchedName string
		var matchedIsDir bool
		var agentSpecContent string
		milestonesDir := filepath.Join(".cyclestone", "milestones")
		files, err := os.ReadDir(milestonesDir)
		if err == nil {
			prefix := m.CreateMilestone.NextID + "-"
			exactID := m.CreateMilestone.NextID
			for _, f := range files {
				name := f.Name()
				if f.IsDir() {
					if name == exactID || strings.HasPrefix(name, prefix) {
						matchedName = name
						matchedIsDir = true
						break
					}
				} else {
					if strings.HasSuffix(name, ".md") && (strings.HasPrefix(name, prefix) || name == exactID+".md") {
						matchedName = name
						matchedIsDir = false
						break
					}
				}
			}

			if matchedName != "" {
				if matchedIsDir {
					dirPath := filepath.Join(milestonesDir, matchedName)
					finalID = matchedName
					subFiles, _ := os.ReadDir(dirPath)
					for _, sf := range subFiles {
						if !sf.IsDir() && strings.HasSuffix(sf.Name(), ".md") && sf.Name() != m.CreateMilestone.NextID+"-orig.md" && sf.Name() != m.CreateMilestone.NextID+"-original.md" {
							data, err := os.ReadFile(filepath.Join(dirPath, sf.Name()))
							if err == nil {
								agentSpecContent = string(data)
								break
							}
						}
					}
					if loadedMS, err := config.LoadMilestoneFromDir(dirPath); err == nil && loadedMS != nil {
						if loadedMS.Title != "" {
							title = loadedMS.Title
						}
						if loadedMS.Goal != "" {
							goal = loadedMS.Goal
						}
					} else {
						if agentSpecContent != "" {
							lines := strings.Split(agentSpecContent, "\n")
							for _, line := range lines {
								trimmed := strings.TrimSpace(line)
								if strings.HasPrefix(trimmed, "# ") {
									tLine := strings.TrimPrefix(trimmed, "# ")
									if idx := strings.Index(tLine, " - "); idx != -1 {
										title = strings.TrimSpace(tLine[idx+3:])
									} else if idx := strings.LastIndex(tLine, ": "); idx != -1 {
										title = strings.TrimSpace(tLine[idx+2:])
									} else {
										title = strings.TrimSpace(tLine)
									}
									break
								}
							}
						}
					}
				} else {
					slugPart := strings.TrimPrefix(matchedName, prefix)
					slugPart = strings.TrimSuffix(slugPart, ".md")
					filePath := filepath.Join(milestonesDir, matchedName)
					contentBytes, err := os.ReadFile(filePath)
					if err == nil {
						agentSpecContent = string(contentBytes)
						lines := strings.Split(agentSpecContent, "\n")
						var firstLine string
						for _, line := range lines {
							trimmed := strings.TrimSpace(line)
							if trimmed != "" {
								firstLine = trimmed
								break
							}
						}
						if firstLine != "" {
							expectedID := m.CreateMilestone.NextID
							if slugPart != "" && slugPart != matchedName {
								expectedID = m.CreateMilestone.NextID + "-" + slugPart
							}
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
					// Remove flat file since it belongs inside the subdirectory
					_ = os.Remove(filePath)
				}
			}
		}

		if matchedName == "" {
			m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error creating milestone: generator runner %s did not create milestone specification file in %s", m.CreateMilestone.RunnerType, milestonesDir)
			return m, nil
		}

		// On success, add to config
		now := time.Now().UTC().Format(time.RFC3339)
		newMilestone := config.Milestone{
			ID:                 finalID,
			Title:              title,
			Goal:               goal,
			AcceptanceCriteria: nil,
			Status:             "Todo",
			Cycles:             0,
			Checks:             nil,
			CreatedBy:          "tui",
			UpdatedBy:          "tui",
			CreatedAt:          now,
			UpdatedAt:          now,
		}

		milestonesDir2 := filepath.Join(filepath.Dir(m.ConfigPath), "milestones")
		if _, err := config.SaveMilestoneToFolder(milestonesDir2, newMilestone, agentSpecContent); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				m.CreateMilestone.ErrorMsg = fmt.Sprintf("Error adding milestone: %v", err)
				return m, nil
			}
		}

		// Write the orig.md file carrying the initial prompt/spec.
		origPath := filepath.Join(milestonesDir2, finalID, m.CreateMilestone.NextID+"-original.md")
		origMilestone := config.Milestone{
			ID:                 finalID,
			Title:              m.CreateMilestone.TitleInput.Value(),
			Goal:               m.CreateMilestone.GoalInput.Value(),
			AcceptanceCriteria: nil,
		}
		if origMilestone.Title == "" {
			origMilestone.Title = title
		}
		origContent := config.FormatMilestoneSpec(origMilestone)
		_ = os.WriteFile(origPath, []byte(origContent), 0644)

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
		m.CreatePlan.Styles = m.Styles
		m.DeletePlan.Styles = m.Styles

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
	case ScreenPlans:
		var plansModel PlansModel
		plansModel, cmd = m.Plans.Update(msg)
		m.Plans = plansModel
		cmds = append(cmds, cmd)
	case ScreenPlanDetails:
		var planDetModel PlanDetailsModel
		planDetModel, cmd = m.PlanDetails.Update(msg)
		m.PlanDetails = planDetModel
		cmds = append(cmds, cmd)
	case ScreenBriefingDetails:
		var briefingDetModel BriefingDetailsModel
		briefingDetModel, cmd = m.BriefingDetails.Update(msg)
		m.BriefingDetails = briefingDetModel
		cmds = append(cmds, cmd)
	case ScreenCreatePlan:
		var createPlanModel CreatePlanModel
		createPlanModel, cmd = m.CreatePlan.Update(msg)
		m.CreatePlan = createPlanModel
		cmds = append(cmds, cmd)
	case ScreenDeletePlan:
		var deletePlanModel DeletePlanModel
		deletePlanModel, cmd = m.DeletePlan.Update(msg)
		m.DeletePlan = deletePlanModel
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
	case ScreenPlans:
		activeView = m.Plans.View()
	case ScreenPlanDetails:
		activeView = m.PlanDetails.View()
	case ScreenBriefingDetails:
		activeView = m.BriefingDetails.View()
	case ScreenCreatePlan:
		activeView = m.CreatePlan.View()
	case ScreenDeletePlan:
		activeView = m.DeletePlan.View()
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
	case ScreenPlans:
		return "PLANS"
	case ScreenPlanDetails:
		return "PLAN DETAILS"
	case ScreenBriefingDetails:
		return "BRIEFING DETAILS"
	case ScreenCreatePlan:
		return "CREATE PLAN"
	case ScreenDeletePlan:
		return "DELETE PLAN"
	default:
		return "UNKNOWN"
	}
}

// refreshUI updates tables and sub-model structures on state change.
func (m *RootModel) refreshUI(milestoneID string) {
	m.Dashboard.State = m.State
	if m.ConfigPath != "" {
		plansDir := filepath.Join(filepath.Dir(m.ConfigPath), "plans")
		var msIDs []string
		if m.Config != nil {
			for _, ms := range m.Config.Milestones {
				msIDs = append(msIDs, ms.ID)
			}
		}
		planning, _ := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs(msIDs))
		m.Dashboard.Planning = planning
	}
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
		m.Details.AgentInstructionsUpdateScore = m.State.GetMilestoneAgentInstructionsUpdateScore(milestoneID)
		m.Details.clampHistorySelection()
	}
}

func (m *RootModel) plansDirectory() (string, bool) {
	if m.ConfigPath == "" {
		return "", false
	}
	return filepath.Join(filepath.Dir(m.ConfigPath), "plans"), true
}

func (m *RootModel) planningMilestoneIDs() []string {
	if m.Config == nil {
		return nil
	}
	ids := make([]string, 0, len(m.Config.Milestones))
	for _, milestone := range m.Config.Milestones {
		ids = append(ids, milestone.ID)
	}
	return ids
}

// syncPlanning replaces every list-level planning copy only after persistence
// and reload have both succeeded.
func (m *RootModel) syncPlanning(planning *config.PlanningState) {
	m.Dashboard.Planning = planning
	m.Plans.Planning = planning
	m.Plans.Config = m.Config
	m.Plans.State = m.State
	m.Plans.UpdateTableRows()
	m.Dashboard.updateTableRows()
}

func (m *RootModel) clearDeletedPlanReferences(planID string) {
	if m.PlanDetails.Plan.ID == planID {
		m.PlanDetails.Plan = config.Plan{}
		m.PlanDetails.Table.SetRows(nil)
	}
	if m.BriefingDetails.Plan.ID == planID {
		m.BriefingDetails = NewBriefingDetailsModel(m.Styles)
	}
	callbackRunning := m.Runner.Ctx != nil && !m.Runner.Finished
	if !callbackRunning {
		if m.BriefingOrigin.PlanID == planID {
			m.BriefingOrigin = BriefingOrigin{}
		}
		if m.activePlanOrigin.PlanID == planID {
			m.activePlanOrigin = BriefingOrigin{}
		}
	}
}

func firstPlanningError(validation config.PlanningValidationResult) string {
	for _, message := range validation.Messages {
		if message.Severity != "error" {
			continue
		}
		if message.Field != "" {
			return fmt.Sprintf("%s: %s", message.Field, message.Message)
		}
		return message.Message
	}
	return "planning validation failed"
}

// finishBriefingExecution maps a terminal Milestone result back to the one
// originating Briefing. Milestone artifacts are already final at this point,
// so planning failures are surfaced without changing the cycle result.
func (m *RootModel) finishBriefingExecution(msg executor.CycleFinishedMsg) tea.Cmd {
	origin := m.BriefingOrigin
	if !origin.PlanRun && m.activePlanOrigin.PlanRun {
		origin = m.activePlanOrigin
	}
	m.BriefingOrigin = BriefingOrigin{}
	m.activePlanOrigin = BriefingOrigin{}
	if origin.PlanRun && m.PlanCycleFinished != nil {
		continuation, err := m.PlanCycleFinished(origin, msg.MilestoneID, msg.Status, msg.Error)
		if err != nil {
			m.setPlanningCompletionWarning(err)
			return nil
		}
		if continuation.Message != "" {
			m.StatusMsg = continuation.Message
			m.StatusTime = time.Now()
		}
		if continuation.NextMilestone != nil {
			if cfg, loadErr := config.LoadConfig(m.ConfigPath); loadErr == nil {
				m.Config = cfg
				m.Dashboard.Config = cfg
			}
			if state, loadErr := config.LoadState(m.StatePath); loadErr == nil {
				m.State = state
				m.Dashboard.State = state
			}
			m.Dashboard.updateTableRows()
			m.QueuePlanCycle(*continuation.NextMilestone, continuation.NextOrigin)
			req := *m.PendingCycle
			m.PendingCycle = nil
			return func() tea.Msg { return ChangeScreenMsg{Screen: ScreenPreflight, Data: req} }
		}
		return nil
	}
	if origin.PlanID == "" || origin.BriefingID == "" || msg.Error != nil || msg.Status != "approved" {
		return nil
	}

	cfg, err := config.LoadConfig(m.ConfigPath)
	if err != nil {
		m.setPlanningCompletionWarning(fmt.Errorf("reload milestone config: %w", err))
		return nil
	}
	milestoneIDs := make([]string, 0, len(cfg.Milestones))
	for _, milestone := range cfg.Milestones {
		milestoneIDs = append(milestoneIDs, milestone.ID)
	}
	plansDir := filepath.Join(filepath.Dir(m.ConfigPath), "plans")
	planning, validation := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs(milestoneIDs))
	if validation.HasErrors() {
		m.setPlanningCompletionWarning(fmt.Errorf("planning files contain validation errors"))
		return nil
	}

	for planIndex := range planning.Plans {
		plan := &planning.Plans[planIndex]
		if plan.ID != origin.PlanID {
			continue
		}
		for briefingIndex := range plan.Briefings {
			briefing := &plan.Briefings[briefingIndex]
			if briefing.ID != origin.BriefingID {
				continue
			}
			if briefing.MilestoneID != msg.MilestoneID {
				m.setPlanningCompletionWarning(fmt.Errorf("Briefing %q now links Milestone %q instead of completed Milestone %q", briefing.ID, briefing.MilestoneID, msg.MilestoneID))
				return nil
			}
			now := planningCompletionTimestamp(plan.CreatedAt, briefing.CreatedAt)
			briefing.Status = "completed"
			briefing.UpdatedAt = now
			briefing.UpdatedBy = "briefing-executor"
			plan.UpdatedAt = now
			plan.UpdatedBy = "briefing-executor"
			if _, validation, err := savePlanningPlan(plansDir, *plan, config.WithKnownMilestoneIDs(milestoneIDs)); err != nil || validation.HasErrors() {
				if err == nil {
					err = fmt.Errorf("updated Plan did not pass validation")
				}
				m.setPlanningCompletionWarning(err)
			}
			return nil
		}
		m.setPlanningCompletionWarning(fmt.Errorf("Briefing %q no longer exists in Plan %q", origin.BriefingID, origin.PlanID))
		return nil
	}
	m.setPlanningCompletionWarning(fmt.Errorf("Plan %q no longer exists", origin.PlanID))
	return nil
}

func planningCompletionTimestamp(createdAt ...string) string {
	stamp := time.Now().UTC()
	for _, value := range createdAt {
		created, err := time.Parse(time.RFC3339, value)
		if err == nil && stamp.Before(created) {
			stamp = created
		}
	}
	return stamp.Format(time.RFC3339)
}

func (m *RootModel) setPlanningCompletionWarning(err error) {
	warning := "Planning update warning: Milestone cycle remains valid, but Briefing completion was not saved: " + err.Error()
	m.StatusMsg = warning
	m.StatusTime = time.Now()
	if m.Runner.LastError == "" {
		m.Runner.LastError = warning
	} else {
		m.Runner.LastError += "; " + warning
	}
	m.Runner.Status += " " + warning
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
	m.Details.AgentInstructionsUpdateScore = m.State.GetMilestoneAgentInstructionsUpdateScore(ms.ID)
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
	// Always resolve the author prefix from merged (global+project) settings so
	// the configured author_prefix (e.g. "pf") reaches AllocateMilestoneID even
	// when only the global settings.yml carries it.
	authorPrefix := config.GetDefaultAuthorPrefix(config.LoadMergedSettings())
	var existingIDs []string
	if cfg != nil {
		for _, ms := range cfg.Milestones {
			existingIDs = append(existingIDs, ms.ID)
		}
	}
	return config.AllocateMilestoneID("", authorPrefix, existingIDs)
}

func generateNextPlanID(planning *config.PlanningState) string {
	// Always resolve the author prefix from merged (global+project) settings so
	// the configured author_prefix (e.g. "pf") reaches AllocatePlanID even when
	// only the global settings.yml carries it.
	authorPrefix := config.GetDefaultAuthorPrefix(config.LoadMergedSettings())
	var existingIDs []string
	if planning != nil {
		for _, p := range planning.Plans {
			existingIDs = append(existingIDs, p.ID)
		}
	}
	return config.AllocatePlanID(authorPrefix, existingIDs)
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
