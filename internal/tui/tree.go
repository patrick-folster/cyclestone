package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/patrick-folster/cyclestone/internal/config"
)

// TreeOptions configures rendering behavior for planning hierarchy trees.
type TreeOptions struct {
	PlanID   string // If non-empty, filter tree to only this plan ID.
	UseASCII bool   // If true, force ASCII tree branch glyphs (|--, \--, |, " "). Auto-detected if TERM_PROGRAM == "vscode".
	MaxWidth int    // If > 0, truncate node text to fit within MaxWidth columns.
	Styled   bool   // If true, apply LipGloss styling. Default false (plain text for CLI).
	Styles   Styles // LipGloss styles to use if Styled is true.
}

type treeNode struct {
	text       string
	styledText string
	children   []*treeNode
}

// RenderTree generates a terminal-friendly string representation of the planning hierarchy.
func RenderTree(plans []config.Plan, milestones []config.Milestone, st *config.State, opts TreeOptions) string {
	msMap := make(map[string]config.Milestone, len(milestones))
	for _, ms := range milestones {
		msMap[ms.ID] = ms
	}

	var targetPlans []config.Plan
	if opts.PlanID != "" {
		for _, p := range plans {
			if p.ID == opts.PlanID {
				targetPlans = append(targetPlans, p)
				break
			}
		}
	} else {
		targetPlans = plans
	}

	rootText := "Milestone Planner"
	var rootStyled string
	if opts.Styled {
		rootStyled = opts.Styles.SectionTitle.Render(rootText)
	} else {
		rootStyled = rootText
	}

	root := &treeNode{
		text:       rootText,
		styledText: rootStyled,
	}

	if len(targetPlans) == 0 {
		emptyText := "(no plans)"
		var emptyStyled string
		if opts.Styled {
			emptyStyled = opts.Styles.SubtleText.Render(emptyText)
		} else {
			emptyStyled = emptyText
		}
		root.children = append(root.children, &treeNode{
			text:       emptyText,
			styledText: emptyStyled,
		})
	} else {
		for _, plan := range targetPlans {
			planNode := buildPlanNode(plan, msMap, st, opts)
			root.children = append(root.children, planNode)
		}
	}

	var sb strings.Builder
	renderTreeNode(&sb, root, "", true, true, opts)
	return sb.String()
}

func buildPlanNode(plan config.Plan, msMap map[string]config.Milestone, st *config.State, opts TreeOptions) *treeNode {
	completedCount := 0
	for _, b := range plan.Briefings {
		if b.Status == "completed" {
			completedCount++
		}
	}
	totalCount := len(plan.Briefings)

	title := plan.Title
	if title == "" {
		title = plan.ID
	}

	text := fmt.Sprintf("Plan: %s - %s [%s] (briefings: %d/%d)", plan.ID, title, formatStatus(plan.Status), completedCount, totalCount)
	if plan.Execution != nil {
		text += fmt.Sprintf(" [execution: %s]", plan.Execution.State)
	}

	var styled string
	if opts.Styled {
		styled = fmt.Sprintf("Plan: %s - %s %s %s",
			opts.Styles.AccentText.Render(plan.ID),
			title,
			renderStatusTag(opts.Styles, plan.Status),
			opts.Styles.SubtleText.Render(fmt.Sprintf("(briefings: %d/%d)", completedCount, totalCount)),
		)
		if plan.Execution != nil {
			styled += " " + opts.Styles.WarningText.Render(fmt.Sprintf("[execution: %s]", plan.Execution.State))
		}
	} else {
		styled = text
	}

	node := &treeNode{
		text:       text,
		styledText: styled,
	}

	for _, briefing := range plan.Briefings {
		briefingNode := buildBriefingNode(briefing, msMap, st, opts)
		node.children = append(node.children, briefingNode)
	}

	return node
}

func buildBriefingNode(briefing config.Briefing, msMap map[string]config.Milestone, st *config.State, opts TreeOptions) *treeNode {
	title := briefing.Title
	if title == "" {
		title = briefing.ID
	}

	var linkTag string
	var linkTagStyled string
	var linkedMS *config.Milestone

	if briefing.MilestoneID == "" {
		linkTag = "[unlinked]"
		if opts.Styled {
			linkTagStyled = opts.Styles.SubtleText.Render("[unlinked]")
		} else {
			linkTagStyled = linkTag
		}
	} else {
		if ms, ok := msMap[briefing.MilestoneID]; ok {
			msCopy := ms
			linkedMS = &msCopy
			linkTag = fmt.Sprintf("[linked: %s]", briefing.MilestoneID)
			if opts.Styled {
				linkTagStyled = opts.Styles.AccentText.Render(fmt.Sprintf("[linked: %s]", briefing.MilestoneID))
			} else {
				linkTagStyled = linkTag
			}
		} else {
			linkTag = fmt.Sprintf("[missing: %s]", briefing.MilestoneID)
			if opts.Styled {
				linkTagStyled = opts.Styles.ErrorText.Render(fmt.Sprintf("[missing: %s]", briefing.MilestoneID))
			} else {
				linkTagStyled = linkTag
			}
		}
	}

	var depsStr string
	var depsStrStyled string
	if len(briefing.DependsOn) > 0 {
		depsStr = fmt.Sprintf(" (depends on: %s)", strings.Join(briefing.DependsOn, ", "))
		if opts.Styled {
			depsStrStyled = opts.Styles.SubtleText.Render(depsStr)
		} else {
			depsStrStyled = depsStr
		}
	}

	text := fmt.Sprintf("Briefing: %s - %s [%s]%s %s", briefing.ID, title, formatStatus(briefing.Status), depsStr, linkTag)

	var styled string
	if opts.Styled {
		styled = fmt.Sprintf("Briefing: %s - %s %s%s %s",
			opts.Styles.DetailLabel.Render(briefing.ID),
			title,
			renderStatusTag(opts.Styles, briefing.Status),
			depsStrStyled,
			linkTagStyled,
		)
	} else {
		styled = text
	}

	node := &treeNode{
		text:       text,
		styledText: styled,
	}

	if linkedMS != nil {
		msNode := buildMilestoneNode(*linkedMS, st, opts)
		node.children = append(node.children, msNode)
	}

	return node
}

