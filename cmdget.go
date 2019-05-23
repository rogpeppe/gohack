package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/errgo.v2/fmt/errors"

	"github.com/rogpeppe/go-internal/modfile"
	"github.com/rogpeppe/go-internal/module"
)

var getCommand = &Command{
	UsageLine: "get [-vcs] [-u] [-f] [module...]",
	Short:     "start hacking a module",
	Long: `
The get command checks out Go module dependencies
into a directory where they can be edited.

It uses $GOHACK/<module> as the destination directory,
or $HOME/gohack/<module> if $GOHACK is empty.

By default it copies module source code from the existing
source directory in $GOPATH/pkg/mod. If the -vcs
flag is specified, it also checks out the version control information into that
directory and updates it to the expected version. If the directory
already exists, it will be updated in place.
`[1:],
}

func init() {
	getCommand.Run = runGet // break init cycle
}

var (
	// TODO implement getUpdate so that we can use gohack -f without
	// overwriting source code.
	// getUpdate = getCommand.Flag.Bool("u", false, "update to current version")
	getForce = getCommand.Flag.Bool("f", false, "force update to current version even if not clean")
	getVCS   = getCommand.Flag.Bool("vcs", false, "get VCS information too")
)

func runGet(cmd *Command, args []string) int {
	if err := runGet1(args); err != nil {
		errorf("%v", err)
	}
	return 0
}

