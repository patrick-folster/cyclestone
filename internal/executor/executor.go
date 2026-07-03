package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/git"
	"github.com/patrick-folster/cyclestone/resources"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

// RunOptions defines options for a milestone cycle run.
type RunOptions struct {
	ConfigPath     string
	StatePath      string
	NoBranchChange bool
	Unrestricted   bool
	SingleAgentID  string // if non-empty, only run this agent
	CycleNote      string
}

func sendExecutorMsg(ctx context.Context, ch chan tea.Msg, msg tea.Msg) bool {
	if ch == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case ch <- msg:
		return true
	case <-ctx.Done():
		select {
		case ch <- msg:
			return true
		default:
			return false
		}
	}
}

func runnerModeLabel(opts RunOptions) string {
	if opts.Unrestricted {
		return "unrestricted"
	}
	return "sandbox"
}

func normalizedMaxModelCalls(settings config.Settings) int {
	if settings.MaxModelCallsPerPhase > 0 {
		return settings.MaxModelCallsPerPhase
	}
	return 50
}

func normalizedMaxTokenBudget(settings config.Settings) int {
	if settings.MaxTokenBudgetPerPhase > 0 {
		return settings.MaxTokenBudgetPerPhase
	}
	return 1000000
}

func configuredModelForRunner(runner string, settings config.Settings) string {
	switch runner {
	case "aider":
		return settings.AiderModel
	case "ollama":
		return settings.OllamaModel
	default:
		return ""
	}
}

func describeRunnerCommand(runner string, opts RunOptions) string {
	switch runner {
	case "manual":
		return "manual phase"
	case "codex":
		modeArgs := "--sandbox workspace-write --ask-for-approval never"
		if opts.Unrestricted {
			modeArgs = "--sandbox danger-full-access --dangerously-bypass-approvals-and-sandbox"
		}
		return "codex " + modeArgs + " exec --cd . --skip-git-repo-check -- -"
	case "agy":
		if opts.Unrestricted {
			return "agy --print - --print-timeout 30m --dangerously-skip-permissions"
		}
		return "agy --print - --print-timeout 30m --sandbox --dangerously-skip-permissions"
	case "aider", "ollama":
		return "aider --message-file <prompt> --yes-always --no-auto-commits --no-dirty-commits --no-gitignore"
	default:
		return "unsupported runner"
	}
}

const maxPreviousCycleSummaryChars = 24000
const maxPromptFileChars = 60000
const maxToolOutputChars = 30000
const maxPhaseReportOutputChars = 24000
const maxRecommenderReportOutputChars = 12000
const maxFallbackHandoffChars = 5000
const maxFallbackHandoffFieldChars = 900
const maxRetainedConversationMessages = 8
const charsPerEstimatedToken = 4

type codexThreadMetadata struct {
	ThreadID string `json:"thread_id"`
}

// AgentStartedMsg is sent when an agent starts execution.
type AgentStartedMsg struct {
	AgentID string
}

// AgentProgressMsg is sent when an agent outputs a line to stdout/stderr.
type AgentProgressMsg struct {
	AgentID string
	LogLine string
}

// AgentCompletedMsg is sent when an agent finishes execution.
type AgentCompletedMsg struct {
	AgentID    string
	ExitCode   int
	Timestamp  time.Time
	OutputFile string
}

// RunnerStatusMsg carries structured live-runner details that are unsafe or
// brittle to infer from free-form streamed logs.
type RunnerStatusMsg struct {
	MilestoneID         string
	CycleNumber         int
	CycleStatus         string
	Phase               string
	AgentID             string
	Runner              string
	Model               string
	Mode                string
	ReportFile          string
	OutputFile          string
	LatestCommand       string
	LatestToolCall      string
	ModelCalls          int
	ToolCalls           int
	EstimatedTokens     int
	PromptTokens        int
	CompletionTokens    int
	MaxModelCalls       int
	MaxTokenBudget      int
	StopOrDoneReason    string
	LastError           string
	NextSuggestedAction string
}

// CycleFinishedMsg is sent when the milestone cycle completes.
type CycleFinishedMsg struct {
	MilestoneID string
	CycleNumber int
	Status      string // "approved", "blocked", "failed"
	ReportFile  string
	Error       error
}

// CycleMetadata holds the aggregated context and state validation info for a milestone cycle.
type CycleMetadata struct {
	MilestoneID    string                   `json:"milestone_id"`
	CycleNumber    int                      `json:"cycle_number"`
	Timestamp      string                   `json:"timestamp"`
	BranchSnapshot []git.RepoBranchSnapshot `json:"branch_snapshot,omitempty"`
	GitContext     string                   `json:"git_context"`
}

// ExecuteCycle runs the milestone cycle as a background task.
func ExecuteCycle(ctx context.Context, milestone config.Milestone, pipeline []config.Agent, opts RunOptions, state *config.State, ch chan tea.Msg) {
	reportsDir := filepath.Join(".cyclestone", "reports")
	if ch != nil {
		sendExecutorMsg(ctx, ch, RunnerStatusMsg{
			MilestoneID: milestone.ID,
			CycleStatus: "preparing",
			Phase:       "preparing",
			Mode:        runnerModeLabel(opts),
		})
	}
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		sendExecutorMsg(ctx, ch, CycleFinishedMsg{MilestoneID: milestone.ID, Error: fmt.Errorf("failed to create reports directory: %w", err)})
		return
	}

	cycleStartTime := time.Now()
	cycleNum, branchName, previousReportPath, reportPath, metadataPath, repos, gitError, err := prepareCycleEnvironment(opts, state, milestone, reportsDir)
	if err != nil {
		sendExecutorMsg(ctx, ch, CycleFinishedMsg{MilestoneID: milestone.ID, Error: err})
		return
	}

	// Initialize cycle log in state
	initCycleLog(state, opts, milestone.ID, cycleNum, branchName)

	// Prepare the main cycle report file
	reportFile, err := os.OpenFile(reportPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		sendExecutorMsg(ctx, ch, CycleFinishedMsg{MilestoneID: milestone.ID, CycleNumber: cycleNum, Error: fmt.Errorf("failed to open report file: %w", err)})
		return
	}
	defer reportFile.Close()

	// Write initial report header
	writeReportHeader(reportFile, milestone.ID, branchName, cycleNum, previousReportPath, metadataPath, opts, gitError)

	var cycleStatus = "approved"
	codexThreadID := ""
	cyclePadded := fmt.Sprintf("%03d", cycleNum)
	codexThreadMetadataPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s-codex-thread.json", milestone.ID, cyclePadded))
	settings := config.LoadMergedSettings()
	if ch != nil {
		sendExecutorMsg(ctx, ch, RunnerStatusMsg{
			MilestoneID:    milestone.ID,
			CycleNumber:    cycleNum,
			CycleStatus:    "running",
			Phase:          "cycle",
			Mode:           runnerModeLabel(opts),
			ReportFile:     reportPath,
			MaxModelCalls:  normalizedMaxModelCalls(settings),
			MaxTokenBudget: normalizedMaxTokenBudget(settings),
		})
	}

	// Iterate through the pipeline
	cycleStatus, interrupted := runAgentPipeline(ctx, pipeline, milestone, opts, state, ch, reportsDir, cycleNum, previousReportPath, metadataPath, settings, reportFile, codexThreadMetadataPath, &codexThreadID)
	if interrupted {
		return
	}

	// Run Package Manager and branch checks if configured
	if ch != nil {
		sendExecutorMsg(ctx, ch, RunnerStatusMsg{MilestoneID: milestone.ID, CycleNumber: cycleNum, CycleStatus: "running", Phase: "post-cycle checks", ReportFile: reportPath})
	}
	cycleStatus = runPostCycleChecks(ctx, milestone, repos, opts, metadataPath, reportFile, cycleStatus)
	if cycleStatus == "failed" && ch != nil {
		sendExecutorMsg(ctx, ch, RunnerStatusMsg{
			MilestoneID:         milestone.ID,
			CycleNumber:         cycleNum,
			CycleStatus:         "failed",
			Phase:               "post-cycle checks",
			ReportFile:          reportPath,
			LastError:           "package or branch policy checks failed",
			NextSuggestedAction: "Review the cycle report and fix failing checks before rerunning.",
		})
	}

	// Run Cycle Recommender Agent
	runRecommenderPhase(ctx, pipeline, milestone, opts, state, ch, reportsDir, cycleNum, reportPath, settings, reportFile, &codexThreadID, codexThreadMetadataPath)

	// Human review steps
	writeReportDetailf(reportFile, "\n## Human Review Steps\n\n")
	writeReportDetailf(reportFile, "1. Review `%s`.\n", reportPath)
	writeReportDetailf(reportFile, "2. Review the cycle summary in `.cyclestone/reports/%s.md`.\n", milestone.ID)
	writeReportDetailf(reportFile, "3. Inspect changed files in each tracked repository listed in the git context with git status and git diff.\n")
	if opts.NoBranchChange {
		writeReportDetailf(reportFile, "4. Confirm repositories remained on their original branches.\n")
	} else {
		writeReportDetailf(reportFile, "4. Confirm changed repositories are on %s-prefixed milestone branches.\n", branchName)
	}
	writeReportDetailf(reportFile, "5. Confirm QA verdict and unresolved issues.\n")
	writeReportDetailf(reportFile, "\nFinished: %s\n", time.Now().Format("2006-01-02 15:04:05 -0700"))

	duration := time.Since(cycleStartTime)
	state.UpdateLastCycleLog(milestone.ID, func(cl *config.MilestoneCycleLog) {
		cl.Status = cycleStatus
		cl.Duration = duration.String()
	})
	state.SetMilestoneCycles(milestone.ID, cycleNum)
	state.SetMilestoneStatus(milestone.ID, strings.ToUpper(cycleStatus[:1])+cycleStatus[1:])
	_ = config.SaveState(opts.StatePath, state)

	// Update cycle summary report
	_ = updateCycleSummaryReport(milestone.ID, cycleNum, reportsDir)

	if ch != nil {
		finalStatus := "finished"
		if cycleStatus == "failed" {
			finalStatus = "failed"
		}
		sendExecutorMsg(ctx, ch, RunnerStatusMsg{
			MilestoneID:         milestone.ID,
			CycleNumber:         cycleNum,
			CycleStatus:         finalStatus,
			Phase:               "complete",
			ReportFile:          reportPath,
			NextSuggestedAction: "Review the report and continue from milestone details.",
		})
	}
	sendExecutorMsg(ctx, ch, CycleFinishedMsg{
		MilestoneID: milestone.ID,
		CycleNumber: cycleNum,
		Status:      cycleStatus,
		ReportFile:  reportPath,
		Error:       nil,
	})
}

