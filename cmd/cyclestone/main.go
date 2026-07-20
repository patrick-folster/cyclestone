package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/tui"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cyclestone", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", ".cyclestone/milestone.yml", "Path to milestone.yml config file")
	statePath := flags.String("state", ".cyclestone/state.json", "Path to state.json tracking file")

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

	noBranchChange := flags.Bool("no-branch-change", defaultNoBranchChange, "Do not switch/create git branches during milestone execution")
	unrestricted := flags.Bool("unrestricted", defaultUnrestricted, "Run agents without sandbox/permission restrictions")
	disableBold := flags.Bool("no-bold", defaultDisableBold, "Disable bold text styling to prevent terminal rendering glitches")
	disableRoundedBorders := flags.Bool("no-rounded-borders", defaultDisableRoundedBorders, "Disable rounded borders to prevent terminal rendering glitches")
	versionFlag := flags.Bool("version", false, "Print the version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *versionFlag {
		printVersion(stdout)
		return 0
	}

	// Validate command line flags
	if *configPath == "" {
		fmt.Fprintln(stderr, "Error: -config parameter cannot be empty")
		return 1
	}
	if *statePath == "" {
		fmt.Fprintln(stderr, "Error: -state parameter cannot be empty")
		return 1
	}

	if flags.NArg() > 0 {
		return runPlanningCommand(flags.Args(), *configPath, stdout, stderr)
	}

	// Check if config file exists. Interactive initialization is handled by the TUI.
	fi, statErr := stdin.Stat()
	isInteractive := statErr == nil && (fi.Mode()&os.ModeCharDevice) != 0
	missingConfig, err := isConfigMissing(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error checking config: %v\n", err)
		return 1
	}
	if missingConfig && !isInteractive {
		fmt.Fprintln(stderr, missingConfigNonInteractiveError())
		return 1
	}

	// Load milestone configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error loading config: %v\n", err)
		return 1
	}

	// Load dynamic state tracking
	st, err := config.LoadState(*statePath)
	if err != nil {
		fmt.Fprintf(stderr, "Error loading state: %v\n", err)
		return 1
	}

	// Run the Bubble Tea program
	rootModel := tui.NewRootModel(cfg, st, *configPath, *statePath, *noBranchChange, *unrestricted, *disableBold, *disableRoundedBorders)
	rootModel.MissingConfig = missingConfig
	if missingConfig {
		rootModel.ActiveScreen = tui.ScreenSetup
	}
	p := tea.NewProgram(rootModel, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(stderr, "Error running TUI: %v\n", err)
		return 1
	}
	return 0
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

func printVersion(w io.Writer) {
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		fmt.Fprintf(w, "Cyclestone %s\n", info.Main.Version)
		return
	}
	fmt.Fprintf(w, "Cyclestone %s\n", Version)
}

func runReadOnlyCommand(args []string, configPath string, stdout, stderr io.Writer) int {
	return runPlanningCommand(args, configPath, stdout, stderr)
}

func runPlanningCommand(args []string, configPath string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 2 && args[0] == "plan" && args[1] == "list":
		return runPlanList(configPath, stdout, stderr)
	case len(args) == 3 && args[0] == "plan" && args[1] == "show":
		return runPlanShow(configPath, args[2], stdout, stderr)
	case len(args) == 4 && args[0] == "briefing" && args[1] == "show":
		return runBriefingShow(configPath, args[2], args[3], stdout, stderr)
	case len(args) >= 2 && args[0] == "plan":
		return runPlanMutatingCommand(args[1:], configPath, stdout, stderr)
	case len(args) >= 2 && args[0] == "briefing":
		return runBriefingMutatingCommand(args[1:], configPath, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unsupported command %q\n", strings.Join(args, " "))
		return 1
	}
}

type planningCommandContext struct {
	state        *config.PlanningState
	validation   config.PlanningValidationResult
	milestoneIDs map[string]bool
	plansDir     string
}

