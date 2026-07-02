package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AgentActionLog tracks an execution step.
type AgentActionLog struct {
	AgentID    string    `json:"agent_id"`
	Timestamp  time.Time `json:"timestamp"`
	ExitCode   int       `json:"exit_code"`
	InputFile  string    `json:"input_file"`
	OutputFile string    `json:"output_file"`
	Duration   string    `json:"duration,omitempty"`
}

// MilestoneCycleLog represents one run cycle of the milestone.
type MilestoneCycleLog struct {
	CycleNumber int              `json:"cycle_number"`
	Timestamp   time.Time        `json:"timestamp"`
	Branch      string           `json:"branch"`
	CommitHash  string           `json:"commit_hash,omitempty"`
	Status      string           `json:"status"` // "approved", "blocked", "failed"
	UserNote    string           `json:"user_note"`
	Actions     []AgentActionLog `json:"actions"`
	Duration    string           `json:"duration,omitempty"`
}

// State tracks the runtime / progress state of the milestones.
type State struct {
	mu                       sync.RWMutex                   `json:"-"`
	ActiveMilestoneID        string                         `json:"active_milestone_id"`
	MilestoneStatuses        map[string]string              `json:"milestone_statuses"` // milestone ID -> status
	MilestoneCycles          map[string]int                 `json:"milestone_cycles"`   // milestone ID -> cycle count
	MilestoneRecommendations map[string]int                 `json:"milestone_recommendations"`
	History                  map[string][]MilestoneCycleLog `json:"history"` // milestone ID -> list of cycles
}

// LoadState reads the state.json tracking file and migrates legacy formats if necessary.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return a clean default state
			return &State{
				MilestoneStatuses:        make(map[string]string),
				MilestoneCycles:          make(map[string]int),
				MilestoneRecommendations: make(map[string]int),
				History:                  make(map[string][]MilestoneCycleLog),
			}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	// Try to parse as new format first
	var st State
	var tempMap map[string]interface{}

	// We check if "history" is a JSON array by doing a raw parse.
	if err := json.Unmarshal(data, &tempMap); err == nil {
		if historyField, exists := tempMap["history"]; exists {
			if _, isArray := historyField.([]interface{}); isArray {
				// It's legacy! Let's force legacy parsing
				return migrateLegacyState(data, path)
			}
		}
	}

	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	if st.MilestoneStatuses == nil {
		st.MilestoneStatuses = make(map[string]string)
	}
	if st.MilestoneCycles == nil {
		st.MilestoneCycles = make(map[string]int)
	}
	if st.MilestoneRecommendations == nil {
		st.MilestoneRecommendations = make(map[string]int)
	}
	if st.History == nil {
		st.History = make(map[string][]MilestoneCycleLog)
	}

	return &st, nil
}

func migrateLegacyState(data []byte, path string) (*State, error) {
	type LegacyCycle struct {
		MilestoneID string `json:"milestone_id"`
		Action      string `json:"action"`
		Timestamp   string `json:"timestamp"`
		Branch      string `json:"branch"`
		CommitHash  string `json:"commit_hash,omitempty"`
	}
	type LegacyState struct {
		ActiveMilestoneID string            `json:"active_milestone_id"`
		MilestoneStatuses map[string]string `json:"milestone_statuses"`
		MilestoneCycles   map[string]int    `json:"milestone_cycles"`
		History           []LegacyCycle     `json:"history"`
	}

	var legacy LegacyState
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("failed to parse legacy state file: %w", err)
	}

	st := &State{
		ActiveMilestoneID:        legacy.ActiveMilestoneID,
		MilestoneStatuses:        legacy.MilestoneStatuses,
		MilestoneCycles:          legacy.MilestoneCycles,
		MilestoneRecommendations: make(map[string]int),
		History:                  make(map[string][]MilestoneCycleLog),
	}
	if st.MilestoneStatuses == nil {
		st.MilestoneStatuses = make(map[string]string)
	}
	if st.MilestoneCycles == nil {
		st.MilestoneCycles = make(map[string]int)
	}
	if st.MilestoneRecommendations == nil {
		st.MilestoneRecommendations = make(map[string]int)
	}

	// Map old cycles to new milestone cycle logs
	for _, lc := range legacy.History {
		t, err := time.Parse("2006-01-02 15:04:05", lc.Timestamp)
		if err != nil {
			t = time.Now()
		}

		existingLogs := st.History[lc.MilestoneID]
		cycleNum := len(existingLogs) + 1

		cycleLog := MilestoneCycleLog{
			CycleNumber: cycleNum,
			Timestamp:   t,
			Branch:      lc.Branch,
			CommitHash:  lc.CommitHash,
			Status:      "approved", // legacy default
			Actions:     []AgentActionLog{},
		}
		st.History[lc.MilestoneID] = append(existingLogs, cycleLog)
	}

	// Save the migrated state back to disk
	if err := SaveState(path, st); err != nil {
		return nil, fmt.Errorf("failed to save migrated state: %w", err)
	}

	return st, nil
}

