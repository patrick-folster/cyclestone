package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		reportsDir := filepath.Join(".cyclestone", "reports")
		if _, err := os.Stat(filepath.Join(".cyclestone", "reports", "milestones", milestone.ID)); err == nil {
			reportsDir = filepath.Join(".cyclestone", "reports", "milestones")
		}
		reportPath := cycleArtifacts(reportsDir, milestone.ID, cycleNum).Report
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
		appendAgentInstructionsToBuilder(&sb, settings)
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
			if strings.HasSuffix(path, "AGENTS.md") || strings.HasSuffix(path, "DECISIONS.md") {
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

	appendAgentInstructionsToBuilder(&sb, settings)

	appendScopedMilestoneContext()

	appendFileContent("Decisions Log", ".cyclestone/DECISIONS.md")
	if _, err := os.Stat(".cyclestone/DECISIONS.md"); os.IsNotExist(err) {
		appendFileContent("Decisions Log", "DECISIONS.md")
	}

	appendFileContent("QA Checklist", ".cyclestone/QA_CHECKLIST.md")
	if _, err := os.Stat(".cyclestone/QA_CHECKLIST.md"); os.IsNotExist(err) {
		appendFileContent("QA Checklist", "QA_CHECKLIST.md")
	}

	appendMilestoneSpecToBuilder(&sb, milestone)

	if cycleNum > 1 && previousReportPath != "" {
		appendPreviousCycleSummary(previousReportPath)
	}

	if gitContextPath != "" {
		appendGitContextToBuilder(&sb, gitContextPath)
		if agent.ID == "pm" || agent.ID == "developer" || agent.ID == "qa" {
			appendEmbeddedRepoInformationalWarningsToBuilder(&sb, git.GetTrackedRepos())
		}
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
		return assembleCompactRecommenderInput(milestone, agent, cycleNum, settings, pipeline)
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

	appendAgentInstructionsToBuilder(&sb, settings)
	appendDecisionsLogToBuilder(&sb)

	switch agent.ID {
	case "pm":
		appendScopedMilestoneContextToBuilder(&sb, milestone, opts)
		appendMilestoneSpecToBuilder(&sb, milestone)
		if gitContextPath != "" {
			appendGitContextToBuilder(&sb, gitContextPath)
			appendEmbeddedRepoInformationalWarningsToBuilder(&sb, git.GetTrackedRepos())
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

func assembleCompactRecommenderInput(milestone config.Milestone, agent config.Agent, cycleNum int, settings config.Settings, pipeline []config.Agent) string {
	cyclePadded := fmt.Sprintf("%03d", cycleNum)
	reportsDir := filepath.Join(".cyclestone", "reports")
	if _, err := os.Stat(filepath.Join(".cyclestone", "reports", "milestones", milestone.ID)); err == nil {
		reportsDir = filepath.Join(".cyclestone", "reports", "milestones")
	}
	qaHandoff := readHandoffOrFallback(milestone.ID, cyclePadded, "qa", maxRecommenderReportOutputChars, pipeline)
	if qaHandoff == "" {
		reportPath := cycleArtifacts(reportsDir, milestone.ID, cycleNum).Report
		qaHandoff = summarizeCycleReport(reportPath)
	}
	qaHandoff = stripEmbeddedRepoInformationalWarningContext(qaHandoff)

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

	return appendInstructionContextToPromptText(promptText, settings)
}

func appendInstructionContextToPromptText(promptText string, settings config.Settings) string {
	var sb strings.Builder
	sb.WriteString(promptText)
	sb.WriteString("\n\n")
	appendAgentInstructionsToBuilder(&sb, settings)
	appendDecisionsLogToBuilder(&sb)
	return sb.String()
}

func appendAgentInstructionsToBuilder(sb *strings.Builder, settings config.Settings) {
	instructionPath := strings.TrimSpace(settings.AgentInstructions.File)
	if instructionPath == "" {
		instructionPath = "AGENTS.md"
	}
	if filepath.IsAbs(instructionPath) || strings.Contains(instructionPath, "..") {
		return
	}
	appendFileContentToBuilder(sb, "Agent Instructions", instructionPath)
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
		if strings.HasSuffix(path, "AGENTS.md") || strings.HasSuffix(path, "DECISIONS.md") {
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
	// Try folder-per-item layout first: .cyclestone/milestones/<id>-<slug>/<id>.md
	milestonesDir := filepath.Join(".cyclestone", "milestones")
	if entries, err := os.ReadDir(milestonesDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), milestone.ID) {
				continue
			}
			candidate := filepath.Join(milestonesDir, entry.Name(), milestone.ID+".md")
			if _, err := os.Stat(candidate); err == nil {
				appendFileContentToBuilder(sb, "Active Milestone Specs", candidate)
				return
			}
		}
	}
	// Legacy flat .md fallback
	activeMilestonePath := filepath.Join(".cyclestone", "milestones", milestone.ID+".md")
	if _, err := os.Stat(activeMilestonePath); err == nil {
		appendFileContentToBuilder(sb, "Active Milestone Specs", activeMilestonePath)
		return
	}
	// _old fallback
	appendFileContentToBuilder(sb, "Active Milestone Specs", filepath.Join("_old", "milestones", milestone.ID+".md"))
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
	repos := git.GetTrackedRepos()
	for _, repo := range repos {
		sb.WriteString("### " + repo.Label + "\n")
		if out, err := exec.Command("git", "-C", repo.Path, "status", "--short").Output(); err == nil {
			sb.Write(out)
		}
	}
	sb.WriteString("```\n\n")
	appendEmbeddedRepoInformationalWarningsToBuilder(sb, repos)
}

func appendChangedFilesToBuilder(sb *strings.Builder) {
	sb.WriteString("## Changed Files\n\n```text\n")
	repos := git.GetTrackedRepos()
	for _, repo := range repos {
		sb.WriteString("### " + repo.Label + "\n")
		if out, err := exec.Command("git", "-C", repo.Path, "diff", "--name-status").Output(); err == nil {
			sb.Write(out)
		}
	}
	sb.WriteString("```\n\n")
	appendEmbeddedRepoInformationalWarningsToBuilder(sb, repos)
}

func appendEmbeddedRepoInformationalWarningsToBuilder(sb *strings.Builder, repos []git.RepoInfo) {
	warnings := embeddedRepoInformationalWarnings(git.DiscoverUntrackedEmbeddedRepos(repos))
	if len(warnings) == 0 {
		return
	}
	sb.WriteString("## Informational Warnings\n\n")
	for _, warning := range warnings {
		sb.WriteString("- " + warning + "\n")
	}
	sb.WriteString("\nThese warnings are for human awareness only. Do not treat them as acceptance gaps, required fixes, failing checks, or cycle-continuation score drivers unless the milestone explicitly targets repository topology.\n\n")
}

func stripEmbeddedRepoInformationalWarningContext(text string) string {
	if text == "" {
		return text
	}
	type block struct {
		heading string
		lines   []string
	}
	var blocks []block
	current := block{}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			blocks = append(blocks, current)
			current = block{heading: strings.TrimLeft(trimmed, "# ")}
		}
		current.lines = append(current.lines, line)
	}
	blocks = append(blocks, current)

	var filtered []string
	for _, block := range blocks {
		lines := block.lines
		if embeddedRepoInformationalOnlyBlock(block.heading, lines) {
			lines = stripEmbeddedRepoInformationalOnlyBlockLines(lines)
		} else {
			lines = stripEmbeddedRepoInformationalWarningLines(lines)
		}
		filtered = append(filtered, lines...)
	}
	return strings.Join(filtered, "\n")
}

