// +build go1.12

package main

import "os"

// UserHomeDir was introduced in Go 1.12. When we drop support for Go 1.11, we can
// lose this file.

func UserHomeDir() (string, error) {
	return os.UserHomeDir()
}