// SaveState writes state.json back to disk.
func SaveState(path string, state *State) error {
	state.mu.Lock()
	defer state.mu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for state file: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// Thread-safe operations on State

func (s *State) GetActiveMilestoneID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveMilestoneID
}

func (s *State) SetActiveMilestoneID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ActiveMilestoneID = id
}

func (s *State) GetMilestoneStatus(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.MilestoneStatuses == nil {
		return "Todo"
	}
	if st, ok := s.MilestoneStatuses[id]; ok {
		return st
	}
	return "Todo"
}

func (s *State) SetMilestoneStatus(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MilestoneStatuses == nil {
		s.MilestoneStatuses = make(map[string]string)
	}
	s.MilestoneStatuses[id] = status
}

func (s *State) GetMilestoneCycles(id string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.MilestoneCycles == nil {
		return 0
	}
	if c, ok := s.MilestoneCycles[id]; ok {
		return c
	}
	return 0
}

func (s *State) IncrementMilestoneCycles(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MilestoneCycles == nil {
		s.MilestoneCycles = make(map[string]int)
	}
	s.MilestoneCycles[id]++
	return s.MilestoneCycles[id]
}

func (s *State) SetMilestoneCycles(id string, cycles int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MilestoneCycles == nil {
		s.MilestoneCycles = make(map[string]int)
	}
	s.MilestoneCycles[id] = cycles
}

func (s *State) GetHistory(id string) []MilestoneCycleLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.History == nil {
		return nil
	}
	logs := s.History[id]
	copied := make([]MilestoneCycleLog, len(logs))
	copy(copied, logs)
	return copied
}

func (s *State) AddCycleLog(id string, log MilestoneCycleLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.History == nil {
		s.History = make(map[string][]MilestoneCycleLog)
	}
	s.History[id] = append(s.History[id], log)
}

func (s *State) UpdateLastCycleLog(id string, updateFn func(log *MilestoneCycleLog)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.History == nil || len(s.History[id]) == 0 {
		return
	}
	logs := s.History[id]
	idx := len(logs) - 1
	updateFn(&logs[idx])
}

func (s *State) GetMilestoneRecommendation(id string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.MilestoneRecommendations == nil {
		return -1
	}
	if score, ok := s.MilestoneRecommendations[id]; ok {
		return score
	}
	return -1
}

func (s *State) SetMilestoneRecommendation(id string, score int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MilestoneRecommendations == nil {
		s.MilestoneRecommendations = make(map[string]int)
	}
	s.MilestoneRecommendations[id] = score
}

