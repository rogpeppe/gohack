package main

import (
	"fmt"
)

var statusCommand = &Command{
	Run:       cmdStatus,
	Short:     "print the current hack status of a module",
	UsageLine: "status [module...]",
	Long: `
The status command prints the status of
all modules that are currently replaced by local
directories. If arguments are given, it prints information
about only the specified modules.
`[1:],
}

func cmdStatus(_ *Command, args []string) int {
	if len(args) > 0 {
		errorf("explicit module status not yet implemented")
		return 2
	}
	if err := printReplacementInfo(); err != nil {
		errorf("%v", err)
	}
	return 0
}

func printReplacementInfo() error {
	for _, r := range mainModFile.Replace {
		if r.Old.Version == "" && r.New.Version == "" {
			fmt.Printf("%s => %s\n", r.Old.Path, r.New.Path)
		}
	}
	return nil
}
