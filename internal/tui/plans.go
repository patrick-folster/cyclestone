package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patrick-folster/cyclestone/internal/config"
)

// BriefingDetailData holds context for viewing Briefing details.
type BriefingDetailData struct {
	Plan     config.Plan
	Briefing config.Briefing
}

// PlansModel manages the flat table listing of all Plans in the TUI.
type PlansModel struct {
	Table    table.Model
	Config   *config.Config
	State    *config.State
	Planning *config.PlanningState
	Width    int
	Height   int
	Styles   Styles
}

// NewPlansModel creates and initializes a PlansModel.
func NewPlansModel(cfg *config.Config, state *config.State, planning *config.PlanningState, styles Styles) PlansModel {
	columns := []table.Column{
		{Title: "ID", Width: 12},
		{Title: "Title", Width: 30},
		{Title: "Status", Width: 12},
		{Title: "Briefings", Width: 12},
		{Title: "Execution", Width: 12},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	ts := table.DefaultStyles()
	ts.Header = styles.TableHeader
	ts.Selected = styles.TableSelectedRow
	t.SetStyles(ts)

	m := PlansModel{
		Table:    t,
		Config:   cfg,
		State:    state,
		Planning: planning,
		Styles:   styles,
	}
	m.UpdateTableRows()
	return m
}

// UpdateTableRows populates the table content from m.Planning.
func (m *PlansModel) UpdateTableRows() {
	var rows []table.Row
	if m.Planning != nil {
		for _, plan := range m.Planning.Plans {
			completedCount := 0
			for _, b := range plan.Briefings {
				if b.Status == "completed" || b.Status == "done" {
					completedCount++
				}
			}
			briefingsProgress := fmt.Sprintf("%d/%d", completedCount, len(plan.Briefings))

			executionState := "-"
			if m.State != nil {
				if exec := m.State.GetPlanExecution(plan.ID); exec != nil && exec.State != "" {
					executionState = exec.State
				}
			}

			title := plan.Title
			if title == "" {
				title = plan.ID
			}
			if m.Width > 0 && m.Width < 50 && len(title) > 18 {
				title = title[:15] + "..."
			}

			status := plan.Status
			if status == "" {
				status = "todo"
			}

			if m.Width > 0 && m.Width < 50 {
				rows = append(rows, table.Row{
					plan.ID,
					title,
					briefingsProgress,
				})
			} else {
				rows = append(rows, table.Row{
					plan.ID,
					title,
					status,
					briefingsProgress,
					executionState,
				})
			}
		}
	}
	m.Table.SetRows(rows)
	if len(rows) > 0 && m.Table.Cursor() < 0 {
		m.Table.SetCursor(0)
	}
}

// SelectPlan moves the table cursor to planID when that Plan is visible.
func (m *PlansModel) SelectPlan(planID string) {
	for index, row := range m.Table.Rows() {
		if len(row) > 0 && row[0] == planID {
			m.Table.SetCursor(index)
			return
		}
	}
	if len(m.Table.Rows()) > 0 {
		m.Table.SetCursor(0)
	}
}

func (m PlansModel) Init() tea.Cmd {
	return nil
}

func (m PlansModel) Update(msg tea.Msg) (PlansModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height

		helpCmds := []string{
			"↑/↓ Navigate",
			"Enter Details",
			"c Create",
			"d Delete",
			"Esc Dashboard",
			"q Quit",
			"Ctrl+C Quit",
		}
		helpWidth := m.Width - 4
		if helpWidth < 10 {
			helpWidth = 10
		}
		helpText := renderCommandHelp(m.Styles, helpCmds, helpWidth)
		helpLines := strings.Count(helpText, "\n") + 1

		rootOverhead := 3
		availableHeight := m.Height - rootOverhead
		tableHeight := availableHeight - (3 + helpLines)
		if tableHeight < 2 {
			tableHeight = 2
		}
		m.Table.SetHeight(tableHeight)

		var columns []table.Column
		if m.Width < 50 {
			idWidth := 10
			briefingWidth := 8
			titleWidth := m.Width - (idWidth + briefingWidth + 5)
			if titleWidth < 10 {
				titleWidth = 10
			}
			columns = []table.Column{
				{Title: "ID", Width: idWidth},
				{Title: "Title", Width: titleWidth},
				{Title: "Briefings", Width: briefingWidth},
			}
		} else {
			idWidth := 12
			statusWidth := 12
			briefingWidth := 10
			execWidth := 12
			titleWidth := m.Width - (idWidth + statusWidth + briefingWidth + execWidth + 7)
			if titleWidth < 10 {
				titleWidth = 10
			}
			columns = []table.Column{
				{Title: "ID", Width: idWidth},
				{Title: "Title", Width: titleWidth},
				{Title: "Status", Width: statusWidth},
				{Title: "Briefings", Width: briefingWidth},
				{Title: "Execution", Width: execWidth},
			}
		}
		m.Table.SetRows(nil)
		m.Table.SetColumns(columns)
		m.UpdateTableRows()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "c":
			return m, changeScreenCmd(ScreenCreatePlan, nil)
		case "d":
			selectedRow := m.Table.SelectedRow()
			if len(selectedRow) > 0 && m.Planning != nil {
				for _, plan := range m.Planning.Plans {
					if plan.ID == selectedRow[0] {
						return m, func() tea.Msg { return ShowDeletePlanMsg{Plan: plan, ReturnScreen: ScreenPlans} }
					}
				}
			}
		case "enter":
			selectedRow := m.Table.SelectedRow()
			if len(selectedRow) > 0 && m.Planning != nil {
				planID := selectedRow[0]
				for _, p := range m.Planning.Plans {
					if p.ID == planID {
						selectedPlan := p
						return m, func() tea.Msg {
							return ChangeScreenMsg{
								Screen: ScreenPlanDetails,
								Data:   selectedPlan,
							}
						}
					}
				}
			}
		case "esc", "backspace", "p":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenDashboard,
				}
			}
		}
	}

	m.Table, cmd = m.Table.Update(msg)
	return m, cmd
}

