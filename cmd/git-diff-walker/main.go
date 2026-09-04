package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"git-tools/diffwalk"
)

const version = "git-diff-walker 0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("git-diff-walker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "-", "read a unified diff from this file; use - for standard input")
	format := flags.String("format", "text", "output format: text or json")
	fileIndex := flags.Int("file", 0, "show one file by its one-based index")
	hunkIndex := flags.Int("hunk", 0, "show one hunk by its one-based index; requires --file")
	search := flags.String("search", "", "show hunks with matching added or removed lines")
	summaryOnly := flags.Bool("summary-only", false, "show only the text summary")
	showVersion := flags.Bool("version", false, "print the version and exit")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: git-diff-walker [options]")
		fmt.Fprintln(stderr, "Example: git diff | git-diff-walker --file 1 --hunk 2")
		flags.VisitAll(func(option *flag.Flag) {
			fmt.Fprintf(stderr, "\nOption: --%s\n", option.Name)
			fmt.Fprintf(stderr, "Description: %s\n", option.Usage)
			if option.DefValue != "" && option.DefValue != "false" && option.DefValue != "0" {
				fmt.Fprintf(stderr, "Default: %s\n", option.DefValue)
			}
		})
	}

	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected arguments: %v\n", flags.Args())
		flags.Usage()
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "error: unknown format %q; expected text or json\n", *format)
		return 2
	}

	reader := stdin
	var inputFile *os.File
	if *inputPath != "-" {
		var err error
		inputFile, err = os.Open(*inputPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: open input %q: %v\n", *inputPath, err)
			return 2
		}
		defer inputFile.Close()
		reader = inputFile
	}

	diff, err := diffwalk.Parse(reader)
	if err != nil {
		fmt.Fprintf(stderr, "error: parse input: %v\n", err)
		return 2
	}
	diff, err = diff.Select(*fileIndex, *hunkIndex)
	if err != nil {
		fmt.Fprintf(stderr, "error: select diff: %v\n", err)
		return 2
	}
	if *search != "" {
		diff, err = diff.Search(*search)
		if err != nil {
			fmt.Fprintf(stderr, "error: search diff: %v\n", err)
			return 2
		}
	}

	if *format == "json" {
		err = diffwalk.RenderJSON(stdout, diff)
	} else {
		err = diffwalk.RenderText(stdout, diff, *summaryOnly)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: render output: %v\n", err)
		return 1
	}
	return 0
}
