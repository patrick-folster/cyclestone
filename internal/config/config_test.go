package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseAgentFile(t *testing.T) {
	// Create a temp agent file with frontmatter
	tmpDir, err := os.MkdirTemp("", "agents_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	agentContent := `---
name: "Test PM"
description: "A test project manager agent"
order: 5
runner_binary: "test-runner"
output_contract: "qa"
---
# Test Agent Prompt
This is the body of the prompt.
`

	agentPath := filepath.Join(tmpDir, "pm.md")
	if err := os.WriteFile(agentPath, []byte(agentContent), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	agent, err := parseAgentFile(agentPath)
	if err != nil {
		t.Fatalf("parseAgentFile failed: %v", err)
	}

	if agent.ID != "pm" {
		t.Errorf("expected ID 'pm', got '%s'", agent.ID)
	}
	if agent.Name != "Test PM" {
		t.Errorf("expected Name 'Test PM', got '%s'", agent.Name)
	}
	if agent.Description != "A test project manager agent" {
		t.Errorf("expected Description 'A test project manager agent', got '%s'", agent.Description)
	}
	if agent.Order != 5 {
		t.Errorf("expected Order 5, got %d", agent.Order)
	}
	if agent.RunnerBinary != "test-runner" {
		t.Errorf("expected runner_binary 'test-runner', got '%s'", agent.RunnerBinary)
	}
	if agent.OutputContract != "qa" {
		t.Errorf("expected output_contract 'qa', got '%s'", agent.OutputContract)
	}
	if !stringsContains(agent.PromptBody, "This is the body of the prompt.") {
		t.Errorf("expected prompt body to contain text, got '%s'", agent.PromptBody)
	}
}

func TestParseAgentFileNoFrontmatter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agents_test")
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

	agentContent := `# Developer Prompt
Some prompt text without frontmatter.
`

	agentPath := filepath.Join(tmpDir, "developer.md")
	if err := os.WriteFile(agentPath, []byte(agentContent), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	agent, err := parseAgentFile(agentPath)
	if err != nil {
		t.Fatalf("parseAgentFile failed: %v", err)
	}

	if agent.ID != "developer" {
		t.Errorf("expected ID 'developer', got '%s'", agent.ID)
	}
	if agent.Name != "Developer" {
		t.Errorf("expected Name 'Developer', got '%s'", agent.Name)
	}
	if agent.Order != 999 {
		t.Errorf("expected default Order 999, got %d", agent.Order)
	}
	if agent.RunnerBinary != "codex" {
		t.Errorf("expected default runner_binary 'codex', got '%s'", agent.RunnerBinary)
	}
}

func TestLoadDynamicAgents(t *testing.T) {
	agents, err := LoadDynamicAgents()
	if err != nil {
		t.Fatalf("LoadDynamicAgents failed: %v", err)
	}

	// We expect at least the 3 default embedded agents
	if len(agents) < 3 {
		t.Fatalf("expected at least 3 agents, got %d", len(agents))
	}

	// Verify order: pm (Order 1), developer (Order 2), qa (Order 3)
	// Even if there are extra local/global agents, the default ones should be sorted in this relative order.
	var pmIdx, devIdx, qaIdx = -1, -1, -1
	for idx, agent := range agents {
		switch agent.ID {
		case "pm":
			pmIdx = idx
		case "developer":
			devIdx = idx
		case "qa":
			qaIdx = idx
		}
	}

	if pmIdx == -1 || devIdx == -1 || qaIdx == -1 {
		t.Fatalf("expected default agents 'pm', 'developer', and 'qa' to be present. Found: %v", agents)
	}

	if !(pmIdx < devIdx && devIdx < qaIdx) {
		t.Errorf("expected agents ordered as pm -> developer -> qa, but indexes are: pm=%d, developer=%d, qa=%d", pmIdx, devIdx, qaIdx)
	}
}

func TestMigrateLegacyState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a legacy state.json file
	legacyJSON := `{
		"active_milestone_id": "MS-1",
		"milestone_statuses": {
			"MS-1": "In Progress"
		},
		"milestone_cycles": {
			"MS-1": 1
		},
		"history": [
			{
				"milestone_id": "MS-1",
				"action": "Started Cycle",
				"timestamp": "2026-06-19 12:00:00",
				"branch": "cyclestone/milestones/0001-legacy-state",
				"commit_hash": "a1b2c3d"
			}
		]
	}`

	statePath := filepath.Join(tmpDir, "state.json")
	if err := os.WriteFile(statePath, []byte(legacyJSON), 0644); err != nil {
		t.Fatalf("failed to write legacy state file: %v", err)
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	if state.ActiveMilestoneID != "MS-1" {
		t.Errorf("expected ActiveMilestoneID 'MS-1', got '%s'", state.ActiveMilestoneID)
	}

	if len(state.History) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(state.History))
	}

	ms1History, exists := state.History["MS-1"]
	if !exists {
		t.Fatalf("expected history for MS-1 to exist")
	}

	if len(ms1History) != 1 {
		t.Fatalf("expected 1 cycle log for MS-1, got %d", len(ms1History))
	}

	log := ms1History[0]
	if log.CycleNumber != 1 {
		t.Errorf("expected cycle number 1, got %d", log.CycleNumber)
	}
	if log.Branch != "cyclestone/milestones/0001-legacy-state" {
		t.Errorf("expected branch 'cyclestone/milestones/0001-legacy-state', got '%s'", log.Branch)
	}
	if log.CommitHash != "a1b2c3d" {
		t.Errorf("expected commit hash 'a1b2c3d', got '%s'", log.CommitHash)
	}
}

func TestAddMilestone(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "milestone.yml")

	// 1. Initial milestone config
	initialYAML := `milestones:
  - id: MS-1
    title: First Milestone
    goal: First goal
    acceptance_criteria:
      - Criterion 1
    status: Todo
    cycles: 0
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	// 2. Add new milestone
	newMs := Milestone{
		ID:                 "MS-2",
		Title:              "Second Milestone",
		Goal:               "Second goal",
		AcceptanceCriteria: []string{"Criterion A", "Criterion B"},
		Status:             "Todo",
		Cycles:             0,
		Checks:             []string{"backend", "frontend"},
	}

	if err := AddMilestone(configPath, newMs); err != nil {
		t.Fatalf("AddMilestone failed: %v", err)
	}

	// 3. Load config and verify fields
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(cfg.Milestones) != 2 {
		t.Fatalf("expected 2 milestones, got %d", len(cfg.Milestones))
	}

	m1 := cfg.Milestones[0]
	if m1.ID != "MS-1" {
		t.Errorf("expected first milestone ID 'MS-1', got '%s'", m1.ID)
	}

	m2 := cfg.Milestones[1]
	if m2.ID != "MS-2" {
		t.Errorf("expected second milestone ID 'MS-2', got '%s'", m2.ID)
	}
	if m2.Title != "Second Milestone" {
		t.Errorf("expected Title 'Second Milestone', got '%s'", m2.Title)
	}
	if len(m2.AcceptanceCriteria) != 2 || m2.AcceptanceCriteria[0] != "Criterion A" {
		t.Errorf("expected acceptance criteria correct, got %v", m2.AcceptanceCriteria)
	}
	if m2.SpecPath != filepath.Join("milestones", "MS-2.md") {
		t.Errorf("expected compact spec path for MS-2, got %s", m2.SpecPath)
	}
	if len(m2.Checks) != 2 || m2.Checks[0] != "backend" {
		t.Errorf("expected checks correct, got %v", m2.Checks)
	}
	specBytes, err := os.ReadFile(filepath.Join(tmpDir, "milestones", "MS-2.md"))
	if err != nil {
		t.Fatalf("expected milestone spec to be written: %v", err)
	}
	if !stringsContains(string(specBytes), "Second goal") {
		t.Errorf("expected spec to contain goal, got %s", string(specBytes))
	}
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read compact config: %v", err)
	}
	configText := string(configBytes)
	if stringsContains(configText, "status:") || stringsContains(configText, "cycles:") || stringsContains(configText, "goal:") || stringsContains(configText, "acceptance_criteria:") {
		t.Errorf("expected compact config without mutable/spec fields, got %s", configText)
	}

	// 4. Verify duplicate prevention
	duplicateMs := Milestone{
		ID:    "MS-2",
		Title: "Duplicate Milestone",
	}
	if err := AddMilestone(configPath, duplicateMs); err == nil {
		t.Error("expected error when adding milestone with duplicate ID, got nil")
	}
}

