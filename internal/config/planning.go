package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)



const PlanningSchemaVersion = 1

// PlanningMetadataOnlyWarning is the explicit warning text clarifying that planning operations affect planning metadata only.
const PlanningMetadataOnlyWarning = "Planning operations affect planning metadata only. Linked milestone specs, compact index entries, runtime state, reports, and branch snapshots are never modified or deleted."

const (
	PlanExecutionModeOnce       = "once"
	PlanExecutionModeContinuous = "continuous"
	PlanExecutionModeReview     = "review"
)

var planningIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Plan is the persisted planning-layer record stored under .cyclestone/plans.
// Runtime execution state is stored in state.json via State.PlanExecutions,
// not in the Plan YAML.
type Plan struct {
	SchemaVersion int        `yaml:"schema_version" json:"schema_version"`
	ID            string     `yaml:"id" json:"id"`
	Title         string     `yaml:"title" json:"title"`
	Objective     string     `yaml:"objective" json:"objective"`
	Status        string     `yaml:"status" json:"status"`
	CreatedAt     string     `yaml:"created_at" json:"created_at"`
	CreatedBy     string     `yaml:"created_by" json:"created_by"`
	UpdatedAt     string     `yaml:"updated_at" json:"updated_at"`
	UpdatedBy     string     `yaml:"updated_by" json:"updated_by"`
	Constraints   []string   `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	BriefingOrder []string   `yaml:"briefing_order" json:"briefing_order"`
	Briefings     []Briefing `yaml:"briefings" json:"briefings"`
}

// PlanExecution is the optional durable coordinator state for an explicitly
// started Plan. Ordinary Milestones and repositories without Plans never use it.
type PlanExecution struct {
	Mode               string `yaml:"mode" json:"mode"`
	State              string `yaml:"state" json:"state"`
	Checkpoint         string `yaml:"checkpoint,omitempty" json:"checkpoint,omitempty"`
	CurrentBriefingID  string `yaml:"current_briefing_id,omitempty" json:"current_briefing_id,omitempty"`
	CurrentMilestoneID string `yaml:"current_milestone_id,omitempty" json:"current_milestone_id,omitempty"`
	PendingApproval    string `yaml:"pending_approval,omitempty" json:"pending_approval,omitempty"`
	StopReason         string `yaml:"stop_reason,omitempty" json:"stop_reason,omitempty"`
	UpdatedAt          string `yaml:"updated_at" json:"updated_at"`
}

// IsValidPlanExecutionMode reports whether mode is a supported queue behavior.
func IsValidPlanExecutionMode(mode string) bool {
	switch mode {
	case PlanExecutionModeOnce, PlanExecutionModeContinuous, PlanExecutionModeReview:
		return true
	}
	return false
}

// Briefing is a same-Plan planning item embedded inside a Plan file.
type Briefing struct {
	ID               string   `yaml:"id" json:"id"`
	Title            string   `yaml:"title" json:"title"`
	Objective        string   `yaml:"objective" json:"objective"`
	Intent           string   `yaml:"intent" json:"intent"`
	Status           string   `yaml:"status" json:"status"`
	CompletionSignal string   `yaml:"completion_signal" json:"completion_signal"`
	CreatedAt        string   `yaml:"created_at" json:"created_at"`
	CreatedBy        string   `yaml:"created_by" json:"created_by"`
	UpdatedAt        string   `yaml:"updated_at" json:"updated_at"`
	UpdatedBy        string   `yaml:"updated_by" json:"updated_by"`
	Constraints      []string `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	DependsOn        []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	MilestoneID      string   `yaml:"milestone_id,omitempty" json:"milestone_id,omitempty"`
}

// PlanningState contains all valid Plan files loaded from a plans directory.
type PlanningState struct {
	Plans []Plan `json:"plans"`
}

// PlanningValidationResult collects file-scoped planning validation findings.
type PlanningValidationResult struct {
	Messages             []PlanningValidationMessage   `json:"messages"`
	UnresolvedReferences []PlanningUnresolvedReference `json:"unresolved_references"`
}

// PlanningValidationMessage is an error or warning from planning persistence.
type PlanningValidationMessage struct {
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

// PlanningUnresolvedReference records a planning-layer reference that could not be resolved.
type PlanningUnresolvedReference struct {
	Kind        string `json:"kind"`
	File        string `json:"file,omitempty"`
	PlanID      string `json:"plan_id,omitempty"`
	BriefingID  string `json:"briefing_id,omitempty"`
	MilestoneID string `json:"milestone_id,omitempty"`
	Message     string `json:"message"`
}

type planningValidationOptions struct {
	knownMilestoneIDs map[string]bool
	milestoneSources  []MilestoneSourceReference
}

// PlanningValidationOption adds optional cross-layer context to planning validation.
type PlanningValidationOption func(*planningValidationOptions)

// WithKnownMilestoneIDs lets planning validation warn about dangling optional milestone references.
func WithKnownMilestoneIDs(ids []string) PlanningValidationOption {
	return func(opts *planningValidationOptions) {
		opts.knownMilestoneIDs = make(map[string]bool, len(ids))
		for _, id := range ids {
			opts.knownMilestoneIDs[id] = true
		}
	}
}

// MilestoneSourceReference is advisory provenance from a milestone back to a Plan Briefing.
type MilestoneSourceReference struct {
	MilestoneID string
	Type        string
	PlanID      string
	BriefingID  string
}

// WithMilestoneSourceReferences lets planning validation warn about dangling optional provenance.
func WithMilestoneSourceReferences(refs []MilestoneSourceReference) PlanningValidationOption {
	return func(opts *planningValidationOptions) {
		opts.milestoneSources = append([]MilestoneSourceReference(nil), refs...)
	}
}

// LoadPlanningState loads every valid *.yml Plan from plansDir. Missing or empty directories are valid.
func LoadPlanningState(plansDir string, options ...PlanningValidationOption) (*PlanningState, PlanningValidationResult) {
	opts := collectPlanningOptions(options)
	var result PlanningValidationResult
	state := &PlanningState{Plans: []Plan{}}

	entries, err := os.ReadDir(plansDir)
	if err != nil {
		if os.IsNotExist(err) {
			return state, result
		}
		result.addError(plansDir, "", fmt.Sprintf("failed to read plans directory: %v", err))
		return state, result
	}

	seenPlanFiles := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			planDir := filepath.Join(plansDir, entry.Name())
			plan, valid := loadPlanningDir(planDir, &result, opts)
			if valid && plan.ID != "" {
				if firstFile, exists := seenPlanFiles[plan.ID]; exists {
					result.addError(planDir, "id", fmt.Sprintf("duplicate Plan ID %q also appears in %s", plan.ID, firstFile))
					continue
				}
				seenPlanFiles[plan.ID] = planDir
				state.Plans = append(state.Plans, plan)
			}
			continue
		}
		if filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		file := filepath.Join(plansDir, entry.Name())
		plan, valid := loadPlanningFile(file, &result, opts)
		if !valid {
			continue
		}
		if firstFile, exists := seenPlanFiles[plan.ID]; exists {
			result.addError(file, "id", fmt.Sprintf("duplicate Plan ID %q also appears in %s", plan.ID, firstFile))
			continue
		}
		seenPlanFiles[plan.ID] = file
		state.Plans = append(state.Plans, plan)
	}

	validateMilestoneSourceReferences(state, opts, &result)
	return state, result
}

