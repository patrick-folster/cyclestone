package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrick-folster/cyclestone/internal/config"
	"gopkg.in/yaml.v3"
)

// phaseHandoff defines the schema for the YAML handoff written at the end of each agent's execution phase.
type phaseHandoff struct {
	MilestoneID      string                 `yaml:"milestone_id"`
	Cycle            int                    `yaml:"cycle"`
	AgentID          string                 `yaml:"agent_id"`
	HumanInput       string                 `yaml:"human_input"` // optional human note or comment from the cycle's execution options
	Summary          map[string]interface{} `yaml:"summary"`
	OutputContract   string                 `yaml:"output_contract,omitempty"`
	ValidationStatus string                 `yaml:"validation_status,omitempty"`
	ValidationErrors []string               `yaml:"validation_errors,omitempty"`
	Fallback         bool                   `yaml:"fallback,omitempty"`
	SourceLog        string                 `yaml:"source_log,omitempty"`
}

type handoffDocumentCandidate struct {
	position int
	text     string
}

type DeveloperOutputContract struct {
	ChangedFiles        []string `yaml:"changed_files"`
	ImplementedBehavior []string `yaml:"implemented_behavior"`
	ChecksRun           []string `yaml:"checks_run"`
	Decisions           []string `yaml:"decisions"`
	Risks               []string `yaml:"risks"`
}

type QACriterionResult struct {
	Criterion string `yaml:"criterion"`
	Result    string `yaml:"result"`
	Notes     string `yaml:"notes,omitempty"`
}

type QAOutputContract struct {
	Verdict         string              `yaml:"verdict"`
	CriteriaResults []QACriterionResult `yaml:"criteria_results"`
	ReviewedFiles   []string            `yaml:"reviewed_files"`
	FailingChecks   []string            `yaml:"failing_checks"`
	RequiredFixes   []string            `yaml:"required_fixes"`
}

type RecommenderOutputContract struct {
	Score          int      `yaml:"score"`
	Verdict        string   `yaml:"verdict"`
	Reason         string   `yaml:"reason"`
	NextCycleFocus []string `yaml:"next_cycle_focus"`
}

type contractValidationResult struct {
	Summary  map[string]interface{}
	Status   string
	Errors   []string
	RawYAML  []byte
	Contract string
}

