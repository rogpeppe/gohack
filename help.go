package main

import (
	"fmt"
	"io"
	"os"
)

// runHelp implements the 'help' command.
func runHelp(args []string) {
	if len(args) == 0 {
		mainUsage(os.Stdout)
		return
	}
	fmt.Printf("specific help not yet implemented\n")
}

func mainUsage(f io.Writer) {
	// TODO better than this
	for _, cmd := range commands {
		fmt.Fprintf(f, "%s\n", cmd.UsageLine)
	}
}
