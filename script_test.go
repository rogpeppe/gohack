package main

import (
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/rogpeppe/modinternal/goproxytest"
	"github.com/rogpeppe/modinternal/gotooltest"
	"github.com/rogpeppe/modinternal/testscript"
)

var proxyURL string

func TestMain(m *testing.M) {
	testscript.RegisterCommand("gohack", main)
	if os.Getenv("GO_GCFLAGS") != "" {
		fmt.Fprintf(os.Stderr, "testing: warning: no tests to run\n") // magic string for cmd/go
		fmt.Printf("cmd/go test is not compatible with $GO_GCFLAGS being set\n")
		fmt.Printf("SKIP\n")
		return
	}
	os.Unsetenv("GOROOT_FINAL")

	srv, err := goproxytest.NewServer("testdata/mod", "")
	if err != nil {
		log.Fatal(err)
	}
	proxyURL = srv.URL

	os.Exit(m.Run())
}

func TestScripts(t *testing.T) {
	p := testscript.Params{
		Dir: "testdata",
		Setup: func(e *testscript.Env) error {
			e.Vars = append(e.Vars, "GOPROXY="+proxyURL)
			return nil
		},
	}
	if err := gotooltest.Setup(&p); err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, p)
}
