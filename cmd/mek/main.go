package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/prateep-r/mek/internal/cli"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

// run executes mek with args and returns the process exit code.
func run(args []string, stderr io.Writer) int {
	root := cli.NewRoot()
	root.SetArgs(args)
	err := root.Execute()
	var exit *runner.ExitError
	var missing *runner.NotFoundError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code // the wrapped CLI already printed its own error
	}
	fmt.Fprintf(stderr, "%s %v\n", ui.Red("mek:"), err)
	if errors.As(err, &missing) {
		return 127
	}
	return 1
}