func TestLoadConfigHydratesCompactSpec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "compact_config_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "milestone.yml")
	specDir := filepath.Join(tmpDir, "milestones")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatalf("failed to create spec dir: %v", err)
	}

	configYAML := `milestones:
  - id: MS-1
    title: Compact Milestone
    spec_path: milestones/MS-1.md
`
	specMarkdown := `# Milestone Spec: MS-1 - Compact Milestone

## Goal
Hydrate the goal from markdown.

## Acceptance Criteria
- [ ] First criterion
- [ ] Second criterion
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "MS-1.md"), []byte(specMarkdown), 0644); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Milestones) != 1 {
		t.Fatalf("expected 1 milestone, got %d", len(cfg.Milestones))
	}
	ms := cfg.Milestones[0]
	if ms.Goal != "Hydrate the goal from markdown." {
		t.Errorf("expected hydrated goal, got %q", ms.Goal)
	}
	if len(ms.AcceptanceCriteria) != 2 || ms.AcceptanceCriteria[1] != "Second criterion" {
		t.Errorf("expected hydrated criteria, got %v", ms.AcceptanceCriteria)
	}
	if ms.Status != "" || ms.Cycles != 0 {
		t.Errorf("expected compact config to leave mutable fields empty/default, got status=%q cycles=%d", ms.Status, ms.Cycles)
	}
}

func TestAddMilestonePreservesExistingSpec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "preserve_spec_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "milestone.yml")
	specDir := filepath.Join(tmpDir, "milestones")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatalf("failed to create spec dir: %v", err)
	}
	existingSpecPath := filepath.Join(specDir, "MS-9.md")
	existingSpec := `# Milestone Spec: MS-9 - Generated Title