func embeddedRepoInformationalOnlyBlock(section string, lines []string) bool {
	hasEmbeddedRepoWarning := false
	for _, line := range lines {
		if isEmbeddedRepoInformationalWarningLine(line) {
			hasEmbeddedRepoWarning = true
			break
		}
	}
	if !hasEmbeddedRepoWarning {
		return false
	}
	actionableField := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if field, ok := embeddedRepoWarningContextField(trimmed); ok {
			actionableField = field
			continue
		}
		if embeddedRepoWarningNeutralField(trimmed) {
			actionableField = ""
			continue
		}
		if trimmed == "" ||
			isEmbeddedRepoInformationalWarningLine(trimmed) ||
			isEmbeddedRepoInformationalWarningSchemaLine(trimmed) ||
			strings.HasPrefix(trimmed, "## ") ||
			strings.HasPrefix(trimmed, "### ") {
			continue
		}
		if actionableField != "" {
			return false
		}
		if isContinuationSignalLine(trimmed, section) {
			return false
		}
	}
	return true
}

func embeddedRepoWarningContextField(line string) (string, bool) {
	key := strings.TrimSpace(line)
	if strings.HasPrefix(key, "- ") {
		return "", false
	}
	key = strings.TrimSuffix(key, "[]")
	key = strings.TrimSpace(key)
	key = strings.TrimSuffix(key, ":")
	switch key {
	case "failing_checks", "required_fixes", "criteria_results", "next_cycle_focus":
		return key, true
	default:
		return "", false
	}
}

