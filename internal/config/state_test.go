package config

import (
	"fmt"
	"path/filepath"
	"testing"
)


func TestPlanExecutionConcurrentGetSet(t *testing.T) {
	t.Parallel()
	statePath := filepath.Join(t.TempDir(), "state.json")
	st, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	exec := &PlanExecution{
		Mode:               PlanExecutionModeContinuous,
		State:              "running",
		Checkpoint:         "briefing-selected",
		CurrentBriefingID: "b-pf-0001",
		CurrentMilestoneID: "ms-pf-0001",
		UpdatedAt:          "2026-07-21T10:00:00Z",
	}

	const goroutines = 50
	done := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			st.SetPlanExecution("plan-concurrent", exec)
			got := st.GetPlanExecution("plan-concurrent")
			if got == nil || got.State != "running" {
				done <- fmt.Errorf("goroutine %d: expected running state, got %v", n, got)
				return
			}
			done <- nil
		}(i)
	}
	for i := 0; i < goroutines; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	// Verify final state is consistent.
	got := st.GetPlanExecution("plan-concurrent")
	if got == nil || got.Mode != PlanExecutionModeContinuous || got.CurrentBriefingID != "b-pf-0001" {
		t.Fatalf("expected consistent execution state after concurrent access, got %+v", got)
	}

	// Delete should remove the entry.
	st.SetPlanExecution("plan-concurrent", nil)
	if got := st.GetPlanExecution("plan-concurrent"); got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}
}
