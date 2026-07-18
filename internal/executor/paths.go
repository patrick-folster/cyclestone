package executor

import (
	"fmt"
	"path/filepath"
	"strconv"
)

type cycleArtifactPaths struct {
	MilestoneDir string
	CycleDir     string
	Report       string
	Metadata     string
	CodexThread  string
	Summary      string
}

type phaseArtifactPaths struct {
	Dir     string
	Input   string
	Output  string
	Handoff string
}

func cycleArtifacts(reportsDir, milestoneID string, cycleNum int) cycleArtifactPaths {
	cyclePadded := fmt.Sprintf("%03d", cycleNum)
	milestoneDir := filepath.Join(reportsDir, milestoneID)
	cycleDir := filepath.Join(milestoneDir, "cycle-"+cyclePadded)
	return cycleArtifactPaths{
		MilestoneDir: milestoneDir,
		CycleDir:     cycleDir,
		Report:       filepath.Join(cycleDir, "report.yaml"),
		Metadata:     filepath.Join(cycleDir, "metadata.json"),
		CodexThread:  filepath.Join(cycleDir, "codex-thread.json"),
		Summary:      filepath.Join(milestoneDir, "summary.md"),
	}
}

func phaseArtifacts(reportsDir, milestoneID string, cycleNum int, agentFileID string) phaseArtifactPaths {
	cycle := cycleArtifacts(reportsDir, milestoneID, cycleNum)
	dir := filepath.Join(cycle.CycleDir, agentFileID)
	return phaseArtifactPaths{
		Dir:     dir,
		Input:   filepath.Join(dir, "input.md"),
		Output:  filepath.Join(dir, "output.log"),
		Handoff: filepath.Join(dir, "handoff.yaml"),
	}
}

func parseCycleDirName(name string) (int, bool) {
	if len(name) != len("cycle-001") || name[:6] != "cycle-" {
		return 0, false
	}
	n, err := strconv.Atoi(name[6:])
	if err != nil {
		return 0, false
	}
	return n, true
}
