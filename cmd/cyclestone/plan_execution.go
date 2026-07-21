package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/tui"
)

type planExecutionRequest struct {
	planID  string
	mode    string
	approve bool
	resume  bool
}

type planExecutionStep struct {
	milestone *config.Milestone
	origin    tui.BriefingOrigin
	message   string
}

func runPlanExecution(args []string, opts briefingExecutionOptions, resume bool, stdout, stderr io.Writer, launch func(tui.RootModel) error) int {
	req, err := parsePlanExecutionRequest(args, resume)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if req.mode == "" && !resume {
		req.mode = config.LoadMergedSettings().DefaultPlanExecutionMode
	}
	if req.mode != "" && !config.IsValidPlanExecutionMode(req.mode) {
		fmt.Fprintf(stderr, "Error: invalid Plan execution mode %q (use once, continuous, or review)\n", req.mode)
		return 1
	}

	if !resume {
		if err := initializePlanExecution(opts.configPath, opts.statePath, req.planID, req.mode); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	} else if err := validatePlanResume(opts.configPath, opts.statePath, req.planID, req.mode); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	step, err := advancePlanExecution(opts.configPath, opts.statePath, req.planID, req.approve)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if step.message != "" {
		fmt.Fprintln(stdout, step.message)
	}
	if step.milestone == nil {
		return 0
	}
	return launchPlanExecutionStep(step, opts, stdout, stderr, launch)
}

func parsePlanExecutionRequest(args []string, resume bool) (planExecutionRequest, error) {
	req := planExecutionRequest{resume: resume}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--approve":
			req.approve = true
		case args[i] == "--mode" && i+1 < len(args):
			i++
			req.mode = args[i]
		case strings.HasPrefix(args[i], "--mode="):
			req.mode = strings.TrimPrefix(args[i], "--mode=")
		case strings.HasPrefix(args[i], "-"):
			return req, fmt.Errorf("unknown Plan execution option %q", args[i])
		case req.planID == "":
			req.planID = args[i]
		default:
			return req, fmt.Errorf("usage: cyclestone plan %s <plan-id> [--mode once|continuous|review] [--approve]", planExecutionVerb(resume))
		}
	}
	if req.planID == "" {
		return req, fmt.Errorf("usage: cyclestone plan %s <plan-id> [--mode once|continuous|review] [--approve]", planExecutionVerb(resume))
	}
	if !resume && req.approve {
		return req, fmt.Errorf("--approve is valid only with plan resume")
	}
	return req, nil
}

func planExecutionVerb(resume bool) string {
	if resume {
		return "resume"
	}
	return "start"
}

func initializePlanExecution(configPath, statePath, planID, mode string) error {
	ctx, err := loadPlanningCommandContext(configPath)
	if err != nil {
		return err
	}
	if ctx.validation.HasErrors() {
		return fmt.Errorf("planning files contain validation errors; no changes were written")
	}
	plan, ok := findPlan(ctx.state, planID)
	if !ok {
		return fmt.Errorf("Plan %q not found", planID)
	}
	if plan.Status != "completed" {
		return fmt.Errorf("Plan %q must be approved (status completed) before execution", planID)
	}
	state, err := config.LoadState(statePath)
	if err != nil {
		return err
	}
	exec := state.GetPlanExecution(planID)
	if exec != nil && (exec.State == "running" || exec.State == "paused" || exec.State == "stopped" || exec.State == "blocked") {
		return fmt.Errorf("Plan %q already has resumable execution state; use plan resume", planID)
	}
	exec = &config.PlanExecution{Mode: mode, State: "running", Checkpoint: "queue-selection", UpdatedAt: planExecutionTimestamp(plan.CreatedAt)}
	plan.UpdatedAt = exec.UpdatedAt
	plan.UpdatedBy = "plan-executor"
	if !savePlanForCommand(ctx, plan, io.Discard) {
		return fmt.Errorf("failed to persist Plan metadata")
	}
	if err := saveExecutionState(statePath, state, planID, exec); err != nil {
		return fmt.Errorf("failed to persist Plan execution start: %w", err)
	}
	return nil
}