// SavePlan validates and atomically writes plan to plansDir/<plan-id>.yml.
func SavePlan(plansDir string, plan Plan, options ...PlanningValidationOption) (PlanningValidationResult, error) {
	result := ValidatePlan(plan, filepath.Join(plansDir, plan.ID+".yml"), options...)
	if result.HasErrors() {
		return result, errors.New("plan validation failed")
	}
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		return result, fmt.Errorf("failed to create plans directory: %w", err)
	}
	data, err := yaml.Marshal(plan)
	if err != nil {
		return result, fmt.Errorf("failed to marshal plan: %w", err)
	}
	if err := atomicWritePlanningFile(filepath.Join(plansDir, plan.ID+".yml"), data, 0644); err != nil {
		return result, err
	}
	return result, nil
}

// SavePlanToFolder validates and writes plan to a folder-per-item layout under plansDir.
func SavePlanToFolder(plansDir string, plan Plan, options ...PlanningValidationOption) (string, PlanningValidationResult, error) {
	// Use the plan ID as the stable directory name so title edits do not
	// create orphan directories.
	planDir := filepath.Join(plansDir, plan.ID)
	if err := os.MkdirAll(planDir, 0755); err != nil {
		var emptyRes PlanningValidationResult
		return "", emptyRes, fmt.Errorf("failed to create plan directory: %w", err)
	}

	// Remove legacy flat .yml file if it exists to avoid duplicate Plan ID errors.
	legacyFlatYML := filepath.Join(plansDir, plan.ID+".yml")
	if _, err := os.Stat(legacyFlatYML); err == nil {
		_ = os.Remove(legacyFlatYML)
	}

	metaPath := filepath.Join(planDir, plan.ID+".yml")
	specPath := filepath.Join(planDir, plan.ID+".md")

	result := ValidatePlan(plan, metaPath, options...)
	if result.HasErrors() {
		return planDir, result, errors.New("plan validation failed")
	}

	data, err := yaml.Marshal(plan)
	if err != nil {
		return planDir, result, fmt.Errorf("failed to marshal plan: %w", err)
	}
	if err := atomicWritePlanningFile(metaPath, data, 0644); err != nil {
		return planDir, result, err
	}
	if err := os.WriteFile(specPath, []byte(plan.Objective), 0644); err != nil {
		return planDir, result, err
	}

	// Save individual briefings into briefings/ subfolder and clean up stale entries.
	briefingsDir := filepath.Join(planDir, "briefings")
	activeBriefingIDs := make(map[string]bool, len(plan.Briefings))
	for _, b := range plan.Briefings {
		activeBriefingIDs[b.ID] = true
		bDir := filepath.Join(briefingsDir, b.ID)
		_ = os.MkdirAll(bDir, 0755)
		bData, err := yaml.Marshal(b)
		if err == nil {
			_ = os.WriteFile(filepath.Join(bDir, b.ID+".yml"), bData, 0644)
		}
		if strings.TrimSpace(b.Objective) != "" {
			_ = os.WriteFile(filepath.Join(bDir, b.ID+".md"), []byte(b.Objective), 0644)
		}
	}
	// Remove stale briefing subdirectories for briefings no longer in the Plan.
	if entries, err := os.ReadDir(briefingsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && !activeBriefingIDs[entry.Name()] {
				_ = os.RemoveAll(filepath.Join(briefingsDir, entry.Name()))
			}
		}
	}

	return planDir, result, nil
}

