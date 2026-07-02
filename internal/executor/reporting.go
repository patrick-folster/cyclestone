package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type phaseCostMetrics struct {
	InputChars       int
	OutputChars      int
	ReportChars      int
	ModelCalls       int
	ModelOutputChars int
	ToolCalls        int
	EstimatedTokens  int
	StopOrDoneReason string
	PromptTokens     int
	CompletionTokens int
}

func writePhaseReportExcerpt(reportFile *os.File, phaseName, outputPath, runner string, exitCode int, maxChars int) {
	fmt.Fprintf(reportFile, "\n## %s Phase\n\n", phaseName)
	fmt.Fprintf(reportFile, "- Output log: `%s`\n", outputPath)
	fmt.Fprintf(reportFile, "- Exit status: %d\n\n", exitCode)
	writeLogExcerpt(reportFile, "### Output Excerpt", outputPath, runner, maxChars)
}

func writeLogExcerpt(reportFile *os.File, heading, outputPath, runner string, maxChars int) {
	fmt.Fprintf(reportFile, "%s\n\n```text\n", heading)
	outBytes, err := os.ReadFile(outputPath)
	if err != nil {
		fmt.Fprintf(reportFile, "(failed to read output log: %v)\n", err)
		fmt.Fprintf(reportFile, "```\n")
		return
	}
	if len(outBytes) == 0 {
		fmt.Fprintf(reportFile, "(No output recorded)\n")
		fmt.Fprintf(reportFile, "```\n")
		return
	}
	text := sanitizeRunnerLogForReport(string(outBytes), runner)
	reportFile.WriteString(limitTextMiddle(text, maxChars, outputPath))
	fmt.Fprintf(reportFile, "\n```\n")
}

func currentFileSize(file *os.File) int {
	if file == nil {
		return 0
	}
	_ = file.Sync()
	info, err := file.Stat()
	if err != nil {
		return 0
	}
	return int(info.Size())
}

func collectPhaseCostMetrics(inputContent, outputPath string) phaseCostMetrics {
	metrics := phaseCostMetrics{
		InputChars:      len([]rune(inputContent)),
		EstimatedTokens: estimateTextTokens(inputContent),
	}
	outBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return metrics
	}
	outputText := string(outBytes)
	metrics.OutputChars = len([]rune(outputText))
	re := regexp.MustCompile(`\[Metrics\]\s+runner=\S+\s+model=\S+\s+model_calls=(\d+)\s+output_chars=(\d+)\s+tool_calls=(\d+)\s+stop_or_done_reason=(\S+)(?:\s+prompt_tokens=(\d+)\s+completion_tokens=(\d+))?`)
	matches := re.FindAllStringSubmatch(outputText, -1)
	if len(matches) == 0 {
		return metrics
	}
	last := matches[len(matches)-1]
	metrics.ModelCalls, _ = strconv.Atoi(last[1])
	metrics.ModelOutputChars, _ = strconv.Atoi(last[2])
	metrics.ToolCalls, _ = strconv.Atoi(last[3])
	if last[4] != "n/a" {
		metrics.StopOrDoneReason = last[4]
	}
	if len(last) >= 7 && last[5] != "" && last[6] != "" {
		metrics.PromptTokens, _ = strconv.Atoi(last[5])
		metrics.CompletionTokens, _ = strconv.Atoi(last[6])
	}
	return metrics
}

func writePhaseCostMetrics(reportFile *os.File, metrics phaseCostMetrics) {
	fmt.Fprintf(reportFile, "\n### Phase Cost Metrics\n\n")
	fmt.Fprintf(reportFile, "- Input chars: %d\n", metrics.InputChars)
	fmt.Fprintf(reportFile, "- Output chars: %d\n", metrics.OutputChars)
	fmt.Fprintf(reportFile, "- Report excerpt chars: %d\n", metrics.ReportChars)
	fmt.Fprintf(reportFile, "- Estimated input tokens: %d\n", metrics.EstimatedTokens)
	if metrics.PromptTokens > 0 || metrics.CompletionTokens > 0 {
		fmt.Fprintf(reportFile, "- Actual prompt tokens: %d\n", metrics.PromptTokens)
		fmt.Fprintf(reportFile, "- Actual completion tokens: %d\n", metrics.CompletionTokens)
		fmt.Fprintf(reportFile, "- Actual total tokens: %d\n", metrics.PromptTokens+metrics.CompletionTokens)
	}
	if metrics.ModelCalls > 0 || metrics.ModelOutputChars > 0 || metrics.ToolCalls > 0 || metrics.StopOrDoneReason != "" {
		fmt.Fprintf(reportFile, "- Model calls: %d\n", metrics.ModelCalls)
		fmt.Fprintf(reportFile, "- Model output chars: %d\n", metrics.ModelOutputChars)
		fmt.Fprintf(reportFile, "- Tool calls: %d\n", metrics.ToolCalls)
		if metrics.StopOrDoneReason != "" {
			fmt.Fprintf(reportFile, "- Stop/done reason: %s\n", metrics.StopOrDoneReason)
		}
	}
}

