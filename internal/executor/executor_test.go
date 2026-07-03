package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	gitpkg "github.com/patrick-folster/cyclestone/internal/git"
	"gopkg.in/yaml.v3"
)

func TestUnsupportedRunnersAreRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	outputPath := filepath.Join(tmpDir, "output.log")
	for _, runner := range []string{"gemini", "openai", "anthropic", "ollama_api", "./runner.sh", "/tmp/runner"} {
		t.Run(runner, func(t *testing.T) {
			exitCode, runErr := runRunner(context.Background(), runner, "test-agent", "TestAgent", "prompt", outputPath, RunOptions{}, nil)
			if exitCode != 1 {
				t.Fatalf("expected exit code 1, got %d", exitCode)
			}
			if runErr == nil || !strings.Contains(runErr.Error(), "unsupported runner: "+runner) {
				t.Fatalf("expected unsupported runner error, got %v", runErr)
			}
		})
	}
}

func TestRunAgentPipelineCancellationDoesNotBlockWithoutListener(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	if err := os.MkdirAll(filepath.Join(".cyclestone", "reports"), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	reportFile, err := os.Create(filepath.Join(".cyclestone", "reports", "MS-CANCEL-cycle-001.yaml"))
	if err != nil {
		t.Fatalf("failed to create report file: %v", err)
	}
	defer reportFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	ch := make(chan tea.Msg)

	go func() {
		runAgentPipeline(
			ctx,
			[]config.Agent{{ID: "developer", Name: "Developer"}},
			config.Milestone{ID: "MS-CANCEL", Goal: "cancel cleanly"},
			RunOptions{},
			&config.State{},
			ch,
			filepath.Join(".cyclestone", "reports"),
			1,
			"",
			"",
			config.Settings{},
			reportFile,
			filepath.Join(".cyclestone", "reports", "thread.json"),
			new(string),
		)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected cancelled pipeline to return without a waiting executor listener")
	}
}
func TestCompactConversationHistoryBoundsOlderToolTurns(t *testing.T) {
	history := []UnifiedMessage{{Role: "user", Content: "initial prompt"}}
	for i := 0; i < 6; i++ {
		history = append(history,
			UnifiedMessage{
				Role: "assistant",
				ToolCalls: []UnifiedToolCall{{
					ID:        fmt.Sprintf("call-%d", i),
					Name:      "run_command",
					Arguments: fmt.Sprintf(`{"command":"echo %d"}`, i),
				}},
			},
			UnifiedMessage{
				Role:       "tool",
				ToolCallID: fmt.Sprintf("call-%d", i),
				ToolName:   "run_command",
				Content:    strings.Repeat("output", 100),
			},
		)
	}

	compacted := compactConversationHistory(history, 8)
	if len(compacted) > maxRetainedConversationMessages {
		t.Fatalf("expected compacted history <= %d messages, got %d", maxRetainedConversationMessages, len(compacted))
	}
	if compacted[0].Role != "user" || compacted[0].Content != "initial prompt" {
		t.Fatalf("expected initial prompt retained, got %#v", compacted[0])
	}
	if compacted[1].Role != "system" || !strings.Contains(compacted[1].Content, "Conversation history compacted") {
		t.Fatalf("expected compaction summary, got %#v", compacted[1])
	}
	if compacted[2].Role == "tool" {
		t.Fatalf("expected compacted suffix to start at a non-tool message, got %#v", compacted[2])
	}
	if compacted[len(compacted)-1].ToolCallID != "call-5" {
		t.Fatalf("expected recent tool result retained, got %#v", compacted[len(compacted)-1])
	}
}

func TestCompactConversationHistoryKeepsLargeToolBatchOwner(t *testing.T) {
	history := []UnifiedMessage{{Role: "user", Content: "initial prompt"}}
	for i := 0; i < 3; i++ {
		history = append(history,
			UnifiedMessage{
				Role: "assistant",
				ToolCalls: []UnifiedToolCall{{
					ID:        fmt.Sprintf("old-call-%d", i),
					Name:      "run_command",
					Arguments: fmt.Sprintf(`{"command":"echo old %d"}`, i),
				}},
			},
			UnifiedMessage{
				Role:       "tool",
				ToolCallID: fmt.Sprintf("old-call-%d", i),
				ToolName:   "run_command",
				Content:    "old output",
			},
		)
	}

	var calls []UnifiedToolCall
	for i := 0; i < maxRetainedConversationMessages; i++ {
		calls = append(calls, UnifiedToolCall{
			ID:        fmt.Sprintf("batch-call-%d", i),
			Name:      "run_command",
			Arguments: fmt.Sprintf(`{"command":"echo batch %d"}`, i),
		})
	}
	history = append(history, UnifiedMessage{Role: "assistant", ToolCalls: calls})
	for i := range calls {
		history = append(history, UnifiedMessage{
			Role:       "tool",
			ToolCallID: calls[i].ID,
			ToolName:   calls[i].Name,
			Content:    "batch output",
		})
	}

	compacted := compactConversationHistory(history, 8)
	var ownerIdx = -1
	for i, msg := range compacted {
		if msg.Role == "assistant" && len(msg.ToolCalls) == len(calls) {
			ownerIdx = i
			break
		}
	}
	if ownerIdx < 0 {
		t.Fatalf("expected large tool batch owner assistant retained, got %#v", compacted)
	}
	for i := 0; i < len(calls); i++ {
		idx := ownerIdx + 1 + i
		if idx >= len(compacted) || compacted[idx].Role != "tool" || compacted[idx].ToolCallID != calls[i].ID {
			t.Fatalf("expected tool result %s after owner assistant, got %#v", calls[i].ID, compacted)
		}
	}
}
func TestOllamaPromptFooterScopedToOllamaRunner(t *testing.T) {
	base := "prompt"
	ollama := appendOllamaPromptFooter(base)
	if !strings.Contains(ollama, "## Ollama Execution Footer") {
		t.Fatalf("expected ollama footer")
	}
	if appendOllamaPromptFooter(ollama) != ollama {
		t.Fatalf("expected footer append to be idempotent")
	}
}

func TestSetupTemporaryAiderSettingsWritesOllamaParams(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	cleanup := setupTemporaryAiderSettings("ollama_chat/qwen3-coder:480b-cloud", config.Settings{
		OllamaNumCtx:     32768,
		OllamaNumPredict: 4096,
	})
	defer cleanup()

	data, err := os.ReadFile(".aider.model.settings.yml")
	if err != nil {
		t.Fatalf("failed to read temporary aider model settings: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "num_ctx: 32768") {
		t.Fatalf("expected num_ctx in temporary aider model settings, got:\n%s", text)
	}
	if !strings.Contains(text, "num_predict: 4096") {
		t.Fatalf("expected num_predict in temporary aider model settings, got:\n%s", text)
	}
}

func TestCodexRunnerRejectsOversizedInputBeforeExec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor_test_codex_size")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("USERPROFILE", oldUserProfile)
		_ = os.Chdir(oldWd)
	}()

	if err := config.SaveProjectSettings(config.Settings{MaxLLMInputChars: 1000}); err != nil {
		t.Fatalf("failed to save project settings: %v", err)
	}

	outputPath := filepath.Join(tmpDir, "output.log")
	input := strings.Repeat("x", 1001)

	exitCode, runErr := runRunner(context.Background(), "codex", "test-agent", "TestAgent", input, outputPath, RunOptions{}, nil)
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 for oversized codex input, got %d", exitCode)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "above codex safety limit") {
		t.Fatalf("expected input size guard error, got %v", runErr)
	}

	outBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output log: %v", err)
	}
	if !strings.Contains(string(outBytes), "Input Size Guard") {
		t.Fatalf("expected input size guard log, got: %s", string(outBytes))
	}
}

func TestMilestoneCreationRejectsOversizedLLMInputBeforeExec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor_test_creator_size")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("USERPROFILE", oldUserProfile)
		_ = os.Chdir(oldWd)
	}()

	if err := config.SaveProjectSettings(config.Settings{MaxLLMInputChars: 1000}); err != nil {
		t.Fatalf("failed to save project settings: %v", err)
	}

	ch := make(chan tea.Msg, 1)
	prompt := strings.Repeat("x", 1001)

	ExecuteMilestoneCreation(context.Background(), "codex", prompt, RunOptions{}, ch, "MS-SIZE", "Oversized")

	msg := <-ch
	finished, ok := msg.(CreateMilestoneFinishedMsg)
	if !ok {
		t.Fatalf("expected CreateMilestoneFinishedMsg, got %T", msg)
	}
	if finished.Error == nil || !strings.Contains(finished.Error.Error(), "above codex safety limit") {
		t.Fatalf("expected input size guard error, got %v", finished.Error)
	}
}

func TestMilestoneCreationRejectsUnsupportedRunner(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	ch := make(chan tea.Msg, 1)
	ExecuteMilestoneCreation(context.Background(), "gemini", "prompt", RunOptions{}, ch, "MS-UNSUPPORTED", "Unsupported")

	msg := <-ch
	finished, ok := msg.(CreateMilestoneFinishedMsg)
	if !ok {
		t.Fatalf("expected CreateMilestoneFinishedMsg, got %T", msg)
	}
	if finished.Error == nil || !strings.Contains(finished.Error.Error(), "unsupported runner: gemini") {
		t.Fatalf("expected unsupported runner error, got %v", finished.Error)
	}
}

func TestWriteGitContextAndDefaultChecksUseDiscoveredRepos(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "executor_git_context_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	if err := exec.Command("git", "init").Run(); err != nil {
		t.Fatalf("failed to run git init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join("services", "api"), 0755); err != nil {
		t.Fatalf("failed to create discovered repo dir: %v", err)
	}
	if err := os.WriteFile("package.json", []byte(`{"scripts":{}}`), 0644); err != nil {
		t.Fatalf("failed to write root package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join("services", "api", "package.json"), []byte(`{"scripts":{}}`), 0644); err != nil {
		t.Fatalf("failed to write service package.json: %v", err)
	}
	if err := os.WriteFile(".gitmodules", []byte(`[submodule "api"]
	path = services/api
	url = https://example.invalid/api.git
`), 0644); err != nil {
		t.Fatalf("failed to write .gitmodules: %v", err)
	}

	oldConfigPath := gitpkg.ConfigPath
	gitpkg.ConfigPath = "missing-milestone.yml"
	defer func() { gitpkg.ConfigPath = oldConfigPath }()

	outputPath := filepath.Join(tmpDir, "git-context.md")
	if err := writeGitContext(outputPath, "MS-X", 2); err != nil {
		t.Fatalf("writeGitContext failed: %v", err)
	}
	contentBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read git context: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "## root") {
		t.Fatalf("expected root section in git context:\n%s", content)
	}
	if !strings.Contains(content, "## services/api") {
		t.Fatalf("expected discovered repo section in git context:\n%s", content)
	}
	if strings.Contains(content, "## backend") || strings.Contains(content, "## frontend") {
		t.Fatalf("did not expect legacy backend/frontend sections:\n%s", content)
	}

	got := defaultPackageCheckDirs()
	want := []string{".", filepath.Join("services", "api")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected default package check dirs:\n got: %v\nwant: %v", got, want)
	}
}

func TestGitContextAndDefaultChecksForReposUseProvidedRepos(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "executor_git_context_for_repos_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	if err := exec.Command("git", "init").Run(); err != nil {
		t.Fatalf("failed to run git init: %v", err)
	}
	if err := os.WriteFile("package.json", []byte(`{"scripts":{}}`), 0644); err != nil {
		t.Fatalf("failed to write root package.json: %v", err)
	}
	if err := os.MkdirAll("configured-extra", 0755); err != nil {
		t.Fatalf("failed to create configured-extra: %v", err)
	}
	if err := os.WriteFile(filepath.Join("configured-extra", "package.json"), []byte(`{"scripts":{}}`), 0644); err != nil {
		t.Fatalf("failed to write configured-extra package.json: %v", err)
	}
	if err := os.MkdirAll("nongit-configured", 0755); err != nil {
		t.Fatalf("failed to create nongit-configured: %v", err)
	}
	if err := os.WriteFile("milestone.yml", []byte(`
repositories:
  - configured-extra
`), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	if err := os.WriteFile(".gitmodules", []byte(`[submodule "missing"]
	path = missing-submodule
	url = https://example.invalid/missing.git
`), 0644); err != nil {
		t.Fatalf("failed to write .gitmodules: %v", err)
	}

	oldConfigPath := gitpkg.ConfigPath
	gitpkg.ConfigPath = "milestone.yml"
	defer func() { gitpkg.ConfigPath = oldConfigPath }()

	repos := []gitpkg.RepoInfo{
		{Label: "root", Path: "."},
		{Label: "nongit-configured", Path: "nongit-configured"},
		{Label: "missing-submodule", Path: "missing-submodule"},
	}
	outputPath := filepath.Join(tmpDir, "git-context.md")
	if err := writeGitContextForRepos(outputPath, "MS-Y", 3, repos); err != nil {
		t.Fatalf("writeGitContextForRepos failed: %v", err)
	}

	contentBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read git context: %v", err)
	}
	content := string(contentBytes)
	for _, expected := range []string{
		"## root",
		"## nongit-configured",
		"No git worktree found at nongit-configured.",
		"## missing-submodule",
		"No git worktree found at missing-submodule.",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("expected %q in git context:\n%s", expected, content)
		}
	}
	if strings.Contains(content, "## configured-extra") {
		t.Fatalf("writeGitContextForRepos rediscovered configured-extra:\n%s", content)
	}

	got := defaultPackageCheckDirsForRepos(repos)
	want := []string{"."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected default package check dirs:\n got: %v\nwant: %v", got, want)
	}
}

