package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/tui"
)

func main() {
	configPath := flag.String("config", ".cyclestone/milestone.yml", "Path to milestone.yml config file")
	statePath := flag.String("state", ".cyclestone/state.json", "Path to state.json tracking file")

	// Load merged settings to determine defaults
	settings := config.LoadMergedSettings()
	autoGit := true
	if settings.AutoGitBranch != nil {
		autoGit = *settings.AutoGitBranch
	}
	// NoBranchChange reflects AutoGitBranch only. CreateMilestoneBranch is a separate
	// setting for branch creation at milestone-creation time, unrelated to cycle execution.
	defaultNoBranchChange := !autoGit
	defaultUnrestricted := settings.DefaultMode == "unrestricted"
	defaultDisableBold := config.DefaultDisableBoldForEnvironment()
	defaultDisableRoundedBorders := config.DefaultDisableRoundedBordersForEnvironment()

	noBranchChange := flag.Bool("no-branch-change", defaultNoBranchChange, "Do not switch/create git branches during milestone execution")
	unrestricted := flag.Bool("unrestricted", defaultUnrestricted, "Run agents without sandbox/permission restrictions")
	disableBold := flag.Bool("no-bold", defaultDisableBold, "Disable bold text styling to prevent terminal rendering glitches")
	disableRoundedBorders := flag.Bool("no-rounded-borders", defaultDisableRoundedBorders, "Disable rounded borders to prevent terminal rendering glitches")
	versionFlag := flag.Bool("version", false, "Print the version and exit")
	flag.Parse()

	if *versionFlag {
		printVersion()
		os.Exit(0)
	}

	// Validate command line flags
	if *configPath == "" {
		fmt.Println("Error: -config parameter cannot be empty")
		os.Exit(1)
	}
	if *statePath == "" {
		fmt.Println("Error: -state parameter cannot be empty")
		os.Exit(1)
	}

	// Check if config file exists. Interactive initialization is handled by the TUI.
	fi, statErr := os.Stdin.Stat()
	isInteractive := statErr == nil && (fi.Mode()&os.ModeCharDevice) != 0
	missingConfig, err := isConfigMissing(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking config: %v\n", err)
		os.Exit(1)
	}
	if missingConfig && !isInteractive {
		fmt.Fprintln(os.Stderr, missingConfigNonInteractiveError())
		os.Exit(1)
	}

	// Load milestone configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Load dynamic state tracking
	st, err := config.LoadState(*statePath)
	if err != nil {
		fmt.Printf("Error loading state: %v\n", err)
		os.Exit(1)
	}

	// Run the Bubble Tea program
	rootModel := tui.NewRootModel(cfg, st, *configPath, *statePath, *noBranchChange, *unrestricted, *disableBold, *disableRoundedBorders)
	rootModel.MissingConfig = missingConfig
	if missingConfig {
		rootModel.ActiveScreen = tui.ScreenSetup
	}
	p := tea.NewProgram(rootModel, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

func missingConfigNonInteractiveError() string {
	return "Error: milestones configuration not found. First-run setup requires an interactive terminal or an existing config file."
}

func isConfigMissing(configPath string) (bool, error) {
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

var Version = "development"

func printVersion() {
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		fmt.Printf("Cyclestone %s\n", info.Main.Version)
		return
	}
	fmt.Printf("Cyclestone %s\n", Version)
}
