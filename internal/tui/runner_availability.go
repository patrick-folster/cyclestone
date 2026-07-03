package tui

import (
	"fmt"
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
	{ID: "ollama", Label: "Ollama via Aider"},
	{ID: "ollama-codex", Label: "Ollama via Codex"},
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
	case "ollama":
		if _, err := exec.LookPath("aider"); err != nil {
			return false, "aider not found on PATH"
		}
		return true, "available through aider on PATH"
	case "ollama-codex":
		if _, err := exec.LookPath("ollama"); err != nil {
			return false, "ollama not found on PATH"
		}
		if _, err := exec.LookPath("codex"); err != nil {
			return false, "codex not found on PATH"
		}
		return true, "available through ollama and codex on PATH"
	}
	return false, "unsupported runner"
}
