package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rogpeppe/go-internal/modfile"
	"github.com/rogpeppe/go-internal/module"
	"github.com/rogpeppe/go-internal/semver"
	"gopkg.in/errgo.v2/fmt/errors"
)

var undoCommand = &Command{
	Run:       cmdUndo,
	Short:     "stop hacking a module",
	UsageLine: "undo [-rm] [-f] [module...]",
	Long: `
The undo command can be used to revert to the non-gohacked
module versions. It only removes the relevant replace
statements from the go.mod file - it does not change any
of the directories referred to. With no arguments, all replace
statements that refer to directories will
be removed.
`[1:],
}

var (
	undoRemove     = undoCommand.Flag.Bool("rm", false, "remove module directory too")
	undoForceClean = undoCommand.Flag.Bool("f", false, "force cleaning of modified-but-not-committed repositories. Do not use this flag unless you really need to!")
)

func cmdUndo(_ *Command, args []string) int {
	if err := cmdUndo1(args); err != nil {
		errorf("%v", err)
	}
	return 0
}

func cmdUndo1(modules []string) error {
	modMap := make(map[string]bool)
	if len(modules) > 0 {
		for _, m := range modules {
			modMap[m] = true
		}
	} else {
		// With no modules specified, we un-gohack all modules
		// we can find with local directory info in the go.mod file.
		for _, r := range mainModFile.Replace {
			if r.Old.Version == "" && r.New.Version == "" {
				modMap[r.Old.Path] = true
				modules = append(modules, r.Old.Path)
			}
		}
	}
	drop := make(map[string]bool)
	for _, r := range mainModFile.Replace {
		if !modMap[r.Old.Path] || r.Old.Version != "" || r.New.Version != "" {
			continue
		}
		// Found a replacement to drop.
		comments := r.Syntax.Comments
		if len(comments.Suffix) == 0 {
			// No comment; we can just drop it.
			drop[r.Old.Path] = true
			delete(modMap, r.Old.Path)
			continue
		}
		prevReplace := splitWasComment(comments.Suffix[0].Token)
		if prevReplace != nil && prevReplace.Old.Path == r.Old.Path {
			// We're popping the old replace statement.
			if r.Syntax.InBlock {
				// When we're in a block, we don't need the "replace" token.
				prevReplace.Syntax.Token = prevReplace.Syntax.Token[1:]
			}
			// Preserve any before and after comments.
			prevReplace.Syntax.Before = r.Syntax.Before
			prevReplace.Syntax.After = r.Syntax.After
			r.Old = prevReplace.Old
			r.New = prevReplace.New
			r.Syntax.Comments.Suffix = prevReplace.Syntax.Comments.Suffix
			r.Syntax.Token = prevReplace.Syntax.Token
		} else {
			// It's not a "was" comment. Just remove it (after this loop so we don't
			// interfere with the current range statement).
			drop[r.Old.Path] = true
		}
		delete(modMap, r.Old.Path)
	}
	for m := range drop {
		if err := mainModFile.DropReplace(m, ""); err != nil {
			return errors.Notef(err, nil, "cannot drop replacement for %v", m)
		}
	}
	failed := make([]string, 0, len(modMap))
	for m := range modMap {
		failed = append(failed, m)
	}
	sort.Strings(failed)
	for _, m := range failed {
		errorf("%s not currently replaced; cannot drop", m)
	}
	if err := writeModFile(mainModFile); err != nil {
		return errors.Wrap(err)
	}
	for _, m := range modules {
		fmt.Printf("dropped %s\n", m)
	}
	return nil
}

// wasCommentPat matches a comment of the form inserted by gohack get,
// for example:
//	// was example.com v1.2.3 => foo.com v1.3.4 // original comment
var wasCommentPat = regexp.MustCompile(`^// was ([^ ]+(?: [^ ]+)?) => ([^ ]+(?: [^ ]+)?)(?: (//.+))?$`)

func splitWasComment(s string) *modfile.Replace {
	parts := wasCommentPat.FindStringSubmatch(s)
	if parts == nil {
		return nil
	}
	old, ok := splitPathVersion(parts[1])
	if !ok {
		return nil
	}
	new, ok := splitPathVersion(parts[2])
	if !ok {
		return nil
	}
	oldComment := parts[3]
	r := &modfile.Replace{
		Old: old,
		New: new,
		Syntax: &modfile.Line{
			Token: tokensForReplace(old, new),
		},
	}
	if oldComment != "" {
		r.Syntax.Comments.Suffix = []modfile.Comment{{
			Token:  oldComment,
			Suffix: true,
		}}
	}
	return r
}

func tokensForReplace(old, new module.Version) []string {
	tokens := make([]string, 0, 6)
	tokens = append(tokens, "replace")
	tokens = append(tokens, old.Path)
	if old.Version != "" {
		tokens = append(tokens, old.Version)
	}
	tokens = append(tokens, "=>")
	tokens = append(tokens, new.Path)
	if new.Version != "" {
		tokens = append(tokens, new.Version)
	}
	return tokens
}

func splitPathVersion(s string) (module.Version, bool) {
	fs := strings.Fields(s)
	if len(fs) != 1 && len(fs) != 2 {
		return module.Version{}, false
	}
	v := module.Version{
		Path: fs[0],
	}
	if len(fs) > 1 {
		if !semver.IsValid(fs[1]) {
			return module.Version{}, false
		}
		v.Version = fs[1]
	}
	return v, true
}