// writePhaseHandoff writes the phase execution results to a YAML handoff file.
// It includes the optional human comment/note inside the human_input property.
func writePhaseHandoff(ctx context.Context, settings config.Settings, path, milestoneID string, cycleNum int, agentID string, outputContract string, outputPath string, maxChars int, cycleNote string) error {
	outBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return err
	}
	text := string(outBytes)
	contract := effectiveOutputContract(agentID, outputContract)
	if contract != "" {
		validation := parseAndValidateContract(text, contract)
		return writeContractPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, cycleNote, validation)
	}
	if parsed, ok := extractHandoffYAML(text); ok {
		return writeParsedPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, parsed, cycleNote)
	}
	if maxChars <= 0 {
		maxChars = 12000
	}

	summaryText := ""

	fieldMaxChars := maxChars
	if fieldMaxChars > maxFallbackHandoffFieldChars {
		fieldMaxChars = maxFallbackHandoffFieldChars
	}
	totalMaxChars := maxChars
	if totalMaxChars > maxFallbackHandoffChars {
		totalMaxChars = maxFallbackHandoffChars
	}
	summary := map[string]interface{}{}

	if summaryText != "" {
		summary["summary"] = summaryText
	} else {
		switch agentID {
		case "pm":
			summary["scope"] = boundedLineMatches(text, []string{"scope", "in-scope", "goal"}, fieldMaxChars)
			summary["non_goals"] = boundedLineMatches(text, []string{"non-goal", "out of scope"}, fieldMaxChars)
			summary["target_paths"] = boundedLineMatches(text, []string{"path", "file", "folder"}, fieldMaxChars)
			summary["acceptance_map"] = boundedLineMatches(text, []string{"acceptance", "criteria"}, fieldMaxChars)
			summary["risks"] = boundedLineMatches(text, []string{"risk", "unknown", "blocker"}, fieldMaxChars)
		case "developer":
			summary["implemented_behavior"] = boundedLineMatches(text, []string{"implemented", "summary", "behavior"}, fieldMaxChars)
			summary["changed_files"] = boundedLineMatches(text, []string{"file", "changed"}, fieldMaxChars)
			summary["checks_run"] = boundedLineMatches(text, []string{"pass", "fail", "test", "lint", "build", "check"}, fieldMaxChars)
			summary["decisions"] = boundedLineMatches(text, []string{"decision", "decided"}, fieldMaxChars)
			summary["risks"] = boundedLineMatches(text, []string{"risk", "skipped", "follow-up"}, fieldMaxChars)
		case "qa":
			summary["verdict"] = boundedLineMatches(text, []string{"verdict", "approved", "blocked", "needs-human-review"}, fieldMaxChars)
			summary["criteria_results"] = boundedLineMatches(text, []string{"criteria", "acceptance", "pass", "fail"}, fieldMaxChars)
			summary["reviewed_files"] = boundedLineMatches(text, []string{"reviewed", "file"}, fieldMaxChars)
			summary["failing_checks"] = boundedLineMatches(text, []string{"fail", "failed", "error"}, fieldMaxChars)
			summary["required_fixes"] = boundedLineMatches(text, []string{"fix", "required", "blocker"}, fieldMaxChars)
		case "recommender":
			summary["score"] = boundedLineMatches(text, []string{"score"}, fieldMaxChars)
			summary["reason"] = boundedLineMatches(text, []string{"reason", "gap", "recommend"}, fieldMaxChars)
		default:
			summary["summary"] = limitTextMiddle(text, fieldMaxChars, outputPath)
		}
		summary = limitFallbackSummary(summary, totalMaxChars, fieldMaxChars)
	}

	handoff := phaseHandoff{
		MilestoneID: milestoneID,
		Cycle:       cycleNum,
		AgentID:     agentID,
		HumanInput:  cycleNote,
		Summary:     summary,
		Fallback:    true,
		SourceLog:   outputPath,
	}
	data, err := marshalPhaseHandoffYAML(handoff)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// writeParsedPhaseHandoff serializes a parsed YAML document from the agent's output as the phase handoff's summary,
