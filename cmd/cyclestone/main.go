package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/patrick-folster/cyclestone/internal/config"
	"github.com/patrick-folster/cyclestone/internal/executor"
	"github.com/patrick-folster/cyclestone/internal/tui"
	"github.com/patrick-folster/cyclestone/resources"
)



const maxPlanGenerationContextChars = 120000
const maxBriefingMilestoneContextChars = 120000

var runPlanGenerationRunner = executePlanGenerationRunner
var runPlanReevaluationRunner = executePlanGenerationRunner

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
		if len(flags.Args()) >= 2 && flags.Args()[0] == "plan" && (flags.Args()[1] == "start" || flags.Args()[1] == "resume") {
			return runPlanExecution(flags.Args()[2:], briefingExecutionOptions{
				configPath:            *configPath,
				statePath:             *statePath,
				noBranchChange:        *noBranchChange,
				unrestricted:          *unrestricted,
				disableBold:           *disableBold,
				disableRoundedBorders: *disableRoundedBorders,
			}, flags.Args()[1] == "resume", stdout, stderr, runRootProgram)
		}
		if len(flags.Args()) >= 2 && flags.Args()[0] == "briefing" && flags.Args()[1] == "execute" {
			return runBriefingExecute(flags.Args()[2:], briefingExecutionOptions{
				configPath:            *configPath,
				statePath:             *statePath,
				noBranchChange:        *noBranchChange,
				unrestricted:          *unrestricted,
				disableBold:           *disableBold,
				disableRoundedBorders: *disableRoundedBorders,
			}, stdout, stderr, runRootProgram)
		}
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

type briefingExecutionOptions struct {
	configPath            string
	statePath             string
	noBranchChange        bool
	unrestricted          bool
	disableBold           bool
	disableRoundedBorders bool
}

func runRootProgram(root tui.RootModel) error {
	_, err := tea.NewProgram(root, tea.WithAltScreen()).Run()
	return err
}