func TestAssembleInputLimitsPreviousCycleReport(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "executor_input_limit_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatalf("failed to create milestone dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-LIMIT.md"), []byte("# MS-LIMIT\n"), 0644); err != nil {
		t.Fatalf("failed to write milestone spec: %v", err)
	}

	previousReportPath := filepath.Join(".cyclestone", "reports", "MS-LIMIT-cycle-001.yaml")
	if err := os.MkdirAll(filepath.Dir(previousReportPath), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	previousReport := strings.Join([]string{
		"# Milestone Cycle Report: MS-LIMIT",
		"- Started: 2026-06-23 18:26:57 -0600",
		"- Branch: cyclestone/milestones/0001-limit",
		"- Cycle: 001",
		"- Cycle mode: initial",
		"",
		"## Project Manager Phase",
		"```text",
		strings.Repeat("REPORT-MIDDLE\n", 30000),
		"Exit status: 0",
		"```",
		"",
		"## Quality Manager Phase",
		"```text",
		"R required QA input missing",
		"O blocked",
		"Exit status: 0",
		"```",
		"",
		"## Human Review Steps",
		"1. Review the prior report.",
		"5. Confirm QA verdict and unresolved issues.",
	}, "\n")
	if err := os.WriteFile(previousReportPath, []byte(previousReport), 0644); err != nil {
		t.Fatalf("failed to write previous report: %v", err)
	}

	input := assembleInput(
		config.Milestone{ID: "MS-LIMIT", Goal: "limit previous report"},
		config.Agent{ID: "pm", Name: "Project Manager", PromptBody: "role prompt"},
		2,
		RunOptions{NoBranchChange: true},
		previousReportPath,
		"",
	)

	if len([]rune(input)) > maxPreviousCycleSummaryChars+20000 {
		t.Fatalf("expected previous report summary to be bounded, got input length %d", len([]rune(input)))
	}
	if !strings.Contains(input, "## Previous Cycle Summary") {
		t.Fatalf("expected previous cycle summary heading in input")
	}
	if !strings.Contains(input, "Source report: "+previousReportPath) {
		t.Fatalf("expected previous report source path in input")
	}
	if strings.Contains(input, "REPORT-MIDDLE") {
		t.Fatalf("expected noisy report body to be omitted from summary")
	}
	if !strings.Contains(input, "R required QA input missing") || !strings.Contains(input, "O blocked") {
		t.Fatalf("expected QA continuation signals to be retained")
	}
	if !strings.Contains(input, "5. Confirm QA verdict and unresolved issues.") {
		t.Fatalf("expected human review signal to be retained")
	}
}

func TestAssembleInputSummarizesCycleReportForRecommender(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "executor_recommender_input_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	reportPath := filepath.Join(".cyclestone", "reports", "MS-REC-cycle-002.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	report := strings.Join([]string{
		"# Milestone Cycle Report: MS-REC",
		"- Started: 2026-06-23 18:26:57 -0600",
		"- Cycle: 002",
		"- Cycle mode: continuation",
		"",
		"## Developer Phase",
		"```text",
		strings.Repeat("DEVELOPER-LOG-NOISE\n", 30000),
		"Exit status: 0",
		"```",
		"",
		"## Quality Manager Phase",
		"```text",
		"R final QA blocker",
		"O blocked",
		"Exit status: 0",
		"```",
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}

	input := assembleInput(
		config.Milestone{ID: "MS-REC", Goal: "summarize recommender report", AcceptanceCriteria: []string{"no huge report body"}},
		config.Agent{ID: "recommender", Name: "Cycle Recommender", PromptBody: "Report:\n{{LATEST_CYCLE_REPORT}}\nCriteria:\n{{ACCEPTANCE_CRITERIA}}"},
		2,
		RunOptions{},
		"",
		"",
	)

	if len([]rune(input)) > maxPreviousCycleSummaryChars+20000 {
		t.Fatalf("expected recommender report summary to be bounded, got input length %d", len([]rune(input)))
	}
	if strings.Contains(input, "DEVELOPER-LOG-NOISE") {
		t.Fatalf("expected noisy report body to be omitted from recommender input")
	}
	if !strings.Contains(input, "Source report: "+reportPath) {
		t.Fatalf("expected report source path in recommender input")
	}
	if !strings.Contains(input, "R final QA blocker") || !strings.Contains(input, "O blocked") {
		t.Fatalf("expected QA continuation signals in recommender input")
	}
}

func TestSanitizeRunnerLogForReportStripsCodexPromptEcho(t *testing.T) {
	logText := strings.Join([]string{
		"$ codex exec -- -",
		"OpenAI Codex v0.142.2",
		"user",
		"# Developer Phase Input",
		"VERY-LARGE-PROMPT-ECHO",
		"assistant",
		"O done",
		"tokens used",
		"123",
	}, "\n")

	got := sanitizeRunnerLogForReport(logText, "codex")
	if strings.Contains(got, "VERY-LARGE-PROMPT-ECHO") {
		t.Fatalf("expected codex prompt echo to be stripped, got:\n%s", got)
	}
	if !strings.HasPrefix(got, "assistant\n") {
		t.Fatalf("expected sanitized log to start at assistant output, got:\n%s", got)
	}

	unchanged := sanitizeRunnerLogForReport(logText, "ollama")
	if !strings.Contains(unchanged, "VERY-LARGE-PROMPT-ECHO") {
		t.Fatalf("expected non-codex logs to remain unchanged")
	}
}

func TestWritePhaseHandoffParsesYAMLOrFallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	yamlLog := filepath.Join(tmpDir, "pm.log")
	yamlHandoff := filepath.Join(tmpDir, "pm-handoff.yaml")
	if err := os.WriteFile(yamlLog, []byte("report\n```yaml\nscope:\n  - one\nrisks:\n  - low\n```\n"), 0644); err != nil {
		t.Fatalf("failed to write yaml log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, yamlHandoff, "MS-H", 1, "pm", "", yamlLog, 1000, "Test human comment", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff YAML failed: %v", err)
	}
	yamlBytes, err := os.ReadFile(yamlHandoff)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "milestone_id: MS-H") ||
		!strings.Contains(string(yamlBytes), "agent_id: pm") ||
		!strings.Contains(string(yamlBytes), "human_input: Test human comment") ||
		!strings.Contains(string(yamlBytes), "summary:") ||
		!strings.Contains(string(yamlBytes), "scope:") ||
		strings.Contains(string(yamlBytes), "fallback:") {
		t.Fatalf("expected parsed yaml handoff, got:\n%s", string(yamlBytes))
	}

	fallbackLog := filepath.Join(tmpDir, "custom.log")
	fallbackHandoff := filepath.Join(tmpDir, "custom-handoff.yaml")
	if err := os.WriteFile(fallbackLog, []byte("Verdict: blocked\nRequired fix: add tests\n"), 0644); err != nil {
		t.Fatalf("failed to write fallback log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, fallbackHandoff, "MS-H", 1, "custom", "", fallbackLog, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff fallback failed: %v", err)
	}
	fallbackBytes, err := os.ReadFile(fallbackHandoff)
	if err != nil {
		t.Fatalf("failed to read fallback handoff: %v", err)
	}
	if !strings.Contains(string(fallbackBytes), "fallback: true") ||
		!strings.Contains(string(fallbackBytes), "human_input: \"\"") ||
		!strings.Contains(string(fallbackBytes), "Required fix") {
		t.Fatalf("expected fallback summary, got:\n%s", string(fallbackBytes))
	}
}

func TestContractHandoffValidatesFinalFencedYAML(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	text := strings.Join([]string{
		"Earlier draft:",
		"```yaml",
		"changed_files:\n  - old\nimplemented_behavior: []\nchecks_run: []\ndecisions: []\nrisks: []",
		"```",
		"Final:",
		"```yaml",
		"changed_files:\n  - internal/executor/handoff.go\nimplemented_behavior:\n  - validated output\nchecks_run:\n  - go test\ndecisions:\n  - use final fence\nrisks: []",
		"```",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "developer", "developer", logPath, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	var handoff phaseHandoff
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if err := yaml.Unmarshal(data, &handoff); err != nil {
		t.Fatalf("failed to unmarshal handoff: %v", err)
	}
	if handoff.OutputContract != "developer" || handoff.ValidationStatus != "valid" {
		t.Fatalf("expected valid developer contract, got %#v", handoff)
	}
	files := handoff.Summary["changed_files"].([]interface{})
	if files[0] != "internal/executor/handoff.go" {
		t.Fatalf("expected final fenced block to win, got %#v", files)
	}
}

func TestAgentIDAloneDoesNotForceOutputContract(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	if err := os.WriteFile(logPath, []byte("custom developer output without structured YAML"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "developer", "", logPath, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	var handoff phaseHandoff
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if err := yaml.Unmarshal(data, &handoff); err != nil {
		t.Fatalf("failed to unmarshal handoff: %v", err)
	}
	if handoff.OutputContract != "" || handoff.ValidationStatus != "" || !handoff.Fallback {
		t.Fatalf("expected fallback without explicit output_contract, got %#v", handoff)
	}
}

func TestAiderOllamaBypassesMissingContractDocument(t *testing.T) {
	for _, runner := range []string{"aider", "ollama"} {
		t.Run(runner, func(t *testing.T) {
			tmpDir := t.TempDir()
			logPath := filepath.Join(tmpDir, "pm.log")
			handoffPath := filepath.Join(tmpDir, "pm-handoff.yaml")
			// No structured YAML document: just conversational Aider output.
			if err := os.WriteFile(logPath, []byte("I'll create a test milestone as requested.\nApplied edit to milestone.md\n"), 0644); err != nil {
				t.Fatalf("failed to write log: %v", err)
			}
			if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-B", 1, "pm", "pm", logPath, 1000, "", runner); err != nil {
				t.Fatalf("writePhaseHandoff failed: %v", err)
			}
			handoff, err := loadPhaseHandoff(handoffPath)
			if err != nil {
				t.Fatalf("failed to load handoff: %v", err)
			}
			if handoff.ValidationStatus == "invalid" || len(handoff.ValidationErrors) > 0 {
				t.Fatalf("expected bypassed fallback handoff without validation errors for %s, got status=%q errors=%#v", runner, handoff.ValidationStatus, handoff.ValidationErrors)
			}
			if handoff.OutputContract != "" {
				t.Fatalf("expected no output_contract on bypassed fallback handoff, got %q", handoff.OutputContract)
			}
			if !handoff.Fallback {
				t.Fatalf("expected fallback handoff for bypassed %s runner", runner)
			}
		})
	}
}

func TestStrictRunnerStillRecordsMissingContractDocument(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "pm.log")
	handoffPath := filepath.Join(tmpDir, "pm-handoff.yaml")
	if err := os.WriteFile(logPath, []byte("conversational output with no yaml"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-S", 1, "pm", "pm", logPath, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.ValidationStatus != "invalid" {
		t.Fatalf("expected invalid status for strict runner missing document, got %q", handoff.ValidationStatus)
	}
	if !strings.Contains(strings.Join(handoff.ValidationErrors, "\n"), "missing yaml document for output contract") {
		t.Fatalf("expected missing document error for strict runner, got %#v", handoff.ValidationErrors)
	}
}

func TestSidecarOutputYAMLSatisfiesContract(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	sidecarPath := strings.TrimSuffix(logPath, filepath.Ext(logPath)) + ".yaml"
	// The CLI log is mangled Aider display output with no clean fenced YAML.
	if err := os.WriteFile(logPath, []byte("$ aider ...\nApplied edit to developer-output.yaml\n"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	// The agent wrote a valid contract document to the sidecar file.
	sidecar := "changed_files:\n  - internal/executor/handoff.go\nimplemented_behavior:\n  - validated output\nchecks_run:\n  - go test\ndecisions: []\nrisks: []\n"
	if err := os.WriteFile(sidecarPath, []byte(sidecar), 0644); err != nil {
		t.Fatalf("failed to write sidecar: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-Y", 1, "developer", "developer", logPath, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.ValidationStatus != "valid" {
		t.Fatalf("expected valid status from sidecar yaml, got %q errors=%#v", handoff.ValidationStatus, handoff.ValidationErrors)
	}
	files := handoff.Summary["changed_files"].([]interface{})
	if files[0] != "internal/executor/handoff.go" {
		t.Fatalf("expected sidecar changed_files, got %#v", files)
	}
}

func TestAiderOllamaBypassCapturesInvalidContractDocument(t *testing.T) {
	for _, runner := range []string{"aider", "ollama"} {
		t.Run(runner, func(t *testing.T) {
			tmpDir := t.TempDir()
			logPath := filepath.Join(tmpDir, "developer.log")
			handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
			sidecarPath := strings.TrimSuffix(logPath, filepath.Ext(logPath)) + ".yaml"
			if err := os.WriteFile(logPath, []byte("$ aider ...\nApplied edit\n"), 0644); err != nil {
				t.Fatalf("failed to write log: %v", err)
			}
			// Contract document present but implemented_behavior is a string,
			// not an array of strings: a strict runner would record this invalid.
			sidecar := "changed_files: []\nimplemented_behavior: |\n  did the thing\nchecks_run: []\ndecisions: []\nrisks: []\n"
			if err := os.WriteFile(sidecarPath, []byte(sidecar), 0644); err != nil {
				t.Fatalf("failed to write sidecar: %v", err)
			}
			if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-P", 1, "developer", "developer", logPath, 1000, "", runner); err != nil {
				t.Fatalf("writePhaseHandoff failed: %v", err)
			}
			handoff, err := loadPhaseHandoff(handoffPath)
			if err != nil {
				t.Fatalf("failed to load handoff: %v", err)
			}
			if handoff.ValidationStatus == "invalid" || len(handoff.ValidationErrors) > 0 {
				t.Fatalf("expected bypassed handoff without validation errors for %s, got status=%q errors=%#v", runner, handoff.ValidationStatus, handoff.ValidationErrors)
			}
			// The output contract must still be set so the TUI details view can
			// render the structured fields for the bypassed handoff.
			if handoff.OutputContract != "developer" {
				t.Fatalf("expected output_contract to remain set for TUI rendering, got %q", handoff.OutputContract)
			}
			if _, ok := handoff.Summary["implemented_behavior"]; !ok {
				t.Fatalf("expected parsed summary to retain implemented_behavior, got %#v", handoff.Summary)
			}
		})
	}
}

func TestAiderOllamaBypassMalformedContractFallsBack(t *testing.T) {
	for _, runner := range []string{"aider", "ollama"} {
		t.Run(runner, func(t *testing.T) {
			tmpDir := t.TempDir()
			logPath := filepath.Join(tmpDir, "developer.log")
			handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
			// A fenced yaml block that is syntactically broken: extractable but
			// not parseable, so validation.Summary is nil while RawYAML is set.
			body := "draft\n```yaml\nchanged_files:\n  - [\n```\n"
			if err := os.WriteFile(logPath, []byte(body), 0644); err != nil {
				t.Fatalf("failed to write log: %v", err)
			}
			if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-M", 1, "developer", "developer", logPath, 1000, "", runner); err != nil {
				t.Fatalf("writePhaseHandoff failed for malformed bypass: %v", err)
			}
			handoff, err := loadPhaseHandoff(handoffPath)
			if err != nil {
				t.Fatalf("failed to load handoff: %v", err)
			}
			if handoff.ValidationStatus == "invalid" || len(handoff.ValidationErrors) > 0 {
				t.Fatalf("expected no validation errors for malformed bypass %s, got status=%q errors=%#v", runner, handoff.ValidationStatus, handoff.ValidationErrors)
			}
			if !handoff.Fallback {
				t.Fatalf("expected heuristic fallback handoff for malformed bypass %s, got %#v", runner, handoff)
			}
		})
	}
}

func TestContractHandoffReportsMalformedMissingAndWrongTypes(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name:     "malformed",
			body:     "```yaml\nchanged_files:\n  - [\n```",
			contains: "malformed yaml",
		},
		{
			name:     "missing",
			body:     "```yaml\nchanged_files: []\n```",
			contains: "missing required field \"implemented_behavior\"",
		},
		{
			name:     "wrong type",
			body:     "```yaml\nchanged_files: file\nimplemented_behavior: []\nchecks_run: []\ndecisions: []\nrisks: []\n```",
			contains: "field \"changed_files\" must be an array of strings",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAndValidateContract(tt.body, "developer")
			if result.Status != "invalid" {
				t.Fatalf("expected invalid status")
			}
			if !strings.Contains(strings.Join(result.Errors, "\n"), tt.contains) {
				t.Fatalf("expected error containing %q, got %#v", tt.contains, result.Errors)
			}
		})
	}
}

func TestPMContractValidationAcceptsYAMLSchema(t *testing.T) {
	body := strings.Join([]string{
		"scope:",
		"  - update prompts",
		"non_goals: []",
		"target_paths:",
		"  - resources/agents",
		"acceptance_map:",
		"  Agent Prompts Updated: |",
		"    Prompts require YAML output.",
		"risks:",
		"  - parser compatibility",
	}, "\n")
	result := parseAndValidateContract(body, "pm")
	if result.Status != "valid" {
		t.Fatalf("expected valid PM YAML contract, got %#v", result.Errors)
	}
}

func TestRecommenderContractRequiresVerdict(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name:     "missing verdict",
			body:     "```yaml\nscore: 1\nreason: complete\nnext_cycle_focus: []\n```",
			contains: "missing required field \"verdict\"",
		},
		{
			name:     "wrong verdict type",
			body:     "```yaml\nscore: 1\nverdict: true\nreason: complete\nnext_cycle_focus: []\n```",
			contains: "field \"verdict\" must be a string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAndValidateContract(tt.body, "recommender")
			if result.Status != "invalid" {
				t.Fatalf("expected invalid status")
			}
			if !strings.Contains(strings.Join(result.Errors, "\n"), tt.contains) {
				t.Fatalf("expected error containing %q, got %#v", tt.contains, result.Errors)
			}
		})
	}
}

