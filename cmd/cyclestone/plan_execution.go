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
		if err := initializePlanExecution(opts.configPath, req.planID, req.mode); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	} else if err := validatePlanResume(opts.configPath, req.planID, req.mode); err != nil {
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

func initializePlanExecution(configPath, planID, mode string) error {
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
	if plan.Execution != nil && (plan.Execution.State == "running" || plan.Execution.State == "paused" || plan.Execution.State == "stopped" || plan.Execution.State == "blocked") {
		return fmt.Errorf("Plan %q already has resumable execution state; use plan resume", planID)
	}
	plan.Execution = &config.PlanExecution{Mode: mode, State: "running", Checkpoint: "queue-selection", UpdatedAt: planExecutionTimestamp(plan.CreatedAt)}
	plan.UpdatedAt = plan.Execution.UpdatedAt
	plan.UpdatedBy = "plan-executor"
	if !savePlanForCommand(ctx, plan, io.Discard) {
		return fmt.Errorf("failed to persist Plan execution start")
	}
	return nil
}

func validatePlanResume(configPath, planID, explicitMode string) error {
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
	if plan.Execution == nil {
		return fmt.Errorf("Plan %q has not been started; use plan start", planID)
	}
	if explicitMode != "" && explicitMode != plan.Execution.Mode {
		plan.Execution.Mode = explicitMode
		plan.Execution.UpdatedAt = planExecutionTimestamp(plan.CreatedAt)
		if !savePlanForCommand(ctx, plan, io.Discard) {
			return fmt.Errorf("failed to persist Plan execution mode")
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
		if !ok || plan.Execution == nil {
			return planExecutionStep{}, fmt.Errorf("Plan %q execution state is unavailable", planID)
		}
		execution := plan.Execution
		if execution.Checkpoint == "approval-required" {
			if _, _, identityErr := currentPlanExecutionIdentity(ctx, plan); identityErr != nil {
				return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", identityErr.Error()+"; approval was not consumed and the Plan execution identity must be repaired")
			}
		}

		if execution.Checkpoint == "cycle-running" {
			_, _, identityErr := currentPlanExecutionIdentity(ctx, plan)
			if identityErr != nil {
				return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", identityErr.Error()+"; repair the Plan execution identity before resuming")
			}
			state, err := config.LoadState(statePath)
			if err != nil {
				return planExecutionStep{}, err
			}
			switch normalizeRuntimeStatus(state.GetMilestoneStatus(execution.CurrentMilestoneID)) {
			case "approved":
				if err := completeCurrentPlanBriefing(ctx, &plan, execution.CurrentMilestoneID); err != nil {
					return planExecutionStep{}, err
				}
				if execution.Mode == config.PlanExecutionModeOnce {
					return pausePlanAfterOne(ctx, plan)
				}
				continue
			case "failed", "blocked":
				return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", "linked Milestone is "+normalizeRuntimeStatus(state.GetMilestoneStatus(execution.CurrentMilestoneID)))
			default:
				return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", "cycle launch was recorded but no terminal Milestone result exists; inspect the cycle and resume after its state is reconciled")
			}
		}

		if execution.CurrentBriefingID != "" && execution.Checkpoint == "cycle-pending" {
			_, milestone, identityErr := currentPlanExecutionIdentity(ctx, plan)
			if identityErr != nil {
				return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", identityErr.Error()+"; repair the Plan execution identity before resuming")
			}
			return planExecutionStep{milestone: &milestone, origin: planBriefingOrigin(plan, execution.CurrentBriefingID, milestone.ID), message: planExecutionSummary(plan)}, nil
		}

		briefing, position, total, selection := selectNextPlanBriefing(plan)
		if selection == "exhausted" {
			execution.State, execution.Checkpoint, execution.StopReason = "completed", "exhausted", "all non-archived Briefings are completed"
			execution.CurrentBriefingID, execution.CurrentMilestoneID, execution.PendingApproval = "", "", ""
			if err := savePlanExecution(ctx, plan); err != nil {
				return planExecutionStep{}, err
			}
			return planExecutionStep{message: planExecutionSummary(plan)}, nil
		}
		if selection == "blocked" {
			execution.State, execution.Checkpoint, execution.StopReason = "blocked", "dependency-deadlock", "incomplete Briefings remain but none have completed dependencies"
			execution.CurrentBriefingID, execution.CurrentMilestoneID, execution.PendingApproval = briefing.ID, briefing.MilestoneID, ""
			if err := savePlanExecution(ctx, plan); err != nil {
				return planExecutionStep{}, err
			}
			return planExecutionStep{message: planExecutionSummary(plan)}, nil
		}

		execution.State, execution.Checkpoint, execution.StopReason = "running", "briefing-selected", ""
		execution.CurrentBriefingID, execution.CurrentMilestoneID = briefing.ID, briefing.MilestoneID
		if err := savePlanExecution(ctx, plan); err != nil {
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
				return stopPlanExecution(ctx, plan, "stopped", fmt.Sprintf("Briefing %q links missing Milestone %q; repair it explicitly, then resume", briefing.ID, briefing.MilestoneID))
			}
		} else {
			result, err := prepareBriefingMilestone(ctx, configPath, briefingMilestoneRequest{planID: plan.ID, briefingID: briefing.ID, actor: "plan-executor", allowActive: true, allowLinked: true})
			if err != nil {
				fresh, _, loadErr := loadPlanningForExecution(configPath, planID)
				if loadErr == nil {
					_, _ = stopPlanExecution(fresh, planFromContext(fresh, planID), "stopped", err.Error()+"; repair the durable generation/link boundary and resume")
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

		state, err := config.LoadState(statePath)
		if err != nil {
			return planExecutionStep{}, err
		}
		switch normalizeRuntimeStatus(state.GetMilestoneStatus(milestone.ID)) {
		case "approved":
			if err := completeCurrentPlanBriefing(ctx, &plan, milestone.ID); err != nil {
				return planExecutionStep{}, err
			}
			if execution.Mode == config.PlanExecutionModeOnce {
				return pausePlanAfterOne(ctx, plan)
			}
			continue
		case "failed", "blocked":
			return stopPlanExecution(ctx, plan, "stopped", "linked Milestone is "+normalizeRuntimeStatus(state.GetMilestoneStatus(milestone.ID)))
		}

		execution = plan.Execution
		execution.CurrentMilestoneID = milestone.ID
		execution.Checkpoint = "milestone-linked"
		gate := "before-cycle:" + briefing.ID + ":" + milestone.ID
		if execution.Mode == config.PlanExecutionModeReview && (!approval || execution.PendingApproval != gate) {
			execution.State, execution.Checkpoint, execution.PendingApproval = "paused", "approval-required", gate
			execution.StopReason = "explicit approval is required before launching the Milestone cycle"
			if err := savePlanExecution(ctx, plan); err != nil {
				return planExecutionStep{}, err
			}
			return planExecutionStep{message: planExecutionSummary(plan)}, nil
		}
		execution.State, execution.Checkpoint, execution.PendingApproval, execution.StopReason = "running", "cycle-pending", "", ""
		if err := savePlanExecution(ctx, plan); err != nil {
			return planExecutionStep{}, err
		}
		origin := planBriefingOrigin(plan, briefing.ID, milestone.ID)
		origin.QueuePosition, origin.QueueTotal = position, total
		return planExecutionStep{milestone: &milestone, origin: origin, message: planExecutionSummary(plan)}, nil
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
	root.PlanCycleStarted = func(origin tui.BriefingOrigin) error { return markPlanCycleStarted(opts.configPath, origin) }
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

func markPlanCycleStarted(configPath string, origin tui.BriefingOrigin) error {
	ctx, plan, err := loadPlanningForExecution(configPath, origin.PlanID)
	if err != nil {
		return err
	}
	execution := plan.Execution
	if execution == nil || execution.Checkpoint != "cycle-pending" || execution.CurrentBriefingID != origin.BriefingID || execution.CurrentMilestoneID != origin.MilestoneID {
		return fmt.Errorf("Plan execution identity or checkpoint changed; resume to reconcile")
	}
	if _, _, identityErr := currentPlanExecutionIdentity(ctx, plan); identityErr != nil {
		_, stopErr := stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", identityErr.Error()+"; Milestone cycle was not launched")
		if stopErr != nil {
			return fmt.Errorf("%v (also failed to persist the stop: %w)", identityErr, stopErr)
		}
		return fmt.Errorf("%v; Milestone cycle was not launched", identityErr)
	}
	execution.Checkpoint = "cycle-running"
	return savePlanExecution(ctx, plan)
}

func finishPlanCycle(configPath, statePath string, origin tui.BriefingOrigin, terminalMilestoneID, status string, cycleErr error) (planExecutionStep, error) {
	ctx, plan, err := loadPlanningForExecution(configPath, origin.PlanID)
	if err != nil {
		return planExecutionStep{}, err
	}
	execution := plan.Execution
	if execution == nil {
		return planExecutionStep{}, fmt.Errorf("Plan execution state disappeared while Milestone %q ran; planning was not advanced", origin.MilestoneID)
	}
	if terminalMilestoneID != origin.MilestoneID {
		return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", fmt.Sprintf("ignored stale terminal event for Milestone %q while Plan execution expected %q; inspect the active cycle before resuming", terminalMilestoneID, origin.MilestoneID))
	}
	if execution.CurrentBriefingID != origin.BriefingID || execution.CurrentMilestoneID != origin.MilestoneID || execution.Checkpoint != "cycle-running" {
		return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", fmt.Sprintf("Plan execution identity changed while Milestone %q ran; planning was not advanced", origin.MilestoneID))
	}
	if _, _, identityErr := currentPlanExecutionIdentity(ctx, plan); identityErr != nil {
		return stopPlanExecutionPreservingCheckpoint(ctx, plan, "stopped", identityErr.Error()+"; planning was not advanced")
	}
	runtimeState, stateErr := config.LoadState(statePath)
	if stateErr != nil {
		return planExecutionStep{}, fmt.Errorf("reload Milestone state after cycle: %w", stateErr)
	}
	persistedStatus := normalizeRuntimeStatus(runtimeState.GetMilestoneStatus(origin.MilestoneID))
	if cycleErr != nil || normalizeRuntimeStatus(status) != "approved" || persistedStatus != "approved" {
		reason := "Milestone cycle did not finish approved"
		if cycleErr != nil {
			reason += ": " + cycleErr.Error()
		} else if persistedStatus != normalizeRuntimeStatus(status) {
			reason += fmt.Sprintf(": terminal message was %q but persisted state is %q", status, persistedStatus)
		}
		return stopPlanExecution(ctx, plan, "stopped", reason)
	}
	if err := completeCurrentPlanBriefing(ctx, &plan, origin.MilestoneID); err != nil {
		return planExecutionStep{}, err
	}
	if execution.Mode == config.PlanExecutionModeOnce {
		return pausePlanAfterOne(ctx, plan)
	}
	return advancePlanExecution(configPath, statePath, origin.PlanID, false)
}

func completeCurrentPlanBriefing(ctx planningCommandContext, plan *config.Plan, milestoneID string) error {
	execution := plan.Execution
	briefing, ok := findBriefing(*plan, execution.CurrentBriefingID)
	index := briefingIndex(*plan, execution.CurrentBriefingID)
	if !ok || briefing.MilestoneID != milestoneID {
		return fmt.Errorf("current Briefing/Milestone link changed; planning was not advanced")
	}
	now := planExecutionTimestamp(plan.CreatedAt, briefing.CreatedAt)
	plan.Briefings[index].Status, plan.Briefings[index].UpdatedAt, plan.Briefings[index].UpdatedBy = "completed", now, "plan-executor"
	execution.CurrentBriefingID, execution.CurrentMilestoneID, execution.PendingApproval = "", "", ""
	execution.State, execution.Checkpoint, execution.StopReason = "running", "briefing-completed", ""
	return savePlanExecution(ctx, *plan)
}

func pausePlanAfterOne(ctx planningCommandContext, plan config.Plan) (planExecutionStep, error) {
	plan.Execution.State, plan.Execution.Checkpoint, plan.Execution.StopReason = "paused", "one-complete", "execution mode stops after one eligible Briefing"
	if err := savePlanExecution(ctx, plan); err != nil {
		return planExecutionStep{}, err
	}
	return planExecutionStep{message: planExecutionSummary(plan)}, nil
}

func stopPlanExecution(ctx planningCommandContext, plan config.Plan, state, reason string) (planExecutionStep, error) {
	plan.Execution.State, plan.Execution.StopReason = state, reason
	if state == "stopped" {
		plan.Execution.Checkpoint = "stopped"
	}
	if err := savePlanExecution(ctx, plan); err != nil {
		return planExecutionStep{}, err
	}
	return planExecutionStep{message: planExecutionSummary(plan)}, nil
}

// stopPlanExecutionPreservingCheckpoint records an actionable stop without
// erasing a durable launch boundary. In particular, cycle-running must remain
// non-launchable across repeated resumes until Milestone state is terminal.
func stopPlanExecutionPreservingCheckpoint(ctx planningCommandContext, plan config.Plan, state, reason string) (planExecutionStep, error) {
	plan.Execution.State, plan.Execution.StopReason = state, reason
	if err := savePlanExecution(ctx, plan); err != nil {
		return planExecutionStep{}, err
	}
	return planExecutionStep{message: planExecutionSummary(plan)}, nil
}

func savePlanExecution(ctx planningCommandContext, plan config.Plan) error {
	now := planExecutionTimestamp(plan.CreatedAt)
	plan.Execution.UpdatedAt, plan.UpdatedAt, plan.UpdatedBy = now, now, "plan-executor"
	if !savePlanForCommand(ctx, plan, io.Discard) {
		return fmt.Errorf("failed to persist Plan execution checkpoint")
	}
	return nil
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
func currentPlanExecutionIdentity(ctx planningCommandContext, plan config.Plan) (config.Briefing, config.Milestone, error) {
	execution := plan.Execution
	if execution == nil || execution.CurrentBriefingID == "" || execution.CurrentMilestoneID == "" {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("Plan execution has incomplete current Briefing/Milestone identity")
	}
	if plan.Status != "completed" {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("Plan %q is no longer approved: status is %q", plan.ID, plan.Status)
	}
	briefing, ok := findBriefing(plan, execution.CurrentBriefingID)
	if !ok {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q no longer exists", execution.CurrentBriefingID)
	}
	if briefing.Status != "active" {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q is no longer executable: status is %q", briefing.ID, briefing.Status)
	}
	if blockedBy := incompleteBriefingDependencies(plan, briefing); len(blockedBy) > 0 {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q now has incomplete dependencies: %s", briefing.ID, strings.Join(blockedBy, ", "))
	}
	if briefing.MilestoneID != execution.CurrentMilestoneID {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("current Briefing %q now links Milestone %q instead of retained Milestone %q", briefing.ID, briefing.MilestoneID, execution.CurrentMilestoneID)
	}
	milestone, ok := milestoneByID(ctx, execution.CurrentMilestoneID)
	if !ok {
		return config.Briefing{}, config.Milestone{}, fmt.Errorf("linked Milestone %q is missing", execution.CurrentMilestoneID)
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

func planBriefingOrigin(plan config.Plan, briefingID, milestoneID string) tui.BriefingOrigin {
	_, position, total, _ := selectSpecificPlanBriefing(plan, briefingID)
	return tui.BriefingOrigin{PlanID: plan.ID, BriefingID: briefingID, MilestoneID: milestoneID, Mode: plan.Execution.Mode, QueuePosition: position, QueueTotal: total, DependencyState: "ready", PlanRun: true}
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

func planExecutionSummary(plan config.Plan) string {
	e := plan.Execution
	message := fmt.Sprintf("Plan %q execution: %s (mode %s, checkpoint %s)", plan.ID, e.State, e.Mode, e.Checkpoint)
	if e.CurrentBriefingID != "" {
		message += fmt.Sprintf("; current Briefing %q", e.CurrentBriefingID)
	}
	if e.CurrentMilestoneID != "" {
		message += fmt.Sprintf("; Milestone %q", e.CurrentMilestoneID)
	}
	if e.PendingApproval != "" {
		message += "; resume with --approve after review"
	}
	if e.StopReason != "" {
		message += "; " + e.StopReason
	}
	return message
}
