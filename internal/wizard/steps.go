package wizard

import (
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---------- Input: a line of text ----------

type input struct {
	common
	def     func(Answers) string
	value   string
	offered *string // the last starting value def gave, if any
}

// Input asks for a line of text; def is its starting value (nil: none, or
// the answer given before), validate checks it (nil: anything goes).
func (f *Form) Input(key, title string, def func(Answers) string, validate func(string, Answers) error) *Form {
	return f.add(&input{common: common{key: key, title: title, validate: validate}, def: def})
}

func (s *input) enter(f *Form) tea.Cmd {
	s.err = ""
	switch v, ok := f.answers[s.key]; {
	case ok && (s.offered == nil || v != *s.offered):
		s.value = v // typed, or there from the start: kept
	case s.def != nil: // an answer left as offered follows the answers before it
		s.value = s.def(f.answers)
		offered := s.value
		s.offered = &offered
	default:
		s.value = ""
	}
	return nil
}

// edit applies a key to a line of text; it reports whether it did.
func edit(value *string, k tea.KeyPressMsg) bool {
	switch k.String() {
	case "backspace":
		if r := []rune(*value); len(r) > 0 {
			*value = string(r[:len(r)-1])
		}
	case "ctrl+u":
		*value = ""
	default:
		if k.Text == "" {
			return false
		}
		*value += k.Text
	}
	return true
}

func (s *input) update(f *Form, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "enter":
		if v := strings.TrimSpace(s.value); s.check(v, f.answers) {
			return f.answer(v)
		}
		return nil
	case "esc":
		return f.back()
	}
	if edit(&s.value, k) {
		s.err = ""
	}
	return nil
}

func (s *input) view(*Form) string {
	out := "  " + s.value + "▏"
	if s.err != "" {
		out += "\n  " + red.Render(s.err)
	}
	return out
}

func (s *input) summary(f *Form) string { return orNone(f.answers[s.key]) }

func orNone(v string) string {
	if v == "" {
		return dim.Render("(none)")
	}
	return v
}

// ---------- Select: one of a list ----------

// other is the Select row that switches to typing a value.
const other = "\x00other"

type choice struct {
	common
	options func(Answers) []Option
	other   bool // offer "type it yourself"
	shown   []Option
	cursor  int
	filter  string
	typing  bool // "type it yourself" chosen
	typed   string
	note    string // why the list is what it is (a failed load)
}

// Select asks for one of options (computed from the answers so far); with
// other, the list ends with "type it yourself".
func (f *Form) Select(key, title string, options func(Answers) []Option, other bool) *Form {
	return f.add(&choice{common: common{key: key, title: title}, options: options, other: other})
}

// Options is a fixed list for Select.
func Options(opts ...Option) func(Answers) []Option { return func(Answers) []Option { return opts } }

func (s *choice) enter(f *Form) tea.Cmd {
	s.filter, s.typing, s.typed, s.err = "", false, "", ""
	s.load(s.options(f.answers), f.answers[s.key])
	return nil
}

// load sets the list, the cursor on cur when it is there.
func (s *choice) load(opts []Option, cur string) {
	s.shown, s.cursor = opts, 0
	if s.other {
		s.shown = append(append([]Option{}, opts...), Option{Value: other, Label: "type it yourself…"})
	}
	found := false
	for i, o := range s.shown {
		if o.Value == cur {
			s.cursor, found = i, true
		}
	}
	if cur != "" && !found && s.other { // a value from outside the list: there to edit
		s.cursor, s.typed, s.typing = len(s.shown)-1, cur, true
	}
}

func (s *choice) visible() []int {
	var out []int
	q := strings.ToLower(s.filter)
	for i, o := range s.shown {
		if o.Value == other || strings.Contains(strings.ToLower(o.label()+" "+o.Value+" "+o.Hint), q) {
			out = append(out, i)
		}
	}
	return out
}

