package wizard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func key(s string) tea.KeyPressMsg {
	special := map[string]tea.KeyPressMsg{
		"enter": {Code: tea.KeyEnter}, "esc": {Code: tea.KeyEscape}, "up": {Code: tea.KeyUp}, "down": {Code: tea.KeyDown},
		"left": {Code: tea.KeyLeft}, "right": {Code: tea.KeyRight}, "tab": {Code: tea.KeyTab},
		"backspace": {Code: tea.KeyBackspace}, "ctrl+c": {Code: 'c', Mod: tea.ModCtrl}, "ctrl+u": {Code: 'u', Mod: tea.ModCtrl},
	}
	if k, ok := special[s]; ok {
		return k
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// drive presses keys on a form (starting it the first time), running the
// commands they return like a program; a key "type:abc" types each letter.
func drive(t *testing.T, f *Form, keys ...string) {
	t.Helper()
	if f.width == 0 {
		run(f, f.Init())
		f.Update(tea.WindowSizeMsg{Width: 80})
	}
	for _, k := range keys {
		if text, ok := strings.CutPrefix(k, "type:"); ok {
			for _, r := range text {
				_, cmd := f.Update(key(string(r)))
				run(f, cmd)
			}
			continue
		}
		_, cmd := f.Update(key(k))
		run(f, cmd)
	}
}

func run(f *Form, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			return
		}
		_, cmd = f.Update(msg)
	}
}

func screen(f *Form) string { return ansi.Strip(f.render()) }

func clouds() *Form {
	return New("Add a context").
		Input("name", "Name", nil, func(v string, _ Answers) error {
			if v == "" {
				return errors.New("a name, please")
			}
			return nil
		}).Help("letters, digits, . _ -").
		Select("cloud", "Cloud", Options(Option{Value: "aws", Label: "AWS", Hint: "IAM Identity Center or a profile"}, Option{Value: "gcp", Label: "GCP"}), false).
		Input("project", "Project", func(a Answers) string { return a["name"] + "-proj" }, nil).When(func(a Answers) bool { return a["cloud"] == "gcp" })
}

func TestInputAndSelect(t *testing.T) {
	f := clouds()
	drive(t, f, "enter") // empty: refused
	if !strings.Contains(screen(f), "a name, please") || !strings.Contains(screen(f), "letters, digits") {
		t.Errorf("validation:\n%s", screen(f))
	}
	drive(t, f, "type:devx", "backspace", "enter")
	if f.answers["name"] != "dev" || !strings.Contains(screen(f), "✓ Name: dev") || !strings.Contains(screen(f), "▸ AWS  IAM Identity Center") {
		t.Fatalf("after name: %v\n%s", f.answers, screen(f))
	}
	drive(t, f, "down", "enter") // GCP: the project question appears, with its default
	if f.answers["cloud"] != "gcp" || !strings.Contains(screen(f), "dev-proj▏") {
		t.Fatalf("project default:\n%s", screen(f))
	}
	drive(t, f, "ctrl+u", "type:p1", "enter")
	got, err := f.result()
	if err != nil || got["project"] != "p1" || !f.done {
		t.Errorf("result: %v %v", got, err)
	}
	if s := screen(f); !strings.Contains(s, "✓ Cloud: GCP") || !strings.Contains(s, "✓ Project: p1") {
		t.Errorf("summary:\n%s", s)
	}
	f.Update(key("x")) // done: keys do nothing
}

func TestBackAndSkip(t *testing.T) {
	f := clouds()
	drive(t, f, "esc")                        // the first step: nowhere to go back to
	drive(t, f, "type:dev", "enter", "enter") // AWS: project skipped, done
	if !f.done || f.answers["project"] != "" {
		t.Fatalf("aws skips the project: %v", f.answers)
	}
	f = clouds()
	drive(t, f, "type:dev", "enter", "esc") // back to the name, kept
	if f.cur != 0 || !strings.Contains(screen(f), "dev▏") {
		t.Errorf("back:\n%s", screen(f))
	}
	f = New("prefilled").Prefill(Answers{"name": "prod"}).Input("name", "Name", nil, nil)
	drive(t, f)
	if !strings.Contains(screen(f), "prod▏") {
		t.Errorf("prefill:\n%s", screen(f))
	}
	drive(t, f, "ctrl+c")
	if _, err := f.result(); !errors.Is(err, ErrCanceled) {
		t.Errorf("cancel: %v", err)
	}
	if strings.Contains(screen(f), "?") {
		t.Errorf("canceled form still asks:\n%s", screen(f))
	}
	f.Update(tea.WindowSizeMsg{Width: 10})
	f.Update("not a key")

	f = New("empty").Input("note", "Note", nil, nil).Select("x", "X", Options(Option{Value: "a"}), false)
	drive(t, f, "enter")
	f.Update("not a key")
	if !strings.Contains(screen(f), "✓ Note: (none)") {
		t.Errorf("empty answer:\n%s", screen(f))
	}
}

