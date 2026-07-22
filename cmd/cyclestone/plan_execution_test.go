package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/executor"
	"github.com/patrick-folster/cyclestone/internal/tui"
)

func TestPlanExecutionGeneratesOnceContinuesAndIgnoresStandaloneMilestone(t *testing.T) {
	originalAdd := addPlanningMilestoneWithSpec
	generations := 0
	addPlanningMilestoneWithSpec = func(path string, milestone config.Milestone, spec string) error {
		generations++
		return originalAdd(path, milestone, spec)
	}
	t.Cleanup(func() { addPlanningMilestoneWithSpec = originalAdd })
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{
		{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"},
		{ID: "standalone", Title: "Standalone", SpecPath: "milestones/standalone.md"},
	}, config.Plan{
		SchemaVersion: 1, ID: "delivery", Title: "Delivery", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"generated", "reused"},
		Briefings: []config.Briefing{
			executionBriefing("generated", nil, ""),
			executionBriefing("reused", []string{"generated"}, "linked"),
		},
	})
	standaloneBefore, err := os.ReadFile(filepath.Join(root, ".cyclestone", "milestones", "standalone", "standalone-specification.md"))
	if err != nil {
		t.Fatal(err)
	}
	state, _ := config.LoadState(statePath)
	state.SetMilestoneStatus("standalone", "In Progress")
	state.SetMilestoneCycles("standalone", 2)
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	unrelatedPaths := []string{
		filepath.Join(root, ".cyclestone", "reports", "milestones", "standalone", "summary.md"),
		filepath.Join(root, ".cyclestone", "reports", "milestones", "standalone", "cycle-002", "report.yaml"),
		filepath.Join(root, ".cyclestone", "reports", "milestones", "standalone", "cycle-002", "03-qa", "handoff.yaml"),
		filepath.Join(root, ".cyclestone", "branch-snapshots", "standalone.json"),
	}
	for i, path := range unrelatedPaths {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("controlled unrelated artifact "+string(rune('A'+i))+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	unrelatedBefore := snapshotUnrelatedMilestone(t, configPath, statePath, "standalone", unrelatedPaths)
	assertUnrelatedMilestoneUnchanged := func(stage string) {
		t.Helper()
		after := snapshotUnrelatedMilestone(t, configPath, statePath, "standalone", unrelatedPaths)
		for name, before := range unrelatedBefore {
			if !bytes.Equal(after[name], before) {
				t.Fatalf("%s changed unrelated artifact %s", stage, name)
			}
		}
	}

	var launched tui.RootModel
	code := runPlanExecution([]string{"delivery", "--mode", "continuous"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(model tui.RootModel) error {
		launched = model
		return nil
	})
	if code != 0 || launched.PendingCycle == nil {
		t.Fatalf("expected generated Briefing launch, code=%d", code)
	}
	assertUnrelatedMilestoneUnchanged("initial selection")
	first := launched.PendingCycle.BriefingOrigin
	if first.BriefingID != "generated" || launched.PendingCycle.Milestone.ID != "ms-pf-0001-generated" {
		t.Fatalf("unexpected first selection: %+v / %+v", first, launched.PendingCycle.Milestone)
	}
	if err := launched.PlanCycleStarted(first); err != nil {
		t.Fatal(err)
	}
	state, _ = config.LoadState(statePath)
	state.SetMilestoneStatus("ms-pf-0001-generated", "Approved")
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	next, err := launched.PlanCycleFinished(first, first.MilestoneID, "approved", nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.NextMilestone == nil || next.NextMilestone.ID != "linked" || next.NextOrigin.BriefingID != "reused" {
		t.Fatalf("expected linked Milestone reuse, got %+v", next)
	}
	if generations != 1 {
		t.Fatalf("pre-existing link triggered generation: generations=%d", generations)
	}
	assertUnrelatedMilestoneUnchanged("generated completion")
	if err := launched.PlanCycleStarted(next.NextOrigin); err != nil {
		t.Fatal(err)
	}
	state, _ = config.LoadState(statePath)
	state.SetMilestoneStatus("linked", "Approved")
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	done, err := launched.PlanCycleFinished(next.NextOrigin, "linked", "approved", nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.NextMilestone != nil || !strings.Contains(done.Message, "checkpoint exhausted") {
		t.Fatalf("expected terminal queue exhaustion, got %+v", done)
	}
	assertUnrelatedMilestoneUnchanged("queue exhaustion")

	state, _ = config.LoadState(statePath)
	if state.GetMilestoneCycles("standalone") != 2 || state.GetMilestoneStatus("standalone") != "In Progress" {
		t.Fatalf("standalone runtime state changed: %+v", state)
	}
	standaloneAfter, _ := os.ReadFile(filepath.Join(root, ".cyclestone", "milestones", "standalone", "standalone-specification.md"))
	if string(standaloneAfter) != string(standaloneBefore) {
		t.Fatal("standalone Milestone spec was modified")
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"linked", "standalone", "ms-pf-0001-generated"}))
	plan, _ := findPlan(planning, "delivery")
	generated, _ := findBriefing(plan, "generated")
	if generated.Status != "completed" || generated.MilestoneID != "ms-pf-0001-generated" {
		t.Fatalf("generated Briefing not durably reconciled: %+v", generated)
	}
	if generations != 1 {
		t.Fatalf("mixed Plan generated %d Milestones, want exactly one", generations)
	}
}

func TestPlanExecutionTerminalFailureThroughRootModelStopsRepeatedResume(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
		SchemaVersion: 1, ID: "failure-plan", Title: "Failure", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"first", "dependent"}, Briefings: []config.Briefing{executionBriefing("first", nil, "linked"), executionBriefing("dependent", []string{"first"}, "")},
	})
	launches := 0
	var model tui.RootModel
	if code := runPlanExecution([]string{"failure-plan", "--mode", "continuous"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(queued tui.RootModel) error {
		launches++
		model = queued
		return queued.PlanCycleStarted(queued.PendingCycle.BriefingOrigin)
	}); code != 0 {
		t.Fatalf("start failed: %d", code)
	}
	origin := model.PendingCycle.BriefingOrigin
	model.BriefingOrigin = origin
	state, _ := config.LoadState(statePath)
	state.SetMilestoneStatus("linked", "Failed")
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	updated, _ := model.Update(executor.CycleFinishedMsg{MilestoneID: "linked", Status: "failed", Error: errors.New("injected terminal failure")})
	model = updated.(tui.RootModel)
	assertStoppedPlanExecution(t, root, "failure-plan", "first", "linked", "injected terminal failure")
	for attempt := 1; attempt <= 2; attempt++ {
		var stdout bytes.Buffer
		if code := runPlanExecution([]string{"failure-plan"}, executionTestOptions(configPath, statePath), true, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
			t.Fatalf("resume %d failed: %d", attempt, code)
		}
		if launches != 1 || !strings.Contains(stdout.String(), "linked Milestone is failed") {
			t.Fatalf("resume %d relaunched or hid stop: launches=%d output=%s", attempt, launches, stdout.String())
		}
		assertStoppedPlanExecution(t, root, "failure-plan", "first", "linked", "linked Milestone is failed")
	}
}

