package toolkit

import (
	"flag"
	"fmt"
	"io"
	"os"

	"git-tools/diffwalk"
)

func runDiffWalk(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var input, format, search string
	var fileIndex, hunkIndex int
	var summaryOnly bool
	fs := flag.NewFlagSet("diff-walk", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&input, "input", "-", "unified diff file; use - for standard input")
	fs.StringVar(&format, "format", "text", "output format: text or json")
	fs.IntVar(&fileIndex, "file", 0, "show one file by its one-based index")
	fs.IntVar(&hunkIndex, "hunk", 0, "show one hunk by its one-based index; requires --file")
	fs.StringVar(&search, "search", "", "show hunks with matching added or removed lines")
	fs.BoolVar(&summaryOnly, "summary-only", false, "show only the text summary")
	setAccessibleUsage(fs, "diff-walk", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("unknown format %q; expected text or json", format)
	}
	reader := stdin
	if input != "-" {
		file, err := os.Open(input)
		if err != nil {
			return fmt.Errorf("open input %q: %w", input, err)
		}
		defer file.Close()
		reader = file
	}
	diff, err := diffwalk.Parse(reader)
	if err != nil {
		return fmt.Errorf("parse input: %w", err)
	}
	diff, err = diff.Select(fileIndex, hunkIndex)
	if err != nil {
		return fmt.Errorf("select diff: %w", err)
	}
	if search != "" {
		diff, err = diff.Search(search)
		if err != nil {
			return fmt.Errorf("search diff: %w", err)
		}
	}
	if format == "json" {
		return diffwalk.RenderJSON(stdout, diff)
	}
	return diffwalk.RenderText(stdout, diff, summaryOnly)
}