func loadPlanningDir(planDir string, result *PlanningValidationResult, opts planningValidationOptions) (Plan, bool) {
	entries, err := os.ReadDir(planDir)
	if err != nil {
		return Plan{}, false
	}

	var planMetaPath, planSpecPath string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			planMetaPath = filepath.Join(planDir, name)
		} else if strings.HasSuffix(name, ".md") {
			planSpecPath = filepath.Join(planDir, name)
		}
	}

	if planMetaPath == "" {
		return Plan{}, false
	}

	plan, valid := loadPlanningFile(planMetaPath, result, opts)
	if !valid {
		return Plan{}, false
	}

	if planSpecPath != "" {
		if specBytes, err := os.ReadFile(planSpecPath); err == nil && len(specBytes) > 0 {
			plan.Objective = string(specBytes)
		}
	}

	// Read briefings from briefings/ subfolder if present
	briefingsDir := filepath.Join(planDir, "briefings")
	if briefingEntries, err := os.ReadDir(briefingsDir); err == nil {
		var dirBriefings []Briefing
		for _, bEntry := range briefingEntries {
			if !bEntry.IsDir() {
				continue
			}
			bDir := filepath.Join(briefingsDir, bEntry.Name())
			bEntries, err := os.ReadDir(bDir)
			if err != nil {
				continue
			}
			var bMetaPath, bSpecPath string
			for _, be := range bEntries {
				if be.IsDir() {
					continue
				}
				if strings.HasSuffix(be.Name(), ".yml") || strings.HasSuffix(be.Name(), ".yaml") {
					bMetaPath = filepath.Join(bDir, be.Name())
				} else if strings.HasSuffix(be.Name(), ".md") {
					bSpecPath = filepath.Join(bDir, be.Name())
				}
			}
			if bMetaPath != "" {
				bData, err := os.ReadFile(bMetaPath)
				if err == nil {
					var b Briefing
					if err := yaml.Unmarshal(bData, &b); err == nil && b.ID != "" {
						if bSpecPath != "" {
							if bSpecBytes, err := os.ReadFile(bSpecPath); err == nil && len(bSpecBytes) > 0 {
								b.Objective = string(bSpecBytes)
							}
						}
						dirBriefings = append(dirBriefings, b)
					}
				}
			}
		}
		if len(dirBriefings) > 0 {
			// Track which briefing IDs were in the inline Plan YAML so we
			// don't re-add archived briefings back into BriefingOrder.
			inlineIDs := make(map[string]bool, len(plan.Briefings))
			for _, b := range plan.Briefings {
				inlineIDs[b.ID] = true
			}
			bMap := make(map[string]Briefing)
			for _, b := range plan.Briefings {
				bMap[b.ID] = b
			}
			for _, db := range dirBriefings {
				bMap[db.ID] = db
			}
			var merged []Briefing
			for _, id := range plan.BriefingOrder {
				if b, ok := bMap[id]; ok {
					merged = append(merged, b)
					delete(bMap, id)
				}
			}
			// Only add directory-only briefings that are truly new (not in the
			// inline Plan YAML). Briefings that were in the inline YAML but
			// removed from BriefingOrder (e.g. archived) stay in Briefings but
			// are not re-added to BriefingOrder.
			for _, b := range dirBriefings {
				if _, remaining := bMap[b.ID]; remaining && !inlineIDs[b.ID] {
					merged = append(merged, b)
					plan.BriefingOrder = append(plan.BriefingOrder, b.ID)
					delete(bMap, b.ID)
				}
			}
			// Preserve any inline-only briefings not in directory and not in order.
			for _, b := range plan.Briefings {
				if _, remaining := bMap[b.ID]; remaining {
					merged = append(merged, b)
					delete(bMap, b.ID)
				}
			}
			plan.Briefings = merged
		}
	}

	return plan, true
}

// DeletePlan removes exactly one persisted Plan record. Planning IDs are
// validated before constructing the path so callers cannot escape plansDir.
func DeletePlan(plansDir, planID string) error {
	var result PlanningValidationResult
	validatePlanningID(planID, "", "id", "Plan ID", &result)
	if result.HasErrors() {
		return errors.New("invalid Plan ID")
	}
	// Try deleting folder if it exists
	entries, err := os.ReadDir(plansDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), planID) {
				return os.RemoveAll(filepath.Join(plansDir, entry.Name()))
			}
		}
	}
	path := filepath.Join(plansDir, planID+".yml")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to delete Plan %q: %w", planID, err)
	}
	return nil
}

// ValidatePlan validates one in-memory Plan without writing it.
func ValidatePlan(plan Plan, file string, options ...PlanningValidationOption) PlanningValidationResult {
	opts := collectPlanningOptions(options)
	var result PlanningValidationResult
	validatePlan(plan, file, &result, opts)
	return result
}

// HasErrors reports whether any validation message has error severity.
func (r PlanningValidationResult) HasErrors() bool {
	for _, msg := range r.Messages {
		if msg.Severity == "error" {
			return true
		}
	}
	return false
}

// HasWarnings reports whether any validation message has warning severity.
func (r PlanningValidationResult) HasWarnings() bool {
	for _, msg := range r.Messages {
		if msg.Severity == "warning" {
			return true
		}
	}
	return false
}

func collectPlanningOptions(options []PlanningValidationOption) planningValidationOptions {
	var opts planningValidationOptions
	for _, option := range options {
		option(&opts)
	}
	return opts
}

func loadPlanningFile(file string, result *PlanningValidationResult, opts planningValidationOptions) (Plan, bool) {
	data, err := os.ReadFile(file)
	if err != nil {
		result.addError(file, "", fmt.Sprintf("failed to read Plan file: %v", err))
		return Plan{}, false
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		result.addError(file, "", fmt.Sprintf("malformed YAML: %v", err))
		return Plan{}, false
	}
	warnUnknownPlanningFields(file, &node, result)

	var plan Plan
	if err := node.Decode(&plan); err != nil {
		result.addError(file, "", fmt.Sprintf("failed to decode Plan: %v", err))
		return Plan{}, false
	}

	before := len(result.Messages)
	validatePlan(plan, file, result, opts)
	if result.hasNewErrorsSince(before) {
		return Plan{}, false
	}
	return plan, true
}

func validatePlan(plan Plan, file string, result *PlanningValidationResult, opts planningValidationOptions) {
	if plan.SchemaVersion != PlanningSchemaVersion {
		result.addError(file, "schema_version", fmt.Sprintf("schema_version must be %d", PlanningSchemaVersion))
	}
	validatePlanningID(plan.ID, file, "id", "Plan ID", result)
	requiredString(plan.Title, file, "title", result)
	requiredString(plan.Objective, file, "objective", result)
	validatePlanningStatus(plan.Status, file, "status", result)
	validatePlanningTimestamp(plan.CreatedAt, file, "created_at", result)
	requiredString(plan.CreatedBy, file, "created_by", result)
	validatePlanningTimestamp(plan.UpdatedAt, file, "updated_at", result)
	requiredString(plan.UpdatedBy, file, "updated_by", result)
	validateTimestampOrder(plan.CreatedAt, plan.UpdatedAt, file, result)

	briefingsByID := make(map[string]Briefing)
	for _, briefing := range plan.Briefings {
		if _, exists := briefingsByID[briefing.ID]; exists {
			result.addError(file, "briefings", fmt.Sprintf("duplicate Briefing ID %q", briefing.ID))
			continue
		}
		briefingsByID[briefing.ID] = briefing
		validateBriefing(plan, briefing, file, result, opts)
	}
	validateBriefingOrder(plan, briefingsByID, file, result)
	validateBriefingDependencies(plan, briefingsByID, file, result)
	validatePlanExecution(plan, briefingsByID, file, result)
}

