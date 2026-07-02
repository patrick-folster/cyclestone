package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
)

func TestExecutionCeilingLimit(t *testing.T) {
	// Create temporary files for test
	tmpDir, err := os.MkdirTemp("", "executor_test_ceiling")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Chdir(tmpDir)

	statePath := filepath.Join(tmpDir, "state.json")
	configPath := filepath.Join(tmpDir, "milestone.yml")
	outputPath := filepath.Join(tmpDir, "output.log")

	// Set up temporary local settings path
	localCyclestoneDir := filepath.Join(".", ".cyclestone")
	err = os.MkdirAll(localCyclestoneDir, 0755)
	if err != nil {
		t.Fatalf("failed to create .cyclestone: %v", err)
	}

	// Configure MaxModelCallsPerPhase to 3
	testSettings := config.Settings{
		DefaultLLM:            "ollama_api",
		MaxModelCallsPerPhase: 3,
	}
	settingsBytes, _ := json.Marshal(testSettings)
	os.WriteFile(filepath.Join(localCyclestoneDir, "settings.yml"), settingsBytes, 0644)

	// Initialize milestone state
	st := &config.State{
		MilestoneStatuses: map[string]string{
			"test-milestone": "Todo",
		},
		History: map[string][]config.MilestoneCycleLog{
			"test-milestone": {
				{
					CycleNumber: 1,
					Status:      "failed",
				},
			},
		},
	}
	stateBytes, _ := json.Marshal(st)
	os.WriteFile(statePath, stateBytes, 0644)

	// Set up mock Ollama server that always returns a unique tool call each turn
	var serverCallCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		serverCallCount++
		// Respond with a stream of 1 chunk containing a unique tool call in Ollama's native format
		// (arguments is a JSON object, no index/id/type fields)
		chunk := fmt.Sprintf(`{"message": {"role": "assistant", "content": "", "tool_calls": [{"function": {"name": "run_command", "arguments": {"command": "echo %d"}}}]}, "done": true}`, serverCallCount)
		w.Write([]byte(chunk + "\n"))
	}))
	defer server.Close()

	// Update local settings to point OllamaHost to our mock server
	testSettings.OllamaHost = server.URL
	testSettings.OllamaModel = "llama3"
	settingsBytes, _ = json.Marshal(testSettings)
	os.WriteFile(filepath.Join(localCyclestoneDir, "settings.yml"), settingsBytes, 0644)

	opts := RunOptions{
		StatePath:      statePath,
		ConfigPath:     configPath,
		NoBranchChange: true,
	}

	ctx := context.Background()
	exitCode, runErr := runLLMOrScript(ctx, "ollama_api", "test-agent", "TestAgent", "initial prompt", outputPath, opts, nil)

	// Since ceiling is 3, and we always return a tool call, we should exceed the limit of 3.
	if exitCode != 3 {
		t.Errorf("expected exit code 3 for ceiling limit, got %d", exitCode)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "model call limit of 3 exceeded") {
		t.Errorf("expected ceiling limit error, got %v", runErr)
	}

	// Verify status in state.json is set to failed
	updatedState, err := config.LoadState(statePath)
	if err != nil {
		t.Fatalf("failed to load updated state: %v", err)
	}
	if updatedState.MilestoneStatuses["test-milestone"] != "Failed" {
		t.Errorf("expected milestone status Failed, got %s", updatedState.MilestoneStatuses["test-milestone"])
	}
}