func embeddedRepoWarningNeutralField(line string) bool {
	key := strings.TrimSpace(line)
	if strings.HasPrefix(key, "- ") {
		return false
	}
	key = strings.TrimSuffix(key, "[]")
	key = strings.TrimSpace(key)
	key = strings.TrimSuffix(key, ":")
	switch key {
	case "reviewed_files", "verdict", "summary", "milestone_id", "cycle", "agent_id", "output_contract", "validation_status", "source_log":
		return true
	default:
		return false
	}
}

func stripEmbeddedRepoInformationalOnlyBlockLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isEmbeddedRepoInformationalWarningLine(trimmed) || isEmbeddedRepoInformationalWarningSchemaLine(trimmed) {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

func stripEmbeddedRepoInformationalWarningLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if isEmbeddedRepoInformationalWarningLine(strings.TrimSpace(line)) {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

func isEmbeddedRepoInformationalWarningLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "embedded git repository detected at") ||
		strings.Contains(lower, "untracked embedded git repositories") ||
		strings.Contains(lower, "do not treat them as acceptance gaps") ||
		strings.Contains(lower, "excluded from recommender scoring; add it to repositories or .gitmodules")
}

func isEmbeddedRepoInformationalWarningSchemaLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return lower == "verdict: blocked" ||
		lower == "verdict: failed" ||
		lower == "verdict: fail" ||
		lower == "verdict: needs-human-review" ||
		lower == "verdict: needs_human_review" ||
		lower == "failing_checks:" ||
		lower == "required_fixes:" ||
		lower == "criteria_results:" ||
		lower == "next_cycle_focus:" ||
		lower == "- []"
}

func assembleAgentInstructionsUpdateInput(milestone config.Milestone, milestoneScoped bool, opts RunOptions) string {
	settings := config.LoadMergedSettings()
	absRoot, err := filepath.Abs(".")
	if err != nil {
		absRoot = "."
	}
	var sb strings.Builder
	sb.WriteString("# AGENTS.md Update Proposal Input\n\n")
	sb.WriteString(fmt.Sprintf("Repository root: %s\n\n", absRoot))
	if milestoneScoped {
		sb.WriteString(fmt.Sprintf("Scope: milestone-scoped (%s)\n\n", milestone.ID))
	} else {
		sb.WriteString("Scope: repository-wide\n\n")
	}
	appendBranchPolicy(&sb, milestone.ID, opts)
	if strings.TrimSpace(opts.CycleNote) != "" {
		sb.WriteString("## Human Message\n\n")
		sb.WriteString(strings.TrimSpace(opts.CycleNote))
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Proposal Rules\n\n")
	sb.WriteString("- Produce a complete proposed replacement for root `AGENTS.md` by editing that file only; Cyclestone will restore the current file and save your proposal for human review.\n")
	sb.WriteString("- Do not edit source files, milestone specs, reports, state, or `.cyclestone/DECISIONS.md`.\n")
	sb.WriteString("- Keep `.cyclestone/DECISIONS.md` as chronological history; do not merge the decision log wholesale into `AGENTS.md`.\n")
	sb.WriteString("- Include only durable repository operating guidance. Do not copy transient milestone notes, raw logs, temporary paths, branch names, or one-off implementation details unless they establish stable guidance.\n\n")

	appendAgentInstructionsToBuilder(&sb, settings)
	appendDecisionsLogToBuilder(&sb)
	if milestoneScoped {
		appendMilestoneScopedAgentInstructionsContext(&sb, milestone, opts)
	} else {
		appendRepositoryAgentInstructionsContext(&sb)
	}
	appendSafetyAndRolePrompt(&sb, absRoot, resources.UpdateAgentInstructionsPrompt)
	return limitTextMiddle(sb.String(), maxAgentInstructionsContextChars, "AGENTS.md update prompt")
}

func appendRepositoryAgentInstructionsContext(sb *strings.Builder) {
	appendFileContentToBuilder(sb, "README", "README.md")
	appendFileContentToBuilder(sb, "Architecture Docs", filepath.Join("docs", "architecture.md"))
	appendFileContentToBuilder(sb, "Updater Prompt", filepath.Join("resources", "update_agent_instructions.md"))
	for _, name := range []string{"pm.md", "developer.md", "qa.md", "recommender.md"} {
		appendFileContentToBuilder(sb, "Built-in Agent Prompt "+name, filepath.Join("resources", "agents", name))
	}
	appendConfiguredChecksContext(sb)
	sb.WriteString("## Source Layout\n\n```text\n")
	if out, err := exec.Command("git", "ls-files").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || shouldExcludeAgentInstructionsContextPath(line) {
				continue
			}
			sb.WriteString(line + "\n")
		}
	}
	sb.WriteString("```\n\n")
	appendFilteredCurrentGitStatusToBuilder(sb)
	appendFilteredChangedFilesToBuilder(sb)
}