// validatePlanExecution is retained for compatibility but no longer reads
// plan.Execution (which has been removed). Plan execution state is validated
// centrally through State.PlanExecutions at runtime, not during Plan file
// validation.
func validatePlanExecution(plan Plan, briefingsByID map[string]Briefing, file string, result *PlanningValidationResult) {
	// No-op: execution state lives in state.json, not in the Plan YAML.
}

func validateBriefing(plan Plan, briefing Briefing, file string, result *PlanningValidationResult, opts planningValidationOptions) {
	prefix := "briefings." + briefing.ID
	validatePlanningID(briefing.ID, file, prefix+".id", "Briefing ID", result)
	requiredString(briefing.Title, file, prefix+".title", result)
	requiredString(briefing.Objective, file, prefix+".objective", result)
	requiredString(briefing.Intent, file, prefix+".intent", result)
	validatePlanningStatus(briefing.Status, file, prefix+".status", result)
	requiredString(briefing.CompletionSignal, file, prefix+".completion_signal", result)
	validatePlanningTimestamp(briefing.CreatedAt, file, prefix+".created_at", result)
	requiredString(briefing.CreatedBy, file, prefix+".created_by", result)
	validatePlanningTimestamp(briefing.UpdatedAt, file, prefix+".updated_at", result)
	requiredString(briefing.UpdatedBy, file, prefix+".updated_by", result)
	validateTimestampOrder(briefing.CreatedAt, briefing.UpdatedAt, file, result)

	if briefing.MilestoneID != "" && !planningIDPattern.MatchString(briefing.MilestoneID) {
		result.addError(file, prefix+".milestone_id", "Milestone ID must use lowercase ASCII letters, numbers, and hyphens")
		return
	}
	if briefing.MilestoneID != "" && opts.knownMilestoneIDs != nil && !opts.knownMilestoneIDs[briefing.MilestoneID] {
		msg := fmt.Sprintf("Briefing %q references missing Milestone %q", briefing.ID, briefing.MilestoneID)
		result.addWarning(file, prefix+".milestone_id", msg)
		result.UnresolvedReferences = append(result.UnresolvedReferences, PlanningUnresolvedReference{
			Kind:        "milestone",
			File:        file,
			PlanID:      plan.ID,
			BriefingID:  briefing.ID,
			MilestoneID: briefing.MilestoneID,
			Message:     msg,
		})
	}
}

func validateBriefingOrder(plan Plan, briefingsByID map[string]Briefing, file string, result *PlanningValidationResult) {
	ordered := make(map[string]int)
	for _, id := range plan.BriefingOrder {
		if _, exists := ordered[id]; exists {
			result.addError(file, "briefing_order", fmt.Sprintf("duplicate Briefing ID %q in briefing_order", id))
		}
		ordered[id]++
		if _, exists := briefingsByID[id]; !exists {
			result.addError(file, "briefing_order", fmt.Sprintf("briefing_order references missing Briefing %q", id))
		}
	}
	for id, briefing := range briefingsByID {
		if briefing.Status != "archived" && ordered[id] == 0 {
			result.addError(file, "briefing_order", fmt.Sprintf("non-archived Briefing %q is missing from briefing_order", id))
		}
	}
}

func validateBriefingDependencies(plan Plan, briefingsByID map[string]Briefing, file string, result *PlanningValidationResult) {
	graph := make(map[string][]string)
	for _, briefing := range plan.Briefings {
		for _, dependencyID := range briefing.DependsOn {
			dependency, exists := briefingsByID[dependencyID]
			if !exists {
				msg := fmt.Sprintf("Briefing %q depends on missing Briefing %q", briefing.ID, dependencyID)
				if briefing.Status == "archived" {
					result.addWarning(file, "briefings."+briefing.ID+".depends_on", msg)
				} else {
					result.addError(file, "briefings."+briefing.ID+".depends_on", msg)
				}
				continue
			}
			graph[briefing.ID] = append(graph[briefing.ID], dependencyID)
			if briefing.Status != "archived" && dependency.Status == "archived" {
				result.addWarning(file, "briefings."+briefing.ID+".depends_on", fmt.Sprintf("Briefing %q depends on archived Briefing %q", briefing.ID, dependencyID))
			}
		}
	}
	if hasPlanningDependencyCycle(graph) {
		result.addError(file, "briefings.depends_on", fmt.Sprintf("Plan %q contains a dependency cycle", plan.ID))
	}
}

func validateMilestoneSourceReferences(state *PlanningState, opts planningValidationOptions, result *PlanningValidationResult) {
	if len(opts.milestoneSources) == 0 {
		return
	}
	plansByID := make(map[string]Plan)
	briefingsByPlan := make(map[string]map[string]bool)
	for _, plan := range state.Plans {
		plansByID[plan.ID] = plan
		briefingsByPlan[plan.ID] = make(map[string]bool)
		for _, briefing := range plan.Briefings {
			briefingsByPlan[plan.ID][briefing.ID] = true
		}
	}
	for _, ref := range opts.milestoneSources {
		if ref.Type != "" && ref.Type != "briefing" {
			result.addWarning("", "source.type", fmt.Sprintf("Milestone %q has unsupported planning source type %q", ref.MilestoneID, ref.Type))
			continue
		}
		if _, exists := plansByID[ref.PlanID]; !exists {
			msg := fmt.Sprintf("Milestone %q references missing source Plan %q", ref.MilestoneID, ref.PlanID)
			result.addWarning("", "source.plan_id", msg)
			result.UnresolvedReferences = append(result.UnresolvedReferences, PlanningUnresolvedReference{
				Kind:        "plan",
				PlanID:      ref.PlanID,
				BriefingID:  ref.BriefingID,
				MilestoneID: ref.MilestoneID,
				Message:     msg,
			})
			continue
		}
		if ref.BriefingID != "" && !briefingsByPlan[ref.PlanID][ref.BriefingID] {
			msg := fmt.Sprintf("Milestone %q references missing source Briefing %q in Plan %q", ref.MilestoneID, ref.BriefingID, ref.PlanID)
			result.addWarning("", "source.briefing_id", msg)
			result.UnresolvedReferences = append(result.UnresolvedReferences, PlanningUnresolvedReference{
				Kind:        "briefing",
				PlanID:      ref.PlanID,
				BriefingID:  ref.BriefingID,
				MilestoneID: ref.MilestoneID,
				Message:     msg,
			})
		}
	}
}

