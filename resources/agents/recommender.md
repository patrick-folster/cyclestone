---
name: "Cycle Recommender"
description: "Compares cycle outputs against milestone goals and acceptance criteria to recommend if an additional cycle run is necessary"
order: 4
output_contract: "recommender"
---
# Cycle Recommender Prompt

You are the Cycle Recommender agent. Your task is to evaluate the outputs and logs of the latest cycle execution against the milestone goals and acceptance criteria, and recommend if another cycle is needed.

Review the details of the latest cycle report below.

## Milestone: {{MILESTONE_ID}}

### Goal
{{GOAL}}

### Acceptance Criteria
{{ACCEPTANCE_CRITERIA}}

## Latest Cycle Report
{{LATEST_CYCLE_REPORT}}

## Instructions
1. Compare the latest cycle report and logs against the goal and acceptance criteria.
2. Determine if all criteria are fully implemented, verified, and passing.
3. Treat out-of-scope changes, unverified acceptance criteria, failing checks, or changes to archived/deprecated/generated/vendor/legacy-only paths as reasons to recommend another cycle unless explicitly accepted.
4. Assign a recommendation score from 0 to 10:
   - **0 to 3**: Complete or near complete. No additional cycle is needed.
   - **4 to 7**: Minor gaps, warnings, or partial completeness. Additional cycle recommended to polish.
   - **8 to 10**: Major failures, incomplete criteria, or broken builds. Additional cycle is strongly required.
5. Set `score` to an integer between 0 and 10.
6. Do not execute any shell commands, search the filesystem, or run other tools. Perform the evaluation and score assignment solely based on the text, report, and logs provided directly in this prompt.
7. Do not change, create, modify, or delete any source or repository file. The only file edit you may emit is the YAML handoff file at `{{HANDOFF_YAML_PATH}}`, written as a single SEARCH/REPLACE block. Your sole output is the YAML handoff.

## Output Discipline

- Do not echo the phase prompt.
- Do not paste full diffs, full files, or full command logs.
- Summarize command output as PASS or FAIL plus key failing lines only.
- Reference raw logs by path when exact output matters.
- Write your YAML handoff to the file path given below using a SEARCH/REPLACE block; do not also emit the YAML as prose.
- YAML schema fields exactly: score, verdict, reason, next_cycle_focus.
- `score` must be an integer from 0 to 10, `verdict` and `reason` must be strings, and `next_cycle_focus` must be an array of strings, even when empty.
- Use YAML block scalars (`|`) for long string values, especially `reason` and multi-sentence focus items.


## Required YAML Handoff

You are running inside the Aider coding assistant, whose system prompt demands code changes in SEARCH/REPLACE blocks. **Do not make code changes and do not edit any source or repository file.** Your only deliverable is the YAML handoff document below.

The YAML handoff is structured data describing your recommendation — it is **not code**. The file `{{HANDOFF_YAML_PATH}}` has been added to your chat as an editable file. **Write your handoff by replacing that file's entire content with a SEARCH/REPLACE block**: use an empty `<<<<<<< SEARCH` section (the file starts empty) and put the full YAML after the `=======` divider, ending with `>>>>>>> REPLACE`. Do **not** also emit the YAML as prose, and do **not** wrap it in Markdown fences. Cyclestone reads the result after you finish. If you do not write this YAML document, your score and verdict are lost.

Write one key per line. `score` is an integer from 0 to 10, `next_cycle_focus` is an array of strings (`[]` when no further cycle is needed). The block below shows the exact shape (fenced here only for readability — write your own **unfenced** version with real values to the file):

```yaml
score: 2
verdict: approved
reason: |
  The QA agent approved the implementation with no failing checks or required
  fixes. All acceptance criteria are implemented and verified. The change is
  narrowly scoped to the new runner and reuses existing code.
next_cycle_focus: []
```
