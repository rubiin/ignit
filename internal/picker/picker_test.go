package picker

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		s       string
		want    bool
	}{
		{"empty pattern matches", "", "go", true},
		{"exact match", "go", "go", true},
		{"case insensitive", "GO", "go", true},
		{"subsequence", "gt", "gitignore", true},
		{"scattered subsequence", "ig", "gitignore", true},
		{"wrong order", "og", "go", false},
		{"missing char", "golang", "go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fuzzyMatch(tt.pattern, tt.s); got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
			}
		})
	}
}

func TestFuzzyMatchIndices(t *testing.T) {
	indices, ok := fuzzyMatchIndices("gt", "gitignore")
	if !ok {
		t.Fatal("fuzzyMatchIndices(\"gt\", \"gitignore\") should match")
	}
	// g=0, t=2 in "gitignore".
	if !reflect.DeepEqual(indices, []int{0, 2}) {
		t.Errorf("indices = %v, want [0 2]", indices)
	}

	if _, ok := fuzzyMatchIndices("xz", "go"); ok {
		t.Error("non-matching pattern should not report ok")
	}
}

func TestHighlight(t *testing.T) {
	style := lipgloss.NewStyle().Bold(true)

	// Pin the color profile: without a TTY it varies by environment, and
	// a mutated profile leaks into every later render. 0 strips colors but
	// keeps bold, so the expected escapes stay deterministic.
	originalProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(0)
	t.Cleanup(func() { lipgloss.SetColorProfile(originalProfile) })

	got := highlight("gitignore", []int{0, 2}, style)
	want := "\x1b[1mg\x1b[0mi\x1b[1mt\x1b[0mignore"
	if got != want {
		t.Errorf("highlight() = %q, want %q", got, want)
	}

	if got := highlight("go", nil, style); got != "go" {
		t.Errorf("highlight() with no indices = %q, want unchanged %q", got, "go")
	}

	// Out-of-range indices must be ignored, not panic.
	if got := highlight("go", []int{-1, 5}, style); got != "go" {
		t.Errorf("highlight() with out-of-range indices = %q, want unchanged %q", got, "go")
	}
}

// updatePicker sends a key message to the picker and returns the new model.
func updatePicker(m Model, key tea.KeyType) Model {
	next, _ := m.Update(tea.KeyMsg{Type: key})
	return next.(Model)
}

// wheelPicker sends a mouse wheel event to the picker and returns the new model.
func wheelPicker(m Model, button tea.MouseButton) Model {
	next, _ := m.Update(tea.MouseMsg(tea.MouseEvent{Action: tea.MouseActionPress, Button: button}))
	return next.(Model)
}

func newTestPicker() Model {
	return New("Select environment", []string{"go", "python", "rust"})
}

// newLargePicker builds a picker with 25 choices to exercise scrolling.
func newLargePicker() Model {
	choices := make([]string, 25)
	for i := range choices {
		choices[i] = fmt.Sprintf("choice%02d", i)
	}
	return New("Select", choices)
}

func TestPickModelFiltering(t *testing.T) {
	m := newTestPicker()

	if len(m.filtered) != 3 {
		t.Fatalf("initial filtered = %v, want all 3 choices", m.filtered)
	}

	m.input.SetValue("py")
	m.applyFilter()
	if !reflect.DeepEqual(m.filtered, []int{1}) {
		t.Errorf("filtered after 'py' = %v, want [1] (python)", m.filtered)
	}

	m.input.SetValue("zzz")
	m.applyFilter()
	if len(m.filtered) != 0 {
		t.Errorf("filtered after 'zzz' = %v, want empty", m.filtered)
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want clamped to 0 when no matches", m.cursor)
	}
}

func TestPickModelFuzzyFiltering(t *testing.T) {
	m := newTestPicker()

	m.input.SetValue("pth")
	m.applyFilter()
	// "pth" is a subsequence of "python" only.
	if !reflect.DeepEqual(m.filtered, []int{1}) {
		t.Errorf("filtered after fuzzy 'pth' = %v, want [1] (python)", m.filtered)
	}
}

func TestPickModelCursorMovement(t *testing.T) {
	m := newTestPicker()

	m = updatePicker(m, tea.KeyDown)
	if m.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.cursor)
	}

	m = updatePicker(m, tea.KeyUp)
	if m.cursor != 0 {
		t.Errorf("cursor after up = %d, want 0", m.cursor)
	}

	// Wraps backwards from 0 to last.
	m = updatePicker(m, tea.KeyUp)
	if m.cursor != 2 {
		t.Errorf("cursor after wrap-around up = %d, want 2", m.cursor)
	}

	// And forward wraps back to 0.
	m = updatePicker(m, tea.KeyDown)
	if m.cursor != 0 {
		t.Errorf("cursor after wrap-around down = %d, want 0", m.cursor)
	}
}

