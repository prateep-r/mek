package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// list is a table with a cursor: which row is chosen, and which rows show.
type list struct{ cursor, offset int }

// key moves the cursor; it reports whether the key was a movement (never
// with no rows).
func (l *list) key(k string, n int) bool {
	if n == 0 {
		return false
	}
	switch k {
	case "up":
		l.cursor--
	case "down":
		l.cursor++
	case "home", "g":
		l.cursor = 0
	case "end", "G":
		l.cursor = n - 1
	case "pgup":
		l.cursor -= 10
	case "pgdown":
		l.cursor += 10
	default:
		return false
	}
	l.clamp(n)
	return true
}

func (l *list) clamp(n int) { l.cursor = max(0, min(l.cursor, n-1)) }

// render draws a header and the rows that fit in h lines, keeping the
// cursor's row in view and marking it. Cells may be styled.
func (l *list) render(header []string, rows [][]string, w, h int) string {
	if len(rows) == 0 {
		return dim.Render("  " + strings.Join(header, "  ") + "\n  (none)")
	}
	widths := make([]int, len(header))
	for _, r := range append([][]string{header}, rows...) {
		for i, c := range r {
			widths[i] = max(widths[i], lipgloss.Width(c))
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i, c := range cells {
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(c)+2))
			}
		}
		return b.String()
	}
	l.clamp(len(rows))
	fit := max(1, h-1)
	l.offset = max(min(l.offset, l.cursor), l.cursor-fit+1)
	out := []string{dim.Render(ansi.Truncate("  "+line(header), w, "…"))}
	for i := l.offset; i < min(len(rows), l.offset+fit); i++ {
		mark := "  "
		if i == l.cursor {
			mark = bold.Render("▸ ")
		}
		out = append(out, ansi.Truncate(mark+line(rows[i]), w, "…"))
	}
	return strings.Join(out, "\n")
}