func TestPlanExecutionCancellationThroughRootModelStopsRepeatedResume(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
		SchemaVersion: 1, ID: "cancel-plan", Title: "Cancel", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"first", "dependent"}, Briefings: []config.Briefing{executionBriefing("first", nil, "linked"), executionBriefing("dependent", []string{"first"}, "")},
	})
	launches := 0
	var model tui.RootModel
	if code := runPlanExecution([]string{"cancel-plan", "--mode", "continuous"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(queued tui.RootModel) error {
		launches++
		queued.CycleExecutor = func(context.Context, config.Milestone, []config.Agent, executor.RunOptions, *config.State, chan tea.Msg) {
		}
		start := *queued.PendingCycle
		updated, _ := queued.Update(start)
		model = updated.(tui.RootModel)
		return nil
	}); code != 0 {
		t.Fatalf("start failed: %d", code)
	}
	state, _ := config.LoadState(statePath)
	state.SetMilestoneStatus("linked", "Failed")
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	updated, routeCmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if routeCmd == nil {
		t.Fatal("cancel key did not return navigation command")
	}
	routed, _ := updated.(tui.RootModel).Update(routeCmd())
	model = routed.(tui.RootModel)
	updated, _ = model.Update(executor.CycleFinishedMsg{MilestoneID: "linked", Status: "failed", Error: context.Canceled})
	model = updated.(tui.RootModel)
	assertStoppedPlanExecution(t, root, "cancel-plan", "first", "linked", "context canceled")
	for attempt := 1; attempt <= 2; attempt++ {
		var stdout bytes.Buffer
		if code := runPlanExecution([]string{"cancel-plan"}, executionTestOptions(configPath, statePath), true, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
			t.Fatalf("resume %d failed: %d", attempt, code)
		}
		if launches != 1 || !strings.Contains(stdout.String(), "linked Milestone is failed") {
			t.Fatalf("resume %d relaunched or hid cancellation: launches=%d output=%s", attempt, launches, stdout.String())
		}
		assertStoppedPlanExecution(t, root, "cancel-plan", "first", "linked", "linked Milestone is failed")
	}
}

