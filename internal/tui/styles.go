package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Styles defines all the lipgloss styles used across the TUI components.
type Styles struct {
	NoBold           bool
	AppTitle         lipgloss.Style
	AppSubtitle      lipgloss.Style
	ActiveBorder     lipgloss.Style
	InactiveBorder   lipgloss.Style
	SuccessText      lipgloss.Style
	WarningText      lipgloss.Style
	ErrorText        lipgloss.Style
	AccentText       lipgloss.Style
	SubtleText       lipgloss.Style
	TableHeader      lipgloss.Style
	TableSelectedRow lipgloss.Style
	ListSelectedRow  lipgloss.Style

	// Layout and Card styles
	Box          lipgloss.Style
	Surface      lipgloss.Style
	Hero         lipgloss.Style
	InfoCard     lipgloss.Style
	StatCard     lipgloss.Style
	DetailHeader lipgloss.Style
	DetailLabel  lipgloss.Style
	DetailValue  lipgloss.Style
	SectionTitle lipgloss.Style
	CommandKey   lipgloss.Style
	HelpStyle    lipgloss.Style
	Spinner      lipgloss.Style

	// Custom status tags
	TodoTag       lipgloss.Style
	InProgressTag lipgloss.Style
	DoneTag       lipgloss.Style
	Footer        lipgloss.Style
	FocusedInput  lipgloss.Style
	BlurredInput  lipgloss.Style

	// Glyphs
	GlyphPointer      string
	GlyphBullet       string
	GlyphCheck        string
	GlyphCross        string
	GlyphWarning      string
	GlyphInfo         string
	GlyphSuccess      string
	GlyphDiamond      string
	GlyphBulletSubtle string
}

