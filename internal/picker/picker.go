// Package picker provides a fuzzy-filtering select list built with
// Bubble Tea: a filter input, highlighted matches, a cursor, and a
// scrollable 10-row window.
package picker

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// maxVisibleRows is the number of list rows shown at once; the view scrolls
// to keep the cursor inside this window.
const maxVisibleRows = 10

// wheelScrollLines is how many rows the mouse wheel scrolls per event.
const wheelScrollLines = 3

// pickerStyles holds the lipgloss styles used by the picker.
type pickerStyles struct {
	title         lipgloss.Style
	item          lipgloss.Style
	selected      lipgloss.Style
	prompt        lipgloss.Style
	hint          lipgloss.Style
	match         lipgloss.Style // matched characters in unselected rows
	matchSelected lipgloss.Style // matched characters in the selected row
}

// newPickerStyles returns the picker styles.
func newPickerStyles() pickerStyles {
	return pickerStyles{
		title:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		item:          lipgloss.NewStyle(),
		selected:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		prompt:        lipgloss.NewStyle().Bold(true).MarginBottom(1),
		hint:          lipgloss.NewStyle().Faint(true).MarginTop(1),
		match:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		matchSelected: lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color("12")),
	}
}

// fuzzyMatchIndices reports whether pattern matches s as a case-insensitive
// subsequence and, when it does, returns the rune indices of s at which the
// pattern characters matched so callers can highlight them.
func fuzzyMatchIndices(pattern, s string) ([]int, bool) {
	pRunes := []rune(strings.ToLower(pattern))
	sRunes := []rune(strings.ToLower(s))

	var indices []int
	i := 0
	for j, r := range sRunes {
		if i < len(pRunes) && pRunes[i] == r {
			indices = append(indices, j)
			i++
		}
	}
	if i != len(pRunes) {
		return nil, false
	}
	return indices, true
}

// fuzzyMatch reports whether pattern matches s as a case-insensitive
// subsequence: every rune of pattern appears in s in order, e.g. "prj"
// matches "My Project".
func fuzzyMatch(pattern, s string) bool {
	_, matched := fuzzyMatchIndices(pattern, s)
	return matched
}

// Model is a Bubble Tea model that shows a filterable list of strings.
// Typing narrows the list with fuzzy matching; enter accepts the highlighted
// entry.
type Model struct {
	label    string
	choices  []string
	filtered []int // indices into choices currently shown
	cursor   int   // position within filtered
	offset   int   // first visible position within filtered
	input    textinput.Model
	styles   pickerStyles
	choice   string // accepted choice, empty when none
	quit     bool   // set once a choice is made or cancelled
}

// New builds a picker for the given choices.
func New(label string, choices []string) Model {
	ti := textinput.New()
	ti.Placeholder = "Filter..."
	ti.Prompt = "> "
	ti.Focus()
	ti.CharLimit = 100
	ti.Width = 40

	m := Model{
		label:   label,
		choices: choices,
		input:   ti,
		styles:  newPickerStyles(),
	}
	m.applyFilter()
	return m
}

// Choice returns the accepted choice, or the empty string when the picker
// was cancelled or nothing was selected.
func (m Model) Choice() string {
	return m.choice
}

// clampView keeps the cursor inside the visible window by shifting offset,
// and clamps the cursor to the filtered range.
func (m *Model) clampView() {
	if m.cursor >= len(m.filtered) {
		m.cursor = max(len(m.filtered)-1, 0)
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+maxVisibleRows {
		m.offset = m.cursor - maxVisibleRows + 1
	}
	m.offset = min(m.offset, max(len(m.filtered)-maxVisibleRows, 0))
	if m.offset < 0 {
		m.offset = 0
	}
}

// applyFilter recomputes the visible choices from the current input value
// and clamps the cursor to the new range.
func (m *Model) applyFilter() {
	m.filtered = m.filtered[:0]
	pattern := strings.TrimSpace(m.input.Value())
	for i, choice := range m.choices {
		if pattern == "" || fuzzyMatch(pattern, choice) {
			m.filtered = append(m.filtered, i)
		}
	}
	m.clampView()
}

// moveCursor moves the cursor by delta positions, wrapping around both ends
// of the filtered list, and scrolls the view to keep the cursor visible.
func (m *Model) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor = ((m.cursor+delta)%len(m.filtered) + len(m.filtered)) % len(m.filtered)
	m.clampView()
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.choice = ""
			m.quit = true
			return m, tea.Quit
		case tea.KeyEnter:
			if len(m.filtered) > 0 {
				m.choice = m.choices[m.filtered[m.cursor]]
				m.quit = true
				return m, tea.Quit
			}
			return m, nil // no matches: ignore enter
		case tea.KeyUp:
			m.moveCursor(-1)
			return m, nil
		case tea.KeyDown:
			m.moveCursor(1)
			return m, nil
		case tea.KeyPgUp:
			m.moveCursor(-maxVisibleRows)
			return m, nil
		case tea.KeyPgDown:
			m.moveCursor(maxVisibleRows)
			return m, nil
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.moveCursor(-wheelScrollLines)
				return m, nil
			case tea.MouseButtonWheelDown:
				m.moveCursor(wheelScrollLines)
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.applyFilter()
	return m, cmd
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(m.styles.title.Render(m.label))
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")

	if len(m.filtered) == 0 {
		b.WriteString(m.styles.hint.Render("no matches"))
		return b.String()
	}

	pattern := strings.TrimSpace(m.input.Value())
	end := min(m.offset+maxVisibleRows, len(m.filtered))
	for pos := m.offset; pos < end; pos++ {
		index := m.filtered[pos]
		choice := m.choices[index]
		style := m.styles.item
		matchStyle := m.styles.match
		prefix := "  "
		if pos == m.cursor {
			style = m.styles.selected
			matchStyle = m.styles.matchSelected
			prefix = "> "
		}
		indices, _ := fuzzyMatchIndices(pattern, choice)
		b.WriteString(prefix)
		b.WriteString(style.Render(highlight(choice, indices, matchStyle)))
		b.WriteString("\n")
	}

	// Scroll indicator, shown only when entries are hidden in either
	// direction.
	var indicator string
	if m.offset > 0 || end < len(m.filtered) {
		indicator = fmt.Sprintf(" (%d-%d of %d)", m.offset+1, end, len(m.filtered))
	}
	b.WriteString(m.styles.hint.Render("(type to filter, up/down or pgup/pgdn to move, enter to select, esc to cancel)" + indicator))
	return b.String()
}

// highlight wraps the runes at the given indices in s with the given style,
// grouping adjacent runes into single styled runs. Without indices, s is
// returned unchanged.
func highlight(s string, indices []int, style lipgloss.Style) string {
	if len(indices) == 0 {
		return s
	}

	runes := []rune(s)
	matched := make(map[int]bool, len(indices))
	for _, i := range indices {
		if i >= 0 && i < len(runes) {
			matched[i] = true
		}
	}

	var b strings.Builder
	for i := 0; i < len(runes); {
		j := i
		for j < len(runes) && matched[j] == matched[i] {
			j++
		}
		seg := string(runes[i:j])
		if matched[i] {
			b.WriteString(style.Render(seg))
		} else {
			b.WriteString(seg)
		}
		i = j
	}
	return b.String()
}