func TestPlanExecutionReconcilesApprovedLinkWithoutLaunching(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "done", Title: "Done", SpecPath: "milestones/done.md"}}, config.Plan{
		SchemaVersion: 1, ID: "approved-plan", Title: "Approved", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "done")},
	})
	state, _ := config.LoadState(statePath)
	state.SetMilestoneStatus("done", "Approved")
	_ = config.SaveState(statePath, state)
	launches := 0
	var stdout, stderr bytes.Buffer
	code := runPlanExecution([]string{"approved-plan", "--mode", "continuous"}, executionTestOptions(configPath, statePath), false, &stdout, &stderr, func(tui.RootModel) error { launches++; return nil })
	if code != 0 || launches != 0 {
		t.Fatalf("expected reconciliation without launch, code=%d launches=%d stderr=%s", code, launches, stderr.String())
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"done"}))
	plan, _ := findPlan(planning, "approved-plan")
	st, _ := config.LoadState(statePath)
	exec := st.GetPlanExecution("approved-plan")
	if plan.Briefings[0].Status != "completed" || exec.State != "completed" || exec.Checkpoint != "exhausted" {
		t.Fatalf("unexpected reconciled Plan: %+v exec=%+v", plan, exec)
	}
}

func TestPlanExecutionOncePausesAfterOneReconciliation(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "done", Title: "Done", SpecPath: "milestones/done.md"}}, config.Plan{
		SchemaVersion: 1, ID: "once-plan", Title: "Once", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"first", "second"}, Briefings: []config.Briefing{executionBriefing("first", nil, "done"), executionBriefing("second", []string{"first"}, "")},
	})
	state, _ := config.LoadState(statePath)
	state.SetMilestoneStatus("done", "Approved")
	_ = config.SaveState(statePath, state)
	launches := 0
	if code := runPlanExecution([]string{"once-plan", "--mode", "once"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 || launches != 0 {
		t.Fatalf("once reconciliation failed: code=%d launches=%d", code, launches)
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"done"}))
	plan, _ := findPlan(planning, "once-plan")
	st2, _ := config.LoadState(statePath)
	exec2 := st2.GetPlanExecution("once-plan")
	if exec2.State != "paused" || exec2.Checkpoint != "one-complete" || plan.Briefings[1].Status != "active" {
		t.Fatalf("once mode did not pause before dependent: %+v exec=%+v", plan, exec2)
	}
}

func TestPlanExecutionStopsOnDanglingLinkAndPreservesIt(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, nil, config.Plan{
		SchemaVersion: 1, ID: "dangling-plan", Title: "Dangling", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "missing")},
	})
	launches := 0
	var stdout bytes.Buffer
	code := runPlanExecution([]string{"dangling-plan"}, executionTestOptions(configPath, statePath), false, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil })
	if code != 0 || launches != 0 || !strings.Contains(stdout.String(), "repair it explicitly") {
		t.Fatalf("expected actionable safe stop, code=%d launches=%d output=%s", code, launches, stdout.String())
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"))
	plan, _ := findPlan(planning, "dangling-plan")
	st3, _ := config.LoadState(statePath)
	exec3 := st3.GetPlanExecution("dangling-plan")
	if plan.Briefings[0].MilestoneID != "missing" || exec3.State != "stopped" || exec3.CurrentBriefingID != "work" {
		t.Fatalf("dangling link/current item not preserved: %+v exec=%+v", plan, exec3)
	}
}

func TestPlanExecutionReviewGateRequiresMatchingApproval(t *testing.T) {
	_, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
		SchemaVersion: 1, ID: "review-plan", Title: "Review", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "linked")},
	})
	launches := 0
	var stdout bytes.Buffer
	if code := runPlanExecution([]string{"review-plan", "--mode", "review"}, executionTestOptions(configPath, statePath), false, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
		t.Fatalf("start failed: %d", code)
	}
	if launches != 0 || !strings.Contains(stdout.String(), "resume with --approve") {
		t.Fatalf("review gate did not pause: launches=%d output=%s", launches, stdout.String())
	}
	if code := runPlanExecution([]string{"review-plan", "--approve"}, executionTestOptions(configPath, statePath), true, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 || launches != 1 {
		t.Fatalf("approved resume did not launch once: code=%d launches=%d", code, launches)
	}
}

