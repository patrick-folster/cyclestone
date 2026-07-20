package tui

import (
	"fmt"
	"strings"

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
	IDInput        textinput.Model
	TitleInput     textinput.Model
	ObjectiveInput textinput.Model
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
		input.Width = 60
		input.PlaceholderStyle = styles.SubtleText
		input.Cursor.Style = styles.AccentText
		return input
	}
	m := CreatePlanModel{
		IDInput:        newInput("lowercase-plan-id", 80),
		TitleInput:     newInput("Plan title", 160),
		ObjectiveInput: newInput("What should this Plan accomplish?", 500),
		Styles:         styles,
	}
	m.IDInput.Focus()
	return m
}

func (m CreatePlanModel) Init() tea.Cmd { return textinput.Blink }

func (m CreatePlanModel) Update(msg tea.Msg) (CreatePlanModel, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		if size.Width > 0 && size.Height > 0 {
			m.Width, m.Height = size.Width, size.Height
			width := size.Width - 10
			if width < 20 {
				width = 20
			}
			if width > 70 {
				width = 70
			}
			m.IDInput.Width, m.TitleInput.Width, m.ObjectiveInput.Width = width, width, width
		}
		return m, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return m, changeScreenCmd(ScreenPlans, nil)
		case "tab", "down":
			m.FocusIndex = (m.FocusIndex + 1) % 5
			return m, m.updateFocus()
		case "shift+tab", "up":
			m.FocusIndex = (m.FocusIndex + 4) % 5
			return m, m.updateFocus()
		case "enter":
			if m.FocusIndex < 3 {
				m.FocusIndex++
				return m, m.updateFocus()
			}
			if m.FocusIndex == 4 {
				return m, changeScreenCmd(ScreenPlans, nil)
			}
			return m, func() tea.Msg {
				return CreatePlanMsg{ID: strings.TrimSpace(m.IDInput.Value()), Title: strings.TrimSpace(m.TitleInput.Value()), Objective: strings.TrimSpace(m.ObjectiveInput.Value())}
			}
		}
	}

	var cmd tea.Cmd
	switch m.FocusIndex {
	case 0:
		m.IDInput, cmd = m.IDInput.Update(msg)
	case 1:
		m.TitleInput, cmd = m.TitleInput.Update(msg)
	case 2:
		m.ObjectiveInput, cmd = m.ObjectiveInput.Update(msg)
	}
	return m, cmd
}

func (m *CreatePlanModel) updateFocus() tea.Cmd {
	m.IDInput.Blur()
	m.TitleInput.Blur()
	m.ObjectiveInput.Blur()
	switch m.FocusIndex {
	case 0:
		return m.IDInput.Focus()
	case 1:
		return m.TitleInput.Focus()
	case 2:
		return m.ObjectiveInput.Focus()
	default:
		return nil
	}
}

func (m CreatePlanModel) View() string {
	button := func(index int, label string) string {
		if m.FocusIndex == index {
			return m.Styles.CommandKey.Render("[ " + label + " ]")
		}
		return m.Styles.SubtleText.Render("[ " + label + " ]")
	}
	lines := []string{
		m.Styles.SectionTitle.Render("Create Plan"),
		m.Styles.DetailLabel.Render("Plan ID (required)") + "\n" + m.IDInput.View(),
		m.Styles.DetailLabel.Render("Title (required)") + "\n" + m.TitleInput.View(),
		m.Styles.DetailLabel.Render("Objective (required)") + "\n" + m.ObjectiveInput.View(),
	}
	if m.ErrorMsg != "" {
		lines = append(lines, m.Styles.RenderError(m.ErrorMsg))
	}
	lines = append(lines,
		lipgloss.JoinHorizontal(lipgloss.Left, button(3, "Create"), "  ", button(4, "Cancel")),
		renderCommandHelp(m.Styles, []string{"Tab/Shift+Tab Move", "Enter Select", "Esc Cancel"}, maxInt(20, m.Width-4)),
	)
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
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
