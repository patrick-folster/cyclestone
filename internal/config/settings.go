package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultOllamaModel = "glm-5.2:cloud"

// AgentGroup represents a serialized pipeline group of agents.
type AgentGroup struct {
	Name     string   `yaml:"name" json:"name"`
	AgentIDs []string `yaml:"agent_ids" json:"agent_ids"`
}

// AgentInstructionsSettings controls the durable instruction file loaded into
// agent prompts and whether agents may propose updates for human review.
type AgentInstructionsSettings struct {
	File             string `yaml:"file,omitempty" json:"file,omitempty"`
	ProposeUpdates   *bool  `yaml:"propose_updates,omitempty" json:"propose_updates,omitempty"`
	AutoApplyUpdates *bool  `yaml:"auto_apply_updates,omitempty" json:"auto_apply_updates,omitempty"`
}

// Settings represents global and project configurations.
type Settings struct {
	DefaultLLM             string `yaml:"default_llm,omitempty" json:"default_llm,omitempty"`                         // "codex", "agy", "aider", "ollama", "ollama-codex", or "" (inherit)
	DefaultMode            string `yaml:"default_mode,omitempty" json:"default_mode,omitempty"`                       // "sandbox", "unrestricted", or "" (inherit)
	AutoGitBranch          *bool  `yaml:"auto_git_branch,omitempty" json:"auto_git_branch,omitempty"`                 // pointer to bool, nil if unset/inherit
	CreateMilestoneBranch  *bool  `yaml:"create_milestone_branch,omitempty" json:"create_milestone_branch,omitempty"` // pointer to bool, nil if unset/inherit
	DisableBold            *bool  `yaml:"disable_bold,omitempty" json:"disable_bold,omitempty"`                       // pointer to bool, nil if unset/inherit
	DisableRoundedBorders  *bool  `yaml:"disable_rounded_borders,omitempty" json:"disable_rounded_borders,omitempty"` // pointer to bool, nil if unset/inherit
	DefaultGitBranchPrefix string `yaml:"default_git_branch_prefix,omitempty" json:"default_git_branch_prefix,omitempty"`

	AiderModel                      string                    `yaml:"aider_model,omitempty" json:"aider_model,omitempty"`
	OllamaModel                     string                    `yaml:"ollama_model,omitempty" json:"ollama_model,omitempty"`
	OllamaCodexModel                string                    `yaml:"ollama_codex_model,omitempty" json:"ollama_codex_model,omitempty"`
	OllamaHost                      string                    `yaml:"ollama_host,omitempty" json:"ollama_host,omitempty"`
	EnableContextCaching            *bool                     `yaml:"enable_context_caching,omitempty" json:"enable_context_caching,omitempty"`
	EnableCompactPhaseHandoffs      *bool                     `yaml:"enable_compact_phase_handoffs,omitempty" json:"enable_compact_phase_handoffs,omitempty"`
	EnableCodexSessionResume        *bool                     `yaml:"enable_codex_session_resume,omitempty" json:"enable_codex_session_resume,omitempty"`
	CacheTTLMinutes                 int                       `yaml:"cache_ttl_minutes,omitempty" json:"cache_ttl_minutes,omitempty"`
	MaxHandoffChars                 int                       `yaml:"max_handoff_chars,omitempty" json:"max_handoff_chars,omitempty"`
	OllamaNumCtx                    int                       `yaml:"ollama_num_ctx,omitempty" json:"ollama_num_ctx,omitempty"`
	OllamaNumPredict                int                       `yaml:"ollama_num_predict,omitempty" json:"ollama_num_predict,omitempty"`
	MaxModelCallsPerPhase           int                       `yaml:"max_model_calls_per_phase,omitempty" json:"max_model_calls_per_phase,omitempty"`
	MaxTokenBudgetPerPhase          int                       `yaml:"max_token_budget_per_phase,omitempty" json:"max_token_budget_per_phase,omitempty"`
	MaxLLMInputChars                int                       `yaml:"max_llm_input_chars,omitempty" json:"max_llm_input_chars,omitempty"`
	MaxRetainedConversationMessages int                       `yaml:"max_retained_conversation_messages,omitempty" json:"max_retained_conversation_messages,omitempty"`
	AgentGroups                     []AgentGroup              `yaml:"agent_groups,omitempty" json:"agent_groups,omitempty"`
	AgentInstructions               AgentInstructionsSettings `yaml:"agent_instructions,omitempty" json:"agent_instructions,omitempty"`
}

