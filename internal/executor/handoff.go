package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/patrick-folster/cyclestone/internal/config"
)

// phaseHandoff defines the schema for the JSON handout written at the end of each agent's execution phase.
type phaseHandoff struct {
	MilestoneID      string                 `json:"milestone_id"`
	Cycle            int                    `json:"cycle"`
	AgentID          string                 `json:"agent_id"`
	HumanInput       string                 `json:"human_input"` // optional human note or comment from the cycle's execution options
	Summary          map[string]interface{} `json:"summary"`
	OutputContract   string                 `json:"output_contract,omitempty"`
	ValidationStatus string                 `json:"validation_status,omitempty"`
	ValidationErrors []string               `json:"validation_errors,omitempty"`
	Fallback         bool                   `json:"fallback,omitempty"`
	SourceLog        string                 `json:"source_log,omitempty"`
}

type handoffJSONCandidate struct {
	position int
	text     string
}

type DeveloperOutputContract struct {
	ChangedFiles        []string `json:"changed_files"`
	ImplementedBehavior []string `json:"implemented_behavior"`
	ChecksRun           []string `json:"checks_run"`
	Decisions           []string `json:"decisions"`
	Risks               []string `json:"risks"`
}

type QACriterionResult struct {
	Criterion string `json:"criterion"`
	Result    string `json:"result"`
	Notes     string `json:"notes,omitempty"`
}

type QAOutputContract struct {
	Verdict         string              `json:"verdict"`
	CriteriaResults []QACriterionResult `json:"criteria_results"`
	ReviewedFiles   []string            `json:"reviewed_files"`
	FailingChecks   []string            `json:"failing_checks"`
	RequiredFixes   []string            `json:"required_fixes"`
}

type RecommenderOutputContract struct {
	Score          int      `json:"score"`
	Verdict        string   `json:"verdict"`
	Reason         string   `json:"reason"`
	NextCycleFocus []string `json:"next_cycle_focus"`
}

type contractValidationResult struct {
	Summary  map[string]interface{}
	Status   string
	Errors   []string
	RawJSON  []byte
	Contract string
}

