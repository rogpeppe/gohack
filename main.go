/*
gohack $module...

	fetch module if needed into $GOHACK/$module
	checkout correct commit
	"is not clean" failure if it's been changed but at right commit
	"is not clean; will not update" failure if changed and at wrong commit
	add replace statement to go.mod

gohack -u $module

	remove replace statement from go.mod
*/
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/tools/go/vcs"
	"gopkg.in/errgo.v2/fmt/errors"
)

var (
	printCommands = flag.Bool("x", false, "show executed commands")
	dryRun        = flag.Bool("n", false, "print but do not execute update commands")
	forceClean    = flag.Bool("force-clean", false, "force cleaning of modified-but-not-committed repositories. Do not use this flag unless you really need to!")
	undo          = flag.Bool("u", false, "undo gohack replace")
)

type Module struct {
	Path     string       // module path
	Version  string       // module version
	Versions []string     // available module versions (with -versions)
	Replace  *Module      // replaced by this module
	Time     *time.Time   // time version was created
	Update   *Module      // available update, if any (with -u)
	Main     bool         // is this the main module?
	Indirect bool         // is this module only an indirect dependency of main module?
	Dir      string       // directory holding files for this module, if any
	GoMod    string       // path to go.mod file for this module, if any
	Error    *ModuleError // error loading module
}

type ModuleError struct {
	Err string // the error itself
}

type replacement struct {
	module string
	dir    string
}

var (
	exitCode = 0
	cwd      = "."
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: gohack module...\n")
		os.Exit(2)
	}
	flag.Parse()
	if err := main1(); err != nil {
		errorf("%v", err)
	}
	os.Exit(exitCode)
}

func main1() error {
	if dir, err := os.Getwd(); err == nil {
		cwd = dir
	} else {
		errorf("cannot get current working directory: %v", err)
	}
	if *undo {
		return undoReplace(flag.Args())
	}
	var repls []*moduleVCSInfo
	mods, err := allModules()
	if err != nil {
		// TODO this happens when a replacement directory has been removed.
		// Perhaps we should be more resilient in that case?
		return errors.Notef(err, nil, "cannot get module info")
	}
	for _, mpath := range flag.Args() {
		m := mods[mpath]
		if m == nil {
			errorf("module %q does not appear to be in use", m)
			continue
		}
		if m.Replace != nil {
			if m.Replace.Path == m.Replace.Dir {
				errorf("%q is already replaced by %q - are you already gohacking it?", mpath, m.Replace.Dir)
			} else {
				errorf("%q is already replaced; will not override replace statement in go.mod", mpath)
			}
			continue
		}
		info, err := getVCSInfoForModule(m)
		if err != nil {
			errorf("cannot get info for %q: %v", err)
			continue
		}
		if err := updateModule(info); err != nil {
			errorf("cannot update %q: %v", m.Path, err)
			continue
		}
		repls = append(repls, info)
	}
	if len(repls) == 0 {
		return errors.New("all modules failed; not replacing anything")
	}
	if err := replace(repls); err != nil {
		errorf("cannot replace: %v", err)
	}
	for _, info := range repls {
		fmt.Printf("%s => %s\n", info.module.Path, info.dir)
	}
	return nil
}

func undoReplace(modules []string) error {
	// TODO get information from go.mod to make sure we're
	// dropping a directory replace, not some other kind of replacement.
	args := []string{"mod", "edit"}
	for _, m := range modules {
		args = append(args, "-dropreplace="+m)
	}
	if _, err := runCmd("go", cwd, args...); err != nil {
		return errors.Notef(err, nil, "failed to remove go.mod replacements")
	}
	return nil
}