func writeGitContext(outputPath string, milestoneID string, cycleNum int) error {
	repos := git.GetTrackedRepos()
	return writeGitContextForRepos(outputPath, milestoneID, cycleNum, repos)
}

func generateGitContextForRepos(milestoneID string, cycleNum int, repos []git.RepoInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Git Context For %s Cycle %03d\n\n", milestoneID, cycleNum))

	for _, repo := range repos {
		context := collectRepoGitContext(repo)
		sb.WriteString(fmt.Sprintf("## %s\n\n", repo.Label))
		if context.IsWorktree {
			sb.WriteString("Branch:\n\n```text\n")
			sb.WriteString(context.Branch + "\n")
			sb.WriteString("```\n\nStatus:\n\n```text\n")
			sb.WriteString(context.Status)
			sb.WriteString("```\n\nChanged files:\n\n```text\n")
			sb.WriteString(context.ChangedFiles)
			sb.WriteString("```\n\nDiff stat:\n\n```text\n")
			sb.WriteString(context.DiffStat)
			sb.WriteString("```\n\n")
		} else {
			sb.WriteString(fmt.Sprintf("No git worktree found at %s.\n\n", repo.Path))
		}
	}
	return sb.String()
}

func writeGitContextForRepos(outputPath string, milestoneID string, cycleNum int, repos []git.RepoInfo) error {
	content := generateGitContextForRepos(milestoneID, cycleNum, repos)
	return os.WriteFile(outputPath, []byte(content), 0644)
}

func loadGitContext(path string) string {
	if path == "" {
		return ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Check if this is a JSON metadata file
	if strings.HasSuffix(path, ".json") {
		var meta CycleMetadata
		if err := json.Unmarshal(content, &meta); err == nil {
			return meta.GitContext
		}
	}
	return string(content)
}

func appendGitContextToBuilder(sb *strings.Builder, path string) {
	if contextStr := loadGitContext(path); contextStr != "" {
		sb.WriteString("## Git Context\n\n")
		sb.WriteString(limitTextMiddle(contextStr, maxPromptFileChars, path))
		sb.WriteString("\n\n")
	}
}

type repoGitContext struct {
	IsWorktree   bool
	Branch       string
	Status       string
	ChangedFiles string
	DiffStat     string
}

func collectRepoGitContext(repo git.RepoInfo) repoGitContext {
	if !git.IsGitWorktree(repo.Path) {
		return repoGitContext{}
	}

	context := repoGitContext{IsWorktree: true}
	if out, err := exec.Command("git", "-C", repo.Path, "branch", "--show-current").Output(); err == nil {
		context.Branch = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", repo.Path, "status", "--short").Output(); err == nil {
		context.Status = string(out)
	}
	if out, err := exec.Command("git", "-C", repo.Path, "diff", "--name-status").Output(); err == nil {
		context.ChangedFiles = string(out)
	}
	if out, err := exec.Command("git", "-C", repo.Path, "diff", "--stat").Output(); err == nil {
		context.DiffStat = string(out)
	}
	return context
}

func defaultPackageCheckDirs() []string {
	repos := git.GetTrackedRepos()
	return defaultPackageCheckDirsForRepos(repos)
}

func defaultPackageCheckDirsForRepos(repos []git.RepoInfo) []string {
	var checkDirs []string
	for _, repo := range repos {
		if _, err := os.Stat(filepath.Join(repo.Path, "package.json")); err == nil {
			checkDirs = append(checkDirs, repo.Path)
		}
	}
	return checkDirs
}

type PackageJSON struct {
	Scripts map[string]string `json:"scripts"`
}

func runChecksForPackage(ctx context.Context, label, dir string, reportFile *os.File) (int, string) {
	var checkFailures int
	var logBuilder strings.Builder

	packageJSONPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return 0, ""
	}

	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		logBuilder.WriteString(fmt.Sprintf("\n## %s checks\n\nFailed to parse package.json: %v\n", label, err))
		return 1, logBuilder.String()
	}

	runCmd := func(title string, args ...string) bool {
		logBuilder.WriteString(fmt.Sprintf("\n## %s %s\n\n```text\n$", label, title))
		for _, arg := range args {
			logBuilder.WriteString(" " + arg)
		}
		logBuilder.WriteString("\n")

		cmd := exec.CommandContext(ctx, "npm", args...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out

		err := cmd.Run()
		logBuilder.WriteString(out.String())

		exitCode := 0
		if err != nil {
			exitCode = 1
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			}
			checkFailures++
		}
		logBuilder.WriteString(fmt.Sprintf("\nExit status: %d\n```\n", exitCode))
		return err == nil
	}

	// 1. Lint
	if lintScript, ok := pkg.Scripts["lint"]; ok {
		if strings.Contains(lintScript, "--fix") {
			logBuilder.WriteString(fmt.Sprintf("\n## %s lint\n\nSkipped npm run lint because the configured lint script contains --fix and may modify files. Run a non-mutating lint command manually before approval.\n", label))
		} else {
			runCmd("lint", "--prefix", dir, "run", "lint")
		}
	} else {
		logBuilder.WriteString(fmt.Sprintf("\n## %s lint\n\nNo lint script found.\n", label))
	}

	// 2. Test
	if _, ok := pkg.Scripts["test"]; ok {
		runCmd("test", "--prefix", dir, "test")
	} else {
		logBuilder.WriteString(fmt.Sprintf("\n## %s test\n\nNo test script found.\n", label))
	}

	// 3. Build
	if _, ok := pkg.Scripts["build"]; ok {
		runCmd("build", "--prefix", dir, "run", "build")
	} else if _, ok := pkg.Scripts["build:packages"]; ok {
		runCmd("build:packages", "--prefix", dir, "run", "build:packages")
	} else {
		logBuilder.WriteString(fmt.Sprintf("\n## %s build\n\nNo build or build:packages script found.\n", label))
	}

	return checkFailures, logBuilder.String()
}