func runBriefingExecute(args []string, opts briefingExecutionOptions, stdout, stderr io.Writer, launch func(tui.RootModel) error) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing execute <plan-id> <briefing-id>")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(opts.configPath, stderr)
	if !ok {
		return 1
	}
	result, err := prepareBriefingMilestone(ctx, opts.configPath, briefingMilestoneRequest{
		planID:      args[0],
		briefingID:  args[1],
		actor:       "briefing-executor",
		allowActive: true,
		allowLinked: true,
	})
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if result.Generated {
		fmt.Fprintf(stdout, "Milestone %q generated from Briefing %q in Plan %q\n", result.Milestone.ID, args[1], args[0])
	}

	cfg, err := config.LoadConfig(opts.configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: reloading milestone config: %v\n", err)
		return 1
	}
	st, err := config.LoadState(opts.statePath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: loading state: %v\n", err)
		return 1
	}
	root := tui.NewRootModel(cfg, st, opts.configPath, opts.statePath, opts.noBranchChange, opts.unrestricted, opts.disableBold, opts.disableRoundedBorders)
	root.QueueBriefingCycle(result.Milestone, tui.BriefingOrigin{PlanID: args[0], BriefingID: args[1]})
	if err := launch(root); err != nil {
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
	case len(args) >= 2 && args[0] == "plan" && args[1] == "tree":
		return runPlanTree(args[2:], configPath, stdout, stderr)
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
	configPath   string
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
	rtState, _ := config.LoadState(filepath.Join(filepath.Dir(configPath), "state.json"))
	for _, plan := range state.Plans {
		progress := planProgress(plan)
		fmt.Fprintf(stdout, "- id: %s\n", plan.ID)
		fmt.Fprintf(stdout, "  title: %s\n", plan.Title)
		fmt.Fprintf(stdout, "  status: %s\n", plan.Status)
		fmt.Fprintf(stdout, "  briefings: %d\n", len(plan.Briefings))
		fmt.Fprintf(stdout, "  progress: %s\n", progress.String())
		if rtState != nil {
			if exec := rtState.GetPlanExecution(plan.ID); exec != nil {
				fmt.Fprintf(stdout, "  execution: %s (%s, %s)\n", exec.State, exec.Mode, exec.Checkpoint)
			}
		}
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
	rtState, _ := config.LoadState(filepath.Join(filepath.Dir(configPath), "state.json"))
	var exec *config.PlanExecution
	if rtState != nil {
		exec = rtState.GetPlanExecution(plan.ID)
	}
	printPlan(stdout, plan, milestoneIDs, buildMilestoneRelationIndex(state), exec)
	return commandStatusFromValidation(validation)
}

func runPlanTree(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("plan tree", stderr)
	ascii := flags.Bool("ascii", false, "Use ASCII tree branch glyphs")
	if err := flags.Parse(args); err != nil {
		return 1
	}

	var planID string
	if flags.NArg() > 0 {
		planID = flags.Arg(0)
	}

	state, validation, _, err := loadPlanningForCommand(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	printPlanningMessages(stderr, validation)

	if planID != "" {
		if _, ok := findPlan(state, planID); !ok {
			fmt.Fprintf(stderr, "Error: Plan %q not found\n", planID)
			return 1
		}
	}

	var milestones []config.Milestone
	if cfg, loadErr := config.LoadConfig(configPath); loadErr == nil && cfg != nil {
		milestones = cfg.Milestones
	}

	statePath := filepath.Join(filepath.Dir(configPath), "state.json")
	st, _ := config.LoadState(statePath)

	opts := tui.TreeOptions{
		PlanID:   planID,
		UseASCII: *ascii,
		Styled:   false,
	}

	out := tui.RenderTree(state.Plans, milestones, st, opts)
	fmt.Fprint(stdout, out)

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
	printBriefing(stdout, plan, briefing, milestoneIDs, buildMilestoneRelationIndex(state))
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
		configPath:   configPath,
	}, nil
}

func runPlanMutatingCommand(args []string, configPath string, stdout, stderr io.Writer) int {
	switch args[0] {
	case "create":
		return runPlanCreate(args[1:], configPath, stdout, stderr)
	case "generate":
		return runPlanGenerate(args[1:], configPath, stdout, stderr)
	case "reevaluate":
		return runPlanReevaluate(args[1:], configPath, stdout, stderr)
	case "edit":
		return runPlanEdit(args[1:], configPath, stdout, stderr)
	case "archive":
		return runPlanStatus(args[1:], configPath, "archive", "archived", "archived", stdout, stderr)
	case "restore":
		return runPlanStatus(args[1:], configPath, "restore", "active", "restored", stdout, stderr)
	case "approve":
		return runPlanStatus(args[1:], configPath, "approve", "completed", "approved", stdout, stderr)
	case "reject":
		return runPlanStatus(args[1:], configPath, "reject", "archived", "rejected", stdout, stderr)
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
	case "approve":
		return runBriefingStatus(args[1:], configPath, "approve", "completed", "approved", stdout, stderr)
	case "reject":
		return runBriefingStatus(args[1:], configPath, "reject", "archived", "rejected", stdout, stderr)
	case "delete":
		return runBriefingDelete(args[1:], configPath, stdout, stderr)
	case "split":
		return runBriefingSplit(args[1:], configPath, stdout, stderr)
	case "merge":
		return runBriefingMerge(args[1:], configPath, stdout, stderr)
	case "dependency":
		return runBriefingDependency(args[1:], configPath, stdout, stderr)
	case "link":
		return runBriefingLink(args[1:], configPath, stdout, stderr)
	case "unlink":
		return runBriefingUnlink(args[1:], configPath, stdout, stderr)
	case "generate-milestone":
		return runBriefingGenerateMilestone(args[1:], configPath, stdout, stderr)
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

type generatedPlanResponse = config.GeneratedPlanResponse
type generatedBriefingResponse = config.GeneratedBriefingResponse


func runPlanGenerate(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("plan generate", stderr)
	goal := flags.String("goal", "", "High-level goal for AI-assisted Plan generation")
	actor := flags.String("actor", "ai-plan-generator", "Actor recorded in planning metadata")
	preview := flags.Bool("preview", false, "Print the generated Plan without writing it")
	responseFile := flags.String("response-file", "", "Read structured generation response from a local file")
	runnerCommand := flags.String("runner-command", "", "Shell command used to generate structured Plan JSON")
	authorPrefix := flags.String("author-prefix", "", "Author prefix handle (e.g. pf, js)")
	if !parsePlanningFlags(flags, args, stderr) {
		return 1
	}
	if strings.TrimSpace(*goal) == "" && flags.NArg() > 0 {
		*goal = strings.Join(flags.Args(), " ")
	}
	if strings.TrimSpace(*goal) == "" {
		fmt.Fprintln(stderr, "Error: usage: cyclestone plan generate --goal <goal> [--preview] [--actor <actor>] [--runner-command <command>] [--response-file <path>]")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}

	var responseText string
	if strings.TrimSpace(*responseFile) != "" {
		data, err := os.ReadFile(*responseFile)
		if err != nil {
			fmt.Fprintf(stderr, "Error: reading generation response: %v\n", err)
			return 1
		}
		responseText = string(data)
	} else {
		prompt := buildPlanGenerationPrompt(configPath, *goal)
		text, err := runPlanGenerationRunner(*runnerCommand, prompt)
		if err != nil {
			fmt.Fprintf(stderr, "Error: generating Plan: %v\n", err)
			return 1
		}
		responseText = text
	}

	generated, err := parseGeneratedPlanResponse(responseText)
	if err != nil {
		fmt.Fprintf(stderr, "Error: invalid generated Plan response: %v\n", err)
		return 1
	}
	plan, err := convertGeneratedPlan(*goal, generated, strings.TrimSpace(*actor), planningTimestamp())
	if err != nil {
		fmt.Fprintf(stderr, "Error: invalid generated Plan response: %v\n", err)
		return 1
	}
	// Always resolve the author prefix from merged (global+project) settings so
	// the configured author_prefix (e.g. "pf") reaches AllocatePlanID even when
	// only the global settings.yml carries it. An explicit --author-prefix flag
	// takes precedence.
	pref := strings.TrimSpace(*authorPrefix)
	if pref == "" {
		pref = config.GetDefaultAuthorPrefix(config.LoadMergedSettings())
	}
	// Collect existing plan and briefing IDs across the entire planning state
	// so the allocated IDs are unique across all plans, not just the new one.
	existingPlanIDs := make([]string, 0, len(ctx.state.Plans))
	for _, p := range ctx.state.Plans {
		existingPlanIDs = append(existingPlanIDs, p.ID)
	}
	newPlanID := config.AllocatePlanID(pref, existingPlanIDs) + "-" + config.PlanningSlug(plan.Title)
	plan.ID = newPlanID
	// Briefing IDs are plan-scoped, starting at 0001 for each plan.
	existingBriefingIDs := make([]string, 0)
	// Map old briefing IDs to new prefixed IDs so DependsOn can be updated.
	oldToNew := map[string]string{}
	for i := range plan.Briefings {
		bSlug := config.PlanningSlug(plan.Briefings[i].Title)
		bID := config.AllocateBriefingID(plan.ID, pref, existingBriefingIDs)
		if bSlug != "" {
			bID = bID + "-" + bSlug
		}
		oldToNew[plan.Briefings[i].ID] = bID
		existingBriefingIDs = append(existingBriefingIDs, bID)
		plan.Briefings[i].ID = bID
	}
	// Update BriefingOrder and DependsOn to reference the new prefixed IDs.
	plan.BriefingOrder = make([]string, 0, len(plan.Briefings))
	for i := range plan.Briefings {
		plan.BriefingOrder = append(plan.BriefingOrder, plan.Briefings[i].ID)
		updatedDeps := make([]string, 0, len(plan.Briefings[i].DependsOn))
		for _, dep := range plan.Briefings[i].DependsOn {
			if newID, ok := oldToNew[dep]; ok {
				updatedDeps = append(updatedDeps, newID)
			} else {
				updatedDeps = append(updatedDeps, dep)
			}
		}
		plan.Briefings[i].DependsOn = updatedDeps
	}
	if _, exists := findPlan(ctx.state, plan.ID); exists {
		fmt.Fprintf(stderr, "Error: generated Plan %q already exists\n", plan.ID)
		return 1
	}
	if _, err := os.Stat(planFilePath(ctx.plansDir, plan.ID)); err == nil {
		fmt.Fprintf(stderr, "Error: Plan file for generated Plan %q already exists\n", plan.ID)
		return 1
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "Error: inspecting generated Plan file: %v\n", err)
		return 1
	}

	milestoneIDs := planningMilestoneIDs(ctx)
	validation := config.ValidatePlan(plan, planFilePath(ctx.plansDir, plan.ID), config.WithKnownMilestoneIDs(milestoneIDs))
	printPlanningMessages(stderr, validation)
	if validation.HasErrors() {
		fmt.Fprintln(stderr, "Error: generated Plan validation failed; no changes were written")
		return 1
	}
	if *preview {
		fmt.Fprintf(stdout, "Generated Plan %q preview\n", plan.ID)
		printPlan(stdout, plan, ctx.milestoneIDs, buildMilestoneRelationIndex(&config.PlanningState{Plans: []config.Plan{plan}}), nil)
		return 0
	}
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Plan %q generated\n", plan.ID)
	printPlan(stdout, plan, ctx.milestoneIDs, buildMilestoneRelationIndex(&config.PlanningState{Plans: []config.Plan{plan}}), nil)
	return 0
}

func runPlanReevaluate(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("plan reevaluate", stderr)
	goal := flags.String("goal", "", "Goal / trigger note for AI Plan re-evaluation")
	actor := flags.String("actor", "ai-planner", "Actor recorded in planning metadata")
	preview := flags.Bool("preview", false, "Print the re-evaluation proposal diff without writing changes")
	autoApply := flags.Bool("auto-apply", false, "Apply proposed plan changes automatically without interactive confirmation")
	responseFile := flags.String("response-file", "", "Read structured re-evaluation proposal response from a local file")
	runnerCommand := flags.String("runner-command", "", "Shell command used to generate structured Plan re-evaluation proposal JSON")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone plan reevaluate <plan-id> [--goal <goal>] [--preview] [--auto-apply] [--actor <actor>] [--runner-command <command>] [--response-file <path>]")
		return 1
	}

	planID := flags.Arg(0)
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}

	plan, ok := findPlan(ctx.state, planID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", planID)
		return 1
	}

	var responseText string
	if strings.TrimSpace(*responseFile) != "" {
		data, err := os.ReadFile(*responseFile)
		if err != nil {
			fmt.Fprintf(stderr, "Error: reading re-evaluation response: %v\n", err)
			return 1
		}
		responseText = string(data)
	} else {
		prompt := executor.BuildPlanReevaluationPrompt(".", plan, *goal)
		text, err := runPlanReevaluationRunner(*runnerCommand, prompt)
		if err != nil {
			fmt.Fprintf(stderr, "Error: re-evaluating Plan: %v\n", err)
			return 1
		}
		responseText = text
	}

	proposal, err := parsePlanReevaluationResponse(responseText)
	if err != nil {
		fmt.Fprintf(stderr, "Error: invalid re-evaluation response: %v\n", err)
		return 1
	}
	if proposal.PlanID == "" {
		proposal.PlanID = plan.ID
	}

	milestoneIDs := planningMilestoneIDs(ctx)
	validation := config.ValidatePlanReevaluationProposal(plan, proposal, milestoneIDs)
	printPlanningMessages(stderr, validation)
	if validation.HasErrors() {
		fmt.Fprintln(stderr, "Error: proposed Plan re-evaluation failed invariant validation; no changes were written")
		return 1
	}

	diff := config.ComputePlanDiff(plan, proposal)
	diffOutput := tui.RenderPlanDiff(diff, 80)
	fmt.Fprint(stdout, diffOutput)

	if !diff.HasChanges {
		fmt.Fprintln(stdout, "No plan modifications proposed.")
		return 0
	}

	if *preview {
		fmt.Fprintln(stdout, "Preview mode enabled; no changes were written to disk.")
		return 0
	}

	settings := config.LoadMergedSettings()
	shouldApply := *autoApply || (settings.AutoApplyPlanReevaluation != nil && *settings.AutoApplyPlanReevaluation)

	if !shouldApply {
		fmt.Fprint(stdout, "Apply proposed plan modifications? [y/N]: ")
		var answer string
		fmt.Scanln(&answer)
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(stdout, "Plan re-evaluation cancelled; no changes were written.")
			return 0
		}
	}

	updatedPlan := config.ApplyPlanReevaluationProposal(plan, proposal, strings.TrimSpace(*actor), planningTimestamp())
	if !savePlanForCommand(ctx, updatedPlan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Plan %q successfully updated via re-evaluation.\n", updatedPlan.ID)
	return 0
}

