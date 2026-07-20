package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patrick-folster/cyclestone/internal/config"
)

// CreatePlanMsg requests persistence of the currently completed Plan form.
type CreatePlanMsg struct {
	ID        string
	Title     string
	Objective string
}

// ShowDeletePlanMsg opens a confirmation screen for one selected Plan.
type ShowDeletePlanMsg struct {
	Plan         config.Plan
	ReturnScreen Screen
}

// DeletePlanMsg requests deletion after the user entered the exact Plan ID.
type DeletePlanMsg struct {
	Plan         config.Plan
	ReturnScreen Screen
}

// CreatePlanModel is the focused, planning-only Plan creation form.
type CreatePlanModel struct {
	NextID         string
	IDInput        textinput.Model
	TitleInput     textinput.Model
	ObjectiveInput textarea.Model
	Form           FormModel
	FocusIndex     int
	Width          int
	Height         int
	Styles         Styles
	ErrorMsg       string
}

// NewCreatePlanModel initializes all required Plan inputs.
func NewCreatePlanModel(styles Styles) CreatePlanModel {
	newInput := func(placeholder string, limit int) textinput.Model {
		input := textinput.New()
		input.Placeholder = placeholder
		input.CharLimit = limit
		input.Width = 50
		input.PlaceholderStyle = styles.SubtleText
		input.Cursor.Style = styles.AccentText
		return input
	}

	obj := textarea.New()
	obj.Placeholder = "Enter the description / objective of the plan..."
	obj.CharLimit = 0
	obj.SetWidth(60)
	obj.SetHeight(8)
	obj.ShowLineNumbers = false
	obj.Cursor.Style = styles.AccentText
	obj.Focus()

	focusedStyle, blurredStyle := textarea.DefaultStyles()
	focusedStyle.Text = styles.FocusedInput
	focusedStyle.Placeholder = styles.SubtleText
	blurredStyle.Text = styles.BlurredInput
	blurredStyle.Placeholder = styles.SubtleText
	obj.FocusedStyle = focusedStyle
	obj.BlurredStyle = blurredStyle

	titleInput := newInput("Plan title (optional, auto-generated if empty)", 160)
	idInput := newInput("Plan ID (optional, auto-generated if empty)", 80)

	form := NewFormModel(styles)
	form.FocusOrder = []int{0, 1, 2, 3, 4}
	form.BindTextArea(0)
	form.BindTextInput(1)
	form.BindTextInput(2)
	form.BindButton(3)
	form.BindButton(4)

	m := CreatePlanModel{
		IDInput:        idInput,
		TitleInput:     titleInput,
		ObjectiveInput: obj,
		Form:           form,
		FocusIndex:     0,
		Width:          80,
		Height:         24,
		Styles:         styles,
	}
	m.recalcHeights()
	return m
}

func (m CreatePlanModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, textarea.Blink)
}

func (m *CreatePlanModel) recalcHeights() {
	var spacingLen = 2
	if m.Height < 22 {
		spacingLen = 1
	}

	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}

	var helpKeys []string
	if m.Height < 18 {
		helpKeys = []string{"Tab Next", "Esc Cancel", "Ctrl+C Cancel"}
	} else if m.Height < 20 {
		helpKeys = []string{"Tab Focus", "Left/Right Change", "Enter Submit", "Esc Cancel", "q Quit", "Ctrl+C Quit"}
	} else {
		helpKeys = []string{"Tab Focus", "Shift+Tab Back", "Left/Right Change", "Enter Select/Confirm", "Esc Cancel", "q Quit", "Ctrl+C Quit"}
	}
	helpText := m.Form.RenderHelp(helpKeys)
	helpLines := strings.Count(helpText, "\n") + 1

	var rootOverhead = 3
	boxHeight := m.Height - rootOverhead - 2
	if boxHeight < 2 {
		boxHeight = 2
	}

	if m.Height < 18 {
		nonTextAreaLines := 3 + spacingLen + helpLines
		if m.ErrorMsg != "" {
			nonTextAreaLines += 1 + spacingLen
		}
		h := boxHeight - nonTextAreaLines
		if h < 2 {
			h = 2
		}
		m.ObjectiveInput.SetHeight(h)
	} else {
		nonBlockLines := 2 + spacingLen
		if m.ErrorMsg != "" {
			nonBlockLines += 1 + spacingLen
		}
		nonBlockLines += 1 + helpLines

		blocksCapacity := boxHeight - nonBlockLines
		h := blocksCapacity
		if h < 2 {
			h = 2
		}
		m.ObjectiveInput.SetHeight(h)
	}

	inputWidth := m.Width - 8
	if inputWidth < 15 {
		inputWidth = 15
	}
	m.ObjectiveInput.SetWidth(inputWidth)

	titleInputWidth := m.Width - 8
	if m.Height < 20 {
		titleInputWidth = m.Width - 16
	}
	if titleInputWidth < 15 {
		titleInputWidth = 15
	}
	m.TitleInput.Width = titleInputWidth
	m.IDInput.Width = titleInputWidth
}

