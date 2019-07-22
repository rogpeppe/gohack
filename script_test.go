package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/goproxytest"
	"github.com/rogpeppe/go-internal/gotooltest"
	"github.com/rogpeppe/go-internal/testscript"
)

var proxyURL string

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(gohackMain{m}, map[string]func() int{
		"gohack": main1,
	}))
}

type gohackMain struct {
	m *testing.M
}

func (m gohackMain) Run() int {
	if os.Getenv("GO_GCFLAGS") != "" {
		fmt.Fprintf(os.Stderr, "testing: warning: no tests to run\n") // magic string for cmd/go
		fmt.Printf("cmd/go test is not compatible with $GO_GCFLAGS being set\n")
		fmt.Printf("SKIP\n")
		return 0
	}
	os.Unsetenv("GOROOT_FINAL")

	// Start the Go proxy server running for all tests.
	srv, err := goproxytest.NewServer("testdata/mod", "")
	if err != nil {
		errorf("cannot start proxy: %v", err)
		return 1
	}
	proxyURL = srv.URL

	return m.m.Run()
}

func TestScripts(t *testing.T) {
	p := testscript.Params{
		Dir: "testdata",
		Setup: func(e *testscript.Env) error {
			e.Vars = append(e.Vars,
				"GOPROXY="+proxyURL,
				"GONOSUMDB=*",
			)
			return nil
		},
	}
	if err := gotooltest.Setup(&p); err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, p)
}
