package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FormFieldKind specifies the input control or custom role for a form field.
type FormFieldKind int

const (
	FormFieldTextInput FormFieldKind = iota
	FormFieldTextArea
	FormFieldCustom
	FormFieldButton
)

// FormFieldBinding defines a field's focus index and control type within a form.
type FormFieldBinding struct {
	FocusIndex int
	Kind       FormFieldKind
}

// FormModel manages common form state, field navigation, keyboard/mouse event handling,
// layout sizing, and error/help rendering across TUI creation forms.
type FormModel struct {
	FocusIndex int
	FocusOrder []int
	Width      int
	Height     int
	Styles     Styles
	ErrorMsg   string
	Bindings   []FormFieldBinding
}

// NewFormModel initializes a reusable form component with styles and default dimensions.
func NewFormModel(styles Styles) FormModel {
	return FormModel{
		FocusIndex: 0,
		Width:      80,
		Height:     24,
		Styles:     styles,
	}
}

// BindTextInput registers a text input field at the specified focus index.
func (f *FormModel) BindTextInput(focusIndex int) {
	f.Bindings = append(f.Bindings, FormFieldBinding{
		FocusIndex: focusIndex,
		Kind:       FormFieldTextInput,
	})
}

// BindTextArea registers a textarea field at the specified focus index.
func (f *FormModel) BindTextArea(focusIndex int) {
	f.Bindings = append(f.Bindings, FormFieldBinding{
		FocusIndex: focusIndex,
		Kind:       FormFieldTextArea,
	})
}

// BindCustom registers a custom interactive element at the specified focus index.
func (f *FormModel) BindCustom(focusIndex int) {
	f.Bindings = append(f.Bindings, FormFieldBinding{
		FocusIndex: focusIndex,
		Kind:       FormFieldCustom,
	})
}

// BindButton registers a form action button at the specified focus index.
func (f *FormModel) BindButton(focusIndex int) {
	f.Bindings = append(f.Bindings, FormFieldBinding{
		FocusIndex: focusIndex,
		Kind:       FormFieldButton,
	})
}

// SyncFocus updates focus state across provided text input and textarea controls.
func (f *FormModel) SyncFocus(inputs []*textinput.Model, areas []*textarea.Model) tea.Cmd {
	var cmds []tea.Cmd
	for _, b := range f.Bindings {
		focused := (b.FocusIndex == f.FocusIndex)
		if b.Kind == FormFieldTextInput {
			for _, input := range inputs {
				if input != nil {
					if focused {
						cmds = append(cmds, input.Focus())
					} else {
						input.Blur()
					}
				}
			}
		} else if b.Kind == FormFieldTextArea {
			for _, area := range areas {
				if area != nil {
					if focused {
						cmds = append(cmds, area.Focus())
					} else {
						area.Blur()
					}
				}
			}
		}
	}
	return tea.Batch(cmds...)
}

// UpdateFocus returns a focus command assuming inputs manage their own blur/focus.
func (f *FormModel) UpdateFocus() tea.Cmd {
	return nil
}

// NextFocus advances focus index using FocusOrder if non-empty, or modulo total bindings.
func (f *FormModel) NextFocus() tea.Cmd {
	if len(f.FocusOrder) > 0 {
		f.FocusIndex = nextFocusInOrder(f.FocusIndex, f.FocusOrder)
	} else if len(f.Bindings) > 0 {
		f.FocusIndex = (f.FocusIndex + 1) % len(f.Bindings)
	}
	return f.UpdateFocus()
}

// PreviousFocus reverses focus index using FocusOrder if non-empty, or modulo total bindings.
func (f *FormModel) PreviousFocus() tea.Cmd {
	if len(f.FocusOrder) > 0 {
		f.FocusIndex = previousFocusInOrder(f.FocusIndex, f.FocusOrder)
	} else if len(f.Bindings) > 0 {
		f.FocusIndex = (f.FocusIndex - 1 + len(f.Bindings)) % len(f.Bindings)
	}
	return f.UpdateFocus()
}