func validatePlanResume(configPath, statePath, planID, explicitMode string) error {
	ctx, err := loadPlanningCommandContext(configPath)
	if err != nil {
		return err
	}
	if ctx.validation.HasErrors() {
		return fmt.Errorf("planning files contain validation errors; no changes were written")
	}
	plan, ok := findPlan(ctx.state, planID)
	if !ok {
		return fmt.Errorf("Plan %q not found", planID)
	}
	state, err := config.LoadState(statePath)
	if err != nil {
		return err
	}
	exec := state.GetPlanExecution(planID)
	if exec == nil {
		return fmt.Errorf("Plan %q has not been started; use plan start", planID)
	}
	if explicitMode != "" && explicitMode != exec.Mode {
		exec.Mode = explicitMode
		exec.UpdatedAt = planExecutionTimestamp(plan.CreatedAt)
		if err := saveExecutionState(statePath, state, planID, exec); err != nil {
			return fmt.Errorf("failed to persist Plan execution mode: %w", err)
		}
	}
	return nil
}

func advancePlanExecution(configPath, statePath, planID string, approval bool) (planExecutionStep, error) {
	for {
		ctx, err := loadPlanningCommandContext(configPath)
		if err != nil {
			return planExecutionStep{}, err
		}
		if ctx.validation.HasErrors() {
			return planExecutionStep{}, fmt.Errorf("planning files contain validation errors")
		}
		plan, ok := findPlan(ctx.state, planID)
		if !ok {
			return planExecutionStep{}, fmt.Errorf("Plan %q execution state is unavailable", planID)
		}
		state, err := config.LoadState(statePath)
		if err != nil {
			return planExecutionStep{}, err
		}
		exec := state.GetPlanExecution(planID)
		if exec == nil {
			return planExecutionStep{}, fmt.Errorf("Plan %q execution state is unavailable", planID)
		}
		if exec.Checkpoint == "approval-required" {
			if _, _, identityErr := currentPlanExecutionIdentity(ctx, plan, exec); identityErr != nil {
				return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", identityErr.Error()+"; approval was not consumed and the Plan execution identity must be repaired")
			}
		}

		if exec.Checkpoint == "cycle-running" {
			_, _, identityErr := currentPlanExecutionIdentity(ctx, plan, exec)
			if identityErr != nil {
				return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", identityErr.Error()+"; repair the Plan execution identity before resuming")
			}
			switch normalizeRuntimeStatus(state.GetMilestoneStatus(exec.CurrentMilestoneID)) {
			case "approved":
				if err := completeCurrentPlanBriefing(configPath, statePath, ctx, state, plan, exec, exec.CurrentMilestoneID); err != nil {
					return planExecutionStep{}, err
				}
				if exec.Mode == config.PlanExecutionModeOnce {
					return pausePlanAfterOne(configPath, statePath, ctx, state, plan, exec)
				}
				continue
			case "failed", "blocked":
				return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", "linked Milestone is "+normalizeRuntimeStatus(state.GetMilestoneStatus(exec.CurrentMilestoneID)))
			default:
				return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", "cycle launch was recorded but no terminal Milestone result exists; inspect the cycle and resume after its state is reconciled")
			}
		}

		if exec.CurrentBriefingID != "" && exec.Checkpoint == "cycle-pending" {
			_, milestone, identityErr := currentPlanExecutionIdentity(ctx, plan, exec)
			if identityErr != nil {
				return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", identityErr.Error()+"; repair the Plan execution identity before resuming")
			}
			return planExecutionStep{milestone: &milestone, origin: planBriefingOrigin(plan, exec, exec.CurrentBriefingID, milestone.ID), message: planExecutionSummary(plan, exec)}, nil
		}

		briefing, position, total, selection := selectNextPlanBriefing(plan)
		if selection == "exhausted" {
			exec.State, exec.Checkpoint, exec.StopReason = "completed", "exhausted", "all non-archived Briefings are completed"
			exec.CurrentBriefingID, exec.CurrentMilestoneID, exec.PendingApproval = "", "", ""
			if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
				return planExecutionStep{}, err
			}
			return planExecutionStep{message: planExecutionSummary(plan, exec)}, nil
		}
		if selection == "blocked" {
			exec.State, exec.Checkpoint, exec.StopReason = "blocked", "dependency-deadlock", "incomplete Briefings remain but none have completed dependencies"
			exec.CurrentBriefingID, exec.CurrentMilestoneID, exec.PendingApproval = briefing.ID, briefing.MilestoneID, ""
			if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
				return planExecutionStep{}, err
			}
			return planExecutionStep{message: planExecutionSummary(plan, exec)}, nil
		}

		exec.State, exec.Checkpoint, exec.StopReason = "running", "briefing-selected", ""
		exec.CurrentBriefingID, exec.CurrentMilestoneID = briefing.ID, briefing.MilestoneID
		if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
			return planExecutionStep{}, err
		}

		ctx, err = loadPlanningCommandContext(configPath)
		if err != nil {
			return planExecutionStep{}, err
		}
		plan, _ = findPlan(ctx.state, planID)
		briefing, _ = findBriefing(plan, briefing.ID)
		var milestone config.Milestone
		if briefing.MilestoneID != "" {
			var exists bool
			milestone, exists = milestoneByID(ctx, briefing.MilestoneID)
			if !exists {
				return stopPlanExecution(configPath, statePath, ctx, state, plan, exec, "stopped", fmt.Sprintf("Briefing %q links missing Milestone %q; repair it explicitly, then resume", briefing.ID, briefing.MilestoneID))
			}
		} else {
			result, err := prepareBriefingMilestone(ctx, configPath, briefingMilestoneRequest{planID: plan.ID, briefingID: briefing.ID, actor: "plan-executor", allowActive: true, allowLinked: true})
			if err != nil {
				fresh, _, loadErr := loadPlanningForExecution(configPath, planID)
				if loadErr == nil {
					freshState, _ := config.LoadState(statePath)
					freshExec := freshState.GetPlanExecution(planID)
					if freshExec != nil {
						_, _ = stopPlanExecution(configPath, statePath, fresh, freshState, planFromContext(fresh, planID), freshExec, "stopped", err.Error()+"; repair the durable generation/link boundary and resume")
					}
				}
				return planExecutionStep{}, err
			}
			milestone = result.Milestone
			ctx, err = loadPlanningCommandContext(configPath)
			if err != nil {
				return planExecutionStep{}, err
			}
			plan, _ = findPlan(ctx.state, planID)
		}

		state, err = config.LoadState(statePath)
		if err != nil {
			return planExecutionStep{}, err
		}
		exec = state.GetPlanExecution(planID)
		switch normalizeRuntimeStatus(state.GetMilestoneStatus(milestone.ID)) {
		case "approved":
			if err := completeCurrentPlanBriefing(configPath, statePath, ctx, state, plan, exec, milestone.ID); err != nil {
				return planExecutionStep{}, err
			}
			if exec.Mode == config.PlanExecutionModeOnce {
				return pausePlanAfterOne(configPath, statePath, ctx, state, plan, exec)
			}
			continue
		case "failed", "blocked":
			return stopPlanExecution(configPath, statePath, ctx, state, plan, exec, "stopped", "linked Milestone is "+normalizeRuntimeStatus(state.GetMilestoneStatus(milestone.ID)))
		}

		exec.CurrentMilestoneID = milestone.ID
		exec.Checkpoint = "milestone-linked"
		gate := "before-cycle:" + briefing.ID + ":" + milestone.ID
		if exec.Mode == config.PlanExecutionModeReview && (!approval || exec.PendingApproval != gate) {
			exec.State, exec.Checkpoint, exec.PendingApproval = "paused", "approval-required", gate
			exec.StopReason = "explicit approval is required before launching the Milestone cycle"
			if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
				return planExecutionStep{}, err
			}
			return planExecutionStep{message: planExecutionSummary(plan, exec)}, nil
		}
		exec.State, exec.Checkpoint, exec.PendingApproval, exec.StopReason = "running", "cycle-pending", "", ""
		if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
			return planExecutionStep{}, err
		}
		origin := planBriefingOrigin(plan, exec, briefing.ID, milestone.ID)
		origin.QueuePosition, origin.QueueTotal = position, total
		return planExecutionStep{milestone: &milestone, origin: origin, message: planExecutionSummary(plan, exec)}, nil
	}
}