func (m CreatePlanModel) Update(msg tea.Msg) (CreatePlanModel, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Form.HandleWindowSize(msg)
		if m.Form.Width <= 0 || m.Form.Height <= 0 {
			return m, nil
		}
		m.Width = m.Form.Width
		m.Height = m.Form.Height
		m.recalcHeights()
		return m, nil

	case tea.MouseMsg:
		if m.FocusIndex == 0 {
			if cmd, handled := m.Form.HandleMouseMsg(msg, &m.ObjectiveInput); handled {
				return m, cmd
			}
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.ErrorMsg = ""
			m.Form.ErrorMsg = ""
			return m, changeScreenCmd(ScreenPlans, nil)

		case "tab":
			m.Form.NextFocusForOrder([]int{0, 1, 2, 3, 4})
			m.FocusIndex = m.Form.FocusIndex
			return m, m.updateFocus()

		case "shift+tab":
			m.Form.PreviousFocusForOrder([]int{0, 1, 2, 3, 4})
			m.FocusIndex = m.Form.FocusIndex
			return m, m.updateFocus()

		case "pgdn", "ctrl+d":
			if m.FocusIndex == 0 {
				return m, m.Form.ScrollTextAreaDown(&m.ObjectiveInput)
			}

		case "pgup", "ctrl+u":
			if m.FocusIndex == 0 {
				return m, m.Form.ScrollTextAreaUp(&m.ObjectiveInput)
			}

		case "down":
			if m.FocusIndex != 0 {
				m.FocusIndex = (m.FocusIndex + 1) % 5
				return m, m.updateFocus()
			}

		case "up":
			if m.FocusIndex != 0 {
				m.FocusIndex = (m.FocusIndex - 1 + 5) % 5
				return m, m.updateFocus()
			}

		case "left", "h", "right", "l":
			if m.FocusIndex == 3 {
				m.FocusIndex = 4
				return m, m.updateFocus()
			} else if m.FocusIndex == 4 {
				m.FocusIndex = 3
				return m, m.updateFocus()
			}

		case "enter":
			if m.FocusIndex == 1 {
				m.FocusIndex = 2
				return m, m.updateFocus()
			} else if m.FocusIndex == 2 {
				m.FocusIndex = 3
				return m, m.updateFocus()
			} else if m.FocusIndex == 3 {
				return m.handleSubmit()
			} else if m.FocusIndex == 4 {
				m.ErrorMsg = ""
				m.Form.ErrorMsg = ""
				return m, changeScreenCmd(ScreenPlans, nil)
			}
		}
	}

	if m.FocusIndex == 0 {
		m.ObjectiveInput, cmd = m.ObjectiveInput.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.FocusIndex == 1 {
		m.TitleInput, cmd = m.TitleInput.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.FocusIndex == 2 {
		m.IDInput, cmd = m.IDInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	m.recalcHeights()
	m.Form.ErrorMsg = m.ErrorMsg
	return m, tea.Batch(cmds...)
}

func (m *CreatePlanModel) updateFocus() tea.Cmd {
	m.Form.FocusIndex = m.FocusIndex
	return m.Form.SyncFocus(
		[]*textinput.Model{&m.TitleInput, &m.IDInput},
		[]*textarea.Model{&m.ObjectiveInput},
	)
}

func (m CreatePlanModel) handleSubmit() (CreatePlanModel, tea.Cmd) {
	objective := strings.TrimSpace(m.ObjectiveInput.Value())
	if objective == "" {
		m.ErrorMsg = "Objective/Description cannot be empty."
		m.Form.ErrorMsg = m.ErrorMsg
		m.recalcHeights()
		return m, nil
	}

	title := strings.TrimSpace(m.TitleInput.Value())
	if title == "" {
		firstLine := strings.Split(objective, "\n")[0]
		firstLine = cleanAutoTitle(firstLine)
		if len(firstLine) > 50 {
			title = firstLine[:50] + "..."
		} else if firstLine != "" {
			title = firstLine
		} else {
			if m.NextID != "" {
				title = "Plan " + m.NextID
			} else {
				title = "New Plan"
			}
		}
	}

	id := strings.TrimSpace(m.IDInput.Value())
	if id == "" {
		slug := slugifyTitle(title)
		if slug != "" {
			if m.NextID != "" {
				id = fmt.Sprintf("%s-%s", m.NextID, slug)
			} else {
				id = slug
			}
		} else {
			if m.NextID != "" {
				id = m.NextID
			} else {
				id = "plan"
			}
		}
	} else {
		id = cleanPlanID(id)
	}

	if id == "" {
		m.ErrorMsg = "Plan ID cannot be empty."
		m.Form.ErrorMsg = m.ErrorMsg
		m.recalcHeights()
		return m, nil
	}

	m.ErrorMsg = ""
	m.Form.ErrorMsg = ""
	return m, func() tea.Msg {
		return CreatePlanMsg{
			ID:        id,
			Title:     title,
			Objective: objective,
		}
	}
}

func cleanPlanID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var sb strings.Builder
	lastHyphen := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			sb.WriteRune('-')
			lastHyphen = true
		}
	}
	return strings.Trim(sb.String(), "-")
}