// embedding it along with metadata and the optional human comment.
func writeParsedPhaseHandoff(path, milestoneID string, cycleNum int, agentID string, outputPath string, parsed []byte, cycleNote string) error {
	var summary map[string]interface{}
	if err := unmarshalYAMLMap(parsed, &summary); err != nil {
		return err
	}
	handoff := phaseHandoff{
		MilestoneID: milestoneID,
		Cycle:       cycleNum,
		AgentID:     agentID,
		HumanInput:  cycleNote,
		Summary:     summary,
		SourceLog:   outputPath,
	}
	data, err := marshalPhaseHandoffYAML(handoff)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func writeContractPhaseHandoff(path, milestoneID string, cycleNum int, agentID string, outputPath string, cycleNote string, validation contractValidationResult) error {
	handoff := phaseHandoff{
		MilestoneID:      milestoneID,
		Cycle:            cycleNum,
		AgentID:          agentID,
		HumanInput:       cycleNote,
		Summary:          validation.Summary,
		OutputContract:   validation.Contract,
		ValidationStatus: validation.Status,
		ValidationErrors: validation.Errors,
		SourceLog:        outputPath,
	}
	if handoff.Summary == nil {
		handoff.Summary = map[string]interface{}{}
	}
	data, err := marshalPhaseHandoffYAML(handoff)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func marshalPhaseHandoffYAML(handoff phaseHandoff) ([]byte, error) {
	return yaml.Marshal(handoff)
}

func effectiveOutputContract(_ string, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	return ""
}

func parseAndValidateContract(text, contract string) contractValidationResult {
	result := contractValidationResult{
		Contract: contract,
		Status:   "invalid",
	}
	raw, err := extractFinalYAMLDocument(text)
	if err != nil {
		result.Errors = []string{err.Error()}
		return result
	}
	result.RawYAML = raw
	var summary map[string]interface{}
	if err := unmarshalYAMLMap(raw, &summary); err != nil {
		result.Errors = []string{fmt.Sprintf("malformed yaml for %s contract: %v", contract, err)}
		return result
	}
	result.Summary = summary
	result.Errors = validateContractSummary(summary, contract)
	if len(result.Errors) == 0 {
		result.Status = "valid"
	}
	return result
}

func extractFinalYAMLDocument(text string) ([]byte, error) {
	blocks := fencedYAMLBlocks(text)
	if len(blocks) == 0 {
		raw := []byte(strings.TrimSpace(text))
		var decoded map[string]interface{}
		if err := unmarshalYAMLMap(raw, &decoded); err == nil && hasKnownHandoffKey(decoded) {
			return raw, nil
		}
		return nil, fmt.Errorf("missing yaml document for output contract")
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		return blocks[i].position < blocks[j].position
	})
	return []byte(strings.TrimSpace(blocks[len(blocks)-1].text)), nil
}

func validateContractSummary(summary map[string]interface{}, contract string) []string {
	switch contract {
	case "pm":
		var errs []string
		errs = append(errs, validateRequiredStringArrays(summary, contract, []string{"scope", "non_goals", "target_paths", "risks"})...)
		errs = append(errs, validateAcceptanceMap(summary, contract)...)
		return errs
	case "developer":
		return validateRequiredStringArrays(summary, contract, []string{"changed_files", "implemented_behavior", "checks_run", "decisions", "risks"})
	case "qa":
		var errs []string
		errs = append(errs, requireStringField(summary, contract, "verdict")...)
		errs = append(errs, validateCriteriaResults(summary, contract)...)
		errs = append(errs, validateRequiredStringArrays(summary, contract, []string{"reviewed_files", "failing_checks", "required_fixes"})...)
		return errs
	case "recommender":
		var errs []string
		errs = append(errs, requireNumberField(summary, contract, "score")...)
		errs = append(errs, requireStringField(summary, contract, "verdict")...)
		errs = append(errs, requireStringField(summary, contract, "reason")...)
		errs = append(errs, validateRequiredStringArrays(summary, contract, []string{"next_cycle_focus"})...)
		return errs
	default:
		return []string{fmt.Sprintf("unknown output contract %q", contract)}
	}
}

func validateAcceptanceMap(summary map[string]interface{}, contract string) []string {
	value, ok := summary["acceptance_map"]
	if !ok {
		return []string{fmt.Sprintf("%s contract missing required field %q", contract, "acceptance_map")}
	}
	items, ok := value.(map[string]interface{})
	if !ok {
		return []string{fmt.Sprintf("%s contract field %q must be an object with string values", contract, "acceptance_map")}
	}
	var errs []string
	for key, item := range items {
		if _, ok := item.(string); !ok {
			errs = append(errs, fmt.Sprintf("%s contract field %q value for %q must be a string", contract, "acceptance_map", key))
		}
	}
	return errs
}

func validateRequiredStringArrays(summary map[string]interface{}, contract string, fields []string) []string {
	var errs []string
	for _, field := range fields {
		value, ok := summary[field]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s contract missing required field %q", contract, field))
			continue
		}
		items, ok := value.([]interface{})
		if !ok {
			errs = append(errs, fmt.Sprintf("%s contract field %q must be an array of strings", contract, field))
			continue
		}
		for idx, item := range items {
			if _, ok := item.(string); !ok {
				errs = append(errs, fmt.Sprintf("%s contract field %q item %d must be a string", contract, field, idx))
			}
		}
	}
	return errs
}

func requireStringField(summary map[string]interface{}, contract, field string) []string {
	value, ok := summary[field]
	if !ok {
		return []string{fmt.Sprintf("%s contract missing required field %q", contract, field)}
	}
	if _, ok := value.(string); !ok {
		return []string{fmt.Sprintf("%s contract field %q must be a string", contract, field)}
	}
	return nil
}

func requireNumberField(summary map[string]interface{}, contract, field string) []string {
	value, ok := summary[field]
	if !ok {
		return []string{fmt.Sprintf("%s contract missing required field %q", contract, field)}
	}
	number, ok := numericValueAsFloat(value)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return []string{fmt.Sprintf("%s contract field %q must be a number", contract, field)}
	}
	if number < 0 || number > 10 || number != float64(int(number)) {
		return []string{fmt.Sprintf("%s contract field %q must be an integer from 0 to 10", contract, field)}
	}
	return nil
}

