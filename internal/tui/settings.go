package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

// SettingsSavedMsg is sent when settings are saved successfully.
type SettingsSavedMsg struct {
	Scope string
}

const (
	settingsScopeGlobal  = "global"
	settingsScopeProject = "project"
)

const (
	settingDefaultLLM = iota
	settingCustomLLM
	settingDefaultMode
	settingAutoGitBranch
	settingCreateMilestoneBranch
	settingDefaultGitBranchPrefix
	settingDisableBold
	settingDisableRoundedBorders
	settingEnableContextCaching
	settingEnableCompactPhaseHandoffs
	settingEnableCodexSessionResume
	settingCacheTTLMinutes
	settingMaxHandoffChars
	settingMaxModelCallsPerPhase
	settingMaxTokenBudgetPerPhase
	settingMaxLLMInputChars
	settingMaxRetainedConversationMessages
	settingGeminiModel
	settingOpenAIModel
	settingAnthropicModel
	settingAiderModel
	settingOllamaModel
	settingOllamaHost
	settingOllamaKeepAlive
	settingOllamaNumCtx
	settingOllamaNumPredict
	settingAgentGroups
	settingSave
	settingCancel
	settingsRowCount
)

type settingsTextField int

const (
	fieldCustomLLM settingsTextField = iota
	fieldCacheTTL
	fieldMaxHandoffChars
	fieldMaxCalls
	fieldTokenBudget
	fieldLLMInputChars
	fieldMaxRetainedConversationMessages
	fieldGeminiModel
	fieldOpenAIModel
	fieldAnthropicModel
	fieldAiderModel
	fieldOllamaModel
	fieldOllamaHost
	fieldOllamaKeepAlive
	fieldOllamaNumCtx
	fieldOllamaNumPredict
	fieldDefaultGitBranchPrefix
)

type settingsGroup struct {
	Name string
	Rows []int
}

var settingsGroups = []settingsGroup{
	{Name: "Runner Selection", Rows: []int{settingDefaultLLM, settingCustomLLM}},
	{Name: "Execution Behavior", Rows: []int{settingDefaultMode, settingAutoGitBranch, settingCreateMilestoneBranch, settingDefaultGitBranchPrefix}},
	{Name: "UI Behavior", Rows: []int{settingDisableBold, settingDisableRoundedBorders}},
	{Name: "Context/Cache Limits", Rows: []int{settingEnableContextCaching, settingEnableCompactPhaseHandoffs, settingEnableCodexSessionResume, settingCacheTTLMinutes, settingMaxHandoffChars, settingMaxModelCallsPerPhase, settingMaxTokenBudgetPerPhase, settingMaxLLMInputChars, settingMaxRetainedConversationMessages}},
	{Name: "Aider Settings", Rows: []int{settingAiderModel}},
	{Name: "Ollama via Aider Settings", Rows: []int{settingOllamaModel, settingOllamaHost, settingOllamaKeepAlive, settingOllamaNumCtx, settingOllamaNumPredict}},
	{Name: "Agent Groups", Rows: []int{settingAgentGroups}},
	{Name: "Save & Exit", Rows: []int{}},
	{Name: "Discard & Exit", Rows: []int{}},
}

// SettingsModel handles editing and saving configurations.
type SettingsModel struct {
	Scope              string // "global" or "project"
	GlobalDraft        config.Settings
	ProjectDraft       config.Settings
	GlobalOriginal     config.Settings
	ProjectOriginal    config.Settings
	ShowDiscardPrompt  bool
	DiscardQuit        bool
	FocusIndex         int
	ActiveGroup        int
	SelectedGroup      int
	GroupScrollOffset  int
	DetailScrollOffset int
	Width              int
	Height             int
	Styles             Styles
	ErrorMsg           string
	SuccessMsg         string

	CustomLLMInput                  textinput.Model
	CacheTTLInput                   textinput.Model
	MaxHandoffInput                 textinput.Model
	MaxCallsInput                   textinput.Model
	TokenBudgetInput                textinput.Model
	LLMInputInput                   textinput.Model
	MaxRetainedConversationMsgInput textinput.Model

	GeminiModelInput            textinput.Model
	OpenAIModelInput            textinput.Model
	AnthropicModelInput         textinput.Model
	AiderModelInput             textinput.Model
	OllamaModelInput            textinput.Model
	OllamaHostInput             textinput.Model
	KeepAliveInput              textinput.Model
	OllamaNumCtxInput           textinput.Model
	OllamaPredictInput          textinput.Model
	DefaultGitBranchPrefixInput textinput.Model
}

// NewSettingsModel instantiates the settings model.
func NewSettingsModel(styles Styles) SettingsModel {
	newInput := func(placeholder string, width int, limit int) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.CharLimit = limit
		ti.Width = width
		ti.TextStyle = styles.BlurredInput
		ti.PlaceholderStyle = styles.SubtleText
		ti.Cursor.Style = styles.AccentText
		return ti
	}

	m := SettingsModel{
		Scope:                           settingsScopeProject,
		FocusIndex:                      settingDefaultLLM,
		ActiveGroup:                     -1,
		Styles:                          styles,
		CustomLLMInput:                  newInput("./path/to/script.sh", 40, 250),
		CacheTTLInput:                   newInput("30", 10, 5),
		MaxHandoffInput:                 newInput("12000", 10, 8),
		MaxCallsInput:                   newInput("50", 10, 5),
		TokenBudgetInput:                newInput("1000000", 15, 8),
		LLMInputInput:                   newInput("900000", 15, 9),
		MaxRetainedConversationMsgInput: newInput("8", 10, 5),
		GeminiModelInput:                newInput("gemini model", 32, 120),
		OpenAIModelInput:                newInput("openai model", 32, 120),
		AnthropicModelInput:             newInput("anthropic model", 32, 120),
		AiderModelInput:                 newInput("aider model", 32, 120),
		OllamaModelInput:                newInput(config.DefaultOllamaModel, 32, 120),
		OllamaHostInput:                 newInput("http://localhost:11434", 40, 200),
		KeepAliveInput:                  newInput("5m", 15, 20),
		OllamaNumCtxInput:               newInput("4096", 10, 8),
		OllamaPredictInput:              newInput("1024", 10, 8),
		DefaultGitBranchPrefixInput:     newInput("cyclestone/milestones/", 32, 120),
	}
	m.loadSettingsDrafts()
	return m
}

func isCustomLLM(val string) bool {
	switch val {
	case "codex", "agy", "aider", "gemini", "openai", "anthropic", "ollama", "ollama_api", "":
		return false
	}
	return true
}

func (m *SettingsModel) loadSettingsDrafts() {
	global, err := config.LoadGlobalSettings()
	if err != nil {
		m.ErrorMsg = fmt.Sprintf("Error loading global settings: %v", err)
	}
	m.GlobalDraft = global

	project, err := config.LoadProjectSettings()
	if err != nil {
		m.ErrorMsg = fmt.Sprintf("Error loading project settings: %v", err)
	}
	m.ProjectDraft = project
	m.normalizeDefaultLLMDrafts()
	m.GlobalOriginal = m.GlobalDraft
	m.ProjectOriginal = m.ProjectDraft

	m.syncCustomInput()
	m.updateTextInputFocus()
	m.updatePlaceholders()
}

func (m *SettingsModel) normalizeDefaultLLMDrafts() {
	m.GlobalDraft.DefaultLLM = normalizeMilestoneRunner(m.GlobalDraft.DefaultLLM)
	if m.ProjectDraft.DefaultLLM != "" {
		m.ProjectDraft.DefaultLLM = normalizeMilestoneRunner(m.ProjectDraft.DefaultLLM)
	}
}

