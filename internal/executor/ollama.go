package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/patrick-folster/cyclestone/internal/config"
	"gopkg.in/yaml.v3"
)

type ollamaMessage struct {
	Role       string              `json:"role"`
	Content    interface{}         `json:"content"`
	ToolCalls  []ollamaPayloadTool `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	Name       string              `json:"name,omitempty"`
}

type ollamaPayloadTool struct {
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function"`
}

func mapToOllamaMessages(history []UnifiedMessage) []ollamaMessage {
	var msgs []ollamaMessage
	for _, m := range history {
		var pt []ollamaPayloadTool
		for _, tc := range m.ToolCalls {
			tool := ollamaPayloadTool{Type: "function"}
			tool.Function.Name = tc.Name
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				args = map[string]interface{}{}
			}
			tool.Function.Arguments = args
			pt = append(pt, tool)
		}

		var content interface{} = m.Content
		if len(pt) > 0 && m.Content == "" {
			content = ""
		}

		oMsg := ollamaMessage{
			Role:      m.Role,
			Content:   content,
			ToolCalls: pt,
		}
		if m.Role == "tool" {
			oMsg.ToolCallID = m.ToolCallID
			oMsg.Name = m.ToolName
		}
		msgs = append(msgs, oMsg)
	}
	return msgs
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

func ollamaOptions(settings config.Settings) map[string]int {
	options := make(map[string]int)
	if settings.OllamaNumCtx > 0 {
		options["num_ctx"] = settings.OllamaNumCtx
	}
	if settings.OllamaNumPredict > 0 {
		options["num_predict"] = settings.OllamaNumPredict
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

func executeOllamaAPI(ctx context.Context, model string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	type ollamaPayload struct {
		Model     string          `json:"model"`
		Messages  []ollamaMessage `json:"messages"`
		Stream    bool            `json:"stream"`
		KeepAlive string          `json:"keep_alive,omitempty"`
		Options   map[string]int  `json:"options,omitempty"`
		Tools     []openAITool    `json:"tools,omitempty"`
	}

	keepAlive := settings.OllamaKeepAlive
	if keepAlive == "" {
		keepAlive = "5m"
	}

	var ollamaTools []openAITool
	if !noTools {
		ollamaTools = getOpenAITools()
	}
	payload := ollamaPayload{
		Model:     model,
		Messages:  mapToOllamaMessages(conversationHistory),
		Stream:    true,
		KeepAlive: keepAlive,
		Options:   ollamaOptions(settings),
		Tools:     ollamaTools,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return apiExecutionResult{}, err
	}

	host := settings.OllamaHost
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimSuffix(host, "/")

	req, err := http.NewRequestWithContext(ctx, "POST", host+"/api/chat", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return apiExecutionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return apiExecutionResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return apiExecutionResult{}, fmt.Errorf("ollama API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var responseText strings.Builder
	var accumulatedToolCalls []UnifiedToolCall

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return apiExecutionResult{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Ollama native tool call format: arguments is a JSON object (not a string),
		// there is no index field, and there is no id field.
		type ollamaToolFunction struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		type ollamaToolCall struct {
			Function ollamaToolFunction `json:"function"`
		}
		type ollamaChunk struct {
			Message struct {
				Role      string           `json:"role"`
				Content   string           `json:"content"`
				ToolCalls []ollamaToolCall `json:"tool_calls"`
			} `json:"message"`
			Done            bool   `json:"done"`
			DoneReason      string `json:"done_reason"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
		}
		var chunk ollamaChunk
		if err := json.Unmarshal([]byte(line), &chunk); err == nil {
			if chunk.DoneReason != "" {
				metrics.StopOrDoneReason = chunk.DoneReason
			}
			if chunk.PromptEvalCount > 0 {
				metrics.PromptTokens = chunk.PromptEvalCount
			}
			if chunk.EvalCount > 0 {
				metrics.CompletionTokens = chunk.EvalCount
			}
			if chunk.Message.Content != "" {
				responseText.WriteString(chunk.Message.Content)
				writer.Write([]byte(chunk.Message.Content))
			}
			for i, tc := range chunk.Message.ToolCalls {
				// Serialize the arguments map back to a JSON string for uniform
				// handling by executeTool, which expects a JSON string.
				argsBytes, marshalErr := json.Marshal(tc.Function.Arguments)
				if marshalErr != nil {
					argsBytes = []byte("{}")
				}
				// Ollama does not provide an id field; generate a synthetic one so
				// tool result messages can be matched back in conversation history.
				synthID := fmt.Sprintf("ollama_tool_%d_%d", time.Now().UnixNano(), i)
				accumulatedToolCalls = append(accumulatedToolCalls, UnifiedToolCall{
					ID:        synthID,
					Name:      tc.Function.Name,
					Arguments: string(argsBytes),
				})
			}
			if chunk.Done {
				break
			}
		}
	}

	msg := UnifiedMessage{
		Role:      "assistant",
		Content:   responseText.String(),
		ToolCalls: accumulatedToolCalls,
	}
	metrics.OutputCharCount = len([]rune(msg.Content))
	metrics.ToolCallCount = len(msg.ToolCalls)
	return apiExecutionResult{Message: msg, Metrics: metrics}, nil
}

// setupTemporaryAiderSettings checks if we need to configure aider context limits
// for Ollama, backs up any existing .aider.model.settings.yml, and writes a merged version.
// It returns a function to restore the original state (or delete the temp file).
func setupTemporaryAiderSettings(model string, settings config.Settings) func() {
	if settings.OllamaNumCtx <= 0 {
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
		Name   string `yaml:"name"`
		NumCtx int    `yaml:"num_ctx,omitempty"`
	}

	var list []AiderModelSetting
	if exists {
		_ = yaml.Unmarshal(backup, &list)
	}

	// Update or add entry
	found := false
	for i, entry := range list {
		if entry.Name == model {
			list[i].NumCtx = settings.OllamaNumCtx
			found = true
			break
		}
	}
	if !found {
		list = append(list, AiderModelSetting{
			Name:   model,
			NumCtx: settings.OllamaNumCtx,
		})
	}

	mergedData, err := yaml.Marshal(list)
	if err == nil {
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
