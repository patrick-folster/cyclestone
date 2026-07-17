package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patrick-folster/cyclestone/internal/config"
	"gopkg.in/yaml.v3"
)

// DetailsModel handles rendering the detail specs and historical log timeline for a milestone.
type DetailsModel struct {
	Milestone               config.Milestone
	History                 []config.MilestoneCycleLog
	Width                   int
	Height                  int
	Styles                  Styles
	ShowAgentSelector       bool
	SelectedAgentIdx        int
	Agents                  []config.Agent
	ShowHistoryTab          bool
	LLM                     string
	Mode                    string
	BranchChange            bool
	Groups                  []config.AgentGroup
	SelectedGroupIdx        int
	ScrollOffset            int // Details scroll offset
	HistoryScrollOffset     int // History scroll offset
	AgentScrollOffset       int // Agent Selector scroll offset
	RecommendationScore     int
	HistorySelectedIdx      int
	ConfirmDeleteMilestone  bool
	ConfirmDeleteCycle      bool
	ShowInstructionDiff     bool
	InstructionReviewStatus map[int]string
}

type detailsPhaseHandoff struct {
	Summary          map[string]interface{} `yaml:"summary"`
	OutputContract   string                 `yaml:"output_contract,omitempty"`
	ValidationStatus string                 `yaml:"validation_status,omitempty"`
	ValidationErrors []string               `yaml:"validation_errors,omitempty"`
}

type proposedInstructionUpdate struct {
	SourceAgent string
	Content     string
	Patch       string
}

// NewDetailsModel creates a DetailsModel instance.
func NewDetailsModel(styles Styles) DetailsModel {
	return DetailsModel{
		Styles: styles,
	}
}

// StartCycleMsg is sent when triggering a run.
type StartCycleMsg struct {
	Milestone      config.Milestone
	SingleAgentID  string
	RunnerLLM      string
	RunnerMode     string
	NoBranchChange bool
	Group          config.AgentGroup
	Note           string
}

// Init initializes the details sub-model.
func (m DetailsModel) Init() tea.Cmd {
	return nil
}

