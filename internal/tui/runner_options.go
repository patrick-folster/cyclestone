package tui

var milestoneRunnerOptions = []string{"codex", "agy", "aider", "ollama"}

func getMilestoneRunnerOptions() []string {
	return append([]string(nil), milestoneRunnerOptions...)
}

func normalizeMilestoneRunner(runner string) string {
	for _, opt := range milestoneRunnerOptions {
		if runner == opt {
			return runner
		}
	}
	return milestoneRunnerOptions[0]
}