func validateCriteriaResults(summary map[string]interface{}, contract string) []string {
	value, ok := summary["criteria_results"]
	if !ok {
		return []string{fmt.Sprintf("%s contract missing required field %q", contract, "criteria_results")}
	}
	items, ok := value.([]interface{})
	if !ok {
		return []string{fmt.Sprintf("%s contract field %q must be an array of objects", contract, "criteria_results")}
	}
	var errs []string
	for idx, item := range items {
		obj, ok := item.(map[string]interface{})
		if !ok {
			errs = append(errs, fmt.Sprintf("%s contract field %q item %d must be an object", contract, "criteria_results", idx))
			continue
		}
		for _, field := range []string{"criterion", "result"} {
			value, ok := obj[field]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s contract criteria_results item %d missing required field %q", contract, idx, field))
				continue
			}
			if _, ok := value.(string); !ok {
				errs = append(errs, fmt.Sprintf("%s contract criteria_results item %d field %q must be a string", contract, idx, field))
			}
		}
		if notes, ok := obj["notes"]; ok {
			if _, ok := notes.(string); !ok {
				errs = append(errs, fmt.Sprintf("%s contract criteria_results item %d field %q must be a string", contract, idx, "notes"))
			}
		}
	}
	return errs
}

func loadPhaseHandoff(path string) (phaseHandoff, error) {
	var handoff phaseHandoff
	data, err := os.ReadFile(path)
	if err != nil {
		return handoff, err
	}
	if err := yaml.Unmarshal(data, &handoff); err == nil {
		handoff.Summary = normalizeHandoffSummary(handoff.Summary)
		return handoff, nil
	}
	return handoff, fmt.Errorf("failed to parse YAML handoff: %s", path)
}

func normalizeHandoffSummary(summary map[string]interface{}) map[string]interface{} {
	if summary == nil {
		return nil
	}
	normalized, ok := normalizeYAMLValue(summary).(map[string]interface{})
	if !ok {
		return summary
	}
	return normalized
}

func phaseHandoffStatus(path string) (string, []string) {
	handoff, err := loadPhaseHandoff(path)
	if err != nil {
		return "", nil
	}
	return handoff.ValidationStatus, handoff.ValidationErrors
}

func contractValidationCycleStatus(agentID, current string) string {
	if current == "failed" {
		return current
	}
	switch agentID {
	case "developer":
		return "failed"
	case "qa":
		return "blocked"
	default:
		return current
	}
}

func qaVerdictFromHandoff(path string) string {
	handoff, err := loadPhaseHandoff(path)
	if err != nil || handoff.Summary == nil {
		return ""
	}
	verdict, _ := handoff.Summary["verdict"].(string)
	return strings.ToLower(strings.TrimSpace(verdict))
}

func applyQAVerdictToCycleStatus(verdict, current string) string {
	if current == "failed" {
		return current
	}
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case "approved", "pass", "passed":
		return current
	case "failed", "fail":
		return "failed"
	case "blocked", "needs-human-review", "needs_human_review":
		return "blocked"
	default:
		return current
	}
}

func parseRecommendationScore(handoffPath string) int {
	if handoff, err := loadPhaseHandoff(handoffPath); err == nil && handoff.Summary != nil && handoff.ValidationStatus != "invalid" {
		if score, ok := numericValueAsIntInRange(handoff.Summary["score"], 0, 10); ok {
			return score
		}
	}
	return -1
}

func extractHandoffYAML(text string) ([]byte, bool) {
	candidates := fencedYAMLBlocks(text)
	candidates = append(candidates, handoffRawYAMLCandidate(text)...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].position < candidates[j].position
	})
	for i := len(candidates) - 1; i >= 0; i-- {
		if pretty, decoded, ok := parseHandoffYAMLDocument(candidates[i].text); ok {
			if hasKnownHandoffKey(decoded) {
				return pretty, true
			}
		}
	}
	return nil, false
}