func launchPlanExecutionStep(step planExecutionStep, opts briefingExecutionOptions, stdout, stderr io.Writer, launch func(tui.RootModel) error) int {
	cfg, err := config.LoadConfig(opts.configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	state, err := config.LoadState(opts.statePath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	root := tui.NewRootModel(cfg, state, opts.configPath, opts.statePath, opts.noBranchChange, opts.unrestricted, opts.disableBold, opts.disableRoundedBorders)
	root.PlanCycleStarted = func(origin tui.BriefingOrigin) error { return markPlanCycleStarted(opts.configPath, opts.statePath, origin) }
	root.PlanCycleFinished = func(origin tui.BriefingOrigin, milestoneID, status string, cycleErr error) (tui.PlanContinuation, error) {
		next, err := finishPlanCycle(opts.configPath, opts.statePath, origin, milestoneID, status, cycleErr)
		if err != nil {
			return tui.PlanContinuation{}, err
		}
		return tui.PlanContinuation{NextMilestone: next.milestone, NextOrigin: next.origin, Message: next.message}, nil
	}
	root.QueuePlanCycle(*step.milestone, step.origin)
	if err := launch(root); err != nil {
		fmt.Fprintf(stderr, "Error running TUI: %v\n", err)
		return 1
	}
	return 0
}

func markPlanCycleStarted(configPath, statePath string, origin tui.BriefingOrigin) error {
	ctx, plan, err := loadPlanningForExecution(configPath, origin.PlanID)
	if err != nil {
		return err
	}
	state, err := config.LoadState(statePath)
	if err != nil {
		return err
	}
	exec := state.GetPlanExecution(origin.PlanID)
	if exec == nil || exec.Checkpoint != "cycle-pending" || exec.CurrentBriefingID != origin.BriefingID || exec.CurrentMilestoneID != origin.MilestoneID {
		return fmt.Errorf("Plan execution identity or checkpoint changed; resume to reconcile")
	}
	if _, _, identityErr := currentPlanExecutionIdentity(ctx, plan, exec); identityErr != nil {
		_, stopErr := stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", identityErr.Error()+"; Milestone cycle was not launched")
		if stopErr != nil {
			return fmt.Errorf("%v (also failed to persist the stop: %w)", identityErr, stopErr)
		}
		return fmt.Errorf("%v; Milestone cycle was not launched", identityErr)
	}
	exec.Checkpoint = "cycle-running"
	return saveExecutionState(statePath, state, origin.PlanID, exec)
}

func finishPlanCycle(configPath, statePath string, origin tui.BriefingOrigin, terminalMilestoneID, status string, cycleErr error) (planExecutionStep, error) {
	ctx, plan, err := loadPlanningForExecution(configPath, origin.PlanID)
	if err != nil {
		return planExecutionStep{}, err
	}
	state, err := config.LoadState(statePath)
	if err != nil {
		return planExecutionStep{}, err
	}
	exec := state.GetPlanExecution(origin.PlanID)
	if exec == nil {
		return planExecutionStep{}, fmt.Errorf("Plan execution state disappeared while Milestone %q ran; planning was not advanced", origin.MilestoneID)
	}
	if terminalMilestoneID != origin.MilestoneID {
		return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", fmt.Sprintf("ignored stale terminal event for Milestone %q while Plan execution expected %q; inspect the active cycle before resuming", terminalMilestoneID, origin.MilestoneID))
	}
	if exec.CurrentBriefingID != origin.BriefingID || exec.CurrentMilestoneID != origin.MilestoneID || exec.Checkpoint != "cycle-running" {
		return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", fmt.Sprintf("Plan execution identity changed while Milestone %q ran; planning was not advanced", origin.MilestoneID))
	}
	if _, _, identityErr := currentPlanExecutionIdentity(ctx, plan, exec); identityErr != nil {
		return stopPlanExecutionPreservingCheckpoint(configPath, statePath, ctx, state, plan, exec, "stopped", identityErr.Error()+"; planning was not advanced")
	}
	persistedStatus := normalizeRuntimeStatus(state.GetMilestoneStatus(origin.MilestoneID))
	if cycleErr != nil || normalizeRuntimeStatus(status) != "approved" || persistedStatus != "approved" {
		reason := "Milestone cycle did not finish approved"
		if cycleErr != nil {
			reason += ": " + cycleErr.Error()
		} else if persistedStatus != normalizeRuntimeStatus(status) {
			reason += fmt.Sprintf(": terminal message was %q but persisted state is %q", status, persistedStatus)
		}
		return stopPlanExecution(configPath, statePath, ctx, state, plan, exec, "stopped", reason)
	}
	if err := completeCurrentPlanBriefing(configPath, statePath, ctx, state, plan, exec, origin.MilestoneID); err != nil {
		return planExecutionStep{}, err
	}
	if exec.Mode == config.PlanExecutionModeOnce {
		return pausePlanAfterOne(configPath, statePath, ctx, state, plan, exec)
	}
	return advancePlanExecution(configPath, statePath, origin.PlanID, false)
}

func completeCurrentPlanBriefing(configPath, statePath string, ctx planningCommandContext, state *config.State, plan config.Plan, exec *config.PlanExecution, milestoneID string) error {
	briefing, ok := findBriefing(plan, exec.CurrentBriefingID)
	index := briefingIndex(plan, exec.CurrentBriefingID)
	if !ok || briefing.MilestoneID != milestoneID {
		return fmt.Errorf("current Briefing/Milestone link changed; planning was not advanced")
	}
	now := planExecutionTimestamp(plan.CreatedAt, briefing.CreatedAt)
	plan.Briefings[index].Status, plan.Briefings[index].UpdatedAt, plan.Briefings[index].UpdatedBy = "completed", now, "plan-executor"
	exec.CurrentBriefingID, exec.CurrentMilestoneID, exec.PendingApproval = "", "", ""
	exec.State, exec.Checkpoint, exec.StopReason = "running", "briefing-completed", ""
	return persistExecAndPlan(configPath, statePath, ctx, state, plan, exec)
}

func pausePlanAfterOne(configPath, statePath string, ctx planningCommandContext, state *config.State, plan config.Plan, exec *config.PlanExecution) (planExecutionStep, error) {
	exec.State, exec.Checkpoint, exec.StopReason = "paused", "one-complete", "execution mode stops after one eligible Briefing"
	if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
		return planExecutionStep{}, err
	}
	return planExecutionStep{message: planExecutionSummary(plan, exec)}, nil
}

func stopPlanExecution(configPath, statePath string, ctx planningCommandContext, state *config.State, plan config.Plan, exec *config.PlanExecution, stateName, reason string) (planExecutionStep, error) {
	exec.State, exec.StopReason = stateName, reason
	if stateName == "stopped" {
		exec.Checkpoint = "stopped"
	}
	if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
		return planExecutionStep{}, err
	}
	return planExecutionStep{message: planExecutionSummary(plan, exec)}, nil
}