func TestPlanExecutionInterruptedRunningCycleDoesNotRelaunch(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
		SchemaVersion: 1, ID: "interrupt-plan", Title: "Interrupt", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "linked")},
	})
	launches := 0
	if code := runPlanExecution([]string{"interrupt-plan"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(model tui.RootModel) error {
		launches++
		return model.PlanCycleStarted(model.PendingCycle.BriefingOrigin)
	}); code != 0 {
		t.Fatalf("start failed: %d", code)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		var stdout bytes.Buffer
		if code := runPlanExecution([]string{"interrupt-plan"}, executionTestOptions(configPath, statePath), true, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
			t.Fatalf("resume %d failed: %d", attempt, code)
		}
		if launches != 1 || !strings.Contains(stdout.String(), "no terminal Milestone result") {
			t.Fatalf("resume %d relaunched or was not explained: launches=%d output=%s", attempt, launches, stdout.String())
		}
		planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"linked"}))
		_, _ = findPlan(planning, "interrupt-plan")
		stInt, _ := config.LoadState(statePath)
		execInt := stInt.GetPlanExecution("interrupt-plan")
		if execInt.Checkpoint != "cycle-running" || execInt.CurrentBriefingID != "work" || execInt.CurrentMilestoneID != "linked" {
			t.Fatalf("resume %d erased the uncertain launch boundary: %+v", attempt, execInt)
		}
	}
}

func TestPlanExecutionPendingAndRunningBoundariesRevalidateLinks(t *testing.T) {
	for _, checkpoint := range []string{"cycle-pending", "cycle-running"} {
		t.Run(checkpoint, func(t *testing.T) {
			root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{
				{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"},
				{ID: "edited", Title: "Edited", SpecPath: "milestones/edited.md"},
			}, config.Plan{
				SchemaVersion: 1, ID: "identity-plan", Title: "Identity", Objective: "Ship", Status: "completed",
				BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "linked")},
			})
			var queued tui.RootModel
			if code := runPlanExecution([]string{"identity-plan"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(model tui.RootModel) error {
				queued = model
				if checkpoint == "cycle-running" {
					return model.PlanCycleStarted(model.PendingCycle.BriefingOrigin)
				}
				return nil
			}); code != 0 {
				t.Fatalf("start failed: %d", code)
			}
			if queued.PendingCycle == nil {
				t.Fatal("expected initial queued cycle")
			}
			plansDir := filepath.Join(root, ".cyclestone", "plans")
			planning, _ := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs([]string{"linked", "edited"}))
			plan, _ := findPlan(planning, "identity-plan")
			plan.Briefings[0].MilestoneID = "edited"
			plan.Briefings[0].UpdatedAt = planExecutionTimestamp(plan.Briefings[0].CreatedAt)
			plan.UpdatedAt = plan.Briefings[0].UpdatedAt
			if _, validation, err := config.SavePlanToFolder(plansDir, plan, config.WithKnownMilestoneIDs([]string{"linked", "edited"})); err != nil || validation.HasErrors() {
				t.Fatalf("edit link: %v %+v", err, validation)
			}
			launches := 0
			var stdout bytes.Buffer
			if code := runPlanExecution([]string{"identity-plan"}, executionTestOptions(configPath, statePath), true, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
				t.Fatalf("resume failed: %d", code)
			}
			if launches != 0 || !strings.Contains(stdout.String(), "instead of retained Milestone") {
				t.Fatalf("changed link was not stopped: launches=%d output=%s", launches, stdout.String())
			}
			planning, _ = config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs([]string{"linked", "edited"}))
			plan, _ = findPlan(planning, "identity-plan")
			stId, _ := config.LoadState(statePath)
			execId := stId.GetPlanExecution("identity-plan")
			if execId.Checkpoint != checkpoint || execId.CurrentMilestoneID != "linked" {
				t.Fatalf("identity stop erased durable boundary: %+v", execId)
			}
		})
	}
}

func TestFinishPlanCycleRejectsStaleTerminalMilestone(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
		SchemaVersion: 1, ID: "stale-plan", Title: "Stale", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "linked")},
	})
	var model tui.RootModel
	if code := runPlanExecution([]string{"stale-plan"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(queued tui.RootModel) error {
		model = queued
		return queued.PlanCycleStarted(queued.PendingCycle.BriefingOrigin)
	}); code != 0 {
		t.Fatalf("start failed: %d", code)
	}
	origin := model.PendingCycle.BriefingOrigin
	step, err := finishPlanCycle(configPath, statePath, origin, "other", "approved", nil)
	if err != nil || !strings.Contains(step.message, "ignored stale terminal event") {
		t.Fatalf("stale event was not durably stopped: step=%+v err=%v", step, err)
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"linked"}))
	plan, _ := findPlan(planning, "stale-plan")
	stStale, _ := config.LoadState(statePath)
	execStale := stStale.GetPlanExecution("stale-plan")
	if execStale.State != "stopped" || execStale.Checkpoint != "cycle-running" || plan.Briefings[0].Status != "active" {
		t.Fatalf("stale event advanced or erased execution: %+v exec=%+v", plan, execStale)
	}
}

