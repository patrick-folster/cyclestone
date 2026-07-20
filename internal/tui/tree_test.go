package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestRenderTree_FullHierarchy(t *testing.T) {
	plans := []config.Plan{
		{
			ID:     "plan-alpha",
			Title:  "Alpha Architecture Plan",
			Status: "active",
			Briefings: []config.Briefing{
				{
					ID:          "b1",
					Title:       "Setup Core Module",
					Status:      "completed",
					MilestoneID: "ms-001",
				},
				{
					ID:          "b2",
					Title:       "API Integration",
					Status:      "in_progress",
					DependsOn:   []string{"b1"},
					MilestoneID: "ms-002",
				},
				{
					ID:     "b3",
					Title:  "Documentation",
					Status: "todo",
				},
				{
					ID:          "b4",
					Title:       "Legacy Link",
					Status:      "todo",
					MilestoneID: "ms-missing-404",
				},
			},
		},
	}

	milestones := []config.Milestone{
		{
			ID:     "ms-001",
			Title:  "Core Module Milestone",
			Status: "Done",
			Cycles: 2,
		},
		{
			ID:     "ms-002",
			Title:  "API Milestone",
			Status: "In Progress",
			Cycles: 1,
		},
		{
			ID:     "ms-standalone",
			Title:  "Standalone Unreferenced Milestone",
			Status: "Todo",
			Cycles: 0,
		},
	}

	state := &config.State{
		ActiveMilestoneID: "ms-002",
		MilestoneStatuses: map[string]string{
			"ms-001": "Done",
			"ms-002": "In Progress",
		},
		MilestoneCycles: map[string]int{
			"ms-001": 2,
			"ms-002": 1,
		},
		History: map[string][]config.MilestoneCycleLog{
			"ms-001": {
				{
					CycleNumber: 1,
					Status:      "approved",
					Duration:    "2m15s",
					UserNote:    "Initial setup",
					Timestamp:   time.Now(),
				},
				{
					CycleNumber: 2,
					Status:      "approved",
					Duration:    "1m45s",
					UserNote:    "Final polish",
					Timestamp:   time.Now(),
				},
			},
			"ms-002": {
				{
					CycleNumber: 1,
					Status:      "in_progress",
					Duration:    "5m00s",
					UserNote:    "Running cycle 1",
					Timestamp:   time.Now(),
				},
			},
		},
	}

	// 1. Unicode rendering
	t.Setenv("TERM_PROGRAM", "")
	optsUnicode := TreeOptions{UseASCII: false}
	outUnicode := RenderTree(plans, milestones, state, optsUnicode)

	if !strings.Contains(outUnicode, "Milestone Planner") {
		t.Errorf("expected output to contain 'Milestone Planner', got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "Plan: plan-alpha - Alpha Architecture Plan") {
		t.Errorf("expected output to contain plan title, got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "[linked: ms-001]") {
		t.Errorf("expected briefing b1 to be tagged [linked: ms-001], got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "(depends on: b1)") {
		t.Errorf("expected briefing b2 to list dependency, got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "[unlinked]") {
		t.Errorf("expected unlinked briefing b3 to have [unlinked], got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "[missing: ms-missing-404]") {
		t.Errorf("expected missing briefing b4 to have [missing: ms-missing-404], got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "Milestone: ms-002 - API Milestone") {
		t.Errorf("expected linked milestone ms-002, got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "[active]") {
		t.Errorf("expected active milestone ms-002 to have [active] tag, got:\n%s", outUnicode)
	}
	if !strings.Contains(outUnicode, "Cycle 1: [approved] (2m15s) - Initial setup") {
		t.Errorf("expected cycle 1 log under ms-001, got:\n%s", outUnicode)
	}

	// Standalone milestone check
	if strings.Contains(outUnicode, "ms-standalone") {
		t.Errorf("standalone milestone 'ms-standalone' should be excluded from Planner tree view, but was found:\n%s", outUnicode)
	}

	// Unicode box drawing characters check
	if !strings.Contains(outUnicode, "├── ") && !strings.Contains(outUnicode, "└── ") {
		t.Errorf("expected Unicode branch characters in non-ASCII mode, got:\n%s", outUnicode)
	}

	// 2. ASCII rendering
	optsASCII := TreeOptions{UseASCII: true}
	outASCII := RenderTree(plans, milestones, state, optsASCII)

	if !strings.Contains(outASCII, "|-- ") && !strings.Contains(outASCII, "\\-- ") {
		t.Errorf("expected ASCII branch characters in ASCII mode, got:\n%s", outASCII)
	}
	if strings.Contains(outASCII, "├── ") || strings.Contains(outASCII, "└── ") {
		t.Errorf("did not expect Unicode box characters in ASCII mode, got:\n%s", outASCII)
	}

	// 3. VS Code environment safeguard test
	t.Setenv("TERM_PROGRAM", "vscode")
	outVSCode := RenderTree(plans, milestones, state, TreeOptions{})
	if !strings.Contains(outVSCode, "|-- ") && !strings.Contains(outVSCode, "\\-- ") {
		t.Errorf("expected ASCII branch characters in VS Code environment, got:\n%s", outVSCode)
	}
}

func TestRenderTree_NarrowWidthTruncation(t *testing.T) {
	plans := []config.Plan{
		{
			ID:     "plan-very-long-id-that-exceeds-narrow-columns",
			Title:  "Super Long Plan Title Describing Architecture In Great Detail",
			Status: "active",
			Briefings: []config.Briefing{
				{
					ID:          "briefing-long-id",
					Title:       "Detailed Briefing Objective Specification Document",
					Status:      "todo",
					MilestoneID: "ms-long",
				},
			},
		},
	}

	milestones := []config.Milestone{
		{
			ID:    "ms-long",
			Title: "Milestone With Extremely Long Descriptive Title",
		},
	}

	optsNarrow := TreeOptions{
		UseASCII: true,
		MaxWidth: 40,
	}

	out := RenderTree(plans, milestones, nil, optsNarrow)
	lines := strings.Split(strings.TrimSpace(out), "\n")

	for _, line := range lines {
		runeCount := len([]rune(line))
		if runeCount > 40 {
			t.Errorf("line exceeds MaxWidth 40 (got %d runes): %q", runeCount, line)
		}
	}
}

func TestRenderTree_PlanFilter(t *testing.T) {
	plans := []config.Plan{
		{ID: "p1", Title: "Plan One"},
		{ID: "p2", Title: "Plan Two"},
	}

	out := RenderTree(plans, nil, nil, TreeOptions{PlanID: "p2"})

	if strings.Contains(out, "Plan One") {
		t.Errorf("expected Plan One to be filtered out, got:\n%s", out)
	}
	if !strings.Contains(out, "Plan Two") {
		t.Errorf("expected Plan Two to be rendered, got:\n%s", out)
	}
}

func TestRenderTree_EmptyPlans(t *testing.T) {
	out := RenderTree(nil, nil, nil, TreeOptions{UseASCII: true})
	if !strings.Contains(out, "Milestone Planner") {
		t.Errorf("expected 'Milestone Planner' root, got:\n%s", out)
	}
	if !strings.Contains(out, "(no plans)") {
		t.Errorf("expected '(no plans)' child node, got:\n%s", out)
	}
}