// DeleteMilestoneCycle removes a specific cycle, renumbers the remaining cycles sequentially, and renames report files to prevent collisions.
func DeleteMilestoneCycle(configPath, statePath, milestoneID string, cycleNum int) error {
	state, err := LoadState(statePath)
	if err != nil {
		return err
	}

	state.mu.Lock()
	logs, exists := state.History[milestoneID]
	if !exists {
		state.mu.Unlock()
		return fmt.Errorf("no history found for milestone %s", milestoneID)
	}

	foundIdx := -1
	for i, log := range logs {
		if log.CycleNumber == cycleNum {
			foundIdx = i
			break
		}
	}
	if foundIdx == -1 {
		state.mu.Unlock()
		return fmt.Errorf("cycle %d not found in milestone %s history", cycleNum, milestoneID)
	}

	reportsDir := filepath.Join(filepath.Dir(configPath), "reports")

	// Delete target cycle files
	targetPrefix := fmt.Sprintf("%s-cycle-%03d", milestoneID, cycleNum)
	files, err := os.ReadDir(reportsDir)
	if err == nil {
		for _, f := range files {
			if !f.IsDir() && strings.HasPrefix(f.Name(), targetPrefix) {
				_ = os.Remove(filepath.Join(reportsDir, f.Name()))
			}
		}
	}

	// Read reports directory again to get the updated list of files after deletion
	dirFiles, readErr := os.ReadDir(reportsDir)

	// Renumber remaining cycles in ascending order and rename files sequentially on disk to avoid collisions
	for i := foundIdx + 1; i < len(logs); i++ {
		oldNum := logs[i].CycleNumber
		newNum := oldNum - 1
		oldPrefix := fmt.Sprintf("%s-cycle-%03d", milestoneID, oldNum)
		newPrefix := fmt.Sprintf("%s-cycle-%03d", milestoneID, newNum)

		if readErr == nil {
			for _, f := range dirFiles {
				if !f.IsDir() && strings.HasPrefix(f.Name(), oldPrefix) {
					newName := strings.Replace(f.Name(), oldPrefix, newPrefix, 1)
					_ = os.Rename(filepath.Join(reportsDir, f.Name()), filepath.Join(reportsDir, newName))
				}
			}
		}
		logs[i].CycleNumber = newNum
	}

	// Remove from history slice
	logs = append(logs[:foundIdx], logs[foundIdx+1:]...)
	state.History[milestoneID] = logs
	state.MilestoneCycles[milestoneID] = len(logs)

	state.mu.Unlock()

	// Save updated state
	if err := SaveState(statePath, state); err != nil {
		return err
	}
	_ = updateCycleSummaryReportAfterDeletion(reportsDir, milestoneID, len(logs))
	return nil
}

func updateCycleSummaryReportAfterDeletion(reportsDir, milestoneID string, remainingCycles int) error {
	summaryPath := filepath.Join(reportsDir, fmt.Sprintf("%s.md", milestoneID))
	if remainingCycles == 0 {
		_ = os.Remove(summaryPath)
		return nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Milestone Cycle Summary: %s\n\n", milestoneID))
	sb.WriteString(fmt.Sprintf("- Milestone file: .cyclestone/milestones/%s.md\n", milestoneID))
	sb.WriteString(fmt.Sprintf("- Latest cycle: %03d\n", remainingCycles))
	sb.WriteString(fmt.Sprintf("- Updated: %s\n", time.Now().Format("2006-01-02 15:04:05 -0700")))
	sb.WriteString("\n## Cycle History\n\n")

	files, err := filepath.Glob(filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-*.md", milestoneID)))
	if err == nil {
		sort.Strings(files)
		for _, file := range files {
			baseName := filepath.Base(file)
			cyclePart := strings.TrimPrefix(baseName, milestoneID+"-cycle-")
			cyclePart = strings.TrimSuffix(cyclePart, ".md")

			var started, verdict string
			data, err := os.ReadFile(file)
			if err == nil {
				lines := strings.Split(string(data), "\n")
				for _, line := range lines {
					if strings.HasPrefix(line, "- Started:") {
						started = strings.TrimPrefix(line, "- Started:")
						started = strings.TrimSpace(started)
					}
					if strings.HasPrefix(line, "Exit status:") || strings.Contains(line, "verdict:") {
						verdict = line
					}
				}
			}

			sb.WriteString(fmt.Sprintf("- Cycle %s: .cyclestone/reports/%s", cyclePart, baseName))
			if started != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", started))
			}
			if verdict != "" {
				sb.WriteString(fmt.Sprintf(" - %s", verdict))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n## Continuation Guidance\n\n")
	sb.WriteString("For additional cycles, run from details screen inside cyclestone.\n")
	sb.WriteString("Later cycles should focus on unresolved QA findings, incomplete acceptance criteria, changed-file verification, and current repository state rather than restarting the milestone from scratch.\n")

	return os.WriteFile(summaryPath, []byte(sb.String()), 0644)
}
