package tui

import (
	"fmt"
	"os"
	"os/exec"
)

type runnerAvailability struct {
	ID        string
	Label     string
	Available bool
	Reason    string
}

var supportedSetupRunners = []runnerAvailability{
	{ID: "codex", Label: "Codex CLI"},
	{ID: "agy", Label: "Agy CLI"},
	{ID: "aider", Label: "Aider CLI"},
	{ID: "gemini", Label: "Gemini API"},
	{ID: "openai", Label: "OpenAI API"},
	{ID: "anthropic", Label: "Anthropic API"},
}

func detectSetupRunnerAvailability() []runnerAvailability {
	runners := append([]runnerAvailability(nil), supportedSetupRunners...)
	for i := range runners {
		runners[i].Available, runners[i].Reason = isRunnerAvailable(runners[i].ID)
	}
	return runners
}

func defaultSetupRunner(runners []runnerAvailability) string {
	for _, runner := range runners {
		if runner.Available {
			return runner.ID
		}
	}
	return ""
}

func isSetupRunnerSelectable(runners []runnerAvailability, id string) bool {
	for _, runner := range runners {
		if runner.ID == id {
			return runner.Available
		}
	}
	return false
}

func isRunnerAvailable(runner string) (bool, string) {
	switch runner {
	case "codex", "agy", "aider":
		if _, err := exec.LookPath(runner); err != nil {
			return false, fmt.Sprintf("%s not found on PATH", runner)
		}
		return true, "available on PATH"
	case "gemini":
		if os.Getenv("GEMINI_API_KEY") == "" {
			return false, "GEMINI_API_KEY is not set"
		}
		return true, "GEMINI_API_KEY is set"
	case "openai":
		if os.Getenv("OPENAI_API_KEY") == "" {
			return false, "OPENAI_API_KEY is not set"
		}
		return true, "OPENAI_API_KEY is set"
	case "anthropic":
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return false, "ANTHROPIC_API_KEY is not set"
		}
		return true, "ANTHROPIC_API_KEY is set"
	}
	return true, ""
}