// stopPlanExecutionPreservingCheckpoint records an actionable stop without
// erasing a durable launch boundary. In particular, cycle-running must remain
// non-launchable across repeated resumes until Milestone state is terminal.
func stopPlanExecutionPreservingCheckpoint(configPath, statePath string, ctx planningCommandContext, state *config.State, plan config.Plan, exec *config.PlanExecution, stateName, reason string) (planExecutionStep, error) {
	exec.State, exec.StopReason = stateName, reason
	if err := persistExecAndPlan(configPath, statePath, ctx, state, plan, exec); err != nil {
		return planExecutionStep{}, err
	}
	return planExecutionStep{message: planExecutionSummary(plan, exec)}, nil
}

// persistExecAndPlan saves the Plan execution state to state.json and the Plan
// metadata (UpdatedAt/UpdatedBy, briefing updates) to the Plan file.
func persistExecAndPlan(configPath, statePath string, ctx planningCommandContext, state *config.State, plan config.Plan, exec *config.PlanExecution) error {
	now := planExecutionTimestamp(plan.CreatedAt)
	exec.UpdatedAt, plan.UpdatedAt, plan.UpdatedBy = now, now, "plan-executor"
	if err := saveExecutionState(statePath, state, plan.ID, exec); err != nil {
		return fmt.Errorf("failed to persist Plan execution checkpoint: %w", err)
	}
	if !savePlanForCommand(ctx, plan, io.Discard) {
		return fmt.Errorf("failed to persist Plan metadata")
	}
	return nil
}