func TestPlanExecutionResumeStopsWhenCurrentBriefingWasRemoved(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
		SchemaVersion: 1, ID: "removed-plan", Title: "Removed", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "linked")},
	})
	if code := runPlanExecution([]string{"removed-plan"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { return nil }); code != 0 {
		t.Fatalf("start failed: %d", code)
	}
	plansDir := filepath.Join(root, ".cyclestone", "plans")
	planning, _ := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs([]string{"linked"}))
	plan, _ := findPlan(planning, "removed-plan")
	plan.Briefings = nil
	plan.BriefingOrder = nil
	plan.UpdatedAt = planExecutionTimestamp(plan.CreatedAt)
	if _, validation, err := config.SavePlanToFolder(plansDir, plan, config.WithKnownMilestoneIDs([]string{"linked"})); err != nil || validation.HasErrors() {
		t.Fatalf("remove current Briefing: %v %+v", err, validation)
	}
	launches := 0
	var stdout bytes.Buffer
	if code := runPlanExecution([]string{"removed-plan"}, executionTestOptions(configPath, statePath), true, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
		t.Fatalf("resume failed: %d", code)
	}
	if launches != 0 || !strings.Contains(stdout.String(), `current Briefing "work" no longer exists`) {
		t.Fatalf("removed current Briefing was not stopped: launches=%d output=%s", launches, stdout.String())
	}
	planning, _ = config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs([]string{"linked"}))
	plan, _ = findPlan(planning, "removed-plan")
	stRem, _ := config.LoadState(statePath)
	execRem := stRem.GetPlanExecution("removed-plan")
	if execRem.State != "stopped" || execRem.Checkpoint != "cycle-pending" || execRem.CurrentBriefingID != "work" || execRem.CurrentMilestoneID != "linked" {
		t.Fatalf("removed Briefing stop did not preserve identity: %+v", execRem)
	}
}

func TestPlanExecutionTerminalStopsDoNotAdvanceDependents(t *testing.T) {
	tests := []struct {
		name            string
		milestoneStatus string
		milestoneID     string
		wantState       string
		wantCheckpoint  string
	}{
		{name: "failed linked milestone", milestoneStatus: "Failed", milestoneID: "linked", wantState: "stopped", wantCheckpoint: "stopped"},
		{name: "dependency deadlock", wantState: "blocked", wantCheckpoint: "dependency-deadlock"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			milestones := []config.Milestone{}
			firstLink := ""
			dependencies := []string{"archived-prerequisite"}
			briefings := []config.Briefing{
				executionBriefing("archived-prerequisite", nil, ""),
				executionBriefing("dependent", dependencies, ""),
			}
			briefings[0].Status = "archived"
			order := []string{"archived-prerequisite", "dependent"}
			if tc.milestoneID != "" {
				milestones = append(milestones, config.Milestone{ID: tc.milestoneID, Title: "Linked", SpecPath: "milestones/linked.md"})
				firstLink = tc.milestoneID
				briefings = []config.Briefing{executionBriefing("first", nil, firstLink), executionBriefing("dependent", []string{"first"}, "")}
				order = []string{"first", "dependent"}
			}
			root, configPath, statePath := writePlanExecutionFixture(t, milestones, config.Plan{SchemaVersion: 1, ID: "stop-plan", Title: "Stop", Objective: "Ship", Status: "completed", BriefingOrder: order, Briefings: briefings})
			if tc.milestoneStatus != "" {
				state, _ := config.LoadState(statePath)
				state.SetMilestoneStatus(tc.milestoneID, tc.milestoneStatus)
				_ = config.SaveState(statePath, state)
			}
			launches := 0
			if code := runPlanExecution([]string{"stop-plan", "--mode", "continuous"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
				t.Fatalf("start failed: %d", code)
			}
			if launches != 0 {
				t.Fatalf("unexpected dependent launch count %d", launches)
			}
			known := []string{}
			if tc.milestoneID != "" {
				known = append(known, tc.milestoneID)
			}
			planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs(known))
			plan, _ := findPlan(planning, "stop-plan")
			stStop, _ := config.LoadState(statePath)
			execStop := stStop.GetPlanExecution("stop-plan")
			if execStop.State != tc.wantState || execStop.Checkpoint != tc.wantCheckpoint || plan.Briefings[len(plan.Briefings)-1].Status != "active" {
				t.Fatalf("unexpected stop state: %+v exec=%+v", plan, execStop)
			}
			if execStop.CurrentBriefingID == "" {
				t.Fatalf("stopped Plan did not retain a resumable current Briefing: %+v", execStop)
			}
		})
	}
}

