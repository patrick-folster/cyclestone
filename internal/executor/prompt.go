package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/git"
	"github.com/patrick-folster/cyclestone/resources"
)

func assembleInput(milestone config.Milestone, agent config.Agent, cycleNum int, opts RunOptions, previousReportPath, gitContextPath string) string {
	settings := config.LoadMergedSettings()
	return assembleInputWithSettings(milestone, agent, cycleNum, opts, previousReportPath, gitContextPath, settings, nil)
}

func assembleInputWithSettings(milestone config.Milestone, agent config.Agent, cycleNum int, opts RunOptions, previousReportPath, gitContextPath string, settings config.Settings, pipeline []config.Agent) string {
	if settings.EnableCompactPhaseHandoffs != nil && *settings.EnableCompactPhaseHandoffs {
		return assemblePhaseInput(milestone, agent, cycleNum, opts, previousReportPath, gitContextPath, settings, pipeline)
	}

	if agent.ID == "recommender" {
		cyclePadded := fmt.Sprintf("%03d", cycleNum)
		reportsDir := filepath.Join(".cyclestone", "reports")
		reportPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.md", milestone.ID, cyclePadded))

		latestCycleReportText := summarizeCycleReport(reportPath)

		var criteriaBuilder strings.Builder
		for _, criterion := range milestone.AcceptanceCriteria {
			criteriaBuilder.WriteString("- " + criterion + "\n")
		}

		absRoot, err := filepath.Abs(".")
		if err != nil {
			absRoot = "."
		}
		promptText := fmt.Sprintf("Repository root: %s\n\n%s", absRoot, agent.PromptBody)
		promptText = strings.ReplaceAll(promptText, "{{MILESTONE_ID}}", milestone.ID)
		promptText = strings.ReplaceAll(promptText, "{{GOAL}}", milestone.Goal)
		promptText = strings.ReplaceAll(promptText, "{{ACCEPTANCE_CRITERIA}}", criteriaBuilder.String())
		promptText = strings.ReplaceAll(promptText, "{{LATEST_CYCLE_REPORT}}", latestCycleReportText)

		var sb strings.Builder
		sb.WriteString(promptText)
		sb.WriteString("\n\n")
		appendAIContextToBuilder(&sb)
		appendDecisionsLogToBuilder(&sb)
		return sb.String()
	}

	var sb strings.Builder
	cycleMode := "initial"
	if cycleNum > 1 {
		cycleMode = "continuation"
	}
	cyclePadded := fmt.Sprintf("%03d", cycleNum)

	absRoot, err := filepath.Abs(".")
	if err != nil {
		absRoot = "."
	}

	sb.WriteString(fmt.Sprintf("# %s Phase Input\n\n", agent.Name))
	sb.WriteString(fmt.Sprintf("Repository root: %s\n\n", absRoot))
	sb.WriteString(fmt.Sprintf("Milestone ID: %s\n\n", milestone.ID))
	sb.WriteString(fmt.Sprintf("Cycle: %d\n\n", cycleNum))
	sb.WriteString(fmt.Sprintf("Cycle mode: %s\n\n", cycleMode))

	// Branch Policy
	sb.WriteString("## Branch Policy\n\n")
	prefix := settings.DefaultGitBranchPrefix
	if prefix == "" {
		prefix = "cyclestone/milestones/"
	}
	branchName := prefix + milestone.ID
	if opts.NoBranchChange {
		sb.WriteString("The milestone runner was started with --no-branch-change. Do not create, checkout, switch, or otherwise change git branches in the root repository, configured repositories, discovered submodules, discovered worktrees, nested subrepositories, or any other repository. Keep every repository on its current branch, even if other milestone instructions or prompts mention milestone branches. Report the current branches you observe instead of changing them.\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("The milestone runner may create or reuse the milestone branch %s in repositories prepared by this script. Repositories changed by this milestone should use %s-prefixed milestone branches unless a human explicitly overrides this policy.\n\n", branchName, prefix))
	}

	// Continuation Mode
	if cycleNum > 1 {
		sb.WriteString("## Continuation Mode\n\n")
		sb.WriteString(fmt.Sprintf("This is cycle %s for the same milestone. Do not restart the milestone from scratch. Use the previous cycle report, current milestone notes, and current git context as primary inputs. Focus on unresolved QA findings, incomplete acceptance criteria, changed-file verification, and any regressions introduced by prior cycles. Keep changes narrowly scoped to closing the remaining gaps.\n\n", cyclePadded))
		sb.WriteString("Phase focus:\n\n")
		sb.WriteString("- PM: refine scope only when the prior cycle exposed ambiguity or missing acceptance criteria.\n")
		sb.WriteString("- Developer: address the concrete open issues and preserve prior correct work.\n")
		sb.WriteString("- QA: verify the current delta plus any blockers carried forward from earlier cycles.\n\n")
	}

	appendFileContent := func(heading, path string) {
		if content, err := os.ReadFile(path); err == nil {
			sb.WriteString(fmt.Sprintf("## %s\n\n", heading))
			text := string(content)
			if strings.HasSuffix(path, "AI_CONTEXT.md") || strings.HasSuffix(path, "DECISIONS.md") {
				text = strings.ReplaceAll(text, "{{WORKSPACE_ROOT}}", absRoot)
			}
			sb.WriteString(limitTextMiddle(text, maxPromptFileChars, path))
			sb.WriteString("\n\n")
		}
	}
	appendPreviousCycleSummary := func(path string) {
		if summary := summarizeCycleReport(path); summary != "" {
			sb.WriteString("## Previous Cycle Summary\n\n")
			sb.WriteString(summary)
			sb.WriteString("\n\n")
		}
	}
	appendScopedMilestoneContext := func() {
		if contextText := buildScopedMilestoneContext(milestone, opts); contextText != "" {
			sb.WriteString("## Scoped Milestone Context\n\n")
			sb.WriteString(contextText)
			sb.WriteString("\n\n")
		}
	}

	appendFileContent("AI Context", ".cyclestone/AI_CONTEXT.md")
	if _, err := os.Stat(".cyclestone/AI_CONTEXT.md"); os.IsNotExist(err) {
		appendFileContent("AI Context", "AI_CONTEXT.md")
	}

	appendScopedMilestoneContext()

	appendFileContent("Decisions Log", ".cyclestone/DECISIONS.md")
	if _, err := os.Stat(".cyclestone/DECISIONS.md"); os.IsNotExist(err) {
		appendFileContent("Decisions Log", "DECISIONS.md")
	}

	appendFileContent("QA Checklist", ".cyclestone/QA_CHECKLIST.md")
	if _, err := os.Stat(".cyclestone/QA_CHECKLIST.md"); os.IsNotExist(err) {
		appendFileContent("QA Checklist", "QA_CHECKLIST.md")
	}

	activeMilestonePath := filepath.Join(".cyclestone", "milestones", milestone.ID+".md")
	appendFileContent("Active Milestone Specs", activeMilestonePath)
	if _, err := os.Stat(activeMilestonePath); os.IsNotExist(err) {
		appendFileContent("Active Milestone Specs", filepath.Join("_old", "milestones", milestone.ID+".md"))
	}

	if cycleNum > 1 && previousReportPath != "" {
		appendPreviousCycleSummary(previousReportPath)
	}

	if gitContextPath != "" {
		appendGitContextToBuilder(&sb, gitContextPath)
	}

	sb.WriteString("## Workspace Safety Rules\n\n")
	safetyRules := strings.ReplaceAll(resources.SafetyRules, "root (`.`)", fmt.Sprintf("root (`%s`)", absRoot))
	safetyRules = strings.ReplaceAll(safetyRules, "outside the root `.`", fmt.Sprintf("outside the root `%s`", absRoot))
	safetyRules = strings.ReplaceAll(safetyRules, "workspace root (`.`)", fmt.Sprintf("workspace root (`%s`)", absRoot))
	sb.WriteString(safetyRules)
	sb.WriteString("\n\n")

	sb.WriteString("## Role Prompt\n\n")
	sb.WriteString(agent.PromptBody)
	sb.WriteString("\n\n")

	res := sb.String()
	if strings.TrimSpace(opts.CycleNote) != "" && (agent.ID == "pm" || agent.ID == "developer" || agent.ID == "qa") {
		res = fmt.Sprintf("# Human Cycle Note\n\n%s\n\n---\n\n%s", strings.TrimSpace(opts.CycleNote), res)
	}
	return res
}

func assemblePhaseInput(milestone config.Milestone, agent config.Agent, cycleNum int, opts RunOptions, previousReportPath, gitContextPath string, settings config.Settings, pipeline []config.Agent) string {
	if agent.ID == "recommender" {
		return assembleCompactRecommenderInput(milestone, agent, cycleNum, pipeline)
	}

	var sb strings.Builder
	cycleMode := "initial"
	if cycleNum > 1 {
		cycleMode = "continuation"
	}
	cyclePadded := fmt.Sprintf("%03d", cycleNum)

	absRoot, err := filepath.Abs(".")
	if err != nil {
		absRoot = "."
	}

	sb.WriteString(fmt.Sprintf("# %s Phase Input\n\n", agent.Name))
	sb.WriteString(fmt.Sprintf("Repository root: %s\n\n", absRoot))
	sb.WriteString(fmt.Sprintf("Milestone ID: %s\n\n", milestone.ID))
	sb.WriteString(fmt.Sprintf("Cycle: %d\n\n", cycleNum))
	sb.WriteString(fmt.Sprintf("Cycle mode: %s\n\n", cycleMode))
	appendBranchPolicy(&sb, milestone.ID, opts)
	if cycleNum > 1 {
		appendContinuationGuidance(&sb, cyclePadded)
	}

	appendAIContextToBuilder(&sb)
	appendDecisionsLogToBuilder(&sb)

	switch agent.ID {
	case "pm":
		appendScopedMilestoneContextToBuilder(&sb, milestone, opts)
		appendMilestoneSpecToBuilder(&sb, milestone)
		if gitContextPath != "" {
			appendGitContextToBuilder(&sb, gitContextPath)
		}
		if cycleNum > 1 && previousReportPath != "" {
			appendPreviousCycleSummaryToBuilder(&sb, previousReportPath)
		}
	case "developer":
		appendHandoffToBuilder(&sb, "PM Handoff", milestone.ID, cyclePadded, "pm", settings.MaxHandoffChars, pipeline)
		appendMilestoneSpecToBuilder(&sb, milestone)
		appendCurrentGitStatusToBuilder(&sb)
	case "qa":
		appendHandoffToBuilder(&sb, "PM Handoff", milestone.ID, cyclePadded, "pm", settings.MaxHandoffChars, pipeline)
		appendHandoffToBuilder(&sb, "Developer Handoff", milestone.ID, cyclePadded, "developer", settings.MaxHandoffChars, pipeline)
		appendChangedFilesToBuilder(&sb)
		appendFileContentToBuilder(&sb, "QA Checklist", ".cyclestone/QA_CHECKLIST.md")
		if _, err := os.Stat(".cyclestone/QA_CHECKLIST.md"); os.IsNotExist(err) {
			appendFileContentToBuilder(&sb, "QA Checklist", "QA_CHECKLIST.md")
		}
	default:
		appendScopedMilestoneContextToBuilder(&sb, milestone, opts)
		appendMilestoneSpecToBuilder(&sb, milestone)
	}

	appendSafetyAndRolePrompt(&sb, absRoot, agent.PromptBody)
	res := sb.String()
	if strings.TrimSpace(opts.CycleNote) != "" && (agent.ID == "pm" || agent.ID == "developer" || agent.ID == "qa") {
		res = fmt.Sprintf("# Human Cycle Note\n\n%s\n\n---\n\n%s", strings.TrimSpace(opts.CycleNote), res)
	}
	return res
}

func assembleCompactRecommenderInput(milestone config.Milestone, agent config.Agent, cycleNum int, pipeline []config.Agent) string {
	cyclePadded := fmt.Sprintf("%03d", cycleNum)
	reportsDir := filepath.Join(".cyclestone", "reports")
	qaHandoff := readHandoffOrFallback(milestone.ID, cyclePadded, "qa", maxRecommenderReportOutputChars, pipeline)
	if qaHandoff == "" {
		reportPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.md", milestone.ID, cyclePadded))
		qaHandoff = summarizeCycleReport(reportPath)
	}

	var criteriaBuilder strings.Builder
	for _, criterion := range milestone.AcceptanceCriteria {
		criteriaBuilder.WriteString("- " + criterion + "\n")
	}

	absRoot, err := filepath.Abs(".")
	if err != nil {
		absRoot = "."
	}
	promptText := fmt.Sprintf("Repository root: %s\n\n%s", absRoot, agent.PromptBody)
	promptText = strings.ReplaceAll(promptText, "{{MILESTONE_ID}}", milestone.ID)
	promptText = strings.ReplaceAll(promptText, "{{GOAL}}", milestone.Goal)
	promptText = strings.ReplaceAll(promptText, "{{ACCEPTANCE_CRITERIA}}", criteriaBuilder.String())
	promptText = strings.ReplaceAll(promptText, "{{LATEST_CYCLE_REPORT}}", qaHandoff)

	var sb strings.Builder
	sb.WriteString(promptText)
	sb.WriteString("\n\n")
	appendAIContextToBuilder(&sb)
	appendDecisionsLogToBuilder(&sb)
	return sb.String()
}

func appendAIContextToBuilder(sb *strings.Builder) {
	appendFileContentToBuilder(sb, "AI Context", ".cyclestone/AI_CONTEXT.md")
	if _, err := os.Stat(".cyclestone/AI_CONTEXT.md"); os.IsNotExist(err) {
		appendFileContentToBuilder(sb, "AI Context", "AI_CONTEXT.md")
	}
}

func appendDecisionsLogToBuilder(sb *strings.Builder) {
	appendFileContentToBuilder(sb, "Decisions Log", ".cyclestone/DECISIONS.md")
	if _, err := os.Stat(".cyclestone/DECISIONS.md"); os.IsNotExist(err) {
		appendFileContentToBuilder(sb, "Decisions Log", "DECISIONS.md")
	}
}

func appendBranchPolicy(sb *strings.Builder, milestoneID string, opts RunOptions) {
	sb.WriteString("## Branch Policy\n\n")
	settings := config.LoadMergedSettings()
	prefix := settings.DefaultGitBranchPrefix
	if prefix == "" {
		prefix = "cyclestone/milestones/"
	}
	branchName := prefix + milestoneID
	if opts.NoBranchChange {
		sb.WriteString("The milestone runner was started with --no-branch-change. Do not create, checkout, switch, or otherwise change git branches in any repository. Report current branches instead of changing them.\n\n")
		return
	}
	sb.WriteString(fmt.Sprintf("The milestone runner may create or reuse the milestone branch %s. Changed repositories should use %s-prefixed milestone branches unless a human explicitly overrides this policy.\n\n", branchName, prefix))
}

func appendContinuationGuidance(sb *strings.Builder, cyclePadded string) {
	sb.WriteString("## Continuation Mode\n\n")
	sb.WriteString(fmt.Sprintf("This is cycle %s for the same milestone. Focus on unresolved QA findings, incomplete acceptance criteria, changed-file verification, and current repository state.\n\n", cyclePadded))
}

func appendFileContentToBuilder(sb *strings.Builder, heading, path string) {
	if content, err := os.ReadFile(path); err == nil {
		sb.WriteString(fmt.Sprintf("## %s\n\n", heading))
		text := string(content)
		if strings.HasSuffix(path, "AI_CONTEXT.md") || strings.HasSuffix(path, "DECISIONS.md") {
			absRoot, err := filepath.Abs(".")
			if err != nil {
				absRoot = "."
			}
			text = strings.ReplaceAll(text, "{{WORKSPACE_ROOT}}", absRoot)
		}
		sb.WriteString(limitTextMiddle(text, maxPromptFileChars, path))
		sb.WriteString("\n\n")
	}
}

func appendPreviousCycleSummaryToBuilder(sb *strings.Builder, path string) {
	if summary := summarizeCycleReport(path); summary != "" {
		sb.WriteString("## Previous Cycle Summary\n\n")
		sb.WriteString(summary)
		sb.WriteString("\n\n")
	}
}

func appendScopedMilestoneContextToBuilder(sb *strings.Builder, milestone config.Milestone, opts RunOptions) {
	if contextText := buildScopedMilestoneContext(milestone, opts); contextText != "" {
		sb.WriteString("## Scoped Milestone Context\n\n")
		sb.WriteString(contextText)
		sb.WriteString("\n\n")
	}
}

func appendMilestoneSpecToBuilder(sb *strings.Builder, milestone config.Milestone) {
	activeMilestonePath := filepath.Join(".cyclestone", "milestones", milestone.ID+".md")
	appendFileContentToBuilder(sb, "Active Milestone Specs", activeMilestonePath)
	if _, err := os.Stat(activeMilestonePath); os.IsNotExist(err) {
		appendFileContentToBuilder(sb, "Active Milestone Specs", filepath.Join("_old", "milestones", milestone.ID+".md"))
	}
}

func appendSafetyAndRolePrompt(sb *strings.Builder, absRoot, promptBody string) {
	sb.WriteString("## Workspace Safety Rules\n\n")
	safetyRules := strings.ReplaceAll(resources.SafetyRules, "root (`.`)", fmt.Sprintf("root (`%s`)", absRoot))
	safetyRules = strings.ReplaceAll(safetyRules, "outside the root `.`", fmt.Sprintf("outside the root `%s`", absRoot))
	safetyRules = strings.ReplaceAll(safetyRules, "workspace root (`.`)", fmt.Sprintf("workspace root (`%s`)", absRoot))
	sb.WriteString(safetyRules)
	sb.WriteString("\n\n")
	sb.WriteString("## Role Prompt\n\n")
	sb.WriteString(promptBody)
	sb.WriteString("\n\n")
}

func appendHandoffToBuilder(sb *strings.Builder, heading, milestoneID, cyclePadded, agentID string, maxChars int, pipeline []config.Agent) {
	text := readHandoffOrFallback(milestoneID, cyclePadded, agentID, maxChars, pipeline)
	if text == "" {
		return
	}
	sb.WriteString(fmt.Sprintf("## %s\n\n", heading))
	sb.WriteString(text)
	sb.WriteString("\n\n")
}

func appendCurrentGitStatusToBuilder(sb *strings.Builder) {
	sb.WriteString("## Current Git Status\n\n```text\n")
	for _, repo := range git.GetTrackedRepos() {
		sb.WriteString("### " + repo.Label + "\n")
		if out, err := exec.Command("git", "-C", repo.Path, "status", "--short").Output(); err == nil {
			sb.Write(out)
		}
	}
	sb.WriteString("```\n\n")
}

func appendChangedFilesToBuilder(sb *strings.Builder) {
	sb.WriteString("## Changed Files\n\n```text\n")
	for _, repo := range git.GetTrackedRepos() {
		sb.WriteString("### " + repo.Label + "\n")
		if out, err := exec.Command("git", "-C", repo.Path, "diff", "--name-status").Output(); err == nil {
			sb.Write(out)
		}
	}
	sb.WriteString("```\n\n")
}
