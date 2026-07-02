package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSettingsMergeAndSave(t *testing.T) {
	// Create temporary home and project directories
	tmpDir, err := os.MkdirTemp("", "settings_test")
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

	// Change working directory to a subdirectory of tmpDir to act as project root
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
	defer func() {
		_ = os.Chdir(oldWd)
	}()

	falseVal := false
	trueVal := true

	// 1. Initial defaults
	defaults := LoadMergedSettings()
	if defaults.DefaultLLM != "codex" {
		t.Errorf("expected default LLM 'codex', got '%s'", defaults.DefaultLLM)
	}
	if defaults.DefaultMode != "sandbox" {
		t.Errorf("expected default mode 'sandbox', got '%s'", defaults.DefaultMode)
	}
	if defaults.AutoGitBranch == nil || !*defaults.AutoGitBranch {
		t.Errorf("expected default AutoGitBranch true, got %v", defaults.AutoGitBranch)
	}
	if defaults.CreateMilestoneBranch == nil || *defaults.CreateMilestoneBranch {
		t.Errorf("expected default CreateMilestoneBranch false, got %v", defaults.CreateMilestoneBranch)
	}
	if defaults.EnableContextCaching == nil || *defaults.EnableContextCaching {
		t.Errorf("expected default EnableContextCaching false, got %v", defaults.EnableContextCaching)
	}
	if defaults.EnableCompactPhaseHandoffs == nil || !*defaults.EnableCompactPhaseHandoffs {
		t.Errorf("expected default EnableCompactPhaseHandoffs true, got %v", defaults.EnableCompactPhaseHandoffs)
	}
	if defaults.EnableCodexSessionResume == nil || *defaults.EnableCodexSessionResume {
		t.Errorf("expected default EnableCodexSessionResume false, got %v", defaults.EnableCodexSessionResume)
	}
	if defaults.CacheTTLMinutes != 30 {
		t.Errorf("expected default CacheTTLMinutes 30, got %d", defaults.CacheTTLMinutes)
	}
	if defaults.MaxHandoffChars != 12000 {
		t.Errorf("expected default MaxHandoffChars 12000, got %d", defaults.MaxHandoffChars)
	}
	if defaults.OllamaKeepAlive != "5m" {
		t.Errorf("expected default OllamaKeepAlive '5m', got '%s'", defaults.OllamaKeepAlive)
	}
	if defaults.MaxModelCallsPerPhase != 50 {
		t.Errorf("expected default MaxModelCallsPerPhase 50, got %d", defaults.MaxModelCallsPerPhase)
	}
	if defaults.MaxLLMInputChars != 900000 {
		t.Errorf("expected default MaxLLMInputChars 900000, got %d", defaults.MaxLLMInputChars)
	}
	if defaults.MaxRetainedConversationMessages != 8 {
		t.Errorf("expected default MaxRetainedConversationMessages 8, got %d", defaults.MaxRetainedConversationMessages)
	}
	if defaults.DefaultGitBranchPrefix != "cyclestone/milestones/" {
		t.Errorf("expected default Git branch prefix 'cyclestone/milestones/', got '%s'", defaults.DefaultGitBranchPrefix)
	}

	// 2. Save global settings
	globalSettings := Settings{
		DefaultLLM:            "agy",
		DefaultMode:           "sandbox",
		AutoGitBranch:         &falseVal,
		CreateMilestoneBranch: &trueVal,
	}
	if err := SaveGlobalSettings(globalSettings); err != nil {
		t.Fatalf("failed to save global settings: %v", err)
	}

	// 3. Merged settings should now reflect global settings
	merged := LoadMergedSettings()
	if merged.DefaultLLM != "agy" {
		t.Errorf("expected merged LLM 'agy' from global, got '%s'", merged.DefaultLLM)
	}
	if merged.AutoGitBranch == nil || *merged.AutoGitBranch {
		t.Errorf("expected merged AutoGitBranch false from global, got %v", merged.AutoGitBranch)
	}
	if merged.CreateMilestoneBranch == nil || !*merged.CreateMilestoneBranch {
		t.Errorf("expected merged CreateMilestoneBranch true from global, got %v", merged.CreateMilestoneBranch)
	}

	// 4. Save project settings (override global)
	projectSettings := Settings{
		DefaultLLM:             "codex",
		DefaultMode:            "unrestricted",
		AutoGitBranch:          &trueVal,
		CreateMilestoneBranch:  &falseVal,
		DefaultGitBranchPrefix: "custom/prefix/",
	}
	if err := SaveProjectSettings(projectSettings); err != nil {
		t.Fatalf("failed to save project settings: %v", err)
	}

	// 5. Merged settings should now reflect project overrides
	mergedOverride := LoadMergedSettings()
	if mergedOverride.DefaultLLM != "codex" {
		t.Errorf("expected merged LLM 'codex' from project override, got '%s'", mergedOverride.DefaultLLM)
	}
	if mergedOverride.DefaultMode != "unrestricted" {
		t.Errorf("expected merged mode 'unrestricted' from project override, got '%s'", mergedOverride.DefaultMode)
	}
	if mergedOverride.AutoGitBranch == nil || !*mergedOverride.AutoGitBranch {
		t.Errorf("expected merged AutoGitBranch true from project override, got %v", mergedOverride.AutoGitBranch)
	}
	if mergedOverride.CreateMilestoneBranch == nil || *mergedOverride.CreateMilestoneBranch {
		t.Errorf("expected merged CreateMilestoneBranch false from project override, got %v", mergedOverride.CreateMilestoneBranch)
	}
	if mergedOverride.DefaultGitBranchPrefix != "custom/prefix/" {
		t.Errorf("expected merged DefaultGitBranchPrefix 'custom/prefix/' from project override, got '%s'", mergedOverride.DefaultGitBranchPrefix)
	}

	// 6. Project settings unsetting / inheriting from global
	projectSettingsInherit := Settings{
		DefaultLLM:            "",
		DefaultMode:           "",
		AutoGitBranch:         nil,
		CreateMilestoneBranch: nil,
	}
	if err := SaveProjectSettings(projectSettingsInherit); err != nil {
		t.Fatalf("failed to save project settings: %v", err)
	}

	// Merged settings should inherit from global
	mergedInherit := LoadMergedSettings()
	if mergedInherit.DefaultLLM != "agy" { // inherited from global (which has agy)
		t.Errorf("expected inherited LLM 'agy' from global, got '%s'", mergedInherit.DefaultLLM)
	}
	if mergedInherit.DefaultMode != "sandbox" { // inherited from global (sandbox)
		t.Errorf("expected inherited Mode 'sandbox' from global, got '%s'", mergedInherit.DefaultMode)
	}
	if mergedInherit.AutoGitBranch == nil || *mergedInherit.AutoGitBranch { // inherited from global (falseVal)
		t.Errorf("expected inherited AutoGitBranch false from global, got %v", mergedInherit.AutoGitBranch)
	}
	if mergedInherit.CreateMilestoneBranch == nil || !*mergedInherit.CreateMilestoneBranch { // inherited from global (trueVal)
		t.Errorf("expected inherited CreateMilestoneBranch true from global, got %v", mergedInherit.CreateMilestoneBranch)
	}

	// 7. Verify AgentGroups merging and default fallback
	defaultsGroups := LoadMergedSettings()
	if len(defaultsGroups.AgentGroups) != 1 || defaultsGroups.AgentGroups[0].Name != "Default" {
		t.Errorf("expected only 'Default' agent group in defaults, got %d groups", len(defaultsGroups.AgentGroups))
	}

	// Add global agent groups
	globalSettingsWithGroups := globalSettings
	globalSettingsWithGroups.AgentGroups = []AgentGroup{
		{Name: "GlobalGroup", AgentIDs: []string{"developer"}},
		{Name: "Default", AgentIDs: []string{"qa"}}, // override default
	}
	if err := SaveGlobalSettings(globalSettingsWithGroups); err != nil {
		t.Fatalf("failed to save global settings with groups: %v", err)
	}

	mergedGlobalGroups := LoadMergedSettings()
	// Should have global overrides
	if len(mergedGlobalGroups.AgentGroups) != 2 {
		t.Errorf("expected 2 agent groups, got %d", len(mergedGlobalGroups.AgentGroups))
	}
	// "Default" must always be first
	if mergedGlobalGroups.AgentGroups[0].Name != "Default" || mergedGlobalGroups.AgentGroups[0].AgentIDs[0] != "qa" {
		t.Errorf("expected 'Default' overridden to 'qa' first, got %v", mergedGlobalGroups.AgentGroups[0])
	}

	// Add project override for group
	projectSettingsWithGroups := projectSettingsInherit
	projectSettingsWithGroups.AgentGroups = []AgentGroup{
		{Name: "ProjectGroup", AgentIDs: []string{"pm"}},
		{Name: "Default", AgentIDs: []string{"developer", "qa"}},
	}
	if err := SaveProjectSettings(projectSettingsWithGroups); err != nil {
		t.Fatalf("failed to save project settings with groups: %v", err)
	}

	mergedProjectGroups := LoadMergedSettings()
	// should have 3 groups: Default (project overridden), GlobalGroup, ProjectGroup
	if len(mergedProjectGroups.AgentGroups) != 3 {
		t.Errorf("expected 3 agent groups, got %d", len(mergedProjectGroups.AgentGroups))
	}
	// Default first
	if mergedProjectGroups.AgentGroups[0].Name != "Default" || len(mergedProjectGroups.AgentGroups[0].AgentIDs) != 2 {
		t.Errorf("expected 'Default' first, got %v", mergedProjectGroups.AgentGroups[0])
	}
}