func writeCodexThreadMetadata(path, threadID string) error {
	data, err := json.MarshalIndent(codexThreadMetadata{ThreadID: threadID}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func parseCodexThreadID(text string) string {
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		var evt struct {
			Msg      string `json:"msg"`
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Thread   struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := json.Unmarshal([]byte(scanner.Text()), &evt); err != nil {
			continue
		}
		if evt.ThreadID != "" && (evt.Msg == "thread.started" || evt.Type == "thread.started" || evt.Msg == "" && evt.Type == "") {
			return evt.ThreadID
		}
		if evt.Thread.ID != "" && (evt.Msg == "thread.started" || evt.Type == "thread.started") {
			return evt.Thread.ID
		}
	}
	return ""
}

func updateCycleSummaryReport(milestoneID string, latest int, reportsDir string) error {
	summaryPath := filepath.Join(reportsDir, fmt.Sprintf("%s.md", milestoneID))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Milestone Cycle Summary: %s\n\n", milestoneID))
	sb.WriteString(fmt.Sprintf("- Milestone file: .cyclestone/milestones/%s.md\n", milestoneID))
	sb.WriteString(fmt.Sprintf("- Latest cycle: %03d\n", latest))
	sb.WriteString(fmt.Sprintf("- Updated: %s\n", time.Now().Format("2006-01-02 15:04:05 -0700")))
	sb.WriteString("\n## Cycle History\n\n")

	files, err := filepath.Glob(filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-*.yaml", milestoneID)))
	if err == nil {
		sort.Strings(files)
		for _, file := range files {
			baseName := filepath.Base(file)
			if !isPrimaryCycleReportFile(milestoneID, baseName) {
				continue
			}
			cyclePart := strings.TrimPrefix(baseName, milestoneID+"-cycle-")
			cyclePart = strings.TrimSuffix(cyclePart, ".yaml")

			report, _ := readCycleReportYAML(file)
			started := strings.TrimSpace(report.Started)
			verdict := firstReportSignal(report.Details)
			if report.ParseError != "" {
				verdict = report.ParseError
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

func isPrimaryCycleReportFile(milestoneID, baseName string) bool {
	prefix := milestoneID + "-cycle-"
	if !strings.HasPrefix(baseName, prefix) || !strings.HasSuffix(baseName, ".yaml") {
		return false
	}
	cyclePart := strings.TrimSuffix(strings.TrimPrefix(baseName, prefix), ".yaml")
	if len(cyclePart) != 3 {
		return false
	}
	_, err := strconv.Atoi(cyclePart)
	return err == nil
}

type cycleReportYAML struct {
	MilestoneID         string `yaml:"milestone_id"`
	Started             string `yaml:"started"`
	Root                string `yaml:"root"`
	Branch              string `yaml:"branch"`
	BranchChanges       string `yaml:"branch_changes"`
	Cycle               string `yaml:"cycle"`
	CycleMode           string `yaml:"cycle_mode"`
	MilestoneFile       string `yaml:"milestone_file"`
	SummaryReport       string `yaml:"summary_report"`
	PreviousCycleReport string `yaml:"previous_cycle_report"`
	CycleMetadata       string `yaml:"cycle_metadata"`
	HumanCycleNote      string `yaml:"human_cycle_note"`
	Details             string `yaml:"details"`
	ParseError          string
}

func readCycleReportYAML(path string) (cycleReportYAML, string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return cycleReportYAML{ParseError: fmt.Sprintf("failed to read YAML report: %v", err)}, ""
	}

	text := string(content)
	var report cycleReportYAML
	if err := yaml.Unmarshal(content, &report); err != nil {
		report.ParseError = fmt.Sprintf("malformed YAML report: %v", err)
	}
	return report, text
}

func buildScopedMilestoneContext(milestone config.Milestone, opts RunOptions) string {
	var sb strings.Builder

	sb.WriteString("Active milestone only. Do not load unrelated milestone specs, reports, state entries, or index entries unless a human explicitly asks.\n\n")
	sb.WriteString("### Milestone Index Entry\n\n")
	sb.WriteString(fmt.Sprintf("- id: %s\n", milestone.ID))
	if milestone.Title != "" {
		sb.WriteString(fmt.Sprintf("- title: %s\n", milestone.Title))
	}
	if milestone.SpecPath != "" {
		sb.WriteString(fmt.Sprintf("- spec_path: %s\n", milestone.SpecPath))
	}
	if milestone.Goal != "" {
		sb.WriteString(fmt.Sprintf("- goal: %s\n", milestone.Goal))
	}
	if len(milestone.AcceptanceCriteria) > 0 {
		sb.WriteString("- acceptance_criteria:\n")
		for _, criterion := range milestone.AcceptanceCriteria {
			sb.WriteString(fmt.Sprintf("  - %s\n", criterion))
		}
	}
	if len(milestone.Checks) > 0 {
		sb.WriteString("- checks:\n")
		for _, check := range milestone.Checks {
			sb.WriteString(fmt.Sprintf("  - %s\n", check))
		}
	}

	statePath := opts.StatePath
	if statePath == "" {
		statePath = filepath.Join(".cyclestone", "state.json")
	}
	state, err := config.LoadState(statePath)
	if err != nil {
		return sb.String()
	}

	type scopedState struct {
		ActiveMilestoneID string                     `json:"active_milestone_id,omitempty"`
		Status            string                     `json:"status"`
		Cycles            int                        `json:"cycles"`
		Recommendation    int                        `json:"recommendation"`
		History           []config.MilestoneCycleLog `json:"history,omitempty"`
	}
	scoped := scopedState{
		ActiveMilestoneID: state.ActiveMilestoneID,
		Status:            state.GetMilestoneStatus(milestone.ID),
		Cycles:            state.GetMilestoneCycles(milestone.ID),
		Recommendation:    state.GetMilestoneRecommendation(milestone.ID),
		History:           state.GetHistory(milestone.ID),
	}
	data, err := json.MarshalIndent(scoped, "", "  ")
	if err != nil {
		return sb.String()
	}
	sb.WriteString("\n### Runtime State\n\n```json\n")
	sb.Write(data)
	sb.WriteString("\n```\n")
	return sb.String()
}

func summarizeCycleReport(path string) string {
	report, text := readCycleReportYAML(path)
	if text == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Source report: %s\n", path))
	sb.WriteString(fmt.Sprintf("Original size: %d chars\n", len([]rune(text))))
	sb.WriteString("Note: this is a bounded continuation summary. Open the source report if exact historical logs are needed.\n\n")

	metadata := cycleReportMetadata(report)
	details := report.Details
	if report.ParseError != "" {
		metadata = append(metadata, report.ParseError)
		details = text
	}

	phases, important := summarizeCycleReportDetails(details)

	appendList := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		sb.WriteString("### " + title + "\n\n")
		for _, line := range lines {
			sb.WriteString("- " + line + "\n")
		}
		sb.WriteString("\n")
	}

	appendList("Metadata", metadata)
	appendList("Top-Level Sections", phases)
	appendList("Key Continuation Signals", important)

	result := sb.String()
	runes := []rune(result)
	if len(runes) > maxPreviousCycleSummaryChars {
		return string(runes[:maxPreviousCycleSummaryChars]) + "\n\n[Previous cycle summary truncated to internal safety limit. Open source report for full history.]\n"
	}
	return result
}

func cycleReportMetadata(report cycleReportYAML) []string {
	fields := []struct {
		label string
		value string
	}{
		{"milestone_id", report.MilestoneID},
		{"started", report.Started},
		{"root", report.Root},
		{"branch", report.Branch},
		{"branch_changes", report.BranchChanges},
		{"cycle", report.Cycle},
		{"cycle_mode", report.CycleMode},
		{"milestone_file", report.MilestoneFile},
		{"summary_report", report.SummaryReport},
		{"previous_cycle_report", report.PreviousCycleReport},
		{"cycle_metadata", report.CycleMetadata},
	}

	var metadata []string
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		metadata = append(metadata, fmt.Sprintf("%s: %s", field.label, field.value))
	}
	return metadata
}

func summarizeCycleReportDetails(details string) ([]string, []string) {
	scanner := bufio.NewScanner(strings.NewReader(details))
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	inFence := false
	currentSection := ""
	var phases []string
	var important []string
	var currentBlock []string
	collectBlock := false
	importantSeen := map[string]bool{}

	flushBlock := func() {
		if len(currentBlock) == 0 {
			return
		}
		important = append(important, currentBlock...)
		currentBlock = nil
	}
	addImportant := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || importantSeen[line] {
			return
		}
		importantSeen[line] = true
		important = append(important, line)
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}

		if !inFence && strings.HasPrefix(trimmed, "## ") {
			flushBlock()
			currentSection = strings.TrimPrefix(trimmed, "## ")
			phases = append(phases, currentSection)
			collectBlock = currentSection == "Check Summary" ||
				currentSection == "Branch Policy Violation" ||
				currentSection == "Branch Policy Check" ||
				currentSection == "Human Review Steps"
			continue
		}
		if !inFence && strings.HasPrefix(trimmed, "### ") {
			flushBlock()
			currentSection = strings.TrimPrefix(trimmed, "### ")
			collectBlock = currentSection == "Execution Stalled"
			continue
		}

		if collectBlock && !inFence {
			if trimmed == "" {
				continue
			}
			currentBlock = append(currentBlock, trimmed)
			continue
		}

		if isContinuationSignalLine(trimmed, currentSection) {
			addImportant(trimmed)
		}
	}
	flushBlock()
	return phases, important
}

func firstReportSignal(details string) string {
	_, important := summarizeCycleReportDetails(details)
	if len(important) == 0 {
		return ""
	}
	return important[0]
}

func limitTextMiddle(text string, maxChars int, source string) string {
	if maxChars <= 0 {
		return text
	}

	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	notice := fmt.Sprintf("\n\n[Content truncated: %s was %d chars; keeping first and last %d chars.]\n\n", source, len(runes), maxChars)
	noticeRunes := []rune(notice)
	if len(noticeRunes) >= maxChars {
		return string(noticeRunes[:maxChars])
	}

	remaining := maxChars - len(noticeRunes)
	headLen := remaining / 2
	tailLen := remaining - headLen

	var sb strings.Builder
	sb.WriteString(string(runes[:headLen]))
	sb.WriteString(notice)
	sb.WriteString(string(runes[len(runes)-tailLen:]))
	return sb.String()
}

func isContinuationSignalLine(line, section string) bool {
	if line == "" {
		return false
	}

	if strings.HasPrefix(line, "Exit status:") ||
		strings.HasPrefix(line, "O approved") ||
		strings.HasPrefix(line, "O blocked") ||
		strings.HasPrefix(line, "O failed") ||
		strings.HasPrefix(line, "R ") ||
		strings.HasPrefix(line, "P ") ||
		strings.HasPrefix(line, "S ") {
		return true
	}

	lower := strings.ToLower(line)
	if strings.Contains(lower, "verdict") ||
		strings.Contains(lower, "blocker") ||
		strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "unresolved") ||
		strings.Contains(lower, "missing") ||
		strings.Contains(lower, "recommendation score") {
		return true
	}

	return strings.Contains(section, "Quality") && strings.HasPrefix(line, "- ")
}

// CreateMilestoneProgressMsg is sent when the creation agent outputs a line to stdout/stderr.
type CreateMilestoneProgressMsg struct {
	LogLine string
}

// CreateMilestoneFinishedMsg is sent when the creation agent finishes execution.
type CreateMilestoneFinishedMsg struct {
	Error error
}

// ExecuteMilestoneCreation runs the creation prompt through a supported runner in the background.
func ExecuteMilestoneCreation(ctx context.Context, runner string, prompt string, opts RunOptions, ch chan tea.Msg, milestoneID string, defaultTitle string) {
	settings := config.LoadMergedSettings()
	if limit, ok := inputSizeLimitForRunner(runner, settings); ok && len([]rune(prompt)) > limit {
		ch <- CreateMilestoneFinishedMsg{Error: inputSizeGuardError(runner, len([]rune(prompt)), limit)}
		return
	}

	// Setup command for agy/codex/aider/ollama.
	var cmd *exec.Cmd
	if runner == "agy" {
		args := []string{"--print", "-", "--print-timeout", "30m"}
		if opts.Unrestricted {
			args = append(args, "--dangerously-skip-permissions")
		} else {
			args = append(args, "--sandbox", "--dangerously-skip-permissions")
		}
		cmd = exec.CommandContext(ctx, "agy", args...)
		cmd.Stdin = strings.NewReader(prompt)
	} else if runner == "aider" || runner == "ollama" {
		cleanupGitignore := setupTemporaryGitignore()
		defer cleanupGitignore()
		var promptFile string
		var cleanup func()
		_ = os.MkdirAll(".cyclestone", 0755)
		promptFile = filepath.Join(".cyclestone", "aider-milestone-prompt.txt")
		if err := os.WriteFile(promptFile, []byte(prompt), 0644); err == nil {
			cleanup = func() { _ = os.Remove(promptFile) }
		} else {
			// Fallback 1: Write to workspace root
			promptFile = ".aider-milestone-prompt.txt"
			if err2 := os.WriteFile(promptFile, []byte(prompt), 0644); err2 == nil {
				cleanup = func() { _ = os.Remove(promptFile) }
			} else {
				// Fallback 2: System temp dir
				tmpFile, err3 := os.CreateTemp("", "aider-milestone-prompt-*.txt")
				if err3 != nil {
					ch <- CreateMilestoneFinishedMsg{Error: fmt.Errorf("failed to create prompt file: %w (fallback errors: %v, %v)", err, err2, err3)}
					return
				}
				promptFile = tmpFile.Name()
				if _, err4 := tmpFile.Write([]byte(prompt)); err4 != nil {
					tmpFile.Close()
					_ = os.Remove(promptFile)
					ch <- CreateMilestoneFinishedMsg{Error: err4}
					return
				}
				tmpFile.Close()
				cleanup = func() { _ = os.Remove(promptFile) }
			}
		}
		defer cleanup()
		args := []string{
			"--message-file", promptFile,
			"--yes-always",
			"--no-auto-commits",
			"--no-dirty-commits",
			"--no-gitignore",
		}
		var model string
		if runner == "aider" {
			model = settings.AiderModel
		} else { // ollama
			model = settings.OllamaModel
			if !strings.Contains(model, "/") {
				model = "ollama_chat/" + model
			}
			cleanup := setupTemporaryAiderSettings(model, settings)
			defer cleanup()
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.CommandContext(ctx, "aider", args...)
		cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
		if runner == "ollama" {
			host := settings.OllamaHost
			if host == "" {
				host = "http://localhost:11434"
			}
			cmd.Env = append(cmd.Env, "OLLAMA_API_BASE="+host)
		}
	} else {
		if runner != "codex" {
			ch <- CreateMilestoneFinishedMsg{Error: fmt.Errorf("unsupported runner: %s", runner)}
			return
		}
		var args []string
		if opts.Unrestricted {
			args = append(args, "--sandbox", "danger-full-access", "--dangerously-bypass-approvals-and-sandbox")
		} else {
			args = append(args, "--sandbox", "workspace-write", "--ask-for-approval", "never")
		}
		args = append(args, "exec", "--cd", ".", "--skip-git-repo-check", "--", "-")

		cmd = exec.CommandContext(ctx, "codex", args...)
		cmd.Stdin = strings.NewReader(prompt)
	}

	// Capture stdout and stderr
	r, w := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w

	scanDone := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			// Send progress update
			ch <- CreateMilestoneProgressMsg{LogLine: line}
		}
		close(scanDone)
	}()

	runErr := cmd.Start()
	if runErr == nil {
		runErr = cmd.Wait()
	}
	w.Close()
	<-scanDone

	var exitCode int
	if runErr != nil {
		exitCode = 1
		if exitError, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
		ch <- CreateMilestoneFinishedMsg{Error: fmt.Errorf("agent exited with code %d: %w", exitCode, runErr)}
	} else {
		ch <- CreateMilestoneFinishedMsg{Error: nil}
	}
}

