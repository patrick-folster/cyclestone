package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrick-folster/cyclestone/resources"

	"gopkg.in/yaml.v3"
)

// Milestone defines a milestone's configuration structure.
type Milestone struct {
	ID                 string   `yaml:"id" json:"id"`
	Title              string   `yaml:"title" json:"title"`
	SpecPath           string   `yaml:"spec_path,omitempty" json:"spec_path,omitempty"`
	Goal               string   `yaml:"goal,omitempty" json:"goal,omitempty"`
	AcceptanceCriteria []string `yaml:"acceptance_criteria,omitempty" json:"acceptance_criteria,omitempty"`
	Status             string   `yaml:"status,omitempty" json:"status,omitempty"` // legacy: runtime state now lives in state.json
	Cycles             int      `yaml:"cycles,omitempty" json:"cycles,omitempty"` // legacy: runtime state now lives in state.json
	Checks             []string `yaml:"checks,omitempty" json:"checks,omitempty"`
}

// Config wraps a collection of Milestones.
type Config struct {
	Milestones   []Milestone `yaml:"milestones"`
	Repositories []string    `yaml:"repositories,omitempty"`
}

// MilestoneMigrationResult summarizes a legacy milestone storage migration.
type MilestoneMigrationResult struct {
	Milestones     int
	SpecsCreated   int
	StatusesCopied int
	CyclesCopied   int
	Changed        bool
}

// Agent represents a dynamic agent loaded from prompts.
type Agent struct {
	ID             string `json:"id"`
	Name           string `yaml:"name" json:"name"`
	Description    string `yaml:"description" json:"description"`
	Order          int    `yaml:"order" json:"order"`
	RunnerBinary   string `yaml:"runner_binary" json:"runner_binary"`
	OutputContract string `yaml:"output_contract,omitempty" json:"output_contract,omitempty"`
	PromptPath     string `json:"prompt_path"`
	PromptBody     string `json:"prompt_body"`
}

// AgentFrontmatter is used for parsing agent configuration from Markdown frontmatter.
type AgentFrontmatter struct {
	Name           string `yaml:"name"`
	Description    string `yaml:"description"`
	Order          *int   `yaml:"order"`
	RunnerBinary   string `yaml:"runner_binary"`
	OutputContract string `yaml:"output_contract"`
}

// LoadConfig reads the milestone.yml file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return a default config if it doesn't exist
			return &Config{Milestones: []Milestone{}}, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	hydrateMilestoneSpecs(path, &cfg)

	return &cfg, nil
}

// GenerateDefaultConfig creates an empty milestone index for a new project.
func GenerateDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("milestones: []\n"), 0644)
}

// InitializeMilestonesConfig creates the default config and companion milestones directory.
func InitializeMilestonesConfig(path string) error {
	if err := GenerateDefaultConfig(path); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(filepath.Dir(path), "milestones"), 0755)
}