func buildMilestoneNode(ms config.Milestone, st *config.State, opts TreeOptions) *treeNode {
	status := ms.Status
	cycles := ms.Cycles
	var history []config.MilestoneCycleLog
	isActive := false

	if st != nil {
		if s, ok := st.MilestoneStatuses[ms.ID]; ok && s != "" {
			status = s
		}
		if c, ok := st.MilestoneCycles[ms.ID]; ok {
			cycles = c
		}
		if h, ok := st.History[ms.ID]; ok {
			history = h
		}
		if st.ActiveMilestoneID == ms.ID {
			isActive = true
		}
	}
	if status == "" {
		status = "Todo"
	}

	var activeTag string
	if isActive {
		activeTag = " [active]"
	}

	text := fmt.Sprintf("Milestone: %s - %s [%s] (cycles: %d)%s", ms.ID, ms.Title, status, cycles, activeTag)

	var styled string
	if opts.Styled {
		styled = fmt.Sprintf("Milestone: %s - %s %s %s",
			opts.Styles.DetailHeader.Render(ms.ID),
			ms.Title,
			renderStatusTag(opts.Styles, status),
			opts.Styles.SubtleText.Render(fmt.Sprintf("(cycles: %d)", cycles)),
		)
		if isActive {
			styled += " " + opts.Styles.WarningText.Render("[active]")
		}
	} else {
		styled = text
	}

	node := &treeNode{
		text:       text,
		styledText: styled,
	}

	for _, cycle := range history {
		cycleNode := buildCycleNode(cycle, opts)
		node.children = append(node.children, cycleNode)
	}

	return node
}

func buildCycleNode(cycle config.MilestoneCycleLog, opts TreeOptions) *treeNode {
	var durStr string
	if cycle.Duration != "" {
		durStr = fmt.Sprintf(" (%s)", cycle.Duration)
	}
	var noteStr string
	if cycle.UserNote != "" {
		noteStr = fmt.Sprintf(" - %s", cycle.UserNote)
	}

	text := fmt.Sprintf("Cycle %d: [%s]%s%s", cycle.CycleNumber, cycle.Status, durStr, noteStr)

	var styled string
	if opts.Styled {
		var statusStyle string
		switch cycle.Status {
		case "approved":
			statusStyle = opts.Styles.SuccessText.Render(fmt.Sprintf("[%s]", cycle.Status))
		case "failed", "blocked":
			statusStyle = opts.Styles.ErrorText.Render(fmt.Sprintf("[%s]", cycle.Status))
		default:
			statusStyle = opts.Styles.SubtleText.Render(fmt.Sprintf("[%s]", cycle.Status))
		}
		styled = fmt.Sprintf("Cycle %d: %s%s%s",
			cycle.CycleNumber,
			statusStyle,
			opts.Styles.SubtleText.Render(durStr),
			noteStr,
		)
	} else {
		styled = text
	}

	return &treeNode{
		text:       text,
		styledText: styled,
	}
}

func formatStatus(status string) string {
	if status == "" {
		return "todo"
	}
	return status
}

func renderStatusTag(styles Styles, status string) string {
	lower := strings.ToLower(status)
	switch lower {
	case "completed", "done", "approved":
		return styles.DoneTag.Render("[" + status + "]")
	case "in_progress", "in progress", "active":
		return styles.InProgressTag.Render("[" + status + "]")
	default:
		return styles.TodoTag.Render("[" + status + "]")
	}
}

func renderTreeNode(sb *strings.Builder, n *treeNode, prefix string, isLast bool, isRoot bool, opts TreeOptions) {
	useASCII := opts.UseASCII || os.Getenv("TERM_PROGRAM") == "vscode"

	var branch string
	if isRoot {
		branch = ""
	} else if useASCII {
		if isLast {
			branch = "\\-- "
		} else {
			branch = "|-- "
		}
	} else {
		if isLast {
			branch = "└── "
		} else {
			branch = "├── "
		}
	}

	fullPrefix := prefix + branch
	prefixWidth := len([]rune(fullPrefix))

	nodeText := n.text
	if opts.Styled {
		nodeText = n.styledText
	}

	if opts.MaxWidth > 0 {
		avail := opts.MaxWidth - prefixWidth
		if avail < 5 {
			avail = 5
		}
		if len([]rune(n.text)) > avail {
			if avail > 3 {
				truncatedRaw := string([]rune(n.text)[:avail-3]) + "..."
				nodeText = truncatedRaw
			} else {
				nodeText = string([]rune(n.text)[:avail])
			}
		}
	}

	sb.WriteString(fullPrefix)
	sb.WriteString(nodeText)
	sb.WriteString("\n")

	var childPrefix string
	if isRoot {
		childPrefix = ""
	} else if useASCII {
		if isLast {
			childPrefix = prefix + "    "
		} else {
			childPrefix = prefix + "|   "
		}
	} else {
		if isLast {
			childPrefix = prefix + "    "
		} else {
			childPrefix = prefix + "│   "
		}
	}

	for i, child := range n.children {
		childIsLast := (i == len(n.children)-1)
		renderTreeNode(sb, child, childPrefix, childIsLast, false, opts)
	}
}