func (m PlansModel) View() string {
	helpCmds := []string{
		"↑/↓ Navigate",
		"Enter Details",
		"c Create",
		"d Delete",
		"Esc Dashboard",
		"q Quit",
		"Ctrl+C Quit",
	}
	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	helpText := renderCommandHelp(m.Styles, helpCmds, helpWidth)

	var tableStr string
	if m.Height < 20 {
		tableStr = m.Table.View()
	} else {
		tableStr = m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Render(m.Table.View())
	}

	elements := []string{
		m.Styles.SectionTitle.Render("Milestone Plans"),
		tableStr,
		helpText,
	}

	return lipgloss.JoinVertical(lipgloss.Left, elements...)
}

// PlanDetailsModel manages the detailed view of a Plan and its Briefings table.
type PlanDetailsModel struct {
	Plan   config.Plan
	Table  table.Model
	Config *config.Config
	State  *config.State
	Width  int
	Height int
	Styles Styles
}

// NewPlanDetailsModel creates and initializes a PlanDetailsModel.
func NewPlanDetailsModel(cfg *config.Config, state *config.State, styles Styles) PlanDetailsModel {
	columns := []table.Column{
		{Title: "ID", Width: 10},
		{Title: "Title", Width: 25},
		{Title: "Status", Width: 12},
		{Title: "Dependencies", Width: 14},
		{Title: "Linked MS", Width: 18},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(8),
	)

	ts := table.DefaultStyles()
	ts.Header = styles.TableHeader
	ts.Selected = styles.TableSelectedRow
	t.SetStyles(ts)

	return PlanDetailsModel{
		Table:  t,
		Config: cfg,
		State:  state,
		Styles: styles,
	}
}

// UpdateTableRows populates the Briefings table for the selected Plan.
func (m *PlanDetailsModel) UpdateTableRows() {
	var rows []table.Row
	msIDs := make(map[string]bool)
	if m.Config != nil {
		for _, ms := range m.Config.Milestones {
			msIDs[ms.ID] = true
		}
	}

	for _, briefing := range m.Plan.Briefings {
		deps := "-"
		if len(briefing.DependsOn) > 0 {
			deps = strings.Join(briefing.DependsOn, ", ")
		}

		linkStr := "[unlinked]"
		if briefing.MilestoneID != "" {
			if msIDs[briefing.MilestoneID] {
				linkStr = fmt.Sprintf("[linked: %s]", briefing.MilestoneID)
			} else {
				linkStr = fmt.Sprintf("[missing: %s]", briefing.MilestoneID)
			}
		}

		title := briefing.Title
		if title == "" {
			title = briefing.ID
		}
		if m.Width > 0 && m.Width < 50 && len(title) > 15 {
			title = title[:12] + "..."
		}

		status := briefing.Status
		if status == "" {
			status = "todo"
		}

		if m.Width > 0 && m.Width < 50 {
			rows = append(rows, table.Row{
				briefing.ID,
				title,
				status,
				linkStr,
			})
		} else {
			rows = append(rows, table.Row{
				briefing.ID,
				title,
				status,
				deps,
				linkStr,
			})
		}
	}
	m.Table.SetRows(rows)
	if len(rows) > 0 && m.Table.Cursor() < 0 {
		m.Table.SetCursor(0)
	}
}