func runPlanList(configPath string, stdout, stderr io.Writer) int {
	state, validation, _, err := loadPlanningForCommand(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	printPlanningMessages(stderr, validation)

	if len(state.Plans) == 0 {
		fmt.Fprintln(stdout, "Plans: none")
		return commandStatusFromValidation(validation)
	}

	fmt.Fprintln(stdout, "Plans:")
	for _, plan := range state.Plans {
		progress := planProgress(plan)
		fmt.Fprintf(stdout, "- id: %s\n", plan.ID)
		fmt.Fprintf(stdout, "  title: %s\n", plan.Title)
		fmt.Fprintf(stdout, "  status: %s\n", plan.Status)
		fmt.Fprintf(stdout, "  briefings: %d\n", len(plan.Briefings))
		fmt.Fprintf(stdout, "  progress: %s\n", progress.String())
	}
	return commandStatusFromValidation(validation)
}

func runPlanShow(configPath, planID string, stdout, stderr io.Writer) int {
	state, validation, milestoneIDs, err := loadPlanningForCommand(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	printPlanningMessages(stderr, validation)

	plan, ok := findPlan(state, planID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", planID)
		return 1
	}
	printPlan(stdout, plan, milestoneIDs)
	return commandStatusFromValidation(validation)
}

func runBriefingShow(configPath, planID, briefingID string, stdout, stderr io.Writer) int {
	state, validation, milestoneIDs, err := loadPlanningForCommand(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	printPlanningMessages(stderr, validation)

	plan, ok := findPlan(state, planID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", planID)
		return 1
	}
	briefing, ok := findBriefing(plan, briefingID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", briefingID, planID)
		return 1
	}
	printBriefing(stdout, plan, briefing, milestoneIDs)
	return commandStatusFromValidation(validation)
}

func loadPlanningForCommand(configPath string) (*config.PlanningState, config.PlanningValidationResult, map[string]bool, error) {
	ctx, err := loadPlanningCommandContext(configPath)
	if err != nil {
		return nil, config.PlanningValidationResult{}, nil, err
	}
	return ctx.state, ctx.validation, ctx.milestoneIDs, nil
}

func loadPlanningCommandContext(configPath string) (planningCommandContext, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return planningCommandContext{}, fmt.Errorf("loading milestone config: %w", err)
	}
	milestoneIDs := make([]string, 0, len(cfg.Milestones))
	milestoneIDSet := make(map[string]bool, len(cfg.Milestones))
	for _, milestone := range cfg.Milestones {
		milestoneIDs = append(milestoneIDs, milestone.ID)
		milestoneIDSet[milestone.ID] = true
	}
	plansDir := filepath.Join(filepath.Dir(configPath), "plans")
	state, validation := config.LoadPlanningState(plansDir, config.WithKnownMilestoneIDs(milestoneIDs))
	return planningCommandContext{
		state:        state,
		validation:   validation,
		milestoneIDs: milestoneIDSet,
		plansDir:     plansDir,
	}, nil
}

func runPlanMutatingCommand(args []string, configPath string, stdout, stderr io.Writer) int {
	switch args[0] {
	case "create":
		return runPlanCreate(args[1:], configPath, stdout, stderr)
	case "edit":
		return runPlanEdit(args[1:], configPath, stdout, stderr)
	case "archive":
		return runPlanStatus(args[1:], configPath, "archive", "archived", "archived", stdout, stderr)
	case "restore":
		return runPlanStatus(args[1:], configPath, "restore", "active", "restored", stdout, stderr)
	case "delete":
		return runPlanDelete(args[1:], configPath, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unsupported command %q\n", "plan "+strings.Join(args, " "))
		return 1
	}
}

func runBriefingMutatingCommand(args []string, configPath string, stdout, stderr io.Writer) int {
	switch args[0] {
	case "add":
		return runBriefingAdd(args[1:], configPath, stdout, stderr)
	case "edit":
		return runBriefingEdit(args[1:], configPath, stdout, stderr)
	case "reorder":
		return runBriefingReorder(args[1:], configPath, stdout, stderr)
	case "archive":
		return runBriefingStatus(args[1:], configPath, "archive", "archived", "archived", stdout, stderr)
	case "restore":
		return runBriefingStatus(args[1:], configPath, "restore", "active", "restored", stdout, stderr)
	case "delete":
		return runBriefingDelete(args[1:], configPath, stdout, stderr)
	case "dependency":
		return runBriefingDependency(args[1:], configPath, stdout, stderr)
	case "link":
		return runBriefingLink(args[1:], configPath, stdout, stderr)
	case "unlink":
		return runBriefingUnlink(args[1:], configPath, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unsupported command %q\n", "briefing "+strings.Join(args, " "))
		return 1
	}
}

func runPlanCreate(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("plan create", stderr)
	title := flags.String("title", "", "Plan title")
	objective := flags.String("objective", "", "Plan objective")
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone plan create <plan-id> --title <title> --objective <objective> [--actor <actor>]")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	planID := flags.Arg(0)
	if _, exists := findPlan(ctx.state, planID); exists {
		fmt.Fprintf(stderr, "Error: Plan %q already exists\n", planID)
		return 1
	}
	if _, err := os.Stat(planFilePath(ctx.plansDir, planID)); err == nil {
		fmt.Fprintf(stderr, "Error: Plan file for %q already exists\n", planID)
		return 1
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "Error: inspecting Plan file: %v\n", err)
		return 1
	}
	now := planningTimestamp()
	plan := config.Plan{
		SchemaVersion: config.PlanningSchemaVersion,
		ID:            planID,
		Title:         strings.TrimSpace(*title),
		Objective:     strings.TrimSpace(*objective),
		Status:        "active",
		CreatedAt:     now,
		CreatedBy:     strings.TrimSpace(*actor),
		UpdatedAt:     now,
		UpdatedBy:     strings.TrimSpace(*actor),
		BriefingOrder: []string{},
		Briefings:     []config.Briefing{},
	}
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Plan %q created\n", plan.ID)
	return 0
}

func runPlanEdit(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("plan edit", stderr)
	title := flags.String("title", "", "Plan title")
	objective := flags.String("objective", "", "Plan objective")
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone plan edit <plan-id> [--title <title>] [--objective <objective>] [--actor <actor>]")
		return 1
	}
	if !flagWasSet(flags, "title") && !flagWasSet(flags, "objective") {
		fmt.Fprintln(stderr, "Error: plan edit requires at least one metadata field")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, ok := findPlan(ctx.state, flags.Arg(0))
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", flags.Arg(0))
		return 1
	}
	if flagWasSet(flags, "title") {
		plan.Title = strings.TrimSpace(*title)
	}
	if flagWasSet(flags, "objective") {
		plan.Objective = strings.TrimSpace(*objective)
	}
	touchPlan(&plan, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Plan %q updated\n", plan.ID)
	return 0
}

func runPlanStatus(args []string, configPath, command, status, action string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("plan "+command, stderr)
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 1 {
		fmt.Fprintf(stderr, "Error: usage: cyclestone plan %s <plan-id> [--actor <actor>]\n", command)
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, ok := findPlan(ctx.state, flags.Arg(0))
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", flags.Arg(0))
		return 1
	}
	plan.Status = status
	touchPlan(&plan, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Plan %q %s\n", plan.ID, action)
	return 0
}

func runPlanDelete(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("plan delete", stderr)
	confirm := flags.String("confirm", "", "Exact Plan ID required to confirm deletion")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone plan delete <plan-id> --confirm <plan-id>")
		return 1
	}
	planID := flags.Arg(0)
	if *confirm != planID {
		fmt.Fprintf(stderr, "Error: deleting Plan %q requires --confirm %s\n", planID, planID)
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	if _, ok := findPlan(ctx.state, planID); !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", planID)
		return 1
	}
	if err := os.Remove(planFilePath(ctx.plansDir, planID)); err != nil {
		fmt.Fprintf(stderr, "Error: deleting Plan %q: %v\n", planID, err)
		return 1
	}
	fmt.Fprintf(stdout, "Plan %q deleted\n", planID)
	return 0
}

func runBriefingAdd(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing add", stderr)
	title := flags.String("title", "", "Briefing title")
	objective := flags.String("objective", "", "Briefing objective")
	intent := flags.String("intent", "", "Briefing intent")
	completionSignal := flags.String("completion-signal", "", "Briefing completion signal")
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing add <plan-id> <briefing-id> --title <title> --objective <objective> --intent <intent> --completion-signal <signal> [--actor <actor>]")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, ok := findPlan(ctx.state, flags.Arg(0))
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", flags.Arg(0))
		return 1
	}
	briefingID := flags.Arg(1)
	if _, exists := findBriefing(plan, briefingID); exists {
		fmt.Fprintf(stderr, "Error: Briefing %q already exists in Plan %q\n", briefingID, plan.ID)
		return 1
	}
	now := planningTimestamp()
	plan.Briefings = append(plan.Briefings, config.Briefing{
		ID:               briefingID,
		Title:            strings.TrimSpace(*title),
		Objective:        strings.TrimSpace(*objective),
		Intent:           strings.TrimSpace(*intent),
		Status:           "active",
		CompletionSignal: strings.TrimSpace(*completionSignal),
		CreatedAt:        now,
		CreatedBy:        strings.TrimSpace(*actor),
		UpdatedAt:        now,
		UpdatedBy:        strings.TrimSpace(*actor),
	})
	plan.BriefingOrder = appendMissing(plan.BriefingOrder, briefingID)
	touchPlan(&plan, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing %q added to Plan %q\n", briefingID, plan.ID)
	return 0
}

func runBriefingEdit(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing edit", stderr)
	title := flags.String("title", "", "Briefing title")
	objective := flags.String("objective", "", "Briefing objective")
	intent := flags.String("intent", "", "Briefing intent")
	completionSignal := flags.String("completion-signal", "", "Briefing completion signal")
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing edit <plan-id> <briefing-id> [metadata flags]")
		return 1
	}
	if !flagWasSet(flags, "title") && !flagWasSet(flags, "objective") && !flagWasSet(flags, "intent") && !flagWasSet(flags, "completion-signal") {
		fmt.Fprintln(stderr, "Error: briefing edit requires at least one metadata field")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, briefingIndex, ok := findPlanBriefing(ctx.state, flags.Arg(0), flags.Arg(1))
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", flags.Arg(1), flags.Arg(0))
		return 1
	}
	briefing := &plan.Briefings[briefingIndex]
	if flagWasSet(flags, "title") {
		briefing.Title = strings.TrimSpace(*title)
	}
	if flagWasSet(flags, "objective") {
		briefing.Objective = strings.TrimSpace(*objective)
	}
	if flagWasSet(flags, "intent") {
		briefing.Intent = strings.TrimSpace(*intent)
	}
	if flagWasSet(flags, "completion-signal") {
		briefing.CompletionSignal = strings.TrimSpace(*completionSignal)
	}
	touchBriefing(&plan, briefing, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing %q updated in Plan %q\n", briefing.ID, plan.ID)
	return 0
}

func runBriefingReorder(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing reorder", stderr)
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() < 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing reorder <plan-id> <briefing-id> [<briefing-id>...] [--actor <actor>]")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, ok := findPlan(ctx.state, flags.Arg(0))
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", flags.Arg(0))
		return 1
	}
	requested := flags.Args()[1:]
	seen := make(map[string]bool, len(requested))
	for _, id := range requested {
		briefing, exists := findBriefing(plan, id)
		if !exists {
			fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", id, plan.ID)
			return 1
		}
		if briefing.Status == "archived" {
			fmt.Fprintf(stderr, "Error: archived Briefing %q cannot be placed in active order\n", id)
			return 1
		}
		if seen[id] {
			fmt.Fprintf(stderr, "Error: duplicate Briefing ID %q in reorder arguments\n", id)
			return 1
		}
		seen[id] = true
	}
	for _, briefing := range plan.Briefings {
		if briefing.Status != "archived" && !seen[briefing.ID] {
			requested = append(requested, briefing.ID)
		}
	}
	plan.BriefingOrder = requested
	touchPlan(&plan, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing order updated for Plan %q\n", plan.ID)
	return 0
}

func runBriefingStatus(args []string, configPath, command, status, action string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing "+command, stderr)
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 2 {
		fmt.Fprintf(stderr, "Error: usage: cyclestone briefing %s <plan-id> <briefing-id> [--actor <actor>]\n", command)
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, briefingIndex, ok := findPlanBriefing(ctx.state, flags.Arg(0), flags.Arg(1))
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", flags.Arg(1), flags.Arg(0))
		return 1
	}
	briefing := &plan.Briefings[briefingIndex]
	briefing.Status = status
	if status == "archived" {
		plan.BriefingOrder = removeString(plan.BriefingOrder, briefing.ID)
	} else {
		plan.BriefingOrder = appendMissing(plan.BriefingOrder, briefing.ID)
	}
	touchBriefing(&plan, briefing, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing %q %s in Plan %q\n", briefing.ID, action, plan.ID)
	return 0
}

func runBriefingDelete(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing delete", stderr)
	confirm := flags.String("confirm", "", "Exact Briefing ID required to confirm deletion")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing delete <plan-id> <briefing-id> --confirm <briefing-id>")
		return 1
	}
	planID, briefingID := flags.Arg(0), flags.Arg(1)
	if *confirm != briefingID {
		fmt.Fprintf(stderr, "Error: deleting Briefing %q requires --confirm %s\n", briefingID, briefingID)
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, briefingIndex, ok := findPlanBriefing(ctx.state, planID, briefingID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", briefingID, planID)
		return 1
	}
	plan.Briefings = append(plan.Briefings[:briefingIndex], plan.Briefings[briefingIndex+1:]...)
	plan.BriefingOrder = removeString(plan.BriefingOrder, briefingID)
	touchPlan(&plan, "manual")
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing %q deleted from Plan %q\n", briefingID, planID)
	return 0
}

func runBriefingDependency(args []string, configPath string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing dependency add|remove <plan-id> <briefing-id> <dependency-id>")
		return 1
	}
	switch args[0] {
	case "add":
		return runBriefingDependencyChange(args[1:], configPath, true, stdout, stderr)
	case "remove":
		return runBriefingDependencyChange(args[1:], configPath, false, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unsupported command %q\n", "briefing dependency "+strings.Join(args, " "))
		return 1
	}
}

func runBriefingDependencyChange(args []string, configPath string, add bool, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing dependency", stderr)
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 3 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing dependency add|remove <plan-id> <briefing-id> <dependency-id> [--actor <actor>]")
		return 1
	}
	planID, briefingID, dependencyID := flags.Arg(0), flags.Arg(1), flags.Arg(2)
	if add && briefingID == dependencyID {
		fmt.Fprintln(stderr, "Error: Briefing cannot depend on itself")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, briefingIndex, ok := findPlanBriefing(ctx.state, planID, briefingID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", briefingID, planID)
		return 1
	}
	if _, exists := findBriefing(plan, dependencyID); !exists {
		fmt.Fprintf(stderr, "Error: dependency Briefing %q not found in Plan %q\n", dependencyID, planID)
		return 1
	}
	briefing := &plan.Briefings[briefingIndex]
	if add {
		briefing.DependsOn = appendMissing(briefing.DependsOn, dependencyID)
	} else if !containsString(briefing.DependsOn, dependencyID) {
		fmt.Fprintf(stderr, "Error: Briefing %q does not depend on %q\n", briefingID, dependencyID)
		return 1
	} else {
		briefing.DependsOn = removeString(briefing.DependsOn, dependencyID)
	}
	touchBriefing(&plan, briefing, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	action := "added"
	if !add {
		action = "removed"
	}
	fmt.Fprintf(stdout, "Dependency %q %s for Briefing %q in Plan %q\n", dependencyID, action, briefingID, planID)
	return 0
}

func runBriefingLink(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing link", stderr)
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 3 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing link <plan-id> <briefing-id> <milestone-id> [--actor <actor>]")
		return 1
	}
	planID, briefingID, milestoneID := flags.Arg(0), flags.Arg(1), flags.Arg(2)
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	if !ctx.milestoneIDs[milestoneID] {
		fmt.Fprintf(stderr, "Error: Milestone %q not found\n", milestoneID)
		return 1
	}
	plan, briefingIndex, ok := findPlanBriefing(ctx.state, planID, briefingID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", briefingID, planID)
		return 1
	}
	briefing := &plan.Briefings[briefingIndex]
	if briefing.MilestoneID != "" && briefing.MilestoneID != milestoneID {
		fmt.Fprintf(stderr, "Error: Briefing %q is already linked to Milestone %q\n", briefingID, briefing.MilestoneID)
		return 1
	}
	if linkedPlanID, linkedBriefingID, exists := findActiveMilestoneLink(ctx.state, milestoneID, planID, briefingID); exists {
		fmt.Fprintf(stderr, "Error: Milestone %q is already linked by Briefing %q in Plan %q\n", milestoneID, linkedBriefingID, linkedPlanID)
		return 1
	}
	briefing.MilestoneID = milestoneID
	touchBriefing(&plan, briefing, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing %q linked to Milestone %q in Plan %q\n", briefingID, milestoneID, planID)
	return 0
}

func runBriefingUnlink(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing unlink", stderr)
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing unlink <plan-id> <briefing-id> [--actor <actor>]")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, briefingIndex, ok := findPlanBriefing(ctx.state, flags.Arg(0), flags.Arg(1))
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", flags.Arg(1), flags.Arg(0))
		return 1
	}
	briefing := &plan.Briefings[briefingIndex]
	briefing.MilestoneID = ""
	touchBriefing(&plan, briefing, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing %q unlinked in Plan %q\n", briefing.ID, plan.ID)
	return 0
}

func newPlanningFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func parsePlanningFlags(flags *flag.FlagSet, args []string, stderr io.Writer) bool {
	if err := flags.Parse(interspersedFlagArgs(args)); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return false
	}
	return true
}

func interspersedFlagArgs(args []string) []string {
	var flagArgs []string
	var positional []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			positional = append(positional, args[index+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flagArgs = append(flagArgs, arg)
			if !strings.Contains(arg, "=") && index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				index++
				flagArgs = append(flagArgs, args[index])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return append(flagArgs, positional...)
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	wasSet := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func loadWritablePlanningContext(configPath string, stderr io.Writer) (planningCommandContext, bool) {
	ctx, err := loadPlanningCommandContext(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return planningCommandContext{}, false
	}
	printPlanningMessages(stderr, ctx.validation)
	if ctx.validation.HasErrors() {
		fmt.Fprintln(stderr, "Error: planning files contain validation errors; no changes were written")
		return planningCommandContext{}, false
	}
	return ctx, true
}

func savePlanForCommand(ctx planningCommandContext, plan config.Plan, stderr io.Writer) bool {
	milestoneIDs := make([]string, 0, len(ctx.milestoneIDs))
	for id := range ctx.milestoneIDs {
		milestoneIDs = append(milestoneIDs, id)
	}
	validation, err := config.SavePlan(ctx.plansDir, plan, config.WithKnownMilestoneIDs(milestoneIDs))
	printPlanningMessages(stderr, validation)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return false
	}
	return true
}

func planFilePath(plansDir, planID string) string {
	return filepath.Join(plansDir, planID+".yml")
}

func planningTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func touchPlan(plan *config.Plan, actor string) {
	plan.UpdatedAt = planningTimestampAtOrAfter(plan.CreatedAt)
	plan.UpdatedBy = strings.TrimSpace(actor)
}

func touchBriefing(plan *config.Plan, briefing *config.Briefing, actor string) {
	briefing.UpdatedAt = planningTimestampAtOrAfter(briefing.CreatedAt)
	briefing.UpdatedBy = strings.TrimSpace(actor)
	touchPlan(plan, actor)
}

func planningTimestampAtOrAfter(createdAt string) string {
	now := time.Now().UTC()
	created, err := time.Parse(time.RFC3339, createdAt)
	if err == nil && now.Before(created) {
		return created.UTC().Format(time.RFC3339)
	}
	return now.Format(time.RFC3339)
}

func findPlanBriefing(state *config.PlanningState, planID, briefingID string) (config.Plan, int, bool) {
	plan, ok := findPlan(state, planID)
	if !ok {
		return config.Plan{}, -1, false
	}
	for index, briefing := range plan.Briefings {
		if briefing.ID == briefingID {
			return plan, index, true
		}
	}
	return config.Plan{}, -1, false
}

func findActiveMilestoneLink(state *config.PlanningState, milestoneID, excludePlanID, excludeBriefingID string) (string, string, bool) {
	for _, plan := range state.Plans {
		for _, briefing := range plan.Briefings {
			if briefing.MilestoneID != milestoneID {
				continue
			}
			if plan.ID == excludePlanID && briefing.ID == excludeBriefingID {
				continue
			}
			if briefing.Status == "active" || briefing.Status == "completed" {
				return plan.ID, briefing.ID, true
			}
		}
	}
	return "", "", false
}

func appendMissing(values []string, value string) []string {
	if containsString(values, value) {
		return values
	}
	return append(values, value)
}

func removeString(values []string, remove string) []string {
	filtered := values[:0]
	for _, value := range values {
		if value != remove {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type progressSummary struct {
	Completed int
	Total     int
}

func planProgress(plan config.Plan) progressSummary {
	var progress progressSummary
	for _, briefing := range plan.Briefings {
		if briefing.Status == "archived" {
			continue
		}
		progress.Total++
		if briefing.Status == "completed" {
			progress.Completed++
		}
	}
	return progress
}

func (p progressSummary) String() string {
	if p.Total == 0 {
		return "0/0 completed"
	}
	return fmt.Sprintf("%d/%d completed (%d%%)", p.Completed, p.Total, p.Completed*100/p.Total)
}

func findPlan(state *config.PlanningState, planID string) (config.Plan, bool) {
	for _, plan := range state.Plans {
		if plan.ID == planID {
			return plan, true
		}
	}
	return config.Plan{}, false
}

func findBriefing(plan config.Plan, briefingID string) (config.Briefing, bool) {
	for _, briefing := range plan.Briefings {
		if briefing.ID == briefingID {
			return briefing, true
		}
	}
	return config.Briefing{}, false
}

func orderedBriefings(plan config.Plan) []config.Briefing {
	byID := make(map[string]config.Briefing, len(plan.Briefings))
	for _, briefing := range plan.Briefings {
		byID[briefing.ID] = briefing
	}
	seen := make(map[string]bool, len(plan.Briefings))
	var ordered []config.Briefing
	for _, id := range plan.BriefingOrder {
		briefing, ok := byID[id]
		if !ok || seen[id] {
			continue
		}
		ordered = append(ordered, briefing)
		seen[id] = true
	}
	var remaining []string
	for id := range byID {
		if !seen[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		ordered = append(ordered, byID[id])
	}
	return ordered
}

func printPlan(w io.Writer, plan config.Plan, milestoneIDs map[string]bool) {
	fmt.Fprintf(w, "Plan: %s\n", plan.ID)
	fmt.Fprintf(w, "Title: %s\n", plan.Title)
	fmt.Fprintf(w, "Status: %s\n", plan.Status)
	fmt.Fprintf(w, "Objective: %s\n", plan.Objective)
	fmt.Fprintf(w, "Created: %s by %s\n", plan.CreatedAt, plan.CreatedBy)
	fmt.Fprintf(w, "Updated: %s by %s\n", plan.UpdatedAt, plan.UpdatedBy)
	fmt.Fprintf(w, "Progress: %s\n", planProgress(plan).String())
	printStringList(w, "Constraints", plan.Constraints)
	fmt.Fprintln(w, "Briefings:")
	briefings := orderedBriefings(plan)
	if len(briefings) == 0 {
		fmt.Fprintln(w, "- none")
		return
	}
	for _, briefing := range briefings {
		fmt.Fprintf(
			w,
			"- %s | %s | readiness: %s | milestone: %s | %s\n",
			briefing.ID,
			briefing.Status,
			briefingReadiness(plan, briefing),
			milestoneRelationship(briefing, milestoneIDs),
			briefing.Title,
		)
	}
}

func printBriefing(w io.Writer, plan config.Plan, briefing config.Briefing, milestoneIDs map[string]bool) {
	fmt.Fprintf(w, "Briefing: %s\n", briefing.ID)
	fmt.Fprintf(w, "Plan: %s\n", plan.ID)
	fmt.Fprintf(w, "Title: %s\n", briefing.Title)
	fmt.Fprintf(w, "Status: %s\n", briefing.Status)
	fmt.Fprintf(w, "Readiness: %s\n", briefingReadiness(plan, briefing))
	fmt.Fprintf(w, "Milestone: %s\n", milestoneRelationship(briefing, milestoneIDs))
	fmt.Fprintf(w, "Objective: %s\n", briefing.Objective)
	fmt.Fprintf(w, "Intent: %s\n", briefing.Intent)
	fmt.Fprintf(w, "Completion Signal: %s\n", briefing.CompletionSignal)
	printStringList(w, "Dependencies", briefing.DependsOn)
	printStringList(w, "Constraints", briefing.Constraints)
}

func printStringList(w io.Writer, label string, values []string) {
	fmt.Fprintf(w, "%s:\n", label)
	if len(values) == 0 {
		fmt.Fprintln(w, "- none")
		return
	}
	for _, value := range values {
		fmt.Fprintf(w, "- %s\n", value)
	}
}

func briefingReadiness(plan config.Plan, briefing config.Briefing) string {
	switch briefing.Status {
	case "completed":
		return "completed"
	case "archived":
		return "archived"
	}
	byID := make(map[string]config.Briefing, len(plan.Briefings))
	for _, candidate := range plan.Briefings {
		byID[candidate.ID] = candidate
	}
	for _, dependencyID := range briefing.DependsOn {
		dependency, ok := byID[dependencyID]
		if !ok || dependency.Status != "completed" {
			return "blocked"
		}
	}
	return "ready"
}

func milestoneRelationship(briefing config.Briefing, milestoneIDs map[string]bool) string {
	if briefing.MilestoneID == "" {
		return "none"
	}
	if milestoneIDs[briefing.MilestoneID] {
		return "linked " + briefing.MilestoneID
	}
	return "missing " + briefing.MilestoneID
}

func printPlanningMessages(w io.Writer, validation config.PlanningValidationResult) {
	for _, msg := range validation.Messages {
		fmt.Fprintf(w, "%s", strings.ToUpper(msg.Severity))
		if msg.File != "" {
			fmt.Fprintf(w, " %s", msg.File)
		}
		if msg.Field != "" {
			fmt.Fprintf(w, " %s", msg.Field)
		}
		fmt.Fprintf(w, ": %s\n", msg.Message)
	}
}

func commandStatusFromValidation(validation config.PlanningValidationResult) int {
	if validation.HasErrors() {
		return 1
	}
	return 0
}