// IsValidLLM checks if a given LLM runner name is supported.
func IsValidLLM(val string) bool {
	switch val {
	case "codex", "agy", "aider", "ollama", "ollama-codex":
		return true
	}
	return false
}

// PredefinedDefaultGroup is the built-in default agent group containing pm, developer, and qa.
var PredefinedDefaultGroup = AgentGroup{
	Name:     "Default",
	AgentIDs: []string{"pm", "developer", "qa", "recommender"},
}

// MergeAgentGroups merges global and project agent groups, project groups overriding global ones with same name.
func MergeAgentGroups(global, project []AgentGroup) []AgentGroup {
	projectMap := make(map[string]bool)
	for _, pg := range project {
		projectMap[strings.ToLower(pg.Name)] = true
	}

	var merged []AgentGroup
	for _, gg := range global {
		if !projectMap[strings.ToLower(gg.Name)] {
			merged = append(merged, gg)
		}
	}
	merged = append(merged, project...)
	return merged
}

// LoadDefaultSettings returns the default settings object.
func LoadDefaultSettings() Settings {
	// Each *bool field MUST have its own independent variable.
	// yaml.v3 unmarshals by writing through the existing pointer (it does not allocate a new
	// one), so if two fields share the same pointer, unmarshaling one silently overwrites the other.
	autoGitBranch := true
	createMilestoneBranch := false
	disableBold := false
	disableRoundedBorders := false
	enableContextCaching := false
	enableCompactPhaseHandoffs := true
	enableCodexSessionResume := false
	proposeInstructionUpdates := true
	autoApplyInstructionUpdates := false
	return Settings{
		DefaultLLM:                      "codex",
		DefaultMode:                     "sandbox",
		AutoGitBranch:                   &autoGitBranch,
		CreateMilestoneBranch:           &createMilestoneBranch,
		DisableBold:                     &disableBold,
		DisableRoundedBorders:           &disableRoundedBorders,
		DefaultGitBranchPrefix:          "cyclestone/milestones/",
		EnableContextCaching:            &enableContextCaching,
		EnableCompactPhaseHandoffs:      &enableCompactPhaseHandoffs,
		EnableCodexSessionResume:        &enableCodexSessionResume,
		CacheTTLMinutes:                 30,
		MaxHandoffChars:                 12000,
		OllamaNumCtx:                    -1,
		OllamaNumPredict:                -1,
		MaxModelCallsPerPhase:           50,
		MaxTokenBudgetPerPhase:          1000000,
		MaxLLMInputChars:                900000,
		MaxRetainedConversationMessages: 8,
		AgentGroups:                     []AgentGroup{PredefinedDefaultGroup},
		AgentInstructions: AgentInstructionsSettings{
			File:             "AGENTS.md",
			ProposeUpdates:   &proposeInstructionUpdates,
			AutoApplyUpdates: &autoApplyInstructionUpdates,
		},
	}
}

func normalizeAgentInstructionsSettings(s *Settings) {
	if strings.TrimSpace(s.AgentInstructions.File) == "" {
		s.AgentInstructions.File = "AGENTS.md"
	}
	if s.AgentInstructions.ProposeUpdates == nil {
		trueVal := true
		s.AgentInstructions.ProposeUpdates = &trueVal
	}
	if s.AgentInstructions.AutoApplyUpdates == nil {
		falseVal := false
		s.AgentInstructions.AutoApplyUpdates = &falseVal
	}
}

func mergeAgentInstructionsSettings(base *Settings, override AgentInstructionsSettings) {
	if strings.TrimSpace(override.File) != "" {
		base.AgentInstructions.File = override.File
	}
	if override.ProposeUpdates != nil {
		base.AgentInstructions.ProposeUpdates = override.ProposeUpdates
	}
	if override.AutoApplyUpdates != nil {
		base.AgentInstructions.AutoApplyUpdates = override.AutoApplyUpdates
	}
	normalizeAgentInstructionsSettings(base)
}

