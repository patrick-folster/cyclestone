package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patrick-folster/cyclestone/internal/config"
)

type CreateScreenMode int

const (
	ModeCreateMilestone CreateScreenMode = iota
	ModeCycleNote
)

// CreateMilestoneMsg is sent when submitting the form to create a new milestone.
type CreateMilestoneMsg struct {
	ID                 string
	Title              string
	Goal               string
	AcceptanceCriteria []string
	Checks             []string
	RunnerType         string
	CreateBranch       bool
}

// CreateMilestoneModel handles the form for creating a new milestone.
type CreateMilestoneModel struct {
	Mode          CreateScreenMode
	RunMilestone  config.Milestone
	RunRunnerLLM  string
	RunRunnerMode string
	RunNoBranch   bool
	RunGroup      config.AgentGroup
	RunSingleID   string
	NextID        string
	TitleInput    textinput.Model
	GoalInput     textarea.Model
	Spinner       spinner.Model
	RunnerType    string
	DefaultLLM    string
	CreateBranch  bool
	Loading       bool
	Logs          []string
	FocusIndex    int
	Width         int
	Height        int
	Styles        Styles
	ErrorMsg      string
}

// NewCreateMilestoneModel instantiates the creation form model.
func NewCreateMilestoneModel(styles Styles) CreateMilestoneModel {
	titleInput := textinput.New()
	titleInput.Placeholder = "Milestone Title (optional, auto-generated if empty)"
	titleInput.CharLimit = 128
	titleInput.Width = 50
	titleInput.PlaceholderStyle = styles.SubtleText
	titleInput.Cursor.Style = styles.AccentText

	goalInput := textarea.New()
	goalInput.Placeholder = "Enter the description / goal of the milestone..."
	goalInput.CharLimit = 0
	goalInput.SetWidth(60)
	goalInput.SetHeight(8)
	goalInput.ShowLineNumbers = false
	goalInput.Cursor.Style = styles.AccentText
	goalInput.Focus()

	focusedStyle, blurredStyle := textarea.DefaultStyles()
	focusedStyle.Text = styles.FocusedInput
	focusedStyle.Placeholder = styles.SubtleText
	blurredStyle.Text = styles.BlurredInput
	blurredStyle.Placeholder = styles.SubtleText
	goalInput.FocusedStyle = focusedStyle
	goalInput.BlurredStyle = blurredStyle

	s := spinner.New()
	s.Spinner = spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    80 * time.Millisecond,
	}
	s.Style = styles.Spinner

	return CreateMilestoneModel{
		TitleInput:   titleInput,
		GoalInput:    goalInput,
		Spinner:      s,
		RunnerType:   normalizeMilestoneRunner(""),
		DefaultLLM:   "",
		CreateBranch: false,
		FocusIndex:   0,
		Styles:       styles,
	}
}

// Init triggers cursor blink.
func (m CreateMilestoneModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, textarea.Blink)
}

