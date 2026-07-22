package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
runner_binary: "aider"
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
	if agent.RunnerBinary != "aider" {
		t.Errorf("expected runner_binary 'aider', got '%s'", agent.RunnerBinary)
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
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	// 1. Create an initial milestone via the folder-per-item layout.
	if _, err := SaveMilestoneToFolder(milestonesDir, Milestone{
		ID: "MS-1", Title: "First Milestone", Goal: "First goal",
		AcceptanceCriteria: []string{"Criterion 1"},
	}, ""); err != nil {
		t.Fatalf("SaveMilestoneToFolder MS-1 failed: %v", err)
	}

	// 2. Add new milestone via AddMilestone.
	newMs := Milestone{
		ID:                 "MS-2",
		Title:              "Second Milestone",
		Goal:               "Second goal",
		AcceptanceCriteria: []string{"Criterion A", "Criterion B"},
		Checks:             []string{"backend", "frontend"},
	}
	if err := AddMilestone(configPath, newMs); err != nil {
		t.Fatalf("AddMilestone failed: %v", err)
	}

	// 3. Load config and verify fields.
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
	if len(m2.Checks) != 2 || m2.Checks[0] != "backend" {
		t.Errorf("expected checks correct, got %v", m2.Checks)
	}
	// Verify the folder-per-item spec file exists and contains the goal.
	specFound := false
	if entries, err := os.ReadDir(milestonesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "MS-2") {
				specBytes, err := os.ReadFile(filepath.Join(milestonesDir, e.Name(), "MS-2-specification.md"))
				if err == nil && stringsContains(string(specBytes), "Second goal") {
					specFound = true
				}
			}
		}
	}
	if !specFound {
		t.Error("expected folder-per-item spec to contain the goal")
	}

	// 4. Verify duplicate prevention.
	duplicateMs := Milestone{ID: "MS-2", Title: "Duplicate Milestone"}
	if err := AddMilestone(configPath, duplicateMs); err == nil {
		t.Error("expected error when adding milestone with duplicate ID, got nil")
	}
}

func TestLoadConfigHydratesCompactSpec(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	// Create a legacy flat .md spec (no companion .yml metadata).
	specMarkdown := `# Milestone Spec: MS-1 - Compact Milestone

## Goal
Hydrate the goal from markdown.

## Acceptance Criteria
- [ ] First criterion
- [ ] Second criterion
`
	_ = os.WriteFile(filepath.Join(milestonesDir, "MS-1.md"), []byte(specMarkdown), 0644)

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Milestones) != 1 {
		t.Fatalf("expected 1 milestone, got %d", len(cfg.Milestones))
	}
	ms := cfg.Milestones[0]
	if ms.ID != "MS-1" {
		t.Errorf("expected ID 'MS-1', got %q", ms.ID)
	}
	if ms.Goal != "Hydrate the goal from markdown." {
		t.Errorf("expected hydrated goal, got %q", ms.Goal)
	}
	if len(ms.AcceptanceCriteria) != 2 || ms.AcceptanceCriteria[1] != "Second criterion" {
		t.Errorf("expected hydrated criteria, got %v", ms.AcceptanceCriteria)
	}
}

func TestAddMilestonePreservesExistingSpec(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	// Create a legacy flat .md spec.
	existingSpecPath := filepath.Join(milestonesDir, "MS-9.md")
	existingSpec := `# Milestone Spec: MS-9 - Generated Title

## Goal
Keep generated content.
`
	_ = os.WriteFile(existingSpecPath, []byte(existingSpec), 0644)

	// AddMilestone should detect the existing flat .md as a duplicate (it is
	// already loadable via LoadAllMilestonesFromDir) and fail. This verifies
	// that legacy flat specs are not silently overwritten.
	ms := Milestone{
		ID:       "MS-9",
		Title:    "Generated Title",
		Goal:     "Fallback goal should not overwrite existing file.",
		SpecPath: filepath.Join("milestones", "MS-9.md"),
	}
	if err := AddMilestone(configPath, ms); err == nil {
		t.Fatal("expected AddMilestone to fail when a legacy flat .md already exists for the same ID")
	}

	// The original flat .md must still be intact (not overwritten).
	specBytes, err := os.ReadFile(existingSpecPath)
	if err != nil {
		t.Fatalf("failed to read existing spec: %v", err)
	}
	if string(specBytes) != existingSpec {
		t.Errorf("expected existing spec to be preserved, got %s", string(specBytes))
	}

	// SaveMilestoneToFolder with the existing flat .md content preserves it.
	_, err = SaveMilestoneToFolder(milestonesDir, Milestone{
		ID:    "MS-9",
		Title: "Generated Title",
	}, existingSpec)
	if err != nil {
		t.Fatalf("SaveMilestoneToFolder failed: %v", err)
	}
	specFound := false
	if entries, err := os.ReadDir(milestonesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "MS-9") {
				specBytes, err := os.ReadFile(filepath.Join(milestonesDir, e.Name(), "MS-9-specification.md"))
				if err == nil && stringsContains(string(specBytes), "Keep generated content.") &&
					!stringsContains(string(specBytes), "Fallback goal should not overwrite") {
					specFound = true
				}
			}
		}
	}
	if !specFound {
		t.Error("expected folder-per-item spec to preserve existing content, not fallback goal")
	}
}

