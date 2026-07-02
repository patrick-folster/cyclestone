package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/patrick-folster/cyclestone/internal/config"
)

type openAIMessage struct {
	Role       string              `json:"role"`
	Content    interface{}         `json:"content"`
	ToolCalls  []openaiPayloadTool `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	Name       string              `json:"name,omitempty"`
}

type openaiPayloadTool struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type openaiDeltaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func getOpenAITools() []openAITool {
	var tools []openAITool
	for _, t := range standardTools {
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return tools
}

func mapToOpenAIMessages(history []UnifiedMessage) []openAIMessage {
	var msgs []openAIMessage
	for _, m := range history {
		var pt []openaiPayloadTool
		for _, tc := range m.ToolCalls {
			tool := openaiPayloadTool{
				ID:   tc.ID,
				Type: "function",
			}
			tool.Function.Name = tc.Name
			tool.Function.Arguments = tc.Arguments
			pt = append(pt, tool)
		}

		var content interface{} = m.Content
		if len(pt) > 0 && m.Content == "" {
			// Use empty string rather than nil so Ollama (and other strict APIs) do not
			// reject the message — Ollama's /api/chat does not accept null content.
			content = ""
		}

		oMsg := openAIMessage{
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

func executeOpenAIAPI(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	type openaiStreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	type openaiPayload struct {
		Model         string               `json:"model"`
		Messages      []openAIMessage      `json:"messages"`
		Stream        bool                 `json:"stream"`
		StreamOptions *openaiStreamOptions `json:"stream_options,omitempty"`
		Tools         []openAITool         `json:"tools,omitempty"`
	}

	var openaiTools []openAITool
	if !noTools {
		openaiTools = getOpenAITools()
	}
	payload := openaiPayload{
		Model:    model,
		Messages: mapToOpenAIMessages(conversationHistory),
		Stream:   true,
		StreamOptions: &openaiStreamOptions{
			IncludeUsage: true,
		},
		Tools: openaiTools,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return apiExecutionResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return apiExecutionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return apiExecutionResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return apiExecutionResult{}, fmt.Errorf("openai API returned status %d: %s", resp.StatusCode, string(bodyBytes))
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
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		type openaiChunk struct {
			Choices []struct {
				Delta struct {
					Content   string                `json:"content"`
					ToolCalls []openaiDeltaToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		var chunk openaiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if chunk.Usage != nil {
				metrics.PromptTokens = chunk.Usage.PromptTokens
				metrics.CompletionTokens = chunk.Usage.CompletionTokens
			}
			for _, choice := range chunk.Choices {
				if choice.FinishReason != "" {
					metrics.StopOrDoneReason = choice.FinishReason
				}
				if choice.Delta.Content != "" {
					responseText.WriteString(choice.Delta.Content)
					writer.Write([]byte(choice.Delta.Content))
				}
				for _, tc := range choice.Delta.ToolCalls {
					idx := tc.Index
					for len(accumulatedToolCalls) <= idx {
						accumulatedToolCalls = append(accumulatedToolCalls, UnifiedToolCall{})
					}
					if tc.ID != "" {
						accumulatedToolCalls[idx].ID = tc.ID
					}
					if tc.Function.Name != "" {
						accumulatedToolCalls[idx].Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						accumulatedToolCalls[idx].Arguments += tc.Function.Arguments
					}
				}
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