func TestRunAgentPipelineCancellationDoesNotBlockWithoutListener(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	if err := os.MkdirAll(filepath.Join(".cyclestone", "reports"), 0755); err != nil {
		t.Fatalf("failed to create reports dir: %v", err)
	}
	reportFile, err := os.Create(filepath.Join(".cyclestone", "reports", "MS-CANCEL-cycle-001.md"))
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

func TestDuplicateToolCallDetector(t *testing.T) {
	// Create temporary files for test
	tmpDir, err := os.MkdirTemp("", "executor_test_dup")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Chdir(tmpDir)

	statePath := filepath.Join(tmpDir, "state.json")
	configPath := filepath.Join(tmpDir, "milestone.yml")
	outputPath := filepath.Join(tmpDir, "output.log")

	localCyclestoneDir := filepath.Join(".", ".cyclestone")
	err = os.MkdirAll(localCyclestoneDir, 0755)
	if err != nil {
		t.Fatalf("failed to create .cyclestone: %v", err)
	}

	testSettings := config.Settings{
		DefaultLLM:            "ollama_api",
		MaxModelCallsPerPhase: 10,
	}

	// Set up mock Ollama server that returns consecutive duplicates
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse request messages to determine the turn
		type ollamaPayload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}

		bodyBytes, _ := io.ReadAll(r.Body)
		var payload ollamaPayload
		_ = json.Unmarshal(bodyBytes, &payload)

		// Check warning in history
		hasWarning := false
		for _, m := range payload.Messages {
			if strings.Contains(m.Content, "System Warning: The tool call you just requested") {
				hasWarning = true
			}
		}

		var chunk string
		if hasWarning {
			// This is Turn 3 (first warning was injected, model still requests duplicate)
			chunk = `{"message": {"role": "assistant", "content": "", "tool_calls": [{"function": {"name": "run_command", "arguments": {"command": "echo dup"}}}]}, "done": true}`
		} else if len(payload.Messages) >= 3 {
			// This is Turn 2 (first tool execution returned an error, model requests duplicate)
			chunk = `{"message": {"role": "assistant", "content": "", "tool_calls": [{"function": {"name": "run_command", "arguments": {"command": "echo dup"}}}]}, "done": true}`
		} else {
			// This is Turn 1
			chunk = `{"message": {"role": "assistant", "content": "", "tool_calls": [{"function": {"name": "run_command", "arguments": {"command": "echo dup"}}}]}, "done": true}`
		}
		w.Write([]byte(chunk + "\n"))
	}))
	defer server.Close()

	testSettings.OllamaHost = server.URL
	testSettings.OllamaModel = "llama3"
	settingsBytes, _ := json.Marshal(testSettings)
	os.WriteFile(filepath.Join(localCyclestoneDir, "settings.yml"), settingsBytes, 0644)

	opts := RunOptions{
		StatePath:      statePath,
		ConfigPath:     configPath,
		NoBranchChange: true,
	}

	ctx := context.Background()
	exitCode, runErr := runLLMOrScript(ctx, "ollama_api", "test-agent", "TestAgent", "initial prompt", outputPath, opts, nil)

	// Since consecutive duplicate was called twice consecutively, we should abort with exit code 2.
	if exitCode != 2 {
		t.Errorf("expected exit code 2 for duplicate tool calls, got %d", exitCode)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "consecutive duplicate tool call detected: run_command") {
		t.Errorf("expected duplicate tool call abort error, got %v", runErr)
	}

	// Verify output log contains warning
	outBytes, _ := os.ReadFile(outputPath)
	outputContent := string(outBytes)
	if !strings.Contains(outputContent, "[Guard Rail] Duplicate tool call detected. Injecting system warning for run_command.") {
		t.Errorf("expected duplicate warning in output log, got: %s", outputContent)
	}
}

