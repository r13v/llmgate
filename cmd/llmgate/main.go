package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/r13v/llmgate/internal/version"
)

const usage = `llmgate configures Claude Code for a LiteLLM-compatible gateway.

Usage:
  llmgate [--help] [--version]

With no arguments, llmgate starts the interactive setup wizard.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("llmgate", flag.ContinueOnError)
	flags.SetOutput(stderr)

	showHelp := flags.Bool("help", false, "show help")
	showVersion := flags.Bool("version", false, "show version")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "llmgate: unexpected argument %q\n\n", flags.Arg(0))
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	switch {
	case *showHelp:
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case *showVersion:
		_, _ = fmt.Fprint(stdout, version.Current().String())
		return 0
	default:
		if err := runWizardPlaceholder(stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "llmgate: %v\n", err)
			return 1
		}
		return 0
	}
}

func runWizardPlaceholder(_ io.Writer) error {
	return errors.New("interactive setup wizard is not implemented yet")
}