func parsePlanReevaluationResponse(text string) (config.PlanReevaluationProposal, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return config.PlanReevaluationProposal{}, fmt.Errorf("response is empty")
	}
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 3 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			text = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	// Find all occurrences of "{"
	var indices []int
	for i := 0; i < len(text); i++ {
		if text[i] == '{' {
			indices = append(indices, i)
		}
	}

	// Try parsing from each index, starting from the last one (most likely the model's actual response)
	for i := len(indices) - 1; i >= 0; i-- {
		startIdx := indices[i]
		decoder := json.NewDecoder(strings.NewReader(text[startIdx:]))
		decoder.DisallowUnknownFields()
		var proposal config.PlanReevaluationProposal
		if err := decoder.Decode(&proposal); err == nil {
			// Ensure it is not a placeholder response and has required fields
			if proposal.PlanID != "" && proposal.PlanID != "<plan-id>" && proposal.Rationale != "Explanation of proposed plan modifications based on execution findings" {
				return proposal, nil
			}
		}
	}

	return config.PlanReevaluationProposal{}, fmt.Errorf("response must contain one non-placeholder JSON object matching the re-evaluation contract")
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
	if err := config.DeletePlan(ctx.plansDir, planID); err != nil {
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

type briefingSplitPartFile struct {
	Parts []briefingPartInput `json:"parts"`
}

type briefingPartInput struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Objective        string   `json:"objective"`
	Intent           string   `json:"intent"`
	Status           string   `json:"status"`
	CompletionSignal string   `json:"completion_signal"`
	Constraints      []string `json:"constraints"`
	DependsOn        []string `json:"depends_on"`
}