func regions() []Option {
	var out []Option
	for i := range 25 {
		out = append(out, Option{Value: fmt.Sprintf("region-%02d", i)})
	}
	return out
}

func TestSelectFilterAndOther(t *testing.T) {
	f := New("r").Select("region", "Region", Options(regions()...), true)
	drive(t, f)
	if s := screen(f); !strings.Contains(s, "more") || !strings.Contains(s, "type to filter") {
		t.Errorf("long list:\n%s", s)
	}
	drive(t, f, "type:1", "down")
	if s := screen(f); !strings.Contains(s, "filter: 1") || !strings.Contains(s, "▸ region-10") {
		t.Errorf("filter:\n%s", s)
	}
	drive(t, f, "esc") // clears the filter, stays
	drive(t, f, "type:zz")
	if s := screen(f); !strings.Contains(s, "type it yourself") {
		t.Errorf("other stays listed:\n%s", s)
	}
	drive(t, f, "enter", "enter") // other chosen; empty typed: ignored
	if !strings.Contains(screen(f), "esc: back to the list") {
		t.Fatalf("typing:\n%s", screen(f))
	}
	drive(t, f, "esc", "enter", "type:mars-1", "enter")
	if f.answers["region"] != "mars-1" || !f.done {
		t.Errorf("typed: %v", f.answers)
	}

	f = New("r").Select("region", "Region", Options(regions()...), false)
	drive(t, f, "type:none-such")
	if !strings.Contains(screen(f), "nothing matches") {
		t.Errorf("no match:\n%s", screen(f))
	}
	drive(t, f, "enter", "left") // nothing to choose; a key that isn't text
	drive(t, f, "esc", "esc")    // filter cleared, then back (the first step)
	for range 30 {
		drive(t, f, "down")
	}
	drive(t, f, "up", "enter")
	if f.answers["region"] != "region-23" {
		t.Errorf("arrows: %v", f.answers)
	}
	f = New("r").Prefill(Answers{"region": "region-07"}).Select("region", "Region", Options(regions()...), false)
	drive(t, f, "enter")
	if f.answers["region"] != "region-07" {
		t.Errorf("prefill cursor: %v", f.answers)
	}
	f = New("r").Prefill(Answers{"region": "custom-9"}).Select("region", "Region", Options(regions()...), true)
	drive(t, f, "enter")
	if s := screen(f); !strings.Contains(s, "custom-9▏") { // a value from outside the list: offered typed
		t.Errorf("outside value:\n%s", s)
	}
	drive(t, f, "enter")
	if f.answers["region"] != "custom-9" || !strings.Contains(screen(f), "✓ Region: custom-9") {
		t.Errorf("summary of a typed value: %v", f.answers)
	}
	drive(t, New("x").Select("x", "X", Options(), false), "type:a")
}

