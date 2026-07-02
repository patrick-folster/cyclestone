package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestSettingsModelRendersScrollableGroupList(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Width = 120
	model.Height = 40
	model.Scope = "project"
	model.ProjectDraft.MaxLLMInputChars = 123456
	model.GlobalDraft.MaxLLMInputChars = 900000
	model.syncCustomInput()

	view := model.View()
	for _, want := range []string{
		"[ Global ]", "[ Project ]",
		"Runner Selection", "Execution Behavior", "UI Behavior", "Context/Cache Limits",
		"Aider Settings", "Ollama via Aider Settings",
		"Agent Groups", "Save & Exit", "Discard & Exit",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected settings view to contain %q, got:\n%s", want, view)
		}
	}
	for _, hiddenGroup := range []string{"Gemini Settings", "OpenAI Settings", "Anthropic Settings"} {
		if strings.Contains(view, hiddenGroup) {
			t.Fatalf("expected settings view to hide %q, got:\n%s", hiddenGroup, view)
		}
	}
	for _, hidden := range []string{"Default LLM / Runner", "Max LLM Input Chars", "Enter to edit pipeline groups"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("expected group list to hide detail row %q, got:\n%s", hidden, view)
		}
	}
	if strings.Contains(view, "Configuration Scope") {
		t.Fatalf("expected old configuration scope row to be removed, got:\n%s", view)
	}
}

func TestSettingsModelTabSwitchingChangesActiveDraft(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Scope = "project"
	model.ProjectDraft.AiderModel = "project-model"
	model.GlobalDraft.AiderModel = "global-model"
	model.syncCustomInput()

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if model.Scope != "global" {
		t.Fatalf("expected global tab, got %q", model.Scope)
	}
	if model.AiderModelInput.Value() != "global-model" {
		t.Fatalf("expected global draft input, got %q", model.AiderModelInput.Value())
	}

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if model.Scope != "project" {
		t.Fatalf("expected project tab, got %q", model.Scope)
	}
	if model.AiderModelInput.Value() != "project-model" {
		t.Fatalf("expected project draft input, got %q", model.AiderModelInput.Value())
	}
}

func TestSettingsModelSaveSyncsAllEditableTextInputsToProjectSettings(t *testing.T) {
	withTempSettingsDir(t, func() {
		model := NewSettingsModel(DefaultStyles(true, true))
		model.Scope = "project"
		model.ProjectDraft.DefaultLLM = "ollama"
		model.CacheTTLInput.SetValue("45")
		model.MaxHandoffInput.SetValue("6000")
		model.MaxCallsInput.SetValue("25")
		model.TokenBudgetInput.SetValue("123456")
		model.LLMInputInput.SetValue("750000")
		model.AiderModelInput.SetValue("aider-test-model")
		model.OllamaModelInput.SetValue("llama3.1")
		model.OllamaHostInput.SetValue("http://ollama:11434")
		model.OllamaNumCtxInput.SetValue("8192")
		model.OllamaPredictInput.SetValue("2048")
		model.DefaultGitBranchPrefixInput.SetValue("test-prefix/")

		updated, cmd := model.handleSave()
		if updated.ErrorMsg != "" {
			t.Fatalf("expected save without error, got %q", updated.ErrorMsg)
		}
		if cmd == nil {
			t.Fatalf("expected save command")
		}

		saved, err := config.LoadProjectSettings()
		if err != nil {
			t.Fatalf("failed to load saved project settings: %v", err)
		}
		if saved.DefaultLLM != "ollama" || saved.CacheTTLMinutes != 45 || saved.MaxHandoffChars != 6000 || saved.MaxModelCallsPerPhase != 25 ||
			saved.MaxTokenBudgetPerPhase != 123456 || saved.MaxLLMInputChars != 750000 ||
			saved.AiderModel != "aider-test-model" || saved.OllamaModel != "llama3.1" ||
			saved.OllamaHost != "http://ollama:11434" || saved.OllamaNumCtx != 8192 ||
			saved.OllamaNumPredict != 2048 || saved.DefaultGitBranchPrefix != "test-prefix/" {
			t.Fatalf("saved settings missing expected fields: %+v", saved)
		}
	})
}

func TestSettingsRunnerOptionsAreRestricted(t *testing.T) {
	if got, want := getLLMOptions(settingsScopeGlobal), []string{"codex", "agy", "aider", "ollama"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("global runner options = %v; want %v", got, want)
	}
	if got, want := getLLMOptions(settingsScopeProject), []string{"codex", "agy", "aider", "ollama", "inherit"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("project runner options = %v; want %v", got, want)
	}
}