## Goal
Keep generated content.
`
	if err := os.WriteFile(existingSpecPath, []byte(existingSpec), 0644); err != nil {
		t.Fatalf("failed to write existing spec: %v", err)
	}

	ms := Milestone{
		ID:       "MS-9",
		Title:    "Generated Title",
		Goal:     "Fallback goal should not overwrite existing file.",
		SpecPath: filepath.Join("milestones", "MS-9.md"),
	}
	if err := AddMilestone(configPath, ms); err != nil {
		t.Fatalf("AddMilestone failed: %v", err)
	}

	specBytes, err := os.ReadFile(existingSpecPath)
	if err != nil {
		t.Fatalf("failed to read existing spec: %v", err)
	}
	if string(specBytes) != existingSpec {
		t.Errorf("expected existing spec to be preserved, got %s", string(specBytes))
	}
}

func TestMigrateMilestoneStorage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "migrate_storage_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	legacyYAML := `milestones:
  - id: MS-1
    title: Legacy Milestone
    goal: Legacy goal
    acceptance_criteria:
      - Legacy criterion
    status: In Progress
    cycles: 3
`
	if err := os.WriteFile(configPath, []byte(legacyYAML), 0644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	result, err := MigrateMilestoneStorage(configPath, statePath)
	if err != nil {
		t.Fatalf("MigrateMilestoneStorage failed: %v", err)
	}
	if !result.Changed || result.Milestones != 1 || result.SpecsCreated != 1 || result.StatusesCopied != 1 || result.CyclesCopied != 1 {
		t.Errorf("unexpected migration result: %+v", result)
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("failed to load migrated state: %v", err)
	}
	if state.GetMilestoneStatus("MS-1") != "In Progress" || state.GetMilestoneCycles("MS-1") != 3 {
		t.Errorf("expected migrated runtime state, got status=%q cycles=%d", state.GetMilestoneStatus("MS-1"), state.GetMilestoneCycles("MS-1"))
	}

	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read migrated config: %v", err)
	}
	configText := string(configBytes)
	if stringsContains(configText, "goal:") || stringsContains(configText, "acceptance_criteria:") || stringsContains(configText, "status:") || stringsContains(configText, "cycles:") {
		t.Errorf("expected compact config, got %s", configText)
	}
	if !stringsContains(configText, "spec_path: milestones/MS-1.md") {
		t.Errorf("expected spec path in compact config, got %s", configText)
	}

	specBytes, err := os.ReadFile(filepath.Join(tmpDir, "milestones", "MS-1.md"))
	if err != nil {
		t.Fatalf("failed to read migrated spec: %v", err)
	}
	if !stringsContains(string(specBytes), "Legacy goal") || !stringsContains(string(specBytes), "Legacy criterion") {
		t.Errorf("expected legacy definition in spec, got %s", string(specBytes))
	}

	secondResult, err := MigrateMilestoneStorage(configPath, statePath)
	if err != nil {
		t.Fatalf("second migration failed: %v", err)
	}
	if secondResult.Changed {
		t.Errorf("expected idempotent migration, got %+v", secondResult)
	}
}

func TestMilestoneRecommendations(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recommendation_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	statePath := filepath.Join(tmpDir, "state.json")

	// Load non-existent state to get clean state
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	// Verify default fallback recommendation score is -1
	if score := state.GetMilestoneRecommendation("MS-1"); score != -1 {
		t.Errorf("expected default recommendation score to be -1, got %d", score)
	}

	// Set recommendation score
	state.SetMilestoneRecommendation("MS-1", 5)
	if score := state.GetMilestoneRecommendation("MS-1"); score != 5 {
		t.Errorf("expected recommendation score to be 5, got %d", score)
	}

	// Save state
	if err := SaveState(statePath, state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	// Reload state
	stateReloaded, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState reloaded failed: %v", err)
	}

	// Verify loaded recommendation score
	if score := stateReloaded.GetMilestoneRecommendation("MS-1"); score != 5 {
		t.Errorf("expected reloaded recommendation score to be 5, got %d", score)
	}

	// Verify loaded non-existent recommendation score is -1
	if score := stateReloaded.GetMilestoneRecommendation("MS-2"); score != -1 {
		t.Errorf("expected non-existent reloaded recommendation score to be -1, got %d", score)
	}
}

func TestConfigRepositoriesSerialization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config_repos_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "milestone.yml")
	initialYAML := `repositories:
  - backend
  - frontend
  - custom_dir
