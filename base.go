package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// A Command is an implementation of a gohack command.
type Command struct {
	// Run runs the command and returns its exit status.
	// The args are the arguments after the command name.
	Run func(cmd *Command, args []string) int

	// UsageLine is the one-line usage message.
	// The first word in the line is taken to be the command name.
	UsageLine string

	// Short is the short description shown in the 'gohack help' output.
	Short string

	// Long is the long message shown in the 'gohack help <this-command>' output.
	Long string

	// Flag is a set of flags specific to this command.
	Flag flag.FlagSet
}

func (c *Command) Name() string {
	return strings.SplitN(c.UsageLine, " ", 2)[0]
}

func (c *Command) Usage() {
	fmt.Fprintf(os.Stderr, "usage: %s\n", c.UsageLine)
	fmt.Fprintf(os.Stderr, "Run 'gohack help %s' for details.\n", c.Name())
}