func hasKnownHandoffKey(decoded map[string]interface{}) bool {
	for _, key := range []string{
		"scope", "non_goals", "target_paths", "acceptance_map",
		"changed_files", "implemented_behavior", "checks_run", "decisions",
		"verdict", "criteria_results", "reviewed_files", "failing_checks", "required_fixes",
		"score", "reason", "next_cycle_focus",
	} {
		if _, ok := decoded[key]; ok {
			return true
		}
	}
	return false
}

func fencedYAMLBlocks(text string) []handoffDocumentCandidate {
	var blocks []handoffDocumentCandidate
	var current strings.Builder
	inYAMLFence := false
	fenceStart := 0
	offset := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			info := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			if inYAMLFence {
				blocks = append(blocks, handoffDocumentCandidate{position: fenceStart, text: current.String()})
				current.Reset()
				inYAMLFence = false
				offset += len(line) + 1
				continue
			}
			fields := strings.Fields(info)
			if len(fields) == 0 {
				fields = []string{""}
			}
			info = strings.ToLower(fields[0])
			inYAMLFence = info == "yaml" || info == "yml"
			fenceStart = offset
			offset += len(line) + 1
			continue
		}
		if inYAMLFence {
			current.WriteString(line)
			current.WriteByte('\n')
		}
		offset += len(line) + 1
	}
	return blocks
}

func handoffRawYAMLCandidate(text string) []handoffDocumentCandidate {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	return []handoffDocumentCandidate{{position: 0, text: trimmed}}
}

func parseHandoffYAMLDocument(candidate string) ([]byte, map[string]interface{}, bool) {
	var decoded map[string]interface{}
	if err := unmarshalYAMLMap([]byte(strings.TrimSpace(candidate)), &decoded); err != nil {
		return nil, nil, false
	}
	pretty, err := yaml.Marshal(decoded)
	if err != nil {
		return nil, nil, false
	}
	return pretty, decoded, true
}

func unmarshalYAMLMap(data []byte, out *map[string]interface{}) error {
	var decoded interface{}
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return err
	}
	normalized := normalizeYAMLValue(decoded)
	mapped, ok := normalized.(map[string]interface{})
	if !ok {
		return fmt.Errorf("expected YAML mapping at document root")
	}
	*out = mapped
	return nil
}

func normalizeYAMLValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = normalizeYAMLValue(item)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[fmt.Sprint(key)] = normalizeYAMLValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for idx, item := range typed {
			out[idx] = normalizeYAMLValue(item)
		}
		return out
	default:
		return value
	}
}

func numericValueAsFloat(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}

func numericValueAsIntInRange(value interface{}, min, max int) (int, bool) {
	number, ok := numericValueAsFloat(value)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	asInt := int(number)
	if number != float64(asInt) || asInt < min || asInt > max {
		return 0, false
	}
	return asInt, true
}

func limitFallbackSummary(summary map[string]interface{}, totalMaxChars, fieldMaxChars int) map[string]interface{} {
	if totalMaxChars <= 0 {
		totalMaxChars = maxFallbackHandoffChars
	}
	if fieldMaxChars <= 0 || fieldMaxChars > maxFallbackHandoffFieldChars {
		fieldMaxChars = maxFallbackHandoffFieldChars
	}
	for key, value := range summary {
		summary[key] = limitFallbackValue(value, fieldMaxChars)
	}
	for marshaledFallbackSummaryLen(summary) > totalMaxChars {
		changed := false
		for key, value := range summary {
			nextLimit := fieldMaxChars / 2
			if nextLimit < 160 {
				nextLimit = 160
			}
			limited := limitFallbackValue(value, nextLimit)
			if fmt.Sprintf("%v", limited) != fmt.Sprintf("%v", value) {
				summary[key] = limited
				changed = true
			}
		}
		if !changed {
			break
		}
		fieldMaxChars /= 2
		if fieldMaxChars < 160 {
			break
		}
	}
	return summary
}

func limitFallbackValue(value interface{}, maxChars int) interface{} {
	switch typed := value.(type) {
	case string:
		return limitTextMiddle(typed, maxChars, "handoff field")
	case []string:
		var out []string
		used := 0
		for _, item := range typed {
			itemLimit := maxChars / 3
			if itemLimit <= 0 || itemLimit > maxChars {
				itemLimit = maxChars
			}
			limited := limitTextMiddle(item, itemLimit, "handoff item")
			itemLen := len([]rune(limited))
			if used+itemLen > maxChars && len(out) > 0 {
				break
			}
			out = append(out, limited)
			used += itemLen
			if len(out) >= 8 {
				break
			}
		}
		return out
	default:
		return value
	}
}