func runBriefingSplit(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing split", stderr)
	partsFile := flags.String("parts-file", "", "JSON file containing {\"parts\":[...]}")
	milestoneLink := flags.String("milestone-link", "", "Part ID that keeps the source milestone link, or none")
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing split <plan-id> <briefing-id> --parts-file <path> [--milestone-link <part-id|none>] [--actor <actor>]")
		return 1
	}
	parts, ok := readBriefingPartsFile(*partsFile, stderr)
	if !ok {
		return 1
	}
	if len(parts) < 2 {
		fmt.Fprintln(stderr, "Error: briefing split requires at least two parts")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	plan, sourceIndex, ok := findPlanBriefing(ctx.state, flags.Arg(0), flags.Arg(1))
	if !ok {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", flags.Arg(1), flags.Arg(0))
		return 1
	}
	source := plan.Briefings[sourceIndex]
	if source.MilestoneID != "" && strings.TrimSpace(*milestoneLink) == "" {
		fmt.Fprintf(stderr, "Error: splitting linked Briefing %q requires --milestone-link <part-id|none>\n", source.ID)
		return 1
	}

	partIDs := make([]string, 0, len(parts))
	partIDSet := make(map[string]bool, len(parts))
	for _, part := range parts {
		partID := strings.TrimSpace(part.ID)
		if partID == "" {
			fmt.Fprintln(stderr, "Error: split part id is required")
			return 1
		}
		if partID == source.ID {
			fmt.Fprintf(stderr, "Error: split part ID %q must differ from source Briefing ID\n", partID)
			return 1
		}
		if partIDSet[partID] {
			fmt.Fprintf(stderr, "Error: duplicate split part ID %q\n", partID)
			return 1
		}
		if _, exists := findBriefing(plan, partID); exists {
			fmt.Fprintf(stderr, "Error: Briefing %q already exists in Plan %q\n", partID, plan.ID)
			return 1
		}
		partIDs = append(partIDs, partID)
		partIDSet[partID] = true
	}
	linkPartID := strings.TrimSpace(*milestoneLink)
	if source.MilestoneID != "" && linkPartID != "none" && !partIDSet[linkPartID] {
		fmt.Fprintf(stderr, "Error: --milestone-link must be one of the split part IDs or none\n")
		return 1
	}

	now := planningTimestamp()
	newBriefings := make([]config.Briefing, 0, len(parts))
	for index, part := range parts {
		status := strings.TrimSpace(part.Status)
		if status == "" {
			status = "active"
		}
		dependsOn := cleanStringList(part.DependsOn)
		if len(dependsOn) == 0 {
			if index == 0 {
				dependsOn = removeAnyString(cleanStringList(source.DependsOn), append(partIDs, source.ID)...)
			} else {
				dependsOn = []string{partIDs[index-1]}
			}
		}
		briefing := config.Briefing{
			ID:               strings.TrimSpace(part.ID),
			Title:            strings.TrimSpace(part.Title),
			Objective:        strings.TrimSpace(part.Objective),
			Intent:           strings.TrimSpace(part.Intent),
			Status:           status,
			CompletionSignal: strings.TrimSpace(part.CompletionSignal),
			CreatedAt:        now,
			CreatedBy:        strings.TrimSpace(*actor),
			UpdatedAt:        now,
			UpdatedBy:        strings.TrimSpace(*actor),
			Constraints:      cleanStringList(part.Constraints),
			DependsOn:        dependsOn,
		}
		if source.MilestoneID != "" && linkPartID == briefing.ID {
			briefing.MilestoneID = source.MilestoneID
		}
		newBriefings = append(newBriefings, briefing)
	}

	plan.Briefings = append(plan.Briefings[:sourceIndex], plan.Briefings[sourceIndex+1:]...)
	plan.Briefings = append(plan.Briefings, newBriefings...)
	plan.BriefingOrder = replaceOrderID(plan.BriefingOrder, source.ID, activeBriefingIDs(newBriefings)...)
	for _, id := range activeBriefingIDs(newBriefings) {
		plan.BriefingOrder = appendMissing(plan.BriefingOrder, id)
	}
	rewriteBriefingDependency(&plan, source.ID, partIDs[len(partIDs)-1], partIDSet)
	touchPlan(&plan, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Briefing %q split into %d Briefings in Plan %q\n", source.ID, len(newBriefings), plan.ID)
	return 0
}

func runBriefingMerge(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing merge", stderr)
	title := flags.String("title", "", "Merged Briefing title")
	objective := flags.String("objective", "", "Merged Briefing objective")
	intent := flags.String("intent", "", "Merged Briefing intent")
	completionSignal := flags.String("completion-signal", "", "Merged Briefing completion signal")
	status := flags.String("status", "", "Merged Briefing status")
	milestoneLink := flags.String("milestone-link", "", "Briefing ID whose milestone link is kept, or none")
	actor := flags.String("actor", "manual", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() < 3 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing merge <plan-id> <target-briefing-id> <merged-briefing-id> [<merged-briefing-id>...] --title <title> --objective <objective> --intent <intent> --completion-signal <signal> [--status <status>] [--milestone-link <briefing-id|none>] [--actor <actor>]")
		return 1
	}
	if strings.TrimSpace(*title) == "" || strings.TrimSpace(*objective) == "" || strings.TrimSpace(*intent) == "" || strings.TrimSpace(*completionSignal) == "" {
		fmt.Fprintln(stderr, "Error: briefing merge requires --title, --objective, --intent, and --completion-signal")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	planID := flags.Arg(0)
	mergeIDs := flags.Args()[1:]
	plan, ok := findPlan(ctx.state, planID)
	if !ok {
		fmt.Fprintf(stderr, "Error: Plan %q not found\n", planID)
		return 1
	}
	mergedSet := make(map[string]bool, len(mergeIDs))
	var merged []config.Briefing
	for _, id := range mergeIDs {
		if mergedSet[id] {
			fmt.Fprintf(stderr, "Error: duplicate Briefing ID %q in merge arguments\n", id)
			return 1
		}
		briefing, exists := findBriefing(plan, id)
		if !exists {
			fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", id, plan.ID)
			return 1
		}
		mergedSet[id] = true
		merged = append(merged, briefing)
	}

	linkByBriefing := make(map[string]string)
	for _, briefing := range merged {
		if briefing.MilestoneID != "" {
			linkByBriefing[briefing.ID] = briefing.MilestoneID
		}
	}
	linkChoice := strings.TrimSpace(*milestoneLink)
	if len(linkByBriefing) > 1 && linkChoice == "" {
		fmt.Fprintln(stderr, "Error: merging multiple linked Briefings requires --milestone-link <briefing-id|none>")
		return 1
	}
	keptMilestoneID := ""
	if linkChoice == "none" {
		keptMilestoneID = ""
	} else if linkChoice != "" {
		var exists bool
		keptMilestoneID, exists = linkByBriefing[linkChoice]
		if !exists {
			fmt.Fprintln(stderr, "Error: --milestone-link must name a merged Briefing with a milestone link or none")
			return 1
		}
	} else {
		for _, milestoneID := range linkByBriefing {
			keptMilestoneID = milestoneID
		}
	}

	targetID := mergeIDs[0]
	targetIndex := -1
	for index, briefing := range plan.Briefings {
		if briefing.ID == targetID {
			targetIndex = index
			break
		}
	}
	if targetIndex < 0 {
		fmt.Fprintf(stderr, "Error: Briefing %q not found in Plan %q\n", targetID, plan.ID)
		return 1
	}
	mergedStatus := strings.TrimSpace(*status)
	if mergedStatus == "" {
		mergedStatus = plan.Briefings[targetIndex].Status
	}
	plan.Briefings[targetIndex].Title = strings.TrimSpace(*title)
	plan.Briefings[targetIndex].Objective = strings.TrimSpace(*objective)
	plan.Briefings[targetIndex].Intent = strings.TrimSpace(*intent)
	plan.Briefings[targetIndex].CompletionSignal = strings.TrimSpace(*completionSignal)
	plan.Briefings[targetIndex].Status = mergedStatus
	plan.Briefings[targetIndex].MilestoneID = keptMilestoneID
	plan.Briefings[targetIndex].Constraints = mergedConstraints(merged)
	plan.Briefings[targetIndex].DependsOn = mergedDependencies(merged, mergedSet, targetID)
	touchBriefing(&plan, &plan.Briefings[targetIndex], *actor)

	filtered := plan.Briefings[:0]
	for _, briefing := range plan.Briefings {
		if briefing.ID != targetID && mergedSet[briefing.ID] {
			continue
		}
		filtered = append(filtered, briefing)
	}
	plan.Briefings = filtered
	plan.BriefingOrder = removeAnyString(plan.BriefingOrder, mergeIDs[1:]...)
	if mergedStatus == "archived" {
		plan.BriefingOrder = removeString(plan.BriefingOrder, targetID)
	} else {
		plan.BriefingOrder = appendMissing(plan.BriefingOrder, targetID)
	}
	rewriteBriefingDependency(&plan, "", targetID, mergedSet)
	touchPlan(&plan, *actor)
	if !savePlanForCommand(ctx, plan, stderr) {
		return 1
	}
	fmt.Fprintf(stdout, "Merged %d Briefings into %q in Plan %q\n", len(mergeIDs), targetID, plan.ID)
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
	replaceLink := flags.Bool("replace-link", false, "Replace an existing Briefing milestone link without deleting the old Milestone")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 3 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing link <plan-id> <briefing-id> <milestone-id> [--replace-link] [--actor <actor>]")
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
	if briefing.MilestoneID != "" && briefing.MilestoneID != milestoneID && !*replaceLink {
		fmt.Fprintf(stderr, "Error: Briefing %q is already linked to Milestone %q; pass --replace-link to replace only the Briefing link\n", briefingID, briefing.MilestoneID)
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

func runBriefingGenerateMilestone(args []string, configPath string, stdout, stderr io.Writer) int {
	flags := newPlanningFlagSet("briefing generate-milestone", stderr)
	milestoneID := flags.String("milestone-id", "", "Milestone ID to create; defaults to <plan-id>-<briefing-id>")
	preview := flags.Bool("preview", false, "Print the generated Milestone without writing it")
	replaceLink := flags.Bool("replace-link", false, "Replace an existing Briefing milestone link without deleting the old Milestone")
	actor := flags.String("actor", "briefing-milestone-generator", "Actor recorded in planning metadata")
	if !parsePlanningFlags(flags, args, stderr) || flags.NArg() != 2 {
		fmt.Fprintln(stderr, "Error: usage: cyclestone briefing generate-milestone <plan-id> <briefing-id> [--milestone-id <id>] [--preview] [--replace-link] [--actor <actor>]")
		return 1
	}
	ctx, ok := loadWritablePlanningContext(configPath, stderr)
	if !ok {
		return 1
	}
	result, err := prepareBriefingMilestone(ctx, configPath, briefingMilestoneRequest{
		planID:      flags.Arg(0),
		briefingID:  flags.Arg(1),
		targetID:    *milestoneID,
		actor:       *actor,
		replaceLink: *replaceLink,
		preview:     *preview,
		allowLinked: false,
		allowActive: false,
	})
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if *preview {
		fmt.Fprintf(stdout, "Generated Milestone %q preview\n", result.Milestone.ID)
		fmt.Fprintf(stdout, "Title: %s\n", result.Milestone.Title)
		fmt.Fprintf(stdout, "Spec Path: %s\n", result.Milestone.SpecPath)
		fmt.Fprintf(stdout, "Briefing Link: Plan %q Briefing %q -> Milestone %q\n\n", flags.Arg(0), flags.Arg(1), result.Milestone.ID)
		fmt.Fprint(stdout, result.Spec)
		return 0
	}
	fmt.Fprintf(stdout, "Milestone %q generated from Briefing %q in Plan %q\n", result.Milestone.ID, flags.Arg(1), flags.Arg(0))
	return 0
}

type briefingMilestoneRequest struct {
	planID      string
	briefingID  string
	targetID    string
	actor       string
	replaceLink bool
	preview     bool
	allowLinked bool
	allowActive bool
}

type briefingMilestoneResult struct {
	Milestone config.Milestone
	Spec      string
	Generated bool
}

var (
	addPlanningMilestoneWithSpec   = addPlanningMilestoneWithSpecFn
	indexExistingPlanningMilestone = indexExistingPlanningMilestoneFn
)

// addPlanningMilestoneWithSpecFn creates a milestone via the folder-per-item layout
// using the supplied spec Markdown, mirroring the legacy config.AddMilestoneWithSpec signature.
func addPlanningMilestoneWithSpecFn(configPath string, milestone config.Milestone, spec string) error {
	milestonesDir := filepath.Join(filepath.Dir(configPath), "milestones")
	_, err := config.SaveMilestoneToFolder(milestonesDir, milestone, spec)
	return err
}

// indexExistingPlanningMilestoneFn re-indexes an existing milestone via the folder-per-item
// layout, mirroring the legacy config.AddMilestone signature.
func indexExistingPlanningMilestoneFn(configPath string, milestone config.Milestone) error {
	milestonesDir := filepath.Join(filepath.Dir(configPath), "milestones")
	// Read the existing spec content if the flat .md exists.
	specContent := ""
	absSpec := filepath.Join(milestonesDir, milestone.ID+".md")
	if data, err := os.ReadFile(absSpec); err == nil {
		specContent = string(data)
	}
	if specContent == "" && milestone.SpecPath != "" {
		alt := milestone.SpecPath
		if !filepath.IsAbs(alt) {
			alt = filepath.Join(filepath.Dir(configPath), alt)
		}
		if data, err := os.ReadFile(alt); err == nil {
			specContent = string(data)
		}
	}
	_, err := config.SaveMilestoneToFolder(milestonesDir, milestone, specContent)
	return err
}

// prepareBriefingMilestone is the shared planning-layer resolver used by both
// generation and execution. It never changes runtime state, reports, or branches.
func prepareBriefingMilestone(ctx planningCommandContext, configPath string, req briefingMilestoneRequest) (briefingMilestoneResult, error) {
	plan, briefingIndex, ok := findPlanBriefing(ctx.state, req.planID, req.briefingID)
	if !ok {
		if _, exists := findPlan(ctx.state, req.planID); !exists {
			return briefingMilestoneResult{}, fmt.Errorf("Plan %q not found", req.planID)
		}
		return briefingMilestoneResult{}, fmt.Errorf("Briefing %q not found in Plan %q", req.briefingID, req.planID)
	}
	briefing := plan.Briefings[briefingIndex]
	eligible := briefing.Status == "completed" || (req.allowActive && briefing.Status == "active")
	if !eligible {
		if req.allowActive {
			return briefingMilestoneResult{}, fmt.Errorf("Briefing %q is not eligible for execution: status is %q", briefing.ID, briefing.Status)
		}
		return briefingMilestoneResult{}, fmt.Errorf("Briefing %q must be completed before generating a Milestone", briefing.ID)
	}
	if blockedBy := incompleteBriefingDependencies(plan, briefing); len(blockedBy) > 0 {
		return briefingMilestoneResult{}, fmt.Errorf("Briefing %q has incomplete dependencies: %s", briefing.ID, strings.Join(blockedBy, ", "))
	}

	if req.allowLinked && briefing.MilestoneID != "" {
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return briefingMilestoneResult{}, fmt.Errorf("loading linked Milestone: %w", err)
		}
		for _, milestone := range cfg.Milestones {
			if milestone.ID == briefing.MilestoneID {
				return briefingMilestoneResult{Milestone: milestone}, nil
			}
		}
		return briefingMilestoneResult{}, fmt.Errorf("Briefing %q links missing Milestone %q", briefing.ID, briefing.MilestoneID)
	}

	targetID := strings.TrimSpace(req.targetID)
	if targetID == "" {
		// Before allocating a new ms-<author>-NNNN[-slug] ID, check whether a
		// milestone was already generated from this Briefing in a previous
		// interrupted run. If the spec file exists on disk and contains the
		// expected Briefing/Plan markers, reuse that ID so the recovery
		// mechanism can reclaim it instead of generating a duplicate.
		baseDir := filepath.Dir(configPath)
		authorPref := config.GetDefaultAuthorPrefix(config.LoadMergedSettings())
		existingMilestoneIDs := planningMilestoneIDs(ctx)
		expectedSlug := config.PlanningSlug(briefing.Title)
		_ = expectedSlug // used for potential future matching refinement
		for _, existingID := range existingMilestoneIDs {
			specCandidate := filepath.Join(baseDir, "milestones", existingID, existingID+".md")
			data, err := os.ReadFile(specCandidate)
			if err != nil {
				// Fall back to legacy flat .md path.
				flatCandidate := filepath.Join(baseDir, "milestones", existingID+".md")
				data, err = os.ReadFile(flatCandidate)
				if err != nil {
					continue
				}
			}
			content := string(data)
			if strings.Contains(content, fmt.Sprintf("Briefing `%s` in Plan `%s`", briefing.ID, plan.ID)) &&
				strings.Contains(content, fmt.Sprintf("- Source Plan: `%s`", plan.ID)) &&
				strings.Contains(content, fmt.Sprintf("- Source Briefing: `%s`", briefing.ID)) {
				targetID = existingID
				break
			}
		}
		if targetID == "" {
			targetID = config.AllocateMilestoneID(plan.ID, authorPref, existingMilestoneIDs)
			if slug := config.PlanningSlug(briefing.Title); slug != "" {
				targetID = targetID + "-" + slug
			}
		}
	}
	if !milestoneIDPattern.MatchString(targetID) {
		return briefingMilestoneResult{}, fmt.Errorf("Milestone ID %q must use lowercase letters, numbers, and hyphens", targetID)
	}
	if linkedPlanID, linkedBriefingID, exists := findActiveMilestoneLink(ctx.state, targetID, plan.ID, briefing.ID); exists {
		return briefingMilestoneResult{}, fmt.Errorf("Milestone %q is already linked by Briefing %q in Plan %q", targetID, linkedBriefingID, linkedPlanID)
	}
	if briefing.MilestoneID != "" && briefing.MilestoneID != targetID && !req.replaceLink {
		return briefingMilestoneResult{}, fmt.Errorf("Briefing %q is already linked to Milestone %q; pass --replace-link to replace only the Briefing link", briefing.ID, briefing.MilestoneID)
	}

	spec := buildBriefingMilestoneSpec(configPath, ctx, plan, briefing, targetID)
	actor := strings.TrimSpace(req.actor)
	if actor == "" {
		actor = "planning"
	}
	now := planningTimestamp()
	prefix := config.GetMilestonePrefix(targetID)
	milestone := config.Milestone{
		ID:               targetID,
		Title:            briefing.Title,
		SpecPath:         filepath.Join("milestones", targetID, prefix+"-specification.md"),
		CreatedBy:        actor,
		UpdatedBy:        actor,
		CreatedAt:        now,
		UpdatedAt:        now,
		ParentBriefingID: briefing.ID,
		ParentPlanID:     plan.ID,
	}
	result := briefingMilestoneResult{Milestone: milestone, Spec: spec}
	if req.preview {
		return result, nil
	}
	// Resolve the spec path, checking the folder-per-item directory first, then
	// the legacy flat .md path for interrupted pre-folder migrations.
	baseDir := filepath.Dir(configPath)
	absoluteSpecPath := filepath.Join(baseDir, "milestones", targetID, prefix+"-specification.md")
	if _, err := os.Stat(absoluteSpecPath); err != nil {
		// Fall back to legacy flat .md path.
		flatPath := filepath.Join(baseDir, "milestones", targetID+".md")
		if _, err := os.Stat(flatPath); err == nil {
			absoluteSpecPath = flatPath
		}
	}
	if ctx.milestoneIDs[targetID] {
		if !req.allowActive || briefing.MilestoneID != "" {
			return briefingMilestoneResult{}, fmt.Errorf("Milestone %q already exists", targetID)
		}
		indexed, exists := milestoneByID(ctx, targetID)
		if !exists || !reclaimableBriefingMilestone(indexed, milestone, absoluteSpecPath, plan.ID, briefing.ID) {
			return briefingMilestoneResult{}, fmt.Errorf("Milestone %q already exists and cannot be proven to belong to Briefing %q in Plan %q", targetID, briefing.ID, plan.ID)
		}
		plan.Briefings[briefingIndex].MilestoneID = targetID
		touchBriefing(&plan, &plan.Briefings[briefingIndex], req.actor)
		if !savePlanForCommand(ctx, plan, io.Discard) {
			return result, fmt.Errorf("recovered Milestone %q, but its Briefing link could not be persisted; execution was not started", targetID)
		}
		return result, nil
	}
	_, specStatErr := os.Stat(absoluteSpecPath)
	specExisted := specStatErr == nil
	if specExisted {
		if !req.allowActive || !reclaimableBriefingMilestone(milestone, milestone, absoluteSpecPath, plan.ID, briefing.ID) {
			return briefingMilestoneResult{}, fmt.Errorf("Milestone spec %q already exists and cannot be proven to belong to Briefing %q in Plan %q", milestone.SpecPath, briefing.ID, plan.ID)
		}
		if err := indexExistingPlanningMilestone(configPath, milestone); err != nil {
			return result, fmt.Errorf("reindexing interrupted Milestone %q: %w", targetID, err)
		}
		ctx.milestoneIDs[targetID] = true
	} else {
		if err := addPlanningMilestoneWithSpec(configPath, milestone, spec); err != nil {
			if _, statErr := os.Stat(absoluteSpecPath); statErr == nil {
				return result, fmt.Errorf("Milestone spec %q was written, but the compact index update failed; execution was not started: %w", milestone.SpecPath, err)
			}
			return briefingMilestoneResult{}, fmt.Errorf("creating Milestone: %w", err)
		}
		result.Generated = true
	}
	ctx.milestoneIDs[targetID] = true
	plan.Briefings[briefingIndex].MilestoneID = targetID
	touchBriefing(&plan, &plan.Briefings[briefingIndex], req.actor)
	if !savePlanForCommand(ctx, plan, io.Discard) {
		return result, fmt.Errorf("Milestone %q was created, but its Briefing link could not be persisted; execution was not started", targetID)
	}
	return result, nil
}

func reclaimableBriefingMilestone(indexed, expected config.Milestone, specPath, planID, briefingID string) bool {
	if indexed.ID != expected.ID || indexed.Title != expected.Title {
		return false
	}
	// Compare spec paths by base name to handle absolute vs relative path differences
	// between legacy flat specs and folder-per-item layouts.
	if filepath.Base(indexed.SpecPath) != filepath.Base(expected.SpecPath) {
		return false
	}
	data, err := os.ReadFile(specPath)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, fmt.Sprintf("# Milestone Spec: %s - %s", expected.ID, expected.Title)) &&
		strings.Contains(content, fmt.Sprintf("Briefing `%s` in Plan `%s`", briefingID, planID)) &&
		strings.Contains(content, fmt.Sprintf("- Source Plan: `%s`", planID)) &&
		strings.Contains(content, fmt.Sprintf("- Source Briefing: `%s`", briefingID))
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
	milestoneIDs := planningMilestoneIDs(ctx)
	_, validation, err := savePlanningPlan(ctx.plansDir, plan, config.WithKnownMilestoneIDs(milestoneIDs))
	printPlanningMessages(stderr, validation)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return false
	}
	return true
}

var savePlanningPlan = config.SavePlanToFolder

func planningMilestoneIDs(ctx planningCommandContext) []string {
	milestoneIDs := make([]string, 0, len(ctx.milestoneIDs))
	for id := range ctx.milestoneIDs {
		milestoneIDs = append(milestoneIDs, id)
	}
	sort.Strings(milestoneIDs)
	return milestoneIDs
}

func parseGeneratedPlanResponse(text string) (generatedPlanResponse, error) {


	return config.ParseGeneratedPlanResponse(text)
}

func convertGeneratedPlan(goal string, response generatedPlanResponse, actor, now string) (config.Plan, error) {
	return config.ConvertGeneratedPlan(goal, response, actor, now)
}

func planningSlug(value string) string {
	return config.PlanningSlug(value)
}

func cleanStringList(values []string) []string {
	return config.CleanStringList(values)
}

func uniquePlanningSlug(title string, used map[string]bool) string {
	return config.UniquePlanningSlug(title, used)
}


func buildPlanGenerationPrompt(configPath, goal string) string {
	root := filepath.Dir(filepath.Dir(configPath))
	var sb strings.Builder
	sb.WriteString("# Cyclestone Plan Generation\n\n")
	if resources.PlanCreatorPrompt != "" {
		prompt := resources.PlanCreatorPrompt
		prompt = strings.ReplaceAll(prompt, "{{GOAL}}", goal)
		prompt = strings.ReplaceAll(prompt, "{{TITLE}}", "")
		prompt = strings.ReplaceAll(prompt, "{{PLAN_ID}}", "")
		sb.WriteString(prompt)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Generate one reviewable Cyclestone Plan from the user goal. Return only one JSON object matching this contract:\n\n")
	sb.WriteString(`{"title":"Plan title","objective":"Plan objective","constraints":["optional"],"briefings":[{"title":"Briefing title","objective":"Briefing objective","intent":"Why this matters","completion_signal":"Observable done signal","constraints":["optional"],"depends_on":["same Plan briefing title or id"]}]}`)
	sb.WriteString("\n\nRules:\n")
	sb.WriteString("- Do not include `milestone_id` on any Briefing.\n")
	sb.WriteString("- Do not create Milestone specs, compact index entries, reports, state, temp files, branches, or runtime artifacts.\n")
	sb.WriteString("- Dependencies must reference only Briefings in the same generated Plan.\n")
	sb.WriteString("- Keep Briefings ordered for future execution and use concise, concrete titles.\n\n")
	sb.WriteString("## User Goal\n\n")
	sb.WriteString(strings.TrimSpace(goal))
	sb.WriteString("\n\n")
	appendGenerationContextFile(&sb, root, "AGENTS.md")
	appendGenerationContextFile(&sb, root, filepath.Join(".cyclestone", "DECISIONS.md"))
	appendGenerationContextFile(&sb, root, filepath.Join("docs", "architecture.md"))
	appendGenerationContextFile(&sb, root, filepath.Join("docs", "planning-data-models.md"))
	appendGenerationTrackedStructure(&sb, root)
	return limitPlanGenerationContext(sb.String())
}



func appendGenerationContextFile(sb *strings.Builder, root, rel string) {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return
	}
	sb.WriteString("## " + rel + "\n\n```text\n")
	sb.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		sb.WriteByte('\n')
	}
	sb.WriteString("```\n\n")
}

