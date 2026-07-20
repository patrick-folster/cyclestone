package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patrick-folster/cyclestone/internal/config"
)

// DashboardModel manages the main dashboard table displaying all milestones.
type DashboardModel struct {
	Table           table.Model
	Config          *config.Config
	State           *config.State
	Planning        *config.PlanningState
	ShowPlannerTree bool
	Width           int
	Height          int
	Styles          Styles
}

// NewDashboardModel creates and returns a new DashboardModel with initial columns.
func NewDashboardModel(cfg *config.Config, state *config.State, styles Styles) DashboardModel {
	columns := []table.Column{
		{Title: "ID", Width: 8},
		{Title: "Title", Width: 32},
		{Title: "Status", Width: 15},
		{Title: "Cycles", Width: 8},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	// Set customized headers and selection colors
	ts := table.DefaultStyles()
	ts.Header = styles.TableHeader
	ts.Selected = styles.TableSelectedRow
	t.SetStyles(ts)

	m := DashboardModel{
		Table:  t,
		Config: cfg,
		State:  state,
		Styles: styles,
	}
	m.updateTableRows()
	return m
}

// updateTableRows populates/refreshes the table content from config and dynamic state.
func (m *DashboardModel) updateTableRows() {
	var rows []table.Row
	for i := len(m.Config.Milestones) - 1; i >= 0; i-- {
		ms := m.Config.Milestones[i]
		status := ms.Status
		if st, ok := m.State.MilestoneStatuses[ms.ID]; ok {
			status = st
		}
		if status == "" {
			status = "Todo"
		}

		cycles := ms.Cycles
		if cyc, ok := m.State.MilestoneCycles[ms.ID]; ok {
			cycles = cyc
		}

		title := ms.Title
		// Abbreviate long milestone titles when width is extremely narrow (< 50 columns)
		if m.Width > 0 && m.Width < 50 && len(title) > 20 {
			title = title[:17] + "..."
		}

		if m.Width > 0 && m.Width < 50 {
			rows = append(rows, table.Row{
				ms.ID,
				title,
				status,
			})
		} else {
			rows = append(rows, table.Row{
				ms.ID,
				title,
				status,
				strconv.Itoa(cycles),
			})
		}
	}
	m.Table.SetRows(rows)
}

// Init initializes the sub-model.
func (m DashboardModel) Init() tea.Cmd {
	return nil
}

// Update handles resizing, navigation and milestone status updates/logging.
func (m DashboardModel) Update(msg tea.Msg) (DashboardModel, tea.Cmd) {
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
			"p Plans",
			"c Create",
			"n Log Cycle",
			"u Update AGENTS",
			"s Status",
			"d Delete",
			"o Options",
			"g Groups",
			"q Quit",
			"Ctrl+C Quit",
		}
		helpWidth := m.Width - 4
		if helpWidth < 10 {
			helpWidth = 10
		}
		helpText := renderCommandHelp(m.Styles, helpCmds, helpWidth)
		helpLines := strings.Count(helpText, "\n") + 1

		// Scale the height of the table dynamically
		var rootOverhead = 3

		availableHeight := m.Height - rootOverhead

		var tableHeight int
		if m.Height < 20 {
			// tableHeight + 2 (table header + separator) + helpLines = availableHeight
			tableHeight = availableHeight - (2 + helpLines)
		} else {
			// Calculate summary card height based on width
			var summaryHeight int
			if m.Width < 40 {
				summaryHeight = 16
			} else if m.Width < 60 {
				summaryHeight = 8
			} else {
				summaryHeight = 4
			}
			// tableHeight + 4 (table overhead: active border top & bottom + header + separator) + summaryHeight + 1 (section title) + helpLines = availableHeight
			tableHeight = availableHeight - summaryHeight - (5 + helpLines)
		}
		if tableHeight < 2 {
			tableHeight = 2
		}
		m.Table.SetHeight(tableHeight)

		// Adjust column widths dynamically to take up available space, hiding Cycles if < 50 columns
		var columns []table.Column
		if m.Width < 50 {
			var idWidth = 6
			var statusWidth = 11
			titleWidth := m.Width - (idWidth + statusWidth + 5)
			if titleWidth < 10 {
				titleWidth = 10
			}
			columns = []table.Column{
				{Title: "ID", Width: idWidth},
				{Title: "Title", Width: titleWidth},
				{Title: "Status", Width: statusWidth},
			}
		} else {
			var idWidth = 8
			var statusWidth = 15
			var cyclesWidth = 8
			if m.Width < 60 {
				idWidth = 6
				statusWidth = 11
				cyclesWidth = 6
			}
			titleWidth := m.Width - (idWidth + statusWidth + cyclesWidth + 6)
			if titleWidth < 10 {
				titleWidth = 10
			}
			columns = []table.Column{
				{Title: "ID", Width: idWidth},
				{Title: "Title", Width: titleWidth},
				{Title: "Status", Width: statusWidth},
				{Title: "Cycles", Width: cyclesWidth},
			}
		}
		m.Table.SetColumns(columns)
		m.updateTableRows()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "p":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenPlans,
				}
			}
		case "enter":
			// Fetch the selected milestone and switch to details screen
			selectedRow := m.Table.SelectedRow()
			if len(selectedRow) > 0 {
				milestoneID := selectedRow[0]
				var selectedMilestone config.Milestone
				found := false
				for _, ms := range m.Config.Milestones {
					if ms.ID == milestoneID {
						selectedMilestone = ms
						// Integrate latest states
						if st, ok := m.State.MilestoneStatuses[ms.ID]; ok {
							selectedMilestone.Status = st
						} else {
							selectedMilestone.Status = "Todo"
						}
						if cyc, ok := m.State.MilestoneCycles[ms.ID]; ok {
							selectedMilestone.Cycles = cyc
						}
						found = true
						break
					}
				}
				if found {
					return m, func() tea.Msg {
						return ChangeScreenMsg{
							Screen: ScreenDetails,
							Data:   selectedMilestone,
						}
					}
				}
			}

		case "c", "ctrl+n":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenCreateMilestone,
				}
			}

		case "n":
			// Log a work cycle for the selected milestone
			selectedRow := m.Table.SelectedRow()
			if len(selectedRow) > 0 {
				milestoneID := selectedRow[0]
				return m, func() tea.Msg {
					return UpdateMilestoneMsg{
						MilestoneID: milestoneID,
						Action:      "cycle_logged",
					}
				}
			}
		case "u":
			settings := config.LoadMergedSettings()
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenCreateMilestone,
					Data: StartCycleMsg{
						Milestone: config.Milestone{
							ID:    "AGENTS.md",
							Title: "Repository AGENTS.md update",
							Goal:  "Generate a reviewable repository-wide root AGENTS.md proposal.",
						},
						RunnerLLM:      normalizeMilestoneRunner(settings.DefaultLLM),
						RunnerMode:     settings.DefaultMode,
						NoBranchChange: true,
						Workflow:       WorkflowAgentInstructionsRepository,
					},
				}
			}

		case "s":
			// Cycle milestone status: Todo -> In Progress -> Done
			selectedRow := m.Table.SelectedRow()
			if len(selectedRow) > 0 {
				milestoneID := selectedRow[0]
				var currentStatus string
				if st, ok := m.State.MilestoneStatuses[milestoneID]; ok {
					currentStatus = st
				} else {
					currentStatus = "Todo"
				}

				var nextStatus string
				switch currentStatus {
				case "Todo":
					nextStatus = "In Progress"
				case "In Progress":
					nextStatus = "Done"
				case "Done":
					nextStatus = "Todo"
				default:
					nextStatus = "Todo"
				}

				return m, func() tea.Msg {
					return UpdateMilestoneMsg{
						MilestoneID: milestoneID,
						Action:      "status_changed",
						Status:      nextStatus,
					}
				}
			}
		case "o":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenSettings,
				}
			}
		case "g":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenAgentGroups,
				}
			}
		case "d":
			selectedRow := m.Table.SelectedRow()
			if len(selectedRow) > 0 {
				milestoneID := selectedRow[0]
				var selectedMilestone config.Milestone
				found := false
				for _, ms := range m.Config.Milestones {
					if ms.ID == milestoneID {
						selectedMilestone = ms
						if st, ok := m.State.MilestoneStatuses[ms.ID]; ok {
							selectedMilestone.Status = st
						} else {
							selectedMilestone.Status = "Todo"
						}
						if cyc, ok := m.State.MilestoneCycles[ms.ID]; ok {
							selectedMilestone.Cycles = cyc
						}
						found = true
						break
					}
				}
				if found {
					return m, func() tea.Msg {
						return ShowDeleteMilestoneMsg{
							Milestone: selectedMilestone,
						}
					}
				}
			}
		}
	}

	m.Table, cmd = m.Table.Update(msg)
	return m, cmd
}

