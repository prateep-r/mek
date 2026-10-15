// Package wizard is mek's terminal forms: a question at a time, answers
// kept above, Esc to go back, Ctrl-C to leave without a trace. A Form is
// put together with a GoF Builder (New().Input(…).Select(…).Review(…)), and
// each step is a GoF State that handles its own keys and draws itself.
package wizard

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Answers are the values chosen so far, by step key.
type Answers map[string]string

// Option is one choice of a Select or Load step.
type Option struct {
	Value string // what goes into Answers
	Label string // what is shown ("" shows Value)
	Hint  string // shown dimmed after the label
}

func (o Option) label() string {
	if o.Label != "" {
		return o.Label
	}
	return o.Value
}

// ErrCanceled: the form was left with Ctrl-C (or "Cancel" on review).
var ErrCanceled = errors.New("canceled — nothing was changed")

var (
	bold   = lipgloss.NewStyle().Bold(true)
	dim    = lipgloss.NewStyle().Faint(true)
	red    = lipgloss.NewStyle().Foreground(lipgloss.Red)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	accent = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
)

// step is one question.
type step interface {
	base() *common
	enter(f *Form) tea.Cmd               // it became the current step
	update(f *Form, msg tea.Msg) tea.Cmd // a key or message for it
	view(f *Form) string                 // the question while current
	summary(f *Form) string              // the answer, once given
}

// common is what every step has.
type common struct {
	key, title, help string
	when             func(Answers) bool // nil: always asked
}

func (c *common) base() *common { return c }

// Form is a sequence of steps.
type Form struct {
	title    string
	steps    []step
	cur      int
	answers  Answers
	canceled bool
	done     bool
	width    int
}

// New starts a form.
func New(title string) *Form { return &Form{title: title, answers: Answers{}} }

// Prefill sets answers before the form runs (editing: today's values).
func (f *Form) Prefill(a Answers) *Form {
	for k, v := range a {
		f.answers[k] = v
	}
	return f
}

// When makes the step added last asked only when cond holds.
func (f *Form) When(cond func(Answers) bool) *Form {
	f.steps[len(f.steps)-1].base().when = cond
	return f
}

// Help sets the step added last's one-line explanation.
func (f *Form) Help(text string) *Form {
	f.steps[len(f.steps)-1].base().help = text
	return f
}

func (f *Form) add(s step) *Form {
	f.steps = append(f.steps, s)
	return f
}

func (f *Form) asked(i int) bool {
	w := f.steps[i].base().when
	return w == nil || w(f.answers)
}

// next moves to the next step that is asked, or finishes.
func (f *Form) next() tea.Cmd {
	for f.cur++; f.cur < len(f.steps); f.cur++ {
		if f.asked(f.cur) {
			return f.steps[f.cur].enter(f)
		}
	}
	f.done = true
	return tea.Quit
}

// back moves to the previous step that is asked (its answer kept as the
// starting value).
func (f *Form) back() tea.Cmd {
	for i := f.cur - 1; i >= 0; i-- {
		if f.asked(i) {
			f.cur = i
			return f.steps[i].enter(f)
		}
	}
	return nil
}

// answer records the current step's value and moves on.
func (f *Form) answer(v string) tea.Cmd {
	f.answers[f.steps[f.cur].base().key] = v
	return f.next()
}

func (f *Form) Init() tea.Cmd {
	f.cur = -1
	return f.next()
}

func (f *Form) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		f.width = m.Width
		return f, nil
	case tea.KeyPressMsg:
		if m.String() == "ctrl+c" {
			f.canceled = true
			return f, tea.Quit
		}
	}
	if f.done || f.cur >= len(f.steps) {
		return f, nil
	}
	return f, f.steps[f.cur].update(f, msg)
}

func (f *Form) View() tea.View { return tea.NewView(f.render()) }

// render is the answers so far, then the current question.
func (f *Form) render() string {
	out := []string{bold.Render(f.title)}
	for i := 0; i < f.cur && i < len(f.steps); i++ {
		if f.asked(i) {
			s := f.steps[i]
			out = append(out, green.Render("✓ ")+s.base().title+": "+s.summary(f))
		}
	}
	if f.done || f.canceled || f.cur >= len(f.steps) {
		return strings.Join(out, "\n") + "\n"
	}
	s := f.steps[f.cur]
	out = append(out, accent.Render("? ")+bold.Render(s.base().title))
	if h := s.base().help; h != "" {
		out = append(out, dim.Render("  "+h))
	}
	out = append(out, s.view(f), dim.Render("  enter ok · esc back · ctrl-c cancel"))
	return strings.Join(out, "\n") + "\n"
}

// Answers is what was chosen (after Run).
func (f *Form) Answers() Answers { return f.answers }

// Run shows the form and returns the answers, or ErrCanceled.
func (f *Form) Run() (Answers, error) {
	if _, err := tea.NewProgram(f, programOptions...).Run(); err != nil {
		return nil, err
	}
	return f.result()
}

func (f *Form) result() (Answers, error) {
	if f.canceled || !f.done {
		return nil, ErrCanceled
	}
	return f.answers, nil
}

var programOptions []tea.ProgramOption // test seam: input and output