func appendConfiguredChecksContext(sb *strings.Builder) {
	cfg, err := config.LoadConfig(filepath.Join(".cyclestone", "milestone.yml"))
	if err != nil || cfg == nil {
		return
	}
	repos := append([]string(nil), cfg.Repositories...)
	sort.Strings(repos)
	checkSet := map[string]bool{}
	for _, milestone := range cfg.Milestones {
		for _, check := range milestone.Checks {
			check = strings.TrimSpace(check)
			if check != "" {
				checkSet[check] = true
			}
		}
	}
	checks := make([]string, 0, len(checkSet))
	for check := range checkSet {
		checks = append(checks, check)
	}
	sort.Strings(checks)

	sb.WriteString("## Configured Checks\n\n")
	sb.WriteString("- Default package checks: for each checked directory with `package.json`, run non-mutating npm lint, test, and build/build:packages scripts when present.\n")
	if len(repos) == 0 {
		sb.WriteString("- Configured repositories: none; Cyclestone discovers tracked repositories from the workspace.\n")
	} else {
		sb.WriteString("- Configured repositories:\n")
		for _, repo := range repos {
			sb.WriteString(fmt.Sprintf("  - %s\n", repo))
		}
	}
	if len(checks) == 0 {
		sb.WriteString("- Milestone check directories: none configured; package check directories fall back to tracked repositories with `package.json`.\n\n")
		return
	}
	sb.WriteString("- Milestone check directories:\n")
	for _, check := range checks {
		sb.WriteString(fmt.Sprintf("  - %s\n", check))
	}
	sb.WriteString("\n")
}

func appendFilteredCurrentGitStatusToBuilder(sb *strings.Builder) {
	sb.WriteString("## Current Git Status\n\n```text\n")
	for _, repo := range git.GetTrackedRepos() {
		sb.WriteString("### " + repo.Label + "\n")
		if out, err := exec.Command("git", "-C", repo.Path, "status", "--short").Output(); err == nil {
			sb.WriteString(filterAgentInstructionsGitContext(string(out), gitStatusPathFromLine))
		}
	}
	sb.WriteString("```\n\n")
}

func appendFilteredChangedFilesToBuilder(sb *strings.Builder) {
	sb.WriteString("## Changed Files\n\n```text\n")
	for _, repo := range git.GetTrackedRepos() {
		sb.WriteString("### " + repo.Label + "\n")
		if out, err := exec.Command("git", "-C", repo.Path, "diff", "--name-status").Output(); err == nil {
			sb.WriteString(filterAgentInstructionsGitContext(string(out), gitDiffPathFromLine))
		}
	}
	sb.WriteString("```\n\n")
}

