// Package ui holds small terminal helpers: colors, banners and confirmations.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func colorFor(f *os.File) bool { return os.Getenv("NO_COLOR") == "" && isTTY(f) }

// Palette colors text for one output stream; colors are off when that stream
// is not a terminal or NO_COLOR is set, so pipes and files get plain text.
type Palette struct{ on bool }

var (
	stdout = sync.OnceValue(func() Palette { return Palette{colorFor(os.Stdout)} })
	stderr = sync.OnceValue(func() Palette { return Palette{colorFor(os.Stderr)} })
)

// Out is the palette for command output on stdout.
func Out() Palette { return stdout() }

// Err is the palette for status messages on stderr.
func Err() Palette { return stderr() }

func (p Palette) paint(code, s string) string {
	if !p.on {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (p Palette) Red(s string) string    { return p.paint("1;31", s) }
func (p Palette) Yellow(s string) string { return p.paint("33", s) }
func (p Palette) Green(s string) string  { return p.paint("32", s) }
func (p Palette) Dim(s string) string    { return p.paint("2", s) }
func (p Palette) Bold(s string) string   { return p.paint("1", s) }

// Shortcuts for stderr, where mek prints its own status lines.
func Red(s string) string    { return Err().Red(s) }
func Yellow(s string) string { return Err().Yellow(s) }
func Green(s string) string  { return Err().Green(s) }
func Dim(s string) string    { return Err().Dim(s) }
func Bold(s string) string   { return Err().Bold(s) }

// Info prints a status line to stderr (stdout stays clean for command output).
func Info(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }

// ErrNoTTY is returned when confirmation is required but nobody can answer.
var ErrNoTTY = errors.New("confirmation required but no terminal is available")

// openTTY reads answers from the controlling terminal, so confirmations still
// work when stdin is piped into the wrapped command.
func openTTY() (io.ReadCloser, error) {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return nil, ErrNoTTY
	}
	return f, nil
}

func readLine(prompt string) (string, error) {
	tty, err := openTTY()
	if err != nil {
		return "", err
	}
	defer tty.Close()
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(tty).ReadString('\n')
	if errors.Is(err, io.EOF) && line == "" {
		fmt.Fprintln(os.Stderr) // Ctrl-D: end the prompt line, treat as "no"
		return "", nil
	}
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// Confirm asks a y/N question.
func Confirm(question string) (bool, error) {
	ans, err := readLine(question + " [y/N]: ")
	if err != nil {
		return false, err
	}
	ans = strings.ToLower(ans)
	return ans == "y" || ans == "yes", nil
}

// ConfirmTyped requires the user to type an exact word (the context name).
func ConfirmTyped(question, word string) (bool, error) {
	ans, err := readLine(fmt.Sprintf("%s\nType %s to continue: ", question, Bold(word)))
	if err != nil {
		return false, err
	}
	return ans == word, nil
}