func TestOllamaHistorySerializesToolArgumentsAsObject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor_ollama_history_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Chdir(tmpDir)

	localCyclestoneDir := filepath.Join(".", ".cyclestone")
	if err := os.MkdirAll(localCyclestoneDir, 0755); err != nil {
		t.Fatalf("failed to create .cyclestone: %v", err)
	}

	var requests []map[string]interface{}
	serverCallCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		serverCallCount++

		bodyBytes, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			t.Fatalf("failed to unmarshal request payload: %v", err)
		}
		requests = append(requests, payload)

		if serverCallCount == 1 {
			w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"run_command","arguments":{"command":"echo ok"}}}]},"done":true}` + "\n"))
			return
		}
		w.Write([]byte(`{"message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop"}` + "\n"))
	}))
	defer server.Close()

	settingsBytes, _ := json.Marshal(config.Settings{
		DefaultLLM:            "ollama_api",
		OllamaHost:            server.URL,
		OllamaModel:           "llama3",
		MaxLLMInputChars:      900000,
		MaxModelCallsPerPhase: 5,
	})
	if err := os.WriteFile(filepath.Join(localCyclestoneDir, "settings.yml"), settingsBytes, 0644); err != nil {
		t.Fatalf("failed to write settings: %v", err)
	}

	outputPath := filepath.Join(tmpDir, "output.log")
	exitCode, runErr := runLLMOrScript(context.Background(), "ollama_api", "test-agent", "TestAgent", "initial prompt", outputPath, RunOptions{}, nil)
	if exitCode != 0 || runErr != nil {
		t.Fatalf("expected successful ollama run, exit=%d err=%v", exitCode, runErr)
	}
	if len(requests) < 2 {
		t.Fatalf("expected at least 2 Ollama requests, got %d", len(requests))
	}

	messages, ok := requests[1]["messages"].([]interface{})
	if !ok {
		t.Fatalf("expected second request messages array, got %#v", requests[1]["messages"])
	}
	var foundToolCall bool
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok {
			continue
		}
		toolCalls, ok := message["tool_calls"].([]interface{})
		if !ok || len(toolCalls) == 0 {
			continue
		}
		foundToolCall = true
		toolCall := toolCalls[0].(map[string]interface{})
		function := toolCall["function"].(map[string]interface{})
		if _, ok := function["arguments"].(map[string]interface{}); !ok {
			t.Fatalf("expected Ollama history tool arguments to be object, got %#v", function["arguments"])
		}
	}
	if !foundToolCall {
		t.Fatalf("expected second request to include assistant tool call history, got %#v", messages)
	}
}

func TestOllamaPayloadIncludesOptionsOnlyWhenConfigured(t *testing.T) {
	for _, tc := range []struct {
		name        string
		settings    config.Settings
		wantCtx     float64
		wantPredict float64
	}{
		{
			name: "configured",
			settings: config.Settings{
				DefaultLLM:            "ollama_api",
				OllamaModel:           "llama3",
				OllamaKeepAlive:       "30m",
				OllamaNumCtx:          32768,
				OllamaNumPredict:      4096,
				MaxLLMInputChars:      900000,
				MaxModelCallsPerPhase: 5,
			},
			wantCtx:     32768,
			wantPredict: 4096,
		},
		{
			name: "unset",
			settings: config.Settings{
				DefaultLLM:            "ollama_api",
				OllamaModel:           "llama3",
				MaxLLMInputChars:      900000,
				MaxModelCallsPerPhase: 5,
			},
			wantCtx:     8192,
			wantPredict: 4096,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "executor_ollama_payload_test")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			oldHome := os.Getenv("HOME")
			oldUserProfile := os.Getenv("USERPROFILE")
			oldWd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get wd: %v", err)
			}
			os.Setenv("HOME", tmpDir)
			os.Setenv("USERPROFILE", tmpDir)
			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("failed to chdir: %v", err)
			}
			defer func() {
				os.Setenv("HOME", oldHome)
				os.Setenv("USERPROFILE", oldUserProfile)
				_ = os.Chdir(oldWd)
			}()

			var payload map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodyBytes, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(bodyBytes, &payload); err != nil {
					t.Fatalf("failed to unmarshal request payload: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop"}` + "\n"))
			}))
			defer server.Close()

			tc.settings.OllamaHost = server.URL
			if err := config.SaveProjectSettings(tc.settings); err != nil {
				t.Fatalf("failed to save settings: %v", err)
			}

			outputPath := filepath.Join(tmpDir, "output.log")
			exitCode, runErr := runLLMOrScript(context.Background(), "ollama_api", "test-agent", "TestAgent", "prompt", outputPath, RunOptions{}, nil)
			if exitCode != 0 || runErr != nil {
				t.Fatalf("expected successful ollama run, exit=%d err=%v", exitCode, runErr)
			}

			if got := payload["keep_alive"]; got != "30m" && tc.name == "configured" {
				t.Fatalf("expected configured keep_alive 30m, got %#v", got)
			}
			if got := payload["keep_alive"]; got != "5m" && tc.name == "unset" {
				t.Fatalf("expected default keep_alive 5m, got %#v", got)
			}

			options, hasOptions := payload["options"].(map[string]interface{})
			if !hasOptions {
				t.Fatalf("expected options in payload: %#v", payload)
			}
			if options["num_ctx"] != tc.wantCtx || options["num_predict"] != tc.wantPredict {
				t.Fatalf("unexpected options: %#v", options)
			}
		})
	}
}