// NextFocusForOrder advances focus using a specified custom focus order slice.
func (f *FormModel) NextFocusForOrder(order []int) tea.Cmd {
	f.FocusIndex = nextFocusInOrder(f.FocusIndex, order)
	return f.UpdateFocus()
}

// PreviousFocusForOrder reverses focus using a specified custom focus order slice.
func (f *FormModel) PreviousFocusForOrder(order []int) tea.Cmd {
	f.FocusIndex = previousFocusInOrder(f.FocusIndex, order)
	return f.UpdateFocus()
}

// IsTextAreaFocused returns true if the active FocusIndex is bound to a textarea.
func (f *FormModel) IsTextAreaFocused() bool {
	for _, b := range f.Bindings {
		if b.FocusIndex == f.FocusIndex && b.Kind == FormFieldTextArea {
			return true
		}
	}
	return false
}

// IsTextInputFocused returns true if the active FocusIndex is bound to a text input.
func (f *FormModel) IsTextInputFocused() bool {
	for _, b := range f.Bindings {
		if b.FocusIndex == f.FocusIndex && b.Kind == FormFieldTextInput {
			return true
		}
	}
	return false
}

// ScrollTextAreaDown moves the cursor down by the height of the specified textarea.
func (f *FormModel) ScrollTextAreaDown(area *textarea.Model) tea.Cmd {
	if area == nil {
		return nil
	}
	h := area.Height()
	for i := 0; i < h; i++ {
		area.CursorDown()
	}
	var cmd tea.Cmd
	*area, cmd = area.Update(nil)
	return cmd
}

// ScrollTextAreaUp moves the cursor up by the height of the specified textarea.
func (f *FormModel) ScrollTextAreaUp(area *textarea.Model) tea.Cmd {
	if area == nil {
		return nil
	}
	h := area.Height()
	for i := 0; i < h; i++ {
		area.CursorUp()
	}
	var cmd tea.Cmd
	*area, cmd = area.Update(nil)
	return cmd
}

// HandleMouseMsg handles mouse wheel scrolling for the specified textarea field when focused.
func (f *FormModel) HandleMouseMsg(msg tea.MouseMsg, area *textarea.Model) (tea.Cmd, bool) {
	if area == nil || !f.IsTextAreaFocused() {
		return nil, false
	}
	var cmd tea.Cmd
	if msg.Type == tea.MouseWheelUp {
		area.CursorUp()
		*area, cmd = area.Update(nil)
		return cmd, true
	} else if msg.Type == tea.MouseWheelDown {
		area.CursorDown()
		*area, cmd = area.Update(nil)
		return cmd, true
	}
	return nil, false
}

// HandleWindowSize updates form dimensions with safe TTY / non-TTY fallbacks.
func (f *FormModel) HandleWindowSize(msg tea.WindowSizeMsg) {
	w, h := msg.Width, msg.Height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	f.Width = w
	f.Height = h
}

// RenderError returns the formatted error string if ErrorMsg is non-empty.
func (f *FormModel) RenderError() string {
	if f.ErrorMsg == "" {
		return ""
	}
	return f.Styles.RenderError(f.ErrorMsg)
}

// RenderHelp returns the formatted command help line bounded by form width.
func (f *FormModel) RenderHelp(keys []string) string {
	helpWidth := f.Width - 4
	if helpWidth < 10 {
		helpWidth = 10
	}
	return renderCommandHelp(f.Styles, keys, helpWidth)
}

// RenderButtonWithStyles returns formatted button markup based on whether it holds focus.
func (f *FormModel) RenderButtonWithStyles(index int, label string, focusedStyle, blurredStyle lipgloss.Style) string {
	text := fmt.Sprintf(" [ %s ] ", label)
	if f.FocusIndex == index {
		return focusedStyle.Render(text)
	}
	return blurredStyle.Render(text)
}