func inputSizeLimitForRunner(runner string, settings config.Settings) (int, bool) {
	switch runner {
	case "codex", "agy", "aider", "ollama":
		limit := settings.MaxLLMInputChars
		if limit <= 0 {
			limit = 900000
		}
		return limit, true
	default:
		return 0, false
	}
}

func inputSizeGuardError(runner string, actualChars int, maxChars int) error {
	return fmt.Errorf("input content is %d chars, above %s safety limit of %d chars", actualChars, runner, maxChars)
}

func stringPtrValue(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func buildCodexCommand(ctx context.Context, opts RunOptions, enableResume bool, threadID string) *exec.Cmd {
	var args []string
	if opts.Unrestricted {
		args = append(args, "--sandbox", "danger-full-access", "--dangerously-bypass-approvals-and-sandbox")
	} else {
		args = append(args, "--sandbox", "workspace-write", "--ask-for-approval", "never")
	}
	args = append(args, "exec")
	if enableResume && threadID == "" {
		args = append(args, "--json")
	}
	if enableResume && threadID != "" {
		args = append(args, "resume", threadID)
	}
	args = append(args, "--cd", ".", "--skip-git-repo-check", "--", "-")
	return exec.CommandContext(ctx, "codex", args...)
}

func setupTemporaryAiderSettings(model string, settings config.Settings) func() {
	if settings.OllamaNumCtx <= 0 && settings.OllamaNumPredict <= 0 {
		return func() {}
	}

	const filename = ".aider.model.settings.yml"
	var backup []byte
	exists := false
	if data, err := os.ReadFile(filename); err == nil {
		backup = data
		exists = true
	}

	type AiderModelSetting struct {
		Name        string                 `yaml:"name"`
		ExtraParams map[string]interface{} `yaml:"extra_params,omitempty"`
	}

	var list []AiderModelSetting
	if exists {
		_ = yaml.Unmarshal(backup, &list)
	}

	found := false
	for i, entry := range list {
		if entry.Name == model {
			if list[i].ExtraParams == nil {
				list[i].ExtraParams = make(map[string]interface{})
			}
			if settings.OllamaNumCtx > 0 {
				list[i].ExtraParams["num_ctx"] = settings.OllamaNumCtx
			}
			if settings.OllamaNumPredict > 0 {
				list[i].ExtraParams["num_predict"] = settings.OllamaNumPredict
			}
			found = true
			break
		}
	}
	if !found {
		extraParams := make(map[string]interface{})
		if settings.OllamaNumCtx > 0 {
			extraParams["num_ctx"] = settings.OllamaNumCtx
		}
		if settings.OllamaNumPredict > 0 {
			extraParams["num_predict"] = settings.OllamaNumPredict
		}
		list = append(list, AiderModelSetting{
			Name:        model,
			ExtraParams: extraParams,
		})
	}

	if mergedData, err := yaml.Marshal(list); err == nil {
		_ = os.WriteFile(filename, mergedData, 0644)
	}

	return func() {
		if exists {
			_ = os.WriteFile(filename, backup, 0644)
		} else {
			_ = os.Remove(filename)
		}
	}
}

func appendOllamaPromptFooter(input string) string {
	footer := strings.TrimSpace(`
## Ollama Execution Footer

IMPORTANT: You are running locally. To optimize execution speed and stay within limits, be extremely concise. Avoid conversational chatter, explanations, or describing what tool you are about to call. Call your selected tools directly without writing introductory or wrap-up prose.

Continue using available tools until concrete pass criteria have been checked. Before finalizing, verify changed files, run relevant local checks when possible, and state PASS or FAIL with any failing package or test names.
`)
	if strings.Contains(input, footer) {
		return input
	}
	return strings.TrimRight(input, "\n") + "\n\n" + footer + "\n"
}

func runRunner(ctx context.Context, runner string, agentID string, agentName string, inputContent string, outputPath string, opts RunOptions, ch chan tea.Msg) (int, error) {
	return runRunnerWithSession(ctx, runner, agentID, agentName, inputContent, outputPath, opts, ch, nil)
}

func runRunnerWithSession(ctx context.Context, runner string, agentID string, agentName string, inputContent string, outputPath string, opts RunOptions, ch chan tea.Msg, codexThreadID *string) (int, error) {
	// Clear any sidecar .yaml output left from a previous run of the same cycle
	// so it cannot be mistaken for the current run's structured output.
	removeSidecarOutputYAML(outputPath)
	if runner == "manual" {
		manualMsg := fmt.Sprintf("Manual execution requested. Prompt written to input path. Run using your preferred tool and save results to %s.", outputPath)
		_ = os.WriteFile(outputPath, []byte(manualMsg), 0644)
		if ch != nil {
			sendExecutorMsg(ctx, ch, AgentProgressMsg{AgentID: agentID, LogLine: manualMsg})
		}
		return 0, nil
	}

	logOutFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return 1, fmt.Errorf("failed to open log file: %w", err)
	}
	defer logOutFile.Close()

	settings := config.LoadMergedSettings()
	if limit, ok := inputSizeLimitForRunner(runner, settings); ok && len([]rune(inputContent)) > limit {
		err := inputSizeGuardError(runner, len([]rune(inputContent)), limit)
		logOutFile.WriteString("Input Size Guard: " + err.Error() + "\n")
		if ch != nil {
			sendExecutorMsg(ctx, ch, AgentProgressMsg{AgentID: agentID, LogLine: "Error: " + err.Error()})
		}
		return 1, err
	}

	switch runner {
	case "aider", "ollama":
		return runAiderOrOllama(ctx, runner, agentID, inputContent, settings, ch, logOutFile)

	case "agy":
		return runAgy(ctx, agentID, inputContent, opts, ch, logOutFile)

	case "codex":
		return runCodex(ctx, agentID, inputContent, outputPath, settings, opts, ch, logOutFile, codexThreadID)

	default:
		unsupportedErr := fmt.Errorf("unsupported runner: %s", runner)
		logOutFile.WriteString(fmt.Sprintf("Error: %v\n", unsupportedErr))
		if ch != nil {
			sendExecutorMsg(ctx, ch, AgentProgressMsg{AgentID: agentID, LogLine: fmt.Sprintf("Error: %v", unsupportedErr)})
		}
		return 1, unsupportedErr
	}
}