func filterAgentInstructionsGitContext(text string, pathFn func(string) string) string {
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if shouldExcludeAgentInstructionsContextPath(pathFn(line)) {
			continue
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}

func gitStatusPathFromLine(line string) string {
	if len(line) < 4 {
		return strings.TrimSpace(line)
	}
	path := strings.TrimSpace(line[3:])
	if strings.Contains(path, " -> ") {
		parts := strings.Split(path, " -> ")
		path = parts[len(parts)-1]
	}
	return path
}

func gitDiffPathFromLine(line string) string {
	fields := strings.Split(line, "\t")
	if len(fields) == 0 {
		return strings.TrimSpace(line)
	}
	path := strings.TrimSpace(fields[len(fields)-1])
	if strings.Contains(path, " -> ") {
		parts := strings.Split(path, " -> ")
		path = parts[len(parts)-1]
	}
	return path
}

func appendMilestoneScopedAgentInstructionsContext(sb *strings.Builder, milestone config.Milestone, opts RunOptions) {
	sb.WriteString("## Scoped Context Boundary\n\nOnly the selected milestone spec, reports, handoffs, current git diff/status, and changed files are in scope. Do not load unrelated milestone specs, reports, state entries, or index entries.\n\n")
	appendMilestoneSpecToBuilder(sb, milestone)
	cyclePadded := "001"
	if state, err := config.LoadState(opts.StatePath); err == nil {
		next := state.GetMilestoneCycles(milestone.ID)
		if next < 1 {
			next = 1
		}
		cyclePadded = fmt.Sprintf("%03d", next)
	}
	for _, agentID := range []string{"pm", "developer", "qa", "recommender"} {
		appendHandoffToBuilder(sb, strings.ToUpper(agentID)+" Handoff", milestone.ID, cyclePadded, agentID, maxPromptFileChars, nil)
	}
	if entries, err := os.ReadDir(filepath.Join(".cyclestone", "reports", milestone.ID)); err == nil {
		var reports []string
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, ok := parseCycleDirName(entry.Name()); !ok {
				continue
			}
			reports = append(reports, filepath.Join(".cyclestone", "reports", milestone.ID, entry.Name(), "report.yaml"))
		}
		sort.Strings(reports)
		for _, report := range reports {
			appendFileContentToBuilder(sb, "Milestone Report "+filepath.Dir(report), report)
		}
	}
	appendCurrentGitStatusToBuilder(sb)
	appendChangedFilesToBuilder(sb)
}

func shouldExcludeAgentInstructionsContextPath(path string) bool {
	return strings.HasPrefix(path, ".cyclestone/reports/") ||
		strings.HasPrefix(path, ".cyclestone/temp/") ||
		path == ".cyclestone/state.json" ||
		strings.HasPrefix(path, "vendor/") ||
		strings.HasPrefix(path, "node_modules/") ||
		strings.Contains(path, "/vendor/") ||
		strings.Contains(path, "/node_modules/")
}

// handoffInstruction returns the runner-aware "Required YAML Handoff" intro text
// that explains how the agent should write its structured YAML handoff to the
// dedicated temp file. Aider-based runners (aider, ollama) are instructed to use
// a SEARCH/REPLACE edit block because Aider applies the edit and strips the fence
// markers. All other runners (codex, ollama-codex, etc.) are instructed to write
// clean YAML directly to the file, since they do not understand the SEARCH/REPLACE
// protocol and would write the fence markers literally into the file. The
// instruction text contains the {{HANDOFF_YAML_PATH}} placeholder so it is
// substituted alongside the other prompt placeholders at call time.
func handoffInstruction(runner, agentID string) string {
	aiderRunner := runner == "aider" || runner == "ollama"

	// Per-agent role sentence, deliverable sentence, purpose noun, and
	// consequence text. The developer's role sentence differs between Aider
	// and non-Aider runners because Aider requires SEARCH/REPLACE blocks for
	// code edits.
	var role, deliverable, purpose, consequence string
	switch agentID {
	case "pm":
		role = "**You are the Project Manager: do not make code changes and do not edit any source or repository file.**"
		deliverable = "Your only deliverable is the YAML handoff document below."
		purpose = "your plan"
		consequence = "If you do not write this YAML document, your plan cannot be recorded and the Developer receives nothing."
	case "developer":
		if aiderRunner {
			role = "**Make your code changes with SEARCH/REPLACE blocks as usual — that is your implementation work.**"
		} else {
			role = "**Make your code changes as usual — that is your implementation work.**"
		}
		deliverable = "After all code edits are done, you MUST write the YAML handoff document below."
		purpose = "what you did"
		consequence = "If you do not write this YAML document to that file, your work cannot be recorded and QA has nothing to review."
	case "qa":
		role = "**You are the Quality Manager: do not make code changes and do not edit any source or repository file.**"
		deliverable = "Your only deliverable is the YAML handoff document below."
		purpose = "your verdict"
		consequence = "If you do not write this YAML document, your verdict is lost and the cycle cannot be decided."
	default: // recommender
		role = "**Do not make code changes and do not edit any source or repository file.**"
		deliverable = "Your only deliverable is the YAML handoff document below."
		purpose = "your recommendation"
		consequence = "If you do not write this YAML document, your score and verdict are lost."
	}

	if aiderRunner {
		applyNote := ""
		readNote := "Cyclestone reads the result after you finish. "
		if agentID == "developer" {
			applyNote = "Aider applies this edit and writes the file; cyclestone reads it after you finish. "
			readNote = "" // already stated in applyNote
		}
		para1 := fmt.Sprintf("You are running inside the Aider coding assistant, whose system prompt demands code changes in SEARCH/REPLACE blocks. %s %s", role, deliverable)
		para2 := fmt.Sprintf("The YAML handoff is structured data describing %s — it is **not code**. The file `{{HANDOFF_YAML_PATH}}` has been added to your chat as an editable file. **Write your handoff by replacing that file's entire content with a SEARCH/REPLACE block**: use an empty `<<<<<<< SEARCH` section (the file starts empty) and put the full YAML after the `=======` divider, ending with `>>>>>>> REPLACE`. %sDo **not** also emit the YAML as prose, and do **not** wrap it in Markdown fences. %s%s", purpose, applyNote, readNote, consequence)
		return para1 + "\n\n" + para2
	}

	para1 := fmt.Sprintf("%s %s", role, deliverable)
	para2 := fmt.Sprintf("The YAML handoff is structured data describing %s — it is **not code**. Write your handoff by overwriting the file `{{HANDOFF_YAML_PATH}}` with the full YAML content directly. Do **not** wrap it in SEARCH/REPLACE block markers or Markdown fences, and do **not** also emit the YAML as prose. Cyclestone reads the result after you finish. %s", purpose, consequence)
	return para1 + "\n\n" + para2
}

