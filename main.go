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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/rogpeppe/gohack/internal/dirhash"
	"golang.org/x/tools/go/vcs"
	"gopkg.in/errgo.v2/fmt/errors"
)

var (
	printCommands = flag.Bool("x", false, "show executed commands")
	dryRun        = flag.Bool("n", false, "print but do not execute update commands")
	forceClean    = flag.Bool("force-clean", false, "force cleaning of modified-but-not-committed repositories. Do not use this flag unless you really need to!")
	undo          = flag.Bool("u", false, "undo gohack replace")
	quick         = flag.Bool("q", false, "quick mode; copy only source only without VCS information")
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
By default, it also tries to check out the version control information into that
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
	var repls []*modReplace
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
				// TODO update to the current version instead of printing an error?
				// That would allow us to (for example) upgrade from quick mode to VCS mode
				// for a given module.
				errorf("%q is already replaced by %q - are you already gohacking it?", mpath, m.Replace.Dir)
			} else {
				errorf("%q is already replaced; will not override replace statement in go.mod", mpath)
			}
			continue
		}
		var repl *modReplace
		if *quick {
			repl1, err := updateFromLocalDir(m)
			if err != nil {
				errorf("cannot update %s from local cache: %v", m.Path, err)
				continue
			}
			repl = repl1
		} else {
			repl1, err := updateVCSDir(m)
			if err != nil {
				errorf("cannot update VCS dir: %v", err)
				continue
			}
			repl = repl1
		}
		// Automatically generate a go.mod file if one doesn't
		// already exist, because otherwise the directory cannot be
		// used as a module.
		if err := ensureGoModFile(repl.modulePath, repl.dir); err != nil {
			errorf("%v", err)
		}
		repls = append(repls, repl)
	}
	if len(repls) == 0 {
		return errors.New("all modules failed; not replacing anything")
	}
	if err := replace(repls); err != nil {
		errorf("cannot replace: %v", err)
	}
	for _, info := range repls {
		fmt.Printf("%s => %s\n", info.modulePath, info.dir)
	}
	return nil
}

func updateFromLocalDir(m *listModule) (*modReplace, error) {
	if m.Dir == "" {
		return nil, errors.Newf("no local source code found")
	}
	srcHash, err := hashDir(m.Dir)
	if err != nil {
		return nil, errors.Notef(err, nil, "cannot hash %q", m.Dir)
	}
	destDir := moduleDir(m.Path)
	_, err = os.Stat(destDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.Wrap(err)
	}
	repl := &modReplace{
		modulePath: m.Path,
		dir:        destDir,
	}
	if err != nil {
		// Destination doesn't exist. Copy the entire directory.
		if err := copyFile(destDir, m.Dir); err != nil {
			return nil, errors.Wrap(err)
		}
	} else {
		if !*forceClean {
			// Destination already exists; try to update it.
			isEmpty, err := isEmptyDir(destDir)
			if err != nil {
				return nil, errors.Wrap(err)
			}
			if !isEmpty {
				// The destination directory already exists and has something in.
				destHash, err := checkCleanWithoutVCS(destDir)
				if err != nil {
					return nil, errors.Wrap(err)
				}
				if destHash == srcHash {
					// Everything is exactly as we want it already.
					return repl, nil
				}
			}
		}
		// As it's empty, clean or we're forcing clean, we can safely replace its
		// contents with the current version.
		if err := updateDirWithoutVCS(destDir, m.Dir); err != nil {
			return nil, errors.Notef(err, nil, "cannot update %q from %q", destDir, m.Dir)
		}
	}
	// Write a hash file so we can tell if someone has changed the
	// directory later, so we avoid overwriting their changes.
	if err := writeHashFile(destDir, srcHash); err != nil {
		return nil, errors.Wrap(err)
	}
	return repl, nil
}

func checkCleanWithoutVCS(dir string) (hash string, err error) {
	wantHash, err := readHashFile(dir)
	if err != nil {
		if !os.IsNotExist(errors.Cause(err)) {
			return "", errors.Wrap(err)
		}
		return "", errors.Newf("%q already exists; not overwriting", dir)
	}
	gotHash, err := hashDir(dir)
	if err != nil {
		return "", errors.Notef(err, nil, "cannot hash %q", dir)
	}
	if gotHash != wantHash {
		return "", errors.Newf("%q is not clean; not overwriting", dir)
	}
	return wantHash, nil
}

func updateDirWithoutVCS(destDir, srcDir string) error {
	if err := os.RemoveAll(destDir); err != nil {
		return errors.Wrap(err)
	}
	if err := copyFile(destDir, srcDir); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// hashDir is like dirhash.HashDir except that it ignores the
// gohack hash file in the top level directory.
func hashDir(dir string) (string, error) {
	files, err := dirhash.DirFiles(dir, "")
	if err != nil {
		return "", err
	}
	j := 0
	for _, f := range files {
		if f != hashFile {
			files[j] = f
			j++
		}
	}
	files = files[:j]
	return dirhash.Hash1(files, func(name string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(dir, name))
	})
}

// TODO decide on a good name for this.
const hashFile = ".gohack-modhash"

func readHashFile(dir string) (string, error) {
	data, err := ioutil.ReadFile(filepath.Join(dir, hashFile))
	if err != nil {
		return "", errors.Note(err, os.IsNotExist, "")
	}
	return strings.TrimSpace(string(data)), nil
}

