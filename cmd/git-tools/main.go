package main

import (
	"os"
	"path/filepath"

	"git-tools/toolkit"
)

func main() {
	command := filepath.Base(os.Args[0])
	os.Exit(toolkit.Run(command, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