// DefaultStyles returns a pre-configured set of styles with a modern developer UI palette.
func DefaultStyles(disableBold bool, disableRoundedBorders bool) Styles {
	s := Styles{
		NoBold: disableBold,
	}

	// Adaptive ANSI Colors - Blue-ish Theme for maximum terminal compatibility
	indigo := lipgloss.AdaptiveColor{Light: "4", Dark: "12"}        // Primary Blue
	blue := lipgloss.AdaptiveColor{Light: "6", Dark: "14"}          // Cyan Accent
	teal := lipgloss.AdaptiveColor{Light: "6", Dark: "14"}          // Cyan Accent
	lavender := lipgloss.AdaptiveColor{Light: "6", Dark: "14"}      // Cyan for labels/details
	darkGray := lipgloss.AdaptiveColor{Light: "8", Dark: "8"}       // Inactive borders (Grey)
	text := lipgloss.AdaptiveColor{Light: "0", Dark: "15"}          // Standard text (Black/White)
	muted := lipgloss.AdaptiveColor{Light: "8", Dark: "8"}          // Muted/Grey
	successGreen := lipgloss.AdaptiveColor{Light: "2", Dark: "10"}  // Green
	warningYellow := lipgloss.AdaptiveColor{Light: "3", Dark: "11"} // Yellow
	errorRed := lipgloss.AdaptiveColor{Light: "1", Dark: "9"}       // Red

	// When running inside the VS Code integrated terminal, unicode glyphs like "›" (U+203A)
	// and "◆" (U+25C6) can render as double-width, cause font-related alignment offsets,
	// or fail to display entirely. We fall back to ASCII-safe alternatives (">" and "*")
	// specifically for the "vscode" TERM_PROGRAM to prevent visual spacing glitches.
	if os.Getenv("TERM_PROGRAM") == "vscode" {
		s.GlyphPointer = ">"
		s.GlyphDiamond = "*"
	} else {
		s.GlyphPointer = "›"
		s.GlyphDiamond = "◆"
	}
	s.GlyphBullet = "•"
	s.GlyphCheck = "✓"
	s.GlyphCross = "✗"
	s.GlyphWarning = "!"
	s.GlyphInfo = "i"
	s.GlyphSuccess = "✓"
	s.GlyphBulletSubtle = "·"

	s.AppTitle = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "15", Dark: "0"}).
		Background(indigo).
		Padding(0, 2).
		Bold(!disableBold)

	s.AppSubtitle = lipgloss.NewStyle().
		Foreground(muted).
		PaddingLeft(1)

	// Standard styling defaults to Rounded borders. However, in environments where
	// disableRoundedBorders is true (including auto-detected VS Code integrated terminals),
	// we fall back to a Normal (rectangular) border. This protects against double-width character
	// rendering glitches and alignment/border gaps commonly caused by Unicode rounded corner characters
	// in VS Code.
	border := lipgloss.RoundedBorder()
	if disableRoundedBorders {
		border = lipgloss.NormalBorder()
	}

	// RoundedBorder provides high-contrast distinct borders for active/focused panes (transparent bg)
	s.ActiveBorder = lipgloss.NewStyle().
		Border(border).
		BorderForeground(indigo)

	s.InactiveBorder = lipgloss.NewStyle().
		Border(border).
		BorderForeground(darkGray)

	s.SuccessText = lipgloss.NewStyle().Foreground(successGreen).Bold(!disableBold)
	s.WarningText = lipgloss.NewStyle().Foreground(warningYellow)
	s.ErrorText = lipgloss.NewStyle().Foreground(errorRed).Bold(!disableBold)
	s.AccentText = lipgloss.NewStyle().Foreground(blue).Bold(!disableBold)
	s.SubtleText = lipgloss.NewStyle().Foreground(muted)

	s.TableHeader = lipgloss.NewStyle().
		Foreground(blue).
		Bold(!disableBold).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(darkGray)

	s.TableSelectedRow = lipgloss.NewStyle().
		Background(indigo).
		Foreground(lipgloss.AdaptiveColor{Light: "15", Dark: "0"}).
		Padding(0, 0).
		Bold(!disableBold)

	s.ListSelectedRow = lipgloss.NewStyle().
		Background(indigo).
		Foreground(lipgloss.AdaptiveColor{Light: "15", Dark: "0"}).
		Padding(0, 0).
		Bold(!disableBold)

	s.Box = lipgloss.NewStyle().
		Padding(1, 2).
		Border(border).
		BorderForeground(darkGray)

	s.Surface = lipgloss.NewStyle().
		Foreground(text)

	s.Hero = lipgloss.NewStyle().
		Foreground(text).
		Border(lipgloss.ThickBorder(), false, false, true, false).
		BorderForeground(indigo).
		Padding(0, 1)

	s.InfoCard = lipgloss.NewStyle().
		Border(border).
		BorderForeground(teal).
		Padding(0, 1)

	s.StatCard = lipgloss.NewStyle().
		Border(border).
		BorderForeground(darkGray).
		Padding(0, 1)

	s.DetailHeader = lipgloss.NewStyle().
		Foreground(indigo).
		Bold(!disableBold)

	s.DetailLabel = lipgloss.NewStyle().
		Foreground(lavender).
		Bold(!disableBold)

	s.DetailValue = lipgloss.NewStyle().
		Foreground(text)

	s.SectionTitle = lipgloss.NewStyle().
		Foreground(blue).
		Bold(!disableBold)

	s.CommandKey = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "0"}).
		Background(lavender).
		Padding(0, 1).
		Bold(!disableBold)

	s.HelpStyle = lipgloss.NewStyle().
		Foreground(muted)

	s.Spinner = lipgloss.NewStyle().
		Foreground(indigo)

	// Status tags
	s.TodoTag = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "15"}).
		Background(lipgloss.AdaptiveColor{Light: "7", Dark: "8"}).
		Padding(0, 1).
		Bold(!disableBold)

	s.InProgressTag = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "15", Dark: "0"}).
		Background(indigo).
		Padding(0, 1).
		Bold(!disableBold)

	s.DoneTag = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "15", Dark: "0"}).
		Background(successGreen).
		Padding(0, 1).
		Bold(!disableBold)

	s.Footer = lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(muted)

	s.FocusedInput = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "0", Dark: "15"})

	s.BlurredInput = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "8", Dark: "8"})

	return s
}

// RenderSuccess returns success text with standard glyph and consistent formatting
func (s Styles) RenderSuccess(msg string) string {
	return s.SuccessText.Render(s.GlyphSuccess + " " + msg)
}

// RenderError returns error text with standard glyph and consistent formatting
func (s Styles) RenderError(msg string) string {
	return s.ErrorText.Render(s.GlyphCross + " " + msg)
}

// RenderInfo returns info/accent text with standard glyph and consistent formatting
func (s Styles) RenderInfo(msg string) string {
	return s.AccentText.Render(s.GlyphInfo + " " + msg)
}

// RenderWarning returns warning text with standard glyph and consistent formatting
func (s Styles) RenderWarning(msg string) string {
	return s.WarningText.Render(s.GlyphWarning + " " + msg)
}