func (m PlanDetailsModel) Init() tea.Cmd {
	return nil
}

func (m PlanDetailsModel) Update(msg tea.Msg) (PlanDetailsModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height

		helpCmds := []string{
			"↑/↓ Navigate",
			"Enter Briefing Details",
			"d Delete Plan",
			"Esc Plans List",
			"q Quit",
			"Ctrl+C Quit",
		}
		helpWidth := m.Width - 4
		if helpWidth < 10 {
			helpWidth = 10
		}
		helpText := renderCommandHelp(m.Styles, helpCmds, helpWidth)
		helpLines := strings.Count(helpText, "\n") + 1

		headerLines := 5
		if m.Plan.Objective != "" {
			headerLines += 2
		}
		if m.Height < 20 {
			headerLines = 3
		}

		availableHeight := m.Height - 3
		tableHeight := availableHeight - (headerLines + helpLines)
		if tableHeight < 2 {
			tableHeight = 2
		}
		m.Table.SetHeight(tableHeight)

		var columns []table.Column
		if m.Width < 50 {
			idWidth := 8
			statusWidth := 10
			linkWidth := 14
			titleWidth := m.Width - (idWidth + statusWidth + linkWidth + 5)
			if titleWidth < 8 {
				titleWidth = 8
			}
			columns = []table.Column{
				{Title: "ID", Width: idWidth},
				{Title: "Title", Width: titleWidth},
				{Title: "Status", Width: statusWidth},
				{Title: "Link", Width: linkWidth},
			}
		} else {
			idWidth := 8
			statusWidth := 12
			depsWidth := 14
			linkWidth := 28
			titleWidth := m.Width - (idWidth + statusWidth + depsWidth + linkWidth + 7)
			if titleWidth < 10 {
				titleWidth = 10
			}
			columns = []table.Column{
				{Title: "ID", Width: idWidth},
				{Title: "Title", Width: titleWidth},
				{Title: "Status", Width: statusWidth},
				{Title: "Dependencies", Width: depsWidth},
				{Title: "Linked MS", Width: linkWidth},
			}
		}
		m.Table.SetRows(nil)
		m.Table.SetColumns(columns)
		m.UpdateTableRows()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "d":
			return m, func() tea.Msg { return ShowDeletePlanMsg{Plan: m.Plan, ReturnScreen: ScreenPlanDetails} }
		case "enter":
			selectedRow := m.Table.SelectedRow()
			if len(selectedRow) > 0 {
				briefingID := selectedRow[0]
				for _, b := range m.Plan.Briefings {
					if b.ID == briefingID {
						selectedBriefing := b
						return m, func() tea.Msg {
							return ChangeScreenMsg{
								Screen: ScreenBriefingDetails,
								Data: BriefingDetailData{
									Plan:     m.Plan,
									Briefing: selectedBriefing,
								},
							}
						}
					}
				}
			}
		case "esc", "backspace":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenPlans,
				}
			}
		}
	}

	m.Table, cmd = m.Table.Update(msg)
	return m, cmd
}