func TestPickModelPageScrolling(t *testing.T) {
	m := newLargePicker()

	// PgDn moves a full page down.
	m = updatePicker(m, tea.KeyPgDown)
	if m.cursor != 10 {
		t.Errorf("cursor after pgdown = %d, want 10", m.cursor)
	}

	// Another page lands at 20, still within the 25-item list.
	m = updatePicker(m, tea.KeyPgDown)
	if m.cursor != 20 {
		t.Errorf("cursor after 2nd pgdown = %d, want 20", m.cursor)
	}

	// PgUp moves back a full page.
	m = updatePicker(m, tea.KeyPgUp)
	if m.cursor != 10 {
		t.Errorf("cursor after pgup = %d, want 10", m.cursor)
	}

	// Page-size moves keep the cursor at the window edge, not beyond.
	if m.offset != 10 {
		t.Errorf("offset = %d, want 10", m.offset)
	}
}

func TestPickModelPageScrollClampsAtEnds(t *testing.T) {
	m := newLargePicker()

	// Two page-downs from 0 with 25 items: cursor stops at 20 with the
	// window scrolled so the cursor sits on the bottom edge (11-20).
	m = updatePicker(m, tea.KeyPgDown)
	m = updatePicker(m, tea.KeyPgDown)
	if m.cursor != 20 || m.offset != 11 {
		t.Errorf("cursor = %d, offset = %d; want 20/11", m.cursor, m.offset)
	}

	// A third page-down wraps around the list end: (20+10) mod 25 = 5.
	m = updatePicker(m, tea.KeyPgDown)
	if m.cursor != 5 {
		t.Errorf("cursor = %d, want 5 after wrap", m.cursor)
	}

	// Three page-ups from 5: 5 -> 20 (wrapped) -> 10 -> 0.
	m = updatePicker(m, tea.KeyPgUp)
	m = updatePicker(m, tea.KeyPgUp)
	m = updatePicker(m, tea.KeyPgUp)
	if m.cursor != 0 || m.offset != 0 {
		t.Errorf("cursor = %d, offset = %d; want 0/0", m.cursor, m.offset)
	}
}

func TestPickModelMouseWheelScrolling(t *testing.T) {
	m := newLargePicker()

	// Wheel down scrolls by wheelScrollLines (3).
	m = wheelPicker(m, tea.MouseButtonWheelDown)
	m = wheelPicker(m, tea.MouseButtonWheelDown)
	if m.cursor != 2*wheelScrollLines {
		t.Errorf("cursor after two wheel-downs = %d, want %d", m.cursor, 2*wheelScrollLines)
	}

	// Wheel up scrolls back.
	m = wheelPicker(m, tea.MouseButtonWheelUp)
	if m.cursor != wheelScrollLines {
		t.Errorf("cursor after wheel-up = %d, want %d", m.cursor, wheelScrollLines)
	}
}

func TestMoveCursorWraps(t *testing.T) {
	m := newLargePicker()

	// Negative wrap: -1 from 0 lands on the last row.
	m.moveCursor(-1)
	if m.cursor != 24 {
		t.Errorf("cursor after -1 wrap = %d, want 24", m.cursor)
	}

	// Positive wrap: +1 from the last row lands on 0.
	m.moveCursor(1)
	if m.cursor != 0 {
		t.Errorf("cursor after +1 wrap = %d, want 0", m.cursor)
	}

	// moveCursor on an empty filtered list must not panic or move.
	empty := New("Select", nil)
	empty.moveCursor(1)
	if empty.cursor != 0 {
		t.Errorf("cursor moved on empty list: %d", empty.cursor)
	}
}

func TestPickModelShowsAtMostTenRows(t *testing.T) {
	m := newLargePicker()

	view := m.View()
	if got := strings.Count(view, "choice"); got != 10 {
		t.Errorf("View() shows %d rows, want 10", got)
	}
	if strings.Contains(view, "choice10") {
		t.Error("View() should not show the 11th entry")
	}
}

