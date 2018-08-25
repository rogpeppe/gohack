package main

import (
	"fmt"
	"os"

	"gopkg.in/errgo.v2/fmt/errors"
)

var statusCommand = &Command{
	Run:       cmdStatus,
	UsageLine: "status [module...]",
}

func cmdStatus(_ *Command, args []string) {
	if len(args) > 0 {
		errorf("explicit module status not yet implemented")
		os.Exit(2)
	}
	if err := printReplacementInfo(); err != nil {
		errorf("%v", err)
	}
}

func printReplacementInfo() error {
	m, err := goModInfo()
	if err != nil {
		return errors.Wrap(err)
	}
	for _, r := range m.Replace {
		if r.Old.Version == "" && r.New.Version == "" {
			fmt.Printf("%s => %s\n", r.Old.Path, r.New.Path)
		}
	}
	return nil
}