func TestSettingsSaveNormalizesUnsupportedDefaultLLM(t *testing.T) {
	withTempSettingsDir(t, func() {
		model := NewSettingsModel(DefaultStyles(true, true))
		model.Scope = "project"
		model.ProjectDraft.DefaultLLM = "gemini"

		updated, _ := model.handleSave()
		if updated.ErrorMsg != "" {
			t.Fatalf("expected save without error, got %q", updated.ErrorMsg)
		}
		saved, err := config.LoadProjectSettings()
		if err != nil {
			t.Fatalf("failed to load project settings: %v", err)
		}
		if saved.DefaultLLM != "codex" {
			t.Fatalf("expected unsupported project runner to normalize to codex, got %q", saved.DefaultLLM)
		}
	})
}

func TestSettingsModelProjectSavePreservesInheritSentinels(t *testing.T) {
	withTempSettingsDir(t, func() {
		model := NewSettingsModel(DefaultStyles(true, true))
		model.Scope = "project"
		model.ProjectDraft.DefaultLLM = ""
		model.ProjectDraft.DefaultMode = ""
		model.ProjectDraft.AutoGitBranch = nil
		model.ProjectDraft.EnableContextCaching = nil
		model.ProjectDraft.EnableCompactPhaseHandoffs = nil
		model.ProjectDraft.EnableCodexSessionResume = nil
		model.CacheTTLInput.SetValue("")
		model.MaxHandoffInput.SetValue("")
		model.AiderModelInput.SetValue("")
		model.OllamaNumCtxInput.SetValue("")

		updated, _ := model.handleSave()
		if updated.ErrorMsg != "" {
			t.Fatalf("expected save without error, got %q", updated.ErrorMsg)
		}
		saved, err := config.LoadProjectSettings()
		if err != nil {
			t.Fatalf("failed to load saved project settings: %v", err)
		}
		if saved.DefaultLLM != "" || saved.DefaultMode != "" || saved.AutoGitBranch != nil || saved.EnableContextCaching != nil ||
			saved.EnableCompactPhaseHandoffs != nil || saved.EnableCodexSessionResume != nil ||
			saved.CacheTTLMinutes != 0 || saved.MaxHandoffChars != 0 || saved.AiderModel != "" || saved.OllamaNumCtx != 0 {
			t.Fatalf("expected inherit sentinels to remain empty/nil/zero, got %+v", saved)
		}
	})
}

func TestSettingsModelGlobalSaveNormalizesResolvedDefaults(t *testing.T) {
	withTempSettingsDir(t, func() {
		model := NewSettingsModel(DefaultStyles(true, true))
		model.Scope = "global"
		model.GlobalDraft.DefaultLLM = ""
		model.GlobalDraft.DefaultMode = ""
		model.GlobalDraft.AutoGitBranch = nil
		model.GlobalDraft.CacheTTLMinutes = 0
		model.CacheTTLInput.SetValue("")

		updated, _ := model.handleSave()
		if updated.ErrorMsg != "" {
			t.Fatalf("expected save without error, got %q", updated.ErrorMsg)
		}
		saved, err := config.LoadGlobalSettings()
		if err != nil {
			t.Fatalf("failed to load global settings: %v", err)
		}
		if saved.DefaultLLM == "" || saved.DefaultMode == "" || saved.AutoGitBranch == nil || saved.CacheTTLMinutes == 0 {
			t.Fatalf("expected normalized global defaults, got %+v", saved)
		}
	})
}

func TestSettingsModelAgentGroupsEntry(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.ActiveGroup = settingsGroupIndex(t, "Agent Groups")
	model.FocusIndex = settingAgentGroups
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected command to enter agent groups")
	}
	msg, ok := cmd().(ChangeScreenMsg)
	if !ok || msg.Screen != ScreenAgentGroups {
		t.Fatalf("expected ScreenAgentGroups change, got %#v", msg)
	}
}

func TestSettingsModelEnterOpensGroupDetailAndBackReturnsToList(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Width = 80
	model.Height = 20
	model.Scope = "project"
	ollamaGroup := settingsGroupIndex(t, "Ollama via Aider Settings")
	model.SelectedGroup = ollamaGroup

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if model.ActiveGroup != ollamaGroup {
		t.Fatalf("expected active group %d, got %d", ollamaGroup, model.ActiveGroup)
	}
	if model.FocusIndex != settingOllamaModel {
		t.Fatalf("expected first Ollama row focus, got %d", model.FocusIndex)
	}
	view := model.View()
	for _, want := range []string{"Group: Ollama via Aider Settings", "Ollama Model (via Aider)", "Ollama Host (via Aider)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected detail view to contain %q, got:\n%s", want, view)
		}
	}

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if model.ActiveGroup != -1 {
		t.Fatalf("expected return to group list, got active group %d", model.ActiveGroup)
	}
	if model.SelectedGroup != ollamaGroup {
		t.Fatalf("expected selected group preserved, got %d", model.SelectedGroup)
	}
}