func TestRecommenderContractAcceptsYAMLIntegerScore(t *testing.T) {
	body := strings.Join([]string{
		"score: 1",
		"verdict: approved",
		"reason: complete",
		"next_cycle_focus: []",
	}, "\n")
	result := parseAndValidateContract(body, "recommender")
	if result.Status != "valid" {
		t.Fatalf("expected valid recommender YAML contract, got %#v", result.Errors)
	}
}

func TestRecommenderHandoffPersistsMissingVerdictValidationError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "recommender.log")
	handoffPath := filepath.Join(tmpDir, "recommender-handoff.yaml")
	body := "```yaml\nscore: 1\nreason: complete\nnext_cycle_focus: []\n```"
	if err := os.WriteFile(logPath, []byte(body), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "recommender", "recommender", logPath, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.ValidationStatus != "invalid" {
		t.Fatalf("expected invalid validation status, got %q", handoff.ValidationStatus)
	}
	if !strings.Contains(strings.Join(handoff.ValidationErrors, "\n"), "missing required field \"verdict\"") {
		t.Fatalf("expected missing verdict validation error, got %#v", handoff.ValidationErrors)
	}
}

func TestExplicitContractHandoffSurvivesCompactHandoffsDisabled(t *testing.T) {
	disabled := false
	settings := config.Settings{EnableCompactPhaseHandoffs: &disabled}
	if !shouldWritePhaseHandoff(settings, "developer") {
		t.Fatalf("expected explicit output_contract to force handoff persistence")
	}
	if shouldWritePhaseHandoff(settings, "") {
		t.Fatalf("expected uncontracted fallback handoff to honor compact handoff disablement")
	}
}

func TestQAVerdictAndValidationStatusMapping(t *testing.T) {
	if got := applyQAVerdictToCycleStatus("approved", "approved"); got != "approved" {
		t.Fatalf("approved verdict changed status to %q", got)
	}
	if got := applyQAVerdictToCycleStatus("needs-human-review", "approved"); got != "blocked" {
		t.Fatalf("needs-human-review should block, got %q", got)
	}
	if got := contractValidationCycleStatus("developer", "approved"); got != "failed" {
		t.Fatalf("invalid developer output should fail, got %q", got)
	}
	if got := contractValidationCycleStatus("qa", "approved"); got != "blocked" {
		t.Fatalf("invalid QA output should block, got %q", got)
	}
}

func TestRecommenderScoreUsesStructuredHandoffOnly(t *testing.T) {
	tmpDir := t.TempDir()
	handoffPath := filepath.Join(tmpDir, "recommender-handoff.yaml")
	if err := os.WriteFile(handoffPath, []byte("summary:\n  score: 2\noutput_contract: recommender\nvalidation_status: valid\n"), 0644); err != nil {
		t.Fatalf("failed to write handoff: %v", err)
	}
	if got := parseRecommendationScore(handoffPath); got != 2 {
		t.Fatalf("expected structured score, got %d", got)
	}
	if err := os.WriteFile(handoffPath, []byte("summary: {}\noutput_contract: recommender\nvalidation_status: invalid\n"), 0644); err != nil {
		t.Fatalf("failed to write invalid handoff: %v", err)
	}
	if got := parseRecommendationScore(handoffPath); got != -1 {
		t.Fatalf("expected invalid handoff score to be unavailable, got %d", got)
	}
}

func TestExtractHandoffYAMLParsesMultilineFencedBlock(t *testing.T) {
	text := strings.Join([]string{
		"PM report",
		"```yaml",
		"scope:",
		"  - implement parser",
		"target_paths:",
		"  - internal/executor/executor.go",
		"risks: []",
		"```",
	}, "\n")

	parsed, ok := extractHandoffYAML(text)
	if !ok {
		t.Fatalf("expected multiline fenced YAML to parse")
	}
	var summary map[string]interface{}
	if err := yaml.Unmarshal(parsed, &summary); err != nil {
		t.Fatalf("expected valid YAML: %v", err)
	}
	if got := summary["scope"].([]interface{})[0]; got != "implement parser" {
		t.Fatalf("expected parsed scope, got %#v", got)
	}
}

func TestExtractHandoffYAMLSelectsLastValidHandoff(t *testing.T) {
	text := strings.Join([]string{
		"```yaml",
		"scope:\n  - old\nrisks:\n  - old",
		"```",
		"```text",
		`{"scope":["ignored text fence"]}`,
		"```",
		"```yaml",
		"changed_files:\n  - internal/executor/executor.go\nchecks_run:\n  - PASS",
		"```",
		"```yml",
		"verdict: approved\nrequired_fixes: []",
		"```",
	}, "\n")

	parsed, ok := extractHandoffYAML(text)
	if !ok {
		t.Fatalf("expected fenced YAML handoff to parse")
	}
	var summary map[string]interface{}
	if err := yaml.Unmarshal(parsed, &summary); err != nil {
		t.Fatalf("expected valid YAML: %v", err)
	}
	if _, ok := summary["changed_files"]; ok {
		t.Fatalf("expected later bare YAML handoff, got developer object: %s", string(parsed))
	}
	if got := summary["verdict"]; got != "approved" {
		t.Fatalf("expected last QA handoff, got: %s", string(parsed))
	}
}

func TestWritePhaseHandoffCapsFallbackSize(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(fmt.Sprintf("Changed file %03d: internal/executor/file_%03d.go %s\n", i, i, strings.Repeat("x", 160)))
		sb.WriteString(fmt.Sprintf("PASS test %03d: %s\n", i, strings.Repeat("y", 160)))
		sb.WriteString(fmt.Sprintf("Risk %03d: %s\n", i, strings.Repeat("z", 160)))
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-H", 1, "custom-developer", "", logPath, 12000, "Developer note", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff fallback failed: %v", err)
	}
	bytes, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if len([]rune(string(bytes))) > maxFallbackHandoffChars+2000 {
		t.Fatalf("expected fallback handoff to be capped, got %d chars", len([]rune(string(bytes))))
	}
	if !strings.Contains(string(bytes), "fallback: true") || !strings.Contains(string(bytes), "summary:") || !strings.Contains(string(bytes), "human_input: Developer note") {
		t.Fatalf("expected legacy-compatible fallback signal, got:\n%s", string(bytes))
	}
}

