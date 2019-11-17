# Gohack: mutable checkouts of Go module dependencies

The new Go module system is awesome. It ensures repeatable, deterministic
builds of Go code. External module code is cached locally in a read-only
directory, which is great for reproducibility. But if you're used to the
global mutable namespace that is `$GOPATH`, there's an obvious question:
what if I'm hacking on my program and I *want* to change one of those
external modules?

You might want to put a sneaky `log.Printf` statement to find out how
some internal data structure works, or perhaps try out a bug fix to see
if it solves your latest problem. But since all those external modules
are in read-only directories, it's hard to change them. And you really
don't want to change them anyway, because that will break the integrity
checking that the Go tool does when building.

Luckily the modules system provides a way around this: you can add a
`replace` statement to the `go.mod` file which substitutes the contents
of a directory holding a module for the readonly cached copy. You can of
course do this manually, but gohack aims to make this process pain-free.

## Install gohack with

	go get github.com/rogpeppe/gohack

or use [`gobin`](https://github.com/myitcv/gobin):

	gobin github.com/rogpeppe/gohack

## For quick edits to a module (without version control information)
If the module to edit is `example.com/foo/bar`, run:

	gohack get example.com/foo/bar

This will make a _copy_ of the module into `$HOME/gohack/example.com/foo/bar` and
add replace directives to the local `go.mod` file:

	replace example.com/foo/bar => /home/rog/gohack/example.com/foo/bar

__Note__: This copy will __not__ include version control system information so
it is best for quick edits that aren't intended to land back into version control.

## To edit the module with full version control
Run:

	gohack get -vcs example.com/foo/bar

This will _clone_ the module's repository to
`$HOME/gohack/example.com/foo/bar`, check out the correct version of the
source code there and add the replace directive into the local `go.mod` file.

## Undoing replacements

Once you are done hacking and wish to revert to the immutable version, you
can remove the replace statement with:

	gohack undo example.com/foo/bar

or you can remove all gohack replace statements with:

	gohack undo

Note that undoing a replace does *not* remove the external module's
directory - that stays around so your changes are not lost. For example,
you might wish to turn that bug fix into an upstream PR.

If you run gohack on a module that already has a directory, gohack will
try to check out the current version without recreating the repository,
but only if the directory is clean - it won't overwrite your changes
until you've committed or undone them.
