package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestFormModelFocusCycling(t *testing.T) {
	styles := DefaultStyles(true, true)
	form := NewFormModel(styles)

	form.BindTextInput(0)
	form.BindTextInput(1)
	form.BindTextArea(2)
	form.BindButton(3)
	form.BindButton(4)

	form.FocusOrder = []int{0, 1, 2, 3, 4}

	if form.FocusIndex != 0 {
		t.Fatalf("expected initial FocusIndex 0, got %d", form.FocusIndex)
	}

	form.NextFocus()
	if form.FocusIndex != 1 {
		t.Fatalf("expected FocusIndex 1 after NextFocus, got %d", form.FocusIndex)
	}

	form.NextFocus()
	if form.FocusIndex != 2 {
		t.Fatalf("expected FocusIndex 2 after NextFocus, got %d", form.FocusIndex)
	}

	form.PreviousFocus()
	if form.FocusIndex != 1 {
		t.Fatalf("expected FocusIndex 1 after PreviousFocus, got %d", form.FocusIndex)
	}

	form.PreviousFocus()
	if form.FocusIndex != 0 {
		t.Fatalf("expected FocusIndex 0 after PreviousFocus, got %d", form.FocusIndex)
	}

	// Test custom order navigation
	customOrder := []int{0, 2, 4}
	form.NextFocusForOrder(customOrder)
	if form.FocusIndex != 2 {
		t.Fatalf("expected FocusIndex 2 after NextFocusForOrder, got %d", form.FocusIndex)
	}

	form.PreviousFocusForOrder(customOrder)
	if form.FocusIndex != 0 {
		t.Fatalf("expected FocusIndex 0 after PreviousFocusForOrder, got %d", form.FocusIndex)
	}
}

func TestFormModelFieldTypeCheckers(t *testing.T) {
	styles := DefaultStyles(true, true)
	form := NewFormModel(styles)

	form.BindTextInput(0)
	form.BindTextArea(1)
	form.BindButton(2)

	form.FocusIndex = 0
	if !form.IsTextInputFocused() || form.IsTextAreaFocused() {
		t.Fatalf("index 0 should be text input focused")
	}

	form.FocusIndex = 1
	if !form.IsTextAreaFocused() || form.IsTextInputFocused() {
		t.Fatalf("index 1 should be textarea focused")
	}

	form.FocusIndex = 2
	if form.IsTextAreaFocused() || form.IsTextInputFocused() {
		t.Fatalf("index 2 should be button focused (neither text input nor textarea)")
	}
}

func TestFormModelScrollTextAreaAndMouse(t *testing.T) {
	styles := DefaultStyles(true, true)
	form := NewFormModel(styles)
	form.BindTextArea(0)

	area := textarea.New()
	area.SetHeight(5)
	area.SetValue("Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8")

	form.ScrollTextAreaDown(&area)
	form.ScrollTextAreaUp(&area)

	// Test mouse wheel handling
	form.FocusIndex = 0
	_, handled := form.HandleMouseMsg(tea.MouseMsg{Type: tea.MouseWheelDown}, &area)
	if !handled {
		t.Fatalf("expected mouse wheel down on focused textarea to be handled")
	}

	_, handled = form.HandleMouseMsg(tea.MouseMsg{Type: tea.MouseWheelUp}, &area)
	if !handled {
		t.Fatalf("expected mouse wheel up on focused textarea to be handled")
	}
}

func TestFormModelWindowSizeAndRendering(t *testing.T) {
	styles := DefaultStyles(true, true)
	form := NewFormModel(styles)

	// Fallback check
	form.HandleWindowSize(tea.WindowSizeMsg{Width: 0, Height: 0})
	if form.Width != 80 || form.Height != 24 {
		t.Fatalf("expected safe non-TTY fallback 80x24, got %dx%d", form.Width, form.Height)
	}

	form.HandleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 30})
	if form.Width != 100 || form.Height != 30 {
		t.Fatalf("expected 100x30, got %dx%d", form.Width, form.Height)
	}

	// Error rendering
	if form.RenderError() != "" {
		t.Fatalf("empty ErrorMsg should render empty string")
	}
	form.ErrorMsg = "Test error occurred"
	renderedError := form.RenderError()
	if !strings.Contains(renderedError, "Test error occurred") {
		t.Fatalf("RenderError should include error message, got %q", renderedError)
	}

	// Command help rendering
	help := form.RenderHelp([]string{"Tab Focus", "Esc Cancel"})
	if !strings.Contains(help, "Tab") || !strings.Contains(help, "Focus") || !strings.Contains(help, "Esc") {
		t.Fatalf("RenderHelp should render keys, got %q", help)
	}

	// Button rendering
	form.FocusIndex = 0
	btnFocused := form.RenderButtonWithStyles(0, "Submit", styles.CommandKey, styles.SubtleText)
	form.FocusIndex = 1
	btnBlurred := form.RenderButtonWithStyles(0, "Submit", styles.CommandKey, styles.SubtleText)
	if btnFocused == btnBlurred {
		t.Fatalf("focused button rendering should differ from blurred button rendering: focused=%q blurred=%q", btnFocused, btnBlurred)
	}
}

func TestFormModelSyncFocus(t *testing.T) {
	styles := DefaultStyles(true, true)
	form := NewFormModel(styles)
	form.BindTextInput(0)
	form.BindTextArea(1)

	input := textinput.New()
	area := textarea.New()

	form.FocusIndex = 0
	form.SyncFocus([]*textinput.Model{&input}, []*textarea.Model{&area})
	if !input.Focused() {
		t.Fatalf("input 0 should be focused")
	}

	form.FocusIndex = 1
	form.SyncFocus([]*textinput.Model{&input}, []*textarea.Model{&area})
	if input.Focused() {
		t.Fatalf("input 0 should be blurred")
	}
	if !area.Focused() {
		t.Fatalf("area 1 should be focused")
	}
}
