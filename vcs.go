package main

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var kindToVCS = map[string]VCS{
	"bzr": bzrVCS{},
	"hg":  hgVCS{},
	"git": gitVCS{},
}

type VCS interface {
	Kind() string
	Info(dir string) (VCSInfo, error)
	Update(dir string, isTag bool, revid string) error
	Clean(dir string) error
	Create(repo, rootDir string) error
	Fetch(dir string) error
}

type VCSInfo struct {
	revid string
	revno string // optional
	clean bool
}

type gitVCS struct{}

func (gitVCS) Kind() string {
	return "git"
}

func (gitVCS) Info(dir string) (VCSInfo, error) {
	out, err := runCmd(dir, "git", "log", "-n", "1", "--pretty=format:%H %ct", "HEAD")
	if err != nil {
		return VCSInfo{}, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return VCSInfo{}, fmt.Errorf("unexpected git log output %q", out)
	}
	revid := fields[0]
	// validate the revision hash
	revhash, err := hex.DecodeString(revid)
	if err != nil || len(revhash) == 0 {
		return VCSInfo{},
			fmt.Errorf("git rev-parse provided invalid revision %q", revid)
	}
	unixTime, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return VCSInfo{},
			fmt.Errorf("git rev-parse provided invalid time %q", fields[1])
	}

	// `git status --porcelain` outputs one line per changed or untracked file.
	out, err = runCmd(dir, "git", "status", "--porcelain")
	if err != nil {
		return VCSInfo{}, err
	}
	return VCSInfo{
		revid: revid,
		// Empty output (with rc=0) indicates no changes in working copy.
		clean: out == "",
		revno: time.Unix(unixTime, 0).UTC().Format(time.RFC3339),
	}, nil
}

func (gitVCS) Create(repo, rootDir string) error {
	_, err := runUpdateCmd("", "git", "clone", repo, rootDir)
	return err
}

func (gitVCS) Update(dir string, isTag bool, revid string) error {
	_, err := runUpdateCmd(dir, "git", "checkout", revid)
	return err
}

func (gitVCS) Clean(dir string) error {
	_, err := runUpdateCmd(dir, "git", "reset", "--hard", "HEAD")
	return err
}

func (gitVCS) Fetch(dir string) error {
	_, err := runCmd(dir, "git", "fetch")
	return err
}

type bzrVCS struct{}

func (bzrVCS) Kind() string {
	return "bzr"
}

var validBzrInfo = regexp.MustCompile(`^([0-9.]+) ([^ \t]+)$`)
var shelveLine = regexp.MustCompile(`^[0-9]+ (shelves exist|shelf exists)\.`)

func (bzrVCS) Info(dir string) (VCSInfo, error) {
	out, err := runCmd(dir, "bzr", "revision-info", "--tree")
	if err != nil {
		return VCSInfo{}, err
	}
	m := validBzrInfo.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return VCSInfo{}, fmt.Errorf("bzr revision-info has unexpected result %q", out)
	}

	out, err = runCmd(dir, "bzr", "status", "-S")
	if err != nil {
		return VCSInfo{}, err
	}
	clean := true
	statusLines := strings.Split(out, "\n")
	for _, line := range statusLines {
		if line == "" || shelveLine.MatchString(line) {
			continue
		}
		clean = false
		break
	}
	return VCSInfo{
		revid: m[2],
		revno: m[1],
		clean: clean,
	}, nil
}

func (bzrVCS) Create(repo, rootDir string) error {
	_, err := runUpdateCmd("", "bzr", "branch", repo, rootDir)
	return err
}

func (bzrVCS) Clean(dir string) error {
	_, err := runUpdateCmd(dir, "bzr", "revert")
	return err
}

func (bzrVCS) Update(dir string, isTag bool, to string) error {
	if isTag {
		to = "tag:" + to
	} else {
		to = "revid:" + to
	}
	_, err := runUpdateCmd(dir, "bzr", "update", "-r", to)
	return err
}

func (bzrVCS) Fetch(dir string) error {
	_, err := runCmd(dir, "bzr", "pull")
	return err
}

var validHgInfo = regexp.MustCompile(`^([a-f0-9]+) ([0-9]+)$`)

type hgVCS struct{}

func (hgVCS) Info(dir string) (VCSInfo, error) {
	out, err := runCmd(dir, "hg", "log", "-l", "1", "-r", ".", "--template", "{node} {rev}")
	if err != nil {
		return VCSInfo{}, err
	}
	m := validHgInfo.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return VCSInfo{}, fmt.Errorf("hg identify has unexpected result %q", out)
	}
	out, err = runCmd(dir, "hg", "status")
	if err != nil {
		return VCSInfo{}, err
	}
	// TODO(rog) check that tree is clean
	return VCSInfo{
		revid: m[1],
		revno: m[2],
		clean: out == "",
	}, nil
}

func (hgVCS) Kind() string {
	return "hg"
}

func (hgVCS) Create(repo, rootDir string) error {
	_, err := runUpdateCmd("", "hg", "clone", "-U", repo, rootDir)
	return err
}

func (hgVCS) Clean(dir string) error {
	_, err := runUpdateCmd(dir, "hg", "revert", "--all")
	return err
}

func (hgVCS) Update(dir string, isTag bool, revid string) error {
	_, err := runUpdateCmd(dir, "hg", "update", revid)
	return err
}

func (hgVCS) Fetch(dir string) error {
	_, err := runCmd(dir, "hg", "pull")
	return err
}

func runUpdateCmd(dir string, name string, args ...string) (string, error) {
	if *dryRun {
		printShellCommand(dir, name, args)
		return "", nil
	}
	return runCmd(dir, name, args...)
}
