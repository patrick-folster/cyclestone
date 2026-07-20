package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const PlanningSchemaVersion = 1

const (
	PlanExecutionModeOnce       = "once"
	PlanExecutionModeContinuous = "continuous"
	PlanExecutionModeReview     = "review"
)

var planningIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Plan is the persisted planning-layer record stored under .cyclestone/plans.
type Plan struct {
	SchemaVersion int            `yaml:"schema_version" json:"schema_version"`
	ID            string         `yaml:"id" json:"id"`
	Title         string         `yaml:"title" json:"title"`
	Objective     string         `yaml:"objective" json:"objective"`
	Status        string         `yaml:"status" json:"status"`
	CreatedAt     string         `yaml:"created_at" json:"created_at"`
	CreatedBy     string         `yaml:"created_by" json:"created_by"`
	UpdatedAt     string         `yaml:"updated_at" json:"updated_at"`
	UpdatedBy     string         `yaml:"updated_by" json:"updated_by"`
	Constraints   []string       `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	BriefingOrder []string       `yaml:"briefing_order" json:"briefing_order"`
	Briefings     []Briefing     `yaml:"briefings" json:"briefings"`
	Execution     *PlanExecution `yaml:"execution,omitempty" json:"execution,omitempty"`
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

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		files = append(files, filepath.Join(plansDir, entry.Name()))
	}
	sort.Strings(files)

	seenPlanFiles := make(map[string]string)
	for _, file := range files {
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

func validatePlanExecution(plan Plan, briefingsByID map[string]Briefing, file string, result *PlanningValidationResult) {
	if plan.Execution == nil {
		return
	}
	execution := plan.Execution
	if !IsValidPlanExecutionMode(execution.Mode) {
		result.addError(file, "execution.mode", fmt.Sprintf("invalid Plan execution mode %q", execution.Mode))
	}
	switch execution.State {
	case "running", "paused", "stopped", "blocked", "completed":
	default:
		result.addError(file, "execution.state", fmt.Sprintf("invalid Plan execution state %q", execution.State))
	}
	validatePlanningTimestamp(execution.UpdatedAt, file, "execution.updated_at", result)
	if execution.CurrentBriefingID != "" {
		if _, ok := briefingsByID[execution.CurrentBriefingID]; !ok {
			result.addWarning(file, "execution.current_briefing_id", fmt.Sprintf("execution references missing Briefing %q; Plan execution must stop for explicit repair", execution.CurrentBriefingID))
		}
	}
	if execution.CurrentMilestoneID != "" && !planningIDPattern.MatchString(execution.CurrentMilestoneID) {
		result.addError(file, "execution.current_milestone_id", "Milestone ID must use lowercase ASCII letters, numbers, and hyphens")
	}
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
