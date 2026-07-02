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

type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        map[string]interface{} `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      string                 `json:"content,omitempty"`
	IsError      bool                   `json:"is_error,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type anthropicChunk struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJson string `json:"partial_json"`
	} `json:"delta"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func getAnthropicTools() []anthropicTool {
	var tools []anthropicTool
	for _, t := range standardTools {
		tools = append(tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	return tools
}

func mapToAnthropicMessages(history []UnifiedMessage, inputContent string, settings config.Settings) ([]anthropicMessage, string) {
	var msgs []anthropicMessage
	var systemParts []string

	for _, m := range history {
		if m.Role == "system" {
			systemParts = append(systemParts, m.Content)
			continue
		}

		var blocks []anthropicContentBlock
		if m.Role == "tool" {
			isError := strings.HasPrefix(m.Content, "TOOL_ERROR:")
			blocks = append(blocks, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
				IsError:   isError,
			})
		} else {
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{
					Type: "text",
					Text: m.Content,
				})
			}
			for _, tc := range m.ToolCalls {
				var input map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Arguments), &input)
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
		}

		if m.Role == "user" && m.Content == inputContent && settings.EnableContextCaching != nil && *settings.EnableContextCaching {
			if len(blocks) > 0 && blocks[0].Type == "text" {
				blocks[0].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
			}
		}

		msgs = append(msgs, anthropicMessage{
			Role:    m.Role,
			Content: blocks,
		})
	}

	return msgs, strings.Join(systemParts, "\n")
}

func executeAnthropicAPI(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	type anthropicPayload struct {
		Model     string             `json:"model"`
		MaxTokens int                `json:"max_tokens"`
		System    string             `json:"system,omitempty"`
		Messages  []anthropicMessage `json:"messages"`
		Stream    bool               `json:"stream"`
		Tools     []anthropicTool    `json:"tools,omitempty"`
	}

	inputContent := ""
	if len(conversationHistory) > 0 {
		inputContent = conversationHistory[0].Content
	}

	anthropicMsgs, systemPrompt := mapToAnthropicMessages(conversationHistory, inputContent, settings)

	var anthropicTools []anthropicTool
	if !noTools {
		anthropicTools = getAnthropicTools()
	}
	payload := anthropicPayload{
		Model:     model,
		MaxTokens: 8192,
		System:    systemPrompt,
		Messages:  anthropicMsgs,
		Stream:    true,
		Tools:     anthropicTools,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return apiExecutionResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return apiExecutionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return apiExecutionResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return apiExecutionResult{}, fmt.Errorf("anthropic API returned status %d: %s", resp.StatusCode, string(bodyBytes))
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

		var chunk anthropicChunk
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if chunk.Type == "message_start" && chunk.Message != nil {
				metrics.PromptTokens = chunk.Message.Usage.InputTokens
			}
			if chunk.Type == "content_block_delta" && chunk.Delta.Text != "" {
				responseText.WriteString(chunk.Delta.Text)
				writer.Write([]byte(chunk.Delta.Text))
			}
			if chunk.Type == "content_block_start" && chunk.ContentBlock.Type == "tool_use" {
				idx := chunk.Index
				for len(accumulatedToolCalls) <= idx {
					accumulatedToolCalls = append(accumulatedToolCalls, UnifiedToolCall{})
				}
				accumulatedToolCalls[idx].ID = chunk.ContentBlock.ID
				accumulatedToolCalls[idx].Name = chunk.ContentBlock.Name
			}
			if chunk.Type == "content_block_delta" && chunk.Delta.Type == "input_json_delta" {
				idx := chunk.Index
				if idx < len(accumulatedToolCalls) {
					accumulatedToolCalls[idx].Arguments += chunk.Delta.PartialJson
				}
			}
			if chunk.Type == "message_delta" {
				var raw struct {
					Delta struct {
						StopReason string `json:"stop_reason"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &raw); err == nil && raw.Delta.StopReason != "" {
					metrics.StopOrDoneReason = raw.Delta.StopReason
				}
				if chunk.Usage != nil {
					metrics.CompletionTokens = chunk.Usage.OutputTokens
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