// aiderQuietFlags are Aider CLI flags that suppress non-essential UI chrome,
// update checks, analytics, model-metadata warnings, shell-command suggestions,
// and fancy-input handling. They reduce the amount of CLI noise captured in the
// phase output log so it does not leak into fallback handoff summaries. They do
// not alter the model's capabilities or the content of its answer.
var aiderQuietFlags = []string{
	"--no-show-model-warnings",
	"--no-check-update",
	"--no-show-release-notes",
	"--analytics-disable",
	"--no-suggest-shell-commands",
	"--no-fancy-input",
}

// buildAiderArgs constructs the Aider CLI argument list for a phase run. It
// appends aiderQuietFlags to suppress non-essential CLI chrome so it does not
// leak into fallback handoff summaries, then forwards the model when set.
//
// Only the developer agent is permitted to modify repository files. All other
// agents (PM, QA, Recommender, and any custom agents) run with --dry-run so
// Aider displays any proposed edits in the output log without writing them to
// disk. This prevents non-developer phases from accidentally touching source
// files when the model suggests changes. The structured output contract is
// still captured because the handoff parser extracts inline YAML from the
// model's response text, not only from sidecar files.
func buildAiderArgs(agentID, promptFile, model string) []string {
	args := []string{
		"--message-file", promptFile,
		"--yes-always",
		"--no-auto-commits",
		"--no-dirty-commits",
		"--no-gitignore",
	}
	if agentID != "developer" {
		args = append(args, "--dry-run")
	}
	args = append(args, aiderQuietFlags...)
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

func runAiderOrOllama(ctx context.Context, runner string, agentID string, inputContent string, settings config.Settings, ch chan tea.Msg, logOutFile *os.File) (int, error) {
	cleanupGitignore := setupTemporaryGitignore()
	defer cleanupGitignore()
	reportsDir := filepath.Join(".cyclestone", "reports")
	_ = os.MkdirAll(reportsDir, 0755)
	promptFile := filepath.Join(reportsDir, fmt.Sprintf("%s-aider-prompt.txt", agentID))
	var cleanup func()
	if err := os.WriteFile(promptFile, []byte(inputContent), 0644); err != nil {
		// Fallback 1: Write to workspace root
		promptFile = fmt.Sprintf(".%s-aider-prompt.txt", agentID)
		if err2 := os.WriteFile(promptFile, []byte(inputContent), 0644); err2 == nil {
			cleanup = func() { _ = os.Remove(promptFile) }
		} else {
			// Fallback 2: System temp dir
			tmpFile, err3 := os.CreateTemp("", fmt.Sprintf("%s-aider-prompt-*.txt", agentID))
			if err3 != nil {
				return 1, fmt.Errorf("failed to create prompt file: %w (fallback errors: %v, %v)", err, err2, err3)
			}
			promptFile = tmpFile.Name()
			if _, err4 := tmpFile.Write([]byte(inputContent)); err4 != nil {
				tmpFile.Close()
				_ = os.Remove(promptFile)
				return 1, err4
			}
			tmpFile.Close()
			cleanup = func() { _ = os.Remove(promptFile) }
		}
	}
	if cleanup != nil {
		defer cleanup()
	}
	var model string
	if runner == "aider" {
		model = settings.AiderModel
	} else { // ollama
		model = settings.OllamaModel
		if !strings.Contains(model, "/") {
			model = "ollama_chat/" + model
		}
		cleanup := setupTemporaryAiderSettings(model, settings)
		defer cleanup()
	}
	args := buildAiderArgs(agentID, promptFile, model)
	cmd := exec.CommandContext(ctx, "aider", args...)
	cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	if runner == "ollama" {
		host := settings.OllamaHost
		if host == "" {
			host = "http://localhost:11434"
		}
		cmd.Env = append(cmd.Env, "OLLAMA_API_BASE="+host)
	}

	r, w := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w

	scanDone := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			logOutFile.WriteString(line + "\n")
			if ch != nil {
				sendExecutorMsg(ctx, ch, AgentProgressMsg{AgentID: agentID, LogLine: line})
			}
		}
		close(scanDone)
	}()

	runErr := cmd.Start()
	if runErr == nil {
		runErr = cmd.Wait()
	}
	w.Close()
	<-scanDone

	exitCode := 0
	if runErr != nil {
		exitCode = 1
		if exitError, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
		return exitCode, runErr
	}
	return 0, nil
}