// MigrateMilestoneStorage moves legacy milestone definitions into compact index entries.
func MigrateMilestoneStorage(configPath, statePath string) (MilestoneMigrationResult, error) {
	var result MilestoneMigrationResult

	data, err := os.ReadFile(configPath)
	if err != nil {
		return result, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return result, fmt.Errorf("failed to parse config file: %w", err)
	}
	result.Milestones = len(cfg.Milestones)

	state, err := LoadState(statePath)
	if err != nil {
		return result, err
	}

	for i := range cfg.Milestones {
		ms := cfg.Milestones[i]
		specPath := ms.SpecPath
		if specPath == "" {
			specPath = filepath.Join("milestones", ms.ID+".md")
			result.Changed = true
		}
		writePath := specPath
		if !filepath.IsAbs(writePath) {
			writePath = filepath.Join(filepath.Dir(configPath), writePath)
		}
		if _, err := os.Stat(writePath); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(writePath), 0755); err != nil {
				return result, fmt.Errorf("failed to create milestone spec directory: %w", err)
			}
			if err := os.WriteFile(writePath, []byte(formatMilestoneSpec(ms)), 0644); err != nil {
				return result, fmt.Errorf("failed to write milestone spec: %w", err)
			}
			result.SpecsCreated++
			result.Changed = true
		} else if err != nil {
			return result, fmt.Errorf("failed to inspect milestone spec: %w", err)
		}

		if ms.Status != "" {
			if _, exists := state.MilestoneStatuses[ms.ID]; !exists {
				state.MilestoneStatuses[ms.ID] = ms.Status
				result.StatusesCopied++
				result.Changed = true
			}
		}
		if ms.Cycles != 0 {
			if _, exists := state.MilestoneCycles[ms.ID]; !exists {
				state.MilestoneCycles[ms.ID] = ms.Cycles
				result.CyclesCopied++
				result.Changed = true
			}
		}

		if ms.Goal != "" || len(ms.AcceptanceCriteria) > 0 || ms.Status != "" || ms.Cycles != 0 {
			result.Changed = true
		}
		cfg.Milestones[i] = compactMilestoneIndex(ms, specPath)
	}

	if !result.Changed {
		return result, nil
	}
	if err := SaveState(statePath, state); err != nil {
		return result, err
	}
	compactData, err := yaml.Marshal(&cfg)
	if err != nil {
		return result, fmt.Errorf("failed to marshal compact milestone config: %w", err)
	}
	if err := os.WriteFile(configPath, compactData, 0644); err != nil {
		return result, fmt.Errorf("failed to write compact milestone config: %w", err)
	}
	return result, nil
}

// GetGlobalConfigDir returns the default directory path for global configurations.
func GetGlobalConfigDir() string {
	if os.Getenv("OS") == "Windows_NT" {
		appData := os.Getenv("APPDATA")
		if appData != "" {
			return filepath.Join(appData, "cyclestone")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cyclestone")
}

// LoadDynamicAgents scans global config (~/.config/cyclestone/agents/) and local config (.cyclestone/agents/).
func LoadDynamicAgents() ([]Agent, error) {
	agentsMap := make(map[string]Agent)

	// 0. Load embedded default agents
	embeddedFiles, err := resources.AgentsFS.ReadDir("agents")
	if err == nil {
		for _, entry := range embeddedFiles {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			filePath := filepath.Join("agents", entry.Name())
			data, err := resources.AgentsFS.ReadFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to read embedded agent file %s: %w", entry.Name(), err)
			}
			id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			agent, err := parseAgentBytes(id, data, "embedded:"+filePath)
			if err != nil {
				return nil, err
			}
			agentsMap[agent.ID] = agent
		}
	}

	// 1. Scan global config directory
	globalDir := filepath.Join(GetGlobalConfigDir(), "agents")
	globalFiles, err := filepath.Glob(filepath.Join(globalDir, "*.md"))
	if err == nil {
		for _, file := range globalFiles {
			agent, err := parseAgentFile(file)
			if err != nil {
				return nil, err
			}
			agentsMap[agent.ID] = agent
		}
	}

	// 2. Scan local project directory
	localDir := filepath.Join(".cyclestone", "agents")
	localFiles, err := filepath.Glob(filepath.Join(localDir, "*.md"))
	if err == nil {
		for _, file := range localFiles {
			agent, err := parseAgentFile(file)
			if err != nil {
				return nil, err
			}
			// Local overrides global
			agentsMap[agent.ID] = agent
		}
	}

	var agentsList []Agent
	for _, agent := range agentsMap {
		agentsList = append(agentsList, agent)
	}

	// Sort agents by Order first, then alphabetically by ID
	sort.Slice(agentsList, func(i, j int) bool {
		if agentsList[i].Order != agentsList[j].Order {
			return agentsList[i].Order < agentsList[j].Order
		}
		return agentsList[i].ID < agentsList[j].ID
	})

	return agentsList, nil
}

func parseAgentFile(filePath string) (Agent, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Agent{}, err
	}
	id := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	return parseAgentBytes(id, data, filePath)
}