func (m CreatePlanModel) View() string {
	(&m).recalcHeights()
	var sb strings.Builder

	var spacing = "\n\n"
	if m.Height < 22 {
		spacing = "\n"
	}

	useWizard := m.Height < 18

	if useWizard {
		stepNum := m.FocusIndex + 1
		if stepNum > 4 {
			stepNum = 4
		}
		headerText := fmt.Sprintf("CREATE PLAN %s (Step %d/4)", m.NextID, stepNum)
		if idVal := strings.TrimSpace(m.IDInput.Value()); idVal != "" {
			headerText = fmt.Sprintf("CREATE PLAN %s (Step %d/4)", idVal, stepNum)
		} else if m.NextID == "" {
			headerText = fmt.Sprintf("CREATE NEW PLAN (Step %d/4)", stepNum)
		}
		sb.WriteString(m.Styles.DetailHeader.Render(headerText) + "\n" + spacing)

		if m.ErrorMsg != "" {
			m.Form.ErrorMsg = m.ErrorMsg
			sb.WriteString(m.Form.RenderError() + "\n" + spacing)
		}

		switch m.FocusIndex {
		case 0:
			sb.WriteString(m.Styles.DetailLabel.Render("Objective / Description *:") + "\n")
			sb.WriteString(m.ObjectiveInput.View() + "\n")
			sb.WriteString(m.Form.RenderHelp([]string{"Tab Next", "Esc Cancel", "Ctrl+C Cancel"}))

		case 1:
			sb.WriteString(m.Styles.DetailLabel.Render("Title (optional):") + "\n")
			m.TitleInput.TextStyle = m.Styles.FocusedInput
			sb.WriteString(m.TitleInput.View() + "\n")
			sb.WriteString(m.Form.RenderHelp([]string{"Tab Next", "Esc Cancel", "Ctrl+C Cancel"}))

		case 2:
			sb.WriteString(m.Styles.DetailLabel.Render("Plan ID (optional):") + "\n")
			m.IDInput.TextStyle = m.Styles.FocusedInput
			sb.WriteString(m.IDInput.View() + "\n")
			sb.WriteString(m.Form.RenderHelp([]string{"Tab Next", "Esc Cancel", "Ctrl+C Cancel"}))

		case 3, 4:
			sb.WriteString(m.Styles.DetailValue.Render("Ready to create plan?") + "\n\n")
			submitBtn := m.Form.RenderButtonWithStyles(3, "Submit", m.Styles.TableSelectedRow, m.Styles.SuccessText)
			cancelBtn := m.Form.RenderButtonWithStyles(4, "Cancel", m.Styles.TableSelectedRow, m.Styles.HelpStyle)
			sb.WriteString(fmt.Sprintf("%s    %s\n", submitBtn, cancelBtn))
			sb.WriteString(m.Form.RenderHelp([]string{"Tab Toggle", "Enter Confirm", "Esc Cancel", "q Quit", "Ctrl+C Quit"}))
		}
	} else {
		headerText := "CREATE NEW PLAN"
		if m.NextID != "" {
			headerText = fmt.Sprintf("CREATE NEW PLAN (ID: %s)", m.NextID)
		}
		if idVal := strings.TrimSpace(m.IDInput.Value()); idVal != "" {
			headerText = fmt.Sprintf("CREATE NEW PLAN (ID: %s)", idVal)
		}
		sb.WriteString(m.Styles.DetailHeader.Render(headerText) + "\n" + spacing)

		if m.ErrorMsg != "" {
			m.Form.ErrorMsg = m.ErrorMsg
			sb.WriteString(m.Form.RenderError() + "\n" + spacing)
		}

		var allBlocks []FormBlock

		// Block 0: Objective Input
		{
			var blockSb strings.Builder
			var labelStyle lipgloss.Style
			if m.FocusIndex == 0 {
				labelStyle = m.Styles.DetailLabel.Underline(true)
			} else {
				labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
			}
			if m.Height < 20 {
				blockSb.WriteString(labelStyle.Render("Objective *") + "\n")
			} else {
				blockSb.WriteString(labelStyle.Render("Objective / Description *") + "\n")
			}
			blockSb.WriteString(m.ObjectiveInput.View())
			allBlocks = append(allBlocks, FormBlock{FocusIndices: []int{0}, Content: blockSb.String()})
		}

		// Block 1: Title Input
		{
			var blockSb strings.Builder
			var labelStyle lipgloss.Style
			if m.FocusIndex == 1 {
				labelStyle = m.Styles.DetailLabel.Underline(true)
				m.TitleInput.TextStyle = m.Styles.FocusedInput
			} else {
				labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
				m.TitleInput.TextStyle = m.Styles.BlurredInput
			}
			if m.Height < 20 {
				blockSb.WriteString(labelStyle.Render("Title:") + " " + m.TitleInput.View())
			} else {
				blockSb.WriteString(labelStyle.Render("Title (optional)") + "\n")
				blockSb.WriteString(m.TitleInput.View())
			}
			allBlocks = append(allBlocks, FormBlock{FocusIndices: []int{1}, Content: blockSb.String()})
		}

		// Block 2: Plan ID Input
		{
			var blockSb strings.Builder
			var labelStyle lipgloss.Style
			if m.FocusIndex == 2 {
				labelStyle = m.Styles.DetailLabel.Underline(true)
				m.IDInput.TextStyle = m.Styles.FocusedInput
			} else {
				labelStyle = m.Styles.DetailValue.Bold(!m.Styles.NoBold)
				m.IDInput.TextStyle = m.Styles.BlurredInput
			}
			if m.Height < 20 {
				blockSb.WriteString(labelStyle.Render("Plan ID:") + " " + m.IDInput.View())
			} else {
				blockSb.WriteString(labelStyle.Render("Plan ID (optional)") + "\n")
				blockSb.WriteString(m.IDInput.View())
			}
			allBlocks = append(allBlocks, FormBlock{FocusIndices: []int{2}, Content: blockSb.String()})
		}

		// Block 3: Action Buttons
		{
			var blockSb strings.Builder
			submitBtn := m.Form.RenderButtonWithStyles(3, "Submit", m.Styles.TableSelectedRow, m.Styles.SuccessText)
			cancelBtn := m.Form.RenderButtonWithStyles(4, "Cancel", m.Styles.TableSelectedRow, m.Styles.HelpStyle)
			blockSb.WriteString(fmt.Sprintf("%s    %s", submitBtn, cancelBtn))
			allBlocks = append(allBlocks, FormBlock{FocusIndices: []int{3, 4}, Content: blockSb.String()})
		}

		var helpKeys []string
		if m.Height < 20 {
			helpKeys = []string{"Tab Focus", "Left/Right Change", "Enter Submit", "Esc Cancel", "q Quit", "Ctrl+C Quit"}
		} else {
			helpKeys = []string{"Tab Focus", "Shift+Tab Back", "Left/Right Change", "Enter Select/Confirm", "Esc Cancel", "q Quit", "Ctrl+C Quit"}
		}
		helpText := m.Form.RenderHelp(helpKeys)

		var rootOverhead = 3
		boxHeight := m.Height - rootOverhead - 2
		if boxHeight < 2 {
			boxHeight = 2
		}

		body := RenderBoundedBlocks(allBlocks, m.FocusIndex, boxHeight, spacing, helpText, m.ErrorMsg)
		sb.WriteString(body)

		formBox := m.Styles.ActiveBorder.
			Width(m.Width - 4).
			Height(boxHeight).
			Render(truncateLines(sb.String(), boxHeight))

		return formBox
	}

	var rootOverhead = 3
	boxHeight := m.Height - rootOverhead - 2
	if boxHeight < 2 {
		boxHeight = 2
	}
	return m.Styles.ActiveBorder.
		Width(m.Width - 4).
		Height(boxHeight).
		Render(truncateLines(sb.String(), boxHeight))
}