func TestCompactPhaseInputUsesRoleSpecificHandoffsAndSkipsRawPriorLogs(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "reports"), 0755); err != nil {
		t.Fatalf("failed to create reports: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatalf("failed to create milestones: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-P.md"), []byte("# Spec\nAC one\n"), 0644); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "AI_CONTEXT.md"), []byte("AI-CONTEXT-SHOULD-BE-PM-ONLY\n"), 0644); err != nil {
		t.Fatalf("failed to write AI context: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "reports", "MS-P-cycle-001.yaml"), []byte("RAW-PRIOR-LOG\n"+strings.Repeat("noise\n", 100)), 0644); err != nil {
		t.Fatalf("failed to write prior report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "reports", "MS-P-cycle-002-01-pm-handoff.yaml"), []byte("summary:\n  scope:\n    - pm scope\n  target_paths:\n    - internal/executor\n"), 0644); err != nil {
		t.Fatalf("failed to write pm handoff: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "reports", "MS-P-cycle-002-02-developer-handoff.yaml"), []byte("summary:\n  changed_files:\n    - internal/executor/executor.go\n  checks_run:\n    - PASS\n"), 0644); err != nil {
		t.Fatalf("failed to write dev handoff: %v", err)
	}

	pipeline := []config.Agent{
		{ID: "pm", Name: "PM"},
		{ID: "developer", Name: "Developer"},
		{ID: "qa", Name: "Quality Manager"},
	}

	settings := config.LoadDefaultSettings()
	devInput := assemblePhaseInput(
		config.Milestone{ID: "MS-P", Goal: "compact"},
		config.Agent{ID: "developer", Name: "Developer", PromptBody: "dev role"},
		2,
		RunOptions{NoBranchChange: true},
		filepath.Join(".cyclestone", "reports", "MS-P-cycle-001.yaml"),
		"",
		settings,
		pipeline,
	)
	if !strings.Contains(devInput, "## PM Handoff") || !strings.Contains(devInput, "pm scope") {
		t.Fatalf("expected developer input to include PM handoff, got:\n%s", devInput)
	}
	if strings.Contains(devInput, "RAW-PRIOR-LOG") || strings.Contains(devInput, "## QA Checklist") {
		t.Fatalf("expected developer input to skip raw prior logs and QA checklist, got:\n%s", devInput)
	}
	if !strings.Contains(devInput, "AI-CONTEXT-SHOULD-BE-PM-ONLY") {
		t.Fatalf("expected developer input to include AI context, got:\n%s", devInput)
	}

	qaInput := assemblePhaseInput(
		config.Milestone{ID: "MS-P", Goal: "compact"},
		config.Agent{ID: "qa", Name: "Quality Manager", PromptBody: "qa role"},
		2,
		RunOptions{NoBranchChange: true},
		"",
		"",
		settings,
		pipeline,
	)
	if !strings.Contains(qaInput, "## PM Handoff") || !strings.Contains(qaInput, "## Developer Handoff") || !strings.Contains(qaInput, "changed_files") {
		t.Fatalf("expected QA input to include compact handoffs, got:\n%s", qaInput)
	}
	if strings.Contains(qaInput, "RAW-PRIOR-LOG") {
		t.Fatalf("expected QA input to exclude prior report body, got:\n%s", qaInput)
	}
	if !strings.Contains(qaInput, "AI-CONTEXT-SHOULD-BE-PM-ONLY") {
		t.Fatalf("expected QA input to include AI context, got:\n%s", qaInput)
	}
}

func TestReadHandoffOrFallbackUsesBoundedOutputLogForMissingOrMalformedHandoff(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	reportsDir := filepath.Join(".cyclestone", "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	outputPath := filepath.Join(reportsDir, "MS-F-cycle-001-01-pm-output.log")
	if err := os.WriteFile(outputPath, []byte("PM-FALLBACK-HEAD\n"+strings.Repeat("noise\n", 100)+"PM-FALLBACK-TAIL\n"), 0644); err != nil {
		t.Fatalf("failed to write output log: %v", err)
	}

	missing := readHandoffOrFallback("MS-F", "001", "pm", 200, nil)
	if !strings.Contains(missing, "Handoff summary missing") || !strings.Contains(missing, "PM-FALLBACK-HEAD") || !strings.Contains(missing, "PM-FALLBACK-TAIL") {
		t.Fatalf("expected missing handoff to use bounded output log fallback, got:\n%s", missing)
	}

	handoffPath := filepath.Join(reportsDir, "MS-F-cycle-001-01-pm-handoff.yaml")
	if err := os.WriteFile(handoffPath, []byte("not: [valid"), 0644); err != nil {
		t.Fatalf("failed to write malformed handoff: %v", err)
	}
	malformed := readHandoffOrFallback("MS-F", "001", "pm", 200, nil)
	if !strings.Contains(malformed, "Handoff summary malformed") || strings.Contains(malformed, "not: [valid") {
		t.Fatalf("expected malformed handoff to use output log fallback, got:\n%s", malformed)
	}
}

func TestCodexThreadIDParseAndResumeCommandConstruction(t *testing.T) {
	jsonl := "{\"msg\":\"thread.started\",\"thread_id\":\"thread-123\"}\n{\"msg\":\"other\"}\n"
	if got := parseCodexThreadID(jsonl); got != "thread-123" {
		t.Fatalf("expected thread id, got %q", got)
	}

	startCmd := buildCodexCommand(context.Background(), RunOptions{}, true, "")
	startArgs := strings.Join(startCmd.Args, " ")
	if !strings.Contains(startArgs, "exec --json") || strings.Contains(startArgs, "resume") {
		t.Fatalf("unexpected start command args: %v", startCmd.Args)
	}

	resumeCmd := buildCodexCommand(context.Background(), RunOptions{}, true, "thread-123")
	resumeArgs := strings.Join(resumeCmd.Args, " ")
	if !strings.Contains(resumeArgs, "exec resume thread-123") || strings.Contains(resumeArgs, "--json") {
		t.Fatalf("unexpected resume command args: %v", resumeCmd.Args)
	}
}

func TestCodexSessionResumeWithFakeBinary(t *testing.T) {
	withFakeCodexTestDir(t, `#!/bin/sh
printf '%s\n' "$*" >> codex-args.log
if printf '%s\n' "$*" | grep -q -- '--json'; then
  echo '{"msg":"thread.started","thread_id":"thread-fake"}'
fi
echo 'assistant'
echo 'done'
`)

	trueVal := true
	if err := config.SaveProjectSettings(config.Settings{
		EnableCodexSessionResume: &trueVal,
		MaxLLMInputChars:         900000,
	}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	threadID := ""
	pmExit, pmErr := runRunnerWithSession(context.Background(), "codex", "pm", "Project Manager", "pm prompt", "pm.log", RunOptions{}, nil, &threadID)
	if pmExit != 0 || pmErr != nil {
		t.Fatalf("expected fake PM codex success, exit=%d err=%v", pmExit, pmErr)
	}
	if threadID != "thread-fake" {
		t.Fatalf("expected parsed thread id, got %q", threadID)
	}

	devExit, devErr := runRunnerWithSession(context.Background(), "codex", "developer", "Developer", "dev prompt", "dev.log", RunOptions{}, nil, &threadID)
	if devExit != 0 || devErr != nil {
		t.Fatalf("expected fake developer codex success, exit=%d err=%v", devExit, devErr)
	}

	argsBytes, err := os.ReadFile("codex-args.log")
	if err != nil {
		t.Fatalf("failed to read fake codex args: %v", err)
	}
	args := string(argsBytes)
	if !strings.Contains(args, "exec --json") || !strings.Contains(args, "exec resume thread-fake") {
		t.Fatalf("expected start and resume codex calls, got:\n%s", args)
	}
}

func TestCodexSessionResumeFallbackWithFakeBinary(t *testing.T) {
	withFakeCodexTestDir(t, `#!/bin/sh
printf '%s\n' "$*" >> codex-args.log
if printf '%s\n' "$*" | grep -q 'resume thread-fake'; then
  echo 'resume failed'
  exit 9
fi
echo 'assistant'
echo 'fallback ok'
`)

	trueVal := true
	if err := config.SaveProjectSettings(config.Settings{
		EnableCodexSessionResume: &trueVal,
		MaxLLMInputChars:         900000,
	}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	threadID := "thread-fake"
	exitCode, runErr := runRunnerWithSession(context.Background(), "codex", "developer", "Developer", "dev prompt", "dev.log", RunOptions{}, nil, &threadID)
	if exitCode != 0 || runErr != nil {
		t.Fatalf("expected resume fallback success, exit=%d err=%v", exitCode, runErr)
	}
	logBytes, err := os.ReadFile("dev.log")
	if err != nil {
		t.Fatalf("failed to read dev log: %v", err)
	}
	if !strings.Contains(string(logBytes), "[Codex Resume] resume failed; retrying isolated codex exec.") {
		t.Fatalf("expected fallback notice, got:\n%s", string(logBytes))
	}
	argsBytes, err := os.ReadFile("codex-args.log")
	if err != nil {
		t.Fatalf("failed to read fake codex args: %v", err)
	}
	args := string(argsBytes)
	if !strings.Contains(args, "exec resume thread-fake") || !strings.Contains(args, "exec --cd . --skip-git-repo-check -- -") {
		t.Fatalf("expected resume then isolated fallback calls, got:\n%s", args)
	}
}

func withFakeCodexTestDir(t *testing.T, script string) {
	t.Helper()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}
	codexPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake codex: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)
}

func TestWritePhaseReportExcerptBoundsLogContent(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "developer-output.log")
	reportPath := filepath.Join(tmpDir, "cycle.md")

	logText := strings.Join([]string{
		"$ codex exec -- -",
		"user",
		"# Developer Phase Input",
		strings.Repeat("PROMPT-ECHO\n", 1000),
		"assistant",
		strings.Repeat("ASSISTANT-OUTPUT\n", 1000),
		"tokens used",
		"123",
	}, "\n")
	if err := os.WriteFile(outputPath, []byte(logText), 0644); err != nil {
		t.Fatalf("failed to write output log: %v", err)
	}

	reportFile, err := os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to create report: %v", err)
	}
	writePhaseReportExcerpt(reportFile, "Developer", outputPath, "codex", 0, 2000)
	if err := reportFile.Close(); err != nil {
		t.Fatalf("failed to close report: %v", err)
	}

	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	report := string(reportBytes)
	if strings.Contains(report, "PROMPT-ECHO") {
		t.Fatalf("expected codex prompt echo to be omitted from report excerpt")
	}
	if !strings.Contains(report, "- Output log: `"+outputPath+"`") {
		t.Fatalf("expected report to link full output log, got:\n%s", report)
	}
	if !strings.Contains(report, "[Content truncated:") {
		t.Fatalf("expected bounded report excerpt truncation notice, got:\n%s", report)
	}
	if len([]rune(report)) > 3000 {
		t.Fatalf("expected bounded report size, got %d chars", len([]rune(report)))
	}
}

func TestCycleReportHeaderAndDetailsAreValidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "cycle.yaml")
	reportFile, err := os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to create report: %v", err)
	}
	writeReportHeader(reportFile, "MS-YAML", "develop", 1, "", ".cyclestone/reports/MS-YAML-cycle-001-metadata.json", RunOptions{NoBranchChange: true, CycleNote: "human note"}, nil)
	writeReportDetailf(reportFile, "\n## Developer Phase\n\n- Output log: `%s`\n", "developer.log")
	if err := reportFile.Close(); err != nil {
		t.Fatalf("failed to close report: %v", err)
	}

	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("expected generated cycle report to be valid YAML: %v\n%s", err, string(content))
	}
	if parsed["milestone_id"] != "MS-YAML" || parsed["cycle"] != "001" {
		t.Fatalf("unexpected YAML metadata: %#v", parsed)
	}
	details, ok := parsed["details"].(string)
	if !ok || !strings.Contains(details, "## Developer Phase") {
		t.Fatalf("expected report details block to preserve phase text, got %#v", parsed["details"])
	}
}

func TestPrepareCycleEnvironmentUsesYAMLReportPaths(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	reportsDir := filepath.Join(".cyclestone", "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reportsDir, "MS-YAML-cycle-001.yaml"), []byte("milestone_id: MS-YAML\n"), 0644); err != nil {
		t.Fatalf("failed to write previous report: %v", err)
	}

	state := &config.State{}
	state.SetMilestoneCycles("MS-YAML", 1)
	_, _, previousReportPath, reportPath, _, _, _, err := prepareCycleEnvironment(RunOptions{NoBranchChange: true}, state, config.Milestone{ID: "MS-YAML"}, reportsDir)
	if err != nil {
		t.Fatalf("prepareCycleEnvironment failed: %v", err)
	}
	if !strings.HasSuffix(previousReportPath, "MS-YAML-cycle-001.yaml") {
		t.Fatalf("expected previous YAML report path, got %q", previousReportPath)
	}
	if !strings.HasSuffix(reportPath, "MS-YAML-cycle-002.yaml") {
		t.Fatalf("expected current YAML report path, got %q", reportPath)
	}
}

func TestSummarizeCycleReportParsesYAMLEnvelope(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "MS-YAML-cycle-001.yaml")
	report := strings.Join([]string{
		`milestone_id: "MS-YAML"`,
		`started: "2026-07-02 10:00:00 -0500"`,
		`branch: "develop"`,
		`branch_changes: "skipped by --no-branch-change"`,
		`cycle: "001"`,
		`cycle_mode: "continuation"`,
		`details: |-`,
		`  ## Developer Phase`,
		``,
		`  - Exit status: 0`,
		``,
		`  ## QA Phase`,
		``,
		`  verdict: blocked`,
		`  unresolved: summary parser still scanned raw YAML`,
		``,
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}

	summary := summarizeCycleReport(reportPath)
	for _, expected := range []string{
		"milestone_id: MS-YAML",
		"started: 2026-07-02 10:00:00 -0500",
		"branch_changes: skipped by --no-branch-change",
		"Developer Phase",
		"QA Phase",
		"verdict: blocked",
		"unresolved: summary parser still scanned raw YAML",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("expected summary to contain %q, got:\n%s", expected, summary)
		}
	}
}

func TestUpdateCycleSummaryReportReadsYAMLReports(t *testing.T) {
	tmpDir := t.TempDir()
	reportsDir := filepath.Join(tmpDir, "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	reportPath := filepath.Join(reportsDir, "MS-YAML-cycle-001.yaml")
	report := strings.Join([]string{
		`milestone_id: "MS-YAML"`,
		`started: "2026-07-02 10:00:00 -0500"`,
		`details: |-`,
		`  ## QA Phase`,
		``,
		`  verdict: approved`,
		``,
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reportsDir, "MS-YAML-cycle-001-01-pm-handoff.yaml"), []byte("summary:\n  scope: []\n"), 0644); err != nil {
		t.Fatalf("failed to write handoff: %v", err)
	}

	if err := updateCycleSummaryReport("MS-YAML", 1, reportsDir); err != nil {
		t.Fatalf("updateCycleSummaryReport failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(reportsDir, "MS-YAML.md"))
	if err != nil {
		t.Fatalf("failed to read summary report: %v", err)
	}
	summary := string(content)
	if !strings.Contains(summary, ".cyclestone/reports/MS-YAML-cycle-001.yaml (2026-07-02 10:00:00 -0500) - verdict: approved") {
		t.Fatalf("expected YAML metadata and details verdict in cycle summary, got:\n%s", summary)
	}
	if strings.Contains(summary, "handoff.yaml") {
		t.Fatalf("expected handoff YAML to be excluded from cycle summary, got:\n%s", summary)
	}
}

func TestSummarizeCycleReportMalformedYAMLFallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "MS-YAML-cycle-001.yaml")
	report := strings.Join([]string{
		`milestone_id: [`,
		`details: |-`,
		`  ## QA Phase`,
		`  verdict: blocked`,
		``,
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}

	summary := summarizeCycleReport(reportPath)
	if !strings.Contains(summary, "malformed YAML report:") {
		t.Fatalf("expected malformed YAML warning, got:\n%s", summary)
	}
	if !strings.Contains(summary, "verdict: blocked") {
		t.Fatalf("expected fallback text scan to preserve continuation signal, got:\n%s", summary)
	}
}

func TestCollectPhaseCostMetricsParsesRunnerMetrics(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "output.log")
	logText := strings.Join([]string{
		"assistant output",
		"[Metrics] runner=ollama model=llama3 model_calls=3 output_chars=42 tool_calls=2 stop_or_done_reason=stop prompt_tokens=150 completion_tokens=80",
	}, "\n")
	if err := os.WriteFile(outputPath, []byte(logText), 0644); err != nil {
		t.Fatalf("failed to write output log: %v", err)
	}

	metrics := collectPhaseCostMetrics("abcd", outputPath)
	if metrics.InputChars != 4 || metrics.OutputChars != len([]rune(logText)) {
		t.Fatalf("unexpected char metrics: %#v", metrics)
	}
	if metrics.ModelCalls != 3 || metrics.ModelOutputChars != 42 || metrics.ToolCalls != 2 || metrics.StopOrDoneReason != "stop" {
		t.Fatalf("unexpected model metrics: %#v", metrics)
	}
	if metrics.EstimatedTokens != 1 {
		t.Fatalf("expected estimated tokens 1, got %d", metrics.EstimatedTokens)
	}
	if metrics.PromptTokens != 150 || metrics.CompletionTokens != 80 {
		t.Fatalf("expected prompt_tokens=150 completion_tokens=80, got prompt=%d completion=%d", metrics.PromptTokens, metrics.CompletionTokens)
	}
}

func TestWritePhaseCostMetricsOutputsActualTokens(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "report.md")
	f, err := os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to create report: %v", err)
	}
	defer f.Close()

	metrics := phaseCostMetrics{
		InputChars:       100,
		OutputChars:      50,
		ReportChars:      10,
		EstimatedTokens:  25,
		PromptTokens:     35,
		CompletionTokens: 15,
	}

	writePhaseCostMetrics(f, metrics)
	f.Close()

	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}

	contentStr := string(content)
	expectedLines := []string{
		"- Actual prompt tokens: 35",
		"- Actual completion tokens: 15",
		"- Actual total tokens: 50",
	}

	for _, expected := range expectedLines {
		if !strings.Contains(contentStr, expected) {
			t.Errorf("expected report to contain %q, but it did not. Content:\n%s", expected, contentStr)
		}
	}
}