// saveExecutionState sets the execution in state and persists state.json.
func saveExecutionState(statePath string, state *config.State, planID string, exec *config.PlanExecution) error {
	state.SetPlanExecution(planID, exec)
	return config.SaveState(statePath, state)
}

func loadPlanningForExecution(configPath, planID string) (planningCommandContext, config.Plan, error) {
	ctx, err := loadPlanningCommandContext(configPath)
	if err != nil {
		return ctx, config.Plan{}, err
	}
	if ctx.validation.HasErrors() {
		return ctx, config.Plan{}, fmt.Errorf("planning files contain validation errors")
	}
	plan, ok := findPlan(ctx.state, planID)
	if !ok {
		return ctx, config.Plan{}, fmt.Errorf("Plan %q not found", planID)
	}
	return ctx, plan, nil
}

func planFromContext(ctx planningCommandContext, planID string) config.Plan {
	plan, _ := findPlan(ctx.state, planID)
	return plan
}

func milestoneByID(ctx planningCommandContext, id string) (config.Milestone, bool) {
	cfg, err := config.LoadConfig(ctx.configPath)
	if err != nil {
		return config.Milestone{}, false
	}
	for _, milestone := range cfg.Milestones {
		if milestone.ID == id {
			return milestone, true
		}
	}
	return config.Milestone{}, false
}