func parseAgentBytes(id string, data []byte, promptPath string) (Agent, error) {
	content := string(data)
	var fm AgentFrontmatter
	body := content

	// Parse YAML frontmatter if present
	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) >= 3 {
			yamlContent := parts[1]
			body = strings.TrimSpace(parts[2])
			if err := yaml.Unmarshal([]byte(yamlContent), &fm); err != nil {
				return Agent{}, fmt.Errorf("failed to parse YAML frontmatter in %s: %w", promptPath, err)
			}
		}
	}

	// Set defaults
	name := fm.Name
	if name == "" {
		if len(id) > 0 {
			name = strings.ToUpper(id[:1]) + id[1:]
		} else {
			name = id
		}
	}

	order := 999
	if fm.Order != nil {
		order = *fm.Order
	}

	runner := fm.RunnerBinary
	if runner == "" {
		runner = LoadMergedSettings().DefaultLLM
	}

	return Agent{
		ID:             id,
		Name:           name,
		Description:    fm.Description,
		Order:          order,
		RunnerBinary:   runner,
		OutputContract: fm.OutputContract,
		PromptPath:     promptPath,
		PromptBody:     body,
	}, nil
}

func hydrateMilestoneSpecs(configPath string, cfg *Config) {
	for i := range cfg.Milestones {
		ms := &cfg.Milestones[i]
		if ms.SpecPath == "" {
			defaultSpecPath := filepath.Join("milestones", ms.ID+".md")
			candidate := filepath.Join(filepath.Dir(configPath), defaultSpecPath)
			if _, err := os.Stat(candidate); err == nil {
				ms.SpecPath = defaultSpecPath
			}
		}
		if ms.SpecPath == "" || (ms.Goal != "" && len(ms.AcceptanceCriteria) > 0) {
			continue
		}
		specPath := ms.SpecPath
		if !filepath.IsAbs(specPath) {
			specPath = filepath.Join(filepath.Dir(configPath), specPath)
		}
		data, err := os.ReadFile(specPath)
		if err != nil {
			continue
		}
		goal, criteria := parseMilestoneSpecMarkdown(string(data))
		if ms.Goal == "" {
			ms.Goal = goal
		}
		if len(ms.AcceptanceCriteria) == 0 {
			ms.AcceptanceCriteria = criteria
		}
	}
}

func parseMilestoneSpecMarkdown(content string) (string, []string) {
	var section string
	var goalLines []string
	var criteria []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")))
			switch heading {
			case "goal":
				section = "goal"
			case "acceptance criteria":
				section = "criteria"
			default:
				section = ""
			}
			continue
		}
		switch section {
		case "goal":
			goalLines = append(goalLines, line)
		case "criteria":
			item := strings.TrimSpace(trimmed)
			item = strings.TrimPrefix(item, "- [ ] ")
			item = strings.TrimPrefix(item, "- [x] ")
			item = strings.TrimPrefix(item, "- ")
			item = strings.TrimSpace(item)
			if item != "" && !strings.EqualFold(item, "none defined.") {
				criteria = append(criteria, item)
			}
		}
	}
	return strings.TrimSpace(strings.Join(goalLines, "\n")), criteria
}

// AddMilestone saves a compact milestone index entry to milestone.yml.
func AddMilestone(configPath string, ms Milestone) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	// Prevent duplicate IDs
	for _, m := range cfg.Milestones {
		if m.ID == ms.ID {
			return fmt.Errorf("milestone ID %s already exists", ms.ID)
		}
	}

	specPath := ms.SpecPath
	if specPath == "" {
		specPath = filepath.Join("milestones", ms.ID+".md")
	}
	writePath := specPath
	if !filepath.IsAbs(writePath) {
		writePath = filepath.Join(filepath.Dir(configPath), writePath)
	}
	if _, err := os.Stat(writePath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(writePath), 0755); err != nil {
			return fmt.Errorf("failed to create milestone spec directory: %w", err)
		}
		if err := os.WriteFile(writePath, []byte(formatMilestoneSpec(ms)), 0644); err != nil {
			return fmt.Errorf("failed to write milestone spec: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to inspect milestone spec: %w", err)
	}

	cfg.Milestones = append(cfg.Milestones, compactMilestoneIndex(ms, specPath))
	if err := compactMilestoneConfig(configPath, cfg); err != nil {
		return err
	}

	// Marshall and rewrite
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