func TestAssembleInputLimitsPromptFacingContextFiles(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "executor_prompt_file_limit_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatalf("failed to create milestone dir: %v", err)
	}
	largeContext := "CTX-HEAD\n" + strings.Repeat("A", 160000) + "CTX-MIDDLE-OMITTED\n" + strings.Repeat("Z", 160000) + "CTX-TAIL\n"
	if err := os.WriteFile(filepath.Join(".cyclestone", "AI_CONTEXT.md"), []byte(largeContext), 0644); err != nil {
		t.Fatalf("failed to write AI context: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-CONTEXT.md"), []byte("# MS-CONTEXT\n"), 0644); err != nil {
		t.Fatalf("failed to write milestone spec: %v", err)
	}

	input := assembleInput(
		config.Milestone{ID: "MS-CONTEXT", Goal: "limit context files"},
		config.Agent{ID: "pm", Name: "Project Manager", PromptBody: "role prompt"},
		1,
		RunOptions{},
		"",
		"",
	)

	if strings.Contains(input, "CTX-MIDDLE-OMITTED") {
		t.Fatalf("expected noisy context middle to be omitted")
	}
	if !strings.Contains(input, "CTX-HEAD") || !strings.Contains(input, "CTX-TAIL") {
		t.Fatalf("expected context head and tail to be retained")
	}
	if !strings.Contains(input, "[Content truncated: .cyclestone/AI_CONTEXT.md") {
		t.Fatalf("expected truncation notice for AI context")
	}
}

func TestAssembleInputScopesMilestoneStateAndIndex(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "executor_scoped_milestone_input_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatalf("failed to create milestone dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-ACTIVE.md"), []byte("# Milestone Spec: MS-ACTIVE\n"), 0644); err != nil {
		t.Fatalf("failed to write active milestone spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "MILESTONES.md"), []byte("MS-OTHER Other milestone roadmap\n"), 0644); err != nil {
		t.Fatalf("failed to write global milestones overview: %v", err)
	}

	statePath := filepath.Join(".cyclestone", "state.json")
	state := &config.State{
		ActiveMilestoneID: "MS-ACTIVE",
		MilestoneStatuses: map[string]string{
			"MS-ACTIVE": "Todo",
			"MS-OTHER":  "Approved",
		},
		MilestoneCycles: map[string]int{
			"MS-ACTIVE": 2,
			"MS-OTHER":  7,
		},
		MilestoneRecommendations: map[string]int{
			"MS-ACTIVE": 4,
			"MS-OTHER":  9,
		},
		History: map[string][]config.MilestoneCycleLog{
			"MS-ACTIVE": {
				{CycleNumber: 1, Branch: "cyclestone/milestones/0001-active", Status: "failed"},
			},
			"MS-OTHER": {
				{CycleNumber: 7, Branch: "cyclestone/milestones/0002-other", Status: "approved"},
			},
		},
	}
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatalf("failed to write state: %v", err)
	}

	input := assembleInput(
		config.Milestone{
			ID:       "MS-ACTIVE",
			Title:    "Active milestone only",
			SpecPath: "milestones/MS-ACTIVE.md",
			Goal:     "keep context scoped",
		},
		config.Agent{ID: "pm", Name: "Project Manager", PromptBody: "role prompt"},
		3,
		RunOptions{StatePath: statePath},
		"",
		"",
	)

	if !strings.Contains(input, "## Scoped Milestone Context") || !strings.Contains(input, "MS-ACTIVE") {
		t.Fatalf("expected scoped active milestone context in input")
	}
	if strings.Contains(input, "MS-OTHER") || strings.Contains(input, "Other milestone roadmap") || strings.Contains(input, "cyclestone/milestones/0002-other") {
		t.Fatalf("expected unrelated milestone data to be excluded:\n%s", input)
	}
}

func TestAssembleInputWorkspaceRootReplacement(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current wd: %v", err)
	}

	tmpDirRelative, err := os.MkdirTemp(".", "executor_workspace_root_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	tmpDir, err := filepath.Abs(tmpDirRelative)
	if err != nil {
		t.Fatalf("failed to get temp dir abs path: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatalf("failed to create milestone dir: %v", err)
	}

	aiContextContent := "Constraint: Keep work inside {{WORKSPACE_ROOT}}."
	if err := os.WriteFile(filepath.Join(".cyclestone", "AI_CONTEXT.md"), []byte(aiContextContent), 0644); err != nil {
		t.Fatalf("failed to write AI context: %v", err)
	}
	decisionsContent := "Decisions log at {{WORKSPACE_ROOT}}/decisions."
	if err := os.WriteFile(filepath.Join(".cyclestone", "DECISIONS.md"), []byte(decisionsContent), 0644); err != nil {
		t.Fatalf("failed to write decisions log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-TEST.md"), []byte("# MS-TEST\n"), 0644); err != nil {
		t.Fatalf("failed to write milestone spec: %v", err)
	}

	// Test 1: Non-compact layout
	compactDisabled := false
	settingsNonCompact := config.Settings{
		EnableCompactPhaseHandoffs: &compactDisabled,
	}
	inputNonCompact := assembleInputWithSettings(
		config.Milestone{ID: "MS-TEST", Goal: "test workspace root"},
		config.Agent{ID: "pm", Name: "Project Manager", PromptBody: "role prompt"},
		1,
		RunOptions{},
		"",
		"",
		settingsNonCompact,
		nil,
	)

	expectedAI := "Constraint: Keep work inside " + tmpDir + "."
	expectedDecisions := "Decisions log at " + tmpDir + "/decisions."

	if !strings.Contains(inputNonCompact, expectedAI) {
		t.Errorf("Non-compact: expected input to contain %q, but got:\n%s", expectedAI, inputNonCompact)
	}
	if !strings.Contains(inputNonCompact, expectedDecisions) {
		t.Errorf("Non-compact: expected input to contain %q, but got:\n%s", expectedDecisions, inputNonCompact)
	}

	// Test 2: Compact layout
	compactEnabled := true
	settingsCompact := config.Settings{
		EnableCompactPhaseHandoffs: &compactEnabled,
	}
	inputCompact := assembleInputWithSettings(
		config.Milestone{ID: "MS-TEST", Goal: "test workspace root"},
		config.Agent{ID: "pm", Name: "Project Manager", PromptBody: "role prompt"},
		1,
		RunOptions{},
		"",
		"",
		settingsCompact,
		nil,
	)

	if !strings.Contains(inputCompact, expectedAI) {
		t.Errorf("Compact: expected input to contain %q, but got:\n%s", expectedAI, inputCompact)
	}
	if !strings.Contains(inputCompact, expectedDecisions) {
		t.Errorf("Compact: expected input to contain %q, but got:\n%s", expectedDecisions, inputCompact)
	}

	// Test 3: Recommender agent (non-compact)
	inputRecommender := assembleInputWithSettings(
		config.Milestone{ID: "MS-TEST", Goal: "test workspace root"},
		config.Agent{ID: "recommender", Name: "Recommender", PromptBody: "role prompt"},
		1,
		RunOptions{},
		"",
		"",
		settingsNonCompact,
		nil,
	)

	if !strings.Contains(inputRecommender, expectedAI) {
		t.Errorf("Recommender (non-compact): expected input to contain %q, but got:\n%s", expectedAI, inputRecommender)
	}
	if !strings.Contains(inputRecommender, expectedDecisions) {
		t.Errorf("Recommender (non-compact): expected input to contain %q, but got:\n%s", expectedDecisions, inputRecommender)
	}

	// Test 4: Recommender agent (compact)
	inputRecommenderCompact := assembleInputWithSettings(
		config.Milestone{ID: "MS-TEST", Goal: "test workspace root"},
		config.Agent{ID: "recommender", Name: "Recommender", PromptBody: "role prompt"},
		1,
		RunOptions{},
		"",
		"",
		settingsCompact,
		nil,
	)

	if !strings.Contains(inputRecommenderCompact, expectedAI) {
		t.Errorf("Recommender (compact): expected input to contain %q, but got:\n%s", expectedAI, inputRecommenderCompact)
	}
	if !strings.Contains(inputRecommenderCompact, expectedDecisions) {
		t.Errorf("Recommender (compact): expected input to contain %q, but got:\n%s", expectedDecisions, inputRecommenderCompact)
	}
}

func TestLimitTextMiddleBoundsToolOutput(t *testing.T) {
	output := "OUT-HEAD\n" + strings.Repeat("A", 160000) + "OUT-MIDDLE-OMITTED\n" + strings.Repeat("Z", 160000) + "OUT-TAIL\n"
	limited := limitTextMiddle(output, maxToolOutputChars, "read_file output")

	if len([]rune(limited)) > maxToolOutputChars {
		t.Fatalf("expected limited output <= %d chars, got %d", maxToolOutputChars, len([]rune(limited)))
	}
	if strings.Contains(limited, "OUT-MIDDLE-OMITTED") {
		t.Fatalf("expected noisy middle to be omitted")
	}
	if !strings.Contains(limited, "OUT-HEAD") || !strings.Contains(limited, "OUT-TAIL") {
		t.Fatalf("expected output head and tail to be retained")
	}
	if !strings.Contains(limited, "[Content truncated: read_file output") {
		t.Fatalf("expected truncation notice")
	}
}

func TestHumanCycleNoteIntegration(t *testing.T) {
	opts := RunOptions{
		NoBranchChange: true,
		CycleNote:      "IMPORTANT NOTE: Fix the database connection string.",
	}
	milestone := config.Milestone{ID: "MS-1", Goal: "Goal string"}
	agentPM := config.Agent{ID: "pm", Name: "PM Agent", PromptBody: "PM instructions"}
	agentDev := config.Agent{ID: "developer", Name: "Developer Agent", PromptBody: "Dev instructions"}
	agentQA := config.Agent{ID: "qa", Name: "QA Agent", PromptBody: "QA instructions"}
	agentRec := config.Agent{ID: "recommender", Name: "Recommender Agent", PromptBody: "Rec instructions"}

	settings := config.LoadMergedSettings()

	inputPM := assembleInputWithSettings(milestone, agentPM, 1, opts, "", "", settings, []config.Agent{agentPM})
	if !strings.HasPrefix(inputPM, "# Human Cycle Note\n\nIMPORTANT NOTE: Fix the database connection string.\n\n---\n\n") {
		t.Errorf("expected inputPM to have prepended human note, got:\n%s", inputPM)
	}

	inputDev := assembleInputWithSettings(milestone, agentDev, 1, opts, "", "", settings, []config.Agent{agentDev})
	if !strings.HasPrefix(inputDev, "# Human Cycle Note\n\nIMPORTANT NOTE: Fix the database connection string.\n\n---\n\n") {
		t.Errorf("expected inputDev to have prepended human note, got:\n%s", inputDev)
	}

	inputQA := assembleInputWithSettings(milestone, agentQA, 1, opts, "", "", settings, []config.Agent{agentQA})
	if !strings.HasPrefix(inputQA, "# Human Cycle Note\n\nIMPORTANT NOTE: Fix the database connection string.\n\n---\n\n") {
		t.Errorf("expected inputQA to have prepended human note, got:\n%s", inputQA)
	}

	inputRec := assembleInputWithSettings(milestone, agentRec, 1, opts, "", "", settings, []config.Agent{agentRec})
	if strings.Contains(inputRec, "Human Cycle Note") {
		t.Errorf("expected inputRec to NOT contain human note, got:\n%s", inputRec)
	}

	phasePM := assemblePhaseInput(milestone, agentPM, 1, opts, "", "", settings, []config.Agent{agentPM})
	if !strings.HasPrefix(phasePM, "# Human Cycle Note\n\nIMPORTANT NOTE: Fix the database connection string.\n\n---\n\n") {
		t.Errorf("expected phasePM to have prepended human note, got:\n%s", phasePM)
	}
}

func TestCompactConversationHistorySelectiveRetention(t *testing.T) {
	history := []UnifiedMessage{
		{Role: "user", Content: "initial prompt"},
		{
			Role: "assistant",
			ToolCalls: []UnifiedToolCall{{
				ID:        "call-read-1",
				Name:      "read_file",
				Arguments: `{"path":"src/main.go"}`,
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call-read-1",
			ToolName:   "read_file",
			Content:    "fmt.Println(\"hello\")",
		},
		{
			Role: "assistant",
			ToolCalls: []UnifiedToolCall{{
				ID:        "call-write-1",
				Name:      "write_file",
				Arguments: `{"path":"src/helper.go", "content":"func helper() {}"}`,
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call-write-1",
			ToolName:   "write_file",
			Content:    "success",
		},
		{
			Role: "assistant",
			ToolCalls: []UnifiedToolCall{{
				ID:        "call-run-1",
				Name:      "run_command",
				Arguments: `{"command":"go test"}`,
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call-run-1",
			ToolName:   "run_command",
			Content:    "PASS",
		},
		{
			Role:    "assistant",
			Content: "recent assistant turn",
		},
		{
			Role:    "user",
			Content: "recent user query",
		},
	}

	compacted := compactConversationHistory(history, 6)

	// Since maxRetained = 6, retainedTail = 4.
	// We expect the compacted message list to contain:
	// 0: history[0] (user prompt)
	// 1: system compaction message (retaining the read_file / write_file contents)
	// 2..5: recent messages
	if len(compacted) > 6 {
		t.Fatalf("expected compacted size <= 6, got %d", len(compacted))
	}

	systemMsg := compacted[1]
	if systemMsg.Role != "system" {
		t.Fatalf("expected compacted[1] to be system message, got role=%s", systemMsg.Role)
	}

	// Verify read_file content is retained
	if !strings.Contains(systemMsg.Content, "File: src/main.go") || !strings.Contains(systemMsg.Content, "fmt.Println(\"hello\")") {
		t.Errorf("expected system message to retain src/main.go content, got:\n%s", systemMsg.Content)
	}

	// Verify write_file content is retained
	if !strings.Contains(systemMsg.Content, "File: src/helper.go") || !strings.Contains(systemMsg.Content, "func helper() {}") {
		t.Errorf("expected system message to retain src/helper.go content, got:\n%s", systemMsg.Content)
	}

	// Verify run_command output is NOT in retained file contents
	if strings.Contains(systemMsg.Content, "File: go test") || strings.Contains(systemMsg.Content, "PASS\n---") {
		t.Errorf("expected system message NOT to retain run_command output in file list, got:\n%s", systemMsg.Content)
	}
}

func TestRemoveSidecarOutputYAMLClearsStaleFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "pm.log")
	sidecarPath := strings.TrimSuffix(logPath, filepath.Ext(logPath)) + ".yaml"
	if err := os.WriteFile(sidecarPath, []byte("scope: []\n"), 0644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	if _, err := os.Stat(sidecarPath); err != nil {
		t.Fatalf("sidecar should exist before cleanup: %v", err)
	}
	removeSidecarOutputYAML(logPath)
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Fatalf("expected sidecar removed, got err=%v", err)
	}
	// Removing a missing sidecar must not error.
	removeSidecarOutputYAML(logPath)
}

// --- Tests for inline YAML extraction and bullet normalization ---

func TestNormalizeBulletedYAMLConvertsBulletsToHyphens(t *testing.T) {
	raw := "scope:\n \u2022 first item\n \u2022 second item\nrisks: []\n"
	normalized := normalizeBulletedYAML([]byte(raw))
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
		t.Fatalf("normalized YAML failed to parse: %v", err)
	}
	scope, ok := decoded["scope"].([]interface{})
	if !ok {
		t.Fatalf("expected scope to be an array, got %T", decoded["scope"])
	}
	if len(scope) != 2 || scope[0] != "first item" || scope[1] != "second item" {
		t.Fatalf("unexpected scope values: %#v", scope)
	}
}

func TestNormalizeBulletedYAMLIgnoresBulletsInsideStrings(t *testing.T) {
	// A bullet inside a quoted string value should not be affected because it
	// is not the first non-whitespace character on its line.
	raw := "decisions:\n  - \"Use bullet \u2022 for emphasis\"\nrisks: []\n"
	normalized := normalizeBulletedYAML([]byte(raw))
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
		t.Fatalf("normalized YAML failed to parse: %v", err)
	}
	decisions, ok := decoded["decisions"].([]interface{})
	if !ok || len(decisions) != 1 {
		t.Fatalf("expected one decision, got %#v", decoded["decisions"])
	}
	if decisions[0] != "Use bullet \u2022 for emphasis" {
		t.Fatalf("expected bullet preserved in string value, got %#v", decisions[0])
	}
}

func TestScanInlineYAMLBlocksFindsYAMLWithoutFences(t *testing.T) {
	text := strings.Join([]string{
		"$ aider --model ollama_chat/glm-5.2:cloud",
		"Applied edit to milestone.md",
		"",
		"scope:",
		"  - implement parser",
		"non_goals: []",
		"target_paths:",
		"  - internal/executor/executor.go",
		"risks: []",
		"",
		"Tokens: 64k sent, 1.8k received.",
	}, "\n")

	blocks := scanInlineYAMLBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 inline block, got %d", len(blocks))
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalizeBulletedYAML([]byte(strings.TrimSpace(blocks[0].text))), &decoded); err != nil {
		t.Fatalf("inline block failed to parse: %v", err)
	}
	if !hasKnownHandoffKey(decoded) {
		t.Fatalf("expected known handoff keys in parsed block, got %#v", decoded)
	}
	scope, ok := decoded["scope"].([]interface{})
	if !ok || len(scope) != 1 || scope[0] != "implement parser" {
		t.Fatalf("unexpected scope: %#v", decoded["scope"])
	}
}

func TestScanInlineYAMLBlocksFindsBlockWithBulletsAndBlankLines(t *testing.T) {
	// Simulates the real Aider CLI output pattern: keys at column 0, bullet
	// list items with blank lines between entries, trailing whitespace padding.
	text := "Aider reasoning here.\n\nscope:                          \n\n \u2022 No actions required\n\nnon_goals:                     \n\n \u2022 Do not make any code changes\n \u2022 Do not modify any files\n\ntarget_paths: []\n\nrisks: []\n\nTokens: 64k sent.\n"

	blocks := scanInlineYAMLBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 inline block, got %d: %#v", len(blocks), blocks)
	}
	normalized := normalizeBulletedYAML([]byte(strings.TrimSpace(blocks[0].text)))
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
		t.Fatalf("inline block with bullets failed to parse: %v", err)
	}
	scope, ok := decoded["scope"].([]interface{})
	if !ok || len(scope) != 1 || scope[0] != "No actions required" {
		t.Fatalf("expected scope array with one item, got %#v", decoded["scope"])
	}
	nonGoals, ok := decoded["non_goals"].([]interface{})
	if !ok || len(nonGoals) != 2 {
		t.Fatalf("expected non_goals array with two items, got %#v", decoded["non_goals"])
	}
}