// Update handles screen adjustments and quick actions like toggling status and work logging.
func (m DetailsModel) Update(msg tea.Msg) (DetailsModel, tea.Cmd) {
	useTabs := m.Width < 70 || m.Height < 22

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		(&m).clampScrollOffset()
		(&m).clampAgentScrollOffset()
		m.clampHistorySelection()
		return m, nil

	case tea.KeyMsg:
		// Handle confirmation screens first
		if m.ConfirmDeleteMilestone {
			switch msg.String() {
			case "y", "Y":
				m.ConfirmDeleteMilestone = false
				return m, func() tea.Msg {
					return DeleteMilestoneMsg{MilestoneID: m.Milestone.ID}
				}
			case "n", "N", "esc":
				m.ConfirmDeleteMilestone = false
				return m, nil
			}
			return m, nil
		}

		if m.ConfirmDeleteCycle {
			switch msg.String() {
			case "y", "Y":
				m.ConfirmDeleteCycle = false
				selectedCycleNum := 0
				if len(m.History) > 0 && m.HistorySelectedIdx >= 0 && m.HistorySelectedIdx < len(m.History) {
					selectedCycleNum = m.History[m.HistorySelectedIdx].CycleNumber
				}
				if selectedCycleNum > 0 {
					return m, func() tea.Msg {
						return DeleteCycleMsg{
							MilestoneID: m.Milestone.ID,
							CycleNumber: selectedCycleNum,
						}
					}
				}
				return m, nil
			case "n", "N", "esc":
				m.ConfirmDeleteCycle = false
				return m, nil
			}
			return m, nil
		}

		if m.ShowAgentSelector {
			switch msg.String() {
			case "up", "k":
				if m.SelectedAgentIdx > 0 {
					m.SelectedAgentIdx--
				}
				(&m).clampAgentScrollOffset()
				return m, nil
			case "down", "j":
				if m.SelectedAgentIdx < len(m.Agents)-1 {
					m.SelectedAgentIdx++
				}
				(&m).clampAgentScrollOffset()
				return m, nil
			case "enter":
				m.ShowAgentSelector = false
				selectedAgent := m.Agents[m.SelectedAgentIdx]
				var group config.AgentGroup
				if m.SelectedGroupIdx >= 0 && m.SelectedGroupIdx < len(m.Groups) {
					group = m.Groups[m.SelectedGroupIdx]
				}
				return m, func() tea.Msg {
					return ChangeScreenMsg{
						Screen: ScreenPreflight,
						Data: StartCycleMsg{
							Milestone:      m.Milestone,
							SingleAgentID:  selectedAgent.ID,
							RunnerLLM:      m.LLM,
							RunnerMode:     m.Mode,
							NoBranchChange: !m.BranchChange,
							Group:          group,
						},
					}
				}
			case "esc":
				m.ShowAgentSelector = false
				if useTabs {
					m.ShowHistoryTab = false
				}
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "up", "k":
			if m.ShowHistoryTab {
				if len(m.History) > 0 {
					m.HistorySelectedIdx--
					m.clampHistorySelection()
					m.HistoryScrollOffset = 0
				}
			} else {
				m.ScrollOffset--
				if m.ScrollOffset < 0 {
					m.ScrollOffset = 0
				}
			}
			return m, nil
		case "down", "j":
			if m.ShowHistoryTab {
				if len(m.History) > 0 {
					m.HistorySelectedIdx++
					m.clampHistorySelection()
					m.HistoryScrollOffset = 0
				}
			} else {
				m.ScrollOffset++
			}
			(&m).clampScrollOffset()
			return m, nil
		case "pgup", "[":
			if m.ShowHistoryTab {
				m.HistoryScrollOffset--
				if m.HistoryScrollOffset < 0 {
					m.HistoryScrollOffset = 0
				}
			}
			return m, nil
		case "pgdn", "]":
			if m.ShowHistoryTab {
				m.HistoryScrollOffset++
				(&m).clampScrollOffset()
			}
			return m, nil
		case "v":
			if m.ShowHistoryTab && m.selectedInstructionUpdate().Content != "" || m.ShowHistoryTab && m.selectedInstructionUpdate().Patch != "" {
				m.ShowInstructionDiff = !m.ShowInstructionDiff
			}
			return m, nil
		case "i":
			if m.ShowHistoryTab {
				if proposal := m.selectedInstructionUpdate(); proposal.Content != "" {
					if err := os.MkdirAll(filepath.Join(".cyclestone", "temp"), 0755); err == nil {
						_ = os.WriteFile(filepath.Join(".cyclestone", "temp", "AGENTS.md.proposed"), []byte(proposal.Content), 0644)
						m.setInstructionReviewStatus("draft saved to .cyclestone/temp/AGENTS.md.proposed")
					}
				}
			}
			return m, nil
		case "y":
			if m.ShowHistoryTab {
				if proposal := m.selectedInstructionUpdate(); proposal.Content != "" {
					if err := os.WriteFile("AGENTS.md", []byte(proposal.Content), 0644); err == nil {
						m.setInstructionReviewStatus("applied to AGENTS.md")
					} else {
						m.setInstructionReviewStatus("apply failed: " + err.Error())
					}
				}
			}
			return m, nil
		case "n":
			if m.ShowHistoryTab && (m.selectedInstructionUpdate().Content != "" || m.selectedInstructionUpdate().Patch != "") {
				m.setInstructionReviewStatus("dismissed")
			}
			return m, nil
		case "o":
			if m.ShowHistoryTab && (m.selectedInstructionUpdate().Content != "" || m.selectedInstructionUpdate().Patch != "") {
				m.setInstructionReviewStatus("kept in report")
			}
			return m, nil
		case "d":
			m.ConfirmDeleteMilestone = true
			return m, nil
		case "x":
			if len(m.History) > 0 {
				m.ConfirmDeleteCycle = true
			}
			return m, nil
		case "tab":
			m.ShowHistoryTab = !m.ShowHistoryTab
			(&m).clampScrollOffset()
			return m, nil
		case "esc", "backspace":
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenDashboard,
				}
			}
		case "r":
			// Run full cycle - transition to ScreenCreateMilestone in note-taking mode
			var group config.AgentGroup
			if m.SelectedGroupIdx >= 0 && m.SelectedGroupIdx < len(m.Groups) {
				group = m.Groups[m.SelectedGroupIdx]
			}
			return m, func() tea.Msg {
				return ChangeScreenMsg{
					Screen: ScreenCreateMilestone,
					Data: StartCycleMsg{
						Milestone:      m.Milestone,
						RunnerLLM:      m.LLM,
						RunnerMode:     m.Mode,
						NoBranchChange: !m.BranchChange,
						Group:          group,
					},
				}
			}
		case "p":
			if len(m.Groups) > 0 {
				m.SelectedGroupIdx = (m.SelectedGroupIdx + 1) % len(m.Groups)
			}
			return m, nil
		case "a":
			// Open agent selector
			agents, err := config.LoadDynamicAgents()
			if err == nil && len(agents) > 0 {
				m.Agents = agents
				m.SelectedAgentIdx = 0
				m.AgentScrollOffset = 0
				m.ShowAgentSelector = true
				m.ShowHistoryTab = true
				(&m).clampAgentScrollOffset()
			}
			return m, nil
		case "s":
			var nextStatus string
			switch m.Milestone.Status {
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
					MilestoneID: m.Milestone.ID,
					Action:      "status_changed",
					Status:      nextStatus,
				}
			}
		case "l":
			// Cycle LLM options
			options := getMilestoneRunnerOptions()

			// Find current index
			currentIdx := -1
			for i, opt := range options {
				if opt == m.LLM {
					currentIdx = i
					break
				}
			}
			if currentIdx == -1 {
				currentIdx = 0
			}

			nextIdx := (currentIdx + 1) % len(options)
			m.LLM = options[nextIdx]
			return m, nil
		case "m":
			if m.Mode == "sandbox" {
				m.Mode = "unrestricted"
			} else {
				m.Mode = "sandbox"
			}
			return m, nil
		case "g":
			m.BranchChange = !m.BranchChange
			return m, nil
		}
	}
	return m, nil
}

func (m DetailsModel) getViewportWidthLeft() int {
	useTabs := m.Width < 70 || m.Height < 22
	if useTabs {
		return m.Width - 4
	}
	marginWidth := 6
	halfWidth := (m.Width - marginWidth) / 2
	if halfWidth < 25 {
		halfWidth = 25
	}
	return halfWidth
}

func (m DetailsModel) getViewportWidthRight() int {
	useTabs := m.Width < 70 || m.Height < 22
	if useTabs {
		return m.Width - 4
	}
	marginWidth := 6
	halfWidth := (m.Width - marginWidth) / 2
	if halfWidth < 25 {
		halfWidth = 25
	}
	return halfWidth
}

func (m DetailsModel) getHelpCommands(useTabs bool, scrollHelp string) []string {
	var cmds []string
	if m.ShowAgentSelector {
		cmds = []string{"↑/↓ Navigate", "Enter Run", "Esc Close", "q Quit", "Ctrl+C Quit"}
	} else {
		cmds = []string{"Esc Dashboard"}
		if useTabs {
			cmds = append(cmds, "Tab Details/History")
		} else {
			cmds = append(cmds, "Tab Switch-Focus")
		}

		if len(m.History) > 0 {
			cmds = append(cmds, "x Delete-Cycle")
		}
		if m.ShowHistoryTab && (m.selectedInstructionUpdate().Content != "" || m.selectedInstructionUpdate().Patch != "") {
			cmds = append(cmds, "v Diff", "y Apply-AGENTS", "i Edit-Draft", "n Dismiss", "o Keep")
		}
		cmds = append(cmds, "d Delete-MS")

		if useTabs {
			cmds = append(cmds, "r Run", "a Agent", "s Status", "l LLM", "m Mode", "g Git", "p Group", "q Quit", "Ctrl+C Quit")
		} else {
			cmds = append(cmds, "r Run-Cycle", "a Agent", "s Status", "l LLM", "m Mode", "g Git", "p Group", "q Quit", "Ctrl+C Quit")
		}

		if scrollHelp != "" {
			cmds = append([]string{scrollHelp}, cmds...)
		}
	}
	return cmds
}