func TestDefaultDisableBoldForVSCodeTerminal(t *testing.T) {
	withIsolatedSettingsEnvironment(t, func() {
		t.Setenv("TERM_PROGRAM", "vscode")
		t.Setenv("NO_BOLD", "")
		t.Setenv("CYCLESTONE_NO_BOLD", "")

		if !DefaultDisableBoldForEnvironment() {
			t.Fatal("expected VS Code terminal to default disable_bold to true when unset")
		}
	})
}

func TestSaveProjectSettingsAtCustomPath(t *testing.T) {
	root := t.TempDir()
	settingsPath := filepath.Join(root, "custom", "settings.yml")
	trueVal := true
	settings := Settings{
		DefaultLLM:            "openai",
		DefaultMode:           "sandbox",
		AutoGitBranch:         &trueVal,
		CreateMilestoneBranch: &trueVal,
	}

	if err := SaveProjectSettingsAt(settingsPath, settings); err != nil {
		t.Fatalf("SaveProjectSettingsAt failed: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read custom settings: %v", err)
	}
	if !strings.Contains(string(data), "default_llm: openai") || !strings.Contains(string(data), "create_milestone_branch: true") {
		t.Fatalf("custom settings did not contain expected fields:\n%s", data)
	}
}

func TestDefaultDisableBoldExplicitProjectSettingOverridesVSCode(t *testing.T) {
	withIsolatedSettingsEnvironment(t, func() {
		t.Setenv("TERM_PROGRAM", "vscode")
		t.Setenv("NO_BOLD", "")
		t.Setenv("CYCLESTONE_NO_BOLD", "")

		falseVal := false
		if err := SaveProjectSettings(Settings{DisableBold: &falseVal}); err != nil {
			t.Fatalf("failed to save project settings: %v", err)
		}

		if DefaultDisableBoldForEnvironment() {
			t.Fatal("expected explicit project disable_bold false to override VS Code default")
		}
	})
}