func (m PlanDetailsModel) View() string {
	helpCmds := []string{
		"↑/↓ Navigate",
		"Enter Briefing Details",
		"d Delete Plan",
		"Esc Plans List",
		"q Quit",
		"Ctrl+C Quit",
	}
	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	helpText := renderCommandHelp(m.Styles, helpCmds, helpWidth)

	var sb strings.Builder
	titleText := fmt.Sprintf("Plan: %s - %s", m.Plan.ID, m.Plan.Title)
	sb.WriteString(m.Styles.DetailHeader.Render(titleText) + "\n")

	completed := 0
	for _, b := range m.Plan.Briefings {
		if b.Status == "completed" || b.Status == "done" {
			completed++
		}
	}
	progressStr := fmt.Sprintf("Briefings: %d/%d completed", completed, len(m.Plan.Briefings))

	execStr := "Execution: none"
	if m.State != nil {
		if exec := m.State.GetPlanExecution(m.Plan.ID); exec != nil && exec.State != "" {
			execStr = fmt.Sprintf("Execution: %s", exec.State)
		}
	}

	statusBadge := renderStatusTag(m.Styles, m.Plan.Status)
	metaLine := fmt.Sprintf("%s  %s  %s", statusBadge, m.Styles.SubtleText.Render(progressStr), m.Styles.WarningText.Render(execStr))
	sb.WriteString(metaLine + "\n")

	if m.Plan.Objective != "" && m.Height >= 20 {
		sb.WriteString(m.Styles.DetailLabel.Render("Objective:") + " " + m.Styles.DetailValue.Render(m.Plan.Objective) + "\n")
	}

	var tableStr string
	if m.Height < 20 {
		tableStr = m.Table.View()
	} else {
		tableStr = m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Render(m.Table.View())
	}

	elements := []string{
		sb.String(),
		m.Styles.SectionTitle.Render("Plan Briefings"),
		tableStr,
		helpText,
	}

	return lipgloss.JoinVertical(lipgloss.Left, elements...)
}

// BriefingDetailsModel handles rendering details for a specific Briefing.
type BriefingDetailsModel struct {
	Plan         config.Plan
	Briefing     config.Briefing
	LinkedMS     *config.Milestone
	History      []config.MilestoneCycleLog
	Width        int
	Height       int
	ScrollOffset int
	Styles       Styles
}

// NewBriefingDetailsModel creates a BriefingDetailsModel instance.
func NewBriefingDetailsModel(styles Styles) BriefingDetailsModel {
	return BriefingDetailsModel{
		Styles: styles,
	}
}

func (m BriefingDetailsModel) Init() tea.Cmd {
	return nil
}

func (m BriefingDetailsModel) Update(msg tea.Msg) (BriefingDetailsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		m.clampScrollOffset()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.ScrollOffset--
			if m.ScrollOffset < 0 {
				m.ScrollOffset = 0
			}
			return m, nil
		case "down", "j":
			m.ScrollOffset++
			m.clampScrollOffset()
			return m, nil
		case "pgup", "[":
			m.ScrollOffset -= 5
			if m.ScrollOffset < 0 {
				m.ScrollOffset = 0
			}
			return m, nil
		case "pgdn", "]":
			m.ScrollOffset += 5
			m.clampScrollOffset()
			return m, nil
		case "esc", "backspace":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenPlanDetails,
					Data:   m.Plan,
				}
			}
		}
	}
	return m, nil
}

func (m *BriefingDetailsModel) clampScrollOffset() {
	content := m.renderContent()
	lines := len(strings.Split(content, "\n"))
	visibleHeight := m.Height - 5
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	maxScroll := lines - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.ScrollOffset > maxScroll {
		m.ScrollOffset = maxScroll
	}
	if m.ScrollOffset < 0 {
		m.ScrollOffset = 0
	}
}

