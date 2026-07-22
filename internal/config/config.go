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

	// Provenance fields for the folder-per-item layout.
	CreatedBy         string `yaml:"created_by,omitempty" json:"created_by,omitempty"`
	UpdatedBy         string `yaml:"updated_by,omitempty" json:"updated_by,omitempty"`
	CreatedAt         string `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt         string `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
	ParentBriefingID  string `yaml:"parent_briefing_id,omitempty" json:"parent_briefing_id,omitempty"`
	ParentPlanID      string `yaml:"parent_plan_id,omitempty" json:"parent_plan_id,omitempty"`
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

// LoadConfig loads milestone configuration from the folder-per-item milestones
// directory. Milestones are scanned exclusively from .cyclestone/milestones/
// subdirectories (and legacy flat .md files) via LoadAllMilestonesFromDir.
// The optional milestone.yml is read only for the Repositories list when present.
func LoadConfig(path string) (*Config, error) {
	var cfg Config

	// Read repositories from the optional milestone.yml when it exists.
	if data, err := os.ReadFile(path); err == nil {
		var fileCfg Config
		if err := yaml.Unmarshal(data, &fileCfg); err == nil {
			cfg.Repositories = fileCfg.Repositories
		}
	}

	// Load milestones exclusively from the directory scan.
	milestonesDir := filepath.Join(filepath.Dir(path), "milestones")
	milestones, err := LoadAllMilestonesFromDir(milestonesDir)
	if err == nil {
		cfg.Milestones = milestones
	}
	if cfg.Milestones == nil {
		cfg.Milestones = []Milestone{}
	}

	sort.Slice(cfg.Milestones, func(i, j int) bool {
		return cfg.Milestones[i].ID < cfg.Milestones[j].ID
	})

	return &cfg, nil
}

// GetMilestonePrefix extracts the sequence/author prefix from a milestone ID.
func GetMilestonePrefix(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) >= 3 && strings.ToLower(parts[0]) == "ms" {
		return strings.Join(parts[:3], "-")
	}
	if len(parts) >= 1 {
		isAllDigits := func(s string) bool {
			if len(s) == 0 {
				return false
			}
			for _, r := range s {
				if r < '0' || r > '9' {
					return false
				}
			}
			return true
		}
		if isAllDigits(parts[0]) {
			return parts[0]
		}
		if len(parts) >= 2 && isAllDigits(parts[1]) {
			return strings.Join(parts[:2], "-")
		}
		return parts[0]
	}
	return id
}

