package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Tool struct {
	Name        string
	Description string
	Parameters  interface{}
}

var standardTools = []Tool{
	{
		Name:        "run_command",
		Description: "Run a shell command in the repository root directory. Returns stdout and stderr.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The command string to execute (e.g., 'go test ./...').",
				},
			},
			"required": []string{"command"},
		},
	},
	{
		Name:        "read_file",
		Description: "Read the content of a file at the specified path. Returns the file content.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "The relative or absolute path of the file to read.",
				},
			},
			"required": []string{"path"},
		},
	},
	{
		Name:        "write_file",
		Description: "Write the specified content to a file at the path, creating parent directories if necessary. Returns success or error.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "The relative or absolute path of the file to write.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The complete text content to write to the file.",
				},
			},
			"required": []string{"path", "content"},
		},
	},
}

type UnifiedMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []UnifiedToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	ToolName   string            `json:"tool_name,omitempty"`
}

type UnifiedToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func getToolCallFingerprint(name string, arguments string) string {
	var parsed interface{}
	if err := json.Unmarshal([]byte(arguments), &parsed); err == nil {
		if normalized, err := json.Marshal(parsed); err == nil {
			return name + "|" + string(normalized)
		}
	}
	return name + "|" + strings.TrimSpace(arguments)
}

func executeTool(name string, arguments string) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("failed to parse tool arguments: %w", err)
	}

	switch name {
	case "run_command":
		cmdStr, ok := args["command"].(string)
		if !ok {
			return "", fmt.Errorf("missing or invalid 'command' argument")
		}
		cmd := exec.Command("bash", "-c", cmdStr)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		output := stdout.String() + stderr.String()
		if err != nil {
			return output, fmt.Errorf("command failed: %w", err)
		}
		return output, nil

	case "read_file":
		pathStr, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing or invalid 'path' argument")
		}
		content, err := os.ReadFile(pathStr)
		if err != nil {
			return "", err
		}
		return string(content), nil

	case "write_file":
		pathStr, ok := args["path"].(string)
		if !ok {
			return "", fmt.Errorf("missing or invalid 'path' argument")
		}
		contentStr, ok := args["content"].(string)
		if !ok {
			return "", fmt.Errorf("missing or invalid 'content' argument")
		}
		if err := os.MkdirAll(filepath.Dir(pathStr), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(pathStr, []byte(contentStr), 0644); err != nil {
			return "", err
		}
		return "success", nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}