func validatePlanningID(id, file, field, label string, result *PlanningValidationResult) {
	if strings.TrimSpace(id) == "" {
		result.addError(file, field, label+" is required")
		return
	}
	if !planningIDPattern.MatchString(id) {
		result.addError(file, field, label+" must use lowercase ASCII letters, numbers, and hyphens")
	}
}

func validatePlanningStatus(status, file, field string, result *PlanningValidationResult) {
	switch status {
	case "active", "completed", "archived":
	case "":
		result.addError(file, field, field+" is required")
	default:
		result.addError(file, field, fmt.Sprintf("invalid status %q", status))
	}
}

func validatePlanningTimestamp(value, file, field string, result *PlanningValidationResult) {
	if strings.TrimSpace(value) == "" {
		result.addError(file, field, field+" is required")
		return
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		result.addError(file, field, fmt.Sprintf("%s must be RFC3339: %v", field, err))
	}
}

func validateTimestampOrder(createdAt, updatedAt, file string, result *PlanningValidationResult) {
	created, createdErr := time.Parse(time.RFC3339, createdAt)
	updated, updatedErr := time.Parse(time.RFC3339, updatedAt)
	if createdErr == nil && updatedErr == nil && updated.Before(created) {
		result.addError(file, "updated_at", "updated_at must be equal to or later than created_at")
	}
}

func requiredString(value, file, field string, result *PlanningValidationResult) {
	if strings.TrimSpace(value) == "" {
		result.addError(file, field, field+" is required")
	}
}

func hasPlanningDependencyCycle(graph map[string][]string) bool {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int)
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case visiting:
			return true
		case visited:
			return false
		}
		state[id] = visiting
		for _, dep := range graph[id] {
			if visit(dep) {
				return true
			}
		}
		state[id] = visited
		return false
	}
	for id := range graph {
		if visit(id) {
			return true
		}
	}
	return false
}

func warnUnknownPlanningFields(file string, root *yaml.Node, result *PlanningValidationResult) {
	if root == nil || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return
	}
	planFields := map[string]bool{
		"schema_version": true,
		"id":             true,
		"title":          true,
		"objective":      true,
		"status":         true,
		"created_at":     true,
		"created_by":     true,
		"updated_at":     true,
		"updated_by":     true,
		"constraints":    true,
		"briefing_order": true,
		"briefings":      true,
		"execution":      true,
	}
	briefingFields := map[string]bool{
		"id":                true,
		"title":             true,
		"objective":         true,
		"intent":            true,
		"status":            true,
		"completion_signal": true,
		"created_at":        true,
		"created_by":        true,
		"updated_at":        true,
		"updated_by":        true,
		"constraints":       true,
		"depends_on":        true,
		"milestone_id":      true,
	}
	executionFields := map[string]bool{
		"mode": true, "state": true, "checkpoint": true,
		"current_briefing_id": true, "current_milestone_id": true,
		"pending_approval": true, "stop_reason": true, "updated_at": true,
	}
	planNode := root.Content[0]
	for i := 0; i+1 < len(planNode.Content); i += 2 {
		key := planNode.Content[i].Value
		value := planNode.Content[i+1]
		if !planFields[key] {
			result.addWarning(file, key, fmt.Sprintf("unknown Plan field %q", key))
			continue
		}
		switch key {
		case "briefings":
			if value.Kind != yaml.SequenceNode {
				continue
			}
			for _, briefingNode := range value.Content {
				if briefingNode.Kind != yaml.MappingNode {
					continue
				}
				for j := 0; j+1 < len(briefingNode.Content); j += 2 {
					briefingKey := briefingNode.Content[j].Value
					if !briefingFields[briefingKey] {
						result.addWarning(file, "briefings."+briefingKey, fmt.Sprintf("unknown Briefing field %q", briefingKey))
					}
				}
			}
		case "execution":
			if value.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(value.Content); j += 2 {
				executionKey := value.Content[j].Value
				if !executionFields[executionKey] {
					result.addWarning(file, "execution."+executionKey, fmt.Sprintf("unknown Plan execution field %q", executionKey))
				}
			}
		}
	}
}

func atomicWritePlanningFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary Plan file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temporary Plan file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set temporary Plan file permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temporary Plan file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace Plan file: %w", err)
	}
	cleanup = false
	return nil
}

func (r *PlanningValidationResult) addError(file, field, message string) {
	r.Messages = append(r.Messages, PlanningValidationMessage{
		Severity: "error",
		File:     file,
		Field:    field,
		Message:  message,
	})
}

func (r *PlanningValidationResult) addWarning(file, field, message string) {
	r.Messages = append(r.Messages, PlanningValidationMessage{
		Severity: "warning",
		File:     file,
		Field:    field,
		Message:  message,
	})
}

func (r PlanningValidationResult) hasNewErrorsSince(index int) bool {
	for _, msg := range r.Messages[index:] {
		if msg.Severity == "error" {
			return true
		}
	}
	return false
}

// PlanReevaluationProposal represents a structured AI Planner re-evaluation proposal.
type PlanReevaluationProposal struct {
	PlanID        string     `yaml:"plan_id" json:"plan_id"`
	Rationale     string     `yaml:"rationale" json:"rationale"`
	BriefingOrder []string   `yaml:"briefing_order" json:"briefing_order"`
	Briefings     []Briefing `yaml:"briefings" json:"briefings"`
}

// BriefingDiffKind indicates the nature of change to a Briefing.
type BriefingDiffKind string

const (
	DiffKindAdded     BriefingDiffKind = "added"
	DiffKindRemoved   BriefingDiffKind = "removed"
	DiffKindModified  BriefingDiffKind = "modified"
	DiffKindReordered BriefingDiffKind = "reordered"
	DiffKindBlocked   BriefingDiffKind = "blocked"
	DiffKindSplit     BriefingDiffKind = "split"
	DiffKindMerge     BriefingDiffKind = "merge"
	DiffKindUnchanged BriefingDiffKind = "unchanged"
)