func (m DetailsModel) getViewportHeight() int {
	var rootOverhead = 3

	useTabs := m.Width < 70 || m.Height < 22

	var scrollHelp string
	draftHeight := m.Height - rootOverhead - 4
	if draftHeight < 3 {
		draftHeight = 3
	}

	leftWidth := m.getViewportWidthLeft()
	rightWidth := m.getViewportWidthRight()
	leftText := m.getDetailsTextForHeight(draftHeight, leftWidth)
	rightText := m.getHistoryTextForHeight(draftHeight, rightWidth)

	totalLeftLines := len(strings.Split(leftText, "\n"))
	totalRightLines := len(strings.Split(rightText, "\n"))

	activeTotalLines := totalLeftLines
	activeHeight := draftHeight - 2
	if m.ShowHistoryTab {
		activeTotalLines = totalRightLines
		activeHeight = draftHeight - 2
	}
	if activeTotalLines > activeHeight {
		scrollHelp = "↑/↓ Scroll"
	}

	helpCommands := m.getHelpCommands(useTabs, scrollHelp)

	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	helpText := renderCommandHelp(m.Styles, helpCommands, helpWidth)
	helpLines := strings.Count(helpText, "\n") + 1

	var overhead int
	if useTabs {
		overhead = 2 + helpLines
	} else {
		overhead = 1 + helpLines
	}

	h := m.Height - rootOverhead - overhead
	if h < 3 {
		return 3
	}
	return h
}

func (m *DetailsModel) clampScrollOffset() {
	height := m.getViewportHeight() - 2
	if height < 1 {
		height = 1
	}

	// Clamp Details scroll offset
	leftWidth := m.getViewportWidthLeft()
	detailsText := m.getDetailsText(leftWidth)
	detailsLines := len(strings.Split(detailsText, "\n"))
	maxDetailsScroll := detailsLines - height
	if maxDetailsScroll < 0 {
		maxDetailsScroll = 0
	}
	if m.ScrollOffset > maxDetailsScroll {
		m.ScrollOffset = maxDetailsScroll
	}
	if m.ScrollOffset < 0 {
		m.ScrollOffset = 0
	}

	// Clamp History scroll offset
	rightWidth := m.getViewportWidthRight()
	historyText := m.getHistoryText(rightWidth)
	historyLines := len(strings.Split(historyText, "\n"))
	maxHistoryScroll := historyLines - height
	if maxHistoryScroll < 0 {
		maxHistoryScroll = 0
	}
	if m.HistoryScrollOffset > maxHistoryScroll {
		m.HistoryScrollOffset = maxHistoryScroll
	}
	if m.HistoryScrollOffset < 0 {
		m.HistoryScrollOffset = 0
	}
}

func (m *DetailsModel) clampAgentScrollOffset() {
	rightHeight := m.getViewportHeight()
	rightWidth := m.getViewportWidthRight()
	spacingLines := 2
	if rightHeight < 12 {
		spacingLines = 1
	}
	footerHelp := renderCommandHelp(m.Styles, []string{"↑/↓ Navigate", "Enter Run", "Esc Close"}, rightWidth-4)
	footerLines := len(strings.Split(footerHelp, "\n"))

	overhead := 1 + spacingLines + 1 + spacingLines + 1 + footerLines
	maxVisibleAgents := (rightHeight - 2) - overhead
	if maxVisibleAgents < 2 {
		maxVisibleAgents = 2
	}

	if m.SelectedAgentIdx < m.AgentScrollOffset {
		m.AgentScrollOffset = m.SelectedAgentIdx
	}
	if m.SelectedAgentIdx >= m.AgentScrollOffset+maxVisibleAgents {
		m.AgentScrollOffset = m.SelectedAgentIdx - maxVisibleAgents + 1
	}
	if m.AgentScrollOffset > len(m.Agents)-maxVisibleAgents {
		m.AgentScrollOffset = len(m.Agents) - maxVisibleAgents
	}
	if m.AgentScrollOffset < 0 {
		m.AgentScrollOffset = 0
	}
}

func (m DetailsModel) getDetailsText(width int) string {
	return m.getDetailsTextForHeight(m.getViewportHeight(), width)
}