// LoadMilestoneFromDir loads a milestone from its folder directory (.cyclestone/milestones/ms-<author>-<seq>-<slug>/).
func LoadMilestoneFromDir(dirPath string) (*Milestone, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read milestone directory %s: %w", dirPath, err)
	}

	var metaPath, specPath string
	var fallbackMeta, fallbackSpec, fallbackOrig string
	var origPath string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, "-metadata.yml") || strings.HasSuffix(name, "-metadata.yaml") {
			metaPath = filepath.Join(dirPath, name)
		} else if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			fallbackMeta = filepath.Join(dirPath, name)
		} else if strings.HasSuffix(name, "-specification.md") {
			specPath = filepath.Join(dirPath, name)
		} else if strings.HasSuffix(name, ".md") {
			if strings.HasSuffix(name, "-original.md") {
				origPath = filepath.Join(dirPath, name)
			} else if strings.HasSuffix(name, "-orig.md") {
				fallbackOrig = filepath.Join(dirPath, name)
			} else {
				fallbackSpec = filepath.Join(dirPath, name)
			}
		}
	}

	if metaPath == "" {
		metaPath = fallbackMeta
	}
	if specPath == "" {
		specPath = fallbackSpec
	}

	if metaPath == "" {
		return nil, fmt.Errorf("no metadata .yml file found in milestone directory %s", dirPath)
	}

	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read milestone metadata file %s: %w", metaPath, err)
	}

	var ms Milestone
	if err := yaml.Unmarshal(metaData, &ms); err != nil {
		return nil, fmt.Errorf("failed to parse milestone metadata %s: %w", metaPath, err)
	}

	prefix := GetMilestonePrefix(ms.ID)
	expectedMetaName := prefix + "-metadata.yml"
	expectedSpecName := prefix + "-specification.md"
	expectedOrigName := prefix + "-original.md"

	migrated := false
	if fallbackMeta != "" && filepath.Base(fallbackMeta) != expectedMetaName {
		migrated = true
	}
	if fallbackSpec != "" && filepath.Base(fallbackSpec) != expectedSpecName {
		migrated = true
	}
	if fallbackOrig != "" && filepath.Base(fallbackOrig) != expectedOrigName {
		migrated = true
	}

	if migrated {
		// Read existing spec content if available.
		var specBytes []byte
		if specPath != "" {
			specBytes, _ = os.ReadFile(specPath)
		}
		// Read existing original prompt file content if available.
		var origContent []byte
		var origReadErr error
		if fallbackOrig != "" {
			origContent, origReadErr = os.ReadFile(fallbackOrig)
		} else if origPath != "" {
			origContent, origReadErr = os.ReadFile(origPath)
		}

		// Re-save using SaveMilestoneToFolder (this writes the new spec/metadata in the new schema)
		baseMilestonesDir := filepath.Dir(filepath.Clean(dirPath))
		_, err = SaveMilestoneToFolder(baseMilestonesDir, ms, string(specBytes))
		if err == nil {
			// Write the new original file if we have content.
			if origReadErr == nil && len(origContent) > 0 {
				newOrigPath := filepath.Join(dirPath, expectedOrigName)
				_ = os.WriteFile(newOrigPath, origContent, 0644)
			}
			// Delete the old legacy files.
			if fallbackMeta != "" && filepath.Base(fallbackMeta) != expectedMetaName {
				_ = os.Remove(fallbackMeta)
			}
			if fallbackSpec != "" && filepath.Base(fallbackSpec) != expectedSpecName {
				_ = os.Remove(fallbackSpec)
			}
			if fallbackOrig != "" && filepath.Base(fallbackOrig) != expectedOrigName {
				_ = os.Remove(fallbackOrig)
			}
			// Set the correct new paths.
			metaPath = filepath.Join(dirPath, expectedMetaName)
			specPath = filepath.Join(dirPath, expectedSpecName)

			// Reload the newly written metadata since SaveMilestoneToFolder updated ms.SpecPath.
			if newMetaData, rErr := os.ReadFile(metaPath); rErr == nil {
				_ = yaml.Unmarshal(newMetaData, &ms)
			}
		}
	}

	if specPath != "" {
		ms.SpecPath = specPath
		specBytes, err := os.ReadFile(specPath)
		if err == nil {
			goal, criteria := parseMilestoneSpecMarkdown(string(specBytes))
			if goal != "" {
				ms.Goal = goal
			}
			if len(criteria) > 0 {
				ms.AcceptanceCriteria = criteria
			}
		}
	}

	return &ms, nil
}

// SaveMilestoneToFolder saves a Milestone into a folder-per-item structure
// under baseMilestonesDir. When specContent is non-empty it is written verbatim
// as the .md spec; otherwise FormatMilestoneSpec(ms) is used.
func SaveMilestoneToFolder(baseMilestonesDir string, ms Milestone, specContent string) (string, error) {
	if strings.TrimSpace(ms.ID) == "" {
		return "", fmt.Errorf("milestone ID cannot be empty")
	}

	// Use the milestone ID as the stable directory name so title edits do
	// not create orphan directories.
	dirPath := filepath.Join(baseMilestonesDir, ms.ID)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create milestone directory %s: %w", dirPath, err)
	}

	prefix := GetMilestonePrefix(ms.ID)
	metaPath := filepath.Join(dirPath, prefix+"-metadata.yml")
	specPath := filepath.Join(dirPath, prefix+"-specification.md")

	ms.SpecPath = specPath

	metaBytes, err := yaml.Marshal(ms)
	if err != nil {
		return "", fmt.Errorf("failed to marshal milestone metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, metaBytes, 0644); err != nil {
		return "", fmt.Errorf("failed to write milestone metadata %s: %w", metaPath, err)
	}

	if strings.TrimSpace(specContent) == "" {
		specContent = FormatMilestoneSpec(ms)
	}
	if err := os.WriteFile(specPath, []byte(specContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write milestone spec %s: %w", specPath, err)
	}

	return dirPath, nil
}

// StampMilestoneProvenance fills empty provenance fields on a Milestone before
// it is persisted. It is a defensive helper so that future call sites cannot
// accidentally save a Milestone without CreatedBy/UpdatedBy/CreatedAt/UpdatedAt.
// If the fields are already populated they are left untouched.
func StampMilestoneProvenance(ms *Milestone, actor, now string) {
	if ms == nil {
		return
	}
	if actor == "" {
		actor = "unknown"
	}
	if ms.CreatedBy == "" {
		ms.CreatedBy = actor
	}
	if ms.UpdatedBy == "" {
		ms.UpdatedBy = actor
	}
	if ms.CreatedAt == "" {
		ms.CreatedAt = now
	}
	if ms.UpdatedAt == "" {
		ms.UpdatedAt = now
	}
}

// LoadAllMilestonesFromDir scans a milestones directory for folder-per-item
// milestone subdirectories and legacy flat .md spec files.
func LoadAllMilestonesFromDir(milestonesDir string) ([]Milestone, error) {
	entries, err := os.ReadDir(milestonesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var milestones []Milestone
	seenIDs := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			dirPath := filepath.Join(milestonesDir, entry.Name())
			ms, err := LoadMilestoneFromDir(dirPath)
			if err == nil && ms != nil && ms.ID != "" && !seenIDs[ms.ID] {
				milestones = append(milestones, *ms)
				seenIDs[ms.ID] = true
			}
			continue
		}
		// Legacy flat .md spec file (no companion .yml metadata).
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		ms := loadLegacyFlatMilestone(filepath.Join(milestonesDir, name))
		if ms != nil && ms.ID != "" && !seenIDs[ms.ID] {
			milestones = append(milestones, *ms)
			seenIDs[ms.ID] = true
		}
	}

	sort.Slice(milestones, func(i, j int) bool {
		return milestones[i].ID < milestones[j].ID
	})

	return milestones, nil
}