// RenderButton returns formatted button markup using default selection or subtle styling.
func (f *FormModel) RenderButton(index int, label string, isSelectedRowStyle bool) string {
	if isSelectedRowStyle {
		return f.RenderButtonWithStyles(index, label, f.Styles.TableSelectedRow, f.Styles.TableSelectedRow)
	}
	return f.RenderButtonWithStyles(index, label, f.Styles.TableSelectedRow, f.Styles.SubtleText)
}

// Helper functions for focus index order navigation
func nextFocusInOrder(current int, order []int) int {
	if len(order) == 0 {
		return current
	}
	for i, focus := range order {
		if focus == current {
			return order[(i+1)%len(order)]
		}
	}
	return order[0]
}

func previousFocusInOrder(current int, order []int) int {
	if len(order) == 0 {
		return current
	}
	for i, focus := range order {
		if focus == current {
			return order[(i-1+len(order))%len(order)]
		}
	}
	return order[len(order)-1]
}

// FormBlock represents a logical section in a scrollable or bounded form view.
type FormBlock struct {
	FocusIndices []int
	Content      string
}

// RenderBoundedBlocks calculates window capacity and joins visible blocks for form views.
func RenderBoundedBlocks(blocks []FormBlock, focusedIndex int, boxHeight int, spacing string, helpText string, errorMsg string) string {
	if len(blocks) == 0 {
		return ""
	}
	focusedBlockIdx := 0
	for idx, blk := range blocks {
		for _, fIdx := range blk.FocusIndices {
			if fIdx == focusedIndex {
				focusedBlockIdx = idx
				break
			}
		}
	}

	helpLines := 0
	if helpText != "" {
		helpLines = strings.Count(helpText, "\n") + 1
	}

	spacingLen := len(spacing)
	nonBlockLines := 2 + spacingLen
	if errorMsg != "" {
		nonBlockLines += 1 + spacingLen
	}
	if helpLines > 0 {
		nonBlockLines += 1 + helpLines
	}

	blocksCapacity := boxHeight - nonBlockLines
	if blocksCapacity < 1 {
		blocksCapacity = 1
	}

	startIdx := focusedBlockIdx
	endIdx := focusedBlockIdx
	currentHeight := strings.Count(blocks[focusedBlockIdx].Content, "\n") + 1

	for {
		expanded := false
		if startIdx > 0 {
			h := strings.Count(blocks[startIdx-1].Content, "\n") + spacingLen
			if currentHeight+h <= blocksCapacity {
				startIdx--
				currentHeight += h
				expanded = true
			}
		}
		if endIdx < len(blocks)-1 {
			h := strings.Count(blocks[endIdx+1].Content, "\n") + spacingLen
			if currentHeight+h <= blocksCapacity {
				endIdx++
				currentHeight += h
				expanded = true
			}
		}
		if !expanded {
			break
		}
	}

	var visibleBlocks []string
	for i := startIdx; i <= endIdx; i++ {
		visibleBlocks = append(visibleBlocks, blocks[i].Content)
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(visibleBlocks, spacing) + "\n")
	if helpText != "" {
		sb.WriteString(helpText)
	}
	return sb.String()
}

// renderBranchOptions returns formatted radio-button choices for git branch creation.
func renderBranchOptions(styles Styles, createBranch bool, branchPrefix string, id string, compact bool) string {
	if branchPrefix == "" {
		branchPrefix = "cyclestone/milestones/"
	}
	var yesOpt, noOpt string
	if createBranch {
		yesOpt = styles.SuccessText.Render("(•) Yes (create branch: " + branchPrefix + id + "-...)")
		noOpt = styles.HelpStyle.Render("( ) No (stay on current branch)")
	} else {
		yesOpt = styles.HelpStyle.Render("( ) Yes (create branch: " + branchPrefix + id + "-...)")
		noOpt = styles.SuccessText.Render("(•) No (stay on current branch)")
	}
	return fmt.Sprintf("%s    %s", yesOpt, noOpt)
}

