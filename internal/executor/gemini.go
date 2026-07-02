package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/patrick-folster/cyclestone/internal/config"
)

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

func getGeminiTools() []geminiTool {
	var decls []geminiFunctionDeclaration
	for _, t := range standardTools {
		decls = append(decls, geminiFunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return []geminiTool{{FunctionDeclarations: decls}}
}

func mapToGeminiContents(history []UnifiedMessage) []geminiContent {
	var contents []geminiContent
	for _, m := range history {
		role := m.Role
		if role == "assistant" {
			role = "model"
		} else if role == "tool" || role == "system" {
			role = "user"
		}

		var parts []geminiPart
		if m.Role == "tool" {
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(m.Content), &resp); err != nil {
				resp = map[string]interface{}{"output": m.Content}
			}
			parts = append(parts, geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name:     m.ToolName,
					Response: resp,
				},
			})
		} else if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Arguments), &args)
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: args,
					},
				})
			}
		} else {
			parts = append(parts, geminiPart{Text: m.Content})
		}

		contents = append(contents, geminiContent{
			Role:  role,
			Parts: parts,
		})
	}
	return contents
}

func executeGeminiAPI(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?key=%s", model, apiKey)

	inputContent := ""
	if len(conversationHistory) > 0 {
		inputContent = conversationHistory[0].Content
	}

	var cacheName string
	if settings.EnableContextCaching != nil && *settings.EnableContextCaching && len(inputContent) >= 120000 {
		cacheUrl := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/cachedContents?key=%s", apiKey)

		fullModelName := model
		if !strings.HasPrefix(fullModelName, "models/") {
			fullModelName = "models/" + fullModelName
		}

		type geminiCachePart struct {
			Text string `json:"text"`
		}
		type geminiCacheContent struct {
			Parts []geminiCachePart `json:"parts"`
		}
		type geminiCachePayload struct {
			Model    string               `json:"model"`
			Contents []geminiCacheContent `json:"contents"`
			TTL      string               `json:"ttl"`
		}

		ttlSecs := 1800
		if settings.CacheTTLMinutes > 0 {
			ttlSecs = settings.CacheTTLMinutes * 60
		}

		cachePayload := geminiCachePayload{
			Model: fullModelName,
			Contents: []geminiCacheContent{
				{
					Parts: []geminiCachePart{{Text: inputContent}},
				},
			},
			TTL: fmt.Sprintf("%ds", ttlSecs),
		}

		cacheBytes, marshalErr := json.Marshal(cachePayload)
		if marshalErr == nil {
			cacheReq, reqErr := http.NewRequestWithContext(ctx, "POST", cacheUrl, bytes.NewBuffer(cacheBytes))
			if reqErr == nil {
				cacheReq.Header.Set("Content-Type", "application/json")
				cacheResp, respErr := http.DefaultClient.Do(cacheReq)
				if respErr == nil {
					defer cacheResp.Body.Close()
					if cacheResp.StatusCode == http.StatusOK || cacheResp.StatusCode == http.StatusCreated {
						var cacheRespData struct {
							Name string `json:"name"`
						}
						if decodeErr := json.NewDecoder(cacheResp.Body).Decode(&cacheRespData); decodeErr == nil && cacheRespData.Name != "" {
							cacheName = cacheRespData.Name
						}
					}
				}
			}
		}
	}

	var payloadBytes []byte
	var err error
	var geminiTools []geminiTool
	if !noTools {
		geminiTools = getGeminiTools()
	}

	if cacheName != "" {
		type geminiPayloadWithCache struct {
			CachedContent string          `json:"cachedContent"`
			Contents      []geminiContent `json:"contents"`
			Tools         []geminiTool    `json:"tools,omitempty"`
		}
		var contentsToPass []geminiContent
		if len(conversationHistory) <= 1 {
			contentsToPass = []geminiContent{
				{
					Role:  "user",
					Parts: []geminiPart{{Text: "Execute the task."}},
				},
			}
		} else {
			contentsToPass = mapToGeminiContents(conversationHistory[1:])
		}
		payload := geminiPayloadWithCache{
			CachedContent: cacheName,
			Contents:      contentsToPass,
			Tools:         geminiTools,
		}
		payloadBytes, err = json.Marshal(payload)
	} else {
		type geminiPayload struct {
			Contents []geminiContent `json:"contents"`
			Tools    []geminiTool    `json:"tools,omitempty"`
		}
		payload := geminiPayload{
			Contents: mapToGeminiContents(conversationHistory),
			Tools:    geminiTools,
		}
		payloadBytes, err = json.Marshal(payload)
	}

	if err != nil {
		return apiExecutionResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payloadBytes))
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
		return apiExecutionResult{}, fmt.Errorf("gemini API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	dec := json.NewDecoder(resp.Body)
	t, err := dec.Token()
	if err != nil {
		return apiExecutionResult{}, err
	}
	delim, ok := t.(json.Delim)
	if !ok || delim != '[' {
		return apiExecutionResult{}, fmt.Errorf("expected JSON array open bracket '['")
	}

	type GeminiResponseChunk struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string              `json:"text"`
					FunctionCall *geminiFunctionCall `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	var responseText strings.Builder
	var toolCalls []UnifiedToolCall

	for dec.More() {
		var chunk GeminiResponseChunk
		if err := dec.Decode(&chunk); err != nil {
			return apiExecutionResult{}, err
		}
		if chunk.UsageMetadata != nil {
			metrics.PromptTokens = chunk.UsageMetadata.PromptTokenCount
			metrics.CompletionTokens = chunk.UsageMetadata.CandidatesTokenCount
		}
		for _, cand := range chunk.Candidates {
			if cand.FinishReason != "" {
				metrics.StopOrDoneReason = cand.FinishReason
			}
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					responseText.WriteString(part.Text)
					writer.Write([]byte(part.Text))
				}
				if part.FunctionCall != nil {
					argsBytes, err := json.Marshal(part.FunctionCall.Args)
					if err == nil {
						toolCalls = append(toolCalls, UnifiedToolCall{
							ID:        fmt.Sprintf("call_%d", time.Now().UnixNano()),
							Name:      part.FunctionCall.Name,
							Arguments: string(argsBytes),
						})
					}
				}
			}
		}
	}

	_, err = dec.Token()
	if err != nil {
		return apiExecutionResult{}, err
	}

	msg := UnifiedMessage{
		Role:      "assistant",
		Content:   responseText.String(),
		ToolCalls: toolCalls,
	}
	metrics.OutputCharCount = len([]rune(msg.Content))
	metrics.ToolCallCount = len(msg.ToolCalls)
	return apiExecutionResult{Message: msg, Metrics: metrics}, nil
}