func TestPickModelScrollsDownWithCursor(t *testing.T) {
	m := newLargePicker()

	// Move past the bottom of the window: the view must follow.
	for i := 0; i < 12; i++ {
		m = updatePicker(m, tea.KeyDown)
	}
	if m.cursor != 12 {
		t.Fatalf("cursor = %d, want 12", m.cursor)
	}
	if m.offset != 3 {
		t.Errorf("offset = %d, want 3 (cursor on window bottom)", m.offset)
	}

	view := m.View()
	if !strings.Contains(view, "choice12") {
		t.Error("View() should show the cursor row choice12")
	}
	if strings.Contains(view, "choice02") {
		t.Error("View() should have scrolled past choice02")
	}
	if !strings.Contains(view, "(4-13 of 25)") {
		t.Errorf("View() should show scroll indicator '(4-13 of 25)', got:\n%s", view)
	}
}

func TestPickModelScrollsUpWithCursor(t *testing.T) {
	m := newLargePicker()

	// Walk down 15, then up 5: offset should follow back up.
	for i := 0; i < 15; i++ {
		m = updatePicker(m, tea.KeyDown)
	}
	for i := 0; i < 5; i++ {
		m = updatePicker(m, tea.KeyUp)
	}
	if m.cursor != 10 {
		t.Fatalf("cursor = %d, want 10", m.cursor)
	}
	if m.offset != 6 {
		t.Errorf("offset = %d, want 6", m.offset)
	}
}

func TestPickModelWrapScrollsToTail(t *testing.T) {
	m := newLargePicker()

	// Scroll down 5, then wrap upwards past row 0.
	for i := 0; i < 5; i++ {
		m = updatePicker(m, tea.KeyDown)
	}
	for i := 0; i < 6; i++ {
		m = updatePicker(m, tea.KeyUp)
	}
	if m.cursor != 24 {
		t.Fatalf("cursor after wrap = %d, want 24", m.cursor)
	}
	// The window should now show the tail of the list.
	if m.offset != 15 {
		t.Errorf("offset after wrap = %d, want 15", m.offset)
	}
}

func TestPickModelSmallListHasNoIndicator(t *testing.T) {
	m := newTestPicker() // only 3 choices

	if view := m.View(); strings.Contains(view, "of 3") {
		t.Errorf("View() should omit scroll indicator when everything fits, got:\n%s", view)
	}
}

func TestPickModelFilterClampsOffset(t *testing.T) {
	m := newLargePicker()

	// Scroll down, then filter down to a single row: offset must reset
	// so the lone row is visible.
	for i := 0; i < 15; i++ {
		m = updatePicker(m, tea.KeyDown)
	}
	m.input.SetValue("choice05")
	m.applyFilter()

	if len(m.filtered) != 1 {
		t.Fatalf("filtered = %v, want exactly one row", m.filtered)
	}
	if m.cursor != 0 || m.offset != 0 {
		t.Errorf("cursor = %d, offset = %d, want both 0", m.cursor, m.offset)
	}
	if view := m.View(); !strings.Contains(view, "choice05") {
		t.Errorf("View() should show choice05 after filtering, got:\n%s", view)
	}
}

func TestPickModelEnterSelects(t *testing.T) {
	m := newTestPicker()
	m = updatePicker(m, tea.KeyDown) // cursor on "python"

	if got := m.Choices(); got != nil {
		t.Fatalf("before enter: choices = %v, want empty", got)
	}

	m = updatePicker(m, tea.KeyEnter)
	if got := m.Choices(); !reflect.DeepEqual(got, []string{"python"}) {
		t.Errorf("choices after enter = %v, want [python]", got)
	}
	if !m.quit {
		t.Error("quit should be set after enter")
	}
}

func TestPickModelEnterWithNoMatches(t *testing.T) {
	m := newTestPicker()
	m.input.SetValue("zzz")
	m.applyFilter()

	m = updatePicker(m, tea.KeyEnter)
	if got := m.Choices(); got != nil || m.quit {
		t.Errorf("enter with no matches should be ignored, got choices = %v, quit = %v", got, m.quit)
	}
}

func TestPickModelEscapeCancels(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		m := newTestPicker()
		m = updatePicker(m, key)
		if got := m.Choices(); got != nil {
			t.Errorf("choices = %v after %v, want empty", got, key)
		}
		if !m.quit {
			t.Errorf("quit should be set after %v", key)
		}
	}
}

func TestPickModelTypingFilters(t *testing.T) {
	m := newTestPicker()

	// Simulate typing "ru" rune by rune through Update.
	for _, r := range "ru" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}

	if !reflect.DeepEqual(m.filtered, []int{2}) {
		t.Errorf("filtered after typing 'ru' = %v, want [2] (rust)", m.filtered)
	}
}