func TestScanInlineYAMLBlocksFindsMultipleBlocks(t *testing.T) {
	text := strings.Join([]string{
		"reasoning about the task",
		"scope:",
		"  - draft item",
		"risks: []",
		"",
		"More reasoning here.",
		"",
		"changed_files:",
		"  - executor.go",
		"checks_run: []",
		"",
		"Done.",
	}, "\n")

	blocks := scanInlineYAMLBlocks(text)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 inline blocks, got %d", len(blocks))
	}
}

func TestExtractFinalYAMLDocumentFindsInlineBlock(t *testing.T) {
	text := strings.Join([]string{
		"$ aider ...",
		"Applied edit",
		"",
		"verdict: blocked",
		"criteria_results:",
		"  - criterion: test",
		"    result: fail",
		"reviewed_files:",
		"  - executor.go",
		"failing_checks:",
		"  - something broke",
		"required_fixes: []",
		"",
		"Tokens: 64k sent.",
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected inline extraction, got error: %v", err)
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted inline YAML failed to parse: %v", err)
	}
	if verdict, ok := decoded["verdict"].(string); !ok || verdict != "blocked" {
		t.Fatalf("expected verdict=blocked, got %#v", decoded["verdict"])
	}
}

func TestExtractHandoffYAMLFindsInlineBlockWithBullets(t *testing.T) {
	text := strings.Join([]string{
		"PM reasoning about the milestone.",
		"",
		"scope:",
		" \u2022 implement parser",
		"non_goals:",
		" \u2022 no code changes",
		"target_paths: []",
		"acceptance_map: {}",
		"risks: []",
		"",
		"Tokens: 64k sent.",
	}, "\n")

	parsed, ok := extractHandoffYAML(text)
	if !ok {
		t.Fatalf("expected inline YAML handoff to parse")
	}
	var summary map[string]interface{}
	if err := yaml.Unmarshal(parsed, &summary); err != nil {
		t.Fatalf("expected valid YAML: %v", err)
	}
	scope, ok := summary["scope"].([]interface{})
	if !ok || len(scope) != 1 || scope[0] != "implement parser" {
		t.Fatalf("expected scope array with one item after normalization, got %#v", summary["scope"])
	}
}

// TestExtractHandoffYAMLFindsInlineBlockWithFlattenedBlockScalar verifies the
// no-contract extraction path (extractHandoffYAML) also correctly handles
// flattened block-scalar content, not just the contract path
// (extractFinalYAMLDocument). This path is used by custom agents without an
// output_contract.
func TestExtractHandoffYAMLFindsInlineBlockWithFlattenedBlockScalar(t *testing.T) {
	text := strings.Join([]string{
		"Recommender reasoning.",
		"",
		"reason: |",
		"",
		"The cycle was approved with no issues.",
		"next_cycle_focus: []",
		"",
		"Tokens: 10k sent.",
	}, "\n")

	parsed, ok := extractHandoffYAML(text)
	if !ok {
		t.Fatalf("expected inline YAML handoff with block scalar to parse")
	}
	var summary map[string]interface{}
	if err := yaml.Unmarshal(parsed, &summary); err != nil {
		t.Fatalf("expected valid YAML: %v\nparsed:\n%s", err, string(parsed))
	}
	reason, _ := summary["reason"].(string)
	if !strings.Contains(reason, "The cycle was approved") {
		t.Fatalf("expected reason to contain flattened content, got %q", reason)
	}
	if focus, ok := summary["next_cycle_focus"].([]interface{}); !ok || len(focus) != 0 {
		t.Fatalf("expected next_cycle_focus=[], got %#v", summary["next_cycle_focus"])
	}
}

func TestParseAndValidateContractParsesInlineBulletedYAML(t *testing.T) {
	text := strings.Join([]string{
		"$ aider --model ollama_chat/glm-5.2:cloud",
		"Applied edit",
		"",
		"changed_files: []",
		"implemented_behavior:",
		" \u2022 No changes were made as requested.",
		"checks_run: []",
		"decisions:",
		" \u2022 Honored the no-action goal.",
		"risks: []",
		"",
		"Tokens: 65k sent.",
	}, "\n")

	result := parseAndValidateContract(text, "developer")
	if result.Status != "valid" {
		t.Fatalf("expected valid developer contract from inline bulleted YAML, got status=%q errors=%#v", result.Status, result.Errors)
	}
	behavior, ok := result.Summary["implemented_behavior"].([]interface{})
	if !ok || len(behavior) != 1 {
		t.Fatalf("expected implemented_behavior array with one item, got %#v", result.Summary["implemented_behavior"])
	}
}

func TestScanInlineYAMLBlocksIgnoresProseWithColons(t *testing.T) {
	// Lines that contain colons in prose should not be treated as key lines
	// unless they start with a known handoff key at column 0.
	text := strings.Join([]string{
		"The scope: of this project is testing.",
		"We need to review the verdict: it should be approved.",
		"",
		"scope:",
		"  - testing",
		"risks: []",
	}, "\n")

	blocks := scanInlineYAMLBlocks(text)
	// Should only find the block starting at "scope:" (line 3, 0-indexed)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 inline block (ignoring prose), got %d", len(blocks))
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap([]byte(strings.TrimSpace(blocks[0].text)), &decoded); err != nil {
		t.Fatalf("block failed to parse: %v", err)
	}
	if _, ok := decoded["scope"]; !ok {
		t.Fatalf("expected scope key in parsed block, got %#v", decoded)
	}
}

func TestWritePhaseHandoffParsesInlineBulletedYAMLWithoutContract(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "pm.log")
	handoffPath := filepath.Join(tmpDir, "pm-handoff.yaml")
	// Simulates real Aider CLI output: YAML inline with bullet characters
	// and trailing whitespace padding, no markdown fences.
	text := strings.Join([]string{
		"$ aider --model ollama_chat/glm-5.2:cloud",
		"Applied edit",
		"",
		"scope:                          ",
		"",
		" \u2022 No actions required for this test milestone",
		"",
		"non_goals:                      ",
		"",
		" \u2022 Do not make any code changes",
		" \u2022 Do not modify any files",
		"",
		"target_paths: []",
		"",
		"acceptance_map: {}",
		"",
		"risks: []",
		"",
		"Tokens: 64k sent, 1.8k received.",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-I", 1, "pm", "", logPath, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.Fallback {
		t.Fatalf("expected non-fallback handoff from inline YAML, got fallback=true")
	}
	scope, ok := handoff.Summary["scope"].([]interface{})
	if !ok || len(scope) != 1 || scope[0] != "No actions required for this test milestone" {
		t.Fatalf("expected scope array with one string, got %#v", handoff.Summary["scope"])
	}
}

func TestWritePhaseHandoffAiderParsesInlineBulletedContractYAML(t *testing.T) {
	for _, runner := range []string{"aider", "ollama"} {
		t.Run(runner, func(t *testing.T) {
			tmpDir := t.TempDir()
			logPath := filepath.Join(tmpDir, "developer.log")
			handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
			text := strings.Join([]string{
				"$ aider --model ollama_chat/glm-5.2:cloud",
				"Applied edit",
				"",
				"changed_files: []",
				"",
				"implemented_behavior:",
				"",
				" \u2022 No changes were made as the milestone requires no actions.",
				"",
				"checks_run: []",
				"",
				"decisions:",
				"",
				" \u2022 Honored the no-action goal.",
				"",
				"risks: []",
				"",
				"Tokens: 65k sent, 1.0k received.",
			}, "\n")
			if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
				t.Fatalf("failed to write log: %v", err)
			}
			if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-A", 1, "developer", "developer", logPath, 1000, "", runner); err != nil {
				t.Fatalf("writePhaseHandoff failed: %v", err)
			}
			handoff, err := loadPhaseHandoff(handoffPath)
			if err != nil {
				t.Fatalf("failed to load handoff: %v", err)
			}
			if handoff.Fallback {
				t.Fatalf("expected non-fallback handoff for %s, got fallback=true", runner)
			}
			if handoff.OutputContract != "developer" {
				t.Fatalf("expected output_contract=developer, got %q", handoff.OutputContract)
			}
			behavior, ok := handoff.Summary["implemented_behavior"].([]interface{})
			if !ok || len(behavior) != 1 {
				t.Fatalf("expected implemented_behavior array with one item, got %#v", handoff.Summary["implemented_behavior"])
			}
		})
	}
}