// SelectMilestone selects the row in the table matching the given milestone ID.
func (m *DashboardModel) SelectMilestone(id string) {
	for i, row := range m.Table.Rows() {
		if len(row) > 0 && row[0] == id {
			m.Table.SetCursor(i)
			break
		}
	}
}

// View outputs the rendered table string with a help/commands guide.
func (m DashboardModel) View() string {
	if m.ShowPlannerTree {
		return m.viewPlannerTree()
	}

	helpCmds := []string{
		"↑/↓ Navigate",
		"Enter Details",
		"p Plans",
		"c Create",
		"n Log Cycle",
		"u Update AGENTS",
		"s Status",
		"d Delete",
		"o Options",
		"g Groups",
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

	summary := m.summaryCards()
	var elements []string
	if m.Height >= 20 {
		elements = append(elements, summary, m.Styles.SectionTitle.Render("Active Milestones"))
	}
	elements = append(elements, tableStr, helpText)

	return lipgloss.JoinVertical(lipgloss.Left, elements...)
}

func (m DashboardModel) viewPlannerTree() string {
	helpCmds := []string{
		"p Back to Milestones",
		"q Quit",
		"Ctrl+C Quit",
	}
	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	helpText := renderCommandHelp(m.Styles, helpCmds, helpWidth)

	var plans []config.Plan
	if m.Planning != nil {
		plans = m.Planning.Plans
	}

	var milestones []config.Milestone
	if m.Config != nil {
		milestones = m.Config.Milestones
	}

	opts := TreeOptions{
		MaxWidth: m.Width - 4,
		Styled:   true,
		Styles:   m.Styles,
	}

	treeStr := RenderTree(plans, milestones, m.State, opts)
	if m.Height >= 20 {
		treeStr = m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Render(treeStr)
	}

	return lipgloss.JoinVertical(lipgloss.Left, m.Styles.SectionTitle.Render("Milestone Planner Hierarchy"), treeStr, helpText)
}

func (m DashboardModel) summaryCards() string {
	total := len(m.Config.Milestones)
	todo, inProgress, done := 0, 0, 0
	for _, ms := range m.Config.Milestones {
		status := ms.Status
		if st, ok := m.State.MilestoneStatuses[ms.ID]; ok {
			status = st
		}
		switch status {
		case "In Progress":
			inProgress++
		case "Done":
			done++
		default:
			todo++
		}
	}

	if m.Width < 40 {
		cardWidth := m.Width - 4
		if cardWidth < 12 {
			cardWidth = 12
		}
		cards := []string{
			m.renderSummaryCard("Total", total, m.Styles.AccentText, cardWidth),
			m.renderSummaryCard("Todo", todo, m.Styles.SubtleText, cardWidth),
			m.renderSummaryCard("Active", inProgress, m.Styles.WarningText, cardWidth),
			m.renderSummaryCard("Done", done, m.Styles.SuccessText, cardWidth),
		}
		return lipgloss.JoinVertical(lipgloss.Left, cards...)
	}

	if m.Width < 60 {
		cardWidth := (m.Width - 6) / 2
		if cardWidth < 12 {
			cardWidth = 12
		}
		if cardWidth > 26 {
			cardWidth = 26
		}

		cards := []string{
			m.renderSummaryCard("Total", total, m.Styles.AccentText, cardWidth),
			m.renderSummaryCard("Todo", todo, m.Styles.SubtleText, cardWidth),
			m.renderSummaryCard("Active", inProgress, m.Styles.WarningText, cardWidth),
			m.renderSummaryCard("Done", done, m.Styles.SuccessText, cardWidth),
		}

		row1 := lipgloss.JoinHorizontal(lipgloss.Top, cards[0], cards[1])
		row2 := lipgloss.JoinHorizontal(lipgloss.Top, cards[2], cards[3])
		return lipgloss.JoinVertical(lipgloss.Left, row1, row2)
	}

	cardWidth := (m.Width - 10) / 4
	if cardWidth < 12 {
		cardWidth = 12
	}
	if cardWidth > 22 {
		cardWidth = 22
	}

	cards := []string{
		m.renderSummaryCard("Total", total, m.Styles.AccentText, cardWidth),
		m.renderSummaryCard("Todo", todo, m.Styles.SubtleText, cardWidth),
		m.renderSummaryCard("Active", inProgress, m.Styles.WarningText, cardWidth),
		m.renderSummaryCard("Done", done, m.Styles.SuccessText, cardWidth),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cards...)
}

func (m DashboardModel) renderSummaryCard(label string, value int, valueStyle lipgloss.Style, width int) string {
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		m.Styles.SubtleText.Render(label),
		valueStyle.Render(fmt.Sprintf("%d", value)),
	)
	return m.Styles.StatCard.Width(width).Render(body)
}
