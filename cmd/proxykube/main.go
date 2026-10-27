package main

import (
	"fmt"
	"io"
	"os"

	"github.com/thomasilliet/proxykube/internal/command"
)

var exit = os.Exit

// main executes the Cobra application and maps failures to a non-zero process exit.
func main() {
	exit(execute(os.Args[1:], os.Stderr))
}

// execute runs the CLI with explicit arguments and returns a process exit code.
func execute(args []string, stderr io.Writer) int {
	cmd := command.New()
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