func (s *choice) update(f *Form, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if s.typing {
		switch k.String() {
		case "enter":
			if v := strings.TrimSpace(s.typed); s.check(v, f.answers) && v != "" {
				return f.answer(v)
			}
		case "esc":
			s.typing, s.err = false, ""
		default:
			if edit(&s.typed, k) {
				s.err = ""
			}
		}
		return nil
	}
	vis := s.visible()
	pos := 0
	for i, idx := range vis {
		if idx == s.cursor {
			pos = i
		}
	}
	switch k.String() {
	case "up":
		pos = max(0, pos-1)
	case "down":
		pos = min(len(vis)-1, pos+1)
	case "enter":
		if len(vis) == 0 {
			return nil
		}
		if o := s.shown[s.cursor]; o.Value != other {
			return f.answer(o.Value)
		}
		s.typing = true
		return nil
	case "esc":
		if s.filter != "" {
			s.filter = ""
			return nil
		}
		return f.back()
	default:
		if !edit(&s.filter, k) {
			return nil
		}
		vis = s.visible()
		pos = 0
	}
	if len(vis) > 0 {
		s.cursor = vis[pos]
	}
	return nil
}

// window is how many rows of a long list show at once.
const window = 10

func (s *choice) view(f *Form) string {
	var out []string
	if s.note != "" {
		out = append(out, "  "+red.Render(s.note))
	}
	if s.typing {
		out = append(out, "  "+s.typed+"▏")
		if s.err != "" {
			out = append(out, "  "+red.Render(s.err))
		}
		return strings.Join(append(out, dim.Render("  esc: back to the list")), "\n")
	}
	vis := s.visible()
	start := 0
	for i, idx := range vis {
		if idx == s.cursor {
			start = max(0, min(i-window/2, len(vis)-window))
		}
	}
	for _, idx := range vis[start:min(len(vis), start+window)] {
		o, mark := s.shown[idx], "  "
		row := o.label()
		if o.Hint != "" {
			row += dim.Render("  " + o.Hint)
		}
		if idx == s.cursor {
			mark, row = accent.Render("▸ "), bold.Render(o.label())+dim.Render(hintOf(o))
		}
		out = append(out, ansi.Truncate("  "+mark+row, max(20, f.width), "…"))
	}
	if len(vis) == 0 {
		out = append(out, dim.Render("    nothing matches"))
	}
	if len(vis) > window {
		out = append(out, dim.Render(fmt.Sprintf("    … %d more", len(vis)-window)))
	}
	if s.filter != "" {
		out = append(out, dim.Render("  filter: ")+s.filter)
	} else if len(s.shown) > window {
		out = append(out, dim.Render("  type to filter"))
	}
	return strings.Join(out, "\n")
}

func hintOf(o Option) string {
	if o.Hint == "" {
		return ""
	}
	return "  " + o.Hint
}

func (s *choice) summary(f *Form) string {
	v := f.answers[s.key]
	for _, o := range s.shown {
		if o.Value == v && o.Value != other {
			return o.label()
		}
	}
	return orNone(v)
}

// ---------- Load: a Select whose options are fetched ----------

type loader struct {
	choice
	fetch   func(Answers) ([]Option, error)
	loading bool
	seq     int // ignores results of a load left with Esc
}

// loaded carries a fetch's result back to the step that asked for it.
type loaded struct {
	step *loader
	seq  int
	opts []Option
	err  error
}

// Load is a Select whose options fetch gets (a cloud call, in the
// background); when it fails or finds nothing, the step says why and takes
// a typed value instead.
func (f *Form) Load(key, title string, fetch func(Answers) ([]Option, error)) *Form {
	return f.add(&loader{choice: choice{common: common{key: key, title: title}, other: true}, fetch: fetch})
}

func (s *loader) enter(f *Form) tea.Cmd {
	s.filter, s.typing, s.typed, s.note, s.err = "", false, "", "", ""
	s.loading = true
	s.seq++
	seq, a := s.seq, Answers{}
	for k, v := range f.answers {
		a[k] = v
	}
	return func() tea.Msg {
		opts, err := s.fetch(a)
		return loaded{s, seq, opts, err}
	}
}

func (s *loader) update(f *Form, msg tea.Msg) tea.Cmd {
	if m, ok := msg.(loaded); ok {
		if m.step != s || m.seq != s.seq { // another step's, or one left with Esc
			return nil
		}
		s.loading = false
		s.load(m.opts, f.answers[s.key])
		switch {
		case m.err != nil:
			s.note, s.typing = "couldn't list them: "+m.err.Error()+" — type the value", true
		case len(m.opts) == 0:
			s.note, s.typing = "none found — type the value", true
		}
		return nil
	}
	if s.loading {
		if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
			s.loading = false
			return f.back()
		}
		return nil
	}
	return s.choice.update(f, msg)
}

func (s *loader) view(f *Form) string {
	if s.loading {
		return dim.Render("  looking them up…")
	}
	return s.choice.view(f)
}

