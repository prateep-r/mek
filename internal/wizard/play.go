package wizard

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Play runs a form without a terminal, for tests of forms built elsewhere:
// it presses keys in order, running each command to the end first (so a
// Load has its options before the next key). A key is a name — "enter",
// "esc", "up", "down", "left", "right", "tab", "backspace", "ctrl+c",
// "ctrl+u" — or "type:text" for text. An Exec step's command runs in place. Keys left over once the form is done
// are ignored; a form still asking when they run out is canceled.
func Play(f *Form, keys ...string) (Answers, error) {
	f.execute = func(run func() error) tea.Cmd { return func() tea.Msg { return ran{run()} } }
	play(f, f.Init())
	f.Update(tea.WindowSizeMsg{Width: 100})
	for _, k := range keys {
		if text, ok := strings.CutPrefix(k, "type:"); ok {
			for _, r := range text {
				press(f, tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			continue
		}
		press(f, named[k])
	}
	return f.result()
}

var named = map[string]tea.KeyPressMsg{
	"enter": {Code: tea.KeyEnter}, "esc": {Code: tea.KeyEscape}, "up": {Code: tea.KeyUp}, "down": {Code: tea.KeyDown},
	"left": {Code: tea.KeyLeft}, "right": {Code: tea.KeyRight}, "tab": {Code: tea.KeyTab},
	"backspace": {Code: tea.KeyBackspace}, "ctrl+c": {Code: 'c', Mod: tea.ModCtrl}, "ctrl+u": {Code: 'u', Mod: tea.ModCtrl},
}

func press(f *Form, k tea.KeyPressMsg) {
	_, cmd := f.Update(k)
	play(f, cmd)
}

// play runs a command and the ones its messages lead to, as a program would.
func play(f *Form, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			return
		}
		_, cmd = f.Update(msg)
	}
}
