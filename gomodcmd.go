package main

import (
	"encoding/json"
	"io"
	"strings"
	"time"

	"gopkg.in/errgo.v2/fmt/errors"
)

// listModule holds information on a module as printed by go list -m.
type listModule struct {
	Path     string           // module path
	Version  string           // module version
	Versions []string         // available module versions (with -versions)
	Replace  *listModule      // replaced by this module
	Time     *time.Time       // time version was created
	Update   *listModule      // available update, if any (with -u)
	Main     bool             // is this the main module?
	Indirect bool             // is this module only an indirect dependency of main module?
	Dir      string           // directory holding files for this module, if any
	GoMod    string           // path to go.mod file for this module, if any
	Error    *listModuleError // error loading module
}

type listModuleError struct {
	Err string // the error itself
}

// allModules returns information on all the modules used by the root module.
func allModules() (map[string]*listModule, error) {
	// TODO make runCmd return []byte so we don't need the []byte conversion.
	out, err := runCmd(cwd, "go", "list", "-m", "-json", "all")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	mods := make(map[string]*listModule)
	for {
		var m listModule
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Wrap(err)
		}
		if mods[m.Path] != nil {
			return nil, errors.Newf("duplicate module %q in go list output", m.Path)
		}
		mods[m.Path] = &m
	}
	return mods, nil
}

// editGoMod holds module info as printed by go mod edit -json.
type editGoMod struct {
	Module  editModule
	Require []editRequire
	Exclude []editModule
	Replace []editReplace
}

type editModule struct {
	Path    string
	Version string
}

type editRequire struct {
	Path     string
	Version  string
	Indirect bool
}

type editReplace struct {
	Old editModule
	New editModule
}

func goModInfo() (*editGoMod, error) {
	out, err := runCmd(cwd, "go", "mod", "edit", "-json")
	if err != nil {
		return nil, errors.Wrap(err)
	}
	var m editGoMod
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return nil, errors.Wrap(err)
	}
	return &m, nil
}