func TestAPIMetricsWrittenForSuccessLimitDuplicateAndError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor_metrics_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("USERPROFILE", oldUserProfile)
		_ = os.Chdir(oldWd)
	}()

	runCase := func(name string, maxCalls int, handler http.HandlerFunc, wantExit int, wantReason string) string {
		t.Helper()
		server := httptest.NewServer(handler)
		defer server.Close()

		if err := config.SaveProjectSettings(config.Settings{
			DefaultLLM:            "ollama_api",
			OllamaHost:            server.URL,
			OllamaModel:           "llama3",
			MaxModelCallsPerPhase: maxCalls,
			MaxLLMInputChars:      900000,
		}); err != nil {
			t.Fatalf("%s: failed to save settings: %v", name, err)
		}

		outputPath := filepath.Join(tmpDir, name+".log")
		exitCode, _ := runLLMOrScript(context.Background(), "ollama_api", "test-agent", "TestAgent", "prompt", outputPath, RunOptions{StatePath: filepath.Join(tmpDir, "state.json")}, nil)
		if exitCode != wantExit {
			t.Fatalf("%s: expected exit %d, got %d", name, wantExit, exitCode)
		}
		outBytes, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("%s: failed to read output: %v", name, err)
		}
		output := string(outBytes)
		if !strings.Contains(output, "[Metrics] runner=ollama_api model=llama3") {
			t.Fatalf("%s: expected metrics line, got:\n%s", name, output)
		}
		if wantReason != "" && !strings.Contains(output, "stop_or_done_reason="+wantReason) {
			t.Fatalf("%s: expected reason %q, got:\n%s", name, wantReason, output)
		}
		return output
	}

	successOut := runCase("success", 5, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":{"role":"assistant","content":"done"},"done":true,"done_reason":"stop"}` + "\n"))
	}, 0, "stop")
	if !strings.Contains(successOut, "model_calls=1 output_chars=4 tool_calls=0") {
		t.Fatalf("success metrics missing expected counts:\n%s", successOut)
	}

	limitCalls := 0
	limitOut := runCase("limit", 1, func(w http.ResponseWriter, r *http.Request) {
		limitCalls++
		w.Write([]byte(fmt.Sprintf(`{"message":{"role":"assistant","tool_calls":[{"function":{"name":"run_command","arguments":{"command":"echo %d"}}}]},"done":true}`, limitCalls) + "\n"))
	}, 3, "")
	if !strings.Contains(limitOut, "model_calls=1") || !strings.Contains(limitOut, "tool_calls=1") {
		t.Fatalf("limit metrics missing expected counts:\n%s", limitOut)
	}

	duplicateOut := runCase("duplicate", 10, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":{"role":"assistant","tool_calls":[{"function":{"name":"run_command","arguments":{"command":"echo dup"}}}]},"done":true}` + "\n"))
	}, 2, "")
	if !strings.Contains(duplicateOut, "model_calls=3") || !strings.Contains(duplicateOut, "tool_calls=3") {
		t.Fatalf("duplicate metrics missing expected counts:\n%s", duplicateOut)
	}

	errorOut := runCase("api-error", 5, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}, 1, "")
	if !strings.Contains(errorOut, "model_calls=1") {
		t.Fatalf("api error metrics missing model call count:\n%s", errorOut)
	}
}