func (m DetailsModel) getDetailsTextForHeight(leftHeight int, leftWidth int) string {
	var spacing = "\n\n"
	if leftHeight < 12 {
		spacing = "\n"
	}

	// Status badge selection
	var statusBadge string
	switch m.Milestone.Status {
	case "In Progress":
		statusBadge = m.Styles.InProgressTag.Render("IN PROGRESS")
	case "Done":
		statusBadge = m.Styles.DoneTag.Render("DONE")
	default:
		statusBadge = m.Styles.TodoTag.Render("TODO")
	}

	var leftBuilder strings.Builder
	titleText := fmt.Sprintf("%s: %s", m.Milestone.ID, m.Milestone.Title)
	wrappedTitle := wrapText(titleText, leftWidth-4)
	leftBuilder.WriteString(m.Styles.DetailHeader.Render(wrappedTitle) + "\n")
	leftBuilder.WriteString(m.Styles.SubtleText.Render("Milestone control surface") + "\n" + spacing)

	gitText := "YES"
	if !m.BranchChange {
		gitText = "NO"
	}
	groupName := "None"
	if m.SelectedGroupIdx >= 0 && m.SelectedGroupIdx < len(m.Groups) {
		groupName = m.Groups[m.SelectedGroupIdx].Name
	}
	var recScoreStr string
	if m.RecommendationScore < 0 || m.RecommendationScore > 10 {
		recScoreStr = m.Styles.SubtleText.Render("N/A")
	} else {
		scoreStr := fmt.Sprintf("%d/10", m.RecommendationScore)
		if m.RecommendationScore <= 3 {
			recScoreStr = m.Styles.SuccessText.Render(scoreStr)
		} else if m.RecommendationScore <= 7 {
			recScoreStr = m.Styles.WarningText.Render(scoreStr)
		} else {
			recScoreStr = m.Styles.ErrorText.Render(scoreStr)
		}
	}

	// Redesign metadata/status lines to wrap and format dynamically across multiple columns/rows if narrow
	type metaItem struct {
		label string
		value string
	}
	items := []metaItem{
		{m.Styles.DetailLabel.Render("Status:"), statusBadge},
		{m.Styles.DetailLabel.Render("Cycles:"), fmt.Sprintf("%d", m.Milestone.Cycles)},
		{m.Styles.DetailLabel.Render("LLM:"), m.Styles.SuccessText.Render(strings.ToUpper(m.LLM))},
		{m.Styles.DetailLabel.Render("Mode:"), m.Styles.SuccessText.Render(strings.ToUpper(m.Mode))},
		{m.Styles.DetailLabel.Render("Git:"), m.Styles.SuccessText.Render(gitText)},
		{m.Styles.DetailLabel.Render("Group:"), m.Styles.AccentText.Render(groupName)},
		{m.Styles.DetailLabel.Render("Rec Score:"), recScoreStr},
	}

	var currentLine []string
	var currentLineWidth int
	targetWidth := leftWidth - 4
	if targetWidth < 20 {
		targetWidth = 20
	}

	for _, item := range items {
		itemStr := item.label + " " + item.value
		itemWidth := lipgloss.Width(itemStr)
		if len(currentLine) == 0 {
			currentLine = append(currentLine, itemStr)
			currentLineWidth = itemWidth
		} else if currentLineWidth+3+itemWidth <= targetWidth {
			currentLine = append(currentLine, itemStr)
			currentLineWidth += 3 + itemWidth
		} else {
			leftBuilder.WriteString(strings.Join(currentLine, "   ") + "\n")
			currentLine = []string{itemStr}
			currentLineWidth = itemWidth
		}
	}
	if len(currentLine) > 0 {
		leftBuilder.WriteString(strings.Join(currentLine, "   ") + "\n")
	}
	leftBuilder.WriteString(spacing)

	leftBuilder.WriteString(m.Styles.DetailLabel.Render("Goal:") + "\n")
	goalText := m.Milestone.Goal
	if goalText == "" {
		goalText = "No goal specified."
	}
	wrappedGoal := wrapText(goalText, leftWidth-4)
	leftBuilder.WriteString(m.Styles.DetailValue.Render(wrappedGoal) + "\n" + spacing)

	leftBuilder.WriteString(m.Styles.DetailLabel.Render("Acceptance Criteria:") + "\n")
	if len(m.Milestone.AcceptanceCriteria) == 0 {
		leftBuilder.WriteString(m.Styles.DetailValue.Render("- None defined") + "\n" + spacing)
	} else {
		acLimit := len(m.Milestone.AcceptanceCriteria)
		if leftHeight < 12 && acLimit > 2 {
			acLimit = 2
		}
		for idx, ac := range m.Milestone.AcceptanceCriteria {
			if idx >= acLimit {
				leftBuilder.WriteString(fmt.Sprintf("  %s\n", m.Styles.HelpStyle.Render(fmt.Sprintf("... and %d more", len(m.Milestone.AcceptanceCriteria)-acLimit))))
				break
			}
			wrappedAC := wrapTextWithIndent(ac, leftWidth-4, "   ")
			leftBuilder.WriteString(fmt.Sprintf(" %s %s\n", m.Styles.SuccessText.Render(m.Styles.GlyphCheck), m.Styles.DetailValue.Render(wrappedAC)))
		}
		leftBuilder.WriteString("\n")
	}

	if leftHeight >= 12 {
		leftBuilder.WriteString(m.Styles.DetailLabel.Render("Agent Pipeline Flow:") + "\n")
		if m.SelectedGroupIdx >= 0 && m.SelectedGroupIdx < len(m.Groups) {
			group := m.Groups[m.SelectedGroupIdx]
			agents, err := config.LoadDynamicAgents()
			if err != nil || len(agents) == 0 {
				leftBuilder.WriteString(m.Styles.DetailValue.Render("- No dynamic agents found") + "\n")
			} else {
				for _, id := range group.AgentIDs {
					var foundAgent *config.Agent
					for i := range agents {
						if agents[i].ID == id {
							foundAgent = &agents[i]
							break
						}
					}
					if foundAgent == nil {
						leftBuilder.WriteString(fmt.Sprintf("  %s %s (%s missing!)\n",
							m.Styles.ErrorText.Render(m.Styles.GlyphCross),
							m.Styles.ErrorText.Render(id),
							m.Styles.WarningText.Render(m.Styles.GlyphWarning),
						))
					} else {
						runner := foundAgent.RunnerBinary
						if m.LLM != "" && runner != "manual" {
							runner = m.LLM
						}
						leftBuilder.WriteString(fmt.Sprintf("  %s %s (%s)\n",
							m.Styles.WarningText.Render(m.Styles.GlyphPointer),
							m.Styles.DetailValue.Render(foundAgent.Name),
							m.Styles.HelpStyle.Render(runner),
						))
					}
				}
			}
		} else {
			leftBuilder.WriteString(m.Styles.DetailValue.Render("- No active group selected") + "\n")
		}
	}
	return leftBuilder.String()
}

func (m DetailsModel) getHistoryText(width int) string {
	return m.getHistoryTextForHeight(m.getViewportHeight(), width)
}