milestones:
  - id: MS-1
    title: First Milestone
`
	if err := os.WriteFile(configPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(cfg.Repositories) != 3 || cfg.Repositories[0] != "backend" || cfg.Repositories[2] != "custom_dir" {
		t.Errorf("expected 3 repositories, got %v", cfg.Repositories)
	}

	// Test preservation on AddMilestone
	newMs := Milestone{
		ID:    "MS-2",
		Title: "Second Milestone",
	}
	if err := AddMilestone(configPath, newMs); err != nil {
		t.Fatalf("AddMilestone failed: %v", err)
	}

	cfg2, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig reloaded failed: %v", err)
	}

	if len(cfg2.Repositories) != 3 || cfg2.Repositories[0] != "backend" || cfg2.Repositories[2] != "custom_dir" {
		t.Errorf("repositories field was not preserved after AddMilestone: %v", cfg2.Repositories)
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || filepath.Base(s) == substr || (len(s) > 0 && stringsContainsHelper(s, substr)))
}

func stringsContainsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestDeleteMilestone(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")

	cfg := Config{
		Milestones: []Milestone{
			{ID: "MS-1", Title: "First MS", SpecPath: "milestones/MS-1.md"},
			{ID: "MS-2", Title: "Second MS", SpecPath: "milestones/MS-2.md"},
		},
	}
	cfgData, _ := yaml.Marshal(cfg)
	_ = os.WriteFile(configPath, cfgData, 0644)

	state := &State{
		ActiveMilestoneID: "MS-1",
		MilestoneStatuses: map[string]string{"MS-1": "Done", "MS-2": "Todo"},
		MilestoneCycles:   map[string]int{"MS-1": 1, "MS-2": 0},
		History: map[string][]MilestoneCycleLog{
			"MS-1": {{CycleNumber: 1}},
		},
	}
	_ = SaveState(statePath, state)

	_ = os.MkdirAll(filepath.Join(tmpDir, "milestones"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "reports"), 0755)
	spec1 := filepath.Join(tmpDir, "milestones", "MS-1.md")
	spec2 := filepath.Join(tmpDir, "milestones", "MS-2.md")
	_ = os.WriteFile(spec1, []byte("goal 1"), 0644)
	_ = os.WriteFile(spec2, []byte("goal 2"), 0644)

	report1 := filepath.Join(tmpDir, "reports", "MS-1.md")
	report1Cycle := filepath.Join(tmpDir, "reports", "MS-1-cycle-001.yaml")
	report2 := filepath.Join(tmpDir, "reports", "MS-2.md")
	_ = os.WriteFile(report1, []byte("report 1"), 0644)
	_ = os.WriteFile(report1Cycle, []byte("report 1 cycle 1"), 0644)
	_ = os.WriteFile(report2, []byte("report 2"), 0644)

	err := DeleteMilestone(configPath, statePath, "MS-1")
	if err != nil {
		t.Fatalf("DeleteMilestone failed: %v", err)
	}

	newCfg, _ := LoadConfig(configPath)
	if len(newCfg.Milestones) != 1 || newCfg.Milestones[0].ID != "MS-2" {
		t.Errorf("expected only MS-2 in config, got: %v", newCfg.Milestones)
	}

	newState, _ := LoadState(statePath)
	if newState.ActiveMilestoneID != "" {
		t.Errorf("expected active_milestone_id to be cleared, got %q", newState.ActiveMilestoneID)
	}
	if _, exists := newState.MilestoneStatuses["MS-1"]; exists {
		t.Error("expected MS-1 status to be deleted")
	}
	if _, exists := newState.History["MS-1"]; exists {
		t.Error("expected MS-1 history to be deleted")
	}

	if _, err := os.Stat(spec1); !os.IsNotExist(err) {
		t.Error("expected spec1 to be deleted")
	}
	if _, err := os.Stat(spec2); os.IsNotExist(err) {
		t.Error("expected spec2 to be preserved")
	}
	if _, err := os.Stat(report1); !os.IsNotExist(err) {
		t.Error("expected report1 to be deleted")
	}
	if _, err := os.Stat(report1Cycle); !os.IsNotExist(err) {
		t.Error("expected report1Cycle to be deleted")
	}
	if _, err := os.Stat(report2); os.IsNotExist(err) {
		t.Error("expected report2 to be preserved")
	}
}

func TestDeleteMilestoneCycle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")

	state := &State{
		History: map[string][]MilestoneCycleLog{
			"MS-1": {
				{CycleNumber: 1},
				{CycleNumber: 2},
				{CycleNumber: 3},
			},
		},
		MilestoneCycles: map[string]int{"MS-1": 3},
	}
	_ = SaveState(statePath, state)

	reportsDir := filepath.Join(tmpDir, "reports")
	_ = os.MkdirAll(reportsDir, 0755)

	c1File := filepath.Join(reportsDir, "MS-1-cycle-001.yaml")
	c2File := filepath.Join(reportsDir, "MS-1-cycle-002.yaml")
	c3File := filepath.Join(reportsDir, "MS-1-cycle-003.yaml")
	c3Meta := filepath.Join(reportsDir, "MS-1-cycle-003-metadata.json")
	summaryFile := filepath.Join(reportsDir, "MS-1.md")

	_ = os.WriteFile(c1File, []byte("started: \"2026-07-02 09:00:00 -0500\"\ndetails: |-\n  c1\n"), 0644)
	_ = os.WriteFile(c2File, []byte("started: \"2026-07-02 10:00:00 -0500\"\ndetails: |-\n  c2\n"), 0644)
	_ = os.WriteFile(c3File, []byte("started: \"2026-07-02 11:00:00 -0500\"\ndetails: |-\n  verdict: approved\n"), 0644)
	_ = os.WriteFile(c3Meta, []byte("c3meta"), 0644)
	_ = os.WriteFile(summaryFile, []byte("summary"), 0644)

	err := DeleteMilestoneCycle(configPath, statePath, "MS-1", 2)
	if err != nil {
		t.Fatalf("DeleteMilestoneCycle failed: %v", err)
	}

	newState, _ := LoadState(statePath)
	logs := newState.History["MS-1"]
	if len(logs) != 2 {
		t.Fatalf("expected 2 cycles in state, got %d", len(logs))
	}
	if logs[0].CycleNumber != 1 || logs[1].CycleNumber != 2 {
		t.Errorf("expected cycle numbers 1 and 2, got %d and %d", logs[0].CycleNumber, logs[1].CycleNumber)
	}
	if newState.MilestoneCycles["MS-1"] != 2 {
		t.Errorf("expected milestone cycles count to be 2, got %d", newState.MilestoneCycles["MS-1"])
	}

	if _, err := os.Stat(c1File); os.IsNotExist(err) {
		t.Error("expected cycle 1 report to be preserved")
	}

	// The original cycle 2 file was deleted, but cycle 3's file was renamed to cycle 2's path.
	// So c2File path exists now, but its content must be "c3" (not "c2")!
	c2Bytes, err := os.ReadFile(c2File)
	if err != nil {
		t.Errorf("expected cycle 2 report path to exist (as renamed cycle 3), got error: %v", err)
	} else if !strings.Contains(string(c2Bytes), "verdict: approved") {
		t.Errorf("expected cycle 2 report path to contain renamed cycle 3 content, got %q", string(c2Bytes))
	}

	c3RenamedFile := filepath.Join(reportsDir, "MS-1-cycle-002.yaml")
	c3RenamedMeta := filepath.Join(reportsDir, "MS-1-cycle-002-metadata.json")
	if _, err := os.Stat(c3RenamedFile); os.IsNotExist(err) {
		t.Error("expected cycle 3 report to be renamed to cycle 2")
	}
	if _, err := os.Stat(c3RenamedMeta); os.IsNotExist(err) {
		t.Error("expected cycle 3 metadata to be renamed to cycle 2")
	}
	if _, err := os.Stat(c3File); !os.IsNotExist(err) {
		t.Error("expected old cycle 3 file to not exist anymore")
	}

	// Verify summary report was regenerated
	summaryBytes, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Errorf("expected summary report to exist, got error: %v", err)
	} else if !strings.Contains(string(summaryBytes), "Latest cycle: 002") ||
		!strings.Contains(string(summaryBytes), "MS-1-cycle-002.yaml") ||
		!strings.Contains(string(summaryBytes), "verdict: approved") {
		t.Errorf("expected summary report to list YAML cycle reports with parsed verdict, got %q", string(summaryBytes))
	}

	// Now delete remaining cycles to trigger cleanup
	err = DeleteMilestoneCycle(configPath, statePath, "MS-1", 2) // delete the new cycle 2
	if err != nil {
		t.Fatalf("DeleteMilestoneCycle of cycle 2 failed: %v", err)
	}
	err = DeleteMilestoneCycle(configPath, statePath, "MS-1", 1) // delete cycle 1
	if err != nil {
		t.Fatalf("DeleteMilestoneCycle of cycle 1 failed: %v", err)
	}

	// Verify summary report was deleted when 0 cycles are left
	if _, err := os.Stat(summaryFile); !os.IsNotExist(err) {
		t.Error("expected summary report to be deleted when 0 cycles are left")
	}
}