func TestBuildAiderArgsIncludesQuietFlags(t *testing.T) {
	args := buildAiderArgs("pm", "prompt.txt", "ollama_chat/glm-5.2:cloud")
	// Core run flags are preserved.
	if !sliceHas(args, "--message-file", "prompt.txt") {
		t.Fatalf("expected --message-file prompt.txt, got %v", args)
	}
	if !sliceHas(args, "--yes-always") || !sliceHas(args, "--no-auto-commits") || !sliceHas(args, "--no-dirty-commits") || !sliceHas(args, "--no-gitignore") {
		t.Fatalf("expected core run flags, got %v", args)
	}
	// Quiet flags suppress CLI chrome that leaks into fallback handoffs.
	for _, flag := range aiderQuietFlags {
		if !sliceHas(args, flag) {
			t.Fatalf("expected quiet flag %q in args, got %v", flag, args)
		}
	}
	// Model is forwarded when provided.
	if !sliceHas(args, "--model", "ollama_chat/glm-5.2:cloud") {
		t.Fatalf("expected --model forwarded, got %v", args)
	}
}

func TestBuildAiderArgsNeverDisablesRepoMap(t *testing.T) {
	// --map-tokens 0 must never be used: it disables the repo map, which the
	// developer (and other agents) rely on to understand the codebase.
	for _, agentID := range []string{"pm", "developer", "qa", "recommender"} {
		args := buildAiderArgs(agentID, "prompt.txt", "ollama_chat/glm-5.2:cloud")
		for _, v := range args {
			if v == "--map-tokens" {
				t.Fatalf("--map-tokens must never be set for %q, got %v", agentID, args)
			}
		}
	}
}

func TestBuildAiderArgsOmitsModelWhenEmpty(t *testing.T) {
	args := buildAiderArgs("pm", "prompt.txt", "")
	for _, v := range args {
		if v == "--model" {
			t.Fatalf("expected no --model when model is empty, got %v", args)
		}
	}
}

// sliceHas reports whether the given values appear consecutively in args.
func sliceHas(args []string, values ...string) bool {
	for i := 0; i+len(values) <= len(args); i++ {
		match := true
		for j, v := range values {
			if args[i+j] != v {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestAnswerRegionSlicesAfterLastAnswerMarker(t *testing.T) {
	text := "Aider v0.86.2\n► THINKING\n\nsome reasoning\n► ANSWER\n\nNo changes are needed.\n"
	got := answerRegion(text)
	if !strings.Contains(got, "No changes are needed.") {
		t.Fatalf("expected answer text after marker, got %q", got)
	}
	if strings.Contains(got, "Aider v0.86.2") || strings.Contains(got, "some reasoning") {
		t.Fatalf("expected chrome/reasoning stripped, got %q", got)
	}
}

func TestAnswerRegionUsesLastAnswerMarker(t *testing.T) {
	text := "► ANSWER\nfirst answer\n► ANSWER\nsecond answer\n"
	got := answerRegion(text)
	if !strings.Contains(got, "second answer") || strings.Contains(got, "first answer") {
		t.Fatalf("expected text after the last marker, got %q", got)
	}
}

func TestAnswerRegionReturnsFullTextWhenNoMarker(t *testing.T) {
	text := "just prose\nno marker\n"
	if got := answerRegion(text); got != text {
		t.Fatalf("expected full text when no marker, got %q", got)
	}
}

func TestFallbackHandoffExcludesCLIChromeAndEmitsEmptyContractFields(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	// Simulates a real Aider log: CLI chrome + reasoning before ► ANSWER, then
	// a prose answer with no structured YAML document.
	text := strings.Join([]string{
		"Aider v0.86.2",
		"Model: ollama_chat/glm-5.2:cloud with whole edit format",
		"Git repo: .git with 69 files",
		"► THINKING",
		"",
		"The milestone says no code changes.",
		"► ANSWER",
		"",
		"No code changes are needed based on the milestone goal.",
		"Tokens: 8.1k sent, 262 received.",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "developer", "developer", logPath, 1000, "", "aider"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if !handoff.Fallback {
		t.Fatalf("expected fallback handoff, got %#v", handoff)
	}
	// Contract fields must be clean empty arrays, not CLI chrome.
	changed, ok := handoff.Summary["changed_files"].([]interface{})
	if !ok || len(changed) != 0 {
		t.Fatalf("expected empty changed_files, got %#v", handoff.Summary["changed_files"])
	}
	behavior, ok := handoff.Summary["implemented_behavior"].([]interface{})
	if !ok || len(behavior) != 0 {
		t.Fatalf("expected empty implemented_behavior, got %#v", handoff.Summary["implemented_behavior"])
	}
	// The model's actual answer is preserved in the note, not the chrome.
	note, _ := handoff.Summary["note"].(string)
	if !strings.Contains(note, "No code changes are needed") {
		t.Fatalf("expected note to contain the model answer, got %q", note)
	}
	if strings.Contains(note, "Aider v0.86.2") || strings.Contains(note, "Git repo") || strings.Contains(note, "glm-5.2:cloud") {
		t.Fatalf("expected note to exclude CLI chrome, got %q", note)
	}
}

func TestRecommenderFallbackOmitsScore(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "recommender.log")
	handoffPath := filepath.Join(tmpDir, "recommender-handoff.yaml")
	text := "► ANSWER\n\nI cannot evaluate without clarification.\n"
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-R", 1, "recommender", "recommender", logPath, 1000, "", "aider"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	// A numeric score must not be fabricated: -1 means "no recommendation",
	// which is correct when the recommender produced no structured output.
	if got := parseRecommendationScore(handoffPath); got != -1 {
		t.Fatalf("expected -1 (no recommendation) for recommender fallback, got %d", got)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if _, hasScore := handoff.Summary["score"]; hasScore {
		t.Fatalf("expected score to be absent from recommender fallback, got %#v", handoff.Summary["score"])
	}
}

// --- Tests for block-scalar-aware inline YAML extraction ---

// TestScanInlineYAMLBlocksCapturesFlattenedBlockScalarContent verifies that the
// inline scanner does not split a YAML document when a block scalar indicator
// (|) is followed by content at column 0 — the pattern Aider's CLI display
// produces when it strips block-scalar indentation. Before the fix, the scanner
// broke the document at the non-indented content lines, and only the last
// fragment (e.g. "next_cycle_focus: []") survived, silently discarding score,
// verdict, and reason. This uses the exact recommender output pattern from
// milestone cycle 0001 run with ollama/glm-5.2:cloud.
func TestScanInlineYAMLBlocksCapturesFlattenedBlockScalarContent(t *testing.T) {
	// Simulates the Aider CLI answer region: keys at column 0, blank lines
	// between keys, and a block-scalar (reason: |) whose content Aider has
	// flattened to column 0 with trailing whitespace padding.
	text := strings.Join([]string{
		"► ANSWER                                                                      ",
		"",
		"score: 0                                                                      ",
		"",
		"verdict: approved                                                             ",
		"",
		"reason: |                                                                     ",
		"",
		"The QA agent approved the cycle with no failing checks or required fixes. There ",
		"were no acceptance criteria defined for this milestone, and the goal was simply ",
		"to create a test milestone without anything to do.                           ",
		"",
		"next_cycle_focus: []                                                          ",
		"",
		"Tokens: 10k sent, 881 received.",
	}, "\n")

	blocks := scanInlineYAMLBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 inline block (complete document), got %d blocks", len(blocks))
	}
	// The block must contain all four keys, not just the last fragment.
	blockText := blocks[0].text
	for _, key := range []string{"score:", "verdict:", "reason: |", "next_cycle_focus:"} {
		if !strings.Contains(blockText, key) {
			t.Fatalf("expected block to contain %q, got block:\n%s", key, blockText)
		}
	}
	// The flattened block-scalar content must be included in the block.
	if !strings.Contains(blockText, "The QA agent approved the cycle") {
		t.Fatalf("expected block to contain flattened reason content, got block:\n%s", blockText)
	}
}

// TestNormalizeFlattenedBlockScalarsReindentsColumnZeroContent verifies that
// the normalizer re-indents block-scalar content that Aider has flattened to
// column 0, producing valid YAML that the parser can decode with all fields
// intact.
func TestNormalizeFlattenedBlockScalarsReindentsColumnZeroContent(t *testing.T) {
	// The raw document as captured by the inline scanner: block-scalar content
	// is at column 0 (Aider flattened it).
	raw := strings.Join([]string{
		"score: 0",
		"",
		"verdict: approved",
		"",
		"reason: |",
		"",
		"The QA agent approved the cycle with no failing checks.",
		"There were no acceptance criteria defined for this milestone.",
		"",
		"next_cycle_focus: []",
	}, "\n")
	normalized := normalizeFlattenedBlockScalars([]byte(raw))
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
		t.Fatalf("normalized YAML failed to parse: %v\nnormalized:\n%s", err, string(normalized))
	}
	if score, ok := decoded["score"].(int); !ok || score != 0 {
		t.Fatalf("expected score=0, got %#v", decoded["score"])
	}
	if verdict, ok := decoded["verdict"].(string); !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", decoded["verdict"])
	}
	reason, _ := decoded["reason"].(string)
	if !strings.Contains(reason, "The QA agent approved the cycle") {
		t.Fatalf("expected reason to contain the flattened content, got %q", reason)
	}
	if focus, ok := decoded["next_cycle_focus"].([]interface{}); !ok || len(focus) != 0 {
		t.Fatalf("expected next_cycle_focus=[], got %#v", decoded["next_cycle_focus"])
	}
}

// TestNormalizeFlattenedBlockScalarsLeavesIndentedContentUntouched verifies
// that already-indented block-scalar content is not modified — only column-0
// content is re-indented.
func TestNormalizeFlattenedBlockScalarsLeavesIndentedContentUntouched(t *testing.T) {
	raw := strings.Join([]string{
		"reason: |",
		"  Already indented content.",
		"  More content.",
		"next_cycle_focus: []",
	}, "\n")
	normalized := normalizeFlattenedBlockScalars([]byte(raw))
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
		t.Fatalf("already-indented YAML failed to parse: %v", err)
	}
	reason, _ := decoded["reason"].(string)
	if !strings.Contains(reason, "Already indented content") {
		t.Fatalf("expected reason to contain indented content, got %q", reason)
	}
}

// TestExtractFinalYAMLDocumentCapturesRecommenderBlockScalar verifies the
// end-to-end extraction: extractFinalYAMLDocument on a recommender log with
// a flattened block scalar returns a document that parses with all fields
// (score, verdict, reason, next_cycle_focus).
func TestExtractFinalYAMLDocumentCapturesRecommenderBlockScalar(t *testing.T) {
	text := strings.Join([]string{
		"► ANSWER                                                                      ",
		"",
		"score: 0                                                                      ",
		"",
		"verdict: approved                                                             ",
		"",
		"reason: |                                                                     ",
		"",
		"The QA agent approved the cycle with no failing checks or required fixes. There ",
		"were no acceptance criteria defined for this milestone, and the goal was simply ",
		"to create a test milestone without anything to do.                           ",
		"",
		"next_cycle_focus: []                                                          ",
		"",
		"Tokens: 10k sent, 881 received.",
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected extraction to succeed, got error: %v", err)
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted YAML failed to parse: %v", err)
	}
	if score, ok := decoded["score"].(int); !ok || score != 0 {
		t.Fatalf("expected score=0, got %#v", decoded["score"])
	}
	if verdict, ok := decoded["verdict"].(string); !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", decoded["verdict"])
	}
	reason, _ := decoded["reason"].(string)
	if !strings.Contains(reason, "The QA agent approved") {
		t.Fatalf("expected reason to contain content, got %q", reason)
	}
	if focus, ok := decoded["next_cycle_focus"].([]interface{}); !ok || len(focus) != 0 {
		t.Fatalf("expected next_cycle_focus=[], got %#v", decoded["next_cycle_focus"])
	}
}

// TestWritePhaseHandoffOllamaRecommenderCapturesFlattenedBlockScalar is the
// end-to-end regression test: an ollama recommender log with a flattened block
// scalar must produce a handoff that retains score, verdict, reason, and
// next_cycle_focus. Before the fix, only next_cycle_focus survived and the
// recommendation score was silently lost (state.json showed -1 instead of 0).
func TestWritePhaseHandoffOllamaRecommenderCapturesFlattenedBlockScalar(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "recommender.log")
	handoffPath := filepath.Join(tmpDir, "recommender-handoff.yaml")
	// Exact pattern from the real ollama/glm-5.2:cloud recommender output in
	// cycle 0001: keys at column 0 with trailing Aider padding, block-scalar
	// content flattened to column 0.
	text := strings.Join([]string{
		"$ aider --model ollama_chat/glm-5.2:cloud",
		"Applied edit",
		"",
		"► ANSWER                                                                      ",
		"",
		"score: 0                                                                      ",
		"",
		"verdict: approved                                                             ",
		"",
		"reason: |                                                                     ",
		"",
		"The QA agent approved the cycle with no failing checks or required fixes. There ",
		"were no acceptance criteria defined for this milestone, and the goal was simply ",
		"to create a test milestone without anything to do.                           ",
		"",
		"next_cycle_focus: []                                                          ",
		"",
		"Tokens: 10k sent, 881 received.",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-REC", 1, "recommender", "recommender", logPath, 1000, "", "ollama"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	// The score must be captured so parseRecommendationScore returns 0,
	// not -1 (no recommendation).
	if got := parseRecommendationScore(handoffPath); got != 0 {
		t.Fatalf("expected recommendation score=0, got %d", got)
	}
	// verdict and reason must also be present.
	if verdict, ok := handoff.Summary["verdict"].(string); !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved in summary, got %#v", handoff.Summary["verdict"])
	}
	reason, _ := handoff.Summary["reason"].(string)
	if !strings.Contains(reason, "The QA agent approved") {
		t.Fatalf("expected reason to contain content, got %q", reason)
	}
	if handoff.Fallback {
		t.Fatalf("expected non-fallback handoff from structured YAML, got fallback=true")
	}
}

// --- Tests for nested block-scalar normalization (QA criteria_results pattern) ---

// TestNormalizeFlattenedBlockScalarsReindentsNestedSameIndentContent
// verifies that the normalizer re-indents block-scalar content that Aider's
// CLI display has flattened to the SAME indentation as the key — the pattern
// seen with nested block scalars inside list items (e.g. notes: | inside
// criteria_results). Before the fix, only column-0 content was re-indented;
// content at the same indent as the key was left untouched, causing a YAML
// parse error and silent loss of the entire QA handoff.
func TestNormalizeFlattenedBlockScalarsReindentsNestedSameIndentContent(t *testing.T) {
	// Simulates the Aider CLI output for a QA contract: the notes: | key is
	// at indent 3 (inside a criteria_results list item), and the block-scalar
	// content has been flattened to the same indent 3 by Aider's display.
	raw := strings.Join([]string{
		"verdict: approved",
		"",
		"criteria_results:",
		"",
		" - criterion: \"No code changes required\"",
		"   result: \"pass\"",
		"   notes: |",
		"   The milestone goal was to create a test milestone without any changes.",
		"   The developer made no changes.",
		"",
		"reviewed_files: []",
		"",
		"failing_checks: []",
		"",
		"required_fixes: []",
	}, "\n")
	normalized := normalizeFlattenedBlockScalars([]byte(raw))
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
		t.Fatalf("normalized YAML failed to parse: %v\nnormalized:\n%s", err, string(normalized))
	}
	if verdict, ok := decoded["verdict"].(string); !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", decoded["verdict"])
	}
	cr, ok := decoded["criteria_results"].([]interface{})
	if !ok || len(cr) != 1 {
		t.Fatalf("expected criteria_results with 1 item, got %#v", decoded["criteria_results"])
	}
	item, ok := cr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected criteria_results[0] to be a map, got %#v", cr[0])
	}
	if criterion, ok := item["criterion"].(string); !ok || criterion != "No code changes required" {
		t.Fatalf("expected criterion, got %#v", item["criterion"])
	}
	if result, ok := item["result"].(string); !ok || result != "pass" {
		t.Fatalf("expected result=pass, got %#v", item["result"])
	}
	notes, _ := item["notes"].(string)
	if !strings.Contains(notes, "The milestone goal was to create a test milestone") {
		t.Fatalf("expected notes to contain block-scalar content, got %q", notes)
	}
	if !strings.Contains(notes, "The developer made no changes.") {
		t.Fatalf("expected notes to contain second line, got %q", notes)
	}
	if rf, ok := decoded["reviewed_files"].([]interface{}); !ok || len(rf) != 0 {
		t.Fatalf("expected reviewed_files=[], got %#v", decoded["reviewed_files"])
	}
	if fc, ok := decoded["failing_checks"].([]interface{}); !ok || len(fc) != 0 {
		t.Fatalf("expected failing_checks=[], got %#v", decoded["failing_checks"])
	}
	if rf, ok := decoded["required_fixes"].([]interface{}); !ok || len(rf) != 0 {
		t.Fatalf("expected required_fixes=[], got %#v", decoded["required_fixes"])
	}
}