func (m SettingsModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m SettingsModel) Update(msg tea.Msg) (SettingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil
		}
		m.Width = msg.Width
		m.Height = msg.Height
		m.ensureScrollVisible()
		return m, nil
	case tea.KeyMsg:
		if m.ShowDiscardPrompt {
			switch strings.ToLower(msg.String()) {
			case "y":
				m.ShowDiscardPrompt = false
				if m.DiscardQuit {
					return m, tea.Quit
				}
				return m, func() tea.Msg { return ChangeScreenMsg{Screen: ScreenDashboard} }
			case "n", "esc":
				m.ShowDiscardPrompt = false
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "esc":
			if !m.inGroupList() {
				m.ActiveGroup = -1
				m.updateTextInputFocus()
				m.ensureScrollVisible()
				return m, nil
			}
			if m.HasUnsavedChanges() {
				m.ShowDiscardPrompt = true
				m.DiscardQuit = false
				return m, nil
			}
			return m, func() tea.Msg { return ChangeScreenMsg{Screen: ScreenDashboard} }
		case "g":
			if m.IsTextInputFocused() {
				return m.updateActiveTextInput(msg)
			}
			if !m.inGroupList() {
				return m, nil
			}
			m.switchScope(settingsScopeGlobal)
			return m, nil
		case "p":
			if m.IsTextInputFocused() {
				return m.updateActiveTextInput(msg)
			}
			if !m.inGroupList() {
				return m, nil
			}
			m.switchScope(settingsScopeProject)
			return m, nil
		case " ":
			if m.IsTextInputFocused() {
				return m.updateActiveTextInput(msg)
			}
			if m.inGroupList() {
				return m, nil
			}
			switch m.FocusIndex {
			case settingDefaultLLM, settingDefaultMode, settingAutoGitBranch, settingCreateMilestoneBranch,
				settingDisableBold, settingDisableRoundedBorders, settingEnableContextCaching, settingEnableCompactPhaseHandoffs, settingEnableCodexSessionResume:
				m.handleLeftRight(false)
				m.updateTextInputFocus()
			}
			return m, nil
		case "tab", "down":
			if m.inGroupList() {
				m.SelectedGroup = (m.SelectedGroup + 1) % len(settingsGroups)
			} else {
				m.FocusIndex = m.nextFocusInActiveGroup(m.FocusIndex)
				m.updateTextInputFocus()
			}
			m.ensureScrollVisible()
			return m, nil
		case "shift+tab", "up":
			if m.inGroupList() {
				m.SelectedGroup = (m.SelectedGroup - 1 + len(settingsGroups)) % len(settingsGroups)
			} else {
				m.FocusIndex = m.prevFocusInActiveGroup(m.FocusIndex)
				m.updateTextInputFocus()
			}
			m.ensureScrollVisible()
			return m, nil
		case "pgdown":
			if m.inGroupList() {
				m.SelectedGroup = minInt(len(settingsGroups)-1, m.SelectedGroup+m.contentHeight())
			} else {
				rows := m.activeGroupRows()
				m.FocusIndex = rows[minInt(len(rows)-1, m.indexOfRow(rows, m.FocusIndex)+m.contentHeight())]
				m.updateTextInputFocus()
			}
			m.ensureScrollVisible()
			return m, nil
		case "pgup":
			if m.inGroupList() {
				m.SelectedGroup = maxInt(0, m.SelectedGroup-m.contentHeight())
			} else {
				rows := m.activeGroupRows()
				m.FocusIndex = rows[maxInt(0, m.indexOfRow(rows, m.FocusIndex)-m.contentHeight())]
				m.updateTextInputFocus()
			}
			m.ensureScrollVisible()
			return m, nil
		case "left", "h":
			if m.IsTextInputFocused() {
				return m.updateActiveTextInput(msg)
			}
			if m.inGroupList() {
				return m, nil
			}
			m.handleLeftRight(true)
			m.updateTextInputFocus()
			return m, nil
		case "right", "l":
			if m.IsTextInputFocused() {
				return m.updateActiveTextInput(msg)
			}
			if m.inGroupList() {
				selectedGroup := m.clampedSelectedGroup()
				if len(settingsGroups[selectedGroup].Rows) > 0 {
					m.enterSelectedGroup()
				}
				return m, nil
			}
			m.handleLeftRight(false)
			m.updateTextInputFocus()
			return m, nil
		case "b", "backspace":
			if m.IsTextInputFocused() {
				return m.updateActiveTextInput(msg)
			}
			if !m.inGroupList() {
				m.ActiveGroup = -1
				m.updateTextInputFocus()
				m.ensureScrollVisible()
			}
			return m, nil
		case "enter":
			if m.inGroupList() {
				selectedGroup := m.clampedSelectedGroup()
				switch settingsGroups[selectedGroup].Name {
				case "Save & Exit":
					return m.handleSave()
				case "Discard & Exit":
					if m.HasUnsavedChanges() {
						m.ShowDiscardPrompt = true
						m.DiscardQuit = false
						return m, nil
					}
					return m, func() tea.Msg { return ChangeScreenMsg{Screen: ScreenDashboard} }
				default:
					m.enterSelectedGroup()
					return m, nil
				}
			}
			switch m.FocusIndex {
			case settingSave:
				return m.handleSave()
			case settingCancel:
				if m.HasUnsavedChanges() {
					m.ShowDiscardPrompt = true
					m.DiscardQuit = false
					return m, nil
				}
				return m, func() tea.Msg { return ChangeScreenMsg{Screen: ScreenDashboard} }
			case settingAgentGroups:
				return m, func() tea.Msg { return ChangeScreenMsg{Screen: ScreenAgentGroups} }
			default:
				m.FocusIndex = m.nextFocusInActiveGroup(m.FocusIndex)
				m.updateTextInputFocus()
				m.ensureScrollVisible()
				return m, nil
			}
		default:
			if m.IsTextInputFocused() {
				return m.updateActiveTextInput(msg)
			}
		}
	}
	return m, nil
}

func (m *SettingsModel) switchScope(scope string) {
	if m.Scope == scope {
		return
	}
	m.syncInputValuesToDraft()
	m.Scope = scope
	m.syncCustomInput()
	if m.FocusIndex == settingCustomLLM {
		m.FocusIndex = m.firstRowForGroup(m.ActiveGroup)
	}
	m.updateTextInputFocus()
	m.updatePlaceholders()
	m.ensureScrollVisible()
}