func marshaledFallbackSummaryLen(summary map[string]interface{}) int {
	data, err := json.Marshal(summary)
	if err != nil {
		return 0
	}
	return len([]rune(string(data)))
}

func boundedLineMatches(text string, needles []string, maxChars int) []string {
	var lines []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		lower := strings.ToLower(trimmed)
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(needle)) {
				lines = append(lines, trimmed)
				seen[trimmed] = true
				break
			}
		}
		if len(lines) >= 12 {
			break
		}
	}
	if len(lines) == 0 {
		limited := limitTextMiddle(text, maxChars, "phase output")
		for _, line := range strings.Split(limited, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				lines = append(lines, trimmed)
				if len(lines) >= 12 {
					break
				}
			}
		}
	}
	limited := make([]string, 0, len(lines))
	used := 0
	for _, line := range lines {
		lineLimit := maxChars / 3
		if lineLimit < 160 && maxChars >= 160 {
			lineLimit = 160
		}
		if lineLimit <= 0 || lineLimit > maxChars {
			lineLimit = maxChars
		}
		item := limitTextMiddle(line, lineLimit, "handoff line")
		itemLen := len([]rune(item))
		if used+itemLen > maxChars && len(limited) > 0 {
			break
		}
		limited = append(limited, item)
		used += itemLen
	}
	return limited
}

func readHandoffOrFallback(milestoneID, cyclePadded, agentID string, maxChars int, pipeline []config.Agent) string {
	agentFileID := getAgentFileID(agentID, pipeline)
	path := phaseHandoffPath(filepath.Join(".cyclestone", "reports"), milestoneID, cyclePadded, agentFileID)
	content, err := os.ReadFile(path)
	if err == nil {
		if handoffContentValid(content) {
			return limitTextMiddle(string(content), maxChars, path)
		}
	}

	outputPath := filepath.Join(".cyclestone", "reports", fmt.Sprintf("%s-cycle-%s-%s-output.log", milestoneID, cyclePadded, agentFileID))
	output, outputErr := os.ReadFile(outputPath)
	if outputErr != nil {
		return ""
	}
	var sb strings.Builder
	if err != nil {
		sb.WriteString(fmt.Sprintf("Handoff summary missing: %s\n", path))
	} else {
		sb.WriteString(fmt.Sprintf("Handoff summary malformed: %s\n", path))
	}
	sb.WriteString(fmt.Sprintf("Source log fallback: %s\n\n", outputPath))
	sb.WriteString(limitTextMiddle(string(output), maxChars, outputPath))
	return sb.String()
}

func handoffContentValid(content []byte) bool {
	var decoded phaseHandoff
	if err := yaml.Unmarshal(content, &decoded); err != nil {
		return false
	}
	if decoded.MilestoneID != "" || decoded.AgentID != "" || decoded.OutputContract != "" || decoded.ValidationStatus != "" || decoded.SourceLog != "" || decoded.Summary != nil {
		return true
	}
	return false
}

func phaseHandoffPath(reportsDir, milestoneID, cyclePadded, agentFileID string) string {
	return filepath.Join(reportsDir, fmt.Sprintf("%s-cycle-%s-%s-handoff.yaml", milestoneID, cyclePadded, agentFileID))
}

func getAgentFileID(agentID string, pipeline []config.Agent) string {
	for idx, a := range pipeline {
		if a.ID == agentID {
			return fmt.Sprintf("%02d-%s", idx+1, agentID)
		}
	}
	// Fallback for default pipeline if pipeline is empty or agent is not found
	switch agentID {
	case "pm":
		return "01-pm"
	case "developer":
		return "02-developer"
	case "qa":
		return "03-qa"
	case "recommender":
		if len(pipeline) > 0 {
			return fmt.Sprintf("%02d-recommender", len(pipeline)+1)
		}
		return "04-recommender"
	default:
		return agentID
	}
}
