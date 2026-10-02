package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPalette(t *testing.T) {
	on, off := Palette{on: true}, Palette{}
	for name, f := range map[string]func(Palette, string) string{
		"red": Palette.Red, "yellow": Palette.Yellow, "green": Palette.Green, "dim": Palette.Dim, "bold": Palette.Bold,
	} {
		if got := f(off, "x"); got != "x" {
			t.Errorf("%s off = %q, want plain", name, got)
		}
		if got := f(on, "x"); !strings.HasPrefix(got, "\033[") || !strings.HasSuffix(got, "x\033[0m") {
			t.Errorf("%s on = %q, want ANSI-wrapped", name, got)
		}
	}
	// stderr shortcuts and stream palettes (tests run without a terminal: plain)
	for _, f := range []func(string) string{Red, Yellow, Green, Dim, Bold} {
		if got := f("x"); got != "x" {
			t.Errorf("shortcut colored a non-terminal: %q", got)
		}
	}
	if Out().on || Err().on {
		t.Error("palettes must be off when stdout/stderr are not terminals")
	}
}

func TestColorFor(t *testing.T) {
	devnull, err := os.Open(os.DevNull) // a character device, like a terminal
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	t.Setenv("NO_COLOR", "")
	if !colorFor(devnull) {
		t.Error("character device should get color")
	}
	t.Setenv("NO_COLOR", "1")
	if colorFor(devnull) {
		t.Error("NO_COLOR must disable color")
	}
	t.Setenv("NO_COLOR", "")
	regular, _ := os.Create(filepath.Join(t.TempDir(), "f"))
	if colorFor(regular) {
		t.Error("regular file must not get color")
	}
	regular.Close()
	if isTTY(regular) {
		t.Error("closed file (Stat fails) is not a terminal")
	}
}

func TestInfo(t *testing.T) {
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	Info("hello %s", "mek")
	os.Stderr = old
	w.Close()
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if string(buf[:n]) != "hello mek\n" {
		t.Errorf("Info wrote %q", buf[:n])
	}
}

// answer points the "terminal" at a file containing s.
func answer(t *testing.T, s string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	old := ttyPath
	ttyPath = p
	t.Cleanup(func() { ttyPath = old })
}

func TestConfirm(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, " yes ": true, "n\n": false, "\n": false, "": false} {
		answer(t, in)
		got, err := Confirm("go?")
		if err != nil || got != want {
			t.Errorf("Confirm(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestConfirmTyped(t *testing.T) {
	for in, want := range map[string]bool{"prod\n": true, "  prod  \n": true, "Prod\n": false, "y\n": false, "": false} {
		answer(t, in)
		got, err := ConfirmTyped("really?", "prod")
		if err != nil || got != want {
			t.Errorf("ConfirmTyped(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestPromptErrors(t *testing.T) {
	old := ttyPath
	t.Cleanup(func() { ttyPath = old })

	ttyPath = filepath.Join(t.TempDir(), "missing")
	if _, err := Confirm("?"); !errors.Is(err, ErrNoTTY) {
		t.Errorf("no terminal: %v", err)
	}
	ttyPath = t.TempDir() // opens, but reading a directory fails
	if _, err := Confirm("?"); err == nil || errors.Is(err, ErrNoTTY) {
		t.Errorf("read error: %v", err)
	}
	if _, err := ConfirmTyped("?", "prod"); err == nil {
		t.Error("ConfirmTyped must pass the read error on")
	}
}