// loadLegacyFlatMilestone parses a legacy flat .md milestone spec file into a
// Milestone by extracting the ID and title from the H1 header and the
// goal/criteria from the markdown body. The SpecPath is set relative to the
// milestones directory.
func loadLegacyFlatMilestone(mdPath string) *Milestone {
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return nil
	}
	content := string(data)
	ms := Milestone{SpecPath: mdPath}
	// Parse H1 header: "# Milestone Spec: <id> - <title>"
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			// Strip "Milestone Spec: " prefix if present
			rest = strings.TrimPrefix(rest, "Milestone Spec: ")
			// Split on " - " to get ID and title
			idx := strings.Index(rest, " - ")
			if idx > 0 {
				ms.ID = strings.TrimSpace(rest[:idx])
				ms.Title = strings.TrimSpace(rest[idx+3:])
			} else {
				ms.ID = strings.TrimSpace(rest)
			}
			break
		}
	}
	goal, criteria := parseMilestoneSpecMarkdown(content)
	ms.Goal = goal
	ms.AcceptanceCriteria = criteria
	return &ms
}

// GenerateDefaultConfig creates an empty milestone index for a new project.
func GenerateDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("milestones: []\n"), 0644)
}

// InitializeMilestonesConfig creates the .cyclestone/milestones/ and
// .cyclestone/plans/ directories. It no longer generates a legacy milestone.yml index file.
func InitializeMilestonesConfig(path string) error {
	baseDir := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Join(baseDir, "milestones"), 0755); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(baseDir, "plans"), 0755)
}

