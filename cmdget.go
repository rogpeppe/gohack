package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/errgo.v2/fmt/errors"
)

var getCommand = &Command{
	UsageLine: "get [-vcs] [-u] [-f] [module...]",
}

func init() {
	getCommand.Run = runGet // break init cycle
}

var (
	getUpdate = getCommand.Flag.Bool("u", false, "update to current version")
	getForce  = getCommand.Flag.Bool("f", false, "force update to current version even if not clean")
	getVCS    = getCommand.Flag.Bool("vcs", false, "get VCS information too")
)

func runGet(cmd *Command, args []string) {
	if err := runGet1(args); err != nil {
		errorf("%v", err)
	}
}

func runGet1(args []string) error {
	if len(args) == 0 {
		return errors.Newf("get requires at least one module argument")
	}
	var repls []*modReplace
	mods, err := allModules()
	if err != nil {
		// TODO this happens when a replacement directory has been removed.
		// Perhaps we should be more resilient in that case?
		return errors.Notef(err, nil, "cannot get module info")
	}
	for _, mpath := range args {
		m := mods[mpath]
		if m == nil {
			errorf("module %q does not appear to be in use", mpath)
			continue
		}
		if m.Replace != nil {
			if m.Replace.Path == m.Replace.Dir {
				// TODO if -u flag specified, update to the current version instead of printing an error.
				errorf("%q is already replaced by %q - are you already gohacking it?", mpath, m.Replace.Dir)
			} else {
				errorf("%q is already replaced; will not override replace statement in go.mod", mpath)
			}
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