func (m SettingsModel) visibleRows() []int {
	rows := make([]int, 0, settingsRowCount)
	for _, group := range settingsGroups {
		for _, row := range group.Rows {
			if row == settingCustomLLM {
				continue
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func (m SettingsModel) nextFocus(idx int) int {
	rows := m.visibleRows()
	for i, row := range rows {
		if row == idx {
			return rows[(i+1)%len(rows)]
		}
	}
	return rows[0]
}

func (m SettingsModel) prevFocus(idx int) int {
	rows := m.visibleRows()
	for i, row := range rows {
		if row == idx {
			return rows[(i-1+len(rows))%len(rows)]
		}
	}
	return rows[0]
}

func (m SettingsModel) inGroupList() bool {
	return m.ActiveGroup < 0
}

func (m *SettingsModel) enterSelectedGroup() {
	selected := m.clampedSelectedGroup()
	if len(settingsGroups[selected].Rows) == 0 {
		return
	}
	m.ActiveGroup = selected
	m.FocusIndex = m.firstRowForGroup(m.ActiveGroup)
	m.updateTextInputFocus()
	m.ensureScrollVisible()
}

func (m SettingsModel) clampedSelectedGroup() int {
	if m.SelectedGroup < 0 {
		return 0
	}
	if m.SelectedGroup >= len(settingsGroups) {
		return len(settingsGroups) - 1
	}
	return m.SelectedGroup
}

func (m SettingsModel) firstRowForGroup(groupIdx int) int {
	rows := m.rowsForGroup(groupIdx)
	if len(rows) == 0 {
		return m.visibleRows()[0]
	}
	return rows[0]
}

func (m SettingsModel) rowsForGroup(groupIdx int) []int {
	if groupIdx < 0 || groupIdx >= len(settingsGroups) {
		return m.visibleRows()
	}
	group := settingsGroups[groupIdx]
	if len(group.Rows) == 0 {
		return nil
	}
	rows := make([]int, 0, len(group.Rows))
	for _, row := range group.Rows {
		if row == settingCustomLLM {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return m.visibleRows()
	}
	return rows
}

func (m SettingsModel) activeGroupRows() []int {
	return m.rowsForGroup(m.ActiveGroup)
}

func (m SettingsModel) nextFocusInActiveGroup(idx int) int {
	rows := m.activeGroupRows()
	if len(rows) == 0 {
		return 0
	}
	for i, row := range rows {
		if row == idx {
			return rows[(i+1)%len(rows)]
		}
	}
	return rows[0]
}

func (m SettingsModel) prevFocusInActiveGroup(idx int) int {
	rows := m.activeGroupRows()
	if len(rows) == 0 {
		return 0
	}
	for i, row := range rows {
		if row == idx {
			return rows[(i-1+len(rows))%len(rows)]
		}
	}
	return rows[0]
}

func (m SettingsModel) indexOfRow(rows []int, row int) int {
	for i, candidate := range rows {
		if candidate == row {
			return i
		}
	}
	return 0
}

func (m SettingsModel) IsTextInputFocused() bool {
	if m.inGroupList() {
		return false
	}
	return m.textFieldForFocus() >= 0
}

func (m SettingsModel) textFieldForFocus() settingsTextField {
	switch m.FocusIndex {
	case settingCustomLLM:
		return fieldCustomLLM
	case settingCacheTTLMinutes:
		return fieldCacheTTL
	case settingMaxHandoffChars:
		return fieldMaxHandoffChars
	case settingMaxModelCallsPerPhase:
		return fieldMaxCalls
	case settingMaxTokenBudgetPerPhase:
		return fieldTokenBudget
	case settingMaxLLMInputChars:
		return fieldLLMInputChars
	case settingMaxRetainedConversationMessages:
		return fieldMaxRetainedConversationMessages
	case settingGeminiModel:
		return fieldGeminiModel
	case settingOpenAIModel:
		return fieldOpenAIModel
	case settingAnthropicModel:
		return fieldAnthropicModel
	case settingAiderModel:
		return fieldAiderModel
	case settingOllamaModel:
		return fieldOllamaModel
	case settingOllamaHost:
		return fieldOllamaHost
	case settingOllamaKeepAlive:
		return fieldOllamaKeepAlive
	case settingOllamaNumCtx:
		return fieldOllamaNumCtx
	case settingOllamaNumPredict:
		return fieldOllamaNumPredict
	case settingDefaultGitBranchPrefix:
		return fieldDefaultGitBranchPrefix
	default:
		return -1
	}
}

func (m *SettingsModel) inputForField(field settingsTextField) *textinput.Model {
	switch field {
	case fieldCustomLLM:
		return &m.CustomLLMInput
	case fieldCacheTTL:
		return &m.CacheTTLInput
	case fieldMaxHandoffChars:
		return &m.MaxHandoffInput
	case fieldMaxCalls:
		return &m.MaxCallsInput
	case fieldTokenBudget:
		return &m.TokenBudgetInput
	case fieldLLMInputChars:
		return &m.LLMInputInput
	case fieldMaxRetainedConversationMessages:
		return &m.MaxRetainedConversationMsgInput
	case fieldGeminiModel:
		return &m.GeminiModelInput
	case fieldOpenAIModel:
		return &m.OpenAIModelInput
	case fieldAnthropicModel:
		return &m.AnthropicModelInput
	case fieldAiderModel:
		return &m.AiderModelInput
	case fieldOllamaModel:
		return &m.OllamaModelInput
	case fieldOllamaHost:
		return &m.OllamaHostInput
	case fieldOllamaKeepAlive:
		return &m.KeepAliveInput
	case fieldOllamaNumCtx:
		return &m.OllamaNumCtxInput
	case fieldOllamaNumPredict:
		return &m.OllamaPredictInput
	case fieldDefaultGitBranchPrefix:
		return &m.DefaultGitBranchPrefixInput
	default:
		return nil
	}
}

func (m *SettingsModel) updateTextInputFocus() {
	activeField := settingsTextField(-1)
	if !m.inGroupList() {
		activeField = m.textFieldForFocus()
	}
	for _, field := range []settingsTextField{
		fieldCustomLLM, fieldCacheTTL, fieldMaxHandoffChars, fieldMaxCalls, fieldTokenBudget, fieldLLMInputChars, fieldMaxRetainedConversationMessages,
		fieldGeminiModel, fieldOpenAIModel, fieldAnthropicModel, fieldAiderModel, fieldOllamaModel, fieldOllamaHost,
		fieldOllamaKeepAlive, fieldOllamaNumCtx, fieldOllamaNumPredict, fieldDefaultGitBranchPrefix,
	} {
		input := m.inputForField(field)
		if input == nil {
			continue
		}
		if field == activeField {
			input.Focus()
			input.TextStyle = m.Styles.FocusedInput
		} else {
			input.Blur()
			input.TextStyle = m.Styles.BlurredInput
		}
	}
}

func (m SettingsModel) updateActiveTextInput(msg tea.KeyMsg) (SettingsModel, tea.Cmd) {
	field := m.textFieldForFocus()
	input := m.inputForField(field)
	if input == nil {
		return m, nil
	}
	var cmd tea.Cmd
	*input, cmd = input.Update(msg)
	m.applyTextFieldValue(field)
	return m, cmd
}

func (m *SettingsModel) applyTextFieldValue(field settingsTextField) {
	draft := m.getActiveDraft()
	switch field {
	case fieldCustomLLM:
		draft.DefaultLLM = m.CustomLLMInput.Value()
	case fieldCacheTTL:
		draft.CacheTTLMinutes = parseIntOrZero(m.CacheTTLInput.Value())
	case fieldMaxHandoffChars:
		draft.MaxHandoffChars = parseIntOrZero(m.MaxHandoffInput.Value())
	case fieldMaxCalls:
		draft.MaxModelCallsPerPhase = parseIntOrZero(m.MaxCallsInput.Value())
	case fieldTokenBudget:
		draft.MaxTokenBudgetPerPhase = parseIntOrZero(m.TokenBudgetInput.Value())
	case fieldLLMInputChars:
		draft.MaxLLMInputChars = parseIntOrZero(m.LLMInputInput.Value())
	case fieldMaxRetainedConversationMessages:
		draft.MaxRetainedConversationMessages = parseIntOrZero(m.MaxRetainedConversationMsgInput.Value())
	case fieldGeminiModel:
		draft.GeminiModel = m.GeminiModelInput.Value()
	case fieldOpenAIModel:
		draft.OpenAIModel = m.OpenAIModelInput.Value()
	case fieldAnthropicModel:
		draft.AnthropicModel = m.AnthropicModelInput.Value()
	case fieldAiderModel:
		draft.AiderModel = m.AiderModelInput.Value()
	case fieldOllamaModel:
		draft.OllamaModel = m.OllamaModelInput.Value()
	case fieldOllamaHost:
		draft.OllamaHost = m.OllamaHostInput.Value()
	case fieldOllamaKeepAlive:
		draft.OllamaKeepAlive = m.KeepAliveInput.Value()
	case fieldOllamaNumCtx:
		draft.OllamaNumCtx = parseIntOrZero(m.OllamaNumCtxInput.Value())
	case fieldOllamaNumPredict:
		draft.OllamaNumPredict = parseIntOrZero(m.OllamaPredictInput.Value())
	case fieldDefaultGitBranchPrefix:
		draft.DefaultGitBranchPrefix = m.DefaultGitBranchPrefixInput.Value()
	}
}

func parseIntOrZero(val string) int {
	var intVal int
	if _, err := fmt.Sscanf(strings.TrimSpace(val), "%d", &intVal); err != nil {
		return 0
	}
	return intVal
}

func (m *SettingsModel) getActiveDraft() *config.Settings {
	if m.Scope == settingsScopeGlobal {
		return &m.GlobalDraft
	}
	return &m.ProjectDraft
}

func (m *SettingsModel) syncCustomInput() {
	draft := m.getActiveDraft()
	if isCustomLLM(draft.DefaultLLM) {
		m.CustomLLMInput.SetValue(draft.DefaultLLM)
	} else {
		m.CustomLLMInput.SetValue("")
	}
	setIntInput(&m.CacheTTLInput, draft.CacheTTLMinutes)
	setIntInput(&m.MaxHandoffInput, draft.MaxHandoffChars)
	setIntInput(&m.MaxCallsInput, draft.MaxModelCallsPerPhase)
	setIntInput(&m.TokenBudgetInput, draft.MaxTokenBudgetPerPhase)
	setIntInput(&m.LLMInputInput, draft.MaxLLMInputChars)
	setIntInput(&m.MaxRetainedConversationMsgInput, draft.MaxRetainedConversationMessages)
	m.GeminiModelInput.SetValue(draft.GeminiModel)
	m.OpenAIModelInput.SetValue(draft.OpenAIModel)
	m.AnthropicModelInput.SetValue(draft.AnthropicModel)
	m.AiderModelInput.SetValue(draft.AiderModel)
	m.OllamaModelInput.SetValue(draft.OllamaModel)
	m.OllamaHostInput.SetValue(draft.OllamaHost)
	m.KeepAliveInput.SetValue(draft.OllamaKeepAlive)
	setIntInput(&m.OllamaNumCtxInput, draft.OllamaNumCtx)
	setIntInput(&m.OllamaPredictInput, draft.OllamaNumPredict)
	m.DefaultGitBranchPrefixInput.SetValue(draft.DefaultGitBranchPrefix)
}

func (m *SettingsModel) updatePlaceholders() {
	defaults := config.LoadDefaultSettings()

	getStringPlaceholder := func(globalVal, defaultVal string) string {
		if m.Scope == settingsScopeProject {
			if globalVal != "" {
				return globalVal
			}
		}
		return defaultVal
	}

	getIntPlaceholder := func(globalVal, defaultVal int) string {
		if m.Scope == settingsScopeProject {
			if globalVal > 0 {
				return fmt.Sprintf("%d", globalVal)
			}
		}
		if defaultVal > 0 {
			return fmt.Sprintf("%d", defaultVal)
		}
		return ""
	}

	m.DefaultGitBranchPrefixInput.Placeholder = getStringPlaceholder(m.GlobalDraft.DefaultGitBranchPrefix, defaults.DefaultGitBranchPrefix)
	m.GeminiModelInput.Placeholder = getStringPlaceholder(m.GlobalDraft.GeminiModel, "gemini model")
	m.OpenAIModelInput.Placeholder = getStringPlaceholder(m.GlobalDraft.OpenAIModel, "openai model")
	m.AnthropicModelInput.Placeholder = getStringPlaceholder(m.GlobalDraft.AnthropicModel, "anthropic model")
	m.AiderModelInput.Placeholder = getStringPlaceholder(m.GlobalDraft.AiderModel, "aider model")
	m.OllamaModelInput.Placeholder = getStringPlaceholder(m.GlobalDraft.OllamaModel, config.DefaultOllamaModel)
	m.OllamaHostInput.Placeholder = getStringPlaceholder(m.GlobalDraft.OllamaHost, "http://localhost:11434")
	m.KeepAliveInput.Placeholder = getStringPlaceholder(m.GlobalDraft.OllamaKeepAlive, defaults.OllamaKeepAlive)

	m.CacheTTLInput.Placeholder = getIntPlaceholder(m.GlobalDraft.CacheTTLMinutes, defaults.CacheTTLMinutes)
	m.MaxHandoffInput.Placeholder = getIntPlaceholder(m.GlobalDraft.MaxHandoffChars, defaults.MaxHandoffChars)
	m.MaxCallsInput.Placeholder = getIntPlaceholder(m.GlobalDraft.MaxModelCallsPerPhase, defaults.MaxModelCallsPerPhase)
	m.TokenBudgetInput.Placeholder = getIntPlaceholder(m.GlobalDraft.MaxTokenBudgetPerPhase, defaults.MaxTokenBudgetPerPhase)
	m.LLMInputInput.Placeholder = getIntPlaceholder(m.GlobalDraft.MaxLLMInputChars, defaults.MaxLLMInputChars)
	m.MaxRetainedConversationMsgInput.Placeholder = getIntPlaceholder(m.GlobalDraft.MaxRetainedConversationMessages, defaults.MaxRetainedConversationMessages)
	m.OllamaNumCtxInput.Placeholder = getIntPlaceholder(m.GlobalDraft.OllamaNumCtx, defaults.OllamaNumCtx)
	m.OllamaPredictInput.Placeholder = getIntPlaceholder(m.GlobalDraft.OllamaNumPredict, defaults.OllamaNumPredict)
}

func setIntInput(input *textinput.Model, val int) {
	if val > 0 {
		input.SetValue(fmt.Sprintf("%d", val))
	} else {
		input.SetValue("")
	}
}

func (m *SettingsModel) syncInputValuesToDraft() {
	for _, field := range []settingsTextField{
		fieldCustomLLM, fieldCacheTTL, fieldMaxHandoffChars, fieldMaxCalls, fieldTokenBudget, fieldLLMInputChars,
		fieldGeminiModel, fieldOpenAIModel, fieldAnthropicModel, fieldAiderModel, fieldOllamaModel, fieldOllamaHost,
		fieldOllamaKeepAlive, fieldOllamaNumCtx, fieldOllamaNumPredict, fieldDefaultGitBranchPrefix,
	} {
		if field == fieldCustomLLM {
			continue
		}
		m.applyTextFieldValue(field)
	}
}

func getLLMOptions(scope string) []string {
	options := getMilestoneRunnerOptions()
	if scope == settingsScopeGlobal {
		return options
	}
	return append(options, "inherit")
}

func getCurrentLLMOptIndex(val string, options []string) int {
	for i, opt := range options {
		if opt == "inherit" && val == "" {
			return i
		}
		if opt == val {
			return i
		}
	}
	return 0
}

func (m *SettingsModel) handleLeftRight(isLeft bool) {
	switch m.FocusIndex {
	case settingDefaultLLM:
		options := getLLMOptions(m.Scope)
		draft := m.getActiveDraft()
		curIdx := getCurrentLLMOptIndex(draft.DefaultLLM, options)
		newIdx := curIdx + 1
		if isLeft {
			newIdx = curIdx - 1
		}
		newOpt := options[(newIdx+len(options))%len(options)]
		switch newOpt {
		case "inherit":
			draft.DefaultLLM = ""
		default:
			draft.DefaultLLM = newOpt
		}
	case settingDefaultMode:
		m.toggleString("DefaultMode", isLeft, []string{"sandbox", "unrestricted"})
	case settingAutoGitBranch:
		m.toggleBool("AutoGitBranch", isLeft, true)
	case settingCreateMilestoneBranch:
		m.toggleBool("CreateMilestoneBranch", isLeft, false)
	case settingDisableBold:
		m.toggleBool("DisableBold", isLeft, true)
	case settingDisableRoundedBorders:
		m.toggleBool("DisableRoundedBorders", isLeft, false)
	case settingEnableContextCaching:
		m.toggleBool("EnableContextCaching", isLeft, false)
	case settingEnableCompactPhaseHandoffs:
		m.toggleBool("EnableCompactPhaseHandoffs", isLeft, true)
	case settingEnableCodexSessionResume:
		m.toggleBool("EnableCodexSessionResume", isLeft, false)
	}
}

func (m *SettingsModel) toggleString(field string, isLeft bool, values []string) {
	draft := m.getActiveDraft()
	current := draft.DefaultMode
	options := values
	if m.Scope == settingsScopeProject {
		options = append(append([]string{}, values...), "")
	}
	idx := 0
	for i, val := range options {
		if val == current {
			idx = i
			break
		}
	}
	if isLeft {
		idx = (idx - 1 + len(options)) % len(options)
	} else {
		idx = (idx + 1) % len(options)
	}
	if field == "DefaultMode" {
		draft.DefaultMode = options[idx]
	}
}

func (m *SettingsModel) toggleBool(field string, isLeft bool, globalDefault bool) {
	draft := m.getActiveDraft()
	get := func() *bool {
		switch field {
		case "AutoGitBranch":
			return draft.AutoGitBranch
		case "CreateMilestoneBranch":
			return draft.CreateMilestoneBranch
		case "DisableBold":
			return draft.DisableBold
		case "DisableRoundedBorders":
			return draft.DisableRoundedBorders
		case "EnableContextCaching":
			return draft.EnableContextCaching
		case "EnableCompactPhaseHandoffs":
			return draft.EnableCompactPhaseHandoffs
		case "EnableCodexSessionResume":
			return draft.EnableCodexSessionResume
		default:
			return nil
		}
	}
	set := func(v *bool) {
		switch field {
		case "AutoGitBranch":
			draft.AutoGitBranch = v
		case "CreateMilestoneBranch":
			draft.CreateMilestoneBranch = v
		case "DisableBold":
			draft.DisableBold = v
		case "DisableRoundedBorders":
			draft.DisableRoundedBorders = v
		case "EnableContextCaching":
			draft.EnableContextCaching = v
		case "EnableCompactPhaseHandoffs":
			draft.EnableCompactPhaseHandoffs = v
		case "EnableCodexSessionResume":
			draft.EnableCodexSessionResume = v
		}
	}

	current := get()
	if m.Scope == settingsScopeGlobal {
		val := globalDefault
		if current != nil {
			val = *current
		}
		next := !val
		set(&next)
		return
	}

	states := []*bool{boolPtr(true), boolPtr(false), nil}
	idx := 2
	if current != nil && *current {
		idx = 0
	} else if current != nil {
		idx = 1
	}
	if isLeft {
		idx = (idx - 1 + len(states)) % len(states)
	} else {
		idx = (idx + 1) % len(states)
	}
	set(states[idx])
}

func boolPtr(v bool) *bool {
	val := v
	return &val
}

func (m SettingsModel) handleSave() (SettingsModel, tea.Cmd) {
	m.syncInputValuesToDraft()
	if m.Scope == settingsScopeGlobal {
		m.GlobalDraft.DefaultLLM = normalizeMilestoneRunner(m.GlobalDraft.DefaultLLM)
		m.normalizeGlobalDraft()
	} else if m.ProjectDraft.DefaultLLM != "" {
		m.ProjectDraft.DefaultLLM = normalizeMilestoneRunner(m.ProjectDraft.DefaultLLM)
	}

	draft := m.getActiveDraft()
	if m.Scope == settingsScopeGlobal || draft.DefaultLLM != "" {
		if draft.DefaultLLM == "" && m.Scope == settingsScopeGlobal {
			m.ErrorMsg = "Default LLM cannot be empty"
			return m, nil
		}
		if draft.DefaultLLM != "" && !config.IsValidLLM(draft.DefaultLLM) {
			m.ErrorMsg = fmt.Sprintf("Invalid LLM runner / path: %s", draft.DefaultLLM)
			return m, nil
		}
	}

	var err error
	if m.Scope == settingsScopeGlobal {
		err = config.SaveGlobalSettings(m.GlobalDraft)
		if err == nil {
			m.GlobalOriginal = m.GlobalDraft
		}
	} else {
		err = config.SaveProjectSettings(m.ProjectDraft)
		if err == nil {
			m.ProjectOriginal = m.ProjectDraft
		}
	}
	if err != nil {
		m.ErrorMsg = fmt.Sprintf("Failed to save settings: %v", err)
		return m, nil
	}
	m.updatePlaceholders()
	return m, func() tea.Msg { return SettingsSavedMsg{Scope: m.Scope} }
}

func (m *SettingsModel) normalizeGlobalDraft() {
	defaults := config.LoadDefaultSettings()
	if m.GlobalDraft.DefaultLLM == "" {
		m.GlobalDraft.DefaultLLM = defaults.DefaultLLM
	}
	if m.GlobalDraft.DefaultMode == "" {
		m.GlobalDraft.DefaultMode = defaults.DefaultMode
	}
	if m.GlobalDraft.AutoGitBranch == nil {
		m.GlobalDraft.AutoGitBranch = defaults.AutoGitBranch
	}
	if m.GlobalDraft.CreateMilestoneBranch == nil {
		m.GlobalDraft.CreateMilestoneBranch = defaults.CreateMilestoneBranch
	}
	if m.GlobalDraft.DisableBold == nil {
		m.GlobalDraft.DisableBold = defaults.DisableBold
	}
	if m.GlobalDraft.DisableRoundedBorders == nil {
		m.GlobalDraft.DisableRoundedBorders = defaults.DisableRoundedBorders
	}
	if m.GlobalDraft.EnableContextCaching == nil {
		m.GlobalDraft.EnableContextCaching = defaults.EnableContextCaching
	}
	if m.GlobalDraft.EnableCompactPhaseHandoffs == nil {
		m.GlobalDraft.EnableCompactPhaseHandoffs = defaults.EnableCompactPhaseHandoffs
	}
	if m.GlobalDraft.EnableCodexSessionResume == nil {
		m.GlobalDraft.EnableCodexSessionResume = defaults.EnableCodexSessionResume
	}
	if m.GlobalDraft.CacheTTLMinutes <= 0 {
		m.GlobalDraft.CacheTTLMinutes = defaults.CacheTTLMinutes
	}
	if m.GlobalDraft.MaxHandoffChars <= 0 {
		m.GlobalDraft.MaxHandoffChars = defaults.MaxHandoffChars
	}
	if m.GlobalDraft.OllamaKeepAlive == "" {
		m.GlobalDraft.OllamaKeepAlive = defaults.OllamaKeepAlive
	}
	if m.GlobalDraft.OllamaNumCtx <= 0 {
		m.GlobalDraft.OllamaNumCtx = defaults.OllamaNumCtx
	}
	if m.GlobalDraft.OllamaNumPredict <= 0 {
		m.GlobalDraft.OllamaNumPredict = defaults.OllamaNumPredict
	}
	if m.GlobalDraft.MaxModelCallsPerPhase <= 0 {
		m.GlobalDraft.MaxModelCallsPerPhase = defaults.MaxModelCallsPerPhase
	}
	if m.GlobalDraft.MaxTokenBudgetPerPhase <= 0 {
		m.GlobalDraft.MaxTokenBudgetPerPhase = defaults.MaxTokenBudgetPerPhase
	}
	if m.GlobalDraft.MaxLLMInputChars <= 0 {
		m.GlobalDraft.MaxLLMInputChars = defaults.MaxLLMInputChars
	}
	if len(m.GlobalDraft.AgentGroups) == 0 {
		m.GlobalDraft.AgentGroups = defaults.AgentGroups
	}
}

func (m SettingsModel) View() string {
	if m.Width == 0 || m.Height == 0 {
		return "Loading..."
	}

	var sb strings.Builder
	helpWidth := m.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	if m.ShowDiscardPrompt {
		m.renderDiscardPrompt(&sb, helpWidth)
	} else {
		m.renderScreen(&sb, helpWidth)
	}
	return sb.String()
}

func (m SettingsModel) contentHeight() int {
	reserved := 9
	if m.ErrorMsg != "" {
		reserved += 2
	}
	if m.SuccessMsg != "" {
		reserved += 2
	}
	height := m.Height - reserved
	if height < 1 {
		return 1
	}
	return height
}

func (m *SettingsModel) ensureScrollVisible() {
	if m.SelectedGroup < 0 {
		m.SelectedGroup = 0
	}
	if m.SelectedGroup >= len(settingsGroups) {
		m.SelectedGroup = len(settingsGroups) - 1
	}
	height := m.contentHeight()
	if m.GroupScrollOffset > m.SelectedGroup {
		m.GroupScrollOffset = m.SelectedGroup
	}
	if m.SelectedGroup >= m.GroupScrollOffset+height {
		m.GroupScrollOffset = m.SelectedGroup - height + 1
	}
	if m.GroupScrollOffset < 0 {
		m.GroupScrollOffset = 0
	}

	if m.ActiveGroup < 0 {
		m.DetailScrollOffset = 0
		return
	}
	if m.ActiveGroup >= len(settingsGroups) {
		m.ActiveGroup = len(settingsGroups) - 1
	}
	rows := m.activeGroupRows()
	rowIdx := m.indexOfRow(rows, m.FocusIndex)
	if m.DetailScrollOffset > rowIdx {
		m.DetailScrollOffset = rowIdx
	}
	if rowIdx >= m.DetailScrollOffset+height {
		m.DetailScrollOffset = rowIdx - height + 1
	}
	if m.DetailScrollOffset < 0 {
		m.DetailScrollOffset = 0
	}
}

func (m SettingsModel) renderTabs() string {
	global := "Global"
	project := "Project"
	if m.Scope == settingsScopeGlobal {
		global = m.Styles.SuccessText.Render("[ Global ]")
		project = m.Styles.HelpStyle.Render("[ Project ]")
	} else {
		global = m.Styles.HelpStyle.Render("[ Global ]")
		project = m.Styles.SuccessText.Render("[ Project ]")
	}
	if !m.inGroupList() {
		return fmt.Sprintf("%s  %s", global, project)
	}
	return fmt.Sprintf("%s  %s\n%s", global, project, m.Styles.HelpStyle.Render("g Global  p Project"))
}

func (m SettingsModel) renderScreen(sb *strings.Builder, helpWidth int) {
	headerText := "SETTINGS / OPTIONS"
	if m.HasUnsavedChanges() {
		headerText += " [Modified]"
	}
	sb.WriteString(m.Styles.DetailHeader.Render(headerText) + "\n\n")
	sb.WriteString(m.renderTabs() + "\n\n")
	if m.ErrorMsg != "" {
		sb.WriteString(m.Styles.RenderError(m.ErrorMsg) + "\n\n")
	}
	if m.SuccessMsg != "" {
		sb.WriteString(m.Styles.RenderSuccess(m.SuccessMsg) + "\n\n")
	}

	if m.inGroupList() {
		m.renderGroupList(sb)
		sb.WriteString("\n")
		enterHelp := "Enter Open"
		selectedGroup := m.clampedSelectedGroup()
		switch settingsGroups[selectedGroup].Name {
		case "Save & Exit":
			enterHelp = "Enter Save & Exit"
		case "Discard & Exit":
			enterHelp = "Enter Discard"
		}
		sb.WriteString(renderCommandHelp(m.Styles, []string{"g/p Switch Tab", "↑/↓ Navigate", "PgUp/PgDn Scroll", enterHelp, "Esc Cancel", "q Quit", "Ctrl+C Quit"}, helpWidth))
		return
	}

	m.renderGroupDetail(sb)
	sb.WriteString("\n")
	sb.WriteString(renderCommandHelp(m.Styles, []string{"Esc Back", "b Back", "↑/↓ Navigate", "←/→ Change", "Enter Select/Save", "q Quit", "Ctrl+C Quit"}, helpWidth))
}

func (m SettingsModel) renderDiscardPrompt(sb *strings.Builder, helpWidth int) {
	headerText := "SETTINGS / OPTIONS"
	if m.HasUnsavedChanges() {
		headerText += " [Modified]"
	}
	sb.WriteString(m.Styles.DetailHeader.Render(headerText) + "\n\n")
	sb.WriteString(m.renderTabs() + "\n\n")

	title := m.Styles.RenderWarning("WARNING: Unsaved Changes")
	bodyLines := []string{
		title,
		"",
		"You have unsaved changes in your settings.",
		"Are you sure you want to discard these changes and exit?",
		"",
		renderCommandHelp(m.Styles, []string{"y Yes", "n No"}, helpWidth),
	}

	bodyHeight := m.contentHeight()
	body := m.Styles.Box.
		Width(helpWidth).
		Height(bodyHeight).
		Render(strings.Join(bodyLines, "\n"))
	sb.WriteString(body)
}

func (m SettingsModel) renderGroupList(sb *strings.Builder) {
	height := m.contentHeight()
	offset := clampScrollOffset(m.GroupScrollOffset, m.SelectedGroup, len(settingsGroups), height)
	end := minInt(len(settingsGroups), offset+height)
	sb.WriteString(m.Styles.DetailLabel.Render("Settings Groups") + "\n")
	for i := offset; i < end; i++ {
		group := settingsGroups[i]
		var line string
		if len(group.Rows) == 0 {
			line = group.Name
		} else {
			line = fmt.Sprintf("%s (%d)", group.Name, len(m.rowsForGroup(i)))
		}
		if i == m.SelectedGroup {
			sb.WriteString(m.Styles.TableSelectedRow.Render("> "+line) + "\n")
		} else {
			sb.WriteString(m.Styles.DetailValue.Render("  "+line) + "\n")
		}
	}
	if offset > 0 || end < len(settingsGroups) {
		sb.WriteString(m.Styles.HelpStyle.Render(fmt.Sprintf("Showing %d-%d of %d", offset+1, end, len(settingsGroups))) + "\n")
	}
}

func (m SettingsModel) renderGroupDetail(sb *strings.Builder) {
	rows := m.activeGroupRows()
	height := m.contentHeight()
	activeIdx := m.indexOfRow(rows, m.FocusIndex)
	offset := clampScrollOffset(m.DetailScrollOffset, activeIdx, len(rows), height)
	end := minInt(len(rows), offset+height)
	groupName := settingsGroups[m.ActiveGroup].Name
	sb.WriteString(m.Styles.DetailLabel.Render("Group: "+groupName) + "\n")
	for _, row := range rows[offset:end] {
		sb.WriteString(m.renderRow(row) + "\n")
	}
	if offset > 0 || end < len(rows) {
		sb.WriteString(m.Styles.HelpStyle.Render(fmt.Sprintf("Showing %d-%d of %d", offset+1, end, len(rows))) + "\n")
	}
}

func (m SettingsModel) groupNameForFocus(row int) string {
	for _, group := range settingsGroups {
		for _, groupRow := range group.Rows {
			if row == groupRow {
				return group.Name
			}
		}
	}
	return "Settings"
}

func (m SettingsModel) renderRow(row int) string {
	label := m.rowLabel(row)
	value := m.rowValue(row)
	if row == m.FocusIndex {
		label = m.Styles.DetailLabel.Underline(true).Render(label)
	} else {
		label = m.Styles.DetailValue.Bold(!m.Styles.NoBold).Render(label)
	}
	if row == settingSave || row == settingCancel {
		if row == m.FocusIndex {
			return m.Styles.TableSelectedRow.Render(" [ " + labelPlain(row) + " ] ")
		}
		return m.Styles.HelpStyle.Render(" [ " + labelPlain(row) + " ] ")
	}
	return fmt.Sprintf("%s: %s", label, value)
}

func labelPlain(row int) string {
	if row == settingSave {
		return "Save Settings"
	}
	return "Cancel"
}

func (m SettingsModel) rowLabel(row int) string {
	switch row {
	case settingDefaultLLM:
		return "Default LLM / Runner"
	case settingCustomLLM:
		return "Custom Runner Script Path"
	case settingDefaultMode:
		return "Default Execution Mode"
	case settingAutoGitBranch:
		return "Auto Git Branch"
	case settingCreateMilestoneBranch:
		return "Create new git branch for milestone"
	case settingDefaultGitBranchPrefix:
		return "Default Git Branch Prefix"
	case settingDisableBold:
		return "Disable Bold Text"
	case settingDisableRoundedBorders:
		return "Disable Rounded Borders"
	case settingEnableContextCaching:
		return "Enable LLM Context Caching"
	case settingEnableCompactPhaseHandoffs:
		return "Enable Compact Phase Handoffs"
	case settingEnableCodexSessionResume:
		return "Enable Codex Session Resume"
	case settingCacheTTLMinutes:
		return "Cache TTL Minutes"
	case settingMaxHandoffChars:
		return "Max Handoff Chars"
	case settingMaxModelCallsPerPhase:
		return "Max Model Calls Per Phase"
	case settingMaxTokenBudgetPerPhase:
		return "Max Token Budget Per Phase"
	case settingMaxLLMInputChars:
		return "Max LLM Input Chars"
	case settingMaxRetainedConversationMessages:
		return "Max Retained Conversation Messages"
	case settingGeminiModel:
		return "Gemini Model"
	case settingOpenAIModel:
		return "OpenAI Model"
	case settingAnthropicModel:
		return "Anthropic Model"
	case settingAiderModel:
		return "Aider Model"
	case settingOllamaModel:
		return "Ollama Model (via Aider)"
	case settingOllamaHost:
		return "Ollama Host (via Aider)"
	case settingOllamaKeepAlive:
		return "Ollama Keep-Alive Duration (via Aider)"
	case settingOllamaNumCtx:
		return "Ollama Num Ctx (via Aider)"
	case settingOllamaNumPredict:
		return "Ollama Num Predict (via Aider)"
	case settingAgentGroups:
		return "Agent Groups"
	case settingSave:
		return "Save Settings"
	default:
		return "Cancel"
	}
}

func (m SettingsModel) rowValue(row int) string {
	draft := m.getActiveDraft()
	switch row {
	case settingDefaultLLM:
		return m.renderOptions(getLLMOptions(m.Scope), draft.DefaultLLM, func(opt string) string {
			if opt == "inherit" {
				return fmt.Sprintf("inherit (global: %s)", defaultString(m.GlobalDraft.DefaultLLM, "codex"))
			}
			if opt == "ollama" {
				return "ollama via aider"
			}
			return opt
		}, func(opt string) bool {
			return (opt == "inherit" && draft.DefaultLLM == "") || opt == draft.DefaultLLM
		})
	case settingCustomLLM:
		return m.CustomLLMInput.View()
	case settingDefaultMode:
		options := []string{"sandbox", "unrestricted"}
		if m.Scope == settingsScopeProject {
			options = append(options, "")
		}
		return m.renderOptions(options, draft.DefaultMode, func(opt string) string {
			if opt == "" {
				return fmt.Sprintf("inherit (global: %s)", defaultString(m.GlobalDraft.DefaultMode, "sandbox"))
			}
			return opt
		}, func(opt string) bool { return opt == draft.DefaultMode })
	case settingAutoGitBranch:
		return m.renderBool(draft.AutoGitBranch, boolValue(m.GlobalDraft.AutoGitBranch, true))
	case settingCreateMilestoneBranch:
		return m.renderBool(draft.CreateMilestoneBranch, boolValue(m.GlobalDraft.CreateMilestoneBranch, false))
	case settingDefaultGitBranchPrefix:
		return m.renderStringInputWithInherit(m.DefaultGitBranchPrefixInput.View(), defaultString(m.GlobalDraft.DefaultGitBranchPrefix, "cyclestone/milestones/"))
	case settingDisableBold:
		return m.renderBool(draft.DisableBold, boolValue(m.GlobalDraft.DisableBold, true))
	case settingDisableRoundedBorders:
		return m.renderBool(draft.DisableRoundedBorders, boolValue(m.GlobalDraft.DisableRoundedBorders, false))
	case settingEnableContextCaching:
		return m.renderBool(draft.EnableContextCaching, boolValue(m.GlobalDraft.EnableContextCaching, false))
	case settingEnableCompactPhaseHandoffs:
		return m.renderBool(draft.EnableCompactPhaseHandoffs, boolValue(m.GlobalDraft.EnableCompactPhaseHandoffs, true))
	case settingEnableCodexSessionResume:
		return m.renderBool(draft.EnableCodexSessionResume, boolValue(m.GlobalDraft.EnableCodexSessionResume, false))
	case settingCacheTTLMinutes:
		return m.renderInputWithInherit(m.CacheTTLInput.View(), m.GlobalDraft.CacheTTLMinutes, "30")
	case settingMaxHandoffChars:
		return m.renderInputWithInherit(m.MaxHandoffInput.View(), m.GlobalDraft.MaxHandoffChars, "12000")
	case settingMaxModelCallsPerPhase:
		return m.renderInputWithInherit(m.MaxCallsInput.View(), m.GlobalDraft.MaxModelCallsPerPhase, "50")
	case settingMaxTokenBudgetPerPhase:
		return m.renderInputWithInherit(m.TokenBudgetInput.View(), m.GlobalDraft.MaxTokenBudgetPerPhase, "1000000")
	case settingMaxLLMInputChars:
		return m.renderInputWithInherit(m.LLMInputInput.View(), m.GlobalDraft.MaxLLMInputChars, "900000")
	case settingMaxRetainedConversationMessages:
		return m.renderInputWithInherit(m.MaxRetainedConversationMsgInput.View(), m.GlobalDraft.MaxRetainedConversationMessages, "8")
	case settingGeminiModel:
		return m.renderStringInputWithInherit(m.GeminiModelInput.View(), m.GlobalDraft.GeminiModel)
	case settingOpenAIModel:
		return m.renderStringInputWithInherit(m.OpenAIModelInput.View(), m.GlobalDraft.OpenAIModel)
	case settingAnthropicModel:
		return m.renderStringInputWithInherit(m.AnthropicModelInput.View(), m.GlobalDraft.AnthropicModel)
	case settingAiderModel:
		return m.renderStringInputWithInherit(m.AiderModelInput.View(), m.GlobalDraft.AiderModel)
	case settingOllamaModel:
		return m.renderStringInputWithInherit(m.OllamaModelInput.View(), defaultString(m.GlobalDraft.OllamaModel, config.DefaultOllamaModel))
	case settingOllamaHost:
		return m.renderStringInputWithInherit(m.OllamaHostInput.View(), defaultString(m.GlobalDraft.OllamaHost, "http://localhost:11434"))
	case settingOllamaKeepAlive:
		return m.renderStringInputWithInherit(m.KeepAliveInput.View(), defaultString(m.GlobalDraft.OllamaKeepAlive, "5m"))
	case settingOllamaNumCtx:
		return m.renderInputWithInherit(m.OllamaNumCtxInput.View(), m.GlobalDraft.OllamaNumCtx, "65536")
	case settingOllamaNumPredict:
		return m.renderInputWithInherit(m.OllamaPredictInput.View(), m.GlobalDraft.OllamaNumPredict, "8192")
	case settingAgentGroups:
		return "Enter to edit pipeline groups"
	default:
		return ""
	}
}

func (m SettingsModel) renderOptions(options []string, _ string, display func(string) string, active func(string) bool) string {
	rendered := make([]string, 0, len(options))
	for _, opt := range options {
		prefix := "( ) "
		style := m.Styles.HelpStyle
		if active(opt) {
			prefix = "(•) "
			style = m.Styles.SuccessText
		}
		rendered = append(rendered, style.Render(prefix+display(opt)))
	}
	return strings.Join(rendered, "    ")
}

func (m SettingsModel) renderBool(value *bool, global bool) string {
	options := []string{"yes", "no"}
	if m.Scope == settingsScopeProject {
		options = append(options, "inherit")
	}
	return m.renderOptions(options, "", func(opt string) string {
		if opt == "inherit" {
			return fmt.Sprintf("inherit (global: %s)", yesNo(global))
		}
		return opt
	}, func(opt string) bool {
		if opt == "inherit" {
			return value == nil
		}
		return value != nil && ((*value && opt == "yes") || (!*value && opt == "no"))
	})
}

func (m SettingsModel) renderInputWithInherit(view string, global int, fallback string) string {
	if m.Scope == settingsScopeProject {
		globalText := fallback
		if global > 0 {
			globalText = fmt.Sprintf("%d", global)
		}
		return fmt.Sprintf("%s  %s", view, m.Styles.HelpStyle.Render("(global: "+globalText+")"))
	}
	return view
}

func (m SettingsModel) renderStringInputWithInherit(view string, global string) string {
	if m.Scope == settingsScopeProject {
		return fmt.Sprintf("%s  %s", view, m.Styles.HelpStyle.Render("(global: "+defaultString(global, "unset")+")"))
	}
	return view
}

func boolValue(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func clampScrollOffset(offset, selected, total, height int) int {
	if height < 1 {
		height = 1
	}
	maxOffset := total - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > selected {
		offset = selected
	}
	if selected >= offset+height {
		offset = selected - height + 1
	}
	if offset < 0 {
		return 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func boolPtrsEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func settingsEqual(a, b config.Settings) bool {
	if a.DefaultLLM != b.DefaultLLM {
		return false
	}
	if a.DefaultMode != b.DefaultMode {
		return false
	}
	if !boolPtrsEqual(a.AutoGitBranch, b.AutoGitBranch) {
		return false
	}
	if !boolPtrsEqual(a.CreateMilestoneBranch, b.CreateMilestoneBranch) {
		return false
	}
	if !boolPtrsEqual(a.DisableBold, b.DisableBold) {
		return false
	}
	if !boolPtrsEqual(a.DisableRoundedBorders, b.DisableRoundedBorders) {
		return false
	}
	if a.DefaultGitBranchPrefix != b.DefaultGitBranchPrefix {
		return false
	}
	if a.GeminiModel != b.GeminiModel {
		return false
	}
	if a.OpenAIModel != b.OpenAIModel {
		return false
	}
	if a.AnthropicModel != b.AnthropicModel {
		return false
	}
	if a.AiderModel != b.AiderModel {
		return false
	}
	if a.OllamaModel != b.OllamaModel {
		return false
	}
	if a.OllamaHost != b.OllamaHost {
		return false
	}
	if !boolPtrsEqual(a.EnableContextCaching, b.EnableContextCaching) {
		return false
	}
	if !boolPtrsEqual(a.EnableCompactPhaseHandoffs, b.EnableCompactPhaseHandoffs) {
		return false
	}
	if !boolPtrsEqual(a.EnableCodexSessionResume, b.EnableCodexSessionResume) {
		return false
	}
	if a.CacheTTLMinutes != b.CacheTTLMinutes {
		return false
	}
	if a.MaxHandoffChars != b.MaxHandoffChars {
		return false
	}
	if a.OllamaKeepAlive != b.OllamaKeepAlive {
		return false
	}
	if a.OllamaNumCtx != b.OllamaNumCtx {
		return false
	}
	if a.OllamaNumPredict != b.OllamaNumPredict {
		return false
	}
	if a.MaxModelCallsPerPhase != b.MaxModelCallsPerPhase {
		return false
	}
	if a.MaxTokenBudgetPerPhase != b.MaxTokenBudgetPerPhase {
		return false
	}
	if a.MaxLLMInputChars != b.MaxLLMInputChars {
		return false
	}
	if a.MaxRetainedConversationMessages != b.MaxRetainedConversationMessages {
		return false
	}
	if len(a.AgentGroups) != len(b.AgentGroups) {
		return false
	}
	for i := range a.AgentGroups {
		if a.AgentGroups[i].Name != b.AgentGroups[i].Name {
			return false
		}
		if len(a.AgentGroups[i].AgentIDs) != len(b.AgentGroups[i].AgentIDs) {
			return false
		}
		for j := range a.AgentGroups[i].AgentIDs {
			if a.AgentGroups[i].AgentIDs[j] != b.AgentGroups[i].AgentIDs[j] {
				return false
			}
		}
	}
	return true
}

func (m SettingsModel) HasUnsavedChanges() bool {
	m.syncInputValuesToDraft()
	return !settingsEqual(m.GlobalDraft, m.GlobalOriginal) || !settingsEqual(m.ProjectDraft, m.ProjectOriginal)
}