func appendGenerationTrackedStructure(sb *strings.Builder, root string) {
	sb.WriteString("## Tracked Repository Structure\n\n```text\n")
	cmd := exec.Command("git", "-C", root, "ls-files")
	if out, err := cmd.Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, ".cyclestone/reports/") || strings.HasPrefix(line, ".cyclestone/temp/") {
				continue
			}
			sb.WriteString(line + "\n")
		}
	}
	sb.WriteString("```\n\n")
}

func limitPlanGenerationContext(text string) string {
	if len([]rune(text)) <= maxPlanGenerationContextChars {
		return text
	}
	runes := []rune(text)
	head := maxPlanGenerationContextChars / 2
	tail := maxPlanGenerationContextChars - head
	return string(runes[:head]) + "\n\n[...plan generation context truncated...]\n\n" + string(runes[len(runes)-tail:])
}

func executePlanGenerationRunner(runnerCommand, prompt string) (string, error) {
	runnerCommand = strings.TrimSpace(runnerCommand)
	if runnerCommand == "" {
		settings := config.LoadMergedSettings()
		command, err := defaultPlanGenerationRunnerCommand(settings)
		if err != nil {
			return "", err
		}
		runnerCommand = command
	}
	if runnerCommand == "" {
		return "", fmt.Errorf("no generation runner configured; pass --response-file or --runner-command")
	}
	cmd := exec.Command("bash", "-c", runnerCommand)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText != "" {
			return "", fmt.Errorf("%w: %s", err, errText)
		}
		return "", err
	}
	if strings.TrimSpace(stdout.String()) == "" && strings.TrimSpace(stderr.String()) != "" {
		return stderr.String(), nil
	}
	return stdout.String(), nil
}