func replace(repls []*moduleVCSInfo) error {
	args := []string{
		"mod", "edit",
	}
	for _, info := range repls {
		// TODO should we use relative path here?
		args = append(args, fmt.Sprintf("-replace=%s=%s", info.module.Path, info.dir))
	}
	if _, err := runUpdateCmd(cwd, "go", args...); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

func updateModule(info *moduleVCSInfo) error {
	if info.alreadyExists && !info.clean && *forceClean {
		if err := info.vcs.Clean(info.dir); err != nil {
			return fmt.Errorf("cannot clean: %v", err)
		}
	}

	isTag := true
	updateTo := info.module.Version
	if IsPseudoVersion(updateTo) {
		revID, err := PseudoVersionRev(updateTo)
		if err != nil {
			return errors.Wrap(err)
		}
		isTag = false
		updateTo = revID
	}
	if err := info.vcs.Update(info.dir, isTag, updateTo); err == nil {
		fmt.Printf("updated hack version of %s to %s", info.module.Path, info.module.Version)
		return nil
	}
	if !info.alreadyExists {
		fmt.Printf("creating %s@%s\n", info.module.Path, info.module.Version)
		if err := createRepo(info); err != nil {
			return fmt.Errorf("cannot create repo: %v", err)
		}
	} else {
		fmt.Printf("fetching %s@%s\n", info.module.Path, info.module.Version)
		if err := info.vcs.Fetch(info.dir); err != nil {
			return err
		}
	}
	return info.vcs.Update(info.dir, isTag, updateTo)
}

func createRepo(info *moduleVCSInfo) error {
	// Some version control tools require the parent of the target to exist.
	parent, _ := filepath.Split(info.dir)
	if err := os.MkdirAll(parent, 0777); err != nil {
		return err
	}
	if err := info.vcs.Create(info.root.Repo, info.dir); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

type moduleVCSInfo struct {
	module        *Module
	alreadyExists bool
	dir           string
	root          *vcs.RepoRoot
	vcs           VCS
	// VCSInfo is only filled in when alreadyExists is true.
	VCSInfo
}

func getVCSInfoForModule(m *Module) (*moduleVCSInfo, error) {
	// TODO if module directory already exists, could look in it to see if there's
	// a single VCS directory and use that if so, to avoid hitting the network
	// for vanity imports.
	root, err := vcs.RepoRootForImportPath(m.Path, *printCommands)
	if err != nil {
		return nil, errors.Note(err, nil, "cannot find module root")
	}
	v, ok := kindToVCS[root.VCS.Cmd]
	if !ok {
		return nil, errors.Newf("unknown VCS kind %q", root.VCS.Cmd)
	}
	dir := moduleDir(m.Path)
	dirInfo, err := os.Stat(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.Wrap(err)
	}
	if err == nil && !dirInfo.IsDir() {
		return nil, errors.Newf("%q is not a directory", dir)
	}
	info := &moduleVCSInfo{
		module:        m,
		root:          root,
		alreadyExists: err == nil,
		dir:           dir,
		vcs:           v,
	}
	if !info.alreadyExists {
		return info, nil
	}
	info.VCSInfo, err = info.vcs.Info(dir)
	if err != nil {
		return nil, errors.Notef(err, nil, "cannot get VCS info from %q", dir)
	}
	return info, nil
}

// allModules returns information on all the modules used by the root module.
func allModules() (map[string]*Module, error) {
	var buf bytes.Buffer
	c := exec.Command("go", "list", "-m", "-json", "all")
	c.Stderr = os.Stderr
	c.Stdout = &buf
	err := c.Run()
	if err != nil {
		return nil, errors.Notef(err, nil, "cannot list all modules")
	}
	dec := json.NewDecoder(&buf)
	mods := make(map[string]*Module)
	for {
		var m Module
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

// moduleDir returns the path to the directory to be
// used for storing the module with the given path.
func moduleDir(module string) string {
	// TODO decide what color this bikeshed should be.
	d := os.Getenv("GOHACK")
	if d == "" {
		d = filepath.Join(os.Getenv("HOME"), "gohack")
	}
	return filepath.Join(d, filepath.FromSlash(module))
}

func errorf(f string, a ...interface{}) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(f, a...))
	for _, arg := range a {
		if err, ok := arg.(error); ok {
			fmt.Fprintln(os.Stderr, "error: %s\n", errors.Details(err))
		}
	}
	exitCode = 1
}