func TestAddMilestoneWithSpecWritesSuppliedSpecAndCompactIndex(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	spec := `# Milestone Spec: generated - Generated

## Goal
Use supplied spec content.

## Acceptance Criteria
- [ ] Supplied criterion

## Extra
Keep long-form context.
`
	ms := Milestone{ID: "generated", Title: "Generated"}
	if err := AddMilestoneWithSpec(configPath, ms, spec); err != nil {
		t.Fatalf("AddMilestoneWithSpec failed: %v", err)
	}
	// Read the folder-per-item spec.
	specBytes, err := os.ReadFile(filepath.Join(milestonesDir, "generated", "generated-specification.md"))
	if err != nil {
		t.Fatalf("expected supplied spec to be written: %v", err)
	}
	if string(specBytes) != spec {
		t.Fatalf("expected supplied spec to be preserved, got:\n%s", string(specBytes))
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Milestones) != 1 || cfg.Milestones[0].Goal != "Use supplied spec content." || len(cfg.Milestones[0].AcceptanceCriteria) != 1 {
		t.Fatalf("expected generated milestone to hydrate from supplied spec, got %+v", cfg.Milestones)
	}
	if err := AddMilestoneWithSpec(configPath, ms, spec); err == nil {
		t.Fatal("expected duplicate milestone ID to fail")
	}
}

