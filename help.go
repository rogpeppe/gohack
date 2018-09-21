package main

import (
	"fmt"
	"html/template"
	"io"
	"os"
	"strings"
)

// runHelp implements the 'help' command.
func runHelp(args []string) {
	if len(args) == 0 {
		mainUsage(os.Stdout)
		return
	}
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "gohack help %s: too many arguments\n", strings.Join(args, " "))
		os.Exit(2)
	}
	t := template.Must(template.New("").Parse(commandHelpTemplate))
	for _, c := range commands {
		if c.Name() == args[0] {
			if err := t.Execute(os.Stdout, c); err != nil {
				fmt.Fprintf(os.Stderr, "cannot write output: %v", err)
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "gohack help %s: unknown command\n", args[0])
	os.Exit(2)
}

func mainUsage(f io.Writer) {
	t := template.Must(template.New("").Parse(mainHelpTemplate))
	if err := t.Execute(f, commands); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write output: %v", err)
		os.Exit(1)
	}
}

var mainHelpTemplate = `
The gohack command checks out Go module dependencies
into a directory where they can be edited, and adjusts
the go.mod file appropriately.

Usage:

	gohack <command> [arguments]

The commands are:
{{range .}}
	{{.Name | printf "%-11s"}} {{.Short}}{{end}}

Use "gohack help <command>" for more information about a command.
`[1:]

var commandHelpTemplate = `
usage: {{.UsageLine}}

{{.Long}}`[1:]