func TestLoad(t *testing.T) {
	accounts := func(a Answers) ([]Option, error) {
		if a["portal"] == "broken" {
			return nil, errors.New("not logged in")
		}
		if a["portal"] == "empty" {
			return nil, nil
		}
		return []Option{{Value: "111122223333", Label: "dev", Hint: "111122223333"}, {Value: "444455556666", Label: "prod"}}, nil
	}
	form := func(portal string) *Form {
		return New("acct").Prefill(Answers{"portal": portal}).Load("account", "Account", accounts)
	}
	f := form("ok")
	run(f, f.Init()) // Init alone: the load runs as the program would
	f.Update(tea.WindowSizeMsg{Width: 80})
	drive2 := func(keys ...string) {
		for _, k := range keys {
			_, cmd := f.Update(key(k))
			run(f, cmd)
		}
	}
	drive2("down", "enter")
	if f.answers["account"] != "444455556666" || !strings.Contains(screen(f), "✓ Account: prod") {
		t.Errorf("load: %v\n%s", f.answers, screen(f))
	}
	f = form("broken")
	drive(t, f)
	if s := screen(f); !strings.Contains(s, "couldn't list them: not logged in — type the value") {
		t.Errorf("error:\n%s", s)
	}
	drive(t, f, "type:999988887777", "enter")
	if f.answers["account"] != "999988887777" {
		t.Errorf("typed after error: %v", f.answers)
	}
	f = form("empty")
	drive(t, f)
	if !strings.Contains(screen(f), "none found") {
		t.Errorf("empty:\n%s", screen(f))
	}

	// While loading: Esc goes back; a late result is ignored; keys wait.
	slow := New("s").Input("a", "A", nil, nil).Load("b", "B", accounts)
	run(slow, slow.Init())
	slow.Update(key("x"))
	_, cmd := slow.Update(key("enter")) // to the Load step; its fetch is cmd
	if !strings.Contains(screen(slow), "looking them up") {
		t.Fatalf("loading:\n%s", screen(slow))
	}
	slow.Update(key("down"))  // ignored while loading
	slow.Update(key("esc"))   // back to A
	slow.Update(key("enter")) // and in again: a new load
	slow.Update(cmd())        // the first one's result, stale
	if !strings.Contains(screen(slow), "looking them up") {
		t.Errorf("stale result was used:\n%s", screen(slow))
	}
}

func TestReview(t *testing.T) {
	bad := false
	f := New("rev").Input("name", "Name", nil, nil).Review("Save to ./mek.yaml?", func(a Answers) (string, error) {
		if bad {
			return "", errors.New("invalid: no provider")
		}
		return "contexts:\n  " + a["name"] + ": {}", nil
	})
	drive(t, f, "type:dev", "enter")
	if s := screen(f); !strings.Contains(s, "    contexts:") || !strings.Contains(s, "[Save]") {
		t.Fatalf("review:\n%s", s)
	}
	drive(t, f, "right", "enter") // Back
	if f.cur != 0 {
		t.Errorf("back: %d", f.cur)
	}
	bad = true
	drive(t, f, "enter")
	if s := screen(f); !strings.Contains(s, "invalid: no provider") || !strings.Contains(s, "[Back]") {
		t.Errorf("invalid:\n%s", s)
	}
	drive(t, f, "left", "enter") // Save is no use while invalid
	if f.done {
		t.Error("saved an invalid result")
	}
	drive(t, f, "esc")
	bad = false
	drive(t, f, "enter", "tab", "down", "down", "enter") // Cancel
	if _, err := f.result(); !errors.Is(err, ErrCanceled) {
		t.Errorf("cancel: %v", err)
	}
	f = New("rev").Review("ok?", func(Answers) (string, error) { return "x", nil })
	drive(t, f, "up", "x", "enter")
	if a, err := f.result(); err != nil || a["review"] != "save" || !strings.Contains(screen(f), "✓ ok?: saved") {
		t.Errorf("save: %v %v", a, err)
	}
	f.Update("noise")
	r := &review{}
	r.update(f, "noise")
}

func TestRun(t *testing.T) {
	old := programOptions
	t.Cleanup(func() { programOptions = old })
	programOptions = []tea.ProgramOption{tea.WithInput(strings.NewReader("dev\r")), tea.WithOutput(io.Discard)}
	a, err := New("run").Input("name", "Name", nil, nil).Run()
	if err != nil || a["name"] != "dev" {
		t.Errorf("run: %v %v", a, err)
	}
	if New("x").Answers() == nil {
		t.Error("answers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	programOptions = []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard)}
	if _, err := New("run").Input("name", "Name", nil, nil).Run(); err == nil || errors.Is(err, ErrCanceled) {
		t.Errorf("program error: %v", err)
	}
	programOptions = []tea.ProgramOption{tea.WithInput(strings.NewReader("\x03")), tea.WithOutput(io.Discard)}
	if _, err := New("run").Input("name", "Name", nil, nil).Run(); !errors.Is(err, ErrCanceled) {
		t.Errorf("ctrl-c: %v", err)
	}
}