func (m BriefingDetailsModel) renderContent() string {
	var sb strings.Builder
	width := m.Width - 4
	if width < 20 {
		width = 20
	}

	headerText := fmt.Sprintf("Briefing: %s - %s", m.Briefing.ID, m.Briefing.Title)
	sb.WriteString(m.Styles.DetailHeader.Render(wrapText(headerText, width)) + "\n")
	sb.WriteString(m.Styles.SubtleText.Render(fmt.Sprintf("Plan: %s (%s)", m.Plan.ID, m.Plan.Title)) + "\n\n")

	statusBadge := renderStatusTag(m.Styles, m.Briefing.Status)
	var linkStr string
	if m.Briefing.MilestoneID == "" {
		linkStr = m.Styles.SubtleText.Render("[unlinked]")
	} else if m.LinkedMS != nil {
		linkStr = m.Styles.AccentText.Render(fmt.Sprintf("[linked: %s]", m.Briefing.MilestoneID))
	} else {
		linkStr = m.Styles.ErrorText.Render(fmt.Sprintf("[missing: %s]", m.Briefing.MilestoneID))
	}

	depsStr := "None"
	if len(m.Briefing.DependsOn) > 0 {
		depsStr = strings.Join(m.Briefing.DependsOn, ", ")
	}

	sb.WriteString(fmt.Sprintf("%s: %s   %s: %s   %s: %s\n\n",
		m.Styles.DetailLabel.Render("Status"), statusBadge,
		m.Styles.DetailLabel.Render("Link"), linkStr,
		m.Styles.DetailLabel.Render("Dependencies"), m.Styles.DetailValue.Render(depsStr),
	))

	if m.Briefing.Objective != "" {
		sb.WriteString(m.Styles.DetailLabel.Render("Objective:") + "\n")
		sb.WriteString(m.Styles.DetailValue.Render(wrapText(m.Briefing.Objective, width)) + "\n\n")
	}

	if m.Briefing.Intent != "" {
		sb.WriteString(m.Styles.DetailLabel.Render("Intent:") + "\n")
		sb.WriteString(m.Styles.DetailValue.Render(wrapText(m.Briefing.Intent, width)) + "\n\n")
	}

	if m.Briefing.CompletionSignal != "" {
		sb.WriteString(m.Styles.DetailLabel.Render("Completion Signal:") + "\n")
		sb.WriteString(m.Styles.DetailValue.Render(wrapText(m.Briefing.CompletionSignal, width)) + "\n\n")
	}

	if len(m.Briefing.Constraints) > 0 {
		sb.WriteString(m.Styles.DetailLabel.Render("Constraints:") + "\n")
		for _, c := range m.Briefing.Constraints {
			wrappedC := wrapTextWithIndent(c, width, "  - ")
			sb.WriteString(m.Styles.DetailValue.Render(wrappedC) + "\n")
		}
		sb.WriteString("\n")
	}

	if m.LinkedMS != nil {
		sb.WriteString(m.Styles.SectionTitle.Render("Linked Milestone Details") + "\n")
		sb.WriteString(fmt.Sprintf("%s: %s - %s\n", m.Styles.DetailLabel.Render("Milestone"), m.LinkedMS.ID, m.LinkedMS.Title))
		sb.WriteString(fmt.Sprintf("%s: %s   %s: %d\n",
			m.Styles.DetailLabel.Render("Status"), renderStatusTag(m.Styles, m.LinkedMS.Status),
			m.Styles.DetailLabel.Render("Cycles"), m.LinkedMS.Cycles,
		))
		if m.LinkedMS.Goal != "" {
			sb.WriteString(m.Styles.DetailLabel.Render("Goal:") + " " + m.Styles.DetailValue.Render(m.LinkedMS.Goal) + "\n")
		}
		if len(m.History) > 0 {
			sb.WriteString("\n" + m.Styles.DetailLabel.Render("Recent Cycles:") + "\n")
			for _, cycle := range m.History {
				durStr := ""
				if cycle.Duration != "" {
					durStr = fmt.Sprintf(" (%s)", cycle.Duration)
				}
				noteStr := ""
				if cycle.UserNote != "" {
					noteStr = fmt.Sprintf(" - %s", cycle.UserNote)
				}
				sb.WriteString(fmt.Sprintf("  - Cycle %d: [%s]%s%s\n", cycle.CycleNumber, cycle.Status, durStr, noteStr))
			}
		}
	}

	return sb.String()
}

func (m BriefingDetailsModel) View() string {
	helpCmds := []string{
		"↑/↓ Scroll",
		"Esc Plan Details",
		"q Quit",
		"Ctrl+C Quit",
	}
	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	helpText := renderCommandHelp(m.Styles, helpCmds, helpWidth)

	content := m.renderContent()
	lines := strings.Split(content, "\n")

	visibleHeight := m.Height - 5
	if visibleHeight < 3 {
		visibleHeight = 3
	}

	start := m.ScrollOffset
	end := start + visibleHeight
	if start >= len(lines) {
		start = len(lines) - 1
		if start < 0 {
			start = 0
		}
	}
	if end > len(lines) {
		end = len(lines)
	}

	visibleContent := strings.Join(lines[start:end], "\n")

	return lipgloss.JoinVertical(lipgloss.Left, visibleContent, helpText)
}