// FieldChange records a property edit.
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// BriefingDiff details changes to an individual Briefing.
type BriefingDiff struct {
	Kind            BriefingDiffKind `json:"kind"`
	BriefingID      string           `json:"briefing_id"`
	Title           string           `json:"title"`
	OldBriefing     *Briefing        `json:"old_briefing,omitempty"`
	NewBriefing     *Briefing        `json:"new_briefing,omitempty"`
	FieldChanges    []FieldChange    `json:"field_changes,omitempty"`
	MilestoneLink   string           `json:"milestone_link,omitempty"`
	IsLinkSuggested bool             `json:"is_link_suggested,omitempty"`
	Notes           string           `json:"notes,omitempty"`
}

// PlanDiff collects structured entity-level modifications between old and proposed Plans.
type PlanDiff struct {
	PlanID        string         `json:"plan_id"`
	Rationale     string         `json:"rationale,omitempty"`
	BriefingDiffs []BriefingDiff `json:"briefing_diffs"`
	Warnings      []string       `json:"warnings,omitempty"`
	HasChanges    bool           `json:"has_changes"`
}

// ApplyPlanReevaluationProposal constructs an updated Plan from oldPlan and proposal.
func ApplyPlanReevaluationProposal(oldPlan Plan, proposal PlanReevaluationProposal, actor, timestamp string) Plan {
	newPlan := oldPlan
	newPlan.UpdatedAt = timestamp
	if actor != "" {
		newPlan.UpdatedBy = actor
	}
	newPlan.BriefingOrder = append([]string(nil), proposal.BriefingOrder...)
	newPlan.Briefings = append([]Briefing(nil), proposal.Briefings...)
	return newPlan
}

// ValidatePlanReevaluationProposal validates that proposal adheres to replanning safety invariants.
func ValidatePlanReevaluationProposal(oldPlan Plan, proposal PlanReevaluationProposal, knownMilestoneIDs []string) PlanningValidationResult {
	var result PlanningValidationResult

	if proposal.PlanID != "" && proposal.PlanID != oldPlan.ID {
		result.addError("", "plan_id", fmt.Sprintf("proposal Plan ID %q does not match target Plan ID %q", proposal.PlanID, oldPlan.ID))
		return result
	}

	oldBriefingsByID := make(map[string]Briefing)
	for _, b := range oldPlan.Briefings {
		oldBriefingsByID[b.ID] = b
	}

	knownMilestones := make(map[string]bool)
	for _, id := range knownMilestoneIDs {
		knownMilestones[id] = true
	}

	// Validate invariant: completed briefings in oldPlan must retain completed status and milestone_id
	newBriefingsByID := make(map[string]Briefing)
	for _, b := range proposal.Briefings {
		newBriefingsByID[b.ID] = b
		if old, exists := oldBriefingsByID[b.ID]; exists {
			if old.Status == "completed" && b.Status != "completed" {
				result.addError("", "briefings."+b.ID+".status", fmt.Sprintf("cannot revert completed Briefing %q to %q", b.ID, b.Status))
			}
			if old.MilestoneID != "" && b.MilestoneID != "" && b.MilestoneID != old.MilestoneID {
				result.addError("", "briefings."+b.ID+".milestone_id", fmt.Sprintf("cannot change Milestone ID on Briefing %q from %q to %q", b.ID, old.MilestoneID, b.MilestoneID))
			}
		} else {
			// New briefing in proposal with milestone link must be flagged or checked
			if b.MilestoneID != "" {
				if !knownMilestones[b.MilestoneID] {
					result.addError("", "briefings."+b.ID+".milestone_id", fmt.Sprintf("proposed link on Briefing %q references non-existent Milestone %q", b.ID, b.MilestoneID))
				} else {
					result.addWarning("", "briefings."+b.ID+".milestone_id", fmt.Sprintf("proposed Milestone link %q on new Briefing %q requires explicit user approval", b.MilestoneID, b.ID))
				}
			}
		}
	}

	// Check if old briefings with milestone link were removed/merged - warn that milestones remain intact on disk
	for _, old := range oldPlan.Briefings {
		if _, exists := newBriefingsByID[old.ID]; !exists {
			if old.MilestoneID != "" {
				result.addWarning("", "briefings."+old.ID, fmt.Sprintf("removing Briefing %q preserves linked Milestone %q and execution history intact on disk", old.ID, old.MilestoneID))
			}
		}
	}

	// Construct candidate plan and run full plan validation
	candidate := ApplyPlanReevaluationProposal(oldPlan, proposal, "ai-planner", oldPlan.UpdatedAt)
	candidateValidation := ValidatePlan(candidate, "", WithKnownMilestoneIDs(knownMilestoneIDs))
	for _, msg := range candidateValidation.Messages {
		result.Messages = append(result.Messages, msg)
	}

	return result
}