// writePhaseHandoff writes the phase execution results to a JSON handout file.
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
	if parsed, ok := extractHandoffJSON(text); ok {
		return writeParsedPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, parsed, cycleNote)
	}
	if maxChars <= 0 {
		maxChars = 12000
	}

	summaryText := ""
	apiKey := os.Getenv("GEMINI_API_KEY")
	if len([]rune(text)) > maxChars && apiKey != "" {
		model := settings.GeminiModel
		prompt := fmt.Sprintf(
			"You are an expert technical coordinator. Please summarize the following phase execution log for agent %s on milestone %s (cycle %d). Generate a dense, high-quality, structured summary (max 1000 words) containing:\n1. Core implementation decisions.\n2. List of files changed/created/reviewed.\n3. Specific tasks, open findings, or fixes required for the next phase.\n\nPhase Output:\n\n%s",
			agentID, milestoneID, cycleNum, text,
		)
		dummyWriter := &liveLogWriter{
			agentID: "summarizer",
		}
		res, err := executeAPI(ctx, "gemini", model, apiKey, []UnifiedMessage{{Role: "user", Content: prompt}}, settings, dummyWriter, true)
		if err == nil && res.Message.Content != "" {
			summaryText = strings.TrimSpace(res.Message.Content)
		}
	}

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
			summary["score"] = boundedLineMatches(text, []string{"recommendation_score", "RECOMMENDATION_SCORE", "score"}, fieldMaxChars)
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
	data, err := json.MarshalIndent(handoff, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// writeParsedPhaseHandoff serializes a parsed JSON block from the agent's output as the phase handout's summary,
// embedding it along with metadata and the optional human comment.
func writeParsedPhaseHandoff(path, milestoneID string, cycleNum int, agentID string, outputPath string, parsed []byte, cycleNote string) error {
	var summary map[string]interface{}
	if err := json.Unmarshal(parsed, &summary); err != nil {
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
	data, err := json.MarshalIndent(handoff, "", "  ")
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
	data, err := json.MarshalIndent(handoff, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
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
	raw, err := extractFinalFencedJSONBlock(text)
	if err != nil {
		result.Errors = []string{err.Error()}
		return result
	}
	result.RawJSON = raw
	var summary map[string]interface{}
	if err := json.Unmarshal(raw, &summary); err != nil {
		result.Errors = []string{fmt.Sprintf("malformed final fenced json for %s contract: %v", contract, err)}
		return result
	}
	result.Summary = summary
	result.Errors = validateContractSummary(summary, contract)
	if len(result.Errors) == 0 {
		result.Status = "valid"
	}
	return result
}

func extractFinalFencedJSONBlock(text string) ([]byte, error) {
	blocks := fencedJSONBlocks(text)
	if len(blocks) == 0 {
		candidates := jsonObjectsFromText(text)
		if len(candidates) > 0 {
			sort.SliceStable(candidates, func(i, j int) bool {
				return candidates[i].position < candidates[j].position
			})
			for i := len(candidates) - 1; i >= 0; i-- {
				var decoded map[string]interface{}
				if err := json.Unmarshal([]byte(candidates[i].text), &decoded); err == nil {
					if hasKnownHandoffKey(decoded) {
						return []byte(strings.TrimSpace(candidates[i].text)), nil
					}
				}
			}
			return []byte(strings.TrimSpace(candidates[len(candidates)-1].text)), nil
		}
		return nil, fmt.Errorf("missing final fenced json block")
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		return blocks[i].position < blocks[j].position
	})
	return []byte(strings.TrimSpace(blocks[len(blocks)-1].text)), nil
}

func validateContractSummary(summary map[string]interface{}, contract string) []string {
	switch contract {
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
	number, ok := value.(float64)
	if !ok {
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
	if err := json.Unmarshal(data, &handoff); err != nil {
		return handoff, err
	}
	return handoff, nil
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

func parseRecommendationScore(handoffPath, outputPath string) int {
	if handoff, err := loadPhaseHandoff(handoffPath); err == nil && handoff.Summary != nil && handoff.ValidationStatus != "invalid" {
		if value, ok := handoff.Summary["score"].(float64); ok && value >= 0 && value <= 10 && value == float64(int(value)) {
			return int(value)
		}
	}
	outBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return -1
	}
	re := regexp.MustCompile(`RECOMMENDATION_SCORE:\s*(\d+)`)
	matches := re.FindStringSubmatch(string(outBytes))
	if len(matches) <= 1 {
		return -1
	}
	score, err := strconv.Atoi(matches[1])
	if err != nil || score < 0 || score > 10 {
		return -1
	}
	return score
}

func extractHandoffJSON(text string) ([]byte, bool) {
	candidates := jsonObjectsFromText(text)
	candidates = append(candidates, fencedJSONBlocks(text)...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].position < candidates[j].position
	})
	var fallback []byte
	for i := len(candidates) - 1; i >= 0; i-- {
		if pretty, decoded, ok := parseHandoffJSONObject(candidates[i].text); ok {
			if fallback == nil {
				fallback = pretty
			}
			if hasKnownHandoffKey(decoded) {
				return pretty, true
			}
		}
	}
	if fallback != nil {
		return fallback, true
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

func fencedJSONBlocks(text string) []handoffJSONCandidate {
	var blocks []handoffJSONCandidate
	var current strings.Builder
	inJSONFence := false
	fenceStart := 0
	offset := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			info := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			if inJSONFence {
				blocks = append(blocks, handoffJSONCandidate{position: fenceStart, text: current.String()})
				current.Reset()
				inJSONFence = false
				offset += len(line) + 1
				continue
			}
			inJSONFence = strings.EqualFold(info, "json")
			fenceStart = offset
			offset += len(line) + 1
			continue
		}
		if inJSONFence {
			current.WriteString(line)
			current.WriteByte('\n')
		}
		offset += len(line) + 1
	}
	return blocks
}

func jsonObjectsFromText(text string) []handoffJSONCandidate {
	var objects []handoffJSONCandidate
	for start, r := range text {
		if r != '{' {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(text[start:]))
		var decoded map[string]interface{}
		if err := decoder.Decode(&decoded); err != nil {
			continue
		}
		end := start + int(decoder.InputOffset())
		objects = append(objects, handoffJSONCandidate{position: start, text: text[start:end]})
	}
	return objects
}

func parseHandoffJSONObject(candidate string) ([]byte, map[string]interface{}, bool) {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(candidate)))
	var decoded map[string]interface{}
	if err := decoder.Decode(&decoded); err != nil {
		return nil, nil, false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, false
	}
	pretty, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return nil, nil, false
	}
	return pretty, decoded, true
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
	path := filepath.Join(".cyclestone", "reports", fmt.Sprintf("%s-cycle-%s-%s-handoff.json", milestoneID, cyclePadded, agentFileID))
	content, err := os.ReadFile(path)
	if err == nil && json.Valid(content) {
		return limitTextMiddle(string(content), maxChars, path)
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