func (m DetailsModel) getHistoryTextForHeight(rightHeight int, rightWidth int) string {
	var rightBuilder strings.Builder
	var rightSpacing = "\n\n"
	if rightHeight < 12 {
		rightSpacing = "\n"
	}

	if m.ShowAgentSelector {
		rightBuilder.WriteString(m.Styles.DetailHeader.Render("Select Agent to Run") + "\n" + rightSpacing)
		rightBuilder.WriteString(m.Styles.HelpStyle.Render("Select which agent to execute individually:") + "\n" + rightSpacing)

		// Calculate visible agents window
		spacingLines := 2
		if rightHeight < 12 {
			spacingLines = 1
		}
		footerHelp := renderCommandHelp(m.Styles, []string{"↑/↓ Navigate", "Enter Run", "Esc Close"}, rightWidth-4)
		footerLines := len(strings.Split(footerHelp, "\n"))
		overhead := 1 + spacingLines + 1 + spacingLines + 1 + footerLines
		maxVisibleAgents := (rightHeight - 2) - overhead
		if maxVisibleAgents < 2 {
			maxVisibleAgents = 2
		}

		// Clamp the scroll offset
		if m.SelectedAgentIdx < m.AgentScrollOffset {
			m.AgentScrollOffset = m.SelectedAgentIdx
		}
		if m.SelectedAgentIdx >= m.AgentScrollOffset+maxVisibleAgents {
			m.AgentScrollOffset = m.SelectedAgentIdx - maxVisibleAgents + 1
		}
		if m.AgentScrollOffset > len(m.Agents)-maxVisibleAgents {
			m.AgentScrollOffset = len(m.Agents) - maxVisibleAgents
		}
		if m.AgentScrollOffset < 0 {
			m.AgentScrollOffset = 0
		}

		endIdx := m.AgentScrollOffset + maxVisibleAgents
		if endIdx > len(m.Agents) {
			endIdx = len(m.Agents)
		}

		for idx := m.AgentScrollOffset; idx < endIdx; idx++ {
			a := m.Agents[idx]
			prefix := "  "
			textLine := fmt.Sprintf("%s (%s)", a.Name, a.Description)
			maxLineLen := rightWidth - 5
			if maxLineLen > 10 && len(textLine) > maxLineLen {
				textLine = textLine[:maxLineLen-3] + "..."
			}

			var line string
			if idx == m.SelectedAgentIdx {
				prefix = m.Styles.WarningText.Render(m.Styles.GlyphPointer + " ")
				line = m.Styles.ListSelectedRow.Render(textLine)
			} else {
				line = m.Styles.DetailValue.Render(textLine)
			}
			rightBuilder.WriteString(fmt.Sprintf("%s%s\n", prefix, line))
		}

		// Fill remaining lines to avoid layout jumping
		visibleCount := endIdx - m.AgentScrollOffset
		for i := visibleCount; i < maxVisibleAgents; i++ {
			rightBuilder.WriteString("\n")
		}

		rightBuilder.WriteString("\n" + footerHelp)
	} else {
		rightBuilder.WriteString(m.Styles.DetailHeader.Render("Cycle History & Logs") + "\n" + rightSpacing)

		if len(m.History) == 0 {
			rightBuilder.WriteString(m.Styles.HelpStyle.Render("No history recorded for this milestone.\nPress 'r' to run a milestone cycle."))
		} else {
			m.clampHistorySelection()
			rightBuilder.WriteString(m.Styles.DetailLabel.Render("Select Cycle (↑/↓ Navigate):") + "\n")
			for idx, h := range m.History {
				statusText := h.Status
				if statusText == "approved" || statusText == "Success" {
					statusText = m.Styles.SuccessText.Render(statusText)
				} else if statusText == "failed" {
					statusText = m.Styles.ErrorText.Render(statusText)
				} else {
					statusText = m.Styles.WarningText.Render(statusText)
				}

				prefix := "  "
				lineContent := fmt.Sprintf("Cycle %d - Status: %s (%s)", h.CycleNumber, statusText, h.Timestamp.Format("2006-01-02 15:04:05"))

				if idx == m.HistorySelectedIdx {
					prefix = m.Styles.WarningText.Render(m.Styles.GlyphPointer + " ")
					rightBuilder.WriteString(prefix + m.Styles.ListSelectedRow.Render(lineContent) + "\n")
				} else {
					rightBuilder.WriteString(prefix + m.Styles.DetailValue.Render(lineContent) + "\n")
				}
			}

			rightBuilder.WriteString("\n" + m.Styles.SubtleText.Render(strings.Repeat("─", rightWidth-4)) + "\n\n")

			// Selected cycle rich details
			h := m.History[m.HistorySelectedIdx]

			statusText := h.Status
			if statusText == "approved" || statusText == "Success" {
				statusText = m.Styles.SuccessText.Render(statusText)
			} else if statusText == "failed" {
				statusText = m.Styles.ErrorText.Render(statusText)
			} else {
				statusText = m.Styles.WarningText.Render(statusText)
			}

			rightBuilder.WriteString(m.Styles.DetailLabel.Render(fmt.Sprintf("Cycle %d Rich Details:", h.CycleNumber)) + "\n")
			rightBuilder.WriteString(fmt.Sprintf("  %s %s\n", m.Styles.DetailLabel.Render("Status:"), statusText))
			rightBuilder.WriteString(fmt.Sprintf("  %s %s\n", m.Styles.DetailLabel.Render("Timestamp:"), h.Timestamp.Format("2006-01-02 15:04:05")))

			durationStr := h.Duration
			if durationStr == "" {
				durationStr = "N/A"
			}
			rightBuilder.WriteString(fmt.Sprintf("  %s %s\n", m.Styles.DetailLabel.Render("Duration:"), m.Styles.SuccessText.Render(durationStr)))

			if h.Branch != "" {
				commitStr := ""
				if h.CommitHash != "" && h.CommitHash != "none" {
					commitStr = " @ " + h.CommitHash
				}
				rightBuilder.WriteString(fmt.Sprintf("  %s %s%s\n",
					m.Styles.DetailLabel.Render("Branch:"),
					m.Styles.DetailValue.Render(h.Branch),
					m.Styles.HelpStyle.Render(commitStr),
				))
			}

			noteText := h.UserNote
			if noteText == "" {
				noteText = "None"
			}
			rightBuilder.WriteString(fmt.Sprintf("  %s %s\n", m.Styles.DetailLabel.Render("Note:"), m.Styles.DetailValue.Render(wrapText(noteText, rightWidth-14))))

			rightBuilder.WriteString(m.Styles.DetailLabel.Render("  Actions:") + "\n")
			if len(h.Actions) == 0 {
				rightBuilder.WriteString("    - No actions logged\n")
			} else {
				for _, action := range h.Actions {
					verdict := m.Styles.SuccessText.Render(m.Styles.GlyphCheck)
					if action.ExitCode != 0 {
						verdict = m.Styles.ErrorText.Render(m.Styles.GlyphCross)
					}

					actDuration := action.Duration
					if actDuration == "" {
						actDuration = "N/A"
					}

					rightBuilder.WriteString(fmt.Sprintf("    %s %s (Exit Code: %d, Duration: %s)\n",
						verdict,
						m.Styles.AccentText.Render(action.AgentID),
						action.ExitCode,
						actDuration,
					))
					rightBuilder.WriteString(fmt.Sprintf("      %s %s\n", m.Styles.HelpStyle.Render("Input:"), m.Styles.HelpStyle.Render(action.InputFile)))
					rightBuilder.WriteString(fmt.Sprintf("      %s %s\n", m.Styles.HelpStyle.Render("Output:"), m.Styles.HelpStyle.Render(action.OutputFile)))
					if metadata := m.renderActionContractMetadata(action, rightWidth); metadata != "" {
						rightBuilder.WriteString(metadata)
					}
				}
			}
			if proposal := m.selectedInstructionUpdate(); proposal.Content != "" || proposal.Patch != "" {
				rightBuilder.WriteString(m.renderInstructionUpdateReview(proposal, rightWidth))
			}
		}
	}
	return rightBuilder.String()
}

