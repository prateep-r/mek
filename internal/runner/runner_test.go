package runner

import (
	"os"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		argv []string
		want int
	}{
		{[]string{"true"}, 0},
		{[]string{"sh", "-c", "exit 3"}, 3},
		{[]string{"sh", "-c", "kill -TERM $$"}, 128 + 15}, // killed by a signal
		{[]string{"sh", "-c", `test "$MEK_TEST" = yes`}, 0},
	}
	for _, c := range cases {
		code, err := Run(c.argv, append(os.Environ(), "MEK_TEST=yes"))
		if err != nil || code != c.want {
			t.Errorf("Run(%q) = %d, %v; want %d", c.argv, code, err, c.want)
		}
	}
	if code, err := Run([]string{"mek-no-such-command"}, nil); err == nil || code != 127 {
		t.Errorf("missing command: code=%d err=%v, want 127 and an error", code, err)
	}
}