// BuildPlanReevaluationPrompt assembles full context for AI Plan re-evaluation prompt.
func BuildPlanReevaluationPrompt(workspaceRoot string, plan config.Plan, extraGoal string) string {
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil || absRoot == "" {
		absRoot = workspaceRoot
	}
	if absRoot == "" {
		absRoot = "."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Repository root: %s\n\n", absRoot))
	sb.WriteString("Plan ID: " + plan.ID + "\n")
	sb.WriteString("Plan Title: " + plan.Title + "\n")
	sb.WriteString("Plan Objective: " + plan.Objective + "\n")
	if strings.TrimSpace(extraGoal) != "" {
		sb.WriteString("Re-Evaluation Trigger / Goal: " + strings.TrimSpace(extraGoal) + "\n")
	}
	sb.WriteString("\n")

	sb.WriteString("## Active Plan Specification\n\n")
	sb.WriteString(fmt.Sprintf("Briefing Order: %v\n\n", plan.BriefingOrder))
	for _, b := range plan.Briefings {
		sb.WriteString(fmt.Sprintf("### Briefing %s\n", b.ID))
		sb.WriteString("Title: " + b.Title + "\n")
		sb.WriteString("Objective: " + b.Objective + "\n")
		sb.WriteString("Status: " + b.Status + "\n")
		if b.MilestoneID != "" {
			sb.WriteString("Linked Milestone: " + b.MilestoneID + "\n")
		}
		if len(b.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf("Depends On: %v\n", b.DependsOn))
		}
		if len(b.Constraints) > 0 {
			sb.WriteString(fmt.Sprintf("Constraints: %v\n", b.Constraints))
		}
		sb.WriteString("\n")
	}

	reportsDir := filepath.Join(workspaceRoot, ".cyclestone", "reports")
	if entries, err := os.ReadDir(reportsDir); err == nil && len(entries) > 0 {
		sb.WriteString("## Milestone Execution Reports & QA Findings\n\n")
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			msID := entry.Name()
			summaryPath := filepath.Join(reportsDir, msID, "summary.md")
			if data, err := os.ReadFile(summaryPath); err == nil && len(data) > 0 {
				sb.WriteString(fmt.Sprintf("### Milestone %s Summary\n", msID))
				sb.WriteString(string(data) + "\n\n")
			}
		}
	}

	settings := config.LoadMergedSettings()
	appendAgentInstructionsToBuilder(&sb, settings)
	appendDecisionsLogToBuilder(&sb)

	sb.WriteString("\n## Planner Instructions & Output Schema\n\n")
	plannerPrompt := resources.PlannerPrompt
	if strings.TrimSpace(plannerPrompt) == "" {
		plannerPrompt = "Return a structured JSON proposal matching the PlanReevaluationProposal schema."
	}
	sb.WriteString(plannerPrompt)
	sb.WriteString("\n")

	return sb.String()
}