func defaultPlanGenerationRunnerCommand(settings config.Settings) (string, error) {
	switch settings.DefaultLLM {
	case "", "codex":
		return "codex --sandbox read-only --ask-for-approval never exec --cd . --skip-git-repo-check -- -", nil
	case "ollama-codex":
		model := strings.TrimSpace(settings.OllamaCodexModel)
		if model == "" {
			return "", fmt.Errorf("ollama-codex generation requires an ollama_codex_model setting or --runner-command")
		}
		return fmt.Sprintf("ollama launch codex --model %s -- --sandbox read-only --ask-for-approval never exec --cd . --skip-git-repo-check -- -", shellQuote(model)), nil
	default:
		return "", fmt.Errorf("default runner %q is not supported for Plan generation; pass --runner-command or --response-file", settings.DefaultLLM)
	}
}

var safeShellWordPattern = regexp.MustCompile(`^[A-Za-z0-9._:/@%+=,-]+$`)
var milestoneIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func shellQuote(value string) string {
	if safeShellWordPattern.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func incompleteBriefingDependencies(plan config.Plan, briefing config.Briefing) []string {
	byID := make(map[string]config.Briefing, len(plan.Briefings))
	for _, candidate := range plan.Briefings {
		byID[candidate.ID] = candidate
	}
	var incomplete []string
	for _, dependencyID := range briefing.DependsOn {
		dependency, ok := byID[dependencyID]
		if !ok || dependency.Status != "completed" {
			incomplete = append(incomplete, dependencyID)
		}
	}
	return incomplete
}

func buildBriefingMilestoneSpec(configPath string, ctx planningCommandContext, plan config.Plan, briefing config.Briefing, milestoneID string) string {
	root := filepath.Dir(filepath.Dir(configPath))
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Milestone Spec: %s - %s\n\n", milestoneID, briefing.Title)
	sb.WriteString("## Goal\n")
	sb.WriteString(strings.TrimSpace(briefing.Objective))
	sb.WriteString("\n\n")

	sb.WriteString("## Implementation Prompt\n")
	fmt.Fprintf(&sb, "Implement the work prepared by Briefing `%s` in Plan `%s`.\n\n", briefing.ID, plan.ID)
	appendLabeledParagraph(&sb, "Plan Objective", plan.Objective)
	appendLabeledParagraph(&sb, "Briefing Intent", briefing.Intent)
	appendLabeledParagraph(&sb, "Completion Signal", briefing.CompletionSignal)

	sb.WriteString("## Scope\n")
	writeBulletList(&sb, append([]string{
		"Make the repository changes needed to satisfy the selected Briefing.",
		"Use the repository context and existing architecture summarized below.",
	}, cleanStringList(append(plan.Constraints, briefing.Constraints...))...))
	sb.WriteString("\n")

	sb.WriteString("## Explicit Exclusions\n")
	writeBulletList(&sb, []string{
		"Do not make Milestone execution depend on Plans, Briefings, or `.cyclestone/plans/*.yml`.",
		"Do not start a runner cycle, mutate reports, edit runtime state, or change git branches as part of creation.",
		"Do not auto-link unrelated standalone Milestones to this Briefing.",
	})
	sb.WriteString("\n")

	sb.WriteString("## Acceptance Criteria\n")
	writeChecklist(&sb, []string{
		briefing.CompletionSignal,
		"The implementation remains consistent with `AGENTS.md`, `.cyclestone/DECISIONS.md`, and the documented architecture.",
		"Relevant automated tests are added or updated and pass with the narrowest useful Go test command.",
		"Existing standalone Milestones and runtime artifacts remain unrelated to this generated Milestone unless explicitly changed by the implementation work.",
	})
	sb.WriteString("\n")

	sb.WriteString("## Repository Context\n")
	fmt.Fprintf(&sb, "- Source Plan: `%s` (%s)\n", plan.ID, plan.Title)
	fmt.Fprintf(&sb, "- Source Briefing: `%s` (%s)\n", briefing.ID, briefing.Title)
	fmt.Fprintf(&sb, "- Briefing Status At Generation: `%s`\n", briefing.Status)
	printInlineList(&sb, "- Dependencies", briefing.DependsOn)
	printInlineList(&sb, "- Plan Constraints", plan.Constraints)
	printInlineList(&sb, "- Briefing Constraints", briefing.Constraints)
	sb.WriteString("\n")

	appendCompletedMilestoneContext(&sb, configPath)
	appendGenerationContextFile(&sb, root, "AGENTS.md")
	appendGenerationContextFile(&sb, root, filepath.Join(".cyclestone", "DECISIONS.md"))
	appendGenerationContextFile(&sb, root, filepath.Join("docs", "architecture.md"))
	appendGenerationContextFile(&sb, root, filepath.Join("docs", "planning-data-models.md"))
	appendRelevantTestContext(&sb, root)
	appendGenerationTrackedStructure(&sb, root)

	sb.WriteString("## Testing Expectations\n")
	writeBulletList(&sb, []string{
		"Run targeted Go tests for touched packages first.",
		"Run `git diff --check` before handoff.",
		"Broaden to `go test ./... -count=1` when changes span shared behavior.",
	})
	return limitBriefingMilestoneContext(sb.String())
}

func appendLabeledParagraph(sb *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(sb, "**%s:** %s\n\n", label, value)
}

func writeBulletList(sb *strings.Builder, values []string) {
	wrote := false
	for _, value := range cleanStringList(values) {
		fmt.Fprintf(sb, "- %s\n", value)
		wrote = true
	}
	if !wrote {
		sb.WriteString("- None.\n")
	}
}

func writeChecklist(sb *strings.Builder, values []string) {
	wrote := false
	for _, value := range cleanStringList(values) {
		fmt.Fprintf(sb, "- [ ] %s\n", value)
		wrote = true
	}
	if !wrote {
		sb.WriteString("- [ ] Implementation behavior is complete and verified.\n")
	}
}

func printInlineList(sb *strings.Builder, label string, values []string) {
	cleaned := cleanStringList(values)
	if len(cleaned) == 0 {
		fmt.Fprintf(sb, "%s: none\n", label)
		return
	}
	fmt.Fprintf(sb, "%s: %s\n", label, strings.Join(cleaned, ", "))
}

func appendCompletedMilestoneContext(sb *strings.Builder, configPath string) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return
	}
	state, _ := config.LoadState(filepath.Join(filepath.Dir(configPath), "state.json"))
	sb.WriteString("## Existing And Completed Milestones\n\n")
	if len(cfg.Milestones) == 0 {
		sb.WriteString("- none\n\n")
		return
	}
	for _, milestone := range cfg.Milestones {
		status := ""
		if state != nil {
			status = state.GetMilestoneStatus(milestone.ID)
		}
		if status == "" {
			status = "Todo"
		}
		fmt.Fprintf(sb, "- %s | %s | status: %s\n", milestone.ID, milestone.Title, status)
	}
	sb.WriteString("\n")
}