// currentPlanExecutionIdentity reloads the selected Briefing relationship from
// the typed Plan and compact Milestone index before a launch or reconciliation.
func currentPlanExecutionIdentity(ctx planningCommandContext, plan config.Plan, exec *config.PlanExecution) (config.Briefing, config.Milestone, error) {
	if exec == nil || exec.CurrentBriefingID == "" || exec.CurrentMilestoneID == "" {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("Plan execution has incomplete current Briefing/Milestone identity")
	}
	if plan.Status != "completed" {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("Plan %q is no longer approved: status is %q", plan.ID, plan.Status)
	}
	briefing, ok := findBriefing(plan, exec.CurrentBriefingID)
	if !ok {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q no longer exists", exec.CurrentBriefingID)
	}
	if briefing.Status != "active" {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q is no longer executable: status is %q", briefing.ID, briefing.Status)
	}
	if blockedBy := incompleteBriefingDependencies(plan, briefing); len(blockedBy) > 0 {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q now has incomplete dependencies: %s", briefing.ID, strings.Join(blockedBy, ", "))
	}
	if briefing.MilestoneID != exec.CurrentMilestoneID {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q now links Milestone %q instead of retained Milestone %q", briefing.ID, briefing.MilestoneID, exec.CurrentMilestoneID)
	}
	milestone, ok := milestoneByID(ctx, exec.CurrentMilestoneID)
	if !ok {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("linked Milestone %q is missing", exec.CurrentMilestoneID)
	}
	return briefing, milestone, nil
}