func (m DetailsModel) selectedInstructionUpdate() proposedInstructionUpdate {
	if len(m.History) == 0 || m.HistorySelectedIdx < 0 || m.HistorySelectedIdx >= len(m.History) {
		return proposedInstructionUpdate{}
	}
	for _, action := range m.History[m.HistorySelectedIdx].Actions {
		handoff, ok := loadDetailsPhaseHandoff(action.OutputFile)
		if !ok || handoff.Summary == nil {
			continue
		}
		if text := contractStringValue(handoff.Summary["proposed_agent_instructions_update"]); text != "" {
			return proposedInstructionUpdate{SourceAgent: action.AgentID, Content: text}
		}
		if text := contractStringValue(handoff.Summary["proposed_agents_md_update"]); text != "" {
			return proposedInstructionUpdate{SourceAgent: action.AgentID, Content: text}
		}
		if text := contractStringValue(handoff.Summary["proposed_agent_instructions_patch"]); text != "" {
			return proposedInstructionUpdate{SourceAgent: action.AgentID, Patch: text}
		}
	}
	return proposedInstructionUpdate{}
}

func (m *DetailsModel) setInstructionReviewStatus(status string) {
	if m.InstructionReviewStatus == nil {
		m.InstructionReviewStatus = map[int]string{}
	}
	if len(m.History) > 0 && m.HistorySelectedIdx >= 0 && m.HistorySelectedIdx < len(m.History) {
		m.InstructionReviewStatus[m.History[m.HistorySelectedIdx].CycleNumber] = status
	}
}

func (m DetailsModel) currentInstructionReviewStatus() string {
	if m.InstructionReviewStatus == nil || len(m.History) == 0 || m.HistorySelectedIdx < 0 || m.HistorySelectedIdx >= len(m.History) {
		return ""
	}
	return m.InstructionReviewStatus[m.History[m.HistorySelectedIdx].CycleNumber]
}

func (m DetailsModel) renderInstructionUpdateReview(proposal proposedInstructionUpdate, width int) string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(m.Styles.DetailLabel.Render("  Proposed AGENTS.md Update:") + "\n")
	if proposal.SourceAgent != "" {
		sb.WriteString(fmt.Sprintf("    %s %s\n", m.Styles.HelpStyle.Render("Source:"), m.Styles.DetailValue.Render(proposal.SourceAgent)))
	}
	if status := m.currentInstructionReviewStatus(); status != "" {
		sb.WriteString(fmt.Sprintf("    %s %s\n", m.Styles.HelpStyle.Render("Status:"), m.Styles.DetailValue.Render(status)))
	}
	sb.WriteString(fmt.Sprintf("    %s\n", m.Styles.HelpStyle.Render("v diff  y apply  i save editable draft  n dismiss  o keep in report")))
	if m.ShowInstructionDiff {
		text := proposal.Patch
		if text == "" {
			text = renderInstructionContentDiff(proposal.Content)
		}
		for _, line := range strings.Split(wrapText(text, width-8), "\n") {
			sb.WriteString("    " + m.Styles.DetailValue.Render(line) + "\n")
		}
	}
	return sb.String()
}

func renderInstructionContentDiff(proposed string) string {
	currentBytes, _ := os.ReadFile("AGENTS.md")
	current := strings.TrimSpace(string(currentBytes))
	proposed = strings.TrimSpace(proposed)
	if current == proposed {
		return "No content changes from current AGENTS.md."
	}
	var sb strings.Builder
	if current == "" {
		sb.WriteString("--- AGENTS.md (missing or empty)\n")
	} else {
		sb.WriteString("--- AGENTS.md (current)\n")
	}
	sb.WriteString("+++ AGENTS.md (proposed)\n")
	if current != "" {
		sb.WriteString("- " + current + "\n")
	}
	if proposed != "" {
		sb.WriteString("+ " + proposed + "\n")
	}
	return sb.String()
}