// ---------- Review: the result, before it is written ----------

type review struct {
	common
	preview func(Answers) (string, error)
	text    string
	err     error
	cursor  int
}

var reviewChoices = []string{"Save", "Back", "Cancel"}

// Review shows what the answers make (preview: the YAML and where it goes)
// and asks to save it; when preview fails, its error shows and only Back
// and Cancel remain useful.
func (f *Form) Review(title string, preview func(Answers) (string, error)) *Form {
	return f.add(&review{common: common{key: "review", title: title}, preview: preview})
}

func (s *review) enter(f *Form) tea.Cmd {
	s.text, s.err = s.preview(f.answers)
	s.cursor = 0
	if s.err != nil {
		s.cursor = 1
	}
	return nil
}

func (s *review) update(f *Form, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "left", "up":
		s.cursor = max(0, s.cursor-1)
	case "right", "down", "tab":
		s.cursor = min(len(reviewChoices)-1, s.cursor+1)
	case "esc":
		return f.back()
	case "enter":
		switch {
		case s.cursor == 0 && s.err == nil:
			return f.answer("save")
		case s.cursor == 1:
			return f.back()
		case s.cursor == 2:
			f.canceled = true
			return tea.Quit
		}
	}
	return nil
}

func (s *review) view(*Form) string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s.text, "\n"), "\n") {
		out = append(out, "    "+l)
	}
	if s.err != nil {
		out = append(out, "  "+red.Render(s.err.Error()))
	}
	var row []string
	for i, c := range reviewChoices {
		if i == s.cursor {
			c = accent.Render("[" + c + "]")
		} else {
			c = " " + c + " "
		}
		row = append(row, c)
	}
	return strings.Join(append(out, "  "+strings.Join(row, " ")), "\n")
}

func (s *review) summary(*Form) string { return "saved" }

// ---------- Exec: a command that needs the terminal ----------

type execStep struct {
	common
	show   func(Answers) string // what will run
	run    func(Answers) error
	cursor int
}

// ran carries an Exec step's result back.
type ran struct{ err error }

var execChoices = []string{"Run it", "Skip"}

// Exec offers to run a command that needs the terminal — a login that opens
// a browser — and runs it with the form put away until it ends. Skip moves
// on without it (the answer is "skipped"; after a run, "done"). Make it
// conditional with When, e.g. on not being logged in yet.
func (f *Form) Exec(key, title string, show func(Answers) string, run func(Answers) error) *Form {
	return f.add(&execStep{common: common{key: key, title: title}, show: show, run: run})
}

func (s *execStep) enter(*Form) tea.Cmd {
	s.cursor, s.err = 0, ""
	return nil
}

func (s *execStep) update(f *Form, msg tea.Msg) tea.Cmd {
	if m, ok := msg.(ran); ok {
		if m.err != nil {
			s.err = m.err.Error()
			return nil
		}
		return f.answer("done")
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "left", "up":
		s.cursor = 0
	case "right", "down", "tab":
		s.cursor = 1
	case "esc":
		return f.back()
	case "enter":
		if s.cursor == 1 {
			return f.answer("skipped")
		}
		s.err = ""
		a := f.answers
		return f.execute(func() error { return s.run(a) })
	}
	return nil
}

func (s *execStep) view(f *Form) string {
	out := []string{"  " + dim.Render("runs: ") + s.show(f.answers)}
	if s.err != "" {
		out = append(out, "  "+red.Render(s.err))
	}
	var row []string
	for i, c := range execChoices {
		if i == s.cursor {
			c = accent.Render("[" + c + "]")
		} else {
			c = " " + c + " "
		}
		row = append(row, c)
	}
	return strings.Join(append(out, "  "+strings.Join(row, " ")), "\n")
}

func (s *execStep) summary(f *Form) string { return f.answers[s.key] }

// handOver runs a command with the terminal: Bubble Tea puts the form away
// and brings it back when the command ends.
func handOver(run func() error) tea.Cmd {
	return tea.Exec(&execCommand{run: run}, func(err error) tea.Msg { return ran{err} })
}

// execCommand is a tea.ExecCommand around a function that uses the terminal
// itself (mek's runner attaches it to the child).
type execCommand struct{ run func() error }

func (c *execCommand) Run() error          { return c.run() }
func (c *execCommand) SetStdin(io.Reader)  {}
func (c *execCommand) SetStdout(io.Writer) {}
func (c *execCommand) SetStderr(io.Writer) {}