// LoadGlobalSettings reads the global settings.yml if it exists.
func LoadGlobalSettings() (Settings, error) {
	s := LoadDefaultSettings()
	dir := GetGlobalConfigDir()
	if dir == "" {
		return s, nil
	}
	path := filepath.Join(dir, "settings.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	err = yaml.Unmarshal(data, &s)
	return s, err
}

// LoadProjectSettings reads the local project settings.yml if it exists.
func LoadProjectSettings() (Settings, error) {
	// Project settings draft can have empty/nil fields, so start with an empty struct.
	var s Settings
	path := filepath.Join(".cyclestone", "settings.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	err = yaml.Unmarshal(data, &s)
	return s, err
}

// LoadMergedSettings merges global and project configurations (project overrides global).
func LoadMergedSettings() Settings {
	s := LoadDefaultSettings()

	// Load global first
	global, err := LoadGlobalSettings()
	if err == nil {
		s = global
	}

	// Validate global settings values (global cannot have empty values/nil)
	if !IsValidLLM(s.DefaultLLM) {
		s.DefaultLLM = "codex"
	}
	if s.DefaultMode != "sandbox" && s.DefaultMode != "unrestricted" {
		s.DefaultMode = "sandbox"
	}
	if s.AutoGitBranch == nil {
		trueVal := true
		s.AutoGitBranch = &trueVal
	}
	if s.CreateMilestoneBranch == nil {
		falseVal := false
		s.CreateMilestoneBranch = &falseVal
	}
	if s.DisableBold == nil {
		falseVal := false
		s.DisableBold = &falseVal
	}
	if s.DisableRoundedBorders == nil {
		falseVal := false
		s.DisableRoundedBorders = &falseVal
	}
	if s.EnableContextCaching == nil {
		falseVal := false
		s.EnableContextCaching = &falseVal
	}
	if s.EnableCompactPhaseHandoffs == nil {
		trueVal := true
		s.EnableCompactPhaseHandoffs = &trueVal
	}
	if s.EnableCodexSessionResume == nil {
		falseVal := false
		s.EnableCodexSessionResume = &falseVal
	}
	normalizeAgentInstructionsSettings(&s)
	if s.DefaultGitBranchPrefix == "" {
		s.DefaultGitBranchPrefix = "cyclestone/milestones/"
	}
	if s.CacheTTLMinutes <= 0 {
		s.CacheTTLMinutes = 30
	}
	if s.MaxHandoffChars <= 0 {
		s.MaxHandoffChars = 12000
	}
	if s.MaxModelCallsPerPhase <= 0 {
		s.MaxModelCallsPerPhase = 50
	}
	if s.MaxTokenBudgetPerPhase <= 0 {
		s.MaxTokenBudgetPerPhase = 1000000
	}
	if s.MaxLLMInputChars <= 0 {
		s.MaxLLMInputChars = 900000
	}
	if s.MaxRetainedConversationMessages <= 0 {
		s.MaxRetainedConversationMessages = 8
	}

	// Load project next and override only if explicitly set
	var projectGroups []AgentGroup
	projectPath := filepath.Join(".cyclestone", "settings.yml")
	if _, err := os.Stat(projectPath); err == nil {
		projectData, err := os.ReadFile(projectPath)
		if err == nil {
			var projectSettings Settings
			if err := yaml.Unmarshal(projectData, &projectSettings); err == nil {
				if projectSettings.DefaultLLM != "" {
					s.DefaultLLM = projectSettings.DefaultLLM
				}
				if projectSettings.DefaultMode != "" {
					s.DefaultMode = projectSettings.DefaultMode
				}
				if projectSettings.AutoGitBranch != nil {
					s.AutoGitBranch = projectSettings.AutoGitBranch
				}
				if projectSettings.CreateMilestoneBranch != nil {
					s.CreateMilestoneBranch = projectSettings.CreateMilestoneBranch
				}
				if projectSettings.DisableBold != nil {
					s.DisableBold = projectSettings.DisableBold
				}
				if projectSettings.DisableRoundedBorders != nil {
					s.DisableRoundedBorders = projectSettings.DisableRoundedBorders
				}
				if projectSettings.AiderModel != "" {
					s.AiderModel = projectSettings.AiderModel
				}
				if projectSettings.OllamaModel != "" {
					s.OllamaModel = projectSettings.OllamaModel
				}
				if projectSettings.OllamaCodexModel != "" {
					s.OllamaCodexModel = projectSettings.OllamaCodexModel
				}
				if projectSettings.OllamaHost != "" {
					s.OllamaHost = projectSettings.OllamaHost
				}
				if projectSettings.EnableContextCaching != nil {
					s.EnableContextCaching = projectSettings.EnableContextCaching
				}
				if projectSettings.EnableCompactPhaseHandoffs != nil {
					s.EnableCompactPhaseHandoffs = projectSettings.EnableCompactPhaseHandoffs
				}
				if projectSettings.EnableCodexSessionResume != nil {
					s.EnableCodexSessionResume = projectSettings.EnableCodexSessionResume
				}
				mergeAgentInstructionsSettings(&s, projectSettings.AgentInstructions)
				if projectSettings.CacheTTLMinutes != 0 {
					s.CacheTTLMinutes = projectSettings.CacheTTLMinutes
				}
				if projectSettings.MaxHandoffChars != 0 {
					s.MaxHandoffChars = projectSettings.MaxHandoffChars
				}
				if projectSettings.OllamaNumCtx != 0 {
					s.OllamaNumCtx = projectSettings.OllamaNumCtx
				}
				if projectSettings.OllamaNumPredict != 0 {
					s.OllamaNumPredict = projectSettings.OllamaNumPredict
				}
				if projectSettings.MaxModelCallsPerPhase != 0 {
					s.MaxModelCallsPerPhase = projectSettings.MaxModelCallsPerPhase
				}
				if projectSettings.MaxTokenBudgetPerPhase != 0 {
					s.MaxTokenBudgetPerPhase = projectSettings.MaxTokenBudgetPerPhase
				}
				if projectSettings.DefaultGitBranchPrefix != "" {
					s.DefaultGitBranchPrefix = projectSettings.DefaultGitBranchPrefix
				}
				if projectSettings.MaxLLMInputChars != 0 {
					s.MaxLLMInputChars = projectSettings.MaxLLMInputChars
				}
				if projectSettings.MaxRetainedConversationMessages != 0 {
					s.MaxRetainedConversationMessages = projectSettings.MaxRetainedConversationMessages
				}
				projectGroups = projectSettings.AgentGroups
			}
		}
	}

	var globalGroups []AgentGroup
	if err == nil {
		globalGroups = global.AgentGroups
	}
	s.AgentGroups = MergeAgentGroups(globalGroups, projectGroups)

	// Final validation (ensure merged settings are fully resolved)
	if !IsValidLLM(s.DefaultLLM) {
		s.DefaultLLM = "codex"
	}
	if s.DefaultMode != "sandbox" && s.DefaultMode != "unrestricted" {
		s.DefaultMode = "sandbox"
	}
	if s.AutoGitBranch == nil {
		trueVal := true
		s.AutoGitBranch = &trueVal
	}
	if s.CreateMilestoneBranch == nil {
		falseVal := false
		s.CreateMilestoneBranch = &falseVal
	}
	if s.DisableBold == nil {
		falseVal := false
		s.DisableBold = &falseVal
	}
	if s.DisableRoundedBorders == nil {
		falseVal := false
		s.DisableRoundedBorders = &falseVal
	}
	if s.EnableContextCaching == nil {
		falseVal := false
		s.EnableContextCaching = &falseVal
	}
	if s.EnableCompactPhaseHandoffs == nil {
		trueVal := true
		s.EnableCompactPhaseHandoffs = &trueVal
	}
	if s.EnableCodexSessionResume == nil {
		falseVal := false
		s.EnableCodexSessionResume = &falseVal
	}
	normalizeAgentInstructionsSettings(&s)
	if s.DefaultGitBranchPrefix == "" {
		s.DefaultGitBranchPrefix = "cyclestone/milestones/"
	}
	if s.CacheTTLMinutes <= 0 {
		s.CacheTTLMinutes = 30
	}
	if s.MaxHandoffChars <= 0 {
		s.MaxHandoffChars = 12000
	}
	if s.MaxModelCallsPerPhase <= 0 {
		s.MaxModelCallsPerPhase = 50
	}
	if s.MaxTokenBudgetPerPhase <= 0 {
		s.MaxTokenBudgetPerPhase = 1000000
	}
	if s.MaxLLMInputChars <= 0 {
		s.MaxLLMInputChars = 900000
	}
	if s.MaxRetainedConversationMessages <= 0 {
		s.MaxRetainedConversationMessages = 8
	}
	if s.OllamaNumCtx == 0 {
		s.OllamaNumCtx = -1
	}
	if s.OllamaNumPredict == 0 {
		s.OllamaNumPredict = -1
	}

	if s.OllamaModel == "" {
		s.OllamaModel = DefaultOllamaModel
	}
	if s.OllamaCodexModel == "" {
		s.OllamaCodexModel = DefaultOllamaModel
	}

	// Ensure "Default" is present and listed first
	hasDefault := false
	defaultIdx := -1
	for i, g := range s.AgentGroups {
		if strings.ToLower(g.Name) == "default" {
			hasDefault = true
			defaultIdx = i
			break
		}
	}
	if !hasDefault {
		s.AgentGroups = append([]AgentGroup{PredefinedDefaultGroup}, s.AgentGroups...)
	} else if defaultIdx > 0 {
		defGroup := s.AgentGroups[defaultIdx]
		s.AgentGroups = append(s.AgentGroups[:defaultIdx], s.AgentGroups[defaultIdx+1:]...)
		s.AgentGroups = append([]AgentGroup{defGroup}, s.AgentGroups...)
	}

	return s
}

// DefaultDisableBoldForEnvironment resolves the startup bold setting while allowing
// VS Code terminals to opt into safer rendering only when settings do not say otherwise.
//
// NOTE: The VS Code integrated terminal has known compatibility issues with bold formatting
// in certain themes/fonts, causing character overlapping, alignment issues, or cursor offset
// bugs. Therefore, we auto-detect TERM_PROGRAM == "vscode" to disable bold styling by default.
// Modifying or bypassing this default auto-detection without explicit user config will cause
// visual rendering/alignment bugs in the VS Code integrated terminal.
func DefaultDisableBoldForEnvironment() bool {
	settings := LoadMergedSettings()
	disableBold := false
	if settings.DisableBold != nil {
		disableBold = *settings.DisableBold
	}
	if !disableBoldExplicitlyConfigured() && os.Getenv("TERM_PROGRAM") == "vscode" {
		disableBold = true
	}
	if os.Getenv("NO_BOLD") != "" || os.Getenv("CYCLESTONE_NO_BOLD") != "" {
		disableBold = true
	}
	return disableBold
}

func disableBoldExplicitlyConfigured() bool {
	if dir := GetGlobalConfigDir(); dir != "" && settingsFileHasDisableBold(filepath.Join(dir, "settings.yml")) {
		return true
	}
	return settingsFileHasDisableBold(filepath.Join(".cyclestone", "settings.yml"))
}

func settingsFileHasDisableBold(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings Settings
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return false
	}
	return settings.DisableBold != nil
}

// DefaultDisableRoundedBordersForEnvironment resolves the startup border setting while allowing
// VS Code terminals to opt into normal borders only when settings do not say otherwise.
//
// NOTE: The VS Code integrated terminal frequently suffers from visual glitches when rendering
// Unicode rounded border characters, resulting in double-width border gaps, alignment issues,
// or disjointed boxes. To ensure layout integrity, we auto-detect TERM_PROGRAM == "vscode" and
// default to normal/square ASCII-safe borders unless explicitly overridden by configuration settings.
// Modifying or bypassing this fallback logic will lead to broken layout borders and visual degradation
// in the VS Code terminal environment.
func DefaultDisableRoundedBordersForEnvironment() bool {
	settings := LoadMergedSettings()
	disableBorders := false
	if settings.DisableRoundedBorders != nil {
		disableBorders = *settings.DisableRoundedBorders
	}
	if !disableRoundedBordersExplicitlyConfigured() && os.Getenv("TERM_PROGRAM") == "vscode" {
		disableBorders = true
	}
	return disableBorders
}

func disableRoundedBordersExplicitlyConfigured() bool {
	if dir := GetGlobalConfigDir(); dir != "" && settingsFileHasDisableRoundedBorders(filepath.Join(dir, "settings.yml")) {
		return true
	}
	return settingsFileHasDisableRoundedBorders(filepath.Join(".cyclestone", "settings.yml"))
}

func settingsFileHasDisableRoundedBorders(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings Settings
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return false
	}
	return settings.DisableRoundedBorders != nil
}

// SaveGlobalSettings saves settings to the global settings file.
func SaveGlobalSettings(s Settings) error {
	dir := GetGlobalConfigDir()
	if dir == "" {
		return fmt.Errorf("global config directory not available")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create global config directory: %w", err)
	}
	path := filepath.Join(dir, "settings.yml")
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// SaveProjectSettings saves settings to the project settings file.
func SaveProjectSettings(s Settings) error {
	return SaveProjectSettingsAt(filepath.Join(".cyclestone", "settings.yml"), s)
}

// SaveProjectSettingsAt saves project settings to an explicit settings.yml path.
func SaveProjectSettingsAt(path string, s Settings) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("project settings path cannot be empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create project config directory: %w", err)
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