func TestDefaultDisableBoldEnvOverrideStillForcesNoBold(t *testing.T) {
	withIsolatedSettingsEnvironment(t, func() {
		t.Setenv("TERM_PROGRAM", "")
		t.Setenv("NO_BOLD", "1")
		t.Setenv("CYCLESTONE_NO_BOLD", "")

		falseVal := false
		if err := SaveProjectSettings(Settings{DisableBold: &falseVal}); err != nil {
			t.Fatalf("failed to save project settings: %v", err)
		}

		if !DefaultDisableBoldForEnvironment() {
			t.Fatal("expected NO_BOLD to force disable_bold true")
		}
	})
}

func TestOllamaGenerationSettingsSerializeAndMerge(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "settings_ollama_generation_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	projectRoot := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("failed to create project root: %v", err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("USERPROFILE", oldUserProfile)
		_ = os.Chdir(oldWd)
	}()

	settings := Settings{OllamaNumCtx: 32768, OllamaNumPredict: 4096}
	yamlBytes, err := yaml.Marshal(settings)
	if err != nil {
		t.Fatalf("failed to marshal yaml: %v", err)
	}
	yamlText := string(yamlBytes)
	if !strings.Contains(yamlText, "ollama_num_ctx: 32768") {
		t.Fatalf("expected yaml ollama_num_ctx key, got:\n%s", yamlText)
	}
	if !strings.Contains(yamlText, "ollama_num_predict: 4096") {
		t.Fatalf("expected yaml ollama_num_predict key, got:\n%s", yamlText)
	}

	jsonBytes, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	jsonText := string(jsonBytes)
	if !strings.Contains(jsonText, `"ollama_num_ctx":32768`) {
		t.Fatalf("expected json ollama_num_ctx key, got:\n%s", jsonText)
	}
	if !strings.Contains(jsonText, `"ollama_num_predict":4096`) {
		t.Fatalf("expected json ollama_num_predict key, got:\n%s", jsonText)
	}

	// Test YAML deserialization
	var unmarshaledYAML Settings
	if err := yaml.Unmarshal(yamlBytes, &unmarshaledYAML); err != nil {
		t.Fatalf("failed to unmarshal yaml: %v", err)
	}
	if unmarshaledYAML.OllamaNumCtx != 32768 || unmarshaledYAML.OllamaNumPredict != 4096 {
		t.Fatalf("unexpected unmarshaled yaml values: ctx=%d predict=%d", unmarshaledYAML.OllamaNumCtx, unmarshaledYAML.OllamaNumPredict)
	}

	// Test JSON deserialization
	var unmarshaledJSON Settings
	if err := json.Unmarshal(jsonBytes, &unmarshaledJSON); err != nil {
		t.Fatalf("failed to unmarshal json: %v", err)
	}
	if unmarshaledJSON.OllamaNumCtx != 32768 || unmarshaledJSON.OllamaNumPredict != 4096 {
		t.Fatalf("unexpected unmarshaled json values: ctx=%d predict=%d", unmarshaledJSON.OllamaNumCtx, unmarshaledJSON.OllamaNumPredict)
	}

	if err := SaveGlobalSettings(Settings{OllamaNumCtx: 8192, OllamaNumPredict: 1024}); err != nil {
		t.Fatalf("failed to save global settings: %v", err)
	}
	merged := LoadMergedSettings()
	if merged.OllamaNumCtx != 8192 || merged.OllamaNumPredict != 1024 {
		t.Fatalf("expected global ollama options, got ctx=%d predict=%d", merged.OllamaNumCtx, merged.OllamaNumPredict)
	}

	if err := SaveProjectSettings(Settings{OllamaNumPredict: 2048}); err != nil {
		t.Fatalf("failed to save project settings: %v", err)
	}
	merged = LoadMergedSettings()
	if merged.OllamaNumCtx != 8192 {
		t.Fatalf("expected project unset ollama_num_ctx to inherit global, got %d", merged.OllamaNumCtx)
	}
	if merged.OllamaNumPredict != 2048 {
		t.Fatalf("expected project ollama_num_predict override, got %d", merged.OllamaNumPredict)
	}

	if err := SaveProjectSettings(Settings{}); err != nil {
		t.Fatalf("failed to save empty project settings: %v", err)
	}
	merged = LoadMergedSettings()
	if merged.OllamaNumCtx != 8192 || merged.OllamaNumPredict != 1024 {
		t.Fatalf("expected empty project settings to preserve global values, got ctx=%d predict=%d", merged.OllamaNumCtx, merged.OllamaNumPredict)
	}
}