func settingsGroupIndex(t *testing.T, name string) int {
	t.Helper()
	for i, group := range settingsGroups {
		if group.Name == name {
			return i
		}
	}
	t.Fatalf("could not find settings group %q", name)
	return -1
}

func TestSettingsModelDetailNavigationScrollsConstrainedHeight(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Width = 80
	model.Height = 10
	model.Scope = "project"
	model.SelectedGroup = 3
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if model.ActiveGroup != 3 {
		t.Fatalf("expected Context/Cache Limits detail")
	}

	for i := 0; i < 7; i++ {
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.DetailScrollOffset == 0 {
		t.Fatalf("expected detail scroll offset to move")
	}
	view := model.View()
	for _, want := range []string{"SETTINGS / OPTIONS", "[ Global ]", "[ Project ]", "Group: Context/Cache Limits", "Max LLM Input Chars", "Showing"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected constrained detail view to contain %q, got:\n%s", want, view)
		}
	}
}

func TestSettingsModelGroupListNavigationScrollsConstrainedHeight(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Width = 80
	model.Height = 10
	for i := 0; i < len(settingsGroups)-1; i++ {
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if model.GroupScrollOffset == 0 {
		t.Fatalf("expected group list scroll offset to move")
	}
	view := model.View()
	for _, want := range []string{"Exit", "Showing"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected constrained group list to contain %q, got:\n%s", want, view)
		}
	}
}

func TestSettingsModelTabSwitchingRestrictedToGroupList(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Scope = "project"
	model.ProjectDraft.AiderModel = "project-model"
	model.GlobalDraft.AiderModel = "global-model"
	model.syncCustomInput()

	// Enter a group details view (e.g. active group 1)
	model.ActiveGroup = 1
	model.FocusIndex = settingDefaultMode

	// Try tab switching via 'g' rune. It should be ignored.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if model.Scope != "project" {
		t.Fatalf("expected scope to remain project in details view, got %q", model.Scope)
	}

	// Try tab switching via 'p' rune. It should be ignored.
	model.Scope = "global"
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if model.Scope != "global" {
		t.Fatalf("expected scope to remain global in details view, got %q", model.Scope)
	}
}

func TestSettingsModelSpacebarToggling(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Scope = "project"

	// Go to details view of "Execution Behavior" group (active group 1)
	model.ActiveGroup = 1
	model.FocusIndex = settingAutoGitBranch

	// Ensure auto git branch starts as nil/inherit
	model.ProjectDraft.AutoGitBranch = nil

	// Press space. Since text input is not focused, it should cycle to true.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if model.ProjectDraft.AutoGitBranch == nil || !*model.ProjectDraft.AutoGitBranch {
		t.Fatalf("expected auto git branch to cycle to true, got %+v", model.ProjectDraft.AutoGitBranch)
	}

	// Press space again. Cycle to false.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if model.ProjectDraft.AutoGitBranch == nil || *model.ProjectDraft.AutoGitBranch {
		t.Fatalf("expected auto git branch to cycle to false, got %+v", model.ProjectDraft.AutoGitBranch)
	}

	// Now focus a text input field.
	model.FocusIndex = settingDefaultGitBranchPrefix
	model.updateTextInputFocus()
	if !model.IsTextInputFocused() {
		t.Fatalf("expected text input to be focused")
	}

	// Press space. It should type a space character into the field rather than cycling.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	val := model.DefaultGitBranchPrefixInput.Value()
	if val != " " {
		t.Fatalf("expected space char to be typed into input, got %q", val)
	}
}