func TestMigrateMilestoneStorage(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	legacyYAML := `milestones:
  - id: MS-1
    title: Legacy Milestone
    goal: Legacy goal
    acceptance_criteria:
      - Legacy criterion
    status: In Progress
    cycles: 3
`
	_ = os.WriteFile(configPath, []byte(legacyYAML), 0644)

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

	// The milestone.yml index should have been removed (no repositories to preserve).
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("expected milestone.yml to be removed after migration, got err=%v", err)
	}

	// The folder-per-item spec should exist with the legacy content.
	specFound := false
	if entries, err := os.ReadDir(milestonesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "MS-1") {
				specBytes, err := os.ReadFile(filepath.Join(milestonesDir, e.Name(), "MS-1-specification.md"))
				if err == nil && stringsContains(string(specBytes), "Legacy goal") && stringsContains(string(specBytes), "Legacy criterion") {
					specFound = true
				}
			}
		}
	}
	if !specFound {
		t.Error("expected folder-per-item spec to contain legacy definition")
	}

	// Verify LoadConfig sees the migrated milestone.
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Milestones) != 1 || cfg.Milestones[0].ID != "MS-1" {
		t.Fatalf("expected 1 milestone MS-1 after migration, got %+v", cfg.Milestones)
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
	if score := state.GetMilestoneAgentInstructionsUpdateScore("MS-1"); score != -1 {
		t.Errorf("expected default AGENTS.md update recommendation score to be -1, got %d", score)
	}

	// Set recommendation score
	state.SetMilestoneRecommendation("MS-1", 5)
	state.SetMilestoneAgentInstructionsUpdateScore("MS-1", 7)
	if score := state.GetMilestoneRecommendation("MS-1"); score != 5 {
		t.Errorf("expected recommendation score to be 5, got %d", score)
	}
	if score := state.GetMilestoneAgentInstructionsUpdateScore("MS-1"); score != 7 {
		t.Errorf("expected AGENTS.md update recommendation score to be 7, got %d", score)
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
	if score := stateReloaded.GetMilestoneAgentInstructionsUpdateScore("MS-1"); score != 7 {
		t.Errorf("expected reloaded AGENTS.md update recommendation score to be 7, got %d", score)
	}

	// Verify loaded non-existent recommendation score is -1
	if score := stateReloaded.GetMilestoneRecommendation("MS-2"); score != -1 {
		t.Errorf("expected non-existent reloaded recommendation score to be -1, got %d", score)
	}
	if score := stateReloaded.GetMilestoneAgentInstructionsUpdateScore("MS-2"); score != -1 {
		t.Errorf("expected non-existent reloaded AGENTS.md update recommendation score to be -1, got %d", score)
	}
}

func TestConfigRepositoriesSerialization(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	// milestone.yml carries repositories (milestones are now loaded from directories).
	initialYAML := `repositories:
  - backend
  - frontend
  - custom_dir
`
	_ = os.WriteFile(configPath, []byte(initialYAML), 0644)

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Repositories) != 3 || cfg.Repositories[0] != "backend" || cfg.Repositories[2] != "custom_dir" {
		t.Errorf("expected 3 repositories, got %v", cfg.Repositories)
	}

	// Test preservation on AddMilestone.
	newMs := Milestone{ID: "MS-2", Title: "Second Milestone"}
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
	if len(cfg2.Milestones) != 1 || cfg2.Milestones[0].ID != "MS-2" {
		t.Errorf("expected MS-2 in config, got %+v", cfg2.Milestones)
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
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	// Create two milestones in the folder-per-item layout.
	_, err := SaveMilestoneToFolder(milestonesDir, Milestone{ID: "MS-1", Title: "First MS"}, "")
	if err != nil {
		t.Fatalf("SaveMilestoneToFolder MS-1 failed: %v", err)
	}
	_, err = SaveMilestoneToFolder(milestonesDir, Milestone{ID: "MS-2", Title: "Second MS"}, "")
	if err != nil {
		t.Fatalf("SaveMilestoneToFolder MS-2 failed: %v", err)
	}

	state := &State{
		ActiveMilestoneID:                     "MS-1",
		MilestoneStatuses:                     map[string]string{"MS-1": "Done", "MS-2": "Todo"},
		MilestoneCycles:                       map[string]int{"MS-1": 1, "MS-2": 0},
		MilestoneRecommendations:              map[string]int{"MS-1": 2, "MS-2": 5},
		MilestoneAgentInstructionUpdateScores: map[string]int{"MS-1": 7, "MS-2": 1},
		History: map[string][]MilestoneCycleLog{
			"MS-1": {{CycleNumber: 1}},
		},
	}
	_ = SaveState(statePath, state)

	_ = os.MkdirAll(filepath.Join(tmpDir, "reports"), 0755)
	report1Dir := filepath.Join(tmpDir, "reports", "MS-1")
	report2Dir := filepath.Join(tmpDir, "reports", "MS-2")
	report1 := filepath.Join(report1Dir, "summary.md")
	report1Cycle := filepath.Join(report1Dir, "cycle-001", "report.yaml")
	report2 := filepath.Join(report2Dir, "summary.md")
	_ = os.MkdirAll(filepath.Dir(report1Cycle), 0755)
	_ = os.MkdirAll(report2Dir, 0755)
	_ = os.WriteFile(report1, []byte("report 1"), 0644)
	_ = os.WriteFile(report1Cycle, []byte("report 1 cycle 1"), 0644)
	_ = os.WriteFile(report2, []byte("report 2"), 0644)

	// Find the MS-1 folder path.
	var ms1Dir string
	if entries, err := os.ReadDir(milestonesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "MS-1") {
				ms1Dir = filepath.Join(milestonesDir, e.Name())
			}
		}
	}

	if err := DeleteMilestone(configPath, statePath, "MS-1"); err != nil {
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
	if _, exists := newState.MilestoneRecommendations["MS-1"]; exists {
		t.Error("expected MS-1 recommendation score to be deleted")
	}
	if _, exists := newState.MilestoneAgentInstructionUpdateScores["MS-1"]; exists {
		t.Error("expected MS-1 AGENTS.md update score to be deleted")
	}
	if got := newState.GetMilestoneAgentInstructionsUpdateScore("MS-2"); got != 1 {
		t.Errorf("expected MS-2 AGENTS.md update score to be preserved, got %d", got)
	}

	if ms1Dir != "" {
		if _, err := os.Stat(ms1Dir); !os.IsNotExist(err) {
			t.Error("expected MS-1 directory to be deleted")
		}
	}
	if _, err := os.Stat(report1Dir); !os.IsNotExist(err) {
		t.Error("expected MS-1 report directory to be deleted")
	}
	if _, err := os.Stat(report2); os.IsNotExist(err) {
		t.Error("expected report2 to be preserved")
	}
}

