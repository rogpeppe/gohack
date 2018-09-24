package main

import (
	"fmt"

	"gopkg.in/errgo.v2/fmt/errors"
)

var undoCommand = &Command{
	Run:       cmdReplace,
	UsageLine: "undo [-rm] [-f] [module...]",
}

var (
	undoRemove     = undoCommand.Flag.Bool("rm", false, "remove module directory too")
	undoForceClean = undoCommand.Flag.Bool("f", false, "force cleaning of modified-but-not-committed repositories. Do not use this flag unless you really need to!")
)

func cmdReplace(_ *Command, args []string) {
	if err := cmdReplace1(args); err != nil {
		errorf("%v", err)
	}
}

func cmdReplace1(modules []string) error {
	if len(modules) == 0 {
		// With no modules specified, we un-gohack all modules
		// we can find with local directory info in the go.mod file.
		m, err := goModInfo()
		if err != nil {
			return errors.Wrap(err)
		}
		for _, r := range m.Replace {
			if r.Old.Version == "" && r.New.Version == "" {
				modules = append(modules, r.Old.Path)
			}
		}
	}
	// TODO get information from go.mod to make sure we're
	// dropping a directory replace, not some other kind of replacement.
	args := []string{"mod", "edit"}
	for _, m := range modules {
		args = append(args, "-dropreplace="+m)
	}
	if _, err := runCmd(cwd, "go", args...); err != nil {
		return errors.Notef(err, nil, "failed to remove go.mod replacements")
	}
	for _, m := range modules {
		fmt.Printf("dropped %s\n", m)
	}
	return nil
}
