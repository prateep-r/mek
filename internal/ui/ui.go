// Package ui holds small terminal helpers: colors, banners and confirmations.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Color reports whether stderr should be colored.
func Color() bool { return os.Getenv("NO_COLOR") == "" && isTTY(os.Stderr) }

func paint(code, s string) string {
	if !Color() {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func Red(s string) string    { return paint("1;31", s) }
func Yellow(s string) string { return paint("33", s) }
func Green(s string) string  { return paint("32", s) }
func Dim(s string) string    { return paint("2", s) }
func Bold(s string) string   { return paint("1", s) }

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