func compactMilestoneConfig(configPath string, cfg *Config) error {
	for i := range cfg.Milestones {
		ms := cfg.Milestones[i]
		specPath := ms.SpecPath
		if specPath == "" {
			specPath = filepath.Join("milestones", ms.ID+".md")
		}
		writePath := specPath
		if !filepath.IsAbs(writePath) {
			writePath = filepath.Join(filepath.Dir(configPath), writePath)
		}
		if _, err := os.Stat(writePath); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(writePath), 0755); err != nil {
				return fmt.Errorf("failed to create milestone spec directory: %w", err)
			}
			if err := os.WriteFile(writePath, []byte(formatMilestoneSpec(ms)), 0644); err != nil {
				return fmt.Errorf("failed to write milestone spec: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to inspect milestone spec: %w", err)
		}
		compact := compactMilestoneIndex(ms, specPath)
		if ms.Status != "" && ms.Status != "Todo" {
			compact.Status = ms.Status
		}
		if ms.Cycles != 0 {
			compact.Cycles = ms.Cycles
		}
		cfg.Milestones[i] = compact
	}
	return nil
}

func compactMilestoneIndex(ms Milestone, specPath string) Milestone {
	return Milestone{
		ID:       ms.ID,
		Title:    ms.Title,
		SpecPath: specPath,
		Checks:   ms.Checks,
	}
}

func formatMilestoneSpec(ms Milestone) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Milestone Spec: %s - %s\n\n", ms.ID, ms.Title))
	sb.WriteString("## Goal\n")
	if strings.TrimSpace(ms.Goal) == "" {
		sb.WriteString("None defined.\n")
	} else {
		sb.WriteString(strings.TrimSpace(ms.Goal) + "\n")
	}
	sb.WriteString("\n## Acceptance Criteria\n")
	if len(ms.AcceptanceCriteria) == 0 {
		sb.WriteString("- None defined.\n")
	} else {
		for _, criterion := range ms.AcceptanceCriteria {
			criterion = strings.TrimSpace(criterion)
			if criterion != "" {
				sb.WriteString("- [ ] " + criterion + "\n")
			}
		}
	}
	return sb.String()
}

// DeleteMilestone removes a milestone from config and state files, and cleans up specs and reports.
func DeleteMilestone(configPath, statePath, milestoneID string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	foundIdx := -1
	var specPath string
	for i, ms := range cfg.Milestones {
		if ms.ID == milestoneID {
			foundIdx = i
			specPath = ms.SpecPath
			break
		}
	}
	if foundIdx == -1 {
		return fmt.Errorf("milestone %s not found in config", milestoneID)
	}

	cfg.Milestones = append(cfg.Milestones[:foundIdx], cfg.Milestones[foundIdx+1:]...)

	// Save compact config back to disk
	cfgData, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, cfgData, 0644); err != nil {
		return err
	}

	// Load and update state
	state, err := LoadState(statePath)
	if err != nil {
		return err
	}

	state.mu.Lock()
	if state.ActiveMilestoneID == milestoneID {
		state.ActiveMilestoneID = ""
	}
	if state.MilestoneStatuses != nil {
		delete(state.MilestoneStatuses, milestoneID)
	}
	if state.MilestoneCycles != nil {
		delete(state.MilestoneCycles, milestoneID)
	}
	if state.MilestoneRecommendations != nil {
		delete(state.MilestoneRecommendations, milestoneID)
	}
	if state.MilestoneAgentInstructionUpdateScores != nil {
		delete(state.MilestoneAgentInstructionUpdateScores, milestoneID)
	}
	if state.History != nil {
		delete(state.History, milestoneID)
	}
	state.mu.Unlock()

	if err := SaveState(statePath, state); err != nil {
		return err
	}

	// Remove spec file
	if specPath != "" {
		absSpecPath := specPath
		if !filepath.IsAbs(absSpecPath) {
			absSpecPath = filepath.Join(filepath.Dir(configPath), specPath)
		}
		_ = os.Remove(absSpecPath)
	} else {
		// Try fallback default
		_ = os.Remove(filepath.Join(filepath.Dir(configPath), "milestones", milestoneID+".md"))
	}

	// Remove milestone-owned report artifacts.
	_ = os.RemoveAll(filepath.Join(filepath.Dir(configPath), "reports", milestoneID))

	return nil
}