func TestPickModelInputConfig(t *testing.T) {
	m := newTestPicker()

	if !m.input.Focused() {
		t.Error("input should start focused")
	}
	if m.input.Prompt != "> " {
		t.Errorf("input prompt = %q, want \"> \"", m.input.Prompt)
	}
	if m.input.Placeholder != "Filter..." {
		t.Errorf("input placeholder = %q, want \"Filter...\"", m.input.Placeholder)
	}
	if m.input.CharLimit != 100 {
		t.Errorf("input char limit = %d, want 100", m.input.CharLimit)
	}
	if m.input.Width != 40 {
		t.Errorf("input width = %d, want 40", m.input.Width)
	}
}

func TestPickModelView(t *testing.T) {
	m := newTestPicker()
	view := m.View()

	for _, want := range []string{"Select environment", "go", "python", "rust", "> ", "enter to select"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q", want)
		}
	}

	m.input.SetValue("zzz")
	m.applyFilter()
	if view := m.View(); !strings.Contains(view, "no matches") {
		t.Errorf("View() with no matches should show 'no matches', got %q", view)
	}
}

func TestPickModelInitReturnsBlink(t *testing.T) {
	m := newTestPicker()
	if cmd := m.Init(); cmd == nil {
		t.Error("Init() should return the blink command")
	}
}

func TestPickModelSpaceTogglesSelection(t *testing.T) {
	m := newTestPicker()

	// Space toggles "go" and advances the cursor to "python".
	m = updatePicker(m, tea.KeySpace)
	if m.cursor != 1 {
		t.Errorf("cursor after space = %d, want 1 (advanced)", m.cursor)
	}
	if got := m.Choices(); !reflect.DeepEqual(got, []string{"go"}) {
		t.Errorf("Choices() after one toggle = %v, want [go]", got)
	}

	// Toggle the same row again to deselect.
	m = updatePicker(m, tea.KeyUp)
	m = updatePicker(m, tea.KeySpace)
	if got := m.Choices(); got != nil {
		t.Errorf("Choices() after untoggle = %v, want nil", got)
	}
}

func TestPickModelSpaceTogglesMultiple(t *testing.T) {
	m := newTestPicker()

	// Toggle "go" then "python"; space advances after each toggle.
	m = updatePicker(m, tea.KeySpace)
	m = updatePicker(m, tea.KeySpace)

	if got := m.Choices(); !reflect.DeepEqual(got, []string{"go", "python"}) {
		t.Errorf("Choices() = %v, want [go python]", got)
	}
	if view := m.View(); !strings.Contains(view, "(2 selected)") {
		t.Errorf("View() should show '(2 selected)', got:\n%s", view)
	}
}

func TestPickModelEnterWithSelectionReturnsSelection(t *testing.T) {
	m := newTestPicker()
	m = updatePicker(m, tea.KeySpace) // select "go"

	m = updatePicker(m, tea.KeyEnter)
	if !m.quit {
		t.Error("quit should be set after enter with selections")
	}
	if got := m.Choices(); !reflect.DeepEqual(got, []string{"go"}) {
		t.Errorf("Choices() after enter = %v, want [go]", got)
	}
}

func TestPickModelSelectionSurvivesFiltering(t *testing.T) {
	m := newTestPicker()

	m = updatePicker(m, tea.KeySpace) // select "go"
	m.input.SetValue("py")
	m.applyFilter()
	if view := m.View(); !strings.Contains(view, "(1 selected)") {
		t.Errorf("selected count should survive filtering, got:\n%s", view)
	}

	m.input.SetValue("")
	m.applyFilter()
	if got := m.Choices(); !reflect.DeepEqual(got, []string{"go"}) {
		t.Errorf("Choices() after clearing filter = %v, want [go]", got)
	}
}

func TestPickModelCheckboxMarkers(t *testing.T) {
	m := newTestPicker()
	m = updatePicker(m, tea.KeySpace) // select "go", cursor now on "python"

	// Check markers per line: the cursor row is styled, so its text is
	// wrapped in ANSI codes and cannot be matched with a plain substring.
	checked := map[string]bool{}
	for _, line := range strings.Split(m.View(), "\n") {
		switch {
		case strings.Contains(line, "go"):
			checked["go"] = true
			if !strings.Contains(line, "[x]") {
				t.Errorf("line for go should show selected marker: %q", line)
			}
		case strings.Contains(line, "python"):
			checked["python"] = true
			if !strings.Contains(line, "[ ]") {
				t.Errorf("line for python should show unselected marker: %q", line)
			}
		}
	}
	for _, name := range []string{"go", "python"} {
		if !checked[name] {
			t.Errorf("View() has no line containing %q", name)
		}
	}
}