func (m DetailsModel) renderActionContractMetadata(action config.AgentActionLog, width int) string {
	handoff, ok := loadDetailsPhaseHandoff(action.OutputFile)
	if !ok || handoff.OutputContract == "" {
		return ""
	}
	var sb strings.Builder
	label := strings.ToUpper(handoff.OutputContract)
	if handoff.ValidationStatus != "" {
		label += " " + handoff.ValidationStatus
	}
	sb.WriteString(fmt.Sprintf("      %s %s\n", m.Styles.HelpStyle.Render("Contract:"), m.Styles.HelpStyle.Render(label)))
	for _, errText := range handoff.ValidationErrors {
		sb.WriteString(fmt.Sprintf("        %s %s\n", m.Styles.ErrorText.Render(m.Styles.GlyphCross), m.Styles.DetailValue.Render(wrapText(errText, width-14))))
	}
	switch handoff.OutputContract {
	case "developer":
		appendStringList(&sb, m.Styles, "Changed:", contractStringSlice(handoff.Summary["changed_files"]), width)
		appendStringList(&sb, m.Styles, "Checks:", contractStringSlice(handoff.Summary["checks_run"]), width)
		appendStringList(&sb, m.Styles, "Risks:", contractStringSlice(handoff.Summary["risks"]), width)
	case "qa":
		if verdict, ok := handoff.Summary["verdict"].(string); ok && verdict != "" {
			sb.WriteString(fmt.Sprintf("        %s %s\n", m.Styles.HelpStyle.Render("Verdict:"), m.Styles.DetailValue.Render(verdict)))
		}
		appendStringList(&sb, m.Styles, "Failing:", contractStringSlice(handoff.Summary["failing_checks"]), width)
		appendStringList(&sb, m.Styles, "Fixes:", contractStringSlice(handoff.Summary["required_fixes"]), width)
	case "recommender":
		if score, ok := numericDetailsScore(handoff.Summary["score"]); ok {
			sb.WriteString(fmt.Sprintf("        %s %d/10\n", m.Styles.HelpStyle.Render("Score:"), score))
		}
		if verdict, ok := handoff.Summary["verdict"].(string); ok && verdict != "" {
			sb.WriteString(fmt.Sprintf("        %s %s\n", m.Styles.HelpStyle.Render("Verdict:"), m.Styles.DetailValue.Render(verdict)))
		}
	}
	return sb.String()
}

func loadDetailsPhaseHandoff(outputPath string) (detailsPhaseHandoff, bool) {
	var handoff detailsPhaseHandoff
	if outputPath == "" {
		return handoff, false
	}
	handoffPath := strings.TrimSuffix(outputPath, "-output.log") + "-handoff.yaml"
	if handoffPath == outputPath {
		base := strings.TrimSuffix(outputPath, filepath.Ext(outputPath))
		handoffPath = base + "-handoff.yaml"
	}
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		return handoff, false
	}
	if err := yaml.Unmarshal(data, &handoff); err != nil {
		return handoff, false
	}
	if handoff.OutputContract == "" && handoff.ValidationStatus == "" && len(handoff.Summary) == 0 {
		return handoff, false
	}
	return handoff, true
}

func numericDetailsScore(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	default:
		return 0, false
	}
}

func appendStringList(sb *strings.Builder, styles Styles, label string, values []string, width int) {
	if len(values) == 0 {
		return
	}
	limit := len(values)
	if limit > 4 {
		limit = 4
	}
	for i := 0; i < limit; i++ {
		sb.WriteString(fmt.Sprintf("        %s %s\n", styles.HelpStyle.Render(label), styles.DetailValue.Render(wrapText(values[i], width-16))))
	}
	if len(values) > limit {
		sb.WriteString(fmt.Sprintf("        %s %s\n", styles.HelpStyle.Render(label), styles.HelpStyle.Render(fmt.Sprintf("... and %d more", len(values)-limit))))
	}
}