func runAgy(ctx context.Context, agentID string, inputContent string, opts RunOptions, ch chan tea.Msg, logOutFile *os.File) (int, error) {
	args := []string{"--print", "-", "--print-timeout", "30m"}
	if opts.Unrestricted {
		args = append(args, "--dangerously-skip-permissions")
	} else {
		args = append(args, "--sandbox", "--dangerously-skip-permissions")
	}
	cmd := exec.CommandContext(ctx, "agy", args...)
	cmd.Stdin = strings.NewReader(inputContent)

	r, w := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w

	logOutFile.WriteString(fmt.Sprintf("$ %s %s\n\n", cmd.Path, strings.Join(cmd.Args[1:], " ")))

	scanDone := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			logOutFile.WriteString(line + "\n")
			if ch != nil {
				sendExecutorMsg(ctx, ch, AgentProgressMsg{AgentID: agentID, LogLine: line})
			}
		}
		close(scanDone)
	}()

	runErr := cmd.Start()
	if runErr == nil {
		runErr = cmd.Wait()
	}
	w.Close()
	<-scanDone

	exitCode := 0
	if runErr != nil {
		exitCode = 1
		if exitError, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
		return exitCode, runErr
	}
	return 0, nil
}

func runCodex(ctx context.Context, agentID string, inputContent string, outputPath string, settings config.Settings, opts RunOptions, ch chan tea.Msg, logOutFile *os.File, codexThreadID *string) (int, error) {
	enableResume := settings.EnableCodexSessionResume != nil && *settings.EnableCodexSessionResume && codexThreadID != nil
	cmd := buildCodexCommand(ctx, opts, enableResume, stringPtrValue(codexThreadID))
	cmd.Stdin = strings.NewReader(inputContent)

	r, w := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w

	logOutFile.WriteString(fmt.Sprintf("$ %s %s\n\n", cmd.Path, strings.Join(cmd.Args[1:], " ")))

	scanDone := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			logOutFile.WriteString(line + "\n")
			if ch != nil {
				sendExecutorMsg(ctx, ch, AgentProgressMsg{AgentID: agentID, LogLine: line})
			}
		}
		close(scanDone)
	}()

	runErr := cmd.Start()
	if runErr == nil {
		runErr = cmd.Wait()
	}
	w.Close()
	<-scanDone

	exitCode := 0
	if runErr != nil {
		exitCode = 1
		if exitError, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
		if enableResume && stringPtrValue(codexThreadID) != "" {
			logOutFile.WriteString("\n[Codex Resume] resume failed; retrying isolated codex exec.\n")
			fallbackCmd := buildCodexCommand(ctx, opts, false, "")
			fallbackCmd.Stdin = strings.NewReader(inputContent)
			r, w := io.Pipe()
			fallbackCmd.Stdout = w
			fallbackCmd.Stderr = w
			logOutFile.WriteString(fmt.Sprintf("$ %s %s\n\n", fallbackCmd.Path, strings.Join(fallbackCmd.Args[1:], " ")))
			scanDone := make(chan struct{})
			go func() {
				scanner := bufio.NewScanner(r)
				for scanner.Scan() {
					line := scanner.Text()
					logOutFile.WriteString(line + "\n")
					if ch != nil {
						sendExecutorMsg(ctx, ch, AgentProgressMsg{AgentID: agentID, LogLine: line})
					}
				}
				close(scanDone)
			}()
			fallbackErr := fallbackCmd.Start()
			if fallbackErr == nil {
				fallbackErr = fallbackCmd.Wait()
			}
			w.Close()
			<-scanDone
			if fallbackErr != nil {
				fallbackExit := 1
				if exitError, ok := fallbackErr.(*exec.ExitError); ok {
					fallbackExit = exitError.ExitCode()
				}
				return fallbackExit, fallbackErr
			}
			return 0, nil
		}
		return exitCode, runErr
	}
	if enableResume {
		if parsed := parseCodexThreadID(readFileString(outputPath)); parsed != "" && codexThreadID != nil {
			*codexThreadID = parsed
		}
	}
	return 0, nil
}
func prepareCycleEnvironment(opts RunOptions, state *config.State, milestone config.Milestone, reportsDir string) (cycleNum int, branchName string, previousReportPath string, reportPath string, metadataPath string, repos []git.RepoInfo, gitError error, err error) {
	cycleNum = state.GetMilestoneCycles(milestone.ID) + 1
	cyclePadded := fmt.Sprintf("%03d", cycleNum)
	settings := config.LoadMergedSettings()
	prefix := settings.DefaultGitBranchPrefix
	if prefix == "" {
		prefix = "cyclestone/milestones/"
	}
	branchName = fmt.Sprintf("%s%s", prefix, milestone.ID)
	if opts.NoBranchChange {
		if current, err := git.GetCurrentBranch(); err == nil {
			branchName = current
		} else {
			branchName = "main"
		}
	}

	// Determine previous report path
	if cycleNum > 1 {
		prevPadded := fmt.Sprintf("%03d", cycleNum-1)
		prevPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.yaml", milestone.ID, prevPadded))
		if _, err := os.Stat(prevPath); err == nil {
			previousReportPath = prevPath
		}
	}

	reportPath = filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.yaml", milestone.ID, cyclePadded))
	metadataPath = filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s-metadata.json", milestone.ID, cyclePadded))
	repos = git.GetTrackedRepos()

	// Setup Git branch or snapshot
	var snapshots []git.RepoBranchSnapshot
	if opts.NoBranchChange {
		snapshots, gitError = git.CaptureBranchSnapshotForRepos(repos)
		if gitError != nil {
			gitError = fmt.Errorf("failed to capture branch snapshot: %w", gitError)
		}
	} else {
		// Switch or create branches in root and subdirectories
		for _, repo := range repos {
			_ = git.CheckoutOrCreateBranchInDir(repo.Path, branchName)
		}
	}

	// Generate git context string
	gitContextStr := generateGitContextForRepos(milestone.ID, cycleNum, repos)

	// Build and write cycle metadata JSON
	metadata := CycleMetadata{
		MilestoneID:    milestone.ID,
		CycleNumber:    cycleNum,
		Timestamp:      time.Now().Format(time.RFC3339),
		BranchSnapshot: snapshots,
		GitContext:     gitContextStr,
	}

	if metadataBytes, err := json.MarshalIndent(metadata, "", "  "); err == nil {
		_ = os.WriteFile(metadataPath, metadataBytes, 0644)
	}

	return cycleNum, branchName, previousReportPath, reportPath, metadataPath, repos, gitError, nil
}

func initCycleLog(state *config.State, opts RunOptions, milestoneID string, cycleNum int, branchName string) {
	latestCommit, _ := git.GetLatestCommitHash()
	cycleLog := config.MilestoneCycleLog{
		CycleNumber: cycleNum,
		Timestamp:   time.Now(),
		Branch:      branchName,
		CommitHash:  latestCommit,
		Status:      "failed",
		UserNote:    opts.CycleNote,
		Actions:     []config.AgentActionLog{},
	}
	state.AddCycleLog(milestoneID, cycleLog)
	_ = config.SaveState(opts.StatePath, state)
}

func writeReportHeader(reportFile *os.File, milestoneID string, branchName string, cycleNum int, previousReportPath string, metadataPath string, opts RunOptions, gitError error) {
	cyclePadded := fmt.Sprintf("%03d", cycleNum)
	cycleMode := "initial"
	if cycleNum > 1 {
		cycleMode = "continuation"
	}

	fmt.Fprintf(reportFile, "milestone_id: %s\n", yamlQuote(milestoneID))
	fmt.Fprintf(reportFile, "started: %s\n", yamlQuote(time.Now().Format("2006-01-02 15:04:05 -0700")))
	fmt.Fprintf(reportFile, "root: %s\n", yamlQuote("."))
	fmt.Fprintf(reportFile, "branch: %s\n", yamlQuote(branchName))
	if opts.NoBranchChange {
		fmt.Fprintf(reportFile, "branch_changes: %s\n", yamlQuote("skipped by --no-branch-change"))
	} else {
		fmt.Fprintf(reportFile, "branch_changes: %s\n", yamlQuote("enabled"))
	}
	fmt.Fprintf(reportFile, "cycle: %s\n", yamlQuote(cyclePadded))
	fmt.Fprintf(reportFile, "cycle_mode: %s\n", yamlQuote(cycleMode))
	fmt.Fprintf(reportFile, "milestone_file: %s\n", yamlQuote(fmt.Sprintf(".cyclestone/milestones/%s.md", milestoneID)))
	fmt.Fprintf(reportFile, "summary_report: %s\n", yamlQuote(fmt.Sprintf(".cyclestone/reports/%s.md", milestoneID)))
	if previousReportPath != "" {
		fmt.Fprintf(reportFile, "previous_cycle_report: %s\n", yamlQuote(previousReportPath))
	}
	fmt.Fprintf(reportFile, "cycle_metadata: %s\n", yamlQuote(metadataPath))

	if strings.TrimSpace(opts.CycleNote) != "" {
		fmt.Fprintf(reportFile, "human_cycle_note: |-\n")
		writeReportDetailString(reportFile, strings.TrimSpace(opts.CycleNote)+"\n")
	}

	fmt.Fprintf(reportFile, "details: |-\n")
	writeReportDetailf(reportFile, "## Workflow\n\nExecuting PM -> Developer -> QA phases for cycle %s (%s).\n", cyclePadded, cycleMode)

	if gitError != nil {
		writeReportDetailf(reportFile, "\n### Git Configuration Error\n\n%v\n", gitError)
	}
}

func yamlQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}

func runAgentPipeline(ctx context.Context, pipeline []config.Agent, milestone config.Milestone, opts RunOptions, state *config.State, ch chan tea.Msg, reportsDir string, cycleNum int, previousReportPath string, metadataPath string, settings config.Settings, reportFile *os.File, codexThreadMetadataPath string, codexThreadID *string) (cycleStatus string, interrupted bool) {
	cycleStatus = "approved"
	cyclePadded := fmt.Sprintf("%03d", cycleNum)

	for _, agent := range pipeline {
		select {
		case <-ctx.Done():
			reportPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.yaml", milestone.ID, cyclePadded))
			sendExecutorMsg(ctx, ch, RunnerStatusMsg{
				MilestoneID:         milestone.ID,
				CycleNumber:         cycleNum,
				CycleStatus:         "cancelled",
				Phase:               agent.ID,
				AgentID:             agent.ID,
				ReportFile:          reportPath,
				LastError:           context.Canceled.Error(),
				NextSuggestedAction: "Return to details when ready or start another cycle.",
			})
			sendExecutorMsg(ctx, ch, CycleFinishedMsg{MilestoneID: milestone.ID, CycleNumber: cycleNum, Status: "failed", ReportFile: reportPath, Error: context.Canceled})
			return "failed", true
		default:
		}

		// If running a single agent, skip others
		if opts.SingleAgentID != "" && agent.ID != opts.SingleAgentID {
			continue
		}

		sendExecutorMsg(ctx, ch, AgentStartedMsg{AgentID: agent.ID})
		_ = reportFile.Sync()

		// 1. Assemble prompt context
		inputContent := assembleInputWithSettings(milestone, agent, cycleNum, opts, previousReportPath, metadataPath, settings, pipeline)
		runner := agent.RunnerBinary
		if runner == "" {
			runner = settings.DefaultLLM
		}
		if runner == "" {
			runner = "codex"
		}
		if runner == "ollama" {
			inputContent = appendOllamaPromptFooter(inputContent)
		}

		agentFileID := getAgentFileID(agent.ID, pipeline)
		inputPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s-%s-input.md", milestone.ID, cyclePadded, agentFileID))
		outputPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s-%s-output.log", milestone.ID, cyclePadded, agentFileID))

		_ = os.WriteFile(inputPath, []byte(inputContent), 0644)
		if ch != nil {
			sendExecutorMsg(ctx, ch, RunnerStatusMsg{
				MilestoneID:     milestone.ID,
				CycleNumber:     cycleNum,
				CycleStatus:     "running",
				Phase:           agent.ID,
				AgentID:         agent.ID,
				Runner:          runner,
				Model:           configuredModelForRunner(runner, settings),
				Mode:            runnerModeLabel(opts),
				ReportFile:      filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.yaml", milestone.ID, cyclePadded)),
				OutputFile:      outputPath,
				LatestCommand:   describeRunnerCommand(runner, opts),
				MaxModelCalls:   normalizedMaxModelCalls(settings),
				MaxTokenBudget:  normalizedMaxTokenBudget(settings),
				EstimatedTokens: estimateTextTokens(inputContent),
			})
		}

		actionStartTime := time.Now()
		exitCode, runErr := runRunnerWithSession(ctx, runner, agent.ID, agent.Name, inputContent, outputPath, opts, ch, codexThreadID)
		actionDuration := time.Since(actionStartTime)
		if *codexThreadID != "" {
			_ = writeCodexThreadMetadata(codexThreadMetadataPath, *codexThreadID)
		}
		handoffPath := phaseHandoffPath(reportsDir, milestone.ID, cyclePadded, agentFileID)
		writeHandoff := shouldWritePhaseHandoff(settings, agent.OutputContract)
		if writeHandoff {
			_ = writePhaseHandoff(ctx, settings, handoffPath, milestone.ID, cycleNum, agent.ID, agent.OutputContract, outputPath, settings.MaxHandoffChars, opts.CycleNote, runner)
		}

		if agent.ID == "recommender" {
			state.SetMilestoneRecommendation(milestone.ID, parseRecommendationScore(handoffPath))
		}

		if runner == "manual" {
			writeReportDetailf(reportFile, "\n## %s Phase (Manual Mode)\n\nPrompt written to `%s`. Complete manually and record logs.\n", agent.Name, inputPath)
		} else {
			reportBefore := currentFileSize(reportFile)
			writePhaseReportExcerpt(reportFile, agent.Name, outputPath, runner, exitCode, maxPhaseReportOutputChars)
			reportAfter := currentFileSize(reportFile)
			metrics := collectPhaseCostMetrics(inputContent, outputPath)
			metrics.ReportChars = reportAfter - reportBefore
			writePhaseCostMetrics(reportFile, metrics)
		}
		if *codexThreadID != "" {
			writeReportDetailf(reportFile, "- Codex thread metadata: `%s`\n", codexThreadMetadataPath)
		}
		if compactPhaseHandoffsEnabled(settings) {
			writeReportDetailf(reportFile, "- Handoff summary: `%s`\n", handoffPath)
		}
		if writeHandoff {
			if status, errors := phaseHandoffStatus(handoffPath); status == "invalid" {
				writeReportDetailf(reportFile, "\n### Output Contract Validation\n\n")
				for _, validationErr := range errors {
					writeReportDetailf(reportFile, "- %s\n", validationErr)
				}
			}
		}

		// Log action back to state
		state.UpdateLastCycleLog(milestone.ID, func(cl *config.MilestoneCycleLog) {
			cl.Actions = append(cl.Actions, config.AgentActionLog{
				AgentID:    agent.ID,
				Timestamp:  actionStartTime,
				ExitCode:   exitCode,
				InputFile:  inputPath,
				OutputFile: outputPath,
				Duration:   actionDuration.String(),
			})
		})
		_ = config.SaveState(opts.StatePath, state)

		sendExecutorMsg(ctx, ch, AgentCompletedMsg{AgentID: agent.ID, ExitCode: exitCode, Timestamp: time.Now(), OutputFile: outputPath})

		if exitCode != 0 {
			cycleStatus = "failed"
			if ch != nil {
				lastError := fmt.Sprintf("agent %s failed with exit code %d", agent.Name, exitCode)
				if runErr != nil {
					lastError = runErr.Error()
				}
				sendExecutorMsg(ctx, ch, RunnerStatusMsg{
					MilestoneID:         milestone.ID,
					CycleNumber:         cycleNum,
					CycleStatus:         "failed",
					Phase:               agent.ID,
					AgentID:             agent.ID,
					Runner:              runner,
					Model:               configuredModelForRunner(runner, settings),
					ReportFile:          filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.yaml", milestone.ID, cyclePadded)),
					OutputFile:          outputPath,
					LastError:           lastError,
					NextSuggestedAction: "Review the output log and rerun the cycle after fixing the failure.",
				})
			}
			writeReportDetailf(reportFile, "\n### Execution Stalled\n\nAgent %s failed with non-zero exit code %d. Execution pipeline stopped.\n", agent.Name, exitCode)
			if runErr != nil {
				writeReportDetailf(reportFile, "Error details: %v\n", runErr)
			}
			break
		}
		if writeHandoff {
			if status, errors := phaseHandoffStatus(handoffPath); status == "invalid" {
				// Only strict runners (codex/agy) reach here: Aider/Ollama bypass
				// strict contract validation inside writePhaseHandoff and never
				// persist an invalid handoff, so their status is never "invalid".
				cycleStatus = contractValidationCycleStatus(agent.ID, cycleStatus)
				if ch != nil {
					sendExecutorMsg(ctx, ch, RunnerStatusMsg{
						MilestoneID:         milestone.ID,
						CycleNumber:         cycleNum,
						CycleStatus:         cycleStatus,
						Phase:               agent.ID,
						AgentID:             agent.ID,
						Runner:              runner,
						Model:               configuredModelForRunner(runner, settings),
						ReportFile:          filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s.yaml", milestone.ID, cyclePadded)),
						OutputFile:          outputPath,
						LastError:           strings.Join(errors, "; "),
						NextSuggestedAction: "Review the output contract validation errors before approving this cycle.",
					})
				}
			} else if agent.ID == "qa" {
				if verdict := qaVerdictFromHandoff(handoffPath); verdict != "" {
					cycleStatus = applyQAVerdictToCycleStatus(verdict, cycleStatus)
				}
			}
		}
	}

	return cycleStatus, false
}

