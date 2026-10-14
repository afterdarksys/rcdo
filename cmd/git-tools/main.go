package main

import (
	"os"
	"path/filepath"

	"github.com/afterdarksys/rcdo/toolkit"
)

func main() {
	command := filepath.Base(os.Args[0])
	os.Exit(toolkit.Run(command, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