func contractStringSlice(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contractStringValue(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

// View structures a responsive split-screen displaying details and chronological cycle history.
func (m DetailsModel) View() string {
	if m.ConfirmDeleteMilestone {
		return m.renderDeleteMilestoneConfirmation()
	}
	if m.ConfirmDeleteCycle {
		return m.renderDeleteCycleConfirmation()
	}

	if m.Milestone.ID == "" {
		return "No milestone selected."
	}

	useTabs := m.Width < 70 || m.Height < 22

	leftWidth := m.getViewportWidthLeft()
	rightWidth := m.getViewportWidthRight()
	leftHeight := m.getViewportHeight()
	rightHeight := leftHeight

	leftText := m.getDetailsText(leftWidth)
	rightText := m.getHistoryText(rightWidth)

	var leftContent, rightContent string
	var totalLeftLines, totalRightLines int

	if useTabs {
		if m.ShowHistoryTab {
			rightContent, totalRightLines = sliceLines(rightText, m.HistoryScrollOffset, rightHeight-2)
		} else {
			leftContent, totalLeftLines = sliceLines(leftText, m.ScrollOffset, leftHeight-2)
		}
	} else {
		leftContent, totalLeftLines = sliceLines(leftText, m.ScrollOffset, leftHeight-2)
		rightContent, totalRightLines = sliceLines(rightText, m.HistoryScrollOffset, rightHeight-2)
	}

	var leftBorder, rightBorder lipgloss.Style
	if useTabs {
		leftBorder = m.Styles.ActiveBorder
		rightBorder = m.Styles.ActiveBorder
	} else {
		if m.ShowHistoryTab {
			leftBorder = m.Styles.InactiveBorder
			rightBorder = m.Styles.ActiveBorder
		} else {
			leftBorder = m.Styles.ActiveBorder
			rightBorder = m.Styles.InactiveBorder
		}
	}

	leftBox := leftBorder.
		Width(leftWidth).
		Height(leftHeight).
		Render(leftContent)

	rightBox := rightBorder.
		Width(rightWidth).
		Height(rightHeight).
		Render(rightContent)

	var mainLayout string
	if useTabs {
		tabLine := lipgloss.JoinHorizontal(
			lipgloss.Top,
			tabStyle(m.Styles, "DETAILS", !m.ShowHistoryTab),
			tabStyle(m.Styles, "HISTORY", m.ShowHistoryTab),
		)
		if m.ShowHistoryTab {
			mainLayout = lipgloss.JoinVertical(lipgloss.Left, tabLine, rightBox)
		} else {
			mainLayout = lipgloss.JoinVertical(lipgloss.Left, tabLine, leftBox)
		}
	} else {
		mainLayout = lipgloss.JoinHorizontal(lipgloss.Top, leftBox, rightBox)
	}

	var scrollHelp string
	activeTotalLines := totalLeftLines
	activeHeight := leftHeight - 2
	if m.ShowHistoryTab {
		activeTotalLines = totalRightLines
		activeHeight = rightHeight - 2
	}
	if activeTotalLines > activeHeight {
		scrollHelp = "↑/↓ Scroll"
	}

	helpCommands := m.getHelpCommands(useTabs, scrollHelp)
	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	helpText := renderCommandHelp(m.Styles, helpCommands, helpWidth)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		mainLayout,
		helpText,
	)
}

func tabStyle(styles Styles, label string, active bool) string {
	if active {
		return styles.CommandKey.Render(" " + label + " ")
	}
	return styles.HelpStyle.Padding(0, 1).Render(label)
}

func sliceLines(s string, startLine, maxLines int) (string, int) {
	lines := strings.Split(s, "\n")
	totalLines := len(lines)
	if startLine < 0 {
		startLine = 0
	}
	if startLine >= totalLines {
		startLine = totalLines - 1
	}
	if startLine < 0 {
		startLine = 0
	}
	end := startLine + maxLines
	if end > totalLines {
		end = totalLines
	}
	return strings.Join(lines[startLine:end], "\n"), totalLines
}

func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	var wrappedLines []string
	for _, line := range lines {
		words := strings.Fields(line)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}
		var sb strings.Builder
		currentLineLength := 0
		for _, word := range words {
			if currentLineLength == 0 {
				sb.WriteString(word)
				currentLineLength = len(word)
			} else if currentLineLength+1+len(word) <= width {
				sb.WriteString(" ")
				sb.WriteString(word)
				currentLineLength += 1 + len(word)
			} else {
				sb.WriteString("\n")
				sb.WriteString(word)
				currentLineLength = len(word)
			}
		}
		wrappedLines = append(wrappedLines, sb.String())
	}
	return strings.Join(wrappedLines, "\n")
}

func wrapTextWithIndent(text string, width int, indent string) string {
	wrapped := wrapText(text, width-len(indent))
	lines := strings.Split(wrapped, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *DetailsModel) clampHistorySelection() {
	if len(m.History) == 0 {
		m.HistorySelectedIdx = 0
		return
	}
	if m.HistorySelectedIdx < 0 {
		m.HistorySelectedIdx = 0
	}
	if m.HistorySelectedIdx >= len(m.History) {
		m.HistorySelectedIdx = len(m.History) - 1
	}
}

func (m DetailsModel) renderDeleteMilestoneConfirmation() string {
	boxWidth := m.Width - 4
	if boxWidth < 20 {
		boxWidth = 20
	}
	boxHeight := m.Height - 6
	if boxHeight < 8 {
		boxHeight = 8
	}

	errorRed := lipgloss.AdaptiveColor{Light: "1", Dark: "9"}
	errorBoxStyle := lipgloss.NewStyle().
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(errorRed)

	title := m.Styles.ErrorText.Render("DELETE MILESTONE: " + m.Milestone.ID)
	bodyLines := []string{
		title,
		"",
		m.Styles.WarningText.Render("Are you sure you want to delete this milestone?"),
		"This will permanently remove:",
		fmt.Sprintf("  • The entry from %s", m.Styles.HelpStyle.Render(".cyclestone/milestone.yml")),
		fmt.Sprintf("  • Its specification file (%s)", m.Styles.HelpStyle.Render(m.Milestone.SpecPath)),
		"  • All associated state tracking and history logs",
		"  • All cycle reports and generated artifacts",
		"",
		m.Styles.ErrorText.Render("WARNING: This action is irreversible!"),
		"",
		renderCommandHelp(m.Styles, []string{"y Yes, Delete", "n No, Cancel"}, boxWidth-4),
	}

	body := errorBoxStyle.
		Width(boxWidth).
		Height(boxHeight).
		Render(strings.Join(bodyLines, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, body)
}

func (m DetailsModel) renderDeleteCycleConfirmation() string {
	boxWidth := m.Width - 4
	if boxWidth < 20 {
		boxWidth = 20
	}
	boxHeight := m.Height - 6
	if boxHeight < 8 {
		boxHeight = 8
	}

	selectedCycleNum := 0
	if len(m.History) > 0 && m.HistorySelectedIdx >= 0 && m.HistorySelectedIdx < len(m.History) {
		selectedCycleNum = m.History[m.HistorySelectedIdx].CycleNumber
	}

	errorRed := lipgloss.AdaptiveColor{Light: "1", Dark: "9"}
	errorBoxStyle := lipgloss.NewStyle().
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(errorRed)

	title := m.Styles.ErrorText.Render(fmt.Sprintf("DELETE CYCLE: %d", selectedCycleNum))
	bodyLines := []string{
		title,
		"",
		m.Styles.WarningText.Render(fmt.Sprintf("Are you sure you want to delete Cycle %d?", selectedCycleNum)),
		"This will permanently remove:",
		"  • This cycle's history record in state.json",
		"  • Corresponding cycle report files and logs on disk",
		"  • Note: Remaining cycles will be renumbered sequentially",
		"",
		m.Styles.ErrorText.Render("WARNING: This action is irreversible!"),
		"",
		renderCommandHelp(m.Styles, []string{"y Yes, Delete", "n No, Cancel"}, boxWidth-4),
	}

	body := errorBoxStyle.
		Width(boxWidth).
		Height(boxHeight).
		Render(strings.Join(bodyLines, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left, body)
}
