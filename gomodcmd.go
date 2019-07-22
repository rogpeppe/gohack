package main

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"path/filepath"
	"strings"
	"time"

	"github.com/rogpeppe/go-internal/modfile"
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

// listModules returns information on the given modules as used by the root module.
func listModules(modules ...string) (mods map[string]*listModule, err error) {
	// TODO make runCmd return []byte so we don't need the []byte conversion.
	args := append([]string{"list", "-m", "-json"}, modules...)
	out, err := runCmd(cwd, "go", args...)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	mods = make(map[string]*listModule)
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

// goModInfo returns the main module's root directory
// and the parsed contents of the go.mod file.
func goModInfo() (string, *modfile.File, error) {
	goModPath, err := findGoMod(cwd)
	if err != nil {
		return "", nil, errors.Notef(err, nil, "cannot find main module")
	}
	rootDir := filepath.Dir(goModPath)
	data, err := ioutil.ReadFile(goModPath)
	if err != nil {
		return "", nil, errors.Notef(err, nil, "cannot read main go.mod file")
	}
	modf, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", nil, errors.Wrap(err)
	}
	return rootDir, modf, nil
}

func findGoMod(dir string) (string, error) {
	out, err := runCmd(dir, "go", "env", "GOMOD")
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("no go.mod file found in any parent directory")
	}
	return strings.TrimSpace(out), nil
}

func writeModFile(modf *modfile.File) error {
	data, err := modf.Format()
	if err != nil {
		return errors.Notef(err, nil, "cannot generate go.mod file")
	}
	if err := ioutil.WriteFile(modf.Syntax.Name, data, 0666); err != nil {
		return errors.Wrap(err)
	}
	return nil
}