func TestFuzzyScoreRanking(t *testing.T) {
	tests := []struct {
		name       string
		pattern    string
		higherIsOn string // the choice expected to score better
		lowerIsOn  string
	}{
		{"prefix beats substring", "go", "go", "golang"},
		{"dense beats scattered", "pyt", "python", "p/y/t/"},
		{"shorter string wins on ties", "go", "go", "gopher"},
		{"word start beats interior", "n", "node", "deno"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			high, low := fuzzyScore(tt.pattern, tt.higherIsOn), fuzzyScore(tt.pattern, tt.lowerIsOn)
			if high <= low {
				t.Errorf("fuzzyScore(%q, %q) = %d should beat fuzzyScore(%q, %q) = %d",
					tt.pattern, tt.higherIsOn, high, tt.pattern, tt.lowerIsOn, low)
			}
		})
	}
}

func TestFuzzyScoreEdgeCases(t *testing.T) {
	if got := fuzzyScore("", "go"); got != 0 {
		t.Errorf("fuzzyScore(\"\", \"go\") = %d, want 0", got)
	}
	if got := fuzzyScore("xz", "go"); got != math.MinInt {
		t.Errorf("fuzzyScore(\"xz\", \"go\") = %d, want math.MinInt", got)
	}
	// Exact match scores better than any other kind.
	if fuzzyScore("go", "go") <= fuzzyScore("go", "cargo") {
		t.Error("exact match should outrank a substring match")
	}
}

func TestApplyFilterRanksBestMatchFirst(t *testing.T) {
	// List order is deliberately unhelpful for "go": the exact match sits
	// after several fuzzy hits.
	m := New("Select", []string{"golang", "argo", "go", "logrotate"})

	m.input.SetValue("go")
	m.applyFilter()

	// All four match as subsequences, but ranking puts the exact prefix
	// match first, then the dense word-start match (golang), then the
	// scattered interior hits (argo, logrotate).
	if got := m.filtered; !reflect.DeepEqual(got, []int{2, 0, 1, 3}) {
		t.Errorf("filtered after 'go' = %v (choices %v), want [2 0 1 3] (go, golang, argo, logrotate)", got, m.choices)
	}
	if !cursorRowIs(m, "go") {
		var dbg []string
		for _, line := range strings.Split(m.View(), "\n") {
			dbg = append(dbg, fmt.Sprintf("%q", line))
		}
		t.Errorf("cursor should sit on the best match 'go', lines:\n%s", strings.Join(dbg, "\n"))
	}
}

// ansiCodes matches ANSI SGR escape sequences.
var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// cursorRowIs reports whether the view's cursor row shows the given choice,
// ignoring ANSI codes around matched characters.
func cursorRowIs(m Model, choice string) bool {
	for _, line := range strings.Split(m.View(), "\n") {
		plain := ansiCodes.ReplaceAllString(line, "")
		if strings.HasPrefix(plain, "> [ ] ") && strings.Contains(plain, choice) {
			return true
		}
	}
	return false
}

func TestApplyFilterNoPatternKeepsOrder(t *testing.T) {
	m := New("Select", []string{"rust", "go", "python"})

	// Empty pattern: original list order, no re-ranking.
	if got := m.filtered; !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Errorf("empty-pattern filtered = %v, want original order [0 1 2]", got)
	}

	// Clearing a pattern must restore the original order too.
	m.input.SetValue("go")
	m.applyFilter()
	m.input.SetValue("")
	m.applyFilter()
	if got := m.filtered; !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Errorf("filtered after clearing pattern = %v, want original order", got)
	}
}

func TestApplyFilterTiesKeepOriginalOrder(t *testing.T) {
	// Equal scores (identical strings except index) must not reorder.
	m := New("Select", []string{"zig", "zr", "zig"})
	m.input.SetValue("zig")
	m.applyFilter()

	if got := m.filtered; !reflect.DeepEqual(got, []int{0, 2}) {
		t.Errorf("filtered = %v, want [0 2] (tie keeps original order)", got)
	}
}

func TestApplyFilterRankSurvivesCursorMotion(t *testing.T) {
	m := New("Select", []string{"golang", "go", "godot"})
	m.input.SetValue("go")
	m.applyFilter()

	// Down, then up must land back on the ranked-best row.
	m = updatePicker(m, tea.KeyDown)
	m = updatePicker(m, tea.KeyUp)
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (best match stays first)", m.cursor)
	}
	if !cursorRowIs(m, "go") {
		t.Errorf("View() should still highlight 'go', got:\n%s", m.View())
	}
}