func TestDeleteMilestoneCycle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "milestone.yml")
	statePath := filepath.Join(tmpDir, "state.json")
	reportsDir := filepath.Join(tmpDir, "reports")

	state := &State{
		History: map[string][]MilestoneCycleLog{
			"MS-1": {
				{CycleNumber: 1},
				{CycleNumber: 2},
				{
					CycleNumber: 3,
					Actions: []AgentActionLog{
						{
							AgentID:    "developer",
							InputFile:  filepath.Join(".cyclestone", "reports", "MS-1", "cycle-003", "02-developer", "input.md"),
							OutputFile: filepath.Join(".cyclestone", "reports", "MS-1", "cycle-003", "02-developer", "output.log"),
						},
						{
							AgentID:    "qa",
							InputFile:  filepath.Join(reportsDir, "MS-1", "cycle-003", "03-qa", "input.md"),
							OutputFile: filepath.Join(reportsDir, "MS-1", "cycle-003", "03-qa", "output.log"),
						},
					},
				},
			},
		},
		MilestoneCycles: map[string]int{"MS-1": 3},
	}
	_ = SaveState(statePath, state)

	_ = os.MkdirAll(reportsDir, 0755)

	c1File := filepath.Join(reportsDir, "MS-1", "cycle-001", "report.yaml")
	c2File := filepath.Join(reportsDir, "MS-1", "cycle-002", "report.yaml")
	c3File := filepath.Join(reportsDir, "MS-1", "cycle-003", "report.yaml")
	c3Meta := filepath.Join(reportsDir, "MS-1", "cycle-003", "metadata.json")
	c3Handoff := filepath.Join(reportsDir, "MS-1", "cycle-003", "03-qa", "handoff.yaml")
	summaryFile := filepath.Join(reportsDir, "MS-1", "summary.md")

	_ = os.MkdirAll(filepath.Dir(c1File), 0755)
	_ = os.MkdirAll(filepath.Dir(c2File), 0755)
	_ = os.MkdirAll(filepath.Dir(c3Handoff), 0755)
	_ = os.WriteFile(c1File, []byte("started: \"2026-07-02 09:00:00 -0500\"\ndetails: |-\n  c1\n"), 0644)
	_ = os.WriteFile(c2File, []byte("started: \"2026-07-02 10:00:00 -0500\"\ndetails: |-\n  c2\n"), 0644)
	_ = os.WriteFile(c3File, []byte("started: \"2026-07-02 11:00:00 -0500\"\ndetails: |-\n  verdict: approved\n"), 0644)
	_ = os.WriteFile(c3Meta, []byte("c3meta"), 0644)
	_ = os.WriteFile(c3Handoff, []byte("summary:\n  verdict: approved\n"), 0644)
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
	if len(logs[1].Actions) != 2 {
		t.Fatalf("expected renamed cycle actions to be preserved, got %d", len(logs[1].Actions))
	}
	expectedRelativeInput := filepath.Join(".cyclestone", "reports", "MS-1", "cycle-002", "02-developer", "input.md")
	expectedRelativeOutput := filepath.Join(".cyclestone", "reports", "MS-1", "cycle-002", "02-developer", "output.log")
	if logs[1].Actions[0].InputFile != expectedRelativeInput || logs[1].Actions[0].OutputFile != expectedRelativeOutput {
		t.Errorf("expected relative action paths to be renumbered, got input %q output %q", logs[1].Actions[0].InputFile, logs[1].Actions[0].OutputFile)
	}
	expectedAbsoluteInput := filepath.Join(reportsDir, "MS-1", "cycle-002", "03-qa", "input.md")
	expectedAbsoluteOutput := filepath.Join(reportsDir, "MS-1", "cycle-002", "03-qa", "output.log")
	if logs[1].Actions[1].InputFile != expectedAbsoluteInput || logs[1].Actions[1].OutputFile != expectedAbsoluteOutput {
		t.Errorf("expected absolute action paths to be renumbered, got input %q output %q", logs[1].Actions[1].InputFile, logs[1].Actions[1].OutputFile)
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

	c3RenamedFile := filepath.Join(reportsDir, "MS-1", "cycle-002", "report.yaml")
	c3RenamedMeta := filepath.Join(reportsDir, "MS-1", "cycle-002", "metadata.json")
	c3RenamedHandoff := filepath.Join(reportsDir, "MS-1", "cycle-002", "03-qa", "handoff.yaml")
	if _, err := os.Stat(c3RenamedFile); os.IsNotExist(err) {
		t.Error("expected cycle 3 report to be renamed to cycle 2")
	}
	if _, err := os.Stat(c3RenamedMeta); os.IsNotExist(err) {
		t.Error("expected cycle 3 metadata to be renamed to cycle 2")
	}
	if _, err := os.Stat(c3RenamedHandoff); os.IsNotExist(err) {
		t.Error("expected cycle 3 handoff to be renamed to cycle 2")
	}
	if _, err := os.Stat(filepath.Join(reportsDir, "MS-1", "cycle-003")); !os.IsNotExist(err) {
		t.Error("expected old cycle 3 file to not exist anymore")
	}

	// Verify summary report was regenerated
	summaryBytes, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Errorf("expected summary report to exist, got error: %v", err)
	} else if !strings.Contains(string(summaryBytes), "Latest cycle: 002") ||
		!strings.Contains(string(summaryBytes), filepath.Join(reportsDir, "MS-1", "cycle-002", "report.yaml")) ||
		!strings.Contains(string(summaryBytes), "verdict: approved") ||
		strings.Contains(string(summaryBytes), "handoff.yaml") {
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

func TestSaveMilestoneToFolderRoundTripWithProvenance(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	milestonesDir := filepath.Join(tmpDir, "milestones")
	_ = os.MkdirAll(milestonesDir, 0755)

	ms := Milestone{
		ID:                 "ms-pf-0001",
		Title:              "Folder Round Trip",
		Goal:               "Verify provenance fields round-trip.",
		AcceptanceCriteria: []string{"Spec persists", "Provenance persists"},
		Checks:             []string{"go test"},
		CreatedBy:          "pf",
		UpdatedBy:          "pf",
		CreatedAt:          "2026-07-21T10:00:00Z",
		UpdatedAt:          "2026-07-21T11:00:00Z",
		ParentBriefingID:   "b-pf-0001",
		ParentPlanID:       "p-pf-0001",
	}
	dir, err := SaveMilestoneToFolder(milestonesDir, ms, "")
	if err != nil {
		t.Fatalf("SaveMilestoneToFolder failed: %v", err)
	}
	if dir == "" {
		t.Fatal("expected non-empty directory path")
	}

	loaded, err := LoadMilestoneFromDir(dir)
	if err != nil {
		t.Fatalf("LoadMilestoneFromDir failed: %v", err)
	}
	if loaded.ID != ms.ID || loaded.Title != ms.Title {
		t.Errorf("expected ID=%s Title=%s, got ID=%s Title=%s", ms.ID, ms.Title, loaded.ID, loaded.Title)
	}
	if loaded.Goal != ms.Goal {
		t.Errorf("expected Goal=%q, got %q", ms.Goal, loaded.Goal)
	}
	if len(loaded.AcceptanceCriteria) != 2 || loaded.AcceptanceCriteria[0] != "Spec persists" {
		t.Errorf("expected acceptance criteria to round-trip, got %v", loaded.AcceptanceCriteria)
	}
	if loaded.CreatedBy != "pf" || loaded.UpdatedBy != "pf" {
		t.Errorf("expected provenance CreatedBy/UpdatedBy to persist, got CreatedBy=%q UpdatedBy=%q", loaded.CreatedBy, loaded.UpdatedBy)
	}
	if loaded.CreatedAt != ms.CreatedAt || loaded.UpdatedAt != ms.UpdatedAt {
		t.Errorf("expected provenance timestamps to persist, got CreatedAt=%q UpdatedAt=%q", loaded.CreatedAt, loaded.UpdatedAt)
	}
	if loaded.ParentBriefingID != "b-pf-0001" || loaded.ParentPlanID != "p-pf-0001" {
		t.Errorf("expected parent links to persist, got ParentBriefingID=%q ParentPlanID=%q", loaded.ParentBriefingID, loaded.ParentPlanID)
	}
}

func TestAuthorPrefixCollisionFreeAllocation(t *testing.T) {
	t.Parallel()
	// Two distinct author prefixes must produce zero ID collisions across all
	// allocation functions.
	authorA := "pf"
	authorB := "js"

	planIDsA := AllocatePlanID(authorA, nil)
	planIDsB := AllocatePlanID(authorB, nil)
	if planIDsA == planIDsB {
		t.Fatalf("distinct author prefixes produced identical Plan IDs: %s", planIDsA)
	}

	existing := []string{planIDsA, planIDsB}
	// Allocate 10 plan IDs for each author and verify no collisions.
	allPlanIDs := make(map[string]bool)
	for i := 0; i < 10; i++ {
		idA := AllocatePlanID(authorA, existing)
		idB := AllocatePlanID(authorB, existing)
		if allPlanIDs[idA] || allPlanIDs[idB] {
			t.Fatalf("Plan ID collision: %s or %s already allocated", idA, idB)
		}
		allPlanIDs[idA] = true
		allPlanIDs[idB] = true
		existing = append(existing, idA, idB)
	}

	// Milestone IDs inheriting parent Plan prefix
	existingMS := []string{}
	for i := 0; i < 10; i++ {
		idA := AllocateMilestoneID("p-pf-0001", "", existingMS)
		idB := AllocateMilestoneID("p-js-0001", "", existingMS)
		if idA == idB {
			t.Fatalf("distinct parent prefixes produced identical Milestone IDs: %s", idA)
		}
		existingMS = append(existingMS, idA, idB)
	}

	// Briefing IDs inheriting parent Plan prefix
	existingB := []string{}
	for i := 0; i < 10; i++ {
		idA := AllocateBriefingID("p-pf-0001", "", existingB)
		idB := AllocateBriefingID("p-js-0001", "", existingB)
		if idA == idB {
			t.Fatalf("distinct parent prefixes produced identical Briefing IDs: %s", idA)
		}
		existingB = append(existingB, idA, idB)
	}
}

func TestStampMilestoneProvenanceFillsEmptyFields(t *testing.T) {
	t.Parallel()
	ms := &Milestone{ID: "ms-pf-0001-test"}
	StampMilestoneProvenance(ms, "tui", "2026-07-21T12:00:00Z")
	if ms.CreatedBy != "tui" || ms.UpdatedBy != "tui" {
		t.Fatalf("expected CreatedBy/UpdatedBy=tui, got %q/%q", ms.CreatedBy, ms.UpdatedBy)
	}
	if ms.CreatedAt != "2026-07-21T12:00:00Z" || ms.UpdatedAt != "2026-07-21T12:00:00Z" {
		t.Fatalf("expected timestamps to be set, got CreatedAt=%q UpdatedAt=%q", ms.CreatedAt, ms.UpdatedAt)
	}
}

func TestStampMilestoneProvenancePreservesExistingFields(t *testing.T) {
	t.Parallel()
	ms := &Milestone{
		ID:        "ms-pf-0001-test",
		CreatedBy: "original-author",
		UpdatedBy: "last-editor",
		CreatedAt: "2026-07-01T08:00:00Z",
		UpdatedAt: "2026-07-02T09:00:00Z",
	}
	StampMilestoneProvenance(ms, "tui", "2026-07-21T12:00:00Z")
	if ms.CreatedBy != "original-author" || ms.UpdatedBy != "last-editor" {
		t.Fatalf("expected existing provenance to be preserved, got CreatedBy=%q UpdatedBy=%q", ms.CreatedBy, ms.UpdatedBy)
	}
	if ms.CreatedAt != "2026-07-01T08:00:00Z" || ms.UpdatedAt != "2026-07-02T09:00:00Z" {
		t.Fatalf("expected existing timestamps to be preserved, got CreatedAt=%q UpdatedAt=%q", ms.CreatedAt, ms.UpdatedAt)
	}
}

func TestStampMilestoneProvenanceNilSafe(t *testing.T) {
	t.Parallel()
	StampMilestoneProvenance(nil, "tui", "2026-07-21T12:00:00Z")
}