// DeletePlanModel requires the exact Plan ID before emitting a delete request.
type DeletePlanModel struct {
	Plan         config.Plan
	ReturnScreen Screen
	ConfirmInput textinput.Model
	FocusIndex   int
	Width        int
	Height       int
	Styles       Styles
	ErrorMsg     string
}

// NewDeletePlanModel initializes a typed confirmation for plan.
func NewDeletePlanModel(plan config.Plan, returnScreen Screen, styles Styles) DeletePlanModel {
	input := textinput.New()
	input.Placeholder = plan.ID
	input.CharLimit = 80
	input.Width = 40
	input.PlaceholderStyle = styles.SubtleText
	input.Cursor.Style = styles.AccentText
	input.Focus()
	return DeletePlanModel{Plan: plan, ReturnScreen: returnScreen, ConfirmInput: input, Styles: styles}
}

func (m DeletePlanModel) Init() tea.Cmd { return textinput.Blink }

func (m DeletePlanModel) Update(msg tea.Msg) (DeletePlanModel, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		if size.Width > 0 && size.Height > 0 {
			m.Width, m.Height = size.Width, size.Height
		}
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return m, changeScreenCmd(m.ReturnScreen, m.returnData())
		case "tab", "shift+tab", "down", "up":
			m.FocusIndex = (m.FocusIndex + 1) % 2
			if m.FocusIndex == 0 {
				return m, m.ConfirmInput.Focus()
			}
			m.ConfirmInput.Blur()
			return m, nil
		case "enter":
			if m.FocusIndex == 1 {
				return m, changeScreenCmd(m.ReturnScreen, m.returnData())
			}
			if m.ConfirmInput.Value() != m.Plan.ID {
				m.ErrorMsg = fmt.Sprintf("Enter exact Plan ID %q to confirm deletion.", m.Plan.ID)
				return m, nil
			}
			return m, func() tea.Msg { return DeletePlanMsg{Plan: m.Plan, ReturnScreen: m.ReturnScreen} }
		}
	}
	var cmd tea.Cmd
	if m.FocusIndex == 0 {
		m.ConfirmInput, cmd = m.ConfirmInput.Update(msg)
	}
	return m, cmd
}

