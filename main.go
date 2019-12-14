/*
The gohack command automates checking out a mutable copy
of a module dependency and adding the relevant replace
statement to the go.mod file.

See https://github.com/rogpeppe/gohack for more details.
*/
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rogpeppe/go-internal/modfile"
	"gopkg.in/errgo.v2/fmt/errors"
)

var (
	printCommands = flag.Bool("x", false, "show executed commands")
	dryRun        = flag.Bool("n", false, "print but do not execute update commands")
)

var (
	exitCode = 0
	cwd      = "."

	mainModFile *modfile.File
)

var commands = []*Command{
	getCommand,
	undoCommand,
	statusCommand,
}

func main() {
	os.Exit(main1())
}

func main1() int {
	if dir, err := os.Getwd(); err == nil {
		cwd = dir
	} else {
		return errorf("cannot get current working directory: %v", err)
	}
	flag.Usage = func() {
		mainUsage(os.Stderr)
	}
	flag.Parse()
	if flag.NArg() == 0 {
		mainUsage(os.Stderr)
		return 2
	}
	cmdName := flag.Arg(0)
	args := flag.Args()[1:]
	if cmdName == "help" {
		return runHelp(args)
	}
	var cmd *Command
	for _, c := range commands {
		if c.Name() == cmdName {
			cmd = c
			break
		}
	}
	if cmd == nil {
		errorf("gohack %s: unknown command\nRun 'gohack help' for usage\n", cmdName)
		return 2
	}

	cmd.Flag.Usage = func() { cmd.Usage() }

	if err := cmd.Flag.Parse(args); err != nil {
		if err != flag.ErrHelp {
			errorf(err.Error())
		}
		return 2
	}

	if _, mf, err := goModInfo(); err == nil {
		mainModFile = mf
	} else {
		return errorf("cannot determine main module: %v", err)
	}

	rcode := cmd.Run(cmd, cmd.Flag.Args())
	return max(exitCode, rcode)
}

const debug = false

func errorf(f string, a ...interface{}) int {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(f, a...))
	if debug {
		for _, arg := range a {
			if err, ok := arg.(error); ok {
				fmt.Fprintf(os.Stderr, "error: %s\n", errors.Details(err))
			}
		}
	}
	exitCode = 1
	return exitCode
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
