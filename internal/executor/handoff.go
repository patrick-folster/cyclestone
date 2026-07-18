package executor

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
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
	Score                        int      `yaml:"score"`
	AgentInstructionsUpdateScore int      `yaml:"agent_instructions_update_score"`
	Verdict                      string   `yaml:"verdict"`
	Reason                       string   `yaml:"reason"`
	NextCycleFocus               []string `yaml:"next_cycle_focus"`
}

type contractValidationResult struct {
	Summary  map[string]interface{}
	Status   string
	Errors   []string
	RawYAML  []byte
	Contract string
}

// readHandoffTempYAML reads the dedicated temp YAML handoff file that the agent
// was instructed to write (via the {{HANDOFF_YAML_PATH}} placeholder in the
// prompt). It returns the trimmed file content and true when the file exists
// and is non-empty.
func readHandoffTempYAML(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// stripSearchReplaceWrapper extracts the content from a SEARCH/REPLACE edit
// block when an agent's dedicated temp handoff file literally contains the
// fence markers. The prompt instructs agents to write their handoff YAML using
// a SEARCH/REPLACE block (an empty <<<<<<< SEARCH section, the full YAML after
// the ======= divider, ending with >>>>>>> REPLACE). Aider applies the edit and
// strips the markers, but other runners (e.g. Codex) may write the markers
// literally into the file. This function returns just the REPLACE section so
// the YAML parser sees a clean document. When the text is not a SEARCH/REPLACE
// block it is returned unchanged.
func stripSearchReplaceWrapper(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<<<<<<< SEARCH") {
		return text
	}
	lines := strings.Split(trimmed, "\n")
	dividerIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "=======" {
			dividerIdx = i
			break
		}
	}
	if dividerIdx == -1 {
		return text
	}
	var out []string
	for i := dividerIdx + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), ">>>>>>> REPLACE") {
			break
		}
		out = append(out, lines[i])
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// parseAndValidateContractContent parses a raw YAML document (typically read
// from the agent's dedicated temp handoff file) and validates it against the
// given output contract. Unlike parseAndValidateContract it does not scan for
// embedded YAML blocks — the content is assumed to be the document itself.
func parseAndValidateContractContent(text, contract string) contractValidationResult {
	result := contractValidationResult{
		Contract: contract,
		Status:   "invalid",
	}
	text = stripSearchReplaceWrapper(text)
	raw := normalizeHandoffYAML([]byte(strings.TrimSpace(text)))
	var summary map[string]interface{}
	if err := unmarshalYAMLMap(raw, &summary); err != nil {
		result.Errors = []string{fmt.Sprintf("malformed yaml for %s contract: %v", contract, err)}
		return result
	}
	result.Summary = summary
	result.RawYAML = raw
	result.Errors = validateContractSummary(summary, contract)
	if len(result.Errors) == 0 {
		result.Status = "valid"
	}
	return result
}