func TestSettingsModelUnsavedChangesExitPrompt(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Width = 100
	model.Height = 30
	model.Scope = "project"

	// Assert no changes initially
	if model.HasUnsavedChanges() {
		t.Fatalf("expected no unsaved changes initially")
	}

	// Make a change
	model.AiderModelInput.SetValue("modified-model")

	// Assert modified state is detected
	if !model.HasUnsavedChanges() {
		t.Fatalf("expected unsaved changes to be detected")
	}

	// Assert modified indicator is rendered in the view
	view := model.View()
	if !strings.Contains(view, "[Modified]") {
		t.Fatalf("expected view to contain '[Modified]' header indicator, got:\n%s", view)
	}

	// Hit escape. It should show the discard warning prompt instead of exiting.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !model.ShowDiscardPrompt {
		t.Fatalf("expected discard prompt to be shown on escape")
	}
	if model.DiscardQuit {
		t.Fatalf("expected DiscardQuit to be false for esc (returns to dashboard)")
	}

	// Assert prompt warning is rendered in the view
	view = model.View()
	if !strings.Contains(view, "WARNING: Unsaved Changes") {
		t.Fatalf("expected view to contain warning prompt, got:\n%s", view)
	}

	// Press 'n' to cancel prompt
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if model.ShowDiscardPrompt {
		t.Fatalf("expected discard prompt to be dismissed after 'n'")
	}

	// Trigger prompt again
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Confirm discard with 'y' -> should send ChangeScreenMsg to dashboard
	var cmd tea.Cmd
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatalf("expected a command on confirming discard")
	}
	msg := cmd()
	changeMsg, ok := msg.(ChangeScreenMsg)
	if !ok || changeMsg.Screen != ScreenDashboard {
		t.Fatalf("expected screen change to dashboard, got %#v", msg)
	}
}

func TestSettingsModelMaxHandoffCharsFocus(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Scope = "project"
	model.ActiveGroup = 3 // Context/Cache Limits group
	model.FocusIndex = settingMaxHandoffChars

	model.updateTextInputFocus()
	if !model.MaxHandoffInput.Focused() {
		t.Fatalf("expected MaxHandoffInput to be focused")
	}
}

func withTempSettingsDir(t *testing.T, fn func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "settings_tui")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("USERPROFILE", oldUserProfile)
	}()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	projectRoot := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	fn()
}

func TestSettingsModelPlaceholderAndValueInheritance(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))

	// Set some global settings values
	model.GlobalDraft.AiderModel = "global-aider"
	model.GlobalDraft.CacheTTLMinutes = 45
	model.GlobalDraft.OllamaNumCtx = 16384

	// Keep project settings unset/empty
	model.ProjectDraft.AiderModel = ""
	model.ProjectDraft.CacheTTLMinutes = 0
	model.ProjectDraft.OllamaNumCtx = 0

	// Sync inputs and placeholders for project scope
	model.Scope = "project"
	model.syncCustomInput()
	model.updatePlaceholders()

	// Verify input values are empty for unset project settings
	if model.AiderModelInput.Value() != "" {
		t.Errorf("expected project scope AiderModel input to be empty, got %q", model.AiderModelInput.Value())
	}
	if model.CacheTTLInput.Value() != "" {
		t.Errorf("expected project scope CacheTTL input to be empty, got %q", model.CacheTTLInput.Value())
	}
	if model.OllamaNumCtxInput.Value() != "" {
		t.Errorf("expected project scope OllamaNumCtx input to be empty, got %q", model.OllamaNumCtxInput.Value())
	}

	// Verify placeholders correctly inherit global values
	if model.AiderModelInput.Placeholder != "global-aider" {
		t.Errorf("expected AiderModel placeholder to be 'global-aider', got %q", model.AiderModelInput.Placeholder)
	}
	if model.CacheTTLInput.Placeholder != "45" {
		t.Errorf("expected CacheTTL placeholder to be '45', got %q", model.CacheTTLInput.Placeholder)
	}
	if model.OllamaNumCtxInput.Placeholder != "16384" {
		t.Errorf("expected OllamaNumCtx placeholder to be '16384', got %q", model.OllamaNumCtxInput.Placeholder)
	}

	// Verify placeholder fallback to system default if global is also unset
	model.GlobalDraft.AiderModel = ""
	model.GlobalDraft.CacheTTLMinutes = 0
	model.GlobalDraft.OllamaNumCtx = 0
	model.updatePlaceholders()

	// "aider model" is the fallback default placeholder
	if model.AiderModelInput.Placeholder != "aider model" {
		t.Errorf("expected AiderModel placeholder to default to 'aider model', got %q", model.AiderModelInput.Placeholder)
	}
	// "30" is the CacheTTLMinutes system default
	if model.CacheTTLInput.Placeholder != "30" {
		t.Errorf("expected CacheTTL placeholder to default to '30', got %q", model.CacheTTLInput.Placeholder)
	}
	// "65536" is the OllamaNumCtx system default
	if model.OllamaNumCtxInput.Placeholder != "65536" {
		t.Errorf("expected OllamaNumCtx placeholder to default to '65536', got %q", model.OllamaNumCtxInput.Placeholder)
	}

	// Switch to global scope and verify placeholders show system defaults
	model.Scope = "global"
	model.syncCustomInput()
	model.updatePlaceholders()

	// At global scope, if a setting is unset, placeholder is system default
	if model.AiderModelInput.Placeholder != "aider model" {
		t.Errorf("expected global scope AiderModel placeholder to default to 'aider model', got %q", model.AiderModelInput.Placeholder)
	}
	if model.CacheTTLInput.Placeholder != "30" {
		t.Errorf("expected global scope CacheTTL placeholder to default to '30', got %q", model.CacheTTLInput.Placeholder)
	}
}