func sanitizeRunnerLogForReport(text, runner string) string {
	if runner != "codex" {
		return text
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "assistant" {
			return strings.Join(lines[i:], "\n")
		}
	}
	return text
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	runes := len([]rune(text))
	return (runes + charsPerEstimatedToken - 1) / charsPerEstimatedToken
}

func estimateMessagesTokens(history []UnifiedMessage) int {
	total := 0
	for _, msg := range history {
		total += estimateTextTokens(msg.Role)
		total += estimateTextTokens(msg.Content)
		total += estimateTextTokens(msg.ToolCallID)
		total += estimateTextTokens(msg.ToolName)
		for _, call := range msg.ToolCalls {
			total += estimateTextTokens(call.ID)
			total += estimateTextTokens(call.Name)
			total += estimateTextTokens(call.Arguments)
		}
	}
	return total
}

func compactConversationHistory(history []UnifiedMessage, maxRetained int) []UnifiedMessage {
	if maxRetained <= 0 {
		maxRetained = 8
	}
	if len(history) <= maxRetained {
		return history
	}

	retainedTail := maxRetained - 2
	if retainedTail < 1 {
		retainedTail = 1
	}
	tailStart := len(history) - retainedTail
	for tailStart > 1 && history[tailStart].Role == "tool" {
		tailStart--
	}
	if tailStart <= 1 {
		return history
	}

	removed := history[1:tailStart]

	// Extract read_file and write_file contents to preserve code context
	fileContents := make(map[string]string)
	findToolCall := func(toolCallID string) (UnifiedToolCall, bool) {
		for _, msg := range history {
			for _, call := range msg.ToolCalls {
				if call.ID == toolCallID {
					return call, true
				}
			}
		}
		return UnifiedToolCall{}, false
	}

	for _, msg := range removed {
		if msg.Role == "tool" && msg.ToolName == "read_file" && !strings.HasPrefix(msg.Content, "TOOL_ERROR:") {
			if call, found := findToolCall(msg.ToolCallID); found {
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(call.Arguments), &args); err == nil {
					if path, ok := args["path"].(string); ok && path != "" {
						fileContents[path] = msg.Content
					}
				}
			}
		} else if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				if call.Name == "write_file" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(call.Arguments), &args); err == nil {
						path, _ := args["path"].(string)
						content, _ := args["content"].(string)
						if path != "" && content != "" {
							fileContents[path] = content
						}
					}
				}
			}
		}
	}

	summary := summarizeCompactedMessages(removed)
	if len(fileContents) > 0 {
		var sb strings.Builder
		sb.WriteString(summary)
		sb.WriteString("\n\nRetained File Contents from previous actions:\n")
		var paths []string
		for p := range fileContents {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			sb.WriteString(fmt.Sprintf("\n---\nFile: %s\n\n%s\n---", p, fileContents[p]))
		}
		summary = sb.String()
	}

	compacted := make([]UnifiedMessage, 0, 2+retainedTail)
	compacted = append(compacted, history[0])
	compacted = append(compacted, UnifiedMessage{
		Role:    "system",
		Content: summary,
	})
	compacted = append(compacted, history[tailStart:]...)
	return compacted
}

func summarizeCompactedMessages(messages []UnifiedMessage) string {
	var assistantCount, toolCount, systemCount, totalChars int
	var toolNames []string
	toolSeen := make(map[string]bool)

	for _, msg := range messages {
		totalChars += len([]rune(msg.Content))
		switch msg.Role {
		case "assistant":
			assistantCount++
		case "tool":
			toolCount++
			if msg.ToolName != "" && !toolSeen[msg.ToolName] {
				toolSeen[msg.ToolName] = true
				toolNames = append(toolNames, msg.ToolName)
			}
		case "system":
			systemCount++
		}
		for _, call := range msg.ToolCalls {
			if call.Name != "" && !toolSeen[call.Name] {
				toolSeen[call.Name] = true
				toolNames = append(toolNames, call.Name)
			}
		}
	}
	sort.Strings(toolNames)
	tools := "none"
	if len(toolNames) > 0 {
		tools = strings.Join(toolNames, ", ")
	}
	return fmt.Sprintf("Conversation history compacted to reduce token use. Removed messages: assistant=%d tool=%d system=%d approx_content_chars=%d tools=%s. Use retained recent messages for exact current state.", assistantCount, toolCount, systemCount, totalChars, tools)
}
