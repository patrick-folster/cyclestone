package executor

import (
	"context"
	"encoding/json"
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
	reportPath := filepath.Join(".cyclestone", "reports", "MS-CANCEL", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
	reportFile, err := os.Create(reportPath)
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

func TestRunAgentPipelineRecommenderPersistsAndReportsBothScores(t *testing.T) {
	withFakeCodexTestDir(t, `#!/bin/sh
input=$(cat)
handoff_path=$(printf '%s\n' "$input" | sed -n 's/.*\(\.cyclestone\/temp\/[^[:space:]]*handoff.yaml\).*/\1/p' | tail -n 1)
if [ -z "$handoff_path" ]; then
  echo "missing handoff path" >&2
  exit 1
fi
mkdir -p "$(dirname "$handoff_path")"
cat > "$handoff_path" <<'YAML'
score: 3
agent_instructions_update_score: 9
verdict: needs-another-cycle
reason: AGENTS.md should be reviewed separately from the cycle recommendation.
next_cycle_focus:
  - Verify score persistence.
YAML
echo '{"msg":"thread.started","thread_id":"thread-recommender-pipeline"}'
`)

	if err := os.MkdirAll(filepath.Join(".cyclestone", "reports"), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(".cyclestone", "temp"), 0755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	statePath := filepath.Join(".cyclestone", "state.json")
	state := &config.State{
		MilestoneStatuses:                     map[string]string{},
		MilestoneCycles:                       map[string]int{},
		MilestoneRecommendations:              map[string]int{},
		MilestoneAgentInstructionUpdateScores: map[string]int{},
		History: map[string][]config.MilestoneCycleLog{
			"MS-PIPE": {{CycleNumber: 1, Branch: "develop", Status: "approved"}},
		},
	}
	reportPath := filepath.Join(".cyclestone", "reports", "MS-PIPE", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
	reportFile, err := os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to create report: %v", err)
	}
	defer reportFile.Close()

	status, interrupted := runAgentPipeline(
		context.Background(),
		[]config.Agent{{
			ID:             "recommender",
			Name:           "Recommender",
			PromptBody:     "Write recommender handoff to {{HANDOFF_YAML_PATH}}. {{HANDOFF_INSTRUCTION}}",
			RunnerBinary:   "codex",
			OutputContract: "recommender",
		}},
		config.Milestone{ID: "MS-PIPE", Goal: "persist recommender scores"},
		RunOptions{StatePath: statePath},
		state,
		nil,
		filepath.Join(".cyclestone", "reports"),
		1,
		"",
		"",
		config.Settings{},
		reportFile,
		filepath.Join(".cyclestone", "reports", "thread.json"),
		new(string),
	)
	if interrupted {
		t.Fatalf("expected pipeline to complete")
	}
	if status != "approved" {
		t.Fatalf("expected approved status, got %q", status)
	}
	if got := state.GetMilestoneRecommendation("MS-PIPE"); got != 3 {
		t.Fatalf("expected recommender score 3, got %d", got)
	}
	if got := state.GetMilestoneAgentInstructionsUpdateScore("MS-PIPE"); got != 9 {
		t.Fatalf("expected AGENTS.md update score 9, got %d", got)
	}
	if err := reportFile.Close(); err != nil {
		t.Fatalf("failed to close report: %v", err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Recommendation score: 3") {
		t.Fatalf("expected cycle recommendation score in report, got:\n%s", text)
	}
	if !strings.Contains(text, "AGENTS.md update recommendation score: 9") {
		t.Fatalf("expected AGENTS.md update score in report, got:\n%s", text)
	}
}

func TestCycleAndPhaseArtifactPathsUseHierarchicalLayout(t *testing.T) {
	reportsDir := filepath.Join(".cyclestone", "reports")
	cycle := cycleArtifacts(reportsDir, "MS-PATH", 1)
	if cycle.Summary != filepath.Join(reportsDir, "MS-PATH", "summary.md") {
		t.Fatalf("unexpected summary path: %q", cycle.Summary)
	}
	if cycle.Report != filepath.Join(reportsDir, "MS-PATH", "cycle-001", "report.yaml") {
		t.Fatalf("unexpected report path: %q", cycle.Report)
	}
	if cycle.Metadata != filepath.Join(reportsDir, "MS-PATH", "cycle-001", "metadata.json") {
		t.Fatalf("unexpected metadata path: %q", cycle.Metadata)
	}
	if cycle.CodexThread != filepath.Join(reportsDir, "MS-PATH", "cycle-001", "codex-thread.json") {
		t.Fatalf("unexpected codex thread metadata path: %q", cycle.CodexThread)
	}

	pipeline := []config.Agent{{ID: "pm"}, {ID: "developer"}, {ID: "qa"}, {ID: "recommender"}}
	for _, tc := range []struct {
		agentID string
		fileID  string
	}{
		{"pm", "01-pm"},
		{"developer", "02-developer"},
		{"qa", "03-qa"},
		{"recommender", "04-recommender"},
	} {
		if got := getAgentFileID(tc.agentID, pipeline); got != tc.fileID {
			t.Fatalf("expected %s file ID %q, got %q", tc.agentID, tc.fileID, got)
		}
		phase := phaseArtifacts(reportsDir, "MS-PATH", 1, tc.fileID)
		base := filepath.Join(reportsDir, "MS-PATH", "cycle-001", tc.fileID)
		if phase.Input != filepath.Join(base, "input.md") ||
			phase.Output != filepath.Join(base, "output.log") ||
			phase.Handoff != filepath.Join(base, "handoff.yaml") {
			t.Fatalf("unexpected paths for %s: %#v", tc.agentID, phase)
		}
	}
}

func TestAssembleAgentInstructionsUpdateInputScopesContextAndHumanMessage(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(".cyclestone", "reports"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("resources", "agents"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("AGENTS.md", []byte("CURRENT AGENTS\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "DECISIONS.md"), []byte("DECISIONS BOUNDARY\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-A.md"), []byte("ACTIVE SPEC\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-B.md"), []byte("UNRELATED SPEC\n"), 0644); err != nil {
		t.Fatal(err)
	}
	activeReport := filepath.Join(".cyclestone", "reports", "MS-A", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(activeReport), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeReport, []byte("ACTIVE REPORT\n"), 0644); err != nil {
		t.Fatal(err)
	}
	unrelatedReport := filepath.Join(".cyclestone", "reports", "MS-B", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(unrelatedReport), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedReport, []byte("UNRELATED REPORT\n"), 0644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(".cyclestone", "state.json")
	state, err := config.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	state.SetMilestoneCycles("MS-A", 1)
	if err := config.SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}

	input := assembleAgentInstructionsUpdateInput(config.Milestone{ID: "MS-A"}, true, RunOptions{StatePath: statePath, CycleNote: "human guidance", NoBranchChange: true})
	for _, want := range []string{"Scope: milestone-scoped (MS-A)", "human guidance", "ACTIVE SPEC", "ACTIVE REPORT", "DECISIONS BOUNDARY", "Do not load unrelated milestone specs"} {
		if !strings.Contains(input, want) {
			t.Fatalf("expected scoped input to contain %q, got:\n%s", want, input)
		}
	}
	for _, forbidden := range []string{"UNRELATED SPEC", "UNRELATED REPORT"} {
		if strings.Contains(input, forbidden) {
			t.Fatalf("expected scoped input to exclude %q, got:\n%s", forbidden, input)
		}
	}
}

func TestAssembleAgentInstructionsUpdateInputRepositoryContextIncludesChecksAndExcludesGeneratedRuntime(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	for _, dir := range []string{
		filepath.Join(".cyclestone", "reports"),
		filepath.Join(".cyclestone", "temp"),
		filepath.Join(".cyclestone", "milestones"),
		filepath.Join("docs"),
		filepath.Join("resources", "agents"),
		filepath.Join("internal", "tui"),
		filepath.Join("vendor", "example"),
		filepath.Join("node_modules", "example"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
	}
	files := map[string]string{
		"AGENTS.md": "ROOT AGENTS CONTENT\n",
		filepath.Join(".cyclestone", "DECISIONS.md"):               "DECISION CONTENT\n",
		filepath.Join(".cyclestone", "milestone.yml"):              "repositories:\n  - service-a\nmilestones:\n  - id: MS-REPO\n    title: Repo checks\n    checks:\n      - frontend\n",
		filepath.Join(".cyclestone", "reports", "generated.yaml"):  "GENERATED REPORT CONTENT\n",
		filepath.Join(".cyclestone", "temp", "draft.md"):           "TEMP DRAFT CONTENT\n",
		filepath.Join(".cyclestone", "state.json"):                 `{"MS-REPO":"runtime state"}`,
		filepath.Join("README.md"):                                 "README CONTENT\n",
		filepath.Join("docs", "architecture.md"):                   "ARCHITECTURE CONTENT\n",
		filepath.Join("resources", "update_agent_instructions.md"): "UPDATER PROMPT CONTENT\n",
		filepath.Join("resources", "agents", "pm.md"):              "PM PROMPT CONTENT\n",
		filepath.Join("resources", "agents", "developer.md"):       "DEVELOPER PROMPT CONTENT\n",
		filepath.Join("resources", "agents", "qa.md"):              "QA PROMPT CONTENT\n",
		filepath.Join("resources", "agents", "recommender.md"):     "RECOMMENDER PROMPT CONTENT\n",
		filepath.Join("internal", "tui", "create.go"):              "package tui\n",
		filepath.Join("vendor", "example", "library.go"):           "VENDOR CONTENT\n",
		filepath.Join("node_modules", "example", "package.json"):   "NODE MODULE CONTENT\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", path, err)
		}
	}
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	if err := exec.Command("git", "add", ".").Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}

	input := assembleAgentInstructionsUpdateInput(config.Milestone{ID: "AGENTS.md"}, false, RunOptions{CycleNote: "repo guidance", NoBranchChange: true})
	for _, want := range []string{
		"Scope: repository-wide",
		"repo guidance",
		"ROOT AGENTS CONTENT",
		"DECISION CONTENT",
		"README CONTENT",
		"ARCHITECTURE CONTENT",
		"UPDATER PROMPT CONTENT",
		"PM PROMPT CONTENT",
		"## Configured Checks",
		"service-a",
		"frontend",
		"internal/tui/create.go",
	} {
		if !strings.Contains(input, want) {
			t.Fatalf("expected repository input to contain %q, got:\n%s", want, input)
		}
	}
	for _, forbidden := range []string{
		"GENERATED REPORT CONTENT",
		"TEMP DRAFT CONTENT",
		"runtime state",
		"VENDOR CONTENT",
		"NODE MODULE CONTENT",
		".cyclestone/reports/generated.yaml",
		".cyclestone/temp/draft.md",
		".cyclestone/state.json",
		"vendor/example/library.go",
		"node_modules/example/package.json",
	} {
		if strings.Contains(input, forbidden) {
			t.Fatalf("expected repository input to exclude %q, got:\n%s", forbidden, input)
		}
	}
}

func TestAssembleAgentInstructionsUpdateInputWithoutOptionalInstructionFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	input := assembleAgentInstructionsUpdateInput(config.Milestone{ID: "MS-MISSING-INSTRUCTIONS"}, false, RunOptions{NoBranchChange: true})
	if !strings.Contains(input, "# AGENTS.md Update Proposal Input") || !strings.Contains(input, "Scope: repository-wide") {
		t.Fatalf("expected update prompt to build without optional instruction files, got:\n%s", input)
	}
	for _, omitted := range []string{"## Agent Instructions", "## Decisions Log"} {
		if strings.Contains(input, omitted) {
			t.Fatalf("expected missing optional section %q to be omitted, got:\n%s", omitted, input)
		}
	}
}

func TestExecuteAgentInstructionsUpdateCapturesProposalAndRestoresAgents(t *testing.T) {
	withFakeCodexTestDir(t, `#!/bin/sh
cat >/dev/null
cat > AGENTS.md <<'EOF'
PROPOSED AGENTS
EOF
echo proposal written
`)
	if err := os.WriteFile("AGENTS.md", []byte("ORIGINAL AGENTS\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ch := make(chan tea.Msg, 20)
	ExecuteAgentInstructionsUpdate(context.Background(), config.Milestone{ID: "AGENTS.md", Title: "Repository update"}, false, "codex", RunOptions{NoBranchChange: true}, ch)

	agentsBytes, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(agentsBytes) != "ORIGINAL AGENTS\n" {
		t.Fatalf("expected AGENTS.md restored, got %q", string(agentsBytes))
	}
	draftBytes, err := os.ReadFile(filepath.Join(".cyclestone", "temp", "AGENTS.md.proposed"))
	if err != nil {
		t.Fatalf("expected proposal draft: %v", err)
	}
	if !strings.Contains(string(draftBytes), "PROPOSED AGENTS") {
		t.Fatalf("expected captured proposal, got %q", string(draftBytes))
	}
	foundFinished := false
	for len(ch) > 0 {
		if msg, ok := (<-ch).(CycleFinishedMsg); ok {
			foundFinished = true
			if msg.Error != nil || msg.Status != "approved" {
				t.Fatalf("expected approved finish, got %#v", msg)
			}
		}
	}
	if !foundFinished {
		t.Fatal("expected CycleFinishedMsg")
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

func TestSetupTemporaryAiderSettingsWritesNegativeOneDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// ollama_chat/ models use Ollama's OpenAI-compatible endpoint where
	// num_predict maps to max_tokens, which must be positive. The -1 unlimited
	// sentinel is therefore omitted so Ollama falls back to its default
	// unlimited generation. num_ctx: -1 is still written because the
	// OpenAI-compatible endpoint passes it through to Ollama's native options.
	cleanup := setupTemporaryAiderSettings("ollama_chat/glm-5.2:cloud", config.Settings{
		OllamaNumCtx:     -1,
		OllamaNumPredict: -1,
	})
	defer cleanup()

	data, err := os.ReadFile(".aider.model.settings.yml")
	if err != nil {
		t.Fatalf("expected .aider.model.settings.yml to be written for -1 values, got error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "num_ctx: -1") {
		t.Fatalf("expected num_ctx: -1 in temporary aider model settings, got:\n%s", text)
	}
	if strings.Contains(text, "num_predict") {
		t.Fatalf("expected num_predict to be omitted for ollama_chat/ models with -1, got:\n%s", text)
	}
}

func TestSetupTemporaryAiderSettingsKeepsNegativeOneForNativeOllama(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Native ollama/ models use /api/chat where num_predict: -1 (unlimited) is
	// valid, so it must still be written.
	cleanup := setupTemporaryAiderSettings("ollama/qwen3-coder:480b-cloud", config.Settings{
		OllamaNumCtx:     -1,
		OllamaNumPredict: -1,
	})
	defer cleanup()

	data, err := os.ReadFile(".aider.model.settings.yml")
	if err != nil {
		t.Fatalf("expected .aider.model.settings.yml to be written for -1 values, got error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "num_ctx: -1") {
		t.Fatalf("expected num_ctx: -1 in temporary aider model settings, got:\n%s", text)
	}
	if !strings.Contains(text, "num_predict: -1") {
		t.Fatalf("expected num_predict: -1 for native ollama/ models, got:\n%s", text)
	}
}

func TestSetupTemporaryAiderSettingsStripsStaleNumPredict(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Seed a pre-existing settings file carrying a stale num_predict: -1 entry
	// (e.g. left behind by a previous broken run) for the ollama_chat/ model.
	seed := "- name: ollama_chat/glm-5.2:cloud\n  extra_params:\n    num_ctx: -1\n    num_predict: -1\n"
	if err := os.WriteFile(".aider.model.settings.yml", []byte(seed), 0644); err != nil {
		t.Fatalf("failed to seed settings file: %v", err)
	}

	cleanup := setupTemporaryAiderSettings("ollama_chat/glm-5.2:cloud", config.Settings{
		OllamaNumCtx:     -1,
		OllamaNumPredict: -1,
	})
	defer cleanup()

	data, err := os.ReadFile(".aider.model.settings.yml")
	if err != nil {
		t.Fatalf("expected .aider.model.settings.yml to be written, got error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "num_ctx: -1") {
		t.Fatalf("expected num_ctx: -1 to be preserved, got:\n%s", text)
	}
	if strings.Contains(text, "num_predict") {
		t.Fatalf("expected stale num_predict to be stripped for ollama_chat/ models, got:\n%s", text)
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

	previousReportPath := filepath.Join(".cyclestone", "reports", "MS-LIMIT", "cycle-001", "report.yaml")
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

	reportPath := filepath.Join(".cyclestone", "reports", "MS-REC", "cycle-002", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	report := strings.Join([]string{
		`milestone_id: "MS-REC"`,
		`started: "2026-06-23 18:26:57 -0600"`,
		`cycle: "002"`,
		`cycle_mode: "continuation"`,
		`details: |-`,
		"  ## Developer Phase",
		"",
		"  ```text",
		"  " + strings.ReplaceAll(strings.Repeat("DEVELOPER-LOG-NOISE\n", 30000), "\n", "\n  "),
		"  Exit status: 0",
		"  ```",
		"",
		"  ## Quality Manager Phase",
		"",
		"  R final QA blocker",
		"  O blocked",
		"  Exit status: 0",
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

func TestWritePhaseHandoffPrefersTempYAMLFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	tempYAMLPath := filepath.Join(tmpDir, "developer-temp-handoff.yaml")

	// The console log contains NO structured YAML — only prose. Without the
	// temp file this would produce a fallback handoff.
	if err := os.WriteFile(logPath, []byte("I made the changes.\nNo YAML in console output.\n"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}

	// The agent wrote its structured handoff directly to the temp file.
	tempYAML := strings.Join([]string{
		"changed_files:",
		"  - internal/executor/handoff.go",
		"implemented_behavior:",
		"  - wrote handoff to temp file",
		"checks_run:",
		"  - go test ./internal/executor -> PASS",
		"decisions:",
		"  - prefer temp file over console parsing",
		"risks: []",
	}, "\n")
	if err := os.WriteFile(tempYAMLPath, []byte(tempYAML), 0644); err != nil {
		t.Fatalf("failed to write temp yaml: %v", err)
	}

	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-T", 1, "developer", "developer", logPath, 1000, "", "codex", tempYAMLPath); err != nil {
		t.Fatalf("writePhaseHandoff temp failed: %v", err)
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
		t.Fatalf("expected valid developer contract from temp file, got contract=%q status=%q", handoff.OutputContract, handoff.ValidationStatus)
	}
	files, ok := handoff.Summary["changed_files"].([]interface{})
	if !ok || len(files) != 1 || files[0] != "internal/executor/handoff.go" {
		t.Fatalf("expected changed_files from temp yaml, got %#v", handoff.Summary["changed_files"])
	}
	if strings.Contains(string(data), "fallback: true") {
		t.Fatalf("expected no fallback when temp yaml is valid, got:\n%s", string(data))
	}
}

func TestInterceptAgentInstructionsMutationRestoresFileStates(t *testing.T) {
	stringPtr := func(value string) *string { return &value }
	tests := []struct {
		name            string
		before          *string
		after           *string
		wantChange      string
		wantFinalExists bool
		wantFinal       string
		wantProposed    string
	}{
		{
			name:            "created",
			after:           stringPtr("created instructions\n"),
			wantChange:      "created",
			wantFinalExists: false,
			wantProposed:    "created instructions\n",
		},
		{
			name:            "modified",
			before:          stringPtr("original instructions\n"),
			after:           stringPtr("updated instructions\n"),
			wantChange:      "modified",
			wantFinalExists: true,
			wantFinal:       "original instructions\n",
			wantProposed:    "updated instructions\n",
		},
		{
			name:            "deleted",
			before:          stringPtr("original instructions\n"),
			wantChange:      "deleted",
			wantFinalExists: true,
			wantFinal:       "original instructions\n",
			wantProposed:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)
			if tt.before != nil {
				if err := os.WriteFile("AGENTS.md", []byte(*tt.before), 0644); err != nil {
					t.Fatalf("failed to write initial AGENTS.md: %v", err)
				}
			}

			snapshot, err := snapshotAgentInstructions(config.LoadDefaultSettings())
			if err != nil {
				t.Fatalf("snapshotAgentInstructions failed: %v", err)
			}
			if tt.after != nil {
				if err := os.WriteFile("AGENTS.md", []byte(*tt.after), 0644); err != nil {
					t.Fatalf("failed to write changed AGENTS.md: %v", err)
				}
			} else if err := os.Remove("AGENTS.md"); err != nil {
				t.Fatalf("failed to remove changed AGENTS.md: %v", err)
			}

			interception, err := interceptAgentInstructionsMutation(snapshot)
			if err != nil {
				t.Fatalf("interceptAgentInstructionsMutation failed: %v", err)
			}
			if interception.Change != tt.wantChange || interception.Path != "AGENTS.md" || interception.ProposedContent != tt.wantProposed {
				t.Fatalf("unexpected interception: %#v", interception)
			}
			finalBytes, err := os.ReadFile("AGENTS.md")
			if tt.wantFinalExists {
				if err != nil {
					t.Fatalf("expected restored AGENTS.md: %v", err)
				}
				if string(finalBytes) != tt.wantFinal {
					t.Fatalf("expected restored content %q, got %q", tt.wantFinal, string(finalBytes))
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("expected created AGENTS.md mutation to be removed, read err=%v content=%q", err, string(finalBytes))
			}
		})
	}
}

func TestMergeProposedAgentInstructionsUpdateAddsReviewFields(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	tempYAMLPath := filepath.Join(tmpDir, "developer-temp-handoff.yaml")
	if err := os.WriteFile(logPath, []byte("done\n"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	tempYAML := "changed_files: []\nimplemented_behavior:\n  - implemented code\nchecks_run: []\ndecisions: []\nrisks: []\n"
	if err := os.WriteFile(tempYAMLPath, []byte(tempYAML), 0644); err != nil {
		t.Fatalf("failed to write temp yaml: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-I", 1, "developer", "developer", logPath, 1000, "", "codex", tempYAMLPath); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	interception := agentInstructionsInterception{
		Path:            "AGENTS.md",
		Change:          "modified",
		ProposedContent: "proposed instructions\n",
	}
	if err := mergeProposedAgentInstructionsUpdate(handoffPath, interception); err != nil {
		t.Fatalf("mergeProposedAgentInstructionsUpdate failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.ValidationStatus != "valid" {
		t.Fatalf("expected original validation status to remain valid, got %q", handoff.ValidationStatus)
	}
	if got, _ := handoff.Summary["proposed_agent_instructions_update"].(string); got != "proposed instructions\n" {
		t.Fatalf("expected proposed content in summary, got %#v", handoff.Summary["proposed_agent_instructions_update"])
	}
	if got, _ := handoff.Summary["proposed_agent_instructions_change"].(string); got != "modified" {
		t.Fatalf("expected change type in summary, got %#v", handoff.Summary["proposed_agent_instructions_change"])
	}
}

func TestWritePhaseHandoffFallsBackWhenTempFileMissing(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "pm.log")
	handoffPath := filepath.Join(tmpDir, "pm-handoff.yaml")
	tempYAMLPath := filepath.Join(tmpDir, "does-not-exist.yaml")

	// Console log contains fenced YAML; temp file does not exist. Should fall
	// back to the log-based extraction.
	if err := os.WriteFile(logPath, []byte("report\n```yaml\nscope:\n  - one\nrisks:\n  - low\n```\n"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-F", 1, "pm", "", logPath, 1000, "", "codex", tempYAMLPath); err != nil {
		t.Fatalf("writePhaseHandoff fallback failed: %v", err)
	}
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if !strings.Contains(string(data), "scope:") {
		t.Fatalf("expected parsed yaml from log when temp file missing, got:\n%s", string(data))
	}
	if strings.Contains(string(data), "fallback: true") {
		t.Fatalf("should not be a fallback when log has valid yaml, got:\n%s", string(data))
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

func TestContractHandoffPrefersTrailingRawYAMLOverPromptSampleFence(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.yaml")
	text := strings.Join([]string{
		"Agent instructions:",
		"Return a final YAML document like this sample:",
		"```yaml",
		"changed_files:",
		"  - sample.md",
		"implemented_behavior:",
		"  - sample behavior",
		"checks_run: []",
		"decisions:",
		"  - sample decision",
		"risks: []",
		"```",
		"",
		"Actual attached output:",
		"",
		"changed_files:",
		"  - internal/executor/handoff.go",
		"implemented_behavior:",
		"  - selected trailing raw YAML instead of the prompt sample",
		"checks_run:",
		"  - go test ./internal/executor",
		"decisions:",
		"  - prefer the latest parseable handoff document",
		"risks: []",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "developer", "developer", logPath, 1000, "", "codex"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.OutputContract != "developer" || handoff.ValidationStatus != "valid" {
		t.Fatalf("expected valid developer contract, got %#v", handoff)
	}
	files := handoff.Summary["changed_files"].([]interface{})
	if files[0] != "internal/executor/handoff.go" {
		t.Fatalf("expected trailing raw YAML to win over prompt sample fence, got %#v", files)
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
			body:     "```yaml\nscore: 1\nagent_instructions_update_score: 0\nreason: complete\nnext_cycle_focus: []\n```",
			contains: "missing required field \"verdict\"",
		},
		{
			name:     "wrong verdict type",
			body:     "```yaml\nscore: 1\nagent_instructions_update_score: 0\nverdict: true\nreason: complete\nnext_cycle_focus: []\n```",
			contains: "field \"verdict\" must be a string",
		},
		{
			name:     "missing AGENTS.md update score",
			body:     "```yaml\nscore: 1\nverdict: approved\nreason: complete\nnext_cycle_focus: []\n```",
			contains: "missing required field \"agent_instructions_update_score\"",
		},
		{
			name:     "out of range AGENTS.md update score",
			body:     "```yaml\nscore: 1\nagent_instructions_update_score: 11\nverdict: approved\nreason: complete\nnext_cycle_focus: []\n```",
			contains: "field \"agent_instructions_update_score\" must be an integer from 0 to 10",
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
		"agent_instructions_update_score: 4",
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
	body := "```yaml\nscore: 1\nagent_instructions_update_score: 0\nreason: complete\nnext_cycle_focus: []\n```"
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

func TestQAVerdictFromHandoffIgnoresEmbeddedRepoOnlyBlockingWarning(t *testing.T) {
	tmpDir := t.TempDir()
	handoffPath := filepath.Join(tmpDir, "qa-handoff.yaml")
	handoff := strings.Join([]string{
		"milestone_id: MS-QA",
		"cycle: 1",
		"agent_id: qa",
		"output_contract: qa",
		"validation_status: valid",
		"source_log: qa.log",
		"summary:",
		"  verdict: blocked",
		"  criteria_results: []",
		"  reviewed_files: []",
		"  failing_checks:",
		"    - Embedded Git repository detected at tools/nested without Cyclestone tracking. This is informational only and is excluded from recommender scoring; add it to repositories or .gitmodules if Cyclestone should manage it separately.",
		"  required_fixes:",
		"    - Do not treat them as acceptance gaps, required fixes, failing checks, or cycle-continuation score drivers unless the milestone explicitly targets repository topology.",
	}, "\n")
	if err := os.WriteFile(handoffPath, []byte(handoff), 0644); err != nil {
		t.Fatalf("failed to write QA handoff: %v", err)
	}
	if got := qaVerdictFromHandoff(handoffPath); got != "" {
		t.Fatalf("expected embedded-repo-only blocked QA verdict to be neutralized, got %q", got)
	}

	handoff = strings.Replace(handoff, "  required_fixes:\n    - Do not treat them as acceptance gaps, required fixes, failing checks, or cycle-continuation score drivers unless the milestone explicitly targets repository topology.", "  required_fixes:\n    - Update API docs", 1)
	if err := os.WriteFile(handoffPath, []byte(handoff), 0644); err != nil {
		t.Fatalf("failed to rewrite QA handoff: %v", err)
	}
	if got := qaVerdictFromHandoff(handoffPath); got != "blocked" {
		t.Fatalf("expected real blocked QA verdict to remain, got %q", got)
	}
}

func TestRecommenderScoreUsesStructuredHandoffOnly(t *testing.T) {
	tmpDir := t.TempDir()
	handoffPath := filepath.Join(tmpDir, "recommender-handoff.yaml")
	if err := os.WriteFile(handoffPath, []byte("summary:\n  score: 2\n  agent_instructions_update_score: 6\noutput_contract: recommender\nvalidation_status: valid\n"), 0644); err != nil {
		t.Fatalf("failed to write handoff: %v", err)
	}
	if got := parseRecommendationScore(handoffPath); got != 2 {
		t.Fatalf("expected structured score, got %d", got)
	}
	if got := parseAgentInstructionsUpdateRecommendationScore(handoffPath); got != 6 {
		t.Fatalf("expected structured AGENTS.md update score, got %d", got)
	}
	if err := os.WriteFile(handoffPath, []byte("summary: {}\noutput_contract: recommender\nvalidation_status: invalid\n"), 0644); err != nil {
		t.Fatalf("failed to write invalid handoff: %v", err)
	}
	if got := parseRecommendationScore(handoffPath); got != -1 {
		t.Fatalf("expected invalid handoff score to be unavailable, got %d", got)
	}
	if got := parseAgentInstructionsUpdateRecommendationScore(handoffPath); got != -1 {
		t.Fatalf("expected invalid handoff AGENTS.md update score to be unavailable, got %d", got)
	}
}

func TestRecommenderReportScoreLinesShowBothScores(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "report.md")
	reportFile, err := os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to create report: %v", err)
	}
	writeRecommenderScoreReportLines(reportFile, 2, 8)
	if err := reportFile.Close(); err != nil {
		t.Fatalf("failed to close report: %v", err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Recommendation score: 2") {
		t.Fatalf("expected existing recommendation score line, got:\n%s", text)
	}
	if !strings.Contains(text, "AGENTS.md update recommendation score: 8") {
		t.Fatalf("expected AGENTS.md update score line, got:\n%s", text)
	}

	reportFile, err = os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to recreate report: %v", err)
	}
	writeRecommenderScoreReportLines(reportFile, -1, -1)
	if err := reportFile.Close(); err != nil {
		t.Fatalf("failed to close report: %v", err)
	}
	data, err = os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	text = string(data)
	if !strings.Contains(text, "Recommendation score: N/A") {
		t.Fatalf("expected unavailable recommendation score line, got:\n%s", text)
	}
	if !strings.Contains(text, "AGENTS.md update recommendation score: N/A") {
		t.Fatalf("expected unavailable AGENTS.md update score line, got:\n%s", text)
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
	if err := os.WriteFile("AGENTS.md", []byte("AGENTS-SHOULD-BE-LOADED\n"), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}
	priorReport := filepath.Join(".cyclestone", "reports", "MS-P", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(priorReport), 0755); err != nil {
		t.Fatalf("failed to create prior report dir: %v", err)
	}
	if err := os.WriteFile(priorReport, []byte("RAW-PRIOR-LOG\n"+strings.Repeat("noise\n", 100)), 0644); err != nil {
		t.Fatalf("failed to write prior report: %v", err)
	}
	pmHandoff := filepath.Join(".cyclestone", "reports", "MS-P", "cycle-002", "01-pm", "handoff.yaml")
	if err := os.MkdirAll(filepath.Dir(pmHandoff), 0755); err != nil {
		t.Fatalf("failed to create pm handoff dir: %v", err)
	}
	if err := os.WriteFile(pmHandoff, []byte("summary:\n  scope:\n    - pm scope\n  target_paths:\n    - internal/executor\n"), 0644); err != nil {
		t.Fatalf("failed to write pm handoff: %v", err)
	}
	devHandoff := filepath.Join(".cyclestone", "reports", "MS-P", "cycle-002", "02-developer", "handoff.yaml")
	if err := os.MkdirAll(filepath.Dir(devHandoff), 0755); err != nil {
		t.Fatalf("failed to create dev handoff dir: %v", err)
	}
	if err := os.WriteFile(devHandoff, []byte("summary:\n  changed_files:\n    - internal/executor/executor.go\n  checks_run:\n    - PASS\n"), 0644); err != nil {
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
		priorReport,
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
	if !strings.Contains(devInput, "AGENTS-SHOULD-BE-LOADED") {
		t.Fatalf("expected developer input to include AGENTS.md, got:\n%s", devInput)
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
	if !strings.Contains(qaInput, "AGENTS-SHOULD-BE-LOADED") {
		t.Fatalf("expected QA input to include AGENTS.md, got:\n%s", qaInput)
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
	outputPath := filepath.Join(reportsDir, "MS-F", "cycle-001", "01-pm", "output.log")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		t.Fatalf("failed to create phase dir: %v", err)
	}
	if err := os.WriteFile(outputPath, []byte("PM-FALLBACK-HEAD\n"+strings.Repeat("noise\n", 100)+"PM-FALLBACK-TAIL\n"), 0644); err != nil {
		t.Fatalf("failed to write output log: %v", err)
	}

	missing := readHandoffOrFallback("MS-F", "001", "pm", 200, nil)
	if !strings.Contains(missing, "Handoff summary missing") || !strings.Contains(missing, "PM-FALLBACK-HEAD") || !strings.Contains(missing, "PM-FALLBACK-TAIL") {
		t.Fatalf("expected missing handoff to use bounded output log fallback, got:\n%s", missing)
	}

	handoffPath := filepath.Join(reportsDir, "MS-F", "cycle-001", "01-pm", "handoff.yaml")
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

func TestOllamaCodexCommandConstructionUsesSharedCodexArgs(t *testing.T) {
	restricted := buildOllamaCodexCommand(context.Background(), RunOptions{}, "glm-test:cloud", false, "")
	wantRestricted := []string{"ollama", "launch", "codex", "--model", "glm-test:cloud", "--", "--sandbox", "workspace-write", "--ask-for-approval", "never", "exec", "--cd", ".", "--skip-git-repo-check", "--", "-"}
	if !slicesEqual(restricted.Args, wantRestricted) {
		t.Fatalf("restricted ollama-codex args mismatch:\n got: %v\nwant: %v", restricted.Args, wantRestricted)
	}

	unrestricted := buildOllamaCodexCommand(context.Background(), RunOptions{Unrestricted: true}, "glm-test:cloud", false, "")
	wantUnrestricted := []string{"ollama", "launch", "codex", "--model", "glm-test:cloud", "--", "--sandbox", "danger-full-access", "--dangerously-bypass-approvals-and-sandbox", "exec", "--cd", ".", "--skip-git-repo-check", "--", "-"}
	if !slicesEqual(unrestricted.Args, wantUnrestricted) {
		t.Fatalf("unrestricted ollama-codex args mismatch:\n got: %v\nwant: %v", unrestricted.Args, wantUnrestricted)
	}

	startCmd := buildOllamaCodexCommand(context.Background(), RunOptions{}, "glm-test:cloud", true, "")
	startArgs := strings.Join(startCmd.Args, " ")
	if !strings.Contains(startArgs, "exec --json") || strings.Contains(startArgs, "resume") {
		t.Fatalf("unexpected ollama-codex start command args: %v", startCmd.Args)
	}

	resumeCmd := buildOllamaCodexCommand(context.Background(), RunOptions{}, "glm-test:cloud", true, "thread-123")
	resumeArgs := strings.Join(resumeCmd.Args, " ")
	if !strings.Contains(resumeArgs, "exec resume thread-123") || strings.Contains(resumeArgs, "--json") {
		t.Fatalf("unexpected ollama-codex resume command args: %v", resumeCmd.Args)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

func TestOllamaCodexSessionResumeWithFakeBinary(t *testing.T) {
	withFakeOllamaTestDir(t, `#!/bin/sh
printf '%s\n' "$*" >> ollama-args.log
if printf '%s\n' "$*" | grep -q -- '--json'; then
  echo '{"msg":"thread.started","thread_id":"thread-fake"}'
fi
echo 'assistant'
echo 'done'
`)

	trueVal := true
	if err := config.SaveProjectSettings(config.Settings{
		DefaultLLM:               "ollama-codex",
		OllamaCodexModel:         "glm-test:cloud",
		EnableCodexSessionResume: &trueVal,
		MaxLLMInputChars:         900000,
	}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	threadID := ""
	pmExit, pmErr := runRunnerWithSession(context.Background(), "ollama-codex", "pm", "Project Manager", "pm prompt", "pm.log", RunOptions{}, nil, &threadID)
	if pmExit != 0 || pmErr != nil {
		t.Fatalf("expected fake PM ollama-codex success, exit=%d err=%v", pmExit, pmErr)
	}
	if threadID != "thread-fake" {
		t.Fatalf("expected parsed thread id, got %q", threadID)
	}

	devExit, devErr := runRunnerWithSession(context.Background(), "ollama-codex", "developer", "Developer", "dev prompt", "dev.log", RunOptions{}, nil, &threadID)
	if devExit != 0 || devErr != nil {
		t.Fatalf("expected fake developer ollama-codex success, exit=%d err=%v", devExit, devErr)
	}

	argsBytes, err := os.ReadFile("ollama-args.log")
	if err != nil {
		t.Fatalf("failed to read fake ollama args: %v", err)
	}
	args := string(argsBytes)
	if !strings.Contains(args, "launch codex --model glm-test:cloud --") ||
		!strings.Contains(args, "exec --json") ||
		!strings.Contains(args, "exec resume thread-fake") {
		t.Fatalf("expected start and resume ollama-codex calls, got:\n%s", args)
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

func withFakeOllamaTestDir(t *testing.T, script string) {
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
	ollamaPath := filepath.Join(binDir, "ollama")
	if err := os.WriteFile(ollamaPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake ollama: %v", err)
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
	writeReportHeader(reportFile, "MS-YAML", "develop", 1, "", ".cyclestone/reports/MS-YAML/cycle-001/metadata.json", RunOptions{NoBranchChange: true, CycleNote: "human note"}, nil, nil)
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

func TestCycleReportInformationalWarningsAreYAMLAndExcludedFromSummary(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "MS-WARN", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
	reportFile, err := os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to create report: %v", err)
	}
	warnings := []string{"Embedded Git repository detected at tools/nested without Cyclestone tracking. This is informational only and is excluded from recommender scoring; add it to repositories or .gitmodules if Cyclestone should manage it separately."}
	writeReportHeader(reportFile, "MS-WARN", "develop", 1, "", ".cyclestone/reports/MS-WARN/cycle-001/metadata.json", RunOptions{}, warnings, nil)
	writeReportDetailf(reportFile, "\n## QA Phase\n\nverdict: approved\n")
	if err := reportFile.Close(); err != nil {
		t.Fatalf("failed to close report: %v", err)
	}

	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	var parsed cycleReportYAML
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("expected generated cycle report to be valid YAML: %v\n%s", err, string(content))
	}
	if len(parsed.InformationalWarnings) != 1 || !strings.Contains(parsed.InformationalWarnings[0], "tools/nested") {
		t.Fatalf("expected top-level informational warning in YAML, got %#v", parsed.InformationalWarnings)
	}
	if !strings.Contains(parsed.Details, "## Informational Warnings") {
		t.Fatalf("expected human-readable informational warning in details, got:\n%s", parsed.Details)
	}

	summary := summarizeCycleReport(reportPath)
	if strings.Contains(summary, "Informational Warnings") || strings.Contains(summary, "tools/nested") || strings.Contains(summary, "Embedded Git repository") {
		t.Fatalf("expected informational warnings to be excluded from recommender summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "verdict: approved") {
		t.Fatalf("expected normal continuation signal to remain in summary, got:\n%s", summary)
	}
}

func TestCycleReportSummaryIgnoresEmbeddedRepoOnlyBlockedQAExcerpt(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "MS-WARN-QA", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
	report := strings.Join([]string{
		`milestone_id: "MS-WARN-QA"`,
		`cycle: "001"`,
		`details: |-`,
		`  ## QA Phase`,
		``,
		`  verdict: blocked`,
		`  failing_checks:`,
		`    - Embedded Git repository detected at tools/nested without Cyclestone tracking. This is informational only and is excluded from recommender scoring; add it to repositories or .gitmodules if Cyclestone should manage it separately.`,
		`  required_fixes:`,
		`    - Do not treat them as acceptance gaps, required fixes, failing checks, or cycle-continuation score drivers unless the milestone explicitly targets repository topology.`,
		``,
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}
	summary := summarizeCycleReport(reportPath)
	for _, forbidden := range []string{
		"verdict: blocked",
		"tools/nested",
		"Embedded Git repository detected at",
		"required fixes",
	} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("expected embedded-repo-only QA excerpt to be excluded from summary, found %q in:\n%s", forbidden, summary)
		}
	}

	report = strings.Replace(report, "  required_fixes:\n    - Do not treat them as acceptance gaps, required fixes, failing checks, or cycle-continuation score drivers unless the milestone explicitly targets repository topology.", "  required_fixes:\n    - Update API docs", 1)
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Fatalf("failed to rewrite report: %v", err)
	}
	summary = summarizeCycleReport(reportPath)
	for _, expected := range []string{
		"verdict: blocked",
		"Update API docs",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("expected real QA signal %q to remain in summary, got:\n%s", expected, summary)
		}
	}
}

func TestRecommenderInputExcludesCycleReportInformationalWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	reportPath := filepath.Join(".cyclestone", "reports", "MS-REC-WARN", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	reportFile, err := os.Create(reportPath)
	if err != nil {
		t.Fatalf("failed to create report: %v", err)
	}
	warnings := []string{"Embedded Git repository detected at tools/nested without Cyclestone tracking. This is informational only and is excluded from recommender scoring; add it to repositories or .gitmodules if Cyclestone should manage it separately."}
	writeReportHeader(reportFile, "MS-REC-WARN", "develop", 1, "", ".cyclestone/reports/MS-REC-WARN/cycle-001/metadata.json", RunOptions{}, warnings, nil)
	writeReportDetailf(reportFile, "\n## QA Phase\n\nverdict: approved\n")
	if err := reportFile.Close(); err != nil {
		t.Fatalf("failed to close report: %v", err)
	}

	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact_%v", compact), func(t *testing.T) {
			compact := compact
			input := assembleInputWithSettings(
				config.Milestone{ID: "MS-REC-WARN", Goal: "score clean cycle"},
				config.Agent{ID: "recommender", Name: "Cycle Recommender", PromptBody: "Report:\n{{LATEST_CYCLE_REPORT}}"},
				1,
				RunOptions{},
				"",
				"",
				config.Settings{EnableCompactPhaseHandoffs: &compact},
				[]config.Agent{{ID: "qa", Name: "Quality Manager"}, {ID: "recommender", Name: "Cycle Recommender"}},
			)
			for _, forbidden := range []string{
				"Informational Warnings",
				"informational_warnings",
				"tools/nested",
				"Embedded Git repository",
			} {
				if strings.Contains(input, forbidden) {
					t.Fatalf("expected recommender input to exclude %q, got:\n%s", forbidden, input)
				}
			}
			if !strings.Contains(input, "verdict: approved") {
				t.Fatalf("expected recommender input to retain normal QA signal, got:\n%s", input)
			}
		})
	}
}

func TestCompactRecommenderInputStripsQAEmbeddedRepoInformationalWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	reportsDir := filepath.Join(".cyclestone", "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	pipeline := []config.Agent{
		{ID: "qa", Name: "Quality Manager"},
		{ID: "recommender", Name: "Cycle Recommender"},
	}
	qaHandoffPath := phaseHandoffPath(reportsDir, "MS-REC-HANDOFF", "001", getAgentFileID("qa", pipeline))
	if err := os.MkdirAll(filepath.Dir(qaHandoffPath), 0755); err != nil {
		t.Fatalf("failed to create QA handoff dir: %v", err)
	}
	handoff := strings.Join([]string{
		"milestone_id: MS-REC-HANDOFF",
		"cycle: 1",
		"agent_id: qa",
		"output_contract: qa",
		"validation_status: valid",
		"source_log: qa.log",
		"summary:",
		"  verdict: approved",
		"  reviewed_files:",
		"    - internal/executor/prompt.go",
		"  failing_checks:",
		"    - Embedded Git repository detected at tools/nested without Cyclestone tracking. This is informational only and is excluded from recommender scoring; add it to repositories or .gitmodules if Cyclestone should manage it separately.",
		"  required_fixes:",
		"    - Do not treat them as acceptance gaps, required fixes, failing checks, or cycle-continuation score drivers unless the milestone explicitly targets repository topology.",
		"  criteria_results: []",
	}, "\n")
	if err := os.WriteFile(qaHandoffPath, []byte(handoff), 0644); err != nil {
		t.Fatalf("failed to write QA handoff: %v", err)
	}

	compact := true
	input := assembleInputWithSettings(
		config.Milestone{ID: "MS-REC-HANDOFF", Goal: "score clean handoff"},
		config.Agent{ID: "recommender", Name: "Cycle Recommender", PromptBody: "Report:\n{{LATEST_CYCLE_REPORT}}"},
		1,
		RunOptions{},
		"",
		"",
		config.Settings{EnableCompactPhaseHandoffs: &compact},
		pipeline,
	)
	for _, forbidden := range []string{
		"tools/nested",
		"Embedded Git repository detected at",
		"Do not treat them as acceptance gaps",
		"excluded from recommender scoring; add it to repositories or .gitmodules",
	} {
		if strings.Contains(input, forbidden) {
			t.Fatalf("expected compact recommender input to strip %q, got:\n%s", forbidden, input)
		}
	}
	if !strings.Contains(input, "verdict: approved") || !strings.Contains(input, "internal/executor/prompt.go") {
		t.Fatalf("expected compact recommender input to retain normal QA handoff signal, got:\n%s", input)
	}
}

func TestPhaseInputsIncludeEmbeddedRepoInformationalWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Fatalf("failed to init root repo: %v", err)
	}
	if err := os.MkdirAll(filepath.Join("tools", "nested"), 0755); err != nil {
		t.Fatalf("failed to create nested repo dir: %v", err)
	}
	if err := exec.Command("git", "-C", filepath.Join("tools", "nested"), "init").Run(); err != nil {
		t.Fatalf("failed to init nested repo: %v", err)
	}
	gitContextPath := filepath.Join(tmpDir, "git-context.md")
	if err := os.WriteFile(gitContextPath, []byte("## root\n\nChanged files:\n\n```text\nA\ttools/nested\n```\n"), 0644); err != nil {
		t.Fatalf("failed to write git context: %v", err)
	}

	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("qa_compact_%v", compact), func(t *testing.T) {
			compact := compact
			gitContext := ""
			if !compact {
				gitContext = gitContextPath
			}
			input := assembleInputWithSettings(
				config.Milestone{ID: "MS-QA", Goal: "review nested repo warning"},
				config.Agent{ID: "qa", Name: "Quality Manager", PromptBody: "qa role"},
				1,
				RunOptions{},
				"",
				gitContext,
				config.Settings{EnableCompactPhaseHandoffs: &compact},
				[]config.Agent{{ID: "qa", Name: "Quality Manager"}},
			)
			for _, expected := range []string{
				"## Informational Warnings",
				"tools/nested",
				"Do not treat them as acceptance gaps",
			} {
				if !strings.Contains(input, expected) {
					t.Fatalf("expected QA input to contain %q, got:\n%s", expected, input)
				}
			}
		})
	}

	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("developer_compact_%v", compact), func(t *testing.T) {
			compact := compact
			gitContext := ""
			if !compact {
				gitContext = gitContextPath
			}
			devInput := assembleInputWithSettings(
				config.Milestone{ID: "MS-QA", Goal: "review nested repo warning"},
				config.Agent{ID: "developer", Name: "Developer", PromptBody: "dev role"},
				1,
				RunOptions{},
				"",
				gitContext,
				config.Settings{EnableCompactPhaseHandoffs: &compact},
				[]config.Agent{{ID: "developer", Name: "Developer"}},
			)
			for _, expected := range []string{
				"## Informational Warnings",
				"tools/nested",
				"Do not treat them as acceptance gaps",
			} {
				if !strings.Contains(devInput, expected) {
					t.Fatalf("expected Developer input to contain %q, got:\n%s", expected, devInput)
				}
			}
		})
	}

	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("pm_compact_%v", compact), func(t *testing.T) {
			compact := compact
			pmInput := assembleInputWithSettings(
				config.Milestone{ID: "MS-QA", Goal: "review nested repo warning"},
				config.Agent{ID: "pm", Name: "Project Manager", PromptBody: "pm role"},
				1,
				RunOptions{},
				"",
				gitContextPath,
				config.Settings{EnableCompactPhaseHandoffs: &compact},
				[]config.Agent{{ID: "pm", Name: "Project Manager"}},
			)
			for _, expected := range []string{
				"## Informational Warnings",
				"tools/nested",
				"Do not treat them as acceptance gaps",
			} {
				if !strings.Contains(pmInput, expected) {
					t.Fatalf("expected PM input to contain %q, got:\n%s", expected, pmInput)
				}
			}
		})
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
	previousReport := filepath.Join(reportsDir, "MS-YAML", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(previousReport), 0755); err != nil {
		t.Fatalf("failed to create previous report dir: %v", err)
	}
	if err := os.WriteFile(previousReport, []byte("milestone_id: MS-YAML\n"), 0644); err != nil {
		t.Fatalf("failed to write previous report: %v", err)
	}

	state := &config.State{}
	state.SetMilestoneCycles("MS-YAML", 1)
	_, _, previousReportPath, reportPath, _, _, _, _, err := prepareCycleEnvironment(RunOptions{NoBranchChange: true}, state, config.Milestone{ID: "MS-YAML"}, reportsDir)
	if err != nil {
		t.Fatalf("prepareCycleEnvironment failed: %v", err)
	}
	if previousReportPath != previousReport {
		t.Fatalf("expected previous YAML report path, got %q", previousReportPath)
	}
	if reportPath != filepath.Join(reportsDir, "MS-YAML", "cycle-002", "report.yaml") {
		t.Fatalf("expected current YAML report path, got %q", reportPath)
	}
}

func TestPrepareCycleEnvironmentWritesEmbeddedRepoWarningsToMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	if err := exec.Command("git", "init").Run(); err != nil {
		t.Fatalf("failed to init root repo: %v", err)
	}
	if err := os.MkdirAll(filepath.Join("tools", "nested"), 0755); err != nil {
		t.Fatalf("failed to create nested repo dir: %v", err)
	}
	if err := exec.Command("git", "-C", filepath.Join("tools", "nested"), "init").Run(); err != nil {
		t.Fatalf("failed to init nested repo: %v", err)
	}

	reportsDir := filepath.Join(".cyclestone", "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	_, _, _, _, metadataPath, _, warnings, _, err := prepareCycleEnvironment(RunOptions{NoBranchChange: true}, &config.State{}, config.Milestone{ID: "MS-META"}, reportsDir)
	if err != nil {
		t.Fatalf("prepareCycleEnvironment failed: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "tools/nested") {
		t.Fatalf("expected embedded repo warning from prepareCycleEnvironment, got %#v", warnings)
	}

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("failed to read metadata: %v", err)
	}
	var metadata CycleMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("failed to parse metadata: %v\n%s", err, string(data))
	}
	if len(metadata.InformationalWarnings) != 1 || !strings.Contains(metadata.InformationalWarnings[0], "tools/nested") {
		t.Fatalf("expected metadata informational warning, got %#v", metadata.InformationalWarnings)
	}
}

func TestSummarizeCycleReportParsesYAMLEnvelope(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "MS-YAML", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
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
	reportPath := filepath.Join(reportsDir, "MS-YAML", "cycle-001", "report.yaml")
	report := strings.Join([]string{
		`milestone_id: "MS-YAML"`,
		`started: "2026-07-02 10:00:00 -0500"`,
		`details: |-`,
		`  ## QA Phase`,
		``,
		`  verdict: approved`,
		``,
	}, "\n")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Fatalf("failed to write report: %v", err)
	}
	handoffPath := filepath.Join(reportsDir, "MS-YAML", "cycle-001", "01-pm", "handoff.yaml")
	if err := os.MkdirAll(filepath.Dir(handoffPath), 0755); err != nil {
		t.Fatalf("failed to create handoff dir: %v", err)
	}
	if err := os.WriteFile(handoffPath, []byte("summary:\n  scope: []\n"), 0644); err != nil {
		t.Fatalf("failed to write handoff: %v", err)
	}

	if err := updateCycleSummaryReport("MS-YAML", 1, reportsDir); err != nil {
		t.Fatalf("updateCycleSummaryReport failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(reportsDir, "MS-YAML", "summary.md"))
	if err != nil {
		t.Fatalf("failed to read summary report: %v", err)
	}
	summary := string(content)
	if !strings.Contains(summary, filepath.Join(reportsDir, "MS-YAML", "cycle-001", "report.yaml")+" (2026-07-02 10:00:00 -0500) - verdict: approved") {
		t.Fatalf("expected YAML metadata and details verdict in cycle summary, got:\n%s", summary)
	}
	if strings.Contains(summary, "handoff.yaml") {
		t.Fatalf("expected handoff YAML to be excluded from cycle summary, got:\n%s", summary)
	}
}

func TestSummarizeCycleReportMalformedYAMLFallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	reportPath := filepath.Join(tmpDir, "MS-YAML", "cycle-001", "report.yaml")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
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
	if err := os.WriteFile("AGENTS.md", []byte(largeContext), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
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
	if !strings.Contains(input, "[Content truncated: AGENTS.md") {
		t.Fatalf("expected truncation notice for AGENTS.md")
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

	agentsContent := "Constraint: Keep work inside {{WORKSPACE_ROOT}}."
	if err := os.WriteFile("AGENTS.md", []byte(agentsContent), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
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

func TestAssembleInputWithoutAgentsStillBuildsPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatalf("failed to create milestone dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "DECISIONS.md"), []byte("DECISION-CONTENT"), 0644); err != nil {
		t.Fatalf("failed to write decisions log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-NO-AGENTS.md"), []byte("# MS-NO-AGENTS\nMilestone body"), 0644); err != nil {
		t.Fatalf("failed to write milestone spec: %v", err)
	}

	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact_%v", compact), func(t *testing.T) {
			settings := config.Settings{EnableCompactPhaseHandoffs: &compact}
			input := assembleInputWithSettings(
				config.Milestone{ID: "MS-NO-AGENTS", Goal: "assemble without agents"},
				config.Agent{ID: "pm", Name: "Project Manager", PromptBody: "role prompt"},
				1,
				RunOptions{},
				"",
				"",
				settings,
				nil,
			)

			if !strings.Contains(input, "MS-NO-AGENTS") || !strings.Contains(input, "role prompt") {
				t.Fatalf("expected role and milestone content without AGENTS.md, got:\n%s", input)
			}
			if !strings.Contains(input, "DECISION-CONTENT") {
				t.Fatalf("expected decisions log to remain loaded, got:\n%s", input)
			}
		})
	}
}

func TestAssembleInputWithoutOptionalInstructionFilesStillBuildsPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	if err := os.MkdirAll(filepath.Join(".cyclestone", "milestones"), 0755); err != nil {
		t.Fatalf("failed to create milestone dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "milestones", "MS-NO-OPTIONAL-CONTEXT.md"), []byte("# MS-NO-OPTIONAL-CONTEXT\nMilestone body"), 0644); err != nil {
		t.Fatalf("failed to write milestone spec: %v", err)
	}

	for _, compact := range []bool{false, true} {
		for _, agentID := range []string{"pm", "qa", "recommender"} {
			t.Run(fmt.Sprintf("compact_%v_%s", compact, agentID), func(t *testing.T) {
				settings := config.Settings{EnableCompactPhaseHandoffs: &compact}
				input := assembleInputWithSettings(
					config.Milestone{ID: "MS-NO-OPTIONAL-CONTEXT", Goal: "assemble without optional context"},
					config.Agent{ID: agentID, Name: "Test Agent", PromptBody: "role prompt"},
					1,
					RunOptions{},
					"",
					"",
					settings,
					nil,
				)

				if !strings.Contains(input, "role prompt") {
					t.Fatalf("expected prompt to build without optional instruction files, got:\n%s", input)
				}
				if agentID != "recommender" && !strings.Contains(input, "MS-NO-OPTIONAL-CONTEXT") {
					t.Fatalf("expected phase prompt to include milestone context, got:\n%s", input)
				}
				for _, omitted := range []string{"## Agent Instructions", "## Decisions Log", "## QA Checklist"} {
					if strings.Contains(input, omitted) {
						t.Fatalf("expected missing optional section %q to be omitted, got:\n%s", omitted, input)
					}
				}
			})
		}
	}
}

func TestCompactRecommenderUsesProvidedAgentInstructionSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	if err := os.MkdirAll(filepath.Join(".cyclestone", "reports"), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	if err := os.WriteFile("AGENTS.md", []byte("DEFAULT-AGENTS-SHOULD-NOT-LOAD"), 0644); err != nil {
		t.Fatalf("failed to write default AGENTS.md: %v", err)
	}
	if err := os.WriteFile("CUSTOM_AGENTS.md", []byte("CUSTOM-AGENTS-SHOULD-LOAD"), 0644); err != nil {
		t.Fatalf("failed to write custom AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "DECISIONS.md"), []byte("DECISIONS-SHOULD-LOAD"), 0644); err != nil {
		t.Fatalf("failed to write decisions log: %v", err)
	}

	compactEnabled := true
	input := assembleInputWithSettings(
		config.Milestone{ID: "MS-CUSTOM", Goal: "custom instructions"},
		config.Agent{ID: "recommender", Name: "Recommender", PromptBody: "role prompt"},
		1,
		RunOptions{},
		"",
		"",
		config.Settings{
			EnableCompactPhaseHandoffs: &compactEnabled,
			AgentInstructions: config.AgentInstructionsSettings{
				File: "CUSTOM_AGENTS.md",
			},
		},
		nil,
	)

	if !strings.Contains(input, "CUSTOM-AGENTS-SHOULD-LOAD") {
		t.Fatalf("expected compact recommender input to load configured instruction file, got:\n%s", input)
	}
	if strings.Contains(input, "DEFAULT-AGENTS-SHOULD-NOT-LOAD") {
		t.Fatalf("expected compact recommender input not to reload default AGENTS.md, got:\n%s", input)
	}
	if !strings.Contains(input, "DECISIONS-SHOULD-LOAD") {
		t.Fatalf("expected compact recommender input to include decisions log, got:\n%s", input)
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
	args := buildAiderArgs("pm", "prompt.txt", "ollama_chat/glm-5.2:cloud", "")
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
	// Edit mode is always diff to avoid whole-file replacement truncation.
	if !sliceHas(args, "--edit-format", "diff") {
		t.Fatalf("expected --edit-format diff, got %v", args)
	}
	// Model is forwarded when provided.
	if !sliceHas(args, "--model", "ollama_chat/glm-5.2:cloud") {
		t.Fatalf("expected --model forwarded, got %v", args)
	}
}

func TestBuildAiderArgsDryRunForNonDeveloper(t *testing.T) {
	// Only the developer agent may modify repository files. All other agents
	// must run with --dry-run so Aider never writes to disk.
	nonDeveloperAgents := []string{"pm", "qa", "recommender", "custom-agent"}
	for _, agentID := range nonDeveloperAgents {
		args := buildAiderArgs(agentID, "prompt.txt", "ollama_chat/glm-5.2:cloud", "")
		if !sliceHas(args, "--dry-run") {
			t.Fatalf("expected --dry-run for non-developer agent %q, got %v", agentID, args)
		}
	}
}

func TestBuildAiderArgsNoDryRunForDeveloper(t *testing.T) {
	args := buildAiderArgs("developer", "prompt.txt", "ollama_chat/glm-5.2:cloud", "")
	if sliceHas(args, "--dry-run") {
		t.Fatalf("--dry-run must not be set for developer agent, got %v", args)
	}
}

func TestBuildAiderArgsIncludesHandoffFile(t *testing.T) {
	// The dedicated temp handoff file must be added to the Aider chat via
	// --file so the agent can write structured YAML to it. Without --file,
	// Aider refuses the agent's SEARCH/REPLACE for the handoff with a
	// "file not found" / "NoneType ... splitlines" error (the cycle-003
	// regression). All agents get --file; non-developer agents are still
	// guarded by --dry-run so source files cannot be modified.
	for _, agentID := range []string{"pm", "developer", "qa", "recommender"} {
		args := buildAiderArgs(agentID, "prompt.txt", "ollama_chat/glm-5.2:cloud", ".cyclestone/temp/ms-cycle-001-01-pm-handoff.yaml")
		if !sliceHas(args, "--file", ".cyclestone/temp/ms-cycle-001-01-pm-handoff.yaml") {
			t.Fatalf("expected --file <handoff> for %q, got %v", agentID, args)
		}
	}
}

func TestBuildAiderArgsOmitsHandoffFileWhenEmpty(t *testing.T) {
	args := buildAiderArgs("pm", "prompt.txt", "ollama_chat/glm-5.2:cloud", "")
	for _, v := range args {
		if v == "--file" {
			t.Fatalf("expected no --file when handoff path is empty, got %v", args)
		}
	}
}

func TestBuildAiderArgsNeverDisablesRepoMap(t *testing.T) {
	// --map-tokens 0 must never be used: it disables the repo map, which the
	// developer (and other agents) rely on to understand the codebase.
	for _, agentID := range []string{"pm", "developer", "qa", "recommender"} {
		args := buildAiderArgs(agentID, "prompt.txt", "ollama_chat/glm-5.2:cloud", "")
		for _, v := range args {
			if v == "--map-tokens" {
				t.Fatalf("--map-tokens must never be set for %q, got %v", agentID, args)
			}
		}
	}
}

func TestBuildAiderArgsOmitsModelWhenEmpty(t *testing.T) {
	args := buildAiderArgs("pm", "prompt.txt", "", "")
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
	if got := parseAgentInstructionsUpdateRecommendationScore(handoffPath); got != -1 {
		t.Fatalf("expected -1 (no AGENTS.md update recommendation) for recommender fallback, got %d", got)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if _, hasScore := handoff.Summary["score"]; hasScore {
		t.Fatalf("expected score to be absent from recommender fallback, got %#v", handoff.Summary["score"])
	}
	if _, hasScore := handoff.Summary["agent_instructions_update_score"]; hasScore {
		t.Fatalf("expected AGENTS.md update score to be absent from recommender fallback, got %#v", handoff.Summary["agent_instructions_update_score"])
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

func TestExtractFinalYAMLDocumentPrefersRepeatedRecommenderYAMLAfterTokenFooter(t *testing.T) {
	recommendation := strings.Join([]string{
		"score: 2",
		"verdict: approved",
		"reason: |",
		"  The latest QA report marks every milestone criterion as passing, with no",
		"  failing checks or required fixes.",
		"next_cycle_focus: []",
	}, "\n")
	text := strings.Join([]string{
		"► ANSWER",
		"",
		recommendation,
		"tokens used",
		"5.469",
		recommendation,
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected extraction to succeed, got error: %v", err)
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted YAML failed to parse: %v\nraw:\n%s", err, string(raw))
	}
	if score, ok := decoded["score"].(int); !ok || score != 2 {
		t.Fatalf("expected score=2, got %#v", decoded["score"])
	}
	if verdict, ok := decoded["verdict"].(string); !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", decoded["verdict"])
	}
	if strings.Contains(string(raw), "tokens used") {
		t.Fatalf("extracted YAML must not include token footer, got:\n%s", string(raw))
	}
}

// TestExtractFinalYAMLDocumentStripsAiderChatterFromSearchReplaceBlock is the
// cycle-003 PM regression: the agent emitted its handoff inside an Aider
// SEARCH/REPLACE block (because the dedicated temp handoff file could not be
// written), and Aider appended a token-usage summary plus edit/IO diagnostics
// after the answer. Before the fix, the greedy block-scalar flattening logic
// absorbed the ">>>>>>> REPLACE" fence, the "Tokens:" line, the temp-path
// echo, and the "file not found"/"NoneType ... splitlines" diagnostics into
// the second risks entry. The extracted risks must be clean.
func TestExtractFinalYAMLDocumentStripsAiderChatterFromSearchReplaceBlock(t *testing.T) {
	handoff := strings.Join([]string{
		"scope:",
		"  - Verify the ollama-codex runner is fully integrated across config, executor, and TUI",
		"non_goals:",
		"  - Do not change existing runner behavior",
		"risks:",
		"  - |",
		"    The Codex CLI argument order must match the existing codex runner exactly;",
		"    reuse the shared argument builder to avoid drift.",
		"  - |",
		"    Ensure tests follow existing patterns and do not depend on live network.",
	}, "\n")
	text := strings.Join([]string{
		"► THINKING",
		"I will create this file using a SEARCH/REPLACE block as required by my system instructions.",
		"--------------------------------------------------------------------------------",
		"► ANSWER",
		"",
		".cyclestone/temp/0001-introduce-new-llm-runner-cycle-003-01-pm-handoff.yaml",
		"",
		"<<<<<<< SEARCH",
		"=======",
		handoff,
		">>>>>>> REPLACE",
		"",
		"Tokens: 40k sent, 2.1k received.",
		"",
		".cyclestone/temp/0001-introduce-new-llm-runner-cycle-003-01-pm-handoff.yaml",
		"/home/patrick_dev/Develop/Cyclestone/.cyclestone/temp/0001-introduce-new-llm-run",
		"ner-cycle-003-01-pm-handoff.yaml: file not found error",
		"'NoneType' object has no attribute 'splitlines'",
		"Unable to read /home/patrick_dev/Develop/Cyclestone/.cyclestone/temp/0001-introduce-new-llm-runner-cycle-003-01-pm-handoff.yaml: [Errno 2] No such file or directory: '/home/patrick_dev/Develop/Cyclestone/.cyclestone/temp/0001-introduce-new-llm-runner-cycle-003-01-pm-handoff.yaml'",
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected extraction to succeed, got error: %v", err)
	}
	rawStr := string(raw)
	for _, frag := range []string{">>>>>>> REPLACE", "<<<<<<< SEARCH", "=======", "Tokens:", "file not found", "NoneType", "Unable to read", ".cyclestone/temp/", "-handoff.yaml"} {
		if strings.Contains(rawStr, frag) {
			t.Fatalf("extracted YAML must not contain chatter fragment %q, got:\n%s", frag, rawStr)
		}
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted YAML failed to parse: %v\nraw:\n%s", err, rawStr)
	}
	risks, ok := decoded["risks"].([]interface{})
	if !ok || len(risks) != 2 {
		t.Fatalf("expected 2 risks, got %#v", decoded["risks"])
	}
	first, _ := risks[0].(string)
	if !strings.Contains(first, "Codex CLI argument order") {
		t.Fatalf("unexpected first risk: %q", first)
	}
	second, _ := risks[1].(string)
	if !strings.Contains(second, "Ensure tests follow existing patterns") {
		t.Fatalf("unexpected second risk: %q", second)
	}
	if strings.Contains(second, "REPLACE") || strings.Contains(second, "Tokens") {
		t.Fatalf("second risk absorbed Aider chatter: %q", second)
	}
	scope, ok := decoded["scope"].([]interface{})
	if !ok || len(scope) != 1 {
		t.Fatalf("expected 1 scope entry, got %#v", decoded["scope"])
	}
}

// TestIsAiderChatterLine covers the chatter detector directly so future
// Aider output formats are caught by an explicit assertion.
func TestIsAiderChatterLine(t *testing.T) {
	chatter := []string{
		"<<<<<<< SEARCH",
		">>>>>>> REPLACE",
		"=======",
		"Tokens: 40k sent, 2.1k received.",
		"Tokens: 97k sent, 2.9k received.",
		"file not found error",
		".cyclestone/temp/0001-introduce-new-llm-runner-cycle-003-01-pm-handoff.yaml",
		"/home/patrick_dev/Develop/Cyclestone/.cyclestone/temp/0001-introduce-new-llm-runner-cycle-003-01-pm-handoff.yaml: file not found error",
		"'NoneType' object has no attribute 'splitlines'",
		"Unable to read /home/patrick_dev/Develop/Cyclestone/.cyclestone/temp/x-handoff.yaml: [Errno 2] No such file or directory: '...'",
	}
	for _, line := range chatter {
		if !isAiderChatterLine(line) {
			t.Errorf("expected %q to be chatter", line)
		}
	}
	legit := []string{
		"",
		"risks:",
		"  - |",
		"    Ensure tests follow existing patterns and do not depend on live network.",
		"verdict: approved",
		"next_cycle_focus: []",
		"Unable to read the report; please retry.", // prose, not a file-not-found diagnostic
		"========",                  // not exactly seven '='
		"Note: tokens used were 5.", // not the Aider token-summary shape
	}
	for _, line := range legit {
		if isAiderChatterLine(line) {
			t.Errorf("did not expect %q to be chatter", line)
		}
	}
}

// TestStripSearchReplaceWrapper covers the SEARCH/REPLACE block extractor
// directly: the cycle-003 QA regression where the Codex CLI wrote the fence
// markers literally into the dedicated temp handoff file.
func TestStripSearchReplaceWrapper(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name: "empty search section",
			input: strings.Join([]string{
				"<<<<<<< SEARCH",
				"=======",
				"verdict: approved",
				"reviewed_files: []",
				">>>>>>> REPLACE",
			}, "\n"),
			expect: "verdict: approved\nreviewed_files: []",
		},
		{
			name: "non-empty search section ignored",
			input: strings.Join([]string{
				"<<<<<<< SEARCH",
				"old content",
				"=======",
				"verdict: approved",
				">>>>>>> REPLACE",
			}, "\n"),
			expect: "verdict: approved",
		},
		{
			name:   "no wrapper returns unchanged",
			input:  "verdict: approved\nreviewed_files: []",
			expect: "verdict: approved\nreviewed_files: []",
		},
		{
			name:   "leading/trailing whitespace trimmed",
			input:  "\n\n<<<<<<< SEARCH\n=======\nverdict: approved\n>>>>>>> REPLACE\n\n",
			expect: "verdict: approved",
		},
		{
			name: "missing divider returns unchanged",
			input: strings.Join([]string{
				"<<<<<<< SEARCH",
				"verdict: approved",
				">>>>>>> REPLACE",
			}, "\n"),
			expect: strings.Join([]string{
				"<<<<<<< SEARCH",
				"verdict: approved",
				">>>>>>> REPLACE",
			}, "\n"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripSearchReplaceWrapper(tc.input)
			if got != tc.expect {
				t.Fatalf("expected %q, got %q", tc.expect, got)
			}
		})
	}
}

// TestParseAndValidateContractContentStripsSearchReplaceWrapper is the
// cycle-003 QA end-to-end regression: the Codex CLI runner wrote a valid QA
// contract YAML inside a SEARCH/REPLACE block to the dedicated temp handoff
// file. Before the fix, parseAndValidateContractContent failed with "malformed
// yaml" because the fence markers confused the YAML parser. After the fix the
// wrapper is stripped and the contract validates successfully.
func TestParseAndValidateContractContentStripsSearchReplaceWrapper(t *testing.T) {
	qaYAML := strings.Join([]string{
		"verdict: approved",
		"criteria_results:",
		"  - criterion: \"New runner is valid\"",
		"    result: pass",
		"    notes: |",
		"      The runner is registered in all required locations.",
		"  - criterion: \"Standard checks pass\"",
		"    result: pass",
		"    notes: |",
		"      go test ./... PASS.",
		"reviewed_files:",
		"  - internal/executor/executor.go",
		"  - internal/executor/executor_test.go",
		"failing_checks: []",
		"required_fixes: []",
	}, "\n")
	// Wrap the YAML in a SEARCH/REPLACE block exactly as the Codex CLI agent
	// wrote it to the temp handoff file.
	tempContent := strings.Join([]string{
		"<<<<<<< SEARCH",
		"=======",
		qaYAML,
		">>>>>>> REPLACE",
	}, "\n")

	result := parseAndValidateContractContent(tempContent, "qa")
	if result.Status != "valid" {
		t.Fatalf("expected valid status, got %q with errors %v", result.Status, result.Errors)
	}
	verdict, ok := result.Summary["verdict"].(string)
	if !ok || verdict != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", result.Summary["verdict"])
	}
	cr, ok := result.Summary["criteria_results"].([]interface{})
	if !ok || len(cr) != 2 {
		t.Fatalf("expected 2 criteria_results, got %#v", result.Summary["criteria_results"])
	}
}

// TestHandoffInstructionAider verifies that Aider-based runners (aider, ollama)
// receive the SEARCH/REPLACE block instruction in the handoff prompt, preserving
// the original behavior that Aider applies the edit and strips the fence markers.
func TestHandoffInstructionAider(t *testing.T) {
	for _, runner := range []string{"aider", "ollama"} {
		for _, agentID := range []string{"pm", "developer", "qa", "recommender"} {
			t.Run(runner+"_"+agentID, func(t *testing.T) {
				text := handoffInstruction(runner, agentID)
				if !strings.Contains(text, "You are running inside the Aider coding assistant") {
					t.Fatalf("expected Aider context for runner=%s, got:\n%s", runner, text)
				}
				if !strings.Contains(text, "<<<<<<< SEARCH") {
					t.Fatalf("expected SEARCH/REPLACE instruction for runner=%s, got:\n%s", runner, text)
				}
				if !strings.Contains(text, "{{HANDOFF_YAML_PATH}}") {
					t.Fatalf("expected {{HANDOFF_YAML_PATH}} placeholder for runner=%s", runner)
				}
			})
		}
	}
}

// TestHandoffInstructionNonAider verifies that non-Aider runners (codex,
// ollama-codex) receive the direct-write instruction: no Aider context, no
// SEARCH/REPLACE fences, and an explicit instruction to write clean YAML
// directly to the file.
func TestHandoffInstructionNonAider(t *testing.T) {
	for _, runner := range []string{"codex", "ollama-codex"} {
		for _, agentID := range []string{"pm", "developer", "qa", "recommender"} {
			t.Run(runner+"_"+agentID, func(t *testing.T) {
				text := handoffInstruction(runner, agentID)
				if strings.Contains(text, "You are running inside the Aider") {
					t.Fatalf("non-Aider runner=%s must not mention Aider, got:\n%s", runner, text)
				}
				if strings.Contains(text, "<<<<<<< SEARCH") {
					t.Fatalf("non-Aider runner=%s must not contain SEARCH fence, got:\n%s", runner, text)
				}
				if !strings.Contains(text, "overwriting the file") {
					t.Fatalf("non-Aider runner=%s must instruct direct write, got:\n%s", runner, text)
				}
				if !strings.Contains(text, "{{HANDOFF_YAML_PATH}}") {
					t.Fatalf("expected {{HANDOFF_YAML_PATH}} placeholder for runner=%s", runner)
				}
				if !strings.Contains(text, "Do **not** wrap it in SEARCH/REPLACE block markers") {
					t.Fatalf("non-Aider runner=%s must warn against SEARCH/REPLACE markers, got:\n%s", runner, text)
				}
			})
		}
	}
}

// TestHandoffInstructionAgentSpecificText verifies that each agent gets its
// correct role sentence and consequence text.
func TestHandoffInstructionAgentSpecificText(t *testing.T) {
	tests := []struct {
		agentID         string
		roleFragment    string
		consequenceFrag string
	}{
		{"pm", "Project Manager", "your plan cannot be recorded"},
		{"developer", "implementation work", "QA has nothing to review"},
		{"qa", "Quality Manager", "your verdict is lost"},
		{"recommender", "score and verdict are lost", "score and verdict are lost"},
	}
	for _, tc := range tests {
		t.Run(tc.agentID, func(t *testing.T) {
			text := handoffInstruction("ollama-codex", tc.agentID)
			if !strings.Contains(text, tc.roleFragment) {
				t.Fatalf("agent=%s expected role fragment %q, got:\n%s", tc.agentID, tc.roleFragment, text)
			}
			if !strings.Contains(text, tc.consequenceFrag) {
				t.Fatalf("agent=%s expected consequence fragment %q, got:\n%s", tc.agentID, tc.consequenceFrag, text)
			}
		})
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

// --- Regression tests for collapsed-key normalization (cycle 0001 real output) ---

// TestExtractFinalYAMLDocumentParsesCollapsedQAHandoff reproduces the exact
// malformation seen in the real ollama/glm-5.2:cloud QA output of milestone
// cycle 0001: several mapping keys collapsed onto single lines
// ("verdict: approved criteria_results:", "result: pass notes: \"...\""), a
// multi-line double-quoted criterion value whose closing quote is followed by
// more merged keys on the continuation line, a top-level key
// ("reviewed_files:") appended after a quoted value closes, and a final list
// item with two trailing top-level keys. Before the merged-key normalization
// the document failed to parse and the QA handoff fell back, losing the
// verdict and criteria_results.
func TestExtractFinalYAMLDocumentParsesCollapsedQAHandoff(t *testing.T) {
	text := "► THINKING\n\nI should output the QA report.\n\n--------------------------------------------------------------------------------\n\n► ANSWER\n\n" + strings.Join([]string{
		`verdict: approved criteria_results:`,
		``,
		` • criterion: "Add ollama-codex runner that launches Codex CLI through Ollama"`,
		`   result: pass notes: "Implemented in internal/executor/executor.go via`,
		`   runOllamaCodex and buildOllamaCodexCommand."`,
		` • criterion: "Extract buildCodexArgs from buildCodexCommand" result: pass`,
		`   notes: "buildCodexArgs is now a separate function used by both`,
		`   buildCodexCommand and buildOllamaCodexCommand."`,
		` • criterion: "Runner selectable in setup when both ollama and codex are on`,
		`   PATH" result: pass notes: "internal/tui/runner_availability.go checks for`,
		`   both binaries."`,
		` • criterion: "EnableCodexSessionResume honored for ollama-codex" result: pass`,
		`   notes: "runOllamaCodex includes resume logic and fallback retry identical to`,
		`   runCodex." reviewed_files:`,
		` • ".cyclestone/DECISIONS.md"`,
		` • "README.md"`,
		` • "internal/config/settings.go"`,
		` • "internal/executor/executor.go"`,
		` • "internal/tui/settings.go" failing_checks: [] required_fixes: []`,
		``,
		`Tokens: 51k sent, 1.9k received.`,
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected extraction to succeed, got error: %v", err)
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted YAML failed to parse: %v\nraw:\n%s", err, string(raw))
	}
	if v, _ := decoded["verdict"].(string); v != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", decoded["verdict"])
	}
	cr, ok := decoded["criteria_results"].([]interface{})
	if !ok || len(cr) != 4 {
		t.Fatalf("expected 4 criteria_results, got %#v", decoded["criteria_results"])
	}
	// The third criterion has a multi-line quoted value whose closing quote is
	// followed by merged keys on the continuation line; verify it survived.
	item, _ := cr[2].(map[string]interface{})
	if c, _ := item["criterion"].(string); c != "Runner selectable in setup when both ollama and codex are on PATH" {
		t.Fatalf("expected folded criterion text, got %q", c)
	}
	if r, _ := item["result"].(string); r != "pass" {
		t.Fatalf("expected result=pass, got %q", r)
	}
	if n, _ := item["notes"].(string); n != "internal/tui/runner_availability.go checks for both binaries." {
		t.Fatalf("expected folded notes, got %q", n)
	}
	// reviewed_files was appended after a quoted value closed; it must be a
	// top-level array, not a nested string.
	rf, ok := decoded["reviewed_files"].([]interface{})
	if !ok || len(rf) != 5 {
		t.Fatalf("expected 5 reviewed_files, got %#v", decoded["reviewed_files"])
	}
	for _, fc := range []string{"failing_checks", "required_fixes"} {
		if arr, ok := decoded[fc].([]interface{}); !ok || len(arr) != 0 {
			t.Fatalf("expected %s=[], got %#v", fc, decoded[fc])
		}
	}
	// The fallback must not have leaked THINKING-section content.
	if _, ok := decoded["implemented_behavior"]; ok {
		t.Fatalf("QA document must not contain implemented_behavior")
	}
}

// TestWritePhaseHandoffOllamaQAParsesCollapsedKeys is the end-to-end regression
// for the QA agent: a real-style ollama QA log with collapsed mapping keys and
// multi-line quoted values must produce a non-fallback handoff retaining the
// verdict and structured criteria_results.
func TestWritePhaseHandoffOllamaQAParsesCollapsedKeys(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "qa.log")
	handoffPath := filepath.Join(tmpDir, "qa-handoff.yaml")
	text := "► ANSWER\n\n" + strings.Join([]string{
		`verdict: approved criteria_results:`,
		``,
		` • criterion: "No code changes required" result: pass notes: "Nothing to do."`,
		`reviewed_files: [] failing_checks: [] required_fixes: []`,
		``,
		`Tokens: 8k sent, 1k received.`,
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-Q", 1, "qa", "qa", logPath, 1000, "", "ollama"); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		t.Fatalf("failed to load handoff: %v", err)
	}
	if handoff.Fallback {
		t.Fatalf("expected non-fallback handoff from collapsed YAML, got fallback=true")
	}
	if v, _ := handoff.Summary["verdict"].(string); v != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", handoff.Summary["verdict"])
	}
	cr, _ := handoff.Summary["criteria_results"].([]interface{})
	if len(cr) != 1 {
		t.Fatalf("expected 1 criteria_results item, got %#v", handoff.Summary["criteria_results"])
	}
}

// TestExtractFinalYAMLDocumentParsesCollapsedRecommenderHandoff reproduces the
// real ollama/glm-5.2:cloud recommender output of cycle 0001: the three
// top-level keys ("score: 2 verdict: approved reason: |") collapsed onto one
// line, the block-scalar content flattened to column 0, and the final
// top-level key ("next_cycle_focus: []") appended to the last content line.
// Before the fix the structured handoff fell back, leaving verdict empty and
// score lost.
func TestExtractFinalYAMLDocumentParsesCollapsedRecommenderHandoff(t *testing.T) {
	text := "► ANSWER                                                                        \n\n" + strings.Join([]string{
		`score: 2 verdict: approved reason: | The latest cycle report shows the QA agent`,
		`approved the implementation with no required fixes. The goal to introduce the`,
		`"Ollama via Codex" runner has been met. Tests were added following existing`,
		`patterns. The report is marked as a fallback handoff, but the visible QA`,
		`verdict is "approved" with empty required_fixes, indicating the implementation`,
		`is complete and passing. next_cycle_focus: []`,
		``,
		`Tokens: 36k sent, 1.5k received.`,
	}, "\n")

	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		t.Fatalf("expected extraction to succeed, got error: %v", err)
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(raw, &decoded); err != nil {
		t.Fatalf("extracted YAML failed to parse: %v\nraw:\n%s", err, string(raw))
	}
	if sc, _ := numericValueAsIntInRange(decoded["score"], 0, 10); sc != 2 {
		t.Fatalf("expected score=2, got %#v", decoded["score"])
	}
	if v, _ := decoded["verdict"].(string); v != "approved" {
		t.Fatalf("expected verdict=approved, got %#v", decoded["verdict"])
	}
	reason, _ := decoded["reason"].(string)
	if !strings.Contains(reason, "The latest cycle report") || !strings.Contains(reason, "complete and passing.") {
		t.Fatalf("expected reason to retain flattened content, got %q", reason)
	}
	if focus, ok := decoded["next_cycle_focus"].([]interface{}); !ok || len(focus) != 0 {
		t.Fatalf("expected next_cycle_focus=[], got %#v", decoded["next_cycle_focus"])
	}
}

// TestNormalizeMergedKeysPreservesValidSingleKeyLines ensures the merged-key
// splitter does not alter already-valid YAML lines (single key per line,
// multi-word scalar values, empty collections) or block-scalar content.
func TestNormalizeMergedKeysPreservesValidSingleKeyLines(t *testing.T) {
	raw := strings.Join([]string{
		"verdict: approved",
		"criteria_results:",
		"  - criterion: test",
		"    result: fail",
		"reviewed_files:",
		"  - internal/executor/executor.go",
		"  - a multi word scalar value",
		"reason: |",
		"  The reason text mentions verdict: approved and next_cycle_focus: none",
		"  but must stay intact as block-scalar content.",
		"next_cycle_focus: []",
	}, "\n")
	normalized := normalizeMergedKeys([]byte(raw))
	if string(normalized) != raw {
		t.Fatalf("expected valid YAML unchanged, got:\n%s", string(normalized))
	}
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap(normalizeHandoffYAML([]byte(raw)), &decoded); err != nil {
		t.Fatalf("normalized YAML failed to parse: %v", err)
	}
}