// Update handles input fields, tab navigation and form submission/cancellation.
func (m CreateMilestoneModel) Update(msg tea.Msg) (CreateMilestoneModel, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		m.recalcHeights()
		return m, nil

	case spinner.TickMsg:
		var sCmd tea.Cmd
		m.Spinner, sCmd = m.Spinner.Update(msg)
		return m, sCmd

	case tea.MouseMsg:
		if m.FocusIndex == 0 {
			if msg.Type == tea.MouseWheelUp {
				m.GoalInput.CursorUp()
				m.GoalInput, cmd = m.GoalInput.Update(nil)
				return m, cmd
			} else if msg.Type == tea.MouseWheelDown {
				m.GoalInput.CursorDown()
				m.GoalInput, cmd = m.GoalInput.Update(nil)
				return m, cmd
			}
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.ErrorMsg = ""
			return m, func() tea.Msg {
				if m.Mode == ModeCycleNote {
					return ChangeScreenMsg{
						Screen: ScreenDetails,
						Data:   m.RunMilestone,
					}
				}
				return ChangeScreenMsg{
					Screen: ScreenDashboard,
				}
			}

		case "tab":
			if m.Mode == ModeCycleNote {
				if m.FocusIndex == 0 {
					m.FocusIndex = 4
				} else if m.FocusIndex == 4 {
					m.FocusIndex = 5
				} else {
					m.FocusIndex = 0
				}
			} else {
				m.FocusIndex = (m.FocusIndex + 1) % 6 // Goal (0), Title (1), Runner (2), Git Branch (3), Submit (4), Cancel (5)
			}
			return m, m.updateFocus()

		case "shift+tab":
			if m.Mode == ModeCycleNote {
				if m.FocusIndex == 0 {
					m.FocusIndex = 5
				} else if m.FocusIndex == 4 {
					m.FocusIndex = 0
				} else {
					m.FocusIndex = 4
				}
			} else {
				m.FocusIndex = (m.FocusIndex - 1 + 6) % 6
			}
			return m, m.updateFocus()

		case "pgdn", "ctrl+d":
			if m.FocusIndex == 0 {
				h := m.GoalInput.Height()
				for i := 0; i < h; i++ {
					m.GoalInput.CursorDown()
				}
				m.GoalInput, cmd = m.GoalInput.Update(nil)
				return m, cmd
			}

		case "pgup", "ctrl+u":
			if m.FocusIndex == 0 {
				h := m.GoalInput.Height()
				for i := 0; i < h; i++ {
					m.GoalInput.CursorUp()
				}
				m.GoalInput, cmd = m.GoalInput.Update(nil)
				return m, cmd
			}

		case "down":
			// Navigate to next field using Down arrow, EXCEPT when inside the textarea (where Down moves cursor)
			if m.FocusIndex != 0 {
				if m.Mode == ModeCycleNote {
					if m.FocusIndex == 4 {
						m.FocusIndex = 5
					} else {
						m.FocusIndex = 4
					}
				} else {
					m.FocusIndex = (m.FocusIndex + 1) % 6
				}
				return m, m.updateFocus()
			}

		case "up":
			// Navigate to previous field using Up arrow, EXCEPT when inside the textarea (where Up moves cursor)
			if m.FocusIndex != 0 {
				if m.Mode == ModeCycleNote {
					if m.FocusIndex == 5 {
						m.FocusIndex = 4
					} else {
						m.FocusIndex = 5
					}
				} else {
					m.FocusIndex = (m.FocusIndex - 1 + 6) % 6
				}
				return m, m.updateFocus()
			}

		case "left", "h":
			if m.FocusIndex == 2 {
				opts := getCreateRunnerOptions(m.DefaultLLM)
				curIdx := -1
				for i, opt := range opts {
					if opt == m.RunnerType {
						curIdx = i
						break
					}
				}
				if curIdx == -1 {
					curIdx = 0
				}
				newIdx := (curIdx - 1 + len(opts)) % len(opts)
				m.RunnerType = opts[newIdx]
				return m, nil
			} else if m.FocusIndex == 3 {
				m.CreateBranch = !m.CreateBranch
				return m, nil
			}

		case "right", "l":
			if m.FocusIndex == 2 {
				opts := getCreateRunnerOptions(m.DefaultLLM)
				curIdx := -1
				for i, opt := range opts {
					if opt == m.RunnerType {
						curIdx = i
						break
					}
				}
				if curIdx == -1 {
					curIdx = 0
				}
				newIdx := (curIdx + 1) % len(opts)
				m.RunnerType = opts[newIdx]
				return m, nil
			} else if m.FocusIndex == 3 {
				m.CreateBranch = !m.CreateBranch
				return m, nil
			}

		case "enter":
			if m.FocusIndex == 4 {
				return m.handleSubmit()
			} else if m.FocusIndex == 5 {
				m.ErrorMsg = ""
				return m, func() tea.Msg {
					if m.Mode == ModeCycleNote {
						return ChangeScreenMsg{
							Screen: ScreenDetails,
							Data:   m.RunMilestone,
						}
					}
					return ChangeScreenMsg{
						Screen: ScreenDashboard,
					}
				}
			} else if m.FocusIndex == 1 {
				m.FocusIndex = 2
				return m, m.updateFocus()
			} else if m.FocusIndex == 2 {
				m.FocusIndex = 3
				return m, m.updateFocus()
			} else if m.FocusIndex == 3 {
				m.FocusIndex = 4
				return m, m.updateFocus()
			}
			// Let enter pass to textarea when focused on it (FocusIndex == 0)
		}
	}

	// Update the active input if it's an input field
	if m.FocusIndex == 0 {
		m.GoalInput, cmd = m.GoalInput.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.FocusIndex == 1 {
		m.TitleInput, cmd = m.TitleInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	m.recalcHeights()
	return m, tea.Batch(cmds...)
}

func (m *CreateMilestoneModel) updateFocus() tea.Cmd {
	var cmds []tea.Cmd
	if m.FocusIndex == 0 {
		cmds = append(cmds, m.GoalInput.Focus())
		m.TitleInput.Blur()
	} else if m.FocusIndex == 1 {
		m.GoalInput.Blur()
		cmds = append(cmds, m.TitleInput.Focus())
	} else {
		m.TitleInput.Blur()
		m.GoalInput.Blur()
	}
	return tea.Batch(cmds...)
}

func (m CreateMilestoneModel) handleSubmit() (CreateMilestoneModel, tea.Cmd) {
	if m.Mode == ModeCycleNote {
		note := m.GoalInput.Value()
		m.ErrorMsg = ""
		return m, func() tea.Msg {
			return ChangeScreenMsg{
				Screen: ScreenPreflight,
				Data: StartCycleMsg{
					Milestone:      m.RunMilestone,
					SingleAgentID:  m.RunSingleID,
					RunnerLLM:      m.RunRunnerLLM,
					RunnerMode:     m.RunRunnerMode,
					NoBranchChange: m.RunNoBranch,
					Group:          m.RunGroup,
					Note:           note,
				},
			}
		}
	}

	title := strings.TrimSpace(m.TitleInput.Value())
	goal := strings.TrimSpace(m.GoalInput.Value())

	if goal == "" {
		m.ErrorMsg = "Goal/Description cannot be empty."
		m.recalcHeights()
		return m, nil
	}

	// Auto-generate title if not set
	if title == "" {
		firstLine := strings.Split(goal, "\n")[0]
		firstLine = cleanAutoTitle(firstLine)
		if len(firstLine) > 50 {
			title = firstLine[:50] + "..."
		} else if firstLine != "" {
			title = firstLine
		} else {
			title = "Milestone " + m.NextID
		}
	}

	finalID := m.NextID
	slug := slugifyTitle(title)
	if slug != "" {
		finalID = fmt.Sprintf("%s-%s", m.NextID, slug)
	}

	m.ErrorMsg = ""
	return m, func() tea.Msg {
		return CreateMilestoneMsg{
			ID:                 finalID,
			Title:              title,
			Goal:               goal,
			AcceptanceCriteria: nil,
			Checks:             nil,
			RunnerType:         m.RunnerType,
			CreateBranch:       m.CreateBranch,
		}
	}
}

// View draws the creation form layout.
func (m CreateMilestoneModel) View() string {
	(&m).recalcHeights()
	if m.Loading {
		var sb strings.Builder
		sb.WriteString(m.Styles.DetailHeader.Render(fmt.Sprintf("CREATING MILESTONE %s USING %s", m.NextID, strings.ToUpper(m.RunnerType))) + "\n\n")
		sb.WriteString(fmt.Sprintf("%s %s\n\n", m.Spinner.View(), m.Styles.DetailValue.Render("Generating milestone specification, please wait...")))

		// Show the last 10 lines of logs
		logStart := len(m.Logs) - 10
		if logStart < 0 {
			logStart = 0
		}
		for i := logStart; i < len(m.Logs); i++ {
			sb.WriteString(m.Logs[i] + "\n")
		}

		var rootOverhead = 3
		boxHeight := m.Height - rootOverhead - 2
		if boxHeight < 10 {
			boxHeight = 10
		}

		return m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Height(boxHeight).
			Render(truncateLines(sb.String(), boxHeight))
	}

	if m.Mode == ModeCycleNote {
		var sb strings.Builder
		helpWidth := m.Width - 4
		if helpWidth < 10 {
			helpWidth = 10
		}
		var spacing = "\n\n"
		if m.Height < 22 {
			spacing = "\n"
		}

		sb.WriteString(m.Styles.DetailHeader.Render(fmt.Sprintf("ADD OPTIONAL CYCLE NOTE / COMMENT (Milestone: %s)", m.RunMilestone.ID)) + "\n" + spacing)

		if m.ErrorMsg != "" {
			sb.WriteString(m.Styles.RenderError(m.ErrorMsg) + "\n" + spacing)
		}

		// Textarea Block
		var labelStyle lipgloss.Style
		if m.FocusIndex == 0 {
			labelStyle = m.Styles.DetailLabel.Underline(true)
		} else {
			labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
		}
		sb.WriteString(labelStyle.Render("Optional Cycle Note / Comment") + "\n")
		sb.WriteString(m.GoalInput.View() + spacing)

		// Confirm Buttons Block
		var submitBtn, cancelBtn string
		if m.FocusIndex == 4 {
			submitBtn = m.Styles.TableSelectedRow.Render(" [ Submit ] ")
		} else {
			submitBtn = m.Styles.SuccessText.Render(" [ Submit ] ")
		}

		if m.FocusIndex == 5 {
			cancelBtn = m.Styles.TableSelectedRow.Render(" [ Cancel ] ")
		} else {
			cancelBtn = m.Styles.HelpStyle.Render(" [ Cancel ] ")
		}
		sb.WriteString(fmt.Sprintf("%s    %s\n\n", submitBtn, cancelBtn))

		helpText := renderCommandHelp(m.Styles, []string{"Tab Focus", "Shift+Tab Back", "Enter Select/Confirm", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth)
		sb.WriteString(helpText)

		var rootOverhead = 3
		boxHeight := m.Height - rootOverhead - 2
		if boxHeight < 2 {
			boxHeight = 2
		}

		formBox := m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Height(boxHeight).
			Render(truncateLines(sb.String(), boxHeight))

		return formBox
	}

	var sb strings.Builder

	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}

	var spacing = "\n\n"
	if m.Height < 22 {
		spacing = "\n"
	}

	useWizard := m.Height < 18

	if useWizard {
		stepNum := m.FocusIndex + 1
		if stepNum > 5 {
			stepNum = 5
		}
		sb.WriteString(m.Styles.DetailHeader.Render(fmt.Sprintf("CREATE MILESTONE %s (Step %d/5)", m.NextID, stepNum)) + "\n" + spacing)

		if m.ErrorMsg != "" {
			sb.WriteString(m.Styles.RenderError(m.ErrorMsg) + "\n" + spacing)
		}

		switch m.FocusIndex {
		case 0:
			// Step 1: Goal
			sb.WriteString(m.Styles.DetailLabel.Render("Goal / Description *:") + "\n")
			sb.WriteString(m.GoalInput.View() + "\n")
			sb.WriteString(renderCommandHelp(m.Styles, []string{"Tab Next", "Esc Cancel", "Ctrl+C Cancel"}, helpWidth))

		case 1:
			// Step 2: Title
			sb.WriteString(m.Styles.DetailLabel.Render("Title (optional):") + "\n")
			m.TitleInput.TextStyle = m.Styles.FocusedInput
			sb.WriteString(m.TitleInput.View() + "\n")
			sb.WriteString(renderCommandHelp(m.Styles, []string{"Tab Next", "Esc Cancel", "Ctrl+C Cancel"}, helpWidth))

		case 2:
			// Step 3: Runner
			sb.WriteString(m.Styles.DetailLabel.Render("Runner Selection:") + "\n")
			opts := getCreateRunnerOptions(m.DefaultLLM)
			var renderedOpts []string
			for _, opt := range opts {
				display := opt
				if opt == "ollama-codex" {
					display = "ollama via codex"
				}
				if m.RunnerType == opt {
					renderedOpts = append(renderedOpts, m.Styles.SuccessText.Render(fmt.Sprintf("(•) %s", display)))
				} else {
					renderedOpts = append(renderedOpts, m.Styles.HelpStyle.Render(fmt.Sprintf("( ) %s", display)))
				}
			}
			sb.WriteString(strings.Join(renderedOpts, "  ") + "\n")
			sb.WriteString(renderCommandHelp(m.Styles, []string{"Left/Right Choose", "h/l Choose", "Tab Next", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth))

		case 3:
			// Step 4: Create Git Branch
			sb.WriteString(m.Styles.DetailLabel.Render("Create new Git branch for milestone?") + "\n")
			settings := config.LoadMergedSettings()
			prefix := settings.DefaultGitBranchPrefix
			if prefix == "" {
				prefix = "cyclestone/milestones/"
			}
			var yesOpt, noOpt string
			if m.CreateBranch {
				yesOpt = m.Styles.SuccessText.Render("(•) Yes (create branch: " + prefix + m.NextID + "-...)")
				noOpt = m.Styles.HelpStyle.Render("( ) No (stay on current branch)")
			} else {
				yesOpt = m.Styles.HelpStyle.Render("( ) Yes (create branch: " + prefix + m.NextID + "-...)")
				noOpt = m.Styles.SuccessText.Render("(•) No (stay on current branch)")
			}
			sb.WriteString(fmt.Sprintf("%s    %s\n", yesOpt, noOpt))
			sb.WriteString(renderCommandHelp(m.Styles, []string{"Left/Right Choose", "h/l Choose", "Tab Next", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth))

		case 4, 5:
			// Step 5: Confirm (Submit / Cancel)
			sb.WriteString(m.Styles.DetailValue.Render("Ready to create milestone?") + "\n\n")
			var submitBtn, cancelBtn string
			if m.FocusIndex == 4 {
				submitBtn = m.Styles.TableSelectedRow.Render(" [ Submit ] ")
			} else {
				submitBtn = m.Styles.SuccessText.Render(" [ Submit ] ")
			}

			if m.FocusIndex == 5 {
				cancelBtn = m.Styles.TableSelectedRow.Render(" [ Cancel ] ")
			} else {
				cancelBtn = m.Styles.HelpStyle.Render(" [ Cancel ] ")
			}
			sb.WriteString(fmt.Sprintf("%s    %s\n", submitBtn, cancelBtn))
			sb.WriteString(renderCommandHelp(m.Styles, []string{"Tab Toggle", "Enter Confirm", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth))
		}
	} else {
		sb.WriteString(m.Styles.DetailHeader.Render(fmt.Sprintf("CREATE NEW MILESTONE (ID: %s)", m.NextID)) + "\n" + spacing)

		if m.ErrorMsg != "" {
			sb.WriteString(m.Styles.RenderError(m.ErrorMsg) + "\n" + spacing)
		}

		type blockItem struct {
			focusIndices []int
			content      string
		}
		var allBlocks []blockItem

		// Block 0: Goal Input
		{
			var blockSb strings.Builder
			var labelStyle lipgloss.Style
			if m.FocusIndex == 0 {
				labelStyle = m.Styles.DetailLabel.Underline(true)
			} else {
				labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
			}
			if m.Height < 20 {
				blockSb.WriteString(labelStyle.Render("Goal *") + "\n")
			} else {
				blockSb.WriteString(labelStyle.Render("Description / Goal *") + "\n")
			}
			blockSb.WriteString(m.GoalInput.View())
			allBlocks = append(allBlocks, blockItem{focusIndices: []int{0}, content: blockSb.String()})
		}

		// Block 1: Title Input
		{
			var blockSb strings.Builder
			var labelStyle lipgloss.Style
			if m.FocusIndex == 1 {
				labelStyle = m.Styles.DetailLabel.Underline(true)
			} else {
				labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
			}

			if m.Height < 20 {
				if m.FocusIndex == 1 {
					m.TitleInput.TextStyle = m.Styles.FocusedInput
				} else {
					m.TitleInput.TextStyle = m.Styles.BlurredInput
				}
				blockSb.WriteString(labelStyle.Render("Title:") + " " + m.TitleInput.View())
			} else {
				blockSb.WriteString(labelStyle.Render("Title (optional)") + "\n")
				if m.FocusIndex == 1 {
					m.TitleInput.TextStyle = m.Styles.FocusedInput
				} else {
					m.TitleInput.TextStyle = m.Styles.BlurredInput
				}
				blockSb.WriteString(m.TitleInput.View())
			}
			allBlocks = append(allBlocks, blockItem{focusIndices: []int{1}, content: blockSb.String()})
		}

		// Block 2: Runner Selection
		{
			var blockSb strings.Builder
			var labelStyle lipgloss.Style
			if m.FocusIndex == 2 {
				labelStyle = m.Styles.DetailLabel.Underline(true)
			} else {
				labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
			}

			opts := getCreateRunnerOptions(m.DefaultLLM)
			var renderedOpts []string
			for _, opt := range opts {
				display := opt
				if opt == "ollama-codex" {
					display = "ollama via codex"
				}
				if m.RunnerType == opt {
					renderedOpts = append(renderedOpts, m.Styles.SuccessText.Render(fmt.Sprintf("(•) %s", display)))
				} else {
					renderedOpts = append(renderedOpts, m.Styles.HelpStyle.Render(fmt.Sprintf("( ) %s", display)))
				}
			}

			if m.Height < 20 {
				blockSb.WriteString(labelStyle.Render("Runner:") + " " + strings.Join(renderedOpts, "  "))
			} else {
				blockSb.WriteString(labelStyle.Render("Runner Selection (Left/Right or H/L to choose)") + "\n")
				blockSb.WriteString(strings.Join(renderedOpts, "  "))
			}
			allBlocks = append(allBlocks, blockItem{focusIndices: []int{2}, content: blockSb.String()})
		}

		// Block 3: Create Git Branch Selection
		{
			var blockSb strings.Builder
			var labelStyle lipgloss.Style
			if m.FocusIndex == 3 {
				labelStyle = m.Styles.DetailLabel.Underline(true)
			} else {
				labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
			}

			settings := config.LoadMergedSettings()
			prefix := settings.DefaultGitBranchPrefix
			if prefix == "" {
				prefix = "cyclestone/milestones/"
			}
			var yesOpt, noOpt string
			if m.CreateBranch {
				yesOpt = m.Styles.SuccessText.Render("(•) Yes (create branch: " + prefix + m.NextID + "-...)")
				noOpt = m.Styles.HelpStyle.Render("( ) No (stay on current branch)")
			} else {
				yesOpt = m.Styles.HelpStyle.Render("( ) Yes (create branch: " + prefix + m.NextID + "-...)")
				noOpt = m.Styles.SuccessText.Render("(•) No (stay on current branch)")
			}

			if m.Height < 20 {
				blockSb.WriteString(labelStyle.Render("Git Branch:") + " " + fmt.Sprintf("%s    %s", yesOpt, noOpt))
			} else {
				blockSb.WriteString(labelStyle.Render("Create Milestone Git Branch (Left/Right or H/L to choose)") + "\n")
				blockSb.WriteString(fmt.Sprintf("%s    %s", yesOpt, noOpt))
			}
			allBlocks = append(allBlocks, blockItem{focusIndices: []int{3}, content: blockSb.String()})
		}

		// Block 4: Confirm Buttons
		{
			var blockSb strings.Builder
			var submitBtn, cancelBtn string
			if m.FocusIndex == 4 {
				submitBtn = m.Styles.TableSelectedRow.Render(" [ Submit ] ")
			} else {
				submitBtn = m.Styles.SuccessText.Render(" [ Submit ] ")
			}

			if m.FocusIndex == 5 {
				cancelBtn = m.Styles.TableSelectedRow.Render(" [ Cancel ] ")
			} else {
				cancelBtn = m.Styles.HelpStyle.Render(" [ Cancel ] ")
			}
			blockSb.WriteString(fmt.Sprintf("%s    %s", submitBtn, cancelBtn))
			allBlocks = append(allBlocks, blockItem{focusIndices: []int{4, 5}, content: blockSb.String()})
		}

		focusedBlockIdx := 0
		for idx, blk := range allBlocks {
			for _, fIdx := range blk.focusIndices {
				if fIdx == m.FocusIndex {
					focusedBlockIdx = idx
					break
				}
			}
		}

		var rootOverhead = 3
		boxHeight := m.Height - rootOverhead - 2
		if boxHeight < 2 {
			boxHeight = 2
		}

		var helpText string
		if m.Height < 20 {
			helpText = renderCommandHelp(m.Styles, []string{"Tab Focus", "h/l Change", "Enter Submit", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth)
		} else {
			helpText = renderCommandHelp(m.Styles, []string{"Tab Focus", "Shift+Tab Back", "Left/Right Change", "Enter Select/Confirm", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth)
		}
		helpLines := strings.Count(helpText, "\n") + 1

		spacingLen := len(spacing)
		nonBlockLines := 2 + spacingLen // Header + spacing
		if m.ErrorMsg != "" {
			nonBlockLines += 1 + spacingLen
		}
		nonBlockLines += 1 + helpLines // newline + helpText height

		blocksCapacity := boxHeight - nonBlockLines
		if blocksCapacity < 1 {
			blocksCapacity = 1
		}

		startIdx := focusedBlockIdx
		endIdx := focusedBlockIdx
		currentHeight := strings.Count(allBlocks[focusedBlockIdx].content, "\n") + 1

		for {
			expanded := false
			if startIdx > 0 {
				h := strings.Count(allBlocks[startIdx-1].content, "\n") + spacingLen
				if currentHeight+h <= blocksCapacity {
					startIdx--
					currentHeight += h
					expanded = true
				}
			}
			if endIdx < len(allBlocks)-1 {
				h := strings.Count(allBlocks[endIdx+1].content, "\n") + spacingLen
				if currentHeight+h <= blocksCapacity {
					endIdx++
					currentHeight += h
					expanded = true
				}
			}
			if !expanded {
				break
			}
		}

		var visibleBlocks []string
		for i := startIdx; i <= endIdx; i++ {
			visibleBlocks = append(visibleBlocks, allBlocks[i].content)
		}
		sb.WriteString(strings.Join(visibleBlocks, spacing) + "\n")

		sb.WriteString(helpText)
	}

	var rootOverhead = 3
	boxHeight := m.Height - rootOverhead - 2
	if boxHeight < 2 {
		boxHeight = 2
	}

	formBox := m.Styles.ActiveBorder.
		Width(m.Width - 4).
		Height(boxHeight).
		Render(truncateLines(sb.String(), boxHeight))

	return formBox
}

// cleanAutoTitle strips leading politeness/filler phrases from a goal first
// line and capitalises the first letter so the auto-generated milestone title
// is concise and professional rather than echoing raw prose like "Please create ...".
func cleanAutoTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// Repeatedly strip leading politeness / filler phrases.
	fillerPrefixes := []string{
		"please", "kindly", "could you", "would you",
		"can you", "will you", "i need", "i want", "we need",
		"we want", "need to", "need", "want to", "want", "to",
	}
	for {
		lower := strings.ToLower(s)
		stripped := false
		for _, prefix := range fillerPrefixes {
			if !strings.HasPrefix(lower, prefix) {
				continue
			}
			rest := s[len(prefix):]
			// Require a word boundary: the character immediately after the
			// prefix must be a separator or the end of the string, otherwise
			// the prefix is part of a longer word (e.g. "to" in "token").
			if len(rest) > 0 {
				c := rest[0]
				if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
					continue
				}
			}
			rest = strings.TrimLeft(rest, " ,:;-.!?")
			if rest != "" {
				s = rest
				stripped = true
				break
			}
		}
		if !stripped {
			break
		}
	}

	// Trim trailing filler so titles like "create test milestone without any changes"
	// do not end with noise.
	trailingFiller := []string{
		"without any changes", "without changes", "if possible",
		"when you can", "thanks", "thank you",
	}
	for _, suffix := range trailingFiller {
		lower := strings.ToLower(s)
		if strings.HasSuffix(lower, suffix) {
			s = strings.TrimSpace(s[:len(s)-len(suffix)])
			s = strings.TrimRight(s, " ,:;-.!?")
		}
	}

	// Capitalise the first letter.
	if r := []rune(s); len(r) > 0 && r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 32
		s = string(r)
	}

	return s
}

func slugifyTitle(title string) string {
	stopWords := map[string]bool{
		// Articles
		"a": true, "an": true, "the": true,
		// Conjunctions
		"and": true, "or": true, "but": true,
		// Prepositions
		"for": true, "to": true, "in": true, "on": true, "at": true, "by": true,
		"of": true, "with": true, "without": true, "as": true, "from": true, "into": true,
		"about": true, "than": true,
		// Be-verbs / auxiliaries
		"is": true, "are": true, "be": true, "been": true,
		// Do-verbs
		"do": true, "does": true, "did": true,
		// Pronouns
		"it": true, "its": true, "that": true, "this": true,
		"these": true, "those": true,
		"i": true, "you": true, "we": true, "they": true,
		"me": true, "my": true, "our": true, "your": true,
		"their": true, "us": true, "them": true,
		// Politeness / filler words
		"please": true, "kindly": true,
		// Modal verbs
		"could": true, "would": true, "should": true, "will": true,
		"shall": true, "may": true, "might": true, "must": true, "can": true,
		// Filler / qualifiers
		"just": true, "also": true, "then": true, "some": true, "any": true,
		"too": true, "very": true,
	}

	var clean []string
	var currentWord strings.Builder

	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			currentWord.WriteRune(r)
		} else {
			if currentWord.Len() > 0 {
				word := strings.ToLower(currentWord.String())
				if !stopWords[word] {
					clean = append(clean, word)
				}
				currentWord.Reset()
			}
		}
	}
	if currentWord.Len() > 0 {
		word := strings.ToLower(currentWord.String())
		if !stopWords[word] {
			clean = append(clean, word)
		}
	}

	if len(clean) > 4 {
		clean = clean[:4]
	}

	if len(clean) == 0 {
		return ""
	}
	return strings.Join(clean, "-")
}

func getCreateRunnerOptions(_ string) []string {
	return getMilestoneRunnerOptions()
}

func (m *CreateMilestoneModel) recalcHeights() {
	var spacingLen = 2
	if m.Height < 22 {
		spacingLen = 1
	}

	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}

	var helpText string
	if m.Height < 18 { // useWizard
		helpText = renderCommandHelp(m.Styles, []string{"Tab Next", "Esc Cancel", "Ctrl+C Cancel"}, helpWidth)
	} else if m.Height < 20 {
		helpText = renderCommandHelp(m.Styles, []string{"Tab Focus", "h/l Change", "Enter Submit", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth)
	} else {
		helpText = renderCommandHelp(m.Styles, []string{"Tab Focus", "Shift+Tab Back", "Left/Right Change", "Enter Select/Confirm", "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth)
	}
	helpLines := strings.Count(helpText, "\n") + 1

	var rootOverhead = 3
	boxHeight := m.Height - rootOverhead - 2
	if boxHeight < 2 {
		boxHeight = 2
	}

	if m.Mode == ModeCycleNote {
		var nonTextAreaLines int
		if m.Height < 22 {
			nonTextAreaLines = 3 + 2 + 1 + 3 + helpLines
		} else {
			nonTextAreaLines = 4 + 2 + 2 + 3 + helpLines
		}
		if m.ErrorMsg != "" {
			nonTextAreaLines += 1 + spacingLen
		}
		h := boxHeight - nonTextAreaLines
		if h < 2 {
			h = 2
		}
		m.GoalInput.SetHeight(h)
	} else if m.Height < 18 { // useWizard
		nonTextAreaLines := 3 + spacingLen + helpLines
		if m.ErrorMsg != "" {
			nonTextAreaLines += 1 + spacingLen
		}
		h := boxHeight - nonTextAreaLines
		if h < 2 {
			h = 2
		}
		m.GoalInput.SetHeight(h)
	} else {
		nonBlockLines := 2 + spacingLen // Header + spacing
		if m.ErrorMsg != "" {
			nonBlockLines += 1 + spacingLen
		}
		nonBlockLines += 1 + helpLines // newline + helpText height

		blocksCapacity := boxHeight - nonBlockLines
		h := blocksCapacity
		if h < 2 {
			h = 2
		}
		m.GoalInput.SetHeight(h)
	}

	// Dynamically adjust input widths to prevent overflow
	inputWidth := m.Width - 8
	if inputWidth < 15 {
		inputWidth = 15
	}
	m.GoalInput.SetWidth(inputWidth)

	titleInputWidth := m.Width - 8
	if m.Height < 20 {
		titleInputWidth = m.Width - 16
	}
	if titleInputWidth < 15 {
		titleInputWidth = 15
	}
	m.TitleInput.Width = titleInputWidth
}