func (m DeletePlanModel) returnData() interface{} {
	if m.ReturnScreen == ScreenPlanDetails {
		return m.Plan
	}
	return nil
}

func (m DeletePlanModel) View() string {
	cancel := m.Styles.SubtleText.Render("[ Cancel ]")
	if m.FocusIndex == 1 {
		cancel = m.Styles.CommandKey.Render("[ Cancel ]")
	}
	lines := []string{
		m.Styles.SectionTitle.Render("Delete Plan"),
		m.Styles.WarningText.Render(fmt.Sprintf("Delete %s - %s?", m.Plan.ID, m.Plan.Title)),
		config.PlanningMetadataOnlyWarning,
		fmt.Sprintf("Enter %s to confirm:", m.Styles.DetailValue.Render(m.Plan.ID)),
		m.ConfirmInput.View(),
	}
	if m.ErrorMsg != "" {
		lines = append(lines, m.Styles.RenderError(m.ErrorMsg))
	}
	lines = append(lines, cancel, renderCommandHelp(m.Styles, []string{"Enter Confirm", "Tab Cancel", "Esc Back"}, maxInt(20, m.Width-4)))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func changeScreenCmd(screen Screen, data interface{}) tea.Cmd {
	return func() tea.Msg { return ChangeScreenMsg{Screen: screen, Data: data} }
}
