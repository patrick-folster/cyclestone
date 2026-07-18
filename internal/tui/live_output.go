package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderBoundedLines wraps, tail-selects, truncates, and pads raw live-output
// lines to exactly the content area allocated by the caller.
func renderBoundedLines(lines []string, width int, height int, emptyLine string) string {
	return renderBoundedLinesWithSelection(lines, width, height, emptyLine, true)
}

func renderBoundedLinesFromStart(lines []string, width int, height int, emptyLine string) string {
	return renderBoundedLinesWithSelection(lines, width, height, emptyLine, false)
}

func renderBoundedLinesWithSelection(lines []string, width int, height int, emptyLine string, tail bool) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if len(lines) == 0 && emptyLine != "" {
		lines = []string{emptyLine}
	}

	var wrapped []string
	for _, line := range lines {
		parts := strings.Split(wrapText(line, width), "\n")
		for _, part := range parts {
			wrapped = append(wrapped, truncateDisplayWidth(part, width))
		}
	}
	if len(wrapped) > height {
		if tail {
			wrapped = wrapped[len(wrapped)-height:]
		} else {
			wrapped = wrapped[:height]
		}
	}
	for len(wrapped) < height {
		wrapped = append(wrapped, "")
	}
	return strings.Join(wrapped[:height], "\n")
}

func truncateDisplayWidth(s string, width int) string {
	if width < 1 || lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		next := b.String() + string(r)
		if lipgloss.Width(next) > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