func TestPlanExecutionRecoversIndexedMilestoneAfterLinkSaveFailure(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, nil, config.Plan{
		SchemaVersion: 1, ID: "recover-plan", Title: "Recover", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "")},
	})
	originalSave, originalAdd := savePlanningPlan, addPlanningMilestoneWithSpec
	t.Cleanup(func() { savePlanningPlan, addPlanningMilestoneWithSpec = originalSave, originalAdd })
	saves, generations := 0, 0
	savePlanningPlan = func(dir string, plan config.Plan, options ...config.PlanningValidationOption) (string, config.PlanningValidationResult, error) {
		saves++
		if saves == 4 {
			return "", config.PlanningValidationResult{}, errors.New("injected link save failure")
		}
		return originalSave(dir, plan, options...)
	}
	addPlanningMilestoneWithSpec = func(path string, milestone config.Milestone, spec string) error {
		generations++
		return originalAdd(path, milestone, spec)
	}

	if code := runPlanExecution([]string{"recover-plan"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { return nil }); code != 1 {
		t.Fatalf("expected injected link failure, got code %d", code)
	}
	savePlanningPlan = originalSave
	launches := 0
	if code := runPlanExecution([]string{"recover-plan"}, executionTestOptions(configPath, statePath), true, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
		t.Fatalf("resume failed with code %d", code)
	}
	if generations != 1 || launches != 1 {
		t.Fatalf("recovery duplicated generation or missed launch: generations=%d launches=%d", generations, launches)
	}
	cfg, _ := config.LoadConfig(configPath)
	if len(cfg.Milestones) != 1 || cfg.Milestones[0].ID != "ms-pf-0001-work" {
		t.Fatalf("unexpected compact index after recovery: %+v", cfg.Milestones)
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"ms-pf-0001-work"}))
	plan, _ := findPlan(planning, "recover-plan")
	stRec, _ := config.LoadState(statePath)
	execRec := stRec.GetPlanExecution("recover-plan")
	if plan.Briefings[0].MilestoneID != "ms-pf-0001-work" || execRec.Checkpoint != "cycle-pending" {
		t.Fatalf("recovered link/checkpoint was not durable: %+v exec=%+v", plan, execRec)
	}
}

func TestPlanExecutionRecoversSpecOnlyGenerationBoundary(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, nil, config.Plan{
		SchemaVersion: 1, ID: "spec-plan", Title: "Spec", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "")},
	})
	originalAdd := addPlanningMilestoneWithSpec
	t.Cleanup(func() { addPlanningMilestoneWithSpec = originalAdd })
	generations := 0
	addPlanningMilestoneWithSpec = func(path string, milestone config.Milestone, spec string) error {
		generations++
		writePath := filepath.Join(filepath.Dir(path), milestone.SpecPath)
		if err := os.WriteFile(writePath, []byte(spec), 0644); err != nil {
			return err
		}
		return errors.New("injected index failure")
	}
	if code := runPlanExecution([]string{"spec-plan"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { return nil }); code != 1 {
		t.Fatalf("expected injected index failure, got code %d", code)
	}
	addPlanningMilestoneWithSpec = originalAdd
	launches := 0
	if code := runPlanExecution([]string{"spec-plan"}, executionTestOptions(configPath, statePath), true, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 {
		t.Fatalf("resume failed with code %d", code)
	}
	if generations != 1 || launches != 1 {
		t.Fatalf("spec recovery duplicated generation or launch: generations=%d launches=%d", generations, launches)
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"ms-pf-0001-work"}))
	plan, _ := findPlan(planning, "spec-plan")
	if plan.Briefings[0].MilestoneID != "ms-pf-0001-work" {
		t.Fatalf("spec-only recovery did not persist link: %+v", plan.Briefings[0])
	}
}