func TestLLMValidationAndConfig(t *testing.T) {
	// 1. Verify IsValidLLM validation logic
	validCases := []string{
		"codex", "agy", "aider", "gemini", "openai", "anthropic", "ollama", "ollama_api",
		"./script.sh", "/absolute/path/to/runner", "custom_script.py",
	}
	for _, c := range validCases {
		if !IsValidLLM(c) {
			t.Errorf("expected '%s' to be valid", c)
		}
	}

	invalidCases := []string{
		"invalid", "some_binary", "another-binary",
	}
	for _, c := range invalidCases {
		if IsValidLLM(c) {
			t.Errorf("expected '%s' to be invalid", c)
		}
	}

	// 2. Verify settings loading/saving with new API models
	tmpDir, err := os.MkdirTemp("", "settings_api_test")
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

	oldWd, _ := os.Getwd()
	projectRoot := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(projectRoot, 0755)
	_ = os.Chdir(projectRoot)
	defer func() {
		_ = os.Chdir(oldWd)
	}()

	trueVal := true
	falseVal := false
	s := Settings{
		DefaultLLM:                      "gemini",
		GeminiModel:                     "gemini-2.5-pro",
		OpenAIModel:                     "gpt-4-turbo",
		AnthropicModel:                  "claude-3-opus",
		AiderModel:                      "aider-claude-3-5",
		OllamaModel:                     "mistral",
		OllamaHost:                      "http://127.0.0.1:11434",
		EnableContextCaching:            &trueVal,
		EnableCompactPhaseHandoffs:      &falseVal,
		EnableCodexSessionResume:        &trueVal,
		CacheTTLMinutes:                 45,
		MaxHandoffChars:                 6000,
		OllamaKeepAlive:                 "15m",
		MaxModelCallsPerPhase:           25,
		MaxLLMInputChars:                750000,
		MaxRetainedConversationMessages: 12,
	}

	if err := SaveProjectSettings(s); err != nil {
		t.Fatalf("failed to save project settings: %v", err)
	}

	merged := LoadMergedSettings()
	if merged.DefaultLLM != "gemini" {
		t.Errorf("expected DefaultLLM 'gemini', got '%s'", merged.DefaultLLM)
	}
	if merged.GeminiModel != "gemini-2.5-pro" {
		t.Errorf("expected GeminiModel 'gemini-2.5-pro', got '%s'", merged.GeminiModel)
	}
	if merged.OpenAIModel != "gpt-4-turbo" {
		t.Errorf("expected OpenAIModel 'gpt-4-turbo', got '%s'", merged.OpenAIModel)
	}
	if merged.AnthropicModel != "claude-3-opus" {
		t.Errorf("expected AnthropicModel 'claude-3-opus', got '%s'", merged.AnthropicModel)
	}
	if merged.AiderModel != "aider-claude-3-5" {
		t.Errorf("expected AiderModel 'aider-claude-3-5', got '%s'", merged.AiderModel)
	}
	if merged.OllamaModel != "mistral" {
		t.Errorf("expected OllamaModel 'mistral', got '%s'", merged.OllamaModel)
	}
	if merged.OllamaHost != "http://127.0.0.1:11434" {
		t.Errorf("expected OllamaHost 'http://127.0.0.1:11434', got '%s'", merged.OllamaHost)
	}
	if merged.EnableContextCaching == nil || !*merged.EnableContextCaching {
		t.Errorf("expected EnableContextCaching true, got %v", merged.EnableContextCaching)
	}
	if merged.EnableCompactPhaseHandoffs == nil || *merged.EnableCompactPhaseHandoffs {
		t.Errorf("expected EnableCompactPhaseHandoffs false, got %v", merged.EnableCompactPhaseHandoffs)
	}
	if merged.EnableCodexSessionResume == nil || !*merged.EnableCodexSessionResume {
		t.Errorf("expected EnableCodexSessionResume true, got %v", merged.EnableCodexSessionResume)
	}
	if merged.CacheTTLMinutes != 45 {
		t.Errorf("expected CacheTTLMinutes 45, got %d", merged.CacheTTLMinutes)
	}
	if merged.MaxHandoffChars != 6000 {
		t.Errorf("expected MaxHandoffChars 6000, got %d", merged.MaxHandoffChars)
	}
	if merged.OllamaKeepAlive != "15m" {
		t.Errorf("expected OllamaKeepAlive '15m', got '%s'", merged.OllamaKeepAlive)
	}
	if merged.MaxModelCallsPerPhase != 25 {
		t.Errorf("expected MaxModelCallsPerPhase 25, got %d", merged.MaxModelCallsPerPhase)
	}
	if merged.MaxLLMInputChars != 750000 {
		t.Errorf("expected MaxLLMInputChars 750000, got %d", merged.MaxLLMInputChars)
	}
	if merged.MaxRetainedConversationMessages != 12 {
		t.Errorf("expected MaxRetainedConversationMessages 12, got %d", merged.MaxRetainedConversationMessages)
	}
}

func TestDefaultDisableRoundedBordersForVSCodeTerminal(t *testing.T) {
	withIsolatedSettingsEnvironment(t, func() {
		t.Setenv("TERM_PROGRAM", "vscode")

		if !DefaultDisableRoundedBordersForEnvironment() {
			t.Fatal("expected VS Code terminal to default disable_rounded_borders to true when unset")
		}
	})
}

func TestDefaultDisableRoundedBordersExplicitProjectSettingOverridesVSCode(t *testing.T) {
	withIsolatedSettingsEnvironment(t, func() {
		t.Setenv("TERM_PROGRAM", "vscode")

		falseVal := false
		if err := SaveProjectSettings(Settings{DisableRoundedBorders: &falseVal}); err != nil {
			t.Fatalf("failed to save project settings: %v", err)
		}

		if DefaultDisableRoundedBordersForEnvironment() {
			t.Fatal("expected explicit project disable_rounded_borders false to override VS Code default")
		}
	})
}

func withIsolatedSettingsEnvironment(t *testing.T, fn func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "settings_env")
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