// ComputePlanDiff calculates entity-level differences between oldPlan and proposal.
func ComputePlanDiff(oldPlan Plan, proposal PlanReevaluationProposal) PlanDiff {
	diff := PlanDiff{
		PlanID:    oldPlan.ID,
		Rationale: proposal.Rationale,
	}

	oldByID := make(map[string]Briefing)
	oldOrderIndex := make(map[string]int)
	for idx, id := range oldPlan.BriefingOrder {
		oldOrderIndex[id] = idx
	}
	for _, b := range oldPlan.Briefings {
		oldByID[b.ID] = b
	}

	newByID := make(map[string]Briefing)
	newOrderIndex := make(map[string]int)
	for idx, id := range proposal.BriefingOrder {
		newOrderIndex[id] = idx
	}
	for _, b := range proposal.Briefings {
		newByID[b.ID] = b
	}

	// Process existing & removed briefings
	for _, oldB := range oldPlan.Briefings {
		newB, exists := newByID[oldB.ID]
		if !exists {
			// Briefing was removed
			diff.BriefingDiffs = append(diff.BriefingDiffs, BriefingDiff{
				Kind:          DiffKindRemoved,
				BriefingID:    oldB.ID,
				Title:         oldB.Title,
				OldBriefing:   &oldB,
				MilestoneLink: oldB.MilestoneID,
				Notes:         "Briefing removed from plan; linked milestones remain intact on disk",
			})
			diff.HasChanges = true
			continue
		}

		// Briefing exists in both - check field changes
		var fieldChanges []FieldChange
		if oldB.Title != newB.Title {
			fieldChanges = append(fieldChanges, FieldChange{Field: "title", Old: oldB.Title, New: newB.Title})
		}
		if oldB.Objective != newB.Objective {
			fieldChanges = append(fieldChanges, FieldChange{Field: "objective", Old: oldB.Objective, New: newB.Objective})
		}
		if oldB.Intent != newB.Intent {
			fieldChanges = append(fieldChanges, FieldChange{Field: "intent", Old: oldB.Intent, New: newB.Intent})
		}
		if oldB.Status != newB.Status {
			fieldChanges = append(fieldChanges, FieldChange{Field: "status", Old: oldB.Status, New: newB.Status})
		}
		if oldB.CompletionSignal != newB.CompletionSignal {
			fieldChanges = append(fieldChanges, FieldChange{Field: "completion_signal", Old: oldB.CompletionSignal, New: newB.CompletionSignal})
		}
		if !stringSlicesEqual(oldB.Constraints, newB.Constraints) {
			fieldChanges = append(fieldChanges, FieldChange{Field: "constraints", Old: fmt.Sprintf("%v", oldB.Constraints), New: fmt.Sprintf("%v", newB.Constraints)})
		}
		if !stringSlicesEqual(oldB.DependsOn, newB.DependsOn) {
			fieldChanges = append(fieldChanges, FieldChange{Field: "depends_on", Old: fmt.Sprintf("%v", oldB.DependsOn), New: fmt.Sprintf("%v", newB.DependsOn)})
		}

		oldPos := oldOrderIndex[oldB.ID]
		newPos := newOrderIndex[newB.ID]
		reordered := oldPos != newPos

		linkSuggested := oldB.MilestoneID == "" && newB.MilestoneID != ""

		kind := DiffKindUnchanged
		if newB.Status == "blocked" && oldB.Status != "blocked" {
			kind = DiffKindBlocked
		} else if len(fieldChanges) > 0 {
			kind = DiffKindModified
		} else if reordered {
			kind = DiffKindReordered
		}

		if kind != DiffKindUnchanged || linkSuggested {
			diff.BriefingDiffs = append(diff.BriefingDiffs, BriefingDiff{
				Kind:            kind,
				BriefingID:      newB.ID,
				Title:           newB.Title,
				OldBriefing:     &oldB,
				NewBriefing:     &newB,
				FieldChanges:    fieldChanges,
				MilestoneLink:   newB.MilestoneID,
				IsLinkSuggested: linkSuggested,
			})
			diff.HasChanges = true
		}
	}

	// Process newly added briefings
	for _, newB := range proposal.Briefings {
		if _, exists := oldByID[newB.ID]; !exists {
			linkSuggested := newB.MilestoneID != ""
			diff.BriefingDiffs = append(diff.BriefingDiffs, BriefingDiff{
				Kind:            DiffKindAdded,
				BriefingID:      newB.ID,
				Title:           newB.Title,
				NewBriefing:     &newB,
				MilestoneLink:   newB.MilestoneID,
				IsLinkSuggested: linkSuggested,
			})
			diff.HasChanges = true
		}
	}

	return diff
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GeneratedPlanResponse is the structured schema produced by AI Plan generation.
type GeneratedPlanResponse struct {
	Title       string                      `json:"title" yaml:"title"`
	Objective   string                      `json:"objective" yaml:"objective"`
	Constraints []string                    `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	Briefings   []GeneratedBriefingResponse `json:"briefings" yaml:"briefings"`
}

// GeneratedBriefingResponse is a Briefing item inside an AI-generated Plan proposal.
type GeneratedBriefingResponse struct {
	Title            string   `json:"title" yaml:"title"`
	Objective        string   `json:"objective" yaml:"objective"`
	Intent           string   `json:"intent" yaml:"intent"`
	CompletionSignal string   `json:"completion_signal" yaml:"completion_signal"`
	Constraints      []string `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	DependsOn        []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	MilestoneID      string   `json:"milestone_id,omitempty" yaml:"milestone_id,omitempty"`
}

// ParseGeneratedPlanResponse parses a raw JSON or YAML response into a GeneratedPlanResponse struct.
func ParseGeneratedPlanResponse(text string) (GeneratedPlanResponse, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return GeneratedPlanResponse{}, fmt.Errorf("response is empty")
	}
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 3 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			text = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		var response GeneratedPlanResponse
		decoder := json.NewDecoder(strings.NewReader(text[start : end+1]))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&response); err == nil {
			return response, nil
		}
	}
	var response GeneratedPlanResponse
	if err := yaml.Unmarshal([]byte(text), &response); err == nil && (response.Title != "" || response.Objective != "" || len(response.Briefings) > 0) {
		return response, nil
	}
	return GeneratedPlanResponse{}, fmt.Errorf("response must contain one JSON object")
}