func TestPlanExecutionRevalidatesApprovalAndPreflightIdentity(t *testing.T) {
	for _, checkpoint := range []string{"approval", "preflight"} {
		t.Run(checkpoint, func(t *testing.T) {
			root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
				SchemaVersion: 1, ID: "live-plan", Title: "Live", Objective: "Ship", Status: "completed",
				BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "linked")},
			})
			mode := "continuous"
			if checkpoint == "approval" {
				mode = "review"
			}
			var queued tui.RootModel
			if code := runPlanExecution([]string{"live-plan", "--mode", mode}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(model tui.RootModel) error { queued = model; return nil }); code != 0 {
				t.Fatalf("start failed: %d", code)
			}
			plansDir := filepath.Join(root, ".cyclestone", "plans")
			planning, _ := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs([]string{"linked"}))
			plan, _ := findPlan(planning, "live-plan")
			plan.Briefings[0].Status = "archived"
			plan.Briefings[0].UpdatedAt = planExecutionTimestamp(plan.Briefings[0].CreatedAt)
			plan.UpdatedAt = plan.Briefings[0].UpdatedAt
			if _, validation, err := config.SavePlanToFolder(plansDir, plan, config.WithKnownMilestoneIDs([]string{"linked"})); err != nil || validation.HasErrors() {
				t.Fatalf("archive current Briefing: %v %+v", err, validation)
			}
			if checkpoint == "approval" {
				launches := 0
				var stdout bytes.Buffer
				if code := runPlanExecution([]string{"live-plan", "--approve"}, executionTestOptions(configPath, statePath), true, &stdout, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 || launches != 0 || !strings.Contains(stdout.String(), "no longer executable") {
					t.Fatalf("stale approval launched: code=%d launches=%d output=%s", code, launches, stdout.String())
				}
			} else if err := queued.PlanCycleStarted(queued.PendingCycle.BriefingOrigin); err == nil || !strings.Contains(err.Error(), "no longer executable") {
				t.Fatalf("stale preflight identity was launched: %v", err)
			}
			planning, _ = config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs([]string{"linked"}))
			plan, _ = findPlan(planning, "live-plan")
			stLive, _ := config.LoadState(statePath)
			execLive := stLive.GetPlanExecution("live-plan")
			if execLive.State != "stopped" || execLive.CurrentBriefingID != "work" || execLive.CurrentMilestoneID != "linked" {
				t.Fatalf("identity stop did not retain current IDs: %+v", execLive)
			}
		})
	}
}

func TestPlanExecutionReconcilesApprovedAfterCompletionSaveFailure(t *testing.T) {
	root, configPath, statePath := writePlanExecutionFixture(t, []config.Milestone{{ID: "linked", Title: "Linked", SpecPath: "milestones/linked.md"}}, config.Plan{
		SchemaVersion: 1, ID: "completion-plan", Title: "Completion", Objective: "Ship", Status: "completed",
		BriefingOrder: []string{"work"}, Briefings: []config.Briefing{executionBriefing("work", nil, "linked")},
	})
	var queued tui.RootModel
	if code := runPlanExecution([]string{"completion-plan", "--mode", "continuous"}, executionTestOptions(configPath, statePath), false, &bytes.Buffer{}, &bytes.Buffer{}, func(model tui.RootModel) error {
		queued = model
		return model.PlanCycleStarted(model.PendingCycle.BriefingOrigin)
	}); code != 0 {
		t.Fatalf("start failed: %d", code)
	}
	state, _ := config.LoadState(statePath)
	state.SetMilestoneStatus("linked", "Approved")
	_ = config.SaveState(statePath, state)
	originalSave := savePlanningPlan
	t.Cleanup(func() { savePlanningPlan = originalSave })
	savePlanningPlan = func(string, config.Plan, ...config.PlanningValidationOption) (string, config.PlanningValidationResult, error) {
		return "", config.PlanningValidationResult{}, errors.New("injected completion save failure")
	}
	origin := queued.PendingCycle.BriefingOrigin
	if _, err := queued.PlanCycleFinished(origin, origin.MilestoneID, "approved", nil); err == nil {
		t.Fatal("expected completion save failure")
	}
	savePlanningPlan = originalSave
	launches := 0
	if code := runPlanExecution([]string{"completion-plan"}, executionTestOptions(configPath, statePath), true, &bytes.Buffer{}, &bytes.Buffer{}, func(tui.RootModel) error { launches++; return nil }); code != 0 || launches != 0 {
		t.Fatalf("approved reconciliation reran cycle: code=%d launches=%d", code, launches)
	}
	planning, _ := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{"linked"}))
	plan, _ := findPlan(planning, "completion-plan")
	stComp, _ := config.LoadState(statePath)
	execComp := stComp.GetPlanExecution("completion-plan")
	if plan.Briefings[0].Status != "completed" || execComp.Checkpoint != "exhausted" {
		t.Fatalf("approved completion was not reconciled: %+v exec=%+v", plan, execComp)
	}
}