// TestNormalizeFlattenedBlockScalarsLeavesNestedProperlyIndentedContent
// verifies that nested block-scalar content that is already indented more than
// the key is not modified by the normalizer.
func TestNormalizeFlattenedBlockScalarsLeavesNestedProperlyIndentedContent(t *testing.T) {
	raw := strings.Join([]string{
		"criteria_results:",
		"  - criterion: \"test\"",
		"    result: \"pass\"",
		"    notes: |",
		"      Already properly indented content.",
		"      Second line.",
		"reviewed_files: []",
	}, "\n")
	normalized := normalizeFlattenedBlockScalars([]byte(raw))
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
		t.Fatalf("YAML failed to parse: %v\nnormalized:\n%s", err, string(normalized))
	}
	cr, ok := decoded["criteria_results"].([]interface{})
	if !ok || len(cr) != 1 {
		t.Fatalf("expected criteria_results with 1 item, got %#v", decoded["criteria_results"])
	}
	item, _ := cr[0].(map[string]interface{})
	notes, _ := item["notes"].(string)
	if !strings.Contains(notes, "Already properly indented content.") {
		t.Fatalf("expected notes to contain content, got %q", notes)
	}
}

// TestExtractFinalYAMLDocumentCapturesQANestedBlockScalar verifies the
// end-to-end extraction: extractFinalYAMLDocument on a QA Aider/Ollama log
// with a nested block scalar (notes: | inside criteria_results, content
// flattened to the same indent as the key) returns a document that parses
// with all fields intact. This uses the exact pattern from the real
// ollama/glm-5.2:cloud QA output in milestone cycle 0001.
func TestExtractFinalYAMLDocumentCapturesQANestedBlockScalar(t *testing.T) {
	text := strings.Join([]string{
		"Analytics have been permanently disabled.",
		"--------------------------------------------------------------------------------",
		"Aider v0.86.2",
		"",
		"► THINKING                                                                      ",
		"",
		"The developer handoff says:",
		"",
		"implemented_behavior:",
		"",
		"  - No changes were made as per the milestone goal.",
		"",
		"So the milestone goal is literally to make no changes.",
		"",
		"verdict: \"approved\"",
		"",
		"criteria_results:",
		"",
		" • criterion: \"No code changes required\"",
		"   result: \"pass\"",
		"   notes: \"Developer made no changes as per milestone goal.\"",
		"",
		"reviewed_files: []",
		"",
		"failing_checks: []",
		"",
		"required_fixes: []",
		"",
		"--------------------------------------------------------------------------------",
		"",
		"► ANSWER                                                                        ",
		"",
		" 1 Milestone ID and title: 0001-create-test-milestone-changes - Create Test    ",
		"   Milestone Changes                                                           ",
		" 2 Verdict: approved                                                           ",
		"",
		"verdict: approved                                                               ",
		"",
		"criteria_results:                                                               ",
		"",
		" • criterion: \"No code changes required\"                                        ",
		"   result: \"pass\"                                                               ",
		"   notes: |                                                                     ",
		"   The milestone goal was to create a test milestone without any changes.       ",
		"   The developer made no changes.                                               ",
		"",
		"reviewed_files: []                                                              ",
		"",
		"failing_checks: []                                                              ",
		"",
		"required_fixes: []                                                              ",
		"",
		"Tokens: 8.5k sent, 1.2k received.",
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected extraction to succeed, got error: %v", err)
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted YAML failed to parse: %v\nraw:\n%s", err, string(raw))
	}
	if verdict, ok := decoded["verdict"].(string); !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", decoded["verdict"])
	}
	cr, ok := decoded["criteria_results"].([]interface{})
	if !ok || len(cr) != 1 {
		t.Fatalf("expected criteria_results with 1 item, got %#v", decoded["criteria_results"])
	}
	item, ok := cr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected criteria_results[0] to be a map, got %#v", cr[0])
	}
	notes, _ := item["notes"].(string)
	if !strings.Contains(notes, "The milestone goal was to create a test milestone") {
		t.Fatalf("expected notes to contain block-scalar content, got %q", notes)
	}
	if rf, ok := decoded["reviewed_files"].([]interface{}); !ok || len(rf) != 0 {
		t.Fatalf("expected reviewed_files=[], got %#v", decoded["reviewed_files"])
	}
	if fc, ok := decoded["failing_checks"].([]interface{}); !ok || len(fc) != 0 {
		t.Fatalf("expected failing_checks=[], got %#v", decoded["failing_checks"])
	}
	if rf, ok := decoded["required_fixes"].([]interface{}); !ok || len(rf) != 0 {
		t.Fatalf("expected required_fixes=[], got %#v", decoded["required_fixes"])
	}
	// The extracted document must NOT contain implemented_behavior, which
	// only appeared in the THINKING section as a quoted developer handoff
	// field. Before the fix, the normalizer failed on the nested block
	// scalar, extractFinalYAMLDocument fell back to the THINKING block, and
	// the QA handoff was silently populated with developer-contract fields.
	if _, ok := decoded["implemented_behavior"]; ok {
		t.Fatalf("extracted QA document must not contain implemented_behavior (THINKING-section leakage)")
	}
}

// TestExtractFinalYAMLDocumentPrefersAnswerRegionOverThinking verifies that
// when the model's THINKING section contains YAML-like content with known
// handoff keys, extractFinalYAMLDocument does not pick it up and instead
// returns the YAML from the ANSWER region. Before the fix, when the ANSWER
// YAML failed to parse (e.g. due to nested block-scalar flattening), the
// function fell back to THINKING-section blocks, producing a handoff with
// wrong-contract fields.
func TestExtractFinalYAMLDocumentPrefersAnswerRegionOverThinking(t *testing.T) {
	text := strings.Join([]string{
		"► THINKING                                                                      ",
		"",
		"The developer handoff has:",
		"",
		"changed_files: []                                                              ",
		"",
		"implemented_behavior:                                                          ",
		"  - did stuff                                                                  ",
		"",
		"--------------------------------------------------------------------------------",
		"",
		"► ANSWER                                                                        ",
		"",
		"score: 0                                                                       ",
		"",
		"verdict: approved                                                              ",
		"",
		"reason: |                                                                      ",
		"",
		"All criteria met.                                                              ",
		"",
		"next_cycle_focus: []                                                           ",
		"",
		"Tokens: 10k sent, 881 received.",
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected extraction to succeed, got error: %v", err)
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted YAML failed to parse: %v", err)
	}
	// Must contain the recommender fields from the ANSWER region.
	if verdict, ok := decoded["verdict"].(string); !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved from ANSWER region, got %#v", decoded["verdict"])
	}
	// Must NOT contain THINKING-section fields.
	if _, ok := decoded["changed_files"]; ok {
		t.Fatalf("extracted document must not contain changed_files (THINKING-section leakage)")
	}
	if _, ok := decoded["implemented_behavior"]; ok {
		t.Fatalf("extracted document must not contain implemented_behavior (THINKING-section leakage)")
	}
}

// TestWritePhaseHandoffOllamaQACapturesNestedBlockScalar is the end-to-end
// regression test for the QA agent: an ollama QA log with a nested block
// scalar (notes: | inside criteria_results, content flattened to the same
// indent as the key by Aider's CLI display) must produce a handoff that
// retains verdict, criteria_results (with notes), reviewed_files,
// failing_checks, and required_fixes. Before the fix, the nested block
// scalar caused a parse failure, the extractor fell back to THINKING-section
// content, and the QA handoff was silently populated with
// implemented_behavior: null (a developer-contract field) instead of the QA
// fields.
func TestWritePhaseHandoffOllamaQACapturesNestedBlockScalar(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "qa.log")
	handoffPath := filepath.Join(tmpDir, "qa-handoff.yaml")
	// Exact pattern from the real ollama/glm-5.2:cloud QA output in cycle
	// 0001: the THINKING section quotes developer handoff fields, the ANSWER
	// section has the QA YAML with a nested block scalar whose content is
	// flattened to the same indent as the key (indent 3).
	text := strings.Join([]string{
		"Aider v0.86.2",
		"",
		"► THINKING                                                                      ",
		"",
		"The developer handoff says:",
		"",
		"implemented_behavior:",
		"  - No changes were made.",
		"",
		"verdict: \"approved\"",
		"",
		"criteria_results:",
		" • criterion: \"No code changes required\"",
		"   result: \"pass\"",
		"   notes: \"Developer made no changes.\"",
		"",
		"reviewed_files: []",
		"failing_checks: []",
		"required_fixes: []",
		"",
		"--------------------------------------------------------------------------------",
		"",
		"► ANSWER                                                                        ",
		"",
		" 1 Milestone ID and title: test-ms - Test                                      ",
		" 2 Verdict: approved                                                           ",
		"",
		"verdict: approved                                                               ",
		"",
		"criteria_results:                                                               ",
		"",
		" • criterion: \"No code changes required\"                                        ",
		"   result: \"pass\"                                                               ",
		"   notes: |                                                                     ",
		"   The milestone goal was to create a test milestone without any changes.       ",
		"   The developer made no changes.                                               ",
		"",
		"reviewed_files: []                                                              ",
		"",
		"failing_checks: []                                                              ",
		"",
		"required_fixes: []                                                              ",
		"",
		"Tokens: 8.5k sent, 1.2k received.",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "test-ms", 1, "qa", "qa", logPath, 1000, "", "ollama"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.OutputContract != "qa" {
		t.Fatalf("expected output_contract=qa, got %q", handoff.OutputContract)
	}
	if handoff.Fallback {
		t.Fatalf("expected non-fallback handoff from structured YAML, got fallback=true")
	}
	// verdict must be captured from the ANSWER region.
	verdict, ok := handoff.Summary["verdict"].(string)
	if !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved in summary, got %#v", handoff.Summary["verdict"])
	}
	// criteria_results with notes block-scalar content.
	cr, ok := handoff.Summary["criteria_results"].([]interface{})
	if !ok || len(cr) != 1 {
		t.Fatalf("expected criteria_results with 1 item, got %#v", handoff.Summary["criteria_results"])
	}
	item, ok := cr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected criteria_results[0] to be a map, got %#v", cr[0])
	}
	notes, _ := item["notes"].(string)
	if !strings.Contains(notes, "The milestone goal was to create a test milestone") {
		t.Fatalf("expected notes to contain block-scalar content, got %q", notes)
	}
	// Array fields must be present.
	for _, key := range []string{"reviewed_files", "failing_checks", "required_fixes"} {
		if _, ok := handoff.Summary[key].([]interface{}); !ok {
			t.Fatalf("expected %s to be an array, got %#v", key, handoff.Summary[key])
		}
	}
	// The QA handoff must NOT contain implemented_behavior, which only
	// appeared in the THINKING section as a quoted developer handoff field.
	if _, ok := handoff.Summary["implemented_behavior"]; ok {
		t.Fatalf("QA handoff must not contain implemented_behavior (THINKING-section leakage)")
	}
}