// ConvertGeneratedPlan converts a GeneratedPlanResponse into a valid Plan model.
func ConvertGeneratedPlan(goal string, response GeneratedPlanResponse, actor, now string) (Plan, error) {
	if actor == "" {
		actor = "ai-plan-generator"
	}
	title := strings.TrimSpace(response.Title)
	if title == "" {
		title = strings.TrimSpace(goal)
	}
	planID := PlanningSlug(title)
	if planID == "" {
		return Plan{}, fmt.Errorf("title cannot produce a valid Plan ID")
	}
	objective := strings.TrimSpace(response.Objective)
	if objective == "" {
		return Plan{}, fmt.Errorf("objective is required")
	}
	plan := Plan{
		SchemaVersion: PlanningSchemaVersion,
		ID:            planID,
		Title:         title,
		Objective:     objective,
		Status:        "active",
		CreatedAt:     now,
		CreatedBy:     actor,
		UpdatedAt:     now,
		UpdatedBy:     actor,
		Constraints:   cleanStringList(response.Constraints),
		BriefingOrder: []string{},
		Briefings:     []Briefing{},
	}
	if len(response.Briefings) == 0 {
		return Plan{}, fmt.Errorf("at least one briefing is required")
	}

	usedIDs := map[string]bool{}
	dependencyAliases := map[string]string{}
	for _, generated := range response.Briefings {
		briefingTitle := strings.TrimSpace(generated.Title)
		if briefingTitle == "" {
			return Plan{}, fmt.Errorf("briefing title is required")
		}
		briefingID := uniquePlanningSlug(briefingTitle, usedIDs)
		if briefingID == "" {
			return Plan{}, fmt.Errorf("briefing title %q cannot produce a valid ID", briefingTitle)
		}
		usedIDs[briefingID] = true
		dependencyAliases[briefingID] = briefingID
		dependencyAliases[strings.ToLower(briefingTitle)] = briefingID
		dependencyAliases[PlanningSlug(briefingTitle)] = briefingID
		plan.Briefings = append(plan.Briefings, Briefing{
			ID:               briefingID,
			Title:            briefingTitle,
			Objective:        strings.TrimSpace(generated.Objective),
			Intent:           strings.TrimSpace(generated.Intent),
			Status:           "active",
			CompletionSignal: strings.TrimSpace(generated.CompletionSignal),
			CreatedAt:        now,
			CreatedBy:        actor,
			UpdatedAt:        now,
			UpdatedBy:        actor,
			Constraints:      cleanStringList(generated.Constraints),
			MilestoneID:      strings.TrimSpace(generated.MilestoneID),
		})
		plan.BriefingOrder = append(plan.BriefingOrder, briefingID)
	}

	for index, generated := range response.Briefings {
		if strings.TrimSpace(generated.MilestoneID) != "" {
			return Plan{}, fmt.Errorf("briefing %q must not include milestone_id", plan.Briefings[index].Title)
		}
		for _, dependency := range cleanStringList(generated.DependsOn) {
			key := dependency
			if mapped, ok := dependencyAliases[key]; ok {
				plan.Briefings[index].DependsOn = appendMissing(plan.Briefings[index].DependsOn, mapped)
				continue
			}
			if mapped, ok := dependencyAliases[strings.ToLower(key)]; ok {
				plan.Briefings[index].DependsOn = appendMissing(plan.Briefings[index].DependsOn, mapped)
				continue
			}
			if mapped, ok := dependencyAliases[PlanningSlug(key)]; ok {
				plan.Briefings[index].DependsOn = appendMissing(plan.Briefings[index].DependsOn, mapped)
				continue
			}
			return Plan{}, fmt.Errorf("briefing %q depends on unknown Briefing %q", plan.Briefings[index].Title, dependency)
		}
	}
	return plan, nil
}

// PlanningSlug converts a title or string into a lowercase hyphenated planning slug.
func PlanningSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastHyphen := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func uniquePlanningSlug(title string, used map[string]bool) string {
	base := PlanningSlug(title)
	if base == "" {
		base = "briefing"
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[candidate] {
			return candidate
		}
	}
	return ""
}

// CleanStringList removes empty and whitespace-only strings from a slice.
func CleanStringList(values []string) []string {
	return cleanStringList(values)
}

// UniquePlanningSlug derives a unique planning slug from title avoiding used IDs.
func UniquePlanningSlug(title string, used map[string]bool) string {
	return uniquePlanningSlug(title, used)
}

func cleanStringList(values []string) []string {
	var cleaned []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}


func appendMissing(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}

// ExtractAuthorPrefix extracts the author prefix from a formatted ID (e.g. "p-pf-0001" -> "pf").
func ExtractAuthorPrefix(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) >= 2 {
		p0 := strings.ToLower(parts[0])
		if p0 == "p" || p0 == "b" || p0 == "ms" {
			return strings.ToLower(parts[1])
		}
	}
	return ""
}

// AllocatePlanID generates an ID like "p-pf-0001" based on author prefix and existing IDs.
func AllocatePlanID(authorPrefix string, existingIDs []string) string {
	if authorPrefix == "" {
		authorPrefix = "dev"
	}
	authorPrefix = strings.ToLower(authorPrefix)
	pattern := fmt.Sprintf("p-%s-", authorPrefix)
	maxSeq := 0
	for _, id := range existingIDs {
		if strings.HasPrefix(id, pattern) {
			numStr := strings.TrimPrefix(id, pattern)
			if idx := strings.IndexByte(numStr, '-'); idx != -1 {
				numStr = numStr[:idx]
			}
			var seq int
			if _, err := fmt.Sscanf(numStr, "%d", &seq); err == nil && seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	return fmt.Sprintf("p-%s-%04d", authorPrefix, maxSeq+1)
}

// AllocateBriefingID generates a briefing ID like "b-js-0001", inheriting the parent Plan's author prefix.
func AllocateBriefingID(parentPlanID string, defaultAuthorPrefix string, existingIDs []string) string {
	prefix := ExtractAuthorPrefix(parentPlanID)
	if prefix == "" {
		prefix = defaultAuthorPrefix
	}
	if prefix == "" {
		prefix = "dev"
	}
	prefix = strings.ToLower(prefix)
	pattern := fmt.Sprintf("b-%s-", prefix)
	maxSeq := 0
	for _, id := range existingIDs {
		if strings.HasPrefix(id, pattern) {
			numStr := strings.TrimPrefix(id, pattern)
			if idx := strings.IndexByte(numStr, '-'); idx != -1 {
				numStr = numStr[:idx]
			}
			var seq int
			if _, err := fmt.Sscanf(numStr, "%d", &seq); err == nil && seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	return fmt.Sprintf("b-%s-%04d", prefix, maxSeq+1)
}

// AllocateMilestoneID generates a milestone ID like "ms-js-0001", inheriting parent Plan/Briefing prefix if present.
func AllocateMilestoneID(parentPlanID string, defaultAuthorPrefix string, existingIDs []string) string {
	prefix := ExtractAuthorPrefix(parentPlanID)
	if prefix == "" {
		prefix = defaultAuthorPrefix
	}
	if prefix == "" {
		prefix = "dev"
	}
	prefix = strings.ToLower(prefix)
	pattern := fmt.Sprintf("ms-%s-", prefix)
	maxSeq := 0
	for _, id := range existingIDs {
		if strings.HasPrefix(id, pattern) {
			numStr := strings.TrimPrefix(id, pattern)
			if idx := strings.IndexByte(numStr, '-'); idx != -1 {
				numStr = numStr[:idx]
			}
			var seq int
			if _, err := fmt.Sscanf(numStr, "%d", &seq); err == nil && seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	return fmt.Sprintf("ms-%s-%04d", prefix, maxSeq+1)
}