func writePlanExecutionFixture(t *testing.T, milestones []config.Milestone, plan config.Plan) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	cyclestoneDir := filepath.Join(root, ".cyclestone")
	configPath, statePath := filepath.Join(cyclestoneDir, "milestone.yml"), filepath.Join(cyclestoneDir, "state.json")
	if err := os.MkdirAll(filepath.Join(cyclestoneDir, "milestones"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatal(err)
	}
	for _, milestone := range milestones {
		if err := config.AddMilestoneWithSpec(configPath, milestone, "# "+milestone.Title+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := config.LoadState(statePath)
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	plan.CreatedAt, plan.UpdatedAt, plan.CreatedBy, plan.UpdatedBy = now, now, "test", "test"
	for i := range plan.Briefings {
		plan.Briefings[i].CreatedAt, plan.Briefings[i].UpdatedAt, plan.Briefings[i].CreatedBy, plan.Briefings[i].UpdatedBy = now, now, "test", "test"
	}
	ids := make([]string, 0, len(milestones))
	for _, milestone := range milestones {
		ids = append(ids, milestone.ID)
	}
	if _, validation, err := config.SavePlanToFolder(filepath.Join(cyclestoneDir, "plans"), plan, config.WithKnownMilestoneIDs(ids)); err != nil || validation.HasErrors() {
		t.Fatalf("save Plan: %v %+v", err, validation)
	}
	return root, configPath, statePath
}

func executionBriefing(id string, dependencies []string, milestoneID string) config.Briefing {
	return config.Briefing{ID: id, Title: strings.Title(id), Objective: "Do " + id, Intent: "Ship", Status: "active", CompletionSignal: "Done", DependsOn: dependencies, MilestoneID: milestoneID}
}

func executionTestOptions(configPath, statePath string) briefingExecutionOptions {
	return briefingExecutionOptions{configPath: configPath, statePath: statePath, noBranchChange: true}
}

func snapshotUnrelatedMilestone(t *testing.T, configPath, statePath, milestoneID string, artifactPaths []string) map[string][]byte {
	t.Helper()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var indexed config.Milestone
	for _, milestone := range cfg.Milestones {
		if milestone.ID == milestoneID {
			indexed = milestone
			break
		}
	}
	if indexed.ID == "" {
		t.Fatalf("controlled Milestone %q disappeared from compact index", milestoneID)
	}
	indexBytes, err := json.Marshal(indexed)
	if err != nil {
		t.Fatal(err)
	}
	state, err := config.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	stateBytes, err := json.Marshal(struct {
		Status  string                     `json:"status"`
		Cycles  int                        `json:"cycles"`
		History []config.MilestoneCycleLog `json:"history"`
	}{state.GetMilestoneStatus(milestoneID), state.GetMilestoneCycles(milestoneID), state.History[milestoneID]})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string][]byte{"compact index entry": indexBytes, "runtime state entry": stateBytes}
	for _, path := range artifactPaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		snapshot[path] = data
	}
	return snapshot
}

func assertStoppedPlanExecution(t *testing.T, root, planID, briefingID, milestoneID, reason string) {
	t.Helper()
	planning, validation := config.LoadPlanningState(filepath.Join(root, ".cyclestone", "plans"), config.WithKnownMilestoneIDs([]string{milestoneID}))
	if validation.HasErrors() {
		t.Fatalf("reload stopped Plan: %+v", validation)
	}
	plan, ok := findPlan(planning, planID)
	if !ok {
		t.Fatalf("Plan %q disappeared", planID)
	}
	st, _ := config.LoadState(filepath.Join(root, ".cyclestone", "state.json"))
	exec := st.GetPlanExecution(planID)
	if exec == nil {
		t.Fatalf("Plan %q execution checkpoint disappeared from state", planID)
	}
	if exec.State != "stopped" || exec.Checkpoint != "stopped" || exec.CurrentBriefingID != briefingID || exec.CurrentMilestoneID != milestoneID || !strings.Contains(exec.StopReason, reason) {
		t.Fatalf("unexpected durable stop: %+v", exec)
	}
	current, ok := findBriefing(plan, briefingID)
	if !ok || current.Status != "active" {
		t.Fatalf("current Briefing advanced after terminal stop: %+v", current)
	}
	dependent, ok := findBriefing(plan, "dependent")
	if !ok || dependent.Status != "active" || dependent.MilestoneID != "" {
		t.Fatalf("dependent Briefing advanced or generated: %+v", dependent)
	}
}