// MigrateMilestoneStorage migrates legacy milestone.yml index entries and flat
// .md specs into the folder-per-item layout. It reads any legacy milestone
// definitions from milestone.yml, writes them as folder-per-item
// <id>/<id>.yml + <id>.md pairs, copies runtime state, and then removes the
// milestone.yml index. Milestones already present as folder-per-item
// directories are left untouched.
func MigrateMilestoneStorage(configPath, statePath string) (MilestoneMigrationResult, error) {
	var result MilestoneMigrationResult

	baseDir := filepath.Dir(configPath)
	milestonesDir := filepath.Join(baseDir, "milestones")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
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

	// Determine which IDs already exist as folder-per-item directories.
	existing, _ := LoadAllMilestonesFromDir(milestonesDir)
	existingIDs := make(map[string]bool, len(existing))
	for _, m := range existing {
		existingIDs[m.ID] = true
	}

	for _, ms := range cfg.Milestones {
		if existingIDs[ms.ID] {
			continue
		}
		// Read the legacy flat .md spec if it exists.
		specContent := ""
		specPath := ms.SpecPath
		if specPath == "" {
			specPath = filepath.Join("milestones", ms.ID+".md")
		}
		absSpec := specPath
		if !filepath.IsAbs(absSpec) {
			absSpec = filepath.Join(baseDir, specPath)
		}
		if specBytes, err := os.ReadFile(absSpec); err == nil {
			specContent = string(specBytes)
		}
		if specContent == "" {
			specContent = FormatMilestoneSpec(ms)
		}
		if _, err := SaveMilestoneToFolder(milestonesDir, ms, specContent); err != nil {
			return result, fmt.Errorf("failed to migrate milestone %s: %w", ms.ID, err)
		}
		result.SpecsCreated++
		result.Changed = true

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
	}

	if !result.Changed {
		return result, nil
	}
	if err := SaveState(statePath, state); err != nil {
		return result, err
	}
	// Remove the legacy milestone.yml index now that all entries are in the
	// folder-per-item layout. Preserve a milestone.yml that only contains
	// repositories by rewriting it without the milestones key.
	if repoData, rerr := os.ReadFile(configPath); rerr == nil {
		var rcfg Config
		if yaml.Unmarshal(repoData, &rcfg) == nil {
			rcfg.Milestones = nil
			if len(rcfg.Repositories) > 0 {
				compactData, _ := yaml.Marshal(&rcfg)
				_ = os.WriteFile(configPath, compactData, 0644)
			} else {
				_ = os.Remove(configPath)
			}
		}
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

// AddMilestone persists a milestone through the folder-per-item layout,
// producing a <id>/<id>.yml + <id>.md pair under the milestones directory.
func AddMilestone(configPath string, ms Milestone) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	for _, m := range cfg.Milestones {
		if m.ID == ms.ID {
			return fmt.Errorf("milestone ID %s already exists", ms.ID)
		}
	}
	milestonesDir := filepath.Join(filepath.Dir(configPath), "milestones")
	// If a legacy flat .md spec already exists, preserve its content.
	specContent := ""
	if ms.SpecPath != "" {
		absSpec := ms.SpecPath
		if !filepath.IsAbs(absSpec) {
			absSpec = filepath.Join(filepath.Dir(configPath), absSpec)
		}
		if data, err := os.ReadFile(absSpec); err == nil {
			specContent = string(data)
		}
	}
	_, err = SaveMilestoneToFolder(milestonesDir, ms, specContent)
	return err
}

// AddMilestoneWithSpec persists a milestone with the supplied spec Markdown
// through the folder-per-item layout, producing a <id>/<id>.yml + <id>.md pair.
func AddMilestoneWithSpec(configPath string, ms Milestone, specMarkdown string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	for _, existing := range cfg.Milestones {
		if existing.ID == ms.ID {
			return fmt.Errorf("milestone ID %s already exists", ms.ID)
		}
	}
	milestonesDir := filepath.Join(filepath.Dir(configPath), "milestones")
	// Check for an existing spec in the folder-per-item layout.
	folderName := ms.ID
	slug := PlanningSlug(ms.Title)
	if slug != "" && !strings.Contains(folderName, slug) {
		folderName = folderName + "-" + slug
	}
	existingDir := filepath.Join(milestonesDir, folderName)
	if _, err := os.Stat(existingDir); err == nil {
		return fmt.Errorf("milestone spec %s already exists", ms.ID)
	}
	// Also check legacy flat .md path.
	legacyPath := filepath.Join(milestonesDir, ms.ID+".md")
	if _, err := os.Stat(legacyPath); err == nil {
		return fmt.Errorf("milestone spec %s already exists", ms.ID)
	}
	_, err = SaveMilestoneToFolder(milestonesDir, ms, specMarkdown)
	return err
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
			if err := os.WriteFile(writePath, []byte(FormatMilestoneSpec(ms)), 0644); err != nil {
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

func FormatMilestoneSpec(ms Milestone) string {
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

// DeleteMilestone removes a milestone's folder-per-item directory (or legacy
// flat .md spec), cleans up state entries, and removes report artifacts. It no
// longer rewrites milestone.yml.
func DeleteMilestone(configPath, statePath, milestoneID string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}

	found := false
	var specPath string
	for _, ms := range cfg.Milestones {
		if ms.ID == milestoneID {
			found = true
			specPath = ms.SpecPath
			break
		}
	}
	if !found {
		return fmt.Errorf("milestone %s not found in config", milestoneID)
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

	// Remove the milestone directory or legacy flat spec.
	milestonesDir := filepath.Join(filepath.Dir(configPath), "milestones")
	removed := false
	if entries, err := os.ReadDir(milestonesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), milestoneID) {
				_ = os.RemoveAll(filepath.Join(milestonesDir, entry.Name()))
				removed = true
				break
			}
		}
	}
	if !removed {
		// Try legacy flat .md path or specPath
		candidates := []string{
			filepath.Join(milestonesDir, milestoneID+".md"),
		}
		if specPath != "" {
			absSpec := specPath
			if !filepath.IsAbs(absSpec) {
				absSpec = filepath.Join(filepath.Dir(configPath), specPath)
			}
			candidates = append(candidates, absSpec)
		}
		for _, c := range candidates {
			_ = os.Remove(c)
		}
	}

	// Remove milestone-owned report artifacts.
	_ = os.RemoveAll(filepath.Join(filepath.Dir(configPath), "reports", "milestones", milestoneID))

	return nil
}