func runPostCycleChecks(ctx context.Context, milestone config.Milestone, repos []git.RepoInfo, opts RunOptions, metadataPath string, reportFile *os.File, cycleStatus string) string {
	var checkFailures int
	if cycleStatus == "approved" {
		checkDirs := milestone.Checks
		if len(checkDirs) == 0 {
			checkDirs = defaultPackageCheckDirsForRepos(repos)
		}
		for _, subdir := range checkDirs {
			if _, err := os.Stat(filepath.Join(subdir, "package.json")); err == nil {
				failures, logs := runChecksForPackage(ctx, subdir, subdir, reportFile)
				checkFailures += failures
				writeReportDetailString(reportFile, logs)
			}
		}
	}

	// Check branch snapshot policy if active
	if opts.NoBranchChange && cycleStatus == "approved" {
		var meta CycleMetadata
		if metadataBytes, err := os.ReadFile(metadataPath); err == nil {
			_ = json.Unmarshal(metadataBytes, &meta)
		}
		ok, description := git.VerifyBranchSnapshot(meta.BranchSnapshot)
		if !ok {
			checkFailures++
			writeReportDetailf(reportFile, "\n## Branch Policy Violation\n\n%s\n", description)
		} else {
			writeReportDetailf(reportFile, "\n## Branch Policy Check\n\nAll tracked repositories remained on their original branches.\n")
		}
	}

	if checkFailures > 0 {
		cycleStatus = "failed"
		writeReportDetailf(reportFile, "\n## Check Summary\n\n%d package check(s) or branch policy checks failed. Review details above.\n", checkFailures)
	} else if cycleStatus == "approved" {
		writeReportDetailf(reportFile, "\n## Check Summary\n\nAll package manager checks completed successfully.\n")
	}

	return cycleStatus
}

func runRecommenderPhase(ctx context.Context, pipeline []config.Agent, milestone config.Milestone, opts RunOptions, state *config.State, ch chan tea.Msg, reportsDir string, cycleNum int, reportPath string, settings config.Settings, reportFile *os.File, codexThreadID *string, codexThreadMetadataPath string) {
	cyclePadded := fmt.Sprintf("%03d", cycleNum)
	hasRecommenderInPipeline := false
	for _, agent := range pipeline {
		if agent.ID == "recommender" {
			hasRecommenderInPipeline = true
			break
		}
	}

	if ctx.Err() == nil && !hasRecommenderInPipeline {
		// Determine active runner
		activeRunner := ""
		for _, agent := range pipeline {
			if agent.RunnerBinary != "" && agent.RunnerBinary != "manual" {
				activeRunner = agent.RunnerBinary
				break
			}
		}
		if activeRunner == "" {
			activeRunner = settings.DefaultLLM
		}
		if activeRunner == "" {
			activeRunner = "codex"
		}

		// Prepare recommender prompt content
		recommenderPromptBody := resources.RecommenderPrompt
		if strings.HasPrefix(recommenderPromptBody, "---\n") || strings.HasPrefix(recommenderPromptBody, "---\r\n") {
			parts := strings.SplitN(recommenderPromptBody, "---", 3)
			if len(parts) >= 3 {
				recommenderPromptBody = strings.TrimSpace(parts[2])
			}
		}

		// Summarize the current report for the recommender
		_ = reportFile.Sync()
		latestCycleReportText := summarizeCycleReport(reportPath)

		var criteriaBuilder strings.Builder
		for _, criterion := range milestone.AcceptanceCriteria {
			criteriaBuilder.WriteString("- " + criterion + "\n")
		}

		absRoot, err := filepath.Abs(".")
		if err != nil {
			absRoot = "."
		}
		promptText := fmt.Sprintf("Repository root: %s\n\n%s", absRoot, recommenderPromptBody)
		promptText = strings.ReplaceAll(promptText, "{{MILESTONE_ID}}", milestone.ID)
		promptText = strings.ReplaceAll(promptText, "{{GOAL}}", milestone.Goal)
		promptText = strings.ReplaceAll(promptText, "{{ACCEPTANCE_CRITERIA}}", criteriaBuilder.String())
		promptText = strings.ReplaceAll(promptText, "{{LATEST_CYCLE_REPORT}}", latestCycleReportText)

		recommenderFileID := getAgentFileID("recommender", pipeline)
		recommenderLogPath := filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s-%s-output.log", milestone.ID, cyclePadded, recommenderFileID))

		if ch != nil {
			sendExecutorMsg(ctx, ch, RunnerStatusMsg{
				MilestoneID:     milestone.ID,
				CycleNumber:     cycleNum,
				CycleStatus:     "running",
				Phase:           "recommender",
				AgentID:         "recommender",
				Runner:          activeRunner,
				Model:           configuredModelForRunner(activeRunner, settings),
				Mode:            runnerModeLabel(opts),
				ReportFile:      reportPath,
				OutputFile:      recommenderLogPath,
				LatestCommand:   describeRunnerCommand(activeRunner, opts),
				MaxModelCalls:   normalizedMaxModelCalls(settings),
				MaxTokenBudget:  normalizedMaxTokenBudget(settings),
				EstimatedTokens: estimateTextTokens(promptText),
			})
			sendExecutorMsg(ctx, ch, AgentStartedMsg{AgentID: "recommender"})
		}
		exitCode, runErr := runRunnerWithSession(ctx, activeRunner, "recommender", "Recommender", promptText, recommenderLogPath, opts, ch, codexThreadID)
		if *codexThreadID != "" {
			_ = writeCodexThreadMetadata(codexThreadMetadataPath, *codexThreadID)
		}
		if ch != nil {
			sendExecutorMsg(ctx, ch, AgentCompletedMsg{AgentID: "recommender", ExitCode: exitCode, Timestamp: time.Now(), OutputFile: recommenderLogPath})
		}
		recommenderHandoffPath := phaseHandoffPath(reportsDir, milestone.ID, cyclePadded, recommenderFileID)
		writeHandoff := shouldWritePhaseHandoff(settings, "recommender")
		if writeHandoff {
			_ = writePhaseHandoff(ctx, settings, recommenderHandoffPath, milestone.ID, cycleNum, "recommender", "recommender", recommenderLogPath, settings.MaxHandoffChars, opts.CycleNote, activeRunner)
		}

		recommenderScore := parseRecommendationScore(recommenderHandoffPath)

		// Save recommendation score to state
		state.SetMilestoneRecommendation(milestone.ID, recommenderScore)

		// Append recommender details to the main cycle report
		writeReportDetailf(reportFile, "\n## Cycle Recommender Phase\n\n")
		if runErr != nil {
			writeReportDetailf(reportFile, "Execution failed: %v\n", runErr)
		} else {
			writeReportDetailf(reportFile, "Cycle Recommender execution succeeded.\n")
		}
		writeReportDetailf(reportFile, "Recommendation score: %d\n\n", recommenderScore)
		if compactPhaseHandoffsEnabled(settings) {
			writeReportDetailf(reportFile, "- Handoff summary: `%s`\n\n", recommenderHandoffPath)
		}
		if writeHandoff {
			if status, errors := phaseHandoffStatus(recommenderHandoffPath); status == "invalid" {
				writeReportDetailf(reportFile, "Output contract validation errors:\n")
				for _, validationErr := range errors {
					writeReportDetailf(reportFile, "- %s\n", validationErr)
				}
				writeReportDetailf(reportFile, "\n")
			}
		}
		reportBefore := currentFileSize(reportFile)
		writeLogExcerpt(reportFile, "### Recommender Output", recommenderLogPath, activeRunner, maxRecommenderReportOutputChars)
		reportAfter := currentFileSize(reportFile)
		metrics := collectPhaseCostMetrics(promptText, recommenderLogPath)
		metrics.ReportChars = reportAfter - reportBefore
		writePhaseCostMetrics(reportFile, metrics)
	}
}

func compactPhaseHandoffsEnabled(settings config.Settings) bool {
	return settings.EnableCompactPhaseHandoffs == nil || *settings.EnableCompactPhaseHandoffs
}

func shouldWritePhaseHandoff(settings config.Settings, outputContract string) bool {
	return compactPhaseHandoffsEnabled(settings) || strings.TrimSpace(outputContract) != ""
}

func setupTemporaryGitignore() func() {
	filename := ".gitignore"
	data, err := os.ReadFile(filename)
	if err != nil {
		return func() {}
	}

	backup := make([]byte, len(data))
	copy(backup, data)

	lines := strings.Split(string(data), "\n")
	modified := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ".cyclestone") || strings.HasPrefix(trimmed, ".aider") {
			lines[i] = "# " + line
			modified = true
		}
	}

	if modified {
		newData := strings.Join(lines, "\n")
		if err := os.WriteFile(filename, []byte(newData), 0644); err != nil {
			return func() {}
		}
		return func() {
			_ = os.WriteFile(filename, backup, 0644)
		}
	}

	return func() {}
}
