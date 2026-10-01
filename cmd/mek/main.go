package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/prateep-r/mek/internal/cli"
	"github.com/prateep-r/mek/internal/runner"
	"github.com/prateep-r/mek/internal/ui"
)

func main() {
	err := cli.NewRoot().Execute()
	if err == nil {
		return
	}
	var exit *runner.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.Code) // the wrapped CLI already printed its own error
	}
	fmt.Fprintf(os.Stderr, "%s %v\n", ui.Red("mek:"), err)
	os.Exit(1)
}