func TestEstimatedTokenBudgetAbortsBeforeModelCall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor_token_budget_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	oldUserProfile := os.Getenv("USERPROFILE")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("USERPROFILE", oldUserProfile)
		_ = os.Chdir(oldWd)
	}()

	var serverCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		w.Write([]byte(`{"message":{"role":"assistant","content":"done"},"done":true}` + "\n"))
	}))
	defer server.Close()

	if err := config.SaveProjectSettings(config.Settings{
		DefaultLLM:             "ollama_api",
		OllamaHost:             server.URL,
		OllamaModel:            "llama3",
		MaxModelCallsPerPhase:  5,
		MaxTokenBudgetPerPhase: 1,
		MaxLLMInputChars:       900000,
	}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	outputPath := filepath.Join(tmpDir, "output.log")
	exitCode, runErr := runLLMOrScript(context.Background(), "ollama_api", "test-agent", "TestAgent", "prompt", outputPath, RunOptions{}, nil)
	if exitCode != 4 {
		t.Fatalf("expected exit 4 for token budget, got %d", exitCode)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "estimated token budget") {
		t.Fatalf("expected token budget error, got %v", runErr)
	}
	if serverCalls != 0 {
		t.Fatalf("expected token budget guard before model call, got %d server calls", serverCalls)
	}
	outBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	if !strings.Contains(string(outBytes), "[Guard Rail] Estimated token budget") {
		t.Fatalf("expected token budget guard in output, got:\n%s", string(outBytes))
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

func TestGeminiFinishReasonCapturedInMetrics(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`[
				{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}
			]`)),
			Request: req,
		}, nil
	})}
	defer func() { http.DefaultClient = oldClient }()

	writer := &liveLogWriter{}
	result, err := executeAPI(
		context.Background(),
		"gemini",
		"gemini-test",
		"test-key",
		[]UnifiedMessage{{Role: "user", Content: "prompt"}},
		config.Settings{},
		writer,
		true,
	)
	if err != nil {
		t.Fatalf("executeAPI returned error: %v", err)
	}
	if result.Message.Content != "done" {
		t.Fatalf("expected response content done, got %q", result.Message.Content)
	}
	if result.Metrics.StopOrDoneReason != "STOP" {
		t.Fatalf("expected Gemini finish reason STOP, got %q", result.Metrics.StopOrDoneReason)
	}
	if result.Metrics.OutputCharCount != 4 || result.Metrics.ToolCallCount != 0 {
		t.Fatalf("unexpected metrics: %#v", result.Metrics)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
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

	exitCode, runErr := runLLMOrScript(context.Background(), "codex", "test-agent", "TestAgent", input, outputPath, RunOptions{}, nil)
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

	previousReportPath := filepath.Join(".cyclestone", "reports", "MS-LIMIT-cycle-001.md")
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

	reportPath := filepath.Join(".cyclestone", "reports", "MS-REC-cycle-002.md")
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

func TestWritePhaseHandoffParsesJSONOrFallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	jsonLog := filepath.Join(tmpDir, "pm.log")
	jsonHandoff := filepath.Join(tmpDir, "pm-handoff.json")
	if err := os.WriteFile(jsonLog, []byte("report\n```json\n{\"scope\":[\"one\"],\"risks\":[\"low\"]}\n```\n"), 0644); err != nil {
		t.Fatalf("failed to write json log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, jsonHandoff, "MS-H", 1, "pm", "", jsonLog, 1000, "Test human comment"); err != nil {
		t.Fatalf("writePhaseHandoff JSON failed: %v", err)
	}
	jsonBytes, err := os.ReadFile(jsonHandoff)
	if err != nil {
		t.Fatalf("failed to read json handoff: %v", err)
	}
	if !strings.Contains(string(jsonBytes), "\"milestone_id\": \"MS-H\"") ||
		!strings.Contains(string(jsonBytes), "\"agent_id\": \"pm\"") ||
		!strings.Contains(string(jsonBytes), "\"human_input\": \"Test human comment\"") ||
		!strings.Contains(string(jsonBytes), "\"summary\"") ||
		!strings.Contains(string(jsonBytes), "\"scope\"") ||
		strings.Contains(string(jsonBytes), "\"fallback\"") {
		t.Fatalf("expected parsed json handoff, got:\n%s", string(jsonBytes))
	}

	fallbackLog := filepath.Join(tmpDir, "custom.log")
	fallbackHandoff := filepath.Join(tmpDir, "custom-handoff.json")
	if err := os.WriteFile(fallbackLog, []byte("Verdict: blocked\nRequired fix: add tests\n"), 0644); err != nil {
		t.Fatalf("failed to write fallback log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, fallbackHandoff, "MS-H", 1, "custom", "", fallbackLog, 1000, ""); err != nil {
		t.Fatalf("writePhaseHandoff fallback failed: %v", err)
	}
	fallbackBytes, err := os.ReadFile(fallbackHandoff)
	if err != nil {
		t.Fatalf("failed to read fallback handoff: %v", err)
	}
	if !strings.Contains(string(fallbackBytes), "\"fallback\": true") ||
		!strings.Contains(string(fallbackBytes), "\"human_input\": \"\"") ||
		!strings.Contains(string(fallbackBytes), "Required fix") {
		t.Fatalf("expected fallback summary, got:\n%s", string(fallbackBytes))
	}
}

func TestContractHandoffValidatesFinalFencedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.json")
	text := strings.Join([]string{
		"Earlier draft:",
		"```json",
		`{"changed_files":["old"],"implemented_behavior":[],"checks_run":[],"decisions":[],"risks":[]}`,
		"```",
		"Final:",
		"```json",
		`{"changed_files":["internal/executor/handoff.go"],"implemented_behavior":["validated output"],"checks_run":["go test"],"decisions":["use final fence"],"risks":[]}`,
		"```",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(text), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "developer", "developer", logPath, 1000, ""); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	var handoff phaseHandoff
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if err := json.Unmarshal(data, &handoff); err != nil {
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
	handoffPath := filepath.Join(tmpDir, "developer-handoff.json")
	if err := os.WriteFile(logPath, []byte("custom developer output without json"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "developer", "", logPath, 1000, ""); err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}
	var handoff phaseHandoff
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if err := json.Unmarshal(data, &handoff); err != nil {
		t.Fatalf("failed to unmarshal handoff: %v", err)
	}
	if handoff.OutputContract != "" || handoff.ValidationStatus != "" || !handoff.Fallback {
		t.Fatalf("expected legacy fallback without explicit output_contract, got %#v", handoff)
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
			body:     "```json\n{\"changed_files\": [\n```",
			contains: "malformed final fenced json",
		},
		{
			name:     "missing",
			body:     "```json\n{\"changed_files\":[]}\n```",
			contains: "missing required field \"implemented_behavior\"",
		},
		{
			name:     "wrong type",
			body:     "```json\n{\"changed_files\":\"file\",\"implemented_behavior\":[],\"checks_run\":[],\"decisions\":[],\"risks\":[]}\n```",
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

func TestRecommenderContractRequiresVerdict(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name:     "missing verdict",
			body:     "```json\n{\"score\":1,\"reason\":\"complete\",\"next_cycle_focus\":[]}\n```",
			contains: "missing required field \"verdict\"",
		},
		{
			name:     "wrong verdict type",
			body:     "```json\n{\"score\":1,\"verdict\":true,\"reason\":\"complete\",\"next_cycle_focus\":[]}\n```",
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

func TestRecommenderHandoffPersistsMissingVerdictValidationError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "recommender.log")
	handoffPath := filepath.Join(tmpDir, "recommender-handoff.json")
	body := "```json\n{\"score\":1,\"reason\":\"complete\",\"next_cycle_focus\":[]}\n```"
	if err := os.WriteFile(logPath, []byte(body), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-C", 1, "recommender", "recommender", logPath, 1000, ""); err != nil {
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

func TestRecommenderScoreUsesStructuredThenLegacyFallback(t *testing.T) {
	tmpDir := t.TempDir()
	handoffPath := filepath.Join(tmpDir, "recommender-handoff.json")
	logPath := filepath.Join(tmpDir, "recommender-output.log")
	if err := os.WriteFile(handoffPath, []byte(`{"summary":{"score":2},"output_contract":"recommender","validation_status":"valid"}`), 0644); err != nil {
		t.Fatalf("failed to write handoff: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("RECOMMENDATION_SCORE: 9"), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if got := parseRecommendationScore(handoffPath, logPath); got != 2 {
		t.Fatalf("expected structured score, got %d", got)
	}
	if err := os.WriteFile(handoffPath, []byte(`{"summary":{},"output_contract":"recommender","validation_status":"invalid"}`), 0644); err != nil {
		t.Fatalf("failed to write invalid handoff: %v", err)
	}
	if got := parseRecommendationScore(handoffPath, logPath); got != 9 {
		t.Fatalf("expected legacy fallback score, got %d", got)
	}
}

func TestExtractHandoffJSONParsesMultilineFencedBlock(t *testing.T) {
	text := strings.Join([]string{
		"PM report",
		"```json",
		"{",
		`  "scope": [`,
		`    "implement parser"`,
		"  ],",
		`  "target_paths": [`,
		`    "internal/executor/executor.go"`,
		"  ],",
		`  "risks": []`,
		"}",
		"```",
	}, "\n")

	parsed, ok := extractHandoffJSON(text)
	if !ok {
		t.Fatalf("expected multiline fenced JSON to parse")
	}
	var summary map[string]interface{}
	if err := json.Unmarshal(parsed, &summary); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if got := summary["scope"].([]interface{})[0]; got != "implement parser" {
		t.Fatalf("expected parsed scope, got %#v", got)
	}
}

func TestExtractHandoffJSONSelectsLastValidHandoff(t *testing.T) {
	text := strings.Join([]string{
		"```json",
		`{"scope":["old"],"risks":["old"]}`,
		"```",
		"```text",
		`{"scope":["ignored text fence"]}`,
		"```",
		"```json",
		`{"changed_files":["internal/executor/executor.go"],"checks_run":["PASS"]}`,
		"```",
		`{"verdict":"approved","required_fixes":[]}`,
	}, "\n")

	parsed, ok := extractHandoffJSON(text)
	if !ok {
		t.Fatalf("expected fenced JSON handoff to parse")
	}
	var summary map[string]interface{}
	if err := json.Unmarshal(parsed, &summary); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if _, ok := summary["changed_files"]; ok {
		t.Fatalf("expected later bare JSON handoff, got developer object: %s", string(parsed))
	}
	if got := summary["verdict"]; got != "approved" {
		t.Fatalf("expected last QA handoff, got: %s", string(parsed))
	}
}

func TestWritePhaseHandoffCapsFallbackSize(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "developer.log")
	handoffPath := filepath.Join(tmpDir, "developer-handoff.json")
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(fmt.Sprintf("Changed file %03d: internal/executor/file_%03d.go %s\n", i, i, strings.Repeat("x", 160)))
		sb.WriteString(fmt.Sprintf("PASS test %03d: %s\n", i, strings.Repeat("y", 160)))
		sb.WriteString(fmt.Sprintf("Risk %03d: %s\n", i, strings.Repeat("z", 160)))
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	if err := writePhaseHandoff(context.Background(), config.Settings{}, handoffPath, "MS-H", 1, "custom-developer", "", logPath, 12000, "Developer note"); err != nil {
		t.Fatalf("writePhaseHandoff fallback failed: %v", err)
	}
	bytes, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}
	if len([]rune(string(bytes))) > maxFallbackHandoffChars+2000 {
		t.Fatalf("expected fallback handoff to be capped, got %d chars", len([]rune(string(bytes))))
	}
	if !strings.Contains(string(bytes), "\"fallback\": true") || !strings.Contains(string(bytes), "\"summary\"") || !strings.Contains(string(bytes), "\"human_input\": \"Developer note\"") {
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
	if err := os.WriteFile(filepath.Join(".cyclestone", "reports", "MS-P-cycle-001.md"), []byte("RAW-PRIOR-LOG\n"+strings.Repeat("noise\n", 100)), 0644); err != nil {
		t.Fatalf("failed to write prior report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "reports", "MS-P-cycle-002-01-pm-handoff.json"), []byte(`{"scope":["pm scope"],"target_paths":["internal/executor"]}`), 0644); err != nil {
		t.Fatalf("failed to write pm handoff: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".cyclestone", "reports", "MS-P-cycle-002-02-developer-handoff.json"), []byte(`{"changed_files":["internal/executor/executor.go"],"checks_run":["PASS"]}`), 0644); err != nil {
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
		filepath.Join(".cyclestone", "reports", "MS-P-cycle-001.md"),
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

	handoffPath := filepath.Join(reportsDir, "MS-F-cycle-001-01-pm-handoff.json")
	if err := os.WriteFile(handoffPath, []byte("{not-json"), 0644); err != nil {
		t.Fatalf("failed to write malformed handoff: %v", err)
	}
	malformed := readHandoffOrFallback("MS-F", "001", "pm", 200, nil)
	if !strings.Contains(malformed, "Handoff summary malformed") || strings.Contains(malformed, "{not-json") {
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
	pmExit, pmErr := runLLMOrScriptWithSession(context.Background(), "codex", "pm", "Project Manager", "pm prompt", "pm.log", RunOptions{}, nil, &threadID)
	if pmExit != 0 || pmErr != nil {
		t.Fatalf("expected fake PM codex success, exit=%d err=%v", pmExit, pmErr)
	}
	if threadID != "thread-fake" {
		t.Fatalf("expected parsed thread id, got %q", threadID)
	}

	devExit, devErr := runLLMOrScriptWithSession(context.Background(), "codex", "developer", "Developer", "dev prompt", "dev.log", RunOptions{}, nil, &threadID)
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
	exitCode, runErr := runLLMOrScriptWithSession(context.Background(), "codex", "developer", "Developer", "dev prompt", "dev.log", RunOptions{}, nil, &threadID)
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

type RoundTripFunc func(req *http.Request) (*http.Response, error)

func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestWritePhaseHandoffLLMSummarizer(t *testing.T) {
	origKey := os.Getenv("GEMINI_API_KEY")
	os.Setenv("GEMINI_API_KEY", "mock-gemini-key")
	defer os.Setenv("GEMINI_API_KEY", origKey)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{
				"candidates": [
					{
						"content": {
							"parts": [
								{"text": "LLM generated summary of decisions, files, and tasks."}
							]
						}
					}
				]
			}
		]`))
	}))
	defer server.Close()

	origTransport := http.DefaultClient.Transport
	defer func() {
		http.DefaultClient.Transport = origTransport
	}()

	http.DefaultClient.Transport = RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		mockReq, err := http.NewRequestWithContext(req.Context(), req.Method, server.URL, req.Body)
		if err != nil {
			return nil, err
		}
		mockReq.Header = req.Header
		// Clear transport on the sub-request so it doesn't infinite loop
		client := &http.Client{Transport: origTransport}
		return client.Do(mockReq)
	})

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "pm.log")
	handoffPath := filepath.Join(tmpDir, "pm-handoff.json")

	longText := strings.Repeat("PM Output Line: important goal and scope details\n", 500)
	if err := os.WriteFile(outputPath, []byte(longText), 0644); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}

	settings := config.Settings{
		GeminiModel: "gemini-1.5-flash",
	}

	err := writePhaseHandoff(context.Background(), settings, handoffPath, "MS-H", 1, "pm", "", outputPath, 100, "Note")
	if err != nil {
		t.Fatalf("writePhaseHandoff failed: %v", err)
	}

	content, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("failed to read handoff: %v", err)
	}

	var handoff struct {
		Summary map[string]interface{} `json:"summary"`
	}
	if err := json.Unmarshal(content, &handoff); err != nil {
		t.Fatalf("failed to unmarshal handoff: %v", err)
	}

	val, ok := handoff.Summary["summary"].(string)
	if !ok || val != "LLM generated summary of decisions, files, and tasks." {
		t.Fatalf("expected LLM summary in handoff, got: %#v", handoff.Summary)
	}
}