func selectNextPlanBriefing(plan config.Plan) (config.Briefing, int, int, string) {
	byID := make(map[string]config.Briefing, len(plan.Briefings))
	for _, briefing := range plan.Briefings {
		byID[briefing.ID] = briefing
	}
	total, position := 0, 0
	for _, id := range plan.BriefingOrder {
		if byID[id].Status != "archived" {
			total++
		}
	}
	var firstBlocked config.Briefing
	firstBlockedPosition := 0
	for _, id := range plan.BriefingOrder {
		briefing := byID[id]
		if briefing.Status == "archived" {
			continue
		}
		position++
		if briefing.Status == "completed" {
			continue
		}
		ready := true
		for _, dependencyID := range briefing.DependsOn {
			if byID[dependencyID].Status != "completed" {
				ready = false
				break
			}
		}
		if ready {
			return briefing, position, total, "ready"
		}
		if firstBlocked.ID == "" {
			firstBlocked, firstBlockedPosition = briefing, position
		}
	}
	if firstBlocked.ID != "" {
		return firstBlocked, firstBlockedPosition, total, "blocked"
	}
	return config.Briefing{}, 0, total, "exhausted"
}

func planBriefingOrigin(plan config.Plan, exec *config.PlanExecution, briefingID, milestoneID string) tui.BriefingOrigin {
	_, position, total, _ := selectSpecificPlanBriefing(plan, briefingID)
	mode := ""
	if exec != nil {
		mode = exec.Mode
	}
	return tui.BriefingOrigin{PlanID: plan.ID, BriefingID: briefingID, MilestoneID: milestoneID, Mode: mode, QueuePosition: position, QueueTotal: total, DependencyState: "ready", PlanRun: true}
}

func selectSpecificPlanBriefing(plan config.Plan, target string) (config.Briefing, int, int, bool) {
	byID := make(map[string]config.Briefing, len(plan.Briefings))
	for _, b := range plan.Briefings {
		byID[b.ID] = b
	}
	total := 0
	for _, id := range plan.BriefingOrder {
		if byID[id].Status != "archived" {
			total++
		}
	}
	position := 0
	for _, id := range plan.BriefingOrder {
		if byID[id].Status == "archived" {
			continue
		}
		position++
		if id == target {
			return byID[id], position, total, true
		}
	}
	return config.Briefing{}, 0, total, false
}

func briefingIndex(plan config.Plan, id string) int {
	for i, briefing := range plan.Briefings {
		if briefing.ID == id {
			return i
		}
	}
	return -1
}

func normalizeRuntimeStatus(status string) string { return strings.ToLower(strings.TrimSpace(status)) }

func planExecutionTimestamp(createdAt ...string) string {
	now := time.Now().UTC()
	for _, value := range createdAt {
		if created, err := time.Parse(time.RFC3339, value); err == nil && now.Before(created) {
			now = created
		}
	}
	return now.Format(time.RFC3339)
}

func planExecutionSummary(plan config.Plan, exec *config.PlanExecution) string {
	if exec == nil {
		return fmt.Sprintf("Plan %q execution: unavailable", plan.ID)
	}
	message := fmt.Sprintf("Plan %q execution: %s (mode %s, checkpoint %s)", plan.ID, exec.State, exec.Mode, exec.Checkpoint)
	if exec.CurrentBriefingID != "" {
		message += fmt.Sprintf("; current Briefing %q", exec.CurrentBriefingID)
	}
	if exec.CurrentMilestoneID != "" {
		message += fmt.Sprintf("; Milestone %q", exec.CurrentMilestoneID)
	}
	if exec.PendingApproval != "" {
		message += "; resume with --approve after review"
	}
	if exec.StopReason != "" {
		message += "; " + exec.StopReason
	}
	return message
}