// writePhaseHandoff writes the phase execution results to a YAML handoff file.
// It includes the optional human comment/note inside the human_input property.
func writePhaseHandoff(ctx context.Context, settings config.Settings, path, milestoneID string, cycleNum int, agentID string, outputContract string, outputPath string, maxChars int, cycleNote string, runner string, handoffYAMLPath ...string) error {
	// Prefer a dedicated temp YAML file written by the agent (specified in the
	// prompt via the {{HANDOFF_YAML_PATH}} placeholder). When the agent writes
	// its structured handoff directly to that file we avoid the brittle
	// console-log extraction/normalization pipeline entirely.
	var tempYAMLPath string
	if len(handoffYAMLPath) > 0 {
		tempYAMLPath = handoffYAMLPath[0]
	}
	if tempContent, ok := readHandoffTempYAML(tempYAMLPath); ok {
		tempContent = stripSearchReplaceWrapper(tempContent)
		contract := effectiveOutputContract(agentID, outputContract)
		if contract != "" {
			validation := parseAndValidateContractContent(tempContent, contract)
			if validation.Status == "valid" {
				return writeContractPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, cycleNote, validation)
			}
			if contractValidationBypassed(runner) {
				if validation.Summary != nil {
					return writeContractPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, cycleNote, contractValidationResult{
						Summary:  validation.Summary,
						Contract: validation.Contract,
					})
				}
			} else {
				return writeContractPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, cycleNote, validation)
			}
		}
		if parsed, _, ok := parseHandoffYAMLDocument(tempContent); ok {
			return writeParsedPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, parsed, cycleNote)
		}
		// The temp file exists but could not be parsed; fall through to the
		// log-based extraction below as a safety net.
	}

	outBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return err
	}
	text := string(outBytes)
	contract := effectiveOutputContract(agentID, outputContract)
	if contract != "" {
		// Fallback path: the dedicated temp handoff file was absent or
		// unparseable. Agents run through the Aider CLI (aider/ollama) often
		// write their structured output contract to a sidecar .yaml file next
		// to the output log instead of emitting it inline. The CLI log display
		// mangles YAML with line wrapping and intermixed UI chrome, so prefer
		// the sidecar document when present so the contract is extracted
		// reliably.
		contractText := text
		if sidecar, ok := readSidecarOutputYAML(outputPath); ok {
			contractText = sidecar
		}
		validation := parseAndValidateContract(contractText, contract)
		if validation.Status == "valid" {
			return writeContractPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, cycleNote, validation)
		}
		if contractValidationBypassed(runner) {
			// Aider/Ollama runners bypass strict contract validation. When the
			// agent produced a parseable YAML document, capture it as the handoff
			// summary with the output contract set (so the TUI can render its
			// fields) but without recording validation errors. When no parseable
			// document was produced, fall through to the heuristic fallback
			// summary below.
			if validation.Summary != nil {
				return writeContractPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, cycleNote, contractValidationResult{
					Summary:  validation.Summary,
					Contract: validation.Contract,
				})
			}
		} else {
			return writeContractPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, cycleNote, validation)
		}
	}
	if parsed, ok := extractHandoffYAML(text); ok {
		return writeParsedPhaseHandoff(path, milestoneID, cycleNum, agentID, outputPath, parsed, cycleNote)
	}
	if maxChars <= 0 {
		maxChars = 12000
	}
	fieldMaxChars := maxChars
	if fieldMaxChars > maxFallbackHandoffFieldChars {
		fieldMaxChars = maxFallbackHandoffFieldChars
	}

	// No structured YAML document was produced. Emit a clean, contract-shaped
	// fallback with empty fields rather than keyword-scraping the raw log: the
	// agent produced no structured output, so fabricating field values from
	// prose or CLI chrome would be misleading. The model's actual answer is
	// preserved verbatim (truncated) in a "note" field for human inspection.
	answerText := strings.TrimSpace(answerRegion(text))
	summary := map[string]interface{}{}
	switch agentID {
	case "pm":
		summary["scope"] = []string{}
		summary["non_goals"] = []string{}
		summary["target_paths"] = []string{}
		summary["acceptance_map"] = map[string]interface{}{}
		summary["risks"] = []string{}
	case "developer":
		summary["changed_files"] = []string{}
		summary["implemented_behavior"] = []string{}
		summary["checks_run"] = []string{}
		summary["decisions"] = []string{}
		summary["risks"] = []string{}
	case "qa":
		summary["verdict"] = ""
		summary["criteria_results"] = []interface{}{}
		summary["reviewed_files"] = []string{}
		summary["failing_checks"] = []string{}
		summary["required_fixes"] = []string{}
	case "recommender":
		// score is intentionally omitted: a numeric default (e.g. 0) would be
		// mistaken for a real recommendation. parseRecommendationScore returns
		// -1 ("no recommendation") when score is absent, which is the correct
		// signal when the recommender produced no structured output.
		summary["verdict"] = ""
		summary["reason"] = ""
		summary["next_cycle_focus"] = []string{}
	default:
		summary["summary"] = limitTextMiddle(answerText, fieldMaxChars, outputPath)
	}
	// For the default agent the answer already lives in summary["summary"]; for
	// the contract-shaped agents, preserve the raw answer in a note field.
	if _, hasSummary := summary["summary"]; !hasSummary && answerText != "" {
		summary["note"] = limitTextMiddle(answerText, fieldMaxChars, "agent answer")
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

func mergeProposedAgentInstructionsUpdate(handoffPath string, interception agentInstructionsInterception) error {
	if strings.TrimSpace(interception.Path) == "" || strings.TrimSpace(interception.Change) == "" {
		return nil
	}
	handoff, err := loadPhaseHandoff(handoffPath)
	if err != nil {
		return err
	}
	if handoff.Summary == nil {
		handoff.Summary = map[string]interface{}{}
	}
	handoff.Summary["proposed_agent_instructions_update"] = interception.ProposedContent
	handoff.Summary["proposed_agent_instructions_path"] = interception.Path
	handoff.Summary["proposed_agent_instructions_change"] = interception.Change
	data, err := marshalPhaseHandoffYAML(handoff)
	if err != nil {
		return err
	}
	return os.WriteFile(handoffPath, data, 0644)
}

func effectiveOutputContract(_ string, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	return ""
}

// contractValidationBypassed reports whether the runner bypasses strict output
// contract validation. Aider and Ollama runners are executed through the Aider
// CLI, which cannot reliably emit the final structured YAML document expected by
// output contracts, so strict validation is bypassed for them.
func contractValidationBypassed(runner string) bool {
	return runner == "aider" || runner == "ollama"
}

// readSidecarOutputYAML reads a sibling .yaml file for an output log path (for
// example "001-01-pm-output.log" -> "001-01-pm-output.yaml"). Agents run through
// the Aider CLI frequently write their structured output contract to such a
// sidecar file rather than emitting it inline in the log. It returns the trimmed
// document content and true when the sidecar exists and is non-empty.
func readSidecarOutputYAML(outputPath string) (string, bool) {
	sidecarPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".yaml"
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// removeSidecarOutputYAML deletes a sibling .yaml sidecar file for an output log
// path if it exists. It is called before a runner executes so that a sidecar left
// over from a previous run of the same cycle cannot be mistaken for the current
// run's structured output.
func removeSidecarOutputYAML(outputPath string) {
	sidecarPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + ".yaml"
	_ = os.Remove(sidecarPath)
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

// stripAiderChatter removes Aider CLI chrome and SEARCH/REPLACE edit-block
// markers from a runner output log before YAML extraction so they cannot be
// absorbed into block-scalar values by the inline scanner. When the dedicated
// temp handoff file cannot be written (for example because the runner is in
// --dry-run mode or the file was not added to the Aider chat), agents
// frequently emit their structured handoff inside a SEARCH/REPLACE block, and
// Aider appends a token-usage summary plus edit/IO diagnostics after the
// model's answer. The greedy block-scalar flattening logic treats these
// column-0 lines as content, polluting list/scalar values. Stripping them
// upfront keeps the fallback log extraction faithful to the agent's intent.
func stripAiderChatter(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if isAiderChatterLine(strings.TrimSpace(line)) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isAiderChatterLine reports whether a (already whitespace-trimmed) log line is
// Aider CLI chrome or a leaked diagnostic that must not appear in a handoff
// document. Blank lines are never chatter.
func isAiderChatterLine(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	// Aider SEARCH/REPLACE edit-block fences. The "=======" divider is matched
	// exactly (seven equals) so a legitimate YAML value of repeated "=" cannot
	// be dropped.
	if strings.HasPrefix(trimmed, "<<<<<<< SEARCH") ||
		strings.HasPrefix(trimmed, ">>>>>>> REPLACE") ||
		trimmed == "=======" {
		return true
	}
	// Aider token-usage summary, e.g. "Tokens: 40k sent, 2.1k received."
	if strings.HasPrefix(trimmed, "Tokens: ") &&
		strings.Contains(trimmed, " sent, ") &&
		strings.Contains(trimmed, " received") {
		return true
	}
	// Aider/orchestrator diagnostics leaked from a failed temp-file write.
	if strings.Contains(trimmed, "file not found error") ||
		strings.Contains(trimmed, "'NoneType' object has no attribute 'splitlines'") {
		return true
	}
	if strings.HasPrefix(trimmed, "Unable to read ") &&
		(strings.Contains(trimmed, "No such file or directory") ||
			strings.Contains(trimmed, "[Errno 2]")) {
		return true
	}
	// Bare echoes of the dedicated temp handoff file path (absolute or
	// relative), with or without a trailing diagnostic suffix such as
	// ": file not found error". Aider wraps long paths across two display
	// lines, so match on the temp directory prefix alone: a line containing
	// ".cyclestone/temp/" is never legitimate handoff YAML content.
	if strings.Contains(trimmed, ".cyclestone/temp/") {
		return true
	}
	return false
}

func extractFinalYAMLDocument(text string) ([]byte, error) {
	text = stripAiderChatter(text)
	candidates := fencedYAMLBlocks(text)
	// Scan for inline YAML blocks that agents emit without markdown fences
	// (common with Aider/Ollama CLI output). Prefer the answer region (after
	// the last "► ANSWER" marker) to avoid picking up YAML-like content from
	// the model's thinking/reasoning section, which can contain handoff keys
	// quoted out of context. Fenced, inline, and raw candidates are evaluated
	// together by source position so a trailing raw handoff attached after an
	// agent prompt's sample fenced YAML wins.
	answerText, answerOffset := answerRegionWithOffset(text)
	inlineBlocks := scanInlineYAMLBlocks(normalizeForScan(answerText))
	if len(inlineBlocks) == 0 {
		// Fall back to the full text when no answer marker is present
		// (e.g. sidecar files, non-Aider runners).
		inlineBlocks = scanInlineYAMLBlocks(normalizeForScan(text))
	} else {
		for i := range inlineBlocks {
			inlineBlocks[i].position += answerOffset
		}
	}
	candidates = append(candidates, inlineBlocks...)
	candidates = append(candidates, handoffRawYAMLCandidate(text)...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].position < candidates[j].position
	})
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := normalizeHandoffYAML([]byte(strings.TrimSpace(candidates[i].text)))
		var decoded map[string]interface{}
		if err := unmarshalYAMLMap(candidate, &decoded); err == nil {
			if hasKnownHandoffKey(decoded) {
				return candidate, nil
			}
			continue
		}
		if looksLikeHandoffYAML(candidate) {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("missing yaml document for output contract")
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
		errs = append(errs, requireNumberField(summary, contract, "agent_instructions_update_score")...)
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
	normalized := strings.ToLower(strings.TrimSpace(verdict))
	if qaBlockingVerdictIsOnlyEmbeddedRepoInformationalWarning(normalized, handoff.Summary) {
		return ""
	}
	return normalized
}

func qaBlockingVerdictIsOnlyEmbeddedRepoInformationalWarning(verdict string, summary map[string]interface{}) bool {
	switch verdict {
	case "failed", "fail", "blocked", "needs-human-review", "needs_human_review":
	default:
		return false
	}
	data, err := yaml.Marshal(summary)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	return embeddedRepoInformationalOnlyBlock("", lines)
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

func parseAgentInstructionsUpdateRecommendationScore(handoffPath string) int {
	if handoff, err := loadPhaseHandoff(handoffPath); err == nil && handoff.Summary != nil && handoff.ValidationStatus != "invalid" {
		if score, ok := numericValueAsIntInRange(handoff.Summary["agent_instructions_update_score"], 0, 10); ok {
			return score
		}
	}
	return -1
}

func extractHandoffYAML(text string) ([]byte, bool) {
	text = stripAiderChatter(text)
	candidates := fencedYAMLBlocks(text)
	// Prefer the answer region for inline blocks to avoid picking up
	// YAML-like content from the model's thinking/reasoning section.
	answerText, answerOffset := answerRegionWithOffset(text)
	inlineBlocks := scanInlineYAMLBlocks(normalizeForScan(answerText))
	if len(inlineBlocks) == 0 {
		inlineBlocks = scanInlineYAMLBlocks(normalizeForScan(text))
	} else {
		for i := range inlineBlocks {
			inlineBlocks[i].position += answerOffset
		}
	}
	candidates = append(candidates, inlineBlocks...)
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
	for _, key := range handoffKeyPrefixes {
		if _, ok := decoded[key]; ok {
			return true
		}
	}
	return false
}

func looksLikeHandoffYAML(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if isHandoffKeyLine(line) {
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

// normalizeForScan applies the line-level normalizations (bullet conversion
// and merged-key splitting) to a log prior to inline-block scanning. Splitting
// collapsed keys first ensures block-scalar indicators land on their own line,
// so scanInlineYAMLBlocks can track flattened block-scalar content and capture
// the full document instead of stopping at the first column-0 content line.
func normalizeForScan(text string) string {
	return string(normalizeMergedKeys(normalizeBulletedYAML([]byte(text))))
}

// normalizeBulletedYAML replaces Unicode bullet characters (•, U+2022) that
// appear as the first non-whitespace on a line with the YAML list marker "- ".
// Agents running through the Aider CLI often use "• " instead of "- " for
// list items. The YAML parser accepts "• text" as a plain scalar string rather
// than a list item, so without this normalization the resulting values are
// strings instead of arrays, failing contract validation.
func normalizeBulletedYAML(data []byte) []byte {
	bullet := "\u2022" // • (U+2022 BULLET)
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		content := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(content, bullet) {
			continue
		}
		leading := line[:len(line)-len(content)]
		afterBullet := content[len(bullet):]
		afterBullet = strings.TrimLeft(afterBullet, " \t")
		lines[i] = leading + "- " + afterBullet
	}
	return []byte(strings.Join(lines, "\n"))
}

// compoundHandoffKeys are the multi-word handoff schema keys whose names are
// distinctive enough that they will not appear as ordinary prose inside a
// block-scalar value. They are used to detect a top-level handoff key that an
// agent has appended to the end of a flattened block-scalar content line
// (e.g. "...complete and passing. next_cycle_focus: []"). The short generic
// keys (scope, risks, verdict, score, reason, decisions) are intentionally
// excluded because they can occur naturally in prose.
var compoundHandoffKeys = []string{
	"non_goals", "target_paths", "acceptance_map",
	"changed_files", "implemented_behavior", "checks_run",
	"criteria_results", "reviewed_files", "failing_checks",
	"required_fixes", "next_cycle_focus",
}

// trailingHandoffKeyRe matches a compound handoff key with an empty flow
// collection value ("[]" or "{}") that an agent has appended to the end of a
// block-scalar content line (e.g. "...passing. next_cycle_focus: []"). The
// value is restricted to empty collections so ordinary prose mentioning a
// handoff key (e.g. "... next_cycle_focus: none") is never split out of
// block-scalar content. The leading whitespace requirement avoids matching a
// key that begins a line (those are handled as structural lines).
var trailingHandoffKeyRe = regexp.MustCompile(`[ \t](` + strings.Join(compoundHandoffKeys, "|") + `):[ \t]*(\[\]|\{\})$`)

// normalizeMergedKeys splits lines where an agent has collapsed several YAML
// mapping keys onto a single line back into one key per line, and moves
// block-scalar content that shares a line with its "|" / ">" indicator onto
// its own line. Agents running through the Aider CLI (notably with
// ollama/glm-5.2:cloud) frequently emit handoff YAML as if it were a flat
// label list, producing lines such as:
//
//	verdict: approved criteria_results:
//	  - criterion: "..." result: pass notes: "..."
//	  - "internal/foo.go" reviewed_files: []   (last list item + next key)
//	score: 2 verdict: approved reason: | The latest cycle report ...
//
// It also tracks double-quoted scalars that span multiple lines: when a quote
// closes mid-line, any keys appended after it (e.g. "notes: ..." on the line
// where a criterion's quoted value finally closes) are split off with the
// correct indentation. The function is block-scalar aware so it never splits
// keys out of genuine block-scalar content except when an agent has appended a
// trailing top-level handoff key to a content line. It must run after
// normalizeBulletedYAML (so "• " has become "- ") and before
// normalizeFlattenedBlockScalars (so block-scalar indicators are on their own
// lines and flattened content can be re-indented).
func normalizeMergedKeys(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+8)
	inBlockScalar := false
	inQuote := false
	quoteIndent := 0
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		content := strings.TrimLeft(line, " ")
		currentIndent := len(line) - len(content)
		hasIndent := currentIndent > 0

		if inBlockScalar {
			// A handoff key at column 0 terminates the block scalar and is
			// processed as a structural line below.
			if !hasIndent && isHandoffKeyLine(line) {
				inBlockScalar = false
			} else {
				// Block-scalar content. An agent may have appended a trailing
				// top-level handoff key to this content line; split it off so
				// the key is not swallowed into the scalar value.
				if before, key, ok := splitTrailingHandoffKey(content); ok {
					out = append(out, strings.Repeat(" ", currentIndent)+before)
					out = append(out, key)
					inBlockScalar = false
					continue
				}
				out = append(out, line)
				continue
			}
		}

		if content == "" {
			out = append(out, line)
			continue // a blank line does not close an open quote
		}

		if inQuote {
			pos, found := findClosingQuote(content)
			if !found {
				out = append(out, line)
				continue // scalar continues onto further lines
			}
			// The quote closes at pos. Keep the head (through the closing
			// quote) on this line; the remainder may hold appended keys.
			head := content[:pos+1]
			out = append(out, strings.Repeat(" ", currentIndent)+head)
			inQuote = false
			remainder := strings.TrimLeft(content[pos+1:], " \t")
			if remainder == "" {
				continue
			}
			remLines, opened, qIndent, decBlock := splitMergedFragment(remainder, quoteIndent)
			out = append(out, remLines...)
			inQuote = opened
			quoteIndent = qIndent
			inBlockScalar = decBlock
			continue
		}

		// Normal line: only structural lines (mapping keys or sequence items)
		// can contain collapsed keys. Block-scalar content is handled above.
		isSeq := strings.HasPrefix(content, "- ") || content == "-"
		isStructural := isInlineYAMLStructuralLine(line) || isSeq
		if !isStructural {
			out = append(out, line)
			continue
		}
		split, opened, qIndent, decBlock := splitMergedLine(line, currentIndent, isSeq)
		out = append(out, split...)
		inQuote = opened
		quoteIndent = qIndent
		inBlockScalar = decBlock
	}
	return []byte(strings.Join(out, "\n"))
}

// splitTrailingHandoffKey checks whether a block-scalar content line ends
// with a compound handoff key (e.g. "next_cycle_focus: []") and, if so,
// returns the prose prefix and the "key: value" text to emit on its own line
// at column 0.
func splitTrailingHandoffKey(content string) (before, key string, ok bool) {
	loc := trailingHandoffKeyRe.FindStringSubmatchIndex(content)
	if loc == nil {
		return "", "", false
	}
	matchStart := loc[0]
	before = strings.TrimRight(content[:matchStart], " \t")
	key = strings.TrimSpace(content[loc[0]:])
	return before, key, true
}

// splitMergedLine rewrites a single structural YAML line that may contain
// several collapsed mapping keys into one key per line. It returns the
// rewritten lines, whether the last value is an open double-quoted scalar
// (and the indentation of its key, for splitting keys appended after the
// quote closes on a later line), and whether the last key declares a block
// scalar.
func splitMergedLine(line string, indent int, isSeq bool) ([]string, bool, int, bool) {
	content := strings.TrimLeft(line, " ")
	rest := content
	prefix := strings.Repeat(" ", indent)
	if isSeq {
		if strings.HasPrefix(rest, "- ") {
			rest = rest[2:]
			prefix += "- "
		} else if rest == "-" {
			return []string{line}, false, 0, false
		}
	}
	firstKeyCol := len(prefix)
	segs := parseMergedSegments(rest)
	if len(segs) == 0 {
		return []string{line}, false, 0, false
	}
	// A single clean segment (no merge, no open quote, no block-scalar trailing
	// content) is preserved verbatim to avoid spurious whitespace changes.
	if len(segs) == 1 && !segs[0].blockInd && segs[0].trailing == "" && !segs[0].valueOpen {
		return []string{line}, false, 0, false
	}
	firstIsScalar := !segs[0].isKey
	return emitMergedSegments(segs, prefix, firstKeyCol, firstIsScalar)
}

// splitMergedFragment processes a fragment of appended keys (the text left on a
// line after a multi-line double-quoted scalar closes). contentIndent is the
// indentation of the mapping the scalar belonged to; sub-keys are aligned to
// it, while top-level handoff keys are emitted at column 0.
func splitMergedFragment(rest string, contentIndent int) ([]string, bool, int, bool) {
	segs := parseMergedSegments(rest)
	if len(segs) == 0 {
		return nil, false, 0, false
	}
	firstPrefix := strings.Repeat(" ", contentIndent)
	if segs[0].isKey && isHandoffKeyName(segs[0].keyName) {
		firstPrefix = "" // a top-level handoff key starts at column 0
	}
	return emitMergedSegments(segs, firstPrefix, contentIndent, false)
}

// emitMergedSegments renders parsed segments as one line per key. firstPrefix
// is prepended to the first emitted line (it carries the original "- " marker
// for sequence items); firstKeyCol is the indentation used for non-top-level
// sub-keys; firstIsScalar indicates the first segment is a bare sequence
// scalar value (so any following keys are new top-level keys).
func emitMergedSegments(segs []mergedSegment, firstPrefix string, firstKeyCol int, firstIsScalar bool) ([]string, bool, int, bool) {
	var out []string
	openedQuote := false
	quoteIndent := 0
	declaresBlock := false
	for i, seg := range segs {
		var text string
		var emitIndent int
		if i == 0 {
			text = firstPrefix + seg.text
			emitIndent = firstKeyCol
		} else {
			switch {
			case firstIsScalar:
				text = seg.text
				emitIndent = 0
			case seg.isKey && isHandoffKeyName(seg.keyName):
				text = seg.text
				emitIndent = 0
			default:
				text = strings.Repeat(" ", firstKeyCol) + seg.text
				emitIndent = firstKeyCol
			}
		}
		out = append(out, text)
		if seg.blockInd && seg.trailing != "" {
			out = append(out, seg.trailing)
		}
		declaresBlock = seg.blockInd
		openedQuote = seg.valueOpen
		if seg.valueOpen {
			quoteIndent = emitIndent
		}
	}
	return out, openedQuote, quoteIndent, declaresBlock
}

// mergedSegment describes one key/value (or scalar) parsed from a collapsed
// line.
type mergedSegment struct {
	text      string // the segment text (e.g. "key: value")
	isKey     bool   // true for a "key: value" mapping entry
	keyName   string // the key name when isKey
	blockInd  bool   // true if the value is a "|" / ">" block-scalar indicator
	trailing  string // block-scalar same-line content (column 0) after an indicator
	valueOpen bool   // true if a double-quoted value is not closed on this line
}

// parseMergedSegments tokenises the content of a collapsed line (after leading
// whitespace and an optional "- " marker) into ordered key/value segments.
func parseMergedSegments(rest string) []mergedSegment {
	var segs []mergedSegment
	i := 0
	n := len(rest)
	for {
		for i < n && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		// Quoted/flow value, or the start of a (possibly multi-word) bare
		// scalar value: collect all consecutive non-key tokens into one
		// segment so sequence items like "- implement parser" stay intact.
		if rest[i] == '"' || rest[i] == '[' || rest[i] == '{' {
			text, _, _, open := collectValue(rest, &i)
			seg := mergedSegment{text: text, valueOpen: open}
			segs = append(segs, seg)
			if open {
				break // value continues on subsequent lines; stop here
			}
			continue
		}
		// Bare token: read until whitespace. A token ending with ":" is a key.
		j := i
		for j < n && rest[j] != ' ' && rest[j] != '\t' {
			j++
		}
		token := rest[i:j]
		if strings.HasSuffix(token, ":") && len(token) > 1 {
			keyName := token[:len(token)-1]
			i = j
			valueText, blockInd, trailing, open := collectValue(rest, &i)
			seg := mergedSegment{isKey: true, keyName: keyName, valueOpen: open}
			if blockInd {
				seg.text = token + " " + valueText
				seg.blockInd = true
				seg.trailing = trailing
			} else {
				seg.text = token
				if valueText != "" {
					seg.text += " " + valueText
				}
			}
			segs = append(segs, seg)
			if open || blockInd {
				break // open quote continues later; block indicator ends the line
			}
			continue
		}
		// Bare scalar value: collect the whole (possibly multi-word) value.
		text, _, _, open := collectValue(rest, &i)
		segs = append(segs, mergedSegment{text: text, valueOpen: open})
		if open {
			break
		}
	}
	return segs
}

// collectValue reads the value tokens following a key (or starting a bare
// scalar) until the next key token or end of line. It returns the joined value
// text, whether the value is a block-scalar indicator, any same-line content
// trailing the indicator, and whether a quoted value is left open (continues
// on later lines). i is advanced past the consumed tokens.
func collectValue(rest string, i *int) (string, bool, string, bool) {
	n := len(rest)
	var parts []string
	for {
		for *i < n && (rest[*i] == ' ' || rest[*i] == '\t') {
			*i++
		}
		if *i >= n {
			break
		}
		if rest[*i] == '"' {
			end, closed := scanDoubleQuoted(rest, *i)
			parts = append(parts, rest[*i:end])
			*i = end
			if !closed {
				return strings.Join(parts, " "), false, "", true
			}
			continue
		}
		if rest[*i] == '[' || rest[*i] == '{' {
			end, _ := scanFlowCollection(rest, *i)
			parts = append(parts, rest[*i:end])
			*i = end
			continue
		}
		j := *i
		for j < n && rest[j] != ' ' && rest[j] != '\t' {
			j++
		}
		token := rest[*i:j]
		// A token ending with ":" is the start of the next key: stop here.
		if strings.HasSuffix(token, ":") && len(token) > 1 {
			break
		}
		*i = j
		if isBlockScalarIndicator(token) {
			// Remaining same-line content after the indicator is block-scalar
			// content flattened onto the indicator line.
			trailing := ""
			for *i < n && (rest[*i] == ' ' || rest[*i] == '\t') {
				*i++
			}
			if *i < n {
				trailing = rest[*i:]
				*i = n
			}
			return token, true, trailing, false
		}
		parts = append(parts, token)
	}
	return strings.Join(parts, " "), false, "", false
}

// scanDoubleQuoted returns the index just past the closing double quote and
// whether the quote was closed on this line.
func scanDoubleQuoted(s string, start int) (int, bool) {
	j := start + 1
	for j < len(s) {
		switch s[j] {
		case '\\':
			j += 2
		case '"':
			return j + 1, true
		default:
			j++
		}
	}
	return j, false
}

// scanFlowCollection returns the index just past the closing "]" or "}".
func scanFlowCollection(s string, start int) (int, bool) {
	close := byte(']')
	if s[start] == '{' {
		close = '}'
	}
	j := start + 1
	for j < len(s) {
		if s[j] == close {
			return j + 1, true
		}
		j++
	}
	return j, false
}

// isBlockScalarIndicator reports whether a bare token is a YAML block-scalar
// indicator ("|" or ">") optionally followed by chomping/indent modifiers.
func isBlockScalarIndicator(token string) bool {
	if token == "" || (token[0] != '|' && token[0] != '>') {
		return false
	}
	for _, r := range token[1:] {
		if r != '-' && r != '+' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// findClosingQuote returns the index of the first unescaped double quote in s,
// i.e. the closing quote of a double-quoted scalar whose opening quote was on
// a previous line. Returns false if not found.
func findClosingQuote(s string) (int, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return i, true
		}
	}
	return 0, false
}

// isHandoffKeyName reports whether name is a known top-level handoff schema
// key.
func isHandoffKeyName(name string) bool {
	for _, key := range handoffKeyPrefixes {
		if key == name {
			return true
		}
	}
	return false
}

// blockScalarContentIndent is the number of indentation spaces added beyond
// the block-scalar key's own indentation when re-indenting flattened content.
const blockScalarContentIndent = 2

// declaresBlockScalar reports whether a YAML line introduces a block scalar
// value using the literal (|) or folded (>) indicator, optionally followed by
// chomping/indentation modifiers (e.g. |-, >+, |2, |-2). Both mapping values
// (key: |) and sequence items (- |) are recognised. When true, subsequent
// non-structural lines are block-scalar content that Aider may flatten to
// column 0, which the inline scanner and normalizer must account for.
func declaresBlockScalar(line string) bool {
	content := strings.TrimSpace(line)
	if content == "" {
		return false
	}
	// Strip a leading sequence marker "- ".
	if strings.HasPrefix(content, "- ") {
		content = strings.TrimSpace(content[2:])
	} else if content == "-" {
		return false
	}
	// For mapping keys, strip the "key:" prefix.
	if idx := strings.Index(content, ":"); idx >= 0 {
		content = strings.TrimSpace(content[idx+1:])
	}
	if content == "" {
		return false
	}
	if content[0] != '|' && content[0] != '>' {
		return false
	}
	// After the indicator only chomping (-, +) and indentation digits are valid.
	rest := content[1:]
	for _, r := range rest {
		if r != '-' && r != '+' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// isInlineYAMLStructuralLine reports whether a line is a YAML structural line
// (a mapping key or a sequence item) as opposed to plain scalar content. It is
// used by the inline scanner to decide whether an indented line should update
// the block-scalar tracking flag.
func isInlineYAMLStructuralLine(line string) bool {
	content := strings.TrimLeft(line, " \t")
	if content == "" {
		return false
	}
	// Sequence item: "- " or "-".
	if strings.HasPrefix(content, "- ") || content == "-" {
		return true
	}
	// Mapping key: "key:" or "key: value" where the key part contains no
	// whitespace (valid for unquoted YAML plain keys).
	if idx := strings.Index(content, ":"); idx > 0 {
		keyPart := content[:idx]
		if !strings.ContainsAny(keyPart, " \t") {
			return true
		}
	}
	return false
}

// normalizeFlattenedBlockScalars re-indents block scalar content (| and >
// indicators) that Aider's CLI display has flattened. A YAML block scalar
// requires its content lines to be indented more than the key; when Aider
// strips that indentation the YAML parser cannot parse the document. This
// function detects key/list lines whose value is a block scalar indicator and
// re-indents subsequent non-structural content lines until the next structural
// line (key or list item). It handles two flattening patterns produced by
// Aider's CLI display:
//   - Content collapsed to column 0 (top-level block scalars).
//   - Content collapsed to the same indentation as the key (nested block
//     scalars inside list items or mappings, e.g. notes: | inside
//     criteria_results).
func normalizeFlattenedBlockScalars(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	inBlockScalar := false
	blockScalarKeyIndent := 0
	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		content := strings.TrimLeft(trimmedRight, " \t")
		currentIndent := len(trimmedRight) - len(content)
		hasIndent := currentIndent > 0

		if content == "" {
			out = append(out, line)
			continue
		}

		if !hasIndent {
			// Line at column 0 (no leading whitespace).
			if isHandoffKeyLine(trimmedRight) {
				inBlockScalar = declaresBlockScalar(trimmedRight)
				if inBlockScalar {
					blockScalarKeyIndent = 0
				}
				out = append(out, trimmedRight)
				continue
			}
			if strings.HasPrefix(content, "- ") || content == "-" {
				inBlockScalar = declaresBlockScalar(content)
				if inBlockScalar {
					blockScalarKeyIndent = 0
				}
				out = append(out, trimmedRight)
				continue
			}
			// Non-key, non-list line at column 0.
			if inBlockScalar {
				// Flattened block-scalar content: re-indent so the YAML parser
				// treats it as block-scalar content rather than a new
				// top-level node.
				needed := blockScalarKeyIndent + blockScalarContentIndent
				out = append(out, strings.Repeat(" ", needed)+content)
				continue
			}
			out = append(out, line)
			continue
		}

		// Indented line: update the block-scalar flag when the line is a
		// structural line (key or list item). Plain content lines leave the
		// flag unchanged so block-scalar content does not prematurely reset it.
		if isInlineYAMLStructuralLine(trimmedRight) {
			inBlockScalar = declaresBlockScalar(trimmedRight)
			if inBlockScalar {
				blockScalarKeyIndent = currentIndent
			}
			out = append(out, line)
			continue
		}
		// Non-structural indented line. When in a block scalar and the
		// content's indentation is at or below the key's, Aider has flattened
		// it. Re-indent so the YAML parser treats it as block-scalar content.
		if inBlockScalar && currentIndent <= blockScalarKeyIndent {
			needed := blockScalarKeyIndent + blockScalarContentIndent
			out = append(out, strings.Repeat(" ", needed)+content)
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// normalizeHandoffYAML applies the full normalization pipeline for handoff YAML
// documents extracted from agent output logs: first converting Unicode bullet
// characters to YAML list markers, then re-indenting block-scalar content that
// Aider's CLI display has flattened (to column 0 or to the key's own indentation).
func normalizeHandoffYAML(data []byte) []byte {
	return normalizeFlattenedBlockScalars(normalizeMergedKeys(normalizeBulletedYAML(data)))
}

// handoffKeyPrefixes lists all YAML keys that are part of the known handoff
// schemas. It is used to detect the start of an inline YAML block within a
// log file.
var handoffKeyPrefixes = []string{
	"scope", "non_goals", "target_paths", "acceptance_map",
	"changed_files", "implemented_behavior", "checks_run", "decisions", "risks",
	"verdict", "criteria_results", "reviewed_files", "failing_checks", "required_fixes",
	"score", "agent_instructions_update_score", "reason", "next_cycle_focus",
}

// isHandoffKeyLine reports whether a line begins with a known handoff key
// followed by a colon at column 0 (no indentation). Trailing whitespace from
// Aider CLI display padding is stripped before checking.
func isHandoffKeyLine(line string) bool {
	trimmed := strings.TrimRight(line, " \t")
	for _, key := range handoffKeyPrefixes {
		if strings.HasPrefix(trimmed, key+":") {
			return true
		}
	}
	return false
}

// scanInlineYAMLBlocks scans a log file for contiguous YAML document blocks
// that are not wrapped in markdown fences. Agents running through the Aider
// CLI often emit the YAML handoff inline at the end of their output without
// fence delimiters. The scanner identifies regions that begin with a known
// handoff key at column 0 and extend through subsequent indented lines, blank
// lines, and additional key lines until a non-YAML line is encountered.
// Trailing whitespace from Aider CLI display padding is stripped from every
// line before scanning.
func scanInlineYAMLBlocks(text string) []handoffDocumentCandidate {
	lines := strings.Split(text, "\n")
	// Right-trim each line to remove Aider CLI padding.
	trimmedLines := make([]string, len(lines))
	for i, line := range lines {
		trimmedLines[i] = strings.TrimRight(line, " \t")
	}

	var candidates []handoffDocumentCandidate
	i := 0
	for i < len(trimmedLines) {
		if !isHandoffKeyLine(trimmedLines[i]) {
			i++
			continue
		}
		// Found a key line — extend the block forward.
		blockStart := i
		j := i + 1
		inBlockScalar := declaresBlockScalar(trimmedLines[i])
		for j < len(trimmedLines) {
			line := trimmedLines[j]
			if line == "" {
				// Blank line: check if the next non-blank line is still YAML.
				k := j + 1
				for k < len(trimmedLines) && trimmedLines[k] == "" {
					k++
				}
				if k >= len(trimmedLines) {
					break
				}
				next := trimmedLines[k]
				if isHandoffKeyLine(next) {
					j = k
					continue
				}
				if inBlockScalar {
					// In block-scalar mode, a blank line followed by non-key
					// content is part of the block scalar: Aider flattens
					// block-scalar content to column 0, so the content is not
					// indented and would otherwise be mistaken for prose.
					j = k
					continue
				}
				if len(next) > 0 && (next[0] == ' ' || next[0] == '\t') {
					j = k
					continue
				}
				break
			}
			if isHandoffKeyLine(line) {
				inBlockScalar = declaresBlockScalar(line)
				j++
				continue
			}
			if line[0] == ' ' || line[0] == '\t' {
				// Indented line: update the block-scalar flag when the line is
				// a structural line (key or list item) so nested block scalars
				// are tracked. Plain content lines leave the flag unchanged.
				if isInlineYAMLStructuralLine(line) {
					inBlockScalar = declaresBlockScalar(line)
				}
				j++
				continue
			}
			// Non-indented, non-key line at column 0.
			if inBlockScalar {
				// Block-scalar content flattened to column 0 by Aider.
				j++
				continue
			}
			break
		}
		blockText := strings.Join(trimmedLines[blockStart:j], "\n")
		pos := 0
		for k := 0; k < blockStart; k++ {
			pos += len(lines[k]) + 1
		}
		candidates = append(candidates, handoffDocumentCandidate{position: pos, text: blockText})
		i = j
	}
	return candidates
}

func parseHandoffYAMLDocument(candidate string) ([]byte, map[string]interface{}, bool) {
	var decoded map[string]interface{}
	normalized := normalizeHandoffYAML([]byte(strings.TrimSpace(candidate)))
	if err := unmarshalYAMLMap(normalized, &decoded); err != nil {
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

// answerRegion returns the portion of a runner output log that contains the
// model's actual answer, stripping preceding CLI chrome and reasoning. Aider
// separates its output with "► ANSWER" (and "► THINKING") section markers; the
// answer is the text after the last "► ANSWER" marker. When no marker is
// present (for example non-Aider runners or plain logs), the full text is
// returned so behaviour degrades gracefully.
func answerRegion(text string) string {
	answer, _ := answerRegionWithOffset(text)
	return answer
}

func answerRegionWithOffset(text string) (string, int) {
	lines := strings.Split(text, "\n")
	lastAnswer := -1
	offset := 0
	answerOffset := 0
	for i, line := range lines {
		if strings.Contains(strings.TrimSpace(line), "► ANSWER") {
			lastAnswer = i
			answerOffset = offset + len(line) + 1
		}
		offset += len(line) + 1
	}
	if lastAnswer < 0 {
		return text, 0
	}
	return strings.Join(lines[lastAnswer+1:], "\n"), answerOffset
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