func runGet1(args []string) error {
	if len(args) == 0 {
		return errors.Newf("get requires at least one module argument")
	}
	var repls []*modReplace
	mods, err := listModules("all")
	if err != nil {
		// TODO this happens when a replacement directory has been removed.
		// Perhaps we should be more resilient in that case?
		return errors.Notef(err, nil, "cannot get module info")
	}
	modf, err := goModInfo()
	if err != nil {
		return errors.Notef(err, nil, "cannot get local module info")
	}
	for _, mpath := range args {
		m := mods[mpath]
		if m == nil {
			errorf("module %q does not appear to be in use", mpath)
			continue
		}
		// Early check that we can replace the module, so we don't
		// do all the work to check it out only to find we can't
		// add the replace directive.
		if err := checkCanReplace(modf, mpath); err != nil {
			errorf("%v", err)
			continue
		}
		if m.Replace != nil && m.Replace.Path == m.Replace.Dir {
			// TODO if -u flag specified, update to the current version instead of printing an error.
			errorf("%q is already replaced by %q - are you already gohacking it?", mpath, m.Replace.Dir)
			continue
		}
		var repl *modReplace
		if *getVCS {
			repl1, err := updateVCSDir(m)
			if err != nil {
				errorf("cannot update VCS dir for %s: %v", m.Path, err)
				continue
			}
			repl = repl1
		} else {
			repl1, err := updateFromLocalDir(m)
			if err != nil {
				errorf("cannot update %s from local cache: %v", m.Path, err)
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
	if err := replace(modf, repls); err != nil {
		return errors.Notef(err, nil, "cannot replace")
	}
	if err := writeModFile(modf); err != nil {
		return errors.Wrap(err)
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
	srcHash, err := hashDir(m.Dir, m.Path)
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
		if err := copyAll(destDir, m.Dir); err != nil {
			return nil, errors.Wrap(err)
		}
	} else {
		if !*getForce {
			// Destination already exists; try to update it.
			isEmpty, err := isEmptyDir(destDir)
			if err != nil {
				return nil, errors.Wrap(err)
			}
			if !isEmpty {
				// The destination directory already exists and has something in.
				destHash, err := checkCleanWithoutVCS(destDir, m.Path)
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

func checkCleanWithoutVCS(dir string, modulePath string) (hash string, err error) {
	wantHash, err := readHashFile(dir)
	if err != nil {
		if !os.IsNotExist(errors.Cause(err)) {
			return "", errors.Wrap(err)
		}
		return "", errors.Newf("%q already exists; not overwriting", dir)
	}
	gotHash, err := hashDir(dir, modulePath)
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
	if err := copyAll(destDir, srcDir); err != nil {
		return errors.Wrap(err)
	}
	return nil
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
		return nil, errors.Notef(err, nil, "cannot get info")
	}
	if err := updateModule(info); err != nil {
		return nil, errors.Wrap(err)
	}
	return &modReplace{
		modulePath: m.Path,
		dir:        info.dir,
	}, nil
}

type modReplace struct {
	modulePath string
	dir        string
}

func replace(f *modfile.File, repls []*modReplace) error {
	for _, repl := range repls {
		// TODO should we use relative path here?
		if err := replaceModule(f, repl.modulePath, repl.dir); err != nil {
			return errors.Wrap(err)
		}
	}
	return nil
}

func updateModule(info *moduleVCSInfo) error {
	// Remove an auto-generated go.mod file if there is one
	// to avoid confusing VCS logic.
	if _, err := removeAutoGoMod(info); err != nil {
		return errors.Wrap(err)
	}
	if info.alreadyExists && !info.clean && *getForce {
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

func ensureGoModFile(modPath, dir string) error {
	goModPath := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		return nil
	}
	if err := ioutil.WriteFile(goModPath, []byte(autoGoMod(modPath)), 0666); err != nil {
		return errors.Wrap(err)
	}
	return nil
}

// removeAutoGoMod removes the module directory's go.mod
// file if it looks like it's been autogenerated by us.
// It reports whether the file was removed.
func removeAutoGoMod(m *moduleVCSInfo) (bool, error) {
	goModPath := filepath.Join(m.dir, "go.mod")
	ok, err := isAutoGoMod(goModPath, m.module.Path)
	if err != nil || !ok {
		return false, err
	}
	if err := os.Remove(goModPath); err != nil {
		return false, errors.Wrap(err)
	}
	return true, nil
}

// isAutoGoMod reports whether the file at path
// looks like it's a go.mod file auto-generated by gohack.
func isAutoGoMod(path string, modulePath string) (bool, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, errors.Wrap(err)
		}
		return false, nil
	}
	if string(data) != autoGoMod(modulePath) {
		return false, nil
	}
	return true, nil
}

// autoGoMod returns the contents of the go.mod file that
// would be auto-generated for the module with the given
// path.
func autoGoMod(mpath string) string {
	return "// Generated by gohack; DO NOT EDIT.\nmodule " + mpath + "\n"
}

// checkCanReplace checks whether it may be possible to replace
// the module in the given go.mod file.
func checkCanReplace(f *modfile.File, mod string) error {
	var found *modfile.Replace
	for _, r := range f.Replace {
		if r.Old.Path != mod {
			continue
		}
		if found != nil {
			return errors.Newf("found multiple existing replacements for %q", mod)
		}
		if r.New.Version == "" {
			return errors.Newf("%s is already replaced by %s", mod, r.New.Path)
		}
	}
	return nil
}

// replaceModule adds or modifies a replace statement in f for mod
// to be replaced with dir.
func replaceModule(f *modfile.File, mod string, dir string) error {
	var found *modfile.Replace
	for _, r := range f.Replace {
		if r.Old.Path != mod {
			continue
		}
		// These checks shouldn't fail when checkCanReplace has been
		// called previously, but check anyway just to be sure.
		if found != nil || r.New.Version == "" {
			panic(errors.Newf("unexpected bad replace for %q (checkCanReplace not called?)", mod))
		}
		found = r
	}
	if found == nil {
		// No existing replace statement. Just add a new one.
		if err := f.AddReplace(mod, "", dir, ""); err != nil {
			return errors.Wrap(err)
		}
		return nil
	}
	// There's an existing replacement for the same target, so modify it
	// but preserve the original replacement information around in a comment.
	token := fmt.Sprintf("// was %s => %s", versionPath(found.Old), versionPath(found.New))
	comments := &found.Syntax.Comments
	if len(comments.Suffix) > 0 {
		// There's already a comment, so preserve it.
		comments.Suffix[0].Token = token + " " + comments.Suffix[0].Token
	} else {
		comments.Suffix = []modfile.Comment{{
			Token: token,
		}}
	}
	found.Old.Version = ""
	found.New.Path = dir
	found.New.Version = ""
	if !found.Syntax.InBlock {
		found.Syntax.Token = []string{"replace"}
	} else {
		found.Syntax.Token = nil
	}
	found.Syntax.Token = append(found.Syntax.Token, []string{
		mod,
		"=>",
		dir,
	}...)
	return nil
}

func versionPath(v module.Version) string {
	if v.Version == "" {
		return v.Path
	}
	return v.Path + " " + v.Version
}