func TestSettingsModelSaveAndExitDirectAction(t *testing.T) {
	withTempSettingsDir(t, func() {
		model := NewSettingsModel(DefaultStyles(true, true))
		model.Width = 100
		model.Height = 30
		model.Scope = "project"

		// Find index of "Save & Exit"
		saveIdx := -1
		for i, g := range settingsGroups {
			if g.Name == "Save & Exit" {
				saveIdx = i
				break
			}
		}
		if saveIdx == -1 {
			t.Fatalf("could not find 'Save & Exit' group")
		}

		model.SelectedGroup = saveIdx
		model.ProjectDraft.AiderModel = "aider-test-direct"
		model.AiderModelInput.SetValue("aider-test-direct")

		// Press Enter on "Save & Exit"
		model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("expected command on Save & Exit")
		}
		msg := cmd()
		savedMsg, ok := msg.(SettingsSavedMsg)
		if !ok || savedMsg.Scope != "project" {
			t.Fatalf("expected SettingsSavedMsg, got %#v", msg)
		}

		saved, err := config.LoadProjectSettings()
		if err != nil {
			t.Fatalf("failed to load project settings: %v", err)
		}
		if saved.AiderModel != "aider-test-direct" {
			t.Fatalf("expected AiderModel to be saved, got %q", saved.AiderModel)
		}
	})
}

func TestSettingsModelDiscardAndExitDirectAction(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Width = 100
	model.Height = 30
	model.Scope = "project"

	// Find index of "Discard & Exit"
	discardIdx := -1
	for i, g := range settingsGroups {
		if g.Name == "Discard & Exit" {
			discardIdx = i
			break
		}
	}
	if discardIdx == -1 {
		t.Fatalf("could not find 'Discard & Exit' group")
	}

	model.SelectedGroup = discardIdx

	// 1. Without changes, press Enter -> should go to dashboard immediately
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected command on Discard & Exit")
	}
	msg := cmd()
	changeMsg, ok := msg.(ChangeScreenMsg)
	if !ok || changeMsg.Screen != ScreenDashboard {
		t.Fatalf("expected ScreenDashboard change, got %#v", msg)
	}

	// 2. With changes, press Enter -> should show discard prompt
	model.AiderModelInput.SetValue("unsaved-change")
	if !model.HasUnsavedChanges() {
		t.Fatalf("expected unsaved changes")
	}

	m2, cmd2 := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 != nil {
		t.Fatalf("expected no command immediately when changes exist, got %#v", cmd2())
	}
	if !m2.ShowDiscardPrompt {
		t.Fatalf("expected discard prompt to be shown")
	}
}

func TestSettingsModelDynamicHelpStrings(t *testing.T) {
	model := NewSettingsModel(DefaultStyles(true, true))
	model.Width = 100
	model.Height = 30
	model.Scope = "project"

	// Find indices
	saveIdx := -1
	discardIdx := -1
	otherIdx := -1
	for i, g := range settingsGroups {
		if g.Name == "Save & Exit" {
			saveIdx = i
		} else if g.Name == "Discard & Exit" {
			discardIdx = i
		} else if otherIdx == -1 {
			otherIdx = i
		}
	}

	// Check standard group help string
	model.SelectedGroup = otherIdx
	view := model.View()
	if !strings.Contains(view, "Enter  Open") {
		t.Errorf("expected 'Enter  Open' in help for standard group, view:\n%s", view)
	}

	// Check Save & Exit help string
	model.SelectedGroup = saveIdx
	view = model.View()
	if !strings.Contains(view, "Enter  Save & Exit") {
		t.Errorf("expected 'Enter  Save & Exit' in help, view:\n%s", view)
	}

	// Check Discard & Exit help string
	model.SelectedGroup = discardIdx
	view = model.View()
	if !strings.Contains(view, "Enter  Discard") {
		t.Errorf("expected 'Enter  Discard' in help, view:\n%s", view)
	}
}
