package tui

import (
	"fmt"
	"strings"

	"github.com/patrick-folster/cyclestone/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AgentGroupsModel manages viewing, editing, and sorting agent pipeline groups.
type AgentGroupsModel struct {
	Groups                []config.AgentGroup // Merged agent groups lists
	AvailableAgents       []config.Agent      // Discovered dynamic agents list
	SelectedGroupIdx      int
	SelectedAgentIdx      int
	AvailableAgentIdx     int
	FocusCol              int // 0: Groups List, 1: Pipeline Agents list, 2: Available Agents list, 3: Naming Group
	NewGroupName          string
	SavePrompt            bool
	Width                 int
	Height                int
	Styles                Styles
	ErrorMsg              string
	SuccessMsg            string
	HasChanges            bool
	GroupScrollOffset     int
	PipelineScrollOffset  int
	AvailableScrollOffset int
}

// NewAgentGroupsModel instantiates a new AgentGroupsModel.
func NewAgentGroupsModel(styles Styles) AgentGroupsModel {
	return AgentGroupsModel{
		Styles: styles,
	}
}

func (m *AgentGroupsModel) loadAgentGroups() {
	settings := config.LoadMergedSettings()
	m.Groups = settings.AgentGroups

	// Load available agents
	agents, err := config.LoadDynamicAgents()
	if err != nil {
		m.ErrorMsg = fmt.Sprintf("Error loading agents: %v", err)
	} else {
		m.AvailableAgents = agents
	}
}

func (m *AgentGroupsModel) isAgentMissing(id string) bool {
	for _, a := range m.AvailableAgents {
		if a.ID == id {
			return false
		}
	}
	return true
}

func (m AgentGroupsModel) Init() tea.Cmd {
	return nil
}

func (m AgentGroupsModel) Update(msg tea.Msg) (AgentGroupsModel, tea.Cmd) {
	var cmd tea.Cmd
	m, cmd = m.updateInner(msg)
	m.ClampScrollOffsets()
	return m, cmd
}

func (m AgentGroupsModel) updateInner(msg tea.Msg) (AgentGroupsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// 1. Handle Naming Group Dialog
		if m.FocusCol == 3 {
			switch msg.String() {
			case "enter":
				name := strings.TrimSpace(m.NewGroupName)
				if name != "" {
					dup := false
					for _, g := range m.Groups {
						if strings.ToLower(g.Name) == strings.ToLower(name) {
							dup = true
							break
						}
					}
					if dup {
						m.ErrorMsg = "Group name already exists."
					} else {
						m.Groups = append(m.Groups, config.AgentGroup{
							Name:     name,
							AgentIDs: []string{},
						})
						m.SelectedGroupIdx = len(m.Groups) - 1
						m.FocusCol = 1 // Focus pipeline agents in the new group to allow additions
						m.NewGroupName = ""
						m.HasChanges = true
						m.ErrorMsg = ""
						m.SuccessMsg = ""
					}
				} else {
					m.FocusCol = 0
				}
			case "esc":
				m.FocusCol = 0
				m.NewGroupName = ""
				m.ErrorMsg = ""
			case "backspace":
				if len(m.NewGroupName) > 0 {
					m.NewGroupName = m.NewGroupName[:len(m.NewGroupName)-1]
				}
			default:
				if len(msg.String()) == 1 && len(m.NewGroupName) < 30 {
					m.NewGroupName += msg.String()
				}
			}
			return m, nil
		}

		// 2. Handle Save Scope Prompt Dialog
		if m.SavePrompt {
			switch msg.String() {
			case "p":
				m.SavePrompt = false
				m.saveGroups("project")
			case "g":
				m.SavePrompt = false
				m.saveGroups("global")
			case "esc":
				m.SavePrompt = false
				m.ErrorMsg = ""
			}
			return m, nil
		}

		// 3. Main Navigation and Commands
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg {
				return ChangeScreenMsg{Screen: ScreenDashboard}
			}

		case "tab", "right", "l":
			if !m.SavePrompt && m.FocusCol != 3 {
				m.FocusCol = (m.FocusCol + 1) % 3
				m.ErrorMsg = ""
				m.SuccessMsg = ""
			}
			return m, nil

		case "shift+tab", "left", "h":
			if !m.SavePrompt && m.FocusCol != 3 {
				m.FocusCol = (m.FocusCol - 1 + 3) % 3
				m.ErrorMsg = ""
				m.SuccessMsg = ""
			}
			return m, nil

		case "1":
			if !m.SavePrompt && m.FocusCol != 3 {
				m.FocusCol = 0
				m.ErrorMsg = ""
				m.SuccessMsg = ""
			}
			return m, nil

		case "2":
			if !m.SavePrompt && m.FocusCol != 3 {
				m.FocusCol = 1
				m.ErrorMsg = ""
				m.SuccessMsg = ""
			}
			return m, nil

		case "3":
			if !m.SavePrompt && m.FocusCol != 3 {
				m.FocusCol = 2
				m.ErrorMsg = ""
				m.SuccessMsg = ""
			}
			return m, nil

		case "up", "k":
			m.handleNav(true)
			return m, nil

		case "down", "j":
			m.handleNav(false)
			return m, nil

		case "shift+up":
			if m.FocusCol == 1 && len(m.Groups) > 0 {
				groupIdx := m.SelectedGroupIdx
				if strings.ToLower(m.Groups[groupIdx].Name) == "default" {
					m.ErrorMsg = "Cannot modify predefined Default group."
					return m, nil
				}
				agents := m.Groups[groupIdx].AgentIDs
				idx := m.SelectedAgentIdx
				if idx > 0 && idx < len(agents) {
					m.Groups[groupIdx].AgentIDs[idx], m.Groups[groupIdx].AgentIDs[idx-1] = m.Groups[groupIdx].AgentIDs[idx-1], m.Groups[groupIdx].AgentIDs[idx]
					m.SelectedAgentIdx--
					m.HasChanges = true
					m.ErrorMsg = ""
					m.SuccessMsg = ""
				}
			}
			return m, nil

		case "shift+down":
			if m.FocusCol == 1 && len(m.Groups) > 0 {
				groupIdx := m.SelectedGroupIdx
				if strings.ToLower(m.Groups[groupIdx].Name) == "default" {
					m.ErrorMsg = "Cannot modify predefined Default group."
					return m, nil
				}
				agents := m.Groups[groupIdx].AgentIDs
				idx := m.SelectedAgentIdx
				if idx >= 0 && idx < len(agents)-1 {
					m.Groups[groupIdx].AgentIDs[idx], m.Groups[groupIdx].AgentIDs[idx+1] = m.Groups[groupIdx].AgentIDs[idx+1], m.Groups[groupIdx].AgentIDs[idx]
					m.SelectedAgentIdx++
					m.HasChanges = true
					m.ErrorMsg = ""
					m.SuccessMsg = ""
				}
			}
			return m, nil

		case "backspace", "delete", "d":
			if m.FocusCol == 1 && len(m.Groups) > 0 {
				groupIdx := m.SelectedGroupIdx
				if strings.ToLower(m.Groups[groupIdx].Name) == "default" {
					m.ErrorMsg = "Cannot modify predefined Default group."
					return m, nil
				}
				agents := m.Groups[groupIdx].AgentIDs
				if len(agents) > 0 && m.SelectedAgentIdx < len(agents) {
					idx := m.SelectedAgentIdx
					m.Groups[groupIdx].AgentIDs = append(agents[:idx], agents[idx+1:]...)
					if m.SelectedAgentIdx >= len(m.Groups[groupIdx].AgentIDs) && m.SelectedAgentIdx > 0 {
						m.SelectedAgentIdx--
					}
					m.HasChanges = true
					m.ErrorMsg = ""
					m.SuccessMsg = ""
				}
			} else if m.FocusCol == 0 && len(m.Groups) > 0 {
				groupIdx := m.SelectedGroupIdx
				if strings.ToLower(m.Groups[groupIdx].Name) == "default" {
					m.ErrorMsg = "Cannot delete predefined Default group."
					return m, nil
				}
				m.Groups = append(m.Groups[:groupIdx], m.Groups[groupIdx+1:]...)
				if m.SelectedGroupIdx >= len(m.Groups) && m.SelectedGroupIdx > 0 {
					m.SelectedGroupIdx--
				}
				m.HasChanges = true
				m.ErrorMsg = ""
				m.SuccessMsg = ""
			}
			return m, nil

		case "enter", "a":
			if m.FocusCol == 2 && len(m.Groups) > 0 {
				groupIdx := m.SelectedGroupIdx
				if strings.ToLower(m.Groups[groupIdx].Name) == "default" {
					m.ErrorMsg = "Cannot modify predefined Default group."
					return m, nil
				}
				if len(m.AvailableAgents) > 0 && m.AvailableAgentIdx < len(m.AvailableAgents) {
					agent := m.AvailableAgents[m.AvailableAgentIdx]
					m.Groups[groupIdx].AgentIDs = append(m.Groups[groupIdx].AgentIDs, agent.ID)
					m.SelectedAgentIdx = len(m.Groups[groupIdx].AgentIDs) - 1
					m.HasChanges = true
					m.ErrorMsg = ""
					m.SuccessMsg = ""
				}
			}
			return m, nil

		case "c":
			m.FocusCol = 3
			m.NewGroupName = ""
			m.ErrorMsg = ""
			m.SuccessMsg = ""
			return m, nil

		case "s":
			if m.HasChanges {
				m.SavePrompt = true
				m.ErrorMsg = ""
				m.SuccessMsg = ""
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *AgentGroupsModel) handleNav(up bool) {
	m.ErrorMsg = ""
	m.SuccessMsg = ""
	switch m.FocusCol {
	case 0:
		if up {
			if m.SelectedGroupIdx > 0 {
				m.SelectedGroupIdx--
				m.SelectedAgentIdx = 0
			}
		} else {
			if m.SelectedGroupIdx < len(m.Groups)-1 {
				m.SelectedGroupIdx++
				m.SelectedAgentIdx = 0
			}
		}
	case 1:
		if len(m.Groups) > 0 {
			agents := m.Groups[m.SelectedGroupIdx].AgentIDs
			if up {
				if m.SelectedAgentIdx > 0 {
					m.SelectedAgentIdx--
				}
			} else {
				if m.SelectedAgentIdx < len(agents)-1 {
					m.SelectedAgentIdx++
				}
			}
		}
	case 2:
		if up {
			if m.AvailableAgentIdx > 0 {
				m.AvailableAgentIdx--
			}
		} else {
			if m.AvailableAgentIdx < len(m.AvailableAgents)-1 {
				m.AvailableAgentIdx++
			}
		}
	}
}

type agentGroupsLayout struct {
	boxHeight   int
	showTabBar  bool
	showHelp    bool
	showSpacers bool
	helpText    string
	helpLines   int
}

func (m *AgentGroupsModel) calculateLayout() agentGroupsLayout {
	if m.Width == 0 || m.Height == 0 {
		return agentGroupsLayout{boxHeight: 1}
	}

	targetActiveHeight := m.Height - 3

	showTabBar := m.Width < 60
	showSpacers := m.Height >= 15
	showHelp := m.Height >= 15

	var helpCmds []string
	switch m.FocusCol {
	case 0:
		helpCmds = []string{"Tab/1-3 Switch", "↑/↓ Nav", "c New Group", "d Delete Group", "s Save", "Esc Exit", "q Quit", "Ctrl+C Quit"}
	case 1:
		helpCmds = []string{"Tab/1-3 Switch", "↑/↓ Nav", "Shift+↑/↓ Reorder", "d Remove Agent", "s Save", "Esc Exit", "q Quit", "Ctrl+C Quit"}
	case 2:
		helpCmds = []string{"Tab/1-3 Switch", "↑/↓ Nav", "Enter Add Agent", "s Save", "Esc Exit", "q Quit", "Ctrl+C Quit"}
	}

	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}

	var boxHeight int
	for {
		var helpLines int
		var helpText string
		if showHelp {
			helpText = renderCommandHelp(m.Styles, helpCmds, helpWidth)
			helpLines = strings.Count(helpText, "\n") + 1
		}

		overhead := 0
		if showTabBar {
			overhead += 2
		}
		if showSpacers {
			overhead += 1
		}
		overhead += 1 // statusMsg
		if showHelp {
			if showSpacers {
				overhead += 1
			}
			overhead += helpLines
		}
		boxHeight = targetActiveHeight - overhead - 2

		// If height constraints indicate small screen, or boxHeight is small, enforce hiding
		if (boxHeight < 5 || m.Height < 15) && (showHelp || showSpacers) {
			if showSpacers {
				showSpacers = false
				continue
			}
			if showHelp {
				showHelp = false
				continue
			}
		}

		if boxHeight >= 1 {
			break
		}

		// Fallback if needed
		if showSpacers {
			showSpacers = false
			continue
		}
		if showHelp {
			showHelp = false
			continue
		}
		boxHeight = 1
		break
	}

	var helpText string
	var helpLines int
	if showHelp {
		helpText = renderCommandHelp(m.Styles, helpCmds, helpWidth)
		helpLines = strings.Count(helpText, "\n") + 1
	}

	return agentGroupsLayout{
		boxHeight:   boxHeight,
		showTabBar:  showTabBar,
		showHelp:    showHelp,
		showSpacers: showSpacers,
		helpText:    helpText,
		helpLines:   helpLines,
	}
}

func (m *AgentGroupsModel) getViewports(boxHeight int) (int, bool, int, bool, int, bool) {
	// Panel 0: Groups List
	total0 := len(m.Groups)
	V_base0 := boxHeight - 4
	if V_base0 < 1 {
		V_base0 = 1
	}
	viewportHeight0 := V_base0
	showPagination0 := total0 > V_base0
	if showPagination0 {
		viewportHeight0 = V_base0 - 1
		if viewportHeight0 < 1 {
			viewportHeight0 = 1
		}
	}

	// Panel 1: Pipeline Agents
	var total1 int
	if m.SelectedGroupIdx >= 0 && m.SelectedGroupIdx < len(m.Groups) {
		total1 = len(m.Groups[m.SelectedGroupIdx].AgentIDs)
	}
	V_base1 := boxHeight - 5
	if V_base1 < 1 {
		V_base1 = 1
	}
	viewportHeight1 := V_base1
	showPagination1 := total1 > V_base1
	if showPagination1 {
		viewportHeight1 = V_base1 - 1
		if viewportHeight1 < 1 {
			viewportHeight1 = 1
		}
	}

	// Panel 2: Available Agents
	total2 := len(m.AvailableAgents)
	V_base2 := boxHeight - 4
	if V_base2 < 1 {
		V_base2 = 1
	}
	viewportHeight2 := V_base2
	showPagination2 := total2 > V_base2
	if showPagination2 {
		viewportHeight2 = V_base2 - 1
		if viewportHeight2 < 1 {
			viewportHeight2 = 1
		}
	}

	return viewportHeight0, showPagination0, viewportHeight1, showPagination1, viewportHeight2, showPagination2
}

func (m *AgentGroupsModel) ClampScrollOffsets() {
	layout := m.calculateLayout()
	boxHeight := layout.boxHeight

	viewportHeight0, _, viewportHeight1, _, viewportHeight2, _ := m.getViewports(boxHeight)

	// Panel 0: Groups List
	if len(m.Groups) <= viewportHeight0 {
		m.GroupScrollOffset = 0
	} else {
		if m.SelectedGroupIdx < m.GroupScrollOffset {
			m.GroupScrollOffset = m.SelectedGroupIdx
		} else if m.SelectedGroupIdx >= m.GroupScrollOffset+viewportHeight0 {
			m.GroupScrollOffset = m.SelectedGroupIdx - viewportHeight0 + 1
		}
		if m.GroupScrollOffset > len(m.Groups)-viewportHeight0 {
			m.GroupScrollOffset = len(m.Groups) - viewportHeight0
		}
		if m.GroupScrollOffset < 0 {
			m.GroupScrollOffset = 0
		}
	}

	// Panel 1: Pipeline Agents
	var pipelineLen int
	if m.SelectedGroupIdx >= 0 && m.SelectedGroupIdx < len(m.Groups) {
		pipelineLen = len(m.Groups[m.SelectedGroupIdx].AgentIDs)
	}
	if pipelineLen <= viewportHeight1 {
		m.PipelineScrollOffset = 0
	} else {
		if m.SelectedAgentIdx < m.PipelineScrollOffset {
			m.PipelineScrollOffset = m.SelectedAgentIdx
		} else if m.SelectedAgentIdx >= m.PipelineScrollOffset+viewportHeight1 {
			m.PipelineScrollOffset = m.SelectedAgentIdx - viewportHeight1 + 1
		}
		if m.PipelineScrollOffset > pipelineLen-viewportHeight1 {
			m.PipelineScrollOffset = pipelineLen - viewportHeight1
		}
		if m.PipelineScrollOffset < 0 {
			m.PipelineScrollOffset = 0
		}
	}

	// Panel 2: Available Agents
	if len(m.AvailableAgents) <= viewportHeight2 {
		m.AvailableScrollOffset = 0
	} else {
		if m.AvailableAgentIdx < m.AvailableScrollOffset {
			m.AvailableScrollOffset = m.AvailableAgentIdx
		} else if m.AvailableAgentIdx >= m.AvailableScrollOffset+viewportHeight2 {
			m.AvailableScrollOffset = m.AvailableAgentIdx - viewportHeight2 + 1
		}
		if m.AvailableScrollOffset > len(m.AvailableAgents)-viewportHeight2 {
			m.AvailableScrollOffset = len(m.AvailableAgents) - viewportHeight2
		}
		if m.AvailableScrollOffset < 0 {
			m.AvailableScrollOffset = 0
		}
	}
}

func (m *AgentGroupsModel) saveGroups(scope string) {
	var err error
	if scope == "project" {
		// Load raw global settings to know which groups are global-only.
		// Only save groups that are project-specific (new or overriding global) to the project file.
		// This prevents merged global groups from being duplicated into the project file.
		globalSettings, globalErr := config.LoadGlobalSettings()

		// Remove groups that are identical to global groups and not intentionally overridden.
		// Since the user edited m.Groups directly, all of m.Groups represents the desired final
		// merged state. For the project file we must only store groups that differ from or
		// supplement the global base.
		var filteredProjectGroups []config.AgentGroup
		if globalErr == nil {
			globalGroupMap := make(map[string]config.AgentGroup)
			for _, g := range globalSettings.AgentGroups {
				globalGroupMap[strings.ToLower(g.Name)] = g
			}
			for _, g := range m.Groups {
				key := strings.ToLower(g.Name)
				if globalGroup, exists := globalGroupMap[key]; exists {
					// Include in project only if it differs from global (user changed it)
					if !agentGroupsEqual(g, globalGroup) {
						filteredProjectGroups = append(filteredProjectGroups, g)
					}
					// else: global handles it; don't duplicate in project
				} else {
					// Project-only group — always save
					filteredProjectGroups = append(filteredProjectGroups, g)
				}
			}
		} else {
			// Could not load global; save all groups to project as fallback
			for _, g := range m.Groups {
				filteredProjectGroups = append(filteredProjectGroups, g)
			}
		}

		var s config.Settings
		s, err = config.LoadProjectSettings()
		if err == nil {
			s.AgentGroups = filteredProjectGroups
			err = config.SaveProjectSettings(s)
		}
	} else {
		// Global: save all currently shown groups as global groups.
		// Strip any groups that only exist because of project overrides by loading raw project
		// settings and excluding groups present only there.
		projectSettings, projectErr := config.LoadProjectSettings()
		projectOnlyNames := make(map[string]bool)
		if projectErr == nil {
			globalSettings, globalErr := config.LoadGlobalSettings()
			if globalErr == nil {
				globalGroupNames := make(map[string]bool)
				for _, g := range globalSettings.AgentGroups {
					globalGroupNames[strings.ToLower(g.Name)] = true
				}
				for _, g := range projectSettings.AgentGroups {
					key := strings.ToLower(g.Name)
					if !globalGroupNames[key] {
						projectOnlyNames[key] = true
					}
				}
			}
		}

		var globalGroups []config.AgentGroup
		for _, g := range m.Groups {
			if !projectOnlyNames[strings.ToLower(g.Name)] {
				globalGroups = append(globalGroups, g)
			}
		}

		var s config.Settings
		s, err = config.LoadGlobalSettings()
		if err == nil {
			s.AgentGroups = globalGroups
			err = config.SaveGlobalSettings(s)
		}
	}

	if err != nil {
		m.ErrorMsg = fmt.Sprintf("Failed to save groups: %v", err)
	} else {
		m.SuccessMsg = fmt.Sprintf("Saved groups to %s settings successfully.", scope)
		m.HasChanges = false
	}
}

// agentGroupsEqual returns true if two AgentGroups have the same name and agent IDs in the same order.
func agentGroupsEqual(a, b config.AgentGroup) bool {
	if strings.ToLower(a.Name) != strings.ToLower(b.Name) {
		return false
	}
	if len(a.AgentIDs) != len(b.AgentIDs) {
		return false
	}
	for i := range a.AgentIDs {
		if a.AgentIDs[i] != b.AgentIDs[i] {
			return false
		}
	}
	return true
}

func (m AgentGroupsModel) View() string {
	if m.Width == 0 || m.Height == 0 {
		return "Loading..."
	}

	layout := m.calculateLayout()
	boxHeight := layout.boxHeight
	showHelp := layout.showHelp
	showSpacers := layout.showSpacers
	helpText := layout.helpText

	viewportHeight0, showPagination0, viewportHeight1, showPagination1, viewportHeight2, showPagination2 := m.getViewports(boxHeight)

	dialogWidth := m.Width - 4
	if dialogWidth < 10 {
		dialogWidth = 10
	}

	// 1. Render Floating Dialogs
	if m.FocusCol == 3 {
		var lines []string
		if boxHeight >= 6 {
			lines = append(lines, m.Styles.DetailHeader.Render("CREATE NEW AGENT GROUP"))
			lines = append(lines, "")
			lines = append(lines, m.Styles.DetailLabel.Render("Enter group name:"))
			lines = append(lines, m.Styles.TableSelectedRow.Render(" "+m.NewGroupName+" "))
			lines = append(lines, "")
			if m.ErrorMsg != "" {
				lines = append(lines, m.Styles.ErrorText.Render(m.ErrorMsg))
				lines = append(lines, "")
			}
			lines = append(lines, renderCommandHelp(m.Styles, []string{"Enter Confirm", "Esc Cancel"}, dialogWidth))
		} else {
			// Compact layouts depending on available boxHeight
			if boxHeight >= 4 {
				lines = append(lines, m.Styles.DetailHeader.Render("CREATE GROUP: ")+m.Styles.TableSelectedRow.Render(" "+m.NewGroupName+" "))
				if m.ErrorMsg != "" {
					lines = append(lines, m.Styles.ErrorText.Render(m.ErrorMsg))
				} else {
					lines = append(lines, m.Styles.DetailLabel.Render("Enter name..."))
				}
				lines = append(lines, "")
				lines = append(lines, renderCommandHelp(m.Styles, []string{"Ent Confirm", "Esc Cancel"}, dialogWidth))
			} else if boxHeight >= 3 {
				lines = append(lines, m.Styles.DetailHeader.Render("CREATE: ")+m.Styles.TableSelectedRow.Render(" "+m.NewGroupName+" "))
				if m.ErrorMsg != "" {
					lines = append(lines, m.Styles.ErrorText.Render(m.ErrorMsg))
				} else {
					lines = append(lines, renderCommandHelp(m.Styles, []string{"Ent Confirm", "Esc Cancel"}, dialogWidth))
				}
			} else if boxHeight >= 2 {
				lines = append(lines, m.Styles.DetailHeader.Render("NAME: ")+m.Styles.TableSelectedRow.Render(" "+m.NewGroupName+" "))
				lines = append(lines, renderCommandHelp(m.Styles, []string{"Ent", "Esc"}, dialogWidth))
			} else { // boxHeight == 1
				lines = append(lines, m.Styles.DetailHeader.Render("NAME: ")+m.Styles.TableSelectedRow.Render(" "+m.NewGroupName+" ")+m.Styles.SubtleText.Render(" [Ent/Esc]"))
			}
		}

		// Ensure we don't return more lines than boxHeight
		if len(lines) > boxHeight {
			lines = lines[:boxHeight]
		}
		dialogContent := strings.Join(lines, "\n")
		dialogContent = truncateLines(dialogContent, boxHeight)

		return m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Height(boxHeight + 2).
			Render(dialogContent)
	}

	if m.SavePrompt {
		var lines []string
		if boxHeight >= 6 {
			lines = append(lines, m.Styles.DetailHeader.Render("SAVE AGENT GROUPS"))
			lines = append(lines, "")
			lines = append(lines, m.Styles.DetailLabel.Render("Select configuration scope to save groups to:"))
			lines = append(lines, "")
			lines = append(lines, m.Styles.SuccessText.Render(" (p) Save to Project Settings (.cyclestone/settings.yml)"))
			lines = append(lines, m.Styles.AccentText.Render(" (g) Save to Global Settings (~/.config/cyclestone/settings.yml)"))
			lines = append(lines, "")
			lines = append(lines, renderCommandHelp(m.Styles, []string{"p Project", "g Global", "Esc Cancel"}, dialogWidth))
		} else {
			// Compact layouts depending on available boxHeight
			if boxHeight >= 4 {
				lines = append(lines, m.Styles.DetailHeader.Render("SAVE TO:")+" "+m.Styles.SuccessText.Render("(p) Project")+" / "+m.Styles.AccentText.Render("(g) Global"))
				if m.ErrorMsg != "" {
					lines = append(lines, m.Styles.ErrorText.Render(m.ErrorMsg))
				} else {
					lines = append(lines, "")
				}
				lines = append(lines, renderCommandHelp(m.Styles, []string{"p Project", "g Global", "Esc Cancel"}, dialogWidth))
			} else if boxHeight >= 3 {
				lines = append(lines, m.Styles.DetailHeader.Render("SAVE:")+" "+m.Styles.SuccessText.Render("(p) Proj")+" / "+m.Styles.AccentText.Render("(g) Glob"))
				if m.ErrorMsg != "" {
					lines = append(lines, m.Styles.ErrorText.Render(m.ErrorMsg))
				} else {
					lines = append(lines, renderCommandHelp(m.Styles, []string{"p Proj", "g Glob", "Esc Esc"}, dialogWidth))
				}
			} else if boxHeight >= 2 {
				lines = append(lines, m.Styles.DetailHeader.Render("SAVE:")+" "+m.Styles.SuccessText.Render("p")+"/"+m.Styles.AccentText.Render("g")+" ("+m.Styles.HelpStyle.Render("Esc to cancel")+")")
				if m.ErrorMsg != "" {
					lines = append(lines, m.Styles.ErrorText.Render(m.ErrorMsg))
				}
			} else { // boxHeight == 1
				lines = append(lines, m.Styles.DetailHeader.Render("SAVE:")+" "+m.Styles.SuccessText.Render("p")+"/"+m.Styles.AccentText.Render("g")+" ("+m.Styles.HelpStyle.Render("Esc")+")")
			}
		}

		// Ensure we don't return more lines than boxHeight
		if len(lines) > boxHeight {
			lines = lines[:boxHeight]
		}
		dialogContent := strings.Join(lines, "\n")
		dialogContent = truncateLines(dialogContent, boxHeight)

		return m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Height(boxHeight + 2).
			Render(dialogContent)
	}

	// Compute panel width depending on layout mode
	var panelWidth int
	if m.Width >= 90 {
		panelWidth = (m.Width - 6) / 3
	} else if m.Width >= 60 {
		panelWidth = (m.Width - 5) / 2
	} else {
		panelWidth = m.Width - 4
	}

	// Panel 0: Groups List
	var sb0 strings.Builder
	sb0.WriteString(m.Styles.SectionTitle.Render("AGENT GROUPS") + "\n\n")
	start0 := m.GroupScrollOffset
	end0 := start0 + viewportHeight0
	if end0 > len(m.Groups) {
		end0 = len(m.Groups)
	}
	for i := start0; i < end0; i++ {
		g := m.Groups[i]
		prefix := "  "
		line := g.Name
		if i == m.SelectedGroupIdx {
			if m.FocusCol == 0 {
				prefix = m.Styles.WarningText.Render(m.Styles.GlyphPointer + " ")
				line = m.Styles.ListSelectedRow.Render(line)
			} else {
				prefix = m.Styles.AccentText.Render(m.Styles.GlyphBulletSubtle + " ")
				line = m.Styles.DetailValue.Bold(!m.Styles.NoBold).Render(line)
			}
		} else {
			line = m.Styles.DetailValue.Render(line)
		}
		sb0.WriteString(fmt.Sprintf("%s%s\n", prefix, line))
	}

	if showPagination0 {
		total0 := len(m.Groups)
		paginationText := m.Styles.HelpStyle.Render(fmt.Sprintf("Showing %d-%d of %d", start0+1, end0, total0))
		sb0.WriteString(fmt.Sprintf("  %s\n", paginationText))
	}

	// Panel 1: Pipeline Agents for selected group
	var sb1 strings.Builder
	if m.SelectedGroupIdx >= 0 && m.SelectedGroupIdx < len(m.Groups) {
		g := m.Groups[m.SelectedGroupIdx]
		sb1.WriteString(m.Styles.SectionTitle.Render("GROUP PIPELINE") + "\n")
		sb1.WriteString(m.Styles.SubtleText.Render(fmt.Sprintf("Pipeline: %s", g.Name)) + "\n\n")

		if len(g.AgentIDs) == 0 {
			placeholder := "No agents in pipeline.\nSelect an agent on the right\nand press Enter to add."
			placeholderLines := strings.Split(placeholder, "\n")
			for i, pl := range placeholderLines {
				if i < viewportHeight1 {
					sb1.WriteString(m.Styles.SubtleText.Render(pl) + "\n")
				}
			}
		} else {
			start1 := m.PipelineScrollOffset
			end1 := start1 + viewportHeight1
			if end1 > len(g.AgentIDs) {
				end1 = len(g.AgentIDs)
			}
			for i := start1; i < end1; i++ {
				id := g.AgentIDs[i]
				prefix := "  "
				missing := m.isAgentMissing(id)
				displayName := id
				if missing {
					displayName = fmt.Sprintf("%s %s [MISSING]", m.Styles.GlyphWarning, id)
				}

				var line string
				if missing {
					line = m.Styles.ErrorText.Render(displayName)
				} else {
					line = m.Styles.DetailValue.Render(displayName)
				}

				if i == m.SelectedAgentIdx {
					if m.FocusCol == 1 {
						prefix = m.Styles.WarningText.Render(m.Styles.GlyphPointer + " ")
						line = m.Styles.ListSelectedRow.Render(displayName)
					} else {
						prefix = m.Styles.AccentText.Render(m.Styles.GlyphBulletSubtle + " ")
					}
				}
				sb1.WriteString(fmt.Sprintf("%s%s\n", prefix, line))
			}

			if showPagination1 {
				total1 := len(g.AgentIDs)
				paginationText := m.Styles.HelpStyle.Render(fmt.Sprintf("Showing %d-%d of %d", start1+1, end1, total1))
				sb1.WriteString(fmt.Sprintf("  %s\n", paginationText))
			}
		}
	}

	// Panel 2: Available Agents list
	var sb2 strings.Builder
	sb2.WriteString(m.Styles.SectionTitle.Render("AVAILABLE AGENTS") + "\n\n")
	start2 := m.AvailableScrollOffset
	end2 := start2 + viewportHeight2
	if end2 > len(m.AvailableAgents) {
		end2 = len(m.AvailableAgents)
	}
	for i := start2; i < end2; i++ {
		a := m.AvailableAgents[i]
		prefix := "  "
		line := fmt.Sprintf("%s (%s)", a.ID, a.Name)
		if i == m.AvailableAgentIdx {
			if m.FocusCol == 2 {
				prefix = m.Styles.WarningText.Render(m.Styles.GlyphPointer + " ")
				line = m.Styles.ListSelectedRow.Render(line)
			} else {
				prefix = m.Styles.AccentText.Render(m.Styles.GlyphBulletSubtle + " ")
				line = m.Styles.DetailValue.Render(line)
			}
		} else {
			line = m.Styles.DetailValue.Render(line)
		}
		sb2.WriteString(fmt.Sprintf("%s%s\n", prefix, line))
	}

	if showPagination2 {
		total2 := len(m.AvailableAgents)
		paginationText := m.Styles.HelpStyle.Render(fmt.Sprintf("Showing %d-%d of %d", start2+1, end2, total2))
		sb2.WriteString(fmt.Sprintf("  %s\n", paginationText))
	}

	// Set border styles depending on focus
	var border0, border1, border2 lipgloss.Style
	if m.FocusCol == 0 {
		border0 = m.Styles.ActiveBorder
	} else {
		border0 = m.Styles.InactiveBorder
	}
	if m.FocusCol == 1 {
		border1 = m.Styles.ActiveBorder
	} else {
		border1 = m.Styles.InactiveBorder
	}
	if m.FocusCol == 2 {
		border2 = m.Styles.ActiveBorder
	} else {
		border2 = m.Styles.InactiveBorder
	}

	contentHeight := boxHeight - 2
	if contentHeight < 1 {
		contentHeight = 1
	}

	render0 := border0.Width(panelWidth).Height(boxHeight).Render(truncateLines(sb0.String(), contentHeight))
	render1 := border1.Width(panelWidth).Height(boxHeight).Render(truncateLines(sb1.String(), contentHeight))
	render2 := border2.Width(panelWidth).Height(boxHeight).Render(truncateLines(sb2.String(), contentHeight))

	var mainLayout string
	if m.Width >= 90 {
		// Three panels side-by-side
		mainLayout = lipgloss.JoinHorizontal(lipgloss.Top, render0, render1, render2)
	} else if m.Width >= 60 {
		// Two panels side-by-side depending on focus column
		if m.FocusCol == 2 {
			mainLayout = lipgloss.JoinHorizontal(lipgloss.Top, render1, render2)
		} else {
			mainLayout = lipgloss.JoinHorizontal(lipgloss.Top, render0, render1)
		}
	} else {
		// Single-column layout with tabbed/paginated headers
		tab1 := "1:Groups"
		tab2 := "2:Pipeline"
		tab3 := "3:Available"
		if m.Width < 45 {
			tab1 = "1:Grps"
			tab2 = "2:Pipe"
			tab3 = "3:Avail"
		}
		if m.Width < 30 {
			tab1 = "1"
			tab2 = "2"
			tab3 = "3"
		}
		if m.FocusCol == 0 {
			tab1 = m.Styles.TableSelectedRow.Render(" " + tab1 + " ")
			tab2 = m.Styles.SubtleText.Render(" " + tab2 + " ")
			tab3 = m.Styles.SubtleText.Render(" " + tab3 + " ")
		} else if m.FocusCol == 1 {
			tab1 = m.Styles.SubtleText.Render(" " + tab1 + " ")
			tab2 = m.Styles.TableSelectedRow.Render(" " + tab2 + " ")
			tab3 = m.Styles.SubtleText.Render(" " + tab3 + " ")
		} else {
			tab1 = m.Styles.SubtleText.Render(" " + tab1 + " ")
			tab2 = m.Styles.SubtleText.Render(" " + tab2 + " ")
			tab3 = m.Styles.TableSelectedRow.Render(" " + tab3 + " ")
		}
		tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tab1, " ", tab2, " ", tab3)

		var activeRender string
		if m.FocusCol == 0 {
			activeRender = render0
		} else if m.FocusCol == 1 {
			activeRender = render1
		} else {
			activeRender = render2
		}

		mainLayout = lipgloss.JoinVertical(lipgloss.Left, tabBar, "\n", activeRender)
	}

	// Status Line
	var statusMsg string
	if m.ErrorMsg != "" {
		statusMsg = m.Styles.RenderError(m.ErrorMsg)
	} else if m.SuccessMsg != "" {
		statusMsg = m.Styles.RenderSuccess(m.SuccessMsg)
	} else if m.HasChanges {
		statusMsg = m.Styles.RenderWarning("Unsaved changes! Press 's' to save.")
	} else {
		statusMsg = m.Styles.RenderInfo("Use Tab or 1-3 to switch focus between panels.")
	}

	// Help Text with responsive command mappings
	var renderedHelpText string
	if showHelp {
		renderedHelpText = helpText
	}

	// Build active view vertically
	var activeViewBuilder []string
	activeViewBuilder = append(activeViewBuilder, mainLayout)
	if showSpacers {
		activeViewBuilder = append(activeViewBuilder, "")
	}
	activeViewBuilder = append(activeViewBuilder, statusMsg)
	if showHelp {
		if showSpacers {
			activeViewBuilder = append(activeViewBuilder, "")
		}
		activeViewBuilder = append(activeViewBuilder, renderedHelpText)
	}

	return lipgloss.JoinVertical(lipgloss.Left, activeViewBuilder...)
}
