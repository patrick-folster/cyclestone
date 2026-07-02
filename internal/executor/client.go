package executor

import (
	"context"
	"fmt"
	"os"

	"github.com/patrick-folster/cyclestone/internal/config"
)

type LLMClient interface {
	ResolveConfig(settings config.Settings) (model string, apiKey string, err error)
	Call(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error)
}

type geminiClient struct{}

func (c geminiClient) ResolveConfig(settings config.Settings) (string, string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", "", fmt.Errorf("GEMINI_API_KEY environment variable is not set")
	}
	return settings.GeminiModel, apiKey, nil
}

func (c geminiClient) Call(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	return executeGeminiAPI(ctx, model, apiKey, conversationHistory, settings, writer, noTools, metrics)
}

type openAIClient struct{}

func (c openAIClient) ResolveConfig(settings config.Settings) (string, string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", "", fmt.Errorf("OPENAI_API_KEY environment variable is not set")
	}
	return settings.OpenAIModel, apiKey, nil
}

func (c openAIClient) Call(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	return executeOpenAIAPI(ctx, model, apiKey, conversationHistory, settings, writer, noTools, metrics)
}

type anthropicClient struct{}

func (c anthropicClient) ResolveConfig(settings config.Settings) (string, string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "", "", fmt.Errorf("ANTHROPIC_API_KEY environment variable is not set")
	}
	return settings.AnthropicModel, apiKey, nil
}

func (c anthropicClient) Call(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	return executeAnthropicAPI(ctx, model, apiKey, conversationHistory, settings, writer, noTools, metrics)
}

type ollamaClient struct{}

func (c ollamaClient) ResolveConfig(settings config.Settings) (string, string, error) {
	return settings.OllamaModel, "", nil
}

func (c ollamaClient) Call(ctx context.Context, model string, apiKey string, conversationHistory []UnifiedMessage, settings config.Settings, writer *liveLogWriter, noTools bool, metrics apiExecutionMetrics) (apiExecutionResult, error) {
	return executeOllamaAPI(ctx, model, conversationHistory, settings, writer, noTools, metrics)
}

var clients = map[string]LLMClient{
	"gemini":     geminiClient{},
	"openai":     openAIClient{},
	"anthropic":  anthropicClient{},
	"ollama":     ollamaClient{},
	"ollama_api": ollamaClient{},
}
