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
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/tools/go/vcs"
	"gopkg.in/errgo.v2/fmt/errors"
)

var (
	printCommands = flag.Bool("x", false, "show executed commands")
	dryRun        = flag.Bool("n", false, "print but do not execute update commands")
	forceClean    = flag.Bool("force-clean", false, "force cleaning of modified-but-not-committed repositories. Do not use this flag unless you really need to!")
	undo          = flag.Bool("u", false, "undo gohack replace")
	// TODO add a flag to enable checkout of source only without VCS information.
)

var (
	exitCode = 0
	cwd      = "."
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: gohack module...\n")
		flag.PrintDefaults()
		fmt.Print(`
The gohack command checks out Go module dependencies
into a directory where they can be edited.
By default it extracts module source code into $HOME/gohack/<module>.
It also tries to check out the version control information into that
directory and update it to the expected version. If the directory
already exists, it will be updated in place.

With no arguments, gohack prints all modules
that are currently replaced by local directories.

The -u flag can be used to revert to the non-gohacked
module versions. It only removes the relevant replace
statements from the go.mod file - it does not change any
of the directories referred to. With the -u flag and no
arguments, all replace statements that refer to directories will
be removed.
`)
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
	if len(flag.Args()) == 0 {
		return printReplacementInfo()
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
			errorf("module %q does not appear to be in use", mpath)
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
			errorf("cannot get info for %q: %v", m.Path, err)
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
	// TODO create a go.mod file in the destination directory if one
	// does not already exist, because otherwise it can't be used.
	// That makes the clean-checking logic harder though; a simpler
	// (though probably annoying) approach might be to just
	// disallow gohack on any module without a go.mod file.

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
	// module holds the module information as printed by go list.
	module *listModule
	// alreadyExists holds whether the replacement directory already exists.
	alreadyExists bool
	// dir holds the path to the replacement directory.
	dir string
	// root holds information on the VCS root of the module.
	root *vcs.RepoRoot
	// vcs holds the implementation of the VCS used by the module.
	vcs VCS
	// VCSInfo holds information on the VCS tree in the replacement
	// directory. It is only filled in when alreadyExists is true.
	VCSInfo
}

// getVCSInfoForModule returns VCS information about the module
// by inspecting the module path and the module's checked out
// directory.
func getVCSInfoForModule(m *listModule) (*moduleVCSInfo, error) {
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
			fmt.Fprintf(os.Stderr, "error: %s\n", errors.Details(err))
		}
	}
	exitCode = 1
}