func writeHashFile(dir string, hash string) error {
	if err := ioutil.WriteFile(filepath.Join(dir, hashFile), []byte(hash), 0666); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

func updateVCSDir(m *listModule) (*modReplace, error) {
	info, err := getVCSInfoForModule(m)
	if err != nil {
		return nil, errors.Notef(err, nil, "cannot get info for %q", m.Path)
	}
	if err := updateModule(info); err != nil {
		return nil, errors.Notef(err, nil, "cannot update %q", m.Path)
	}
	return &modReplace{
		modulePath: m.Path,
		dir:        info.dir,
	}, nil
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

type modReplace struct {
	modulePath string
	dir        string
}

func replace(repls []*modReplace) error {
	args := []string{
		"mod", "edit",
	}
	for _, info := range repls {
		// TODO should we use relative path here?
		args = append(args, fmt.Sprintf("-replace=%s=%s", info.modulePath, info.dir))
	}
	if _, err := runUpdateCmd(cwd, "go", args...); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

func updateModule(info *moduleVCSInfo) error {
	// Remove an auto-generated go.mod file if there is one
	// to avoid confusing VCS logic.
	if _, err := removeAutoGoMod(info); err != nil {
		return errors.Wrap(err)
	}
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
		fmt.Printf("updated hack version of %s to %s\n", info.module.Path, info.module.Version)
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
	// Remove the go.mod file if it was autogenerated so that the
	// normal VCS cleanliness detection works OK.
	removedGoMod, err := removeAutoGoMod(info)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	info.VCSInfo, err = info.vcs.Info(dir)
	if err != nil {
		return nil, errors.Notef(err, nil, "cannot get VCS info from %q", dir)
	}
	if removedGoMod {
		// We removed the autogenerated go.mod file so add it back again.
		if err := ensureGoModFile(info.module.Path, info.dir); err != nil {
			return nil, errors.Wrap(err)
		}
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

const debug = false

func errorf(f string, a ...interface{}) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(f, a...))
	if debug {
		for _, arg := range a {
			if err, ok := arg.(error); ok {
				fmt.Fprintf(os.Stderr, "error: %s\n", errors.Details(err))
			}
		}
	}
	exitCode = 1
}

func ensureGoModFile(modPath, dir string) error {
	modFile := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(modFile); err == nil {
		return nil
	}
	if err := ioutil.WriteFile(modFile, []byte(autoGoMod(modPath)), 0666); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// removeAutoGoMod removes the module directory's go.mod
// file if it looks like it's been autogenerated by us.
// It reports whether the file was removed.
func removeAutoGoMod(m *moduleVCSInfo) (bool, error) {
	modFile := filepath.Join(m.dir, "go.mod")
	data, err := ioutil.ReadFile(modFile)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, errors.Wrap(err)
		}
		return false, nil
	}
	if string(data) != autoGoMod(m.module.Path) {
		return false, nil
	}
	if err := os.Remove(modFile); err != nil {
		return false, errors.Wrap(err)
	}
	return true, nil
}

// autoGoMod returns the contents of the go.mod file that
// would be auto-generated for the module with the given
// path.
func autoGoMod(mpath string) string {
	return "// Generated by gohack; DO NOT EDIT.\nmodule " + mpath + "\n"
}

func isEmptyDir(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, errors.Wrap(err)
	}
	defer f.Close()
	_, err = f.Readdir(1)
	if err != nil && err != io.EOF {
		return false, errors.Wrap(err)
	}
	return err == io.EOF, nil
}

func copyFile(dst, src string) error {
	srcInfo, srcErr := os.Lstat(src)
	if srcErr != nil {
		return errors.Wrap(srcErr)
	}
	_, dstErr := os.Lstat(dst)
	if dstErr == nil {
		return errors.Newf("will not overwrite %q", dst)
	}
	if !os.IsNotExist(dstErr) {
		return errors.Wrap(dstErr)
	}
	switch mode := srcInfo.Mode(); mode & os.ModeType {
	case os.ModeSymlink:
		return errors.Newf("will not copy symbolic link")
	case os.ModeDir:
		return copyDir(dst, src)
	case 0:
		return copyFile1(dst, src)
	default:
		return fmt.Errorf("cannot copy file with mode %v", mode)
	}
}

func copyFile1(dst, src string) error {
	srcf, err := os.Open(src)
	if err != nil {
		return errors.Wrap(err)
	}
	defer srcf.Close()
	dstf, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return errors.Wrap(err)
	}
	defer dstf.Close()
	if _, err := io.Copy(dstf, srcf); err != nil {
		return fmt.Errorf("cannot copy %q to %q: %v", src, dst, err)
	}
	return nil
}

func copyDir(dst, src string) error {
	srcf, err := os.Open(src)
	if err != nil {
		return errors.Wrap(err)
	}
	defer srcf.Close()
	if err := os.Mkdir(dst, 0777); err != nil {
		return errors.Wrap(err)
	}
	for {
		names, err := srcf.Readdirnames(100)
		for _, name := range names {
			if err := copyFile(filepath.Join(dst, name), filepath.Join(src, name)); err != nil {
				return errors.Wrap(err)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.Newf("error reading directory %q: %v", src, err)
		}
	}
	return nil
}
