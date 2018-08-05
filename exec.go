package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"gopkg.in/errgo.v2/fmt/errors"
)

func runCmd(dir string, name string, args ...string) (string, error) {
	var outData, errData bytes.Buffer
	if *printCommands {
		printShellCommand(dir, name, args)
	}
	c := exec.Command(name, args...)
	c.Stdout = &outData
	c.Stderr = &errData
	c.Dir = dir
	err := c.Run()
	if err == nil {
		return outData.String(), nil
	}
	if _, ok := err.(*exec.ExitError); ok && errData.Len() > 0 {
		return "", errors.New(strings.TrimSpace(errData.String()))
	}
	return "", fmt.Errorf("cannot run %q: %v", append([]string{name}, args...), err)
}

var (
	outputDirMutex sync.Mutex
	outputDir      string
)

func printShellCommand(dir, name string, args []string) {
	outputDirMutex.Lock()
	defer outputDirMutex.Unlock()
	if dir != outputDir {
		fmt.Fprintf(os.Stderr, "cd %s\n", shquote(dir))
		outputDir = dir
	}
	var buf bytes.Buffer
	buf.WriteString(name)
	for _, arg := range args {
		buf.WriteString(" ")
		buf.WriteString(shquote(arg))
	}
	fmt.Fprintf(os.Stderr, "%s\n", buf.Bytes())
}

func shquote(s string) string {
	// single-quote becomes single-quote, double-quote, single-quote, double-quote, single-quote
	return `'` + strings.Replace(s, `'`, `'"'"'`, -1) + `'`
}