func appendRelevantTestContext(sb *strings.Builder, root string) {
	sb.WriteString("## Existing Test Files\n\n```text\n")
	wrote := false
	cmd := exec.Command("git", "-C", root, "ls-files", "*_test.go")
	if out, err := cmd.Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				sb.WriteString(line + "\n")
				wrote = true
			}
		}
	}
	if !wrote {
		for _, rel := range []string{
			filepath.Join("cmd", "cyclestone", "main_test.go"),
			filepath.Join("internal", "config", "config_test.go"),
			filepath.Join("internal", "config", "planning_test.go"),
		} {
			sb.WriteString(rel + "\n")
		}
	}
	sb.WriteString("```\n\n")
	appendGenerationContextFile(sb, root, filepath.Join("cmd", "cyclestone", "main_test.go"))
	appendGenerationContextFile(sb, root, filepath.Join("internal", "config", "config_test.go"))
	appendGenerationContextFile(sb, root, filepath.Join("internal", "config", "planning_test.go"))
}

func limitBriefingMilestoneContext(text string) string {
	if len([]rune(text)) <= maxBriefingMilestoneContextChars {
		return text
	}
	runes := []rune(text)
	head := maxBriefingMilestoneContextChars / 2
	tail := maxBriefingMilestoneContextChars - head
	return string(runes[:head]) + "\n\n[...briefing milestone context truncated...]\n\n" + string(runes[len(runes)-tail:])
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

func removeAnyString(values []string, remove ...string) []string {
	removeSet := make(map[string]bool, len(remove))
	for _, value := range remove {
		removeSet[value] = true
	}
	filtered := values[:0]
	for _, value := range values {
		if !removeSet[value] {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func replaceOrderID(order []string, oldID string, replacement ...string) []string {
	var replaced []string
	inserted := false
	for _, id := range order {
		if id != oldID {
			replaced = append(replaced, id)
			continue
		}
		if inserted {
			continue
		}
		replaced = append(replaced, replacement...)
		inserted = true
	}
	return replaced
}

func activeBriefingIDs(briefings []config.Briefing) []string {
	var ids []string
	for _, briefing := range briefings {
		if briefing.Status != "archived" {
			ids = append(ids, briefing.ID)
		}
	}
	return ids
}

func readBriefingPartsFile(path string, stderr io.Writer) ([]briefingPartInput, bool) {
	if strings.TrimSpace(path) == "" {
		fmt.Fprintln(stderr, "Error: briefing split requires --parts-file <path>")
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "Error: reading split parts file: %v\n", err)
		return nil, false
	}
	var object briefingSplitPartFile
	if err := json.Unmarshal(data, &object); err == nil && len(object.Parts) > 0 {
		return object.Parts, true
	}
	if err := json.Unmarshal(data, &object); err == nil && object.Parts != nil {
		return object.Parts, true
	}
	var parts []briefingPartInput
	if err := json.Unmarshal(data, &parts); err != nil {
		fmt.Fprintf(stderr, "Error: parsing split parts file: %v\n", err)
		return nil, false
	}
	return parts, true
}

func mergedConstraints(briefings []config.Briefing) []string {
	var constraints []string
	for _, briefing := range briefings {
		for _, constraint := range briefing.Constraints {
			constraints = appendMissing(constraints, constraint)
		}
	}
	return constraints
}

func mergedDependencies(briefings []config.Briefing, mergedSet map[string]bool, targetID string) []string {
	var dependencies []string
	for _, briefing := range briefings {
		for _, dependencyID := range briefing.DependsOn {
			if dependencyID == targetID || mergedSet[dependencyID] {
				continue
			}
			dependencies = appendMissing(dependencies, dependencyID)
		}
	}
	return dependencies
}

func rewriteBriefingDependency(plan *config.Plan, sourceID, replacementID string, rewrittenIDs map[string]bool) {
	for index := range plan.Briefings {
		briefing := &plan.Briefings[index]
		if briefing.ID == replacementID || rewrittenIDs[briefing.ID] {
			continue
		}
		var dependencies []string
		for _, dependencyID := range briefing.DependsOn {
			if dependencyID == sourceID || rewrittenIDs[dependencyID] {
				dependencies = appendMissing(dependencies, replacementID)
				continue
			}
			dependencies = appendMissing(dependencies, dependencyID)
		}
		briefing.DependsOn = dependencies
	}
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

type milestoneRelationRef struct {
	PlanID     string
	BriefingID string
}

type milestoneRelationIndex map[string][]milestoneRelationRef

func buildMilestoneRelationIndex(state *config.PlanningState) milestoneRelationIndex {
	index := make(milestoneRelationIndex)
	if state == nil {
		return index
	}
	for _, plan := range state.Plans {
		for _, briefing := range plan.Briefings {
			if briefing.MilestoneID == "" || (briefing.Status != "active" && briefing.Status != "completed") {
				continue
			}
			index[briefing.MilestoneID] = append(index[briefing.MilestoneID], milestoneRelationRef{
				PlanID:     plan.ID,
				BriefingID: briefing.ID,
			})
		}
	}
	return index
}

func printPlan(w io.Writer, plan config.Plan, milestoneIDs map[string]bool, relations milestoneRelationIndex, exec *config.PlanExecution) {
	fmt.Fprintf(w, "Plan: %s\n", plan.ID)
	fmt.Fprintf(w, "Title: %s\n", plan.Title)
	fmt.Fprintf(w, "Status: %s\n", plan.Status)
	fmt.Fprintf(w, "Objective: %s\n", plan.Objective)
	fmt.Fprintf(w, "Created: %s by %s\n", plan.CreatedAt, plan.CreatedBy)
	fmt.Fprintf(w, "Updated: %s by %s\n", plan.UpdatedAt, plan.UpdatedBy)
	fmt.Fprintf(w, "Progress: %s\n", planProgress(plan).String())
	if exec != nil {
		fmt.Fprintf(w, "Execution: %s\n", exec.State)
		fmt.Fprintf(w, "Execution Mode: %s\n", exec.Mode)
		fmt.Fprintf(w, "Checkpoint: %s\n", exec.Checkpoint)
		if exec.CurrentBriefingID != "" {
			fmt.Fprintf(w, "Current Briefing: %s\n", exec.CurrentBriefingID)
		}
		if exec.CurrentMilestoneID != "" {
			fmt.Fprintf(w, "Current Milestone: %s\n", exec.CurrentMilestoneID)
		}
		if exec.PendingApproval != "" {
			fmt.Fprintf(w, "Pending Approval: %s\n", exec.PendingApproval)
		}
		if exec.StopReason != "" {
			fmt.Fprintf(w, "Stop Reason: %s\n", exec.StopReason)
		}
	}
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
			milestoneRelationship(plan.ID, briefing, milestoneIDs, relations),
			briefing.Title,
		)
	}
}

func printBriefing(w io.Writer, plan config.Plan, briefing config.Briefing, milestoneIDs map[string]bool, relations milestoneRelationIndex) {
	fmt.Fprintf(w, "Briefing: %s\n", briefing.ID)
	fmt.Fprintf(w, "Plan: %s\n", plan.ID)
	fmt.Fprintf(w, "Title: %s\n", briefing.Title)
	fmt.Fprintf(w, "Status: %s\n", briefing.Status)
	fmt.Fprintf(w, "Readiness: %s\n", briefingReadiness(plan, briefing))
	fmt.Fprintf(w, "Milestone: %s\n", milestoneRelationship(plan.ID, briefing, milestoneIDs, relations))
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

func milestoneRelationship(planID string, briefing config.Briefing, milestoneIDs map[string]bool, relations milestoneRelationIndex) string {
	if briefing.MilestoneID == "" {
		return "none"
	}
	if !milestoneIDs[briefing.MilestoneID] {
		return "missing " + briefing.MilestoneID
	}
	var others []string
	for _, ref := range relations[briefing.MilestoneID] {
		if ref.PlanID == planID && ref.BriefingID == briefing.ID {
			continue
		}
		others = append(others, fmt.Sprintf("Plan %s Briefing %s", ref.PlanID, ref.BriefingID))
	}
	sort.Strings(others)
	if len(others) == 0 {
		return "linked " + briefing.MilestoneID + " (standalone)"
	}
	return "linked " + briefing.MilestoneID + " (also linked by " + strings.Join(others, "; ") + ")"
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
