package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"git-tools/finding"
)

// Threats: pictures and PDFs in a change ticket are easy to skip when the
// reader cannot see them. see runs imgsee or pdfsee and keeps the result in
// the finding contract. It does not describe what a picture shows, and it
// does not treat extracted text as the whole document. Extractor diagnostics
// and terminal controls are withheld. A missing tool or a truncated extract
// is incomplete.

func runSee(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("see", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "image or PDF file")
	tool := fs.String("tool", "", "imgsee or pdfsee path; default is PATH")
	format := fs.String("format", "text", "text or json")
	width := fs.Int("width", 72, "line width; minimum 40")
	text := fs.Bool("text", false, "for a PDF, include a bounded text extract")
	setAccessibleUsage(fs, "see", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *input == "" || *input == "-" || strings.HasPrefix(*input, "-") || !oneOf(*format, "text", "json") || *width < 40 {
		return fmt.Errorf("see requires --input pointing at an image or PDF")
	}
	info, err := os.Stat(*input)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 32<<20 {
		return fmt.Errorf("see requires a regular file of at most 32 MiB")
	}
	kind := ""
	switch strings.ToLower(filepath.Ext(*input)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".tif", ".tiff":
		kind = "image"
	case ".pdf":
		kind = "pdf"
	default:
		return fmt.Errorf("see reads images and PDFs; use markdown-view or to-markdown for text")
	}
	if *text && kind != "pdf" {
		return fmt.Errorf("--text applies to a PDF")
	}
	look := "imgsee"
	if kind == "pdf" {
		look = "pdfsee"
	}
	command := *tool
	if command == "" {
		found, lookErr := exec.LookPath(look)
		if lookErr != nil {
			report := finding.Report{IncompleteChecks: []string{look + " is not on PATH. Install shell2see or pass --tool. File content was not read."}}
			return emitReport(stdout, *format, *width, report)
		}
		command = found
	}
	result := executeReadOnly(command, *input, "info")
	if result.err != nil || ansiPattern.Match(result.stdout) || strings.Contains(result.stderr, "\x1b") {
		report := finding.Report{IncompleteChecks: []string{look + " did not return usable metadata. Extractor diagnostics were withheld."}}
		return emitReport(stdout, *format, *width, report)
	}
	report := finding.Report{CompletedChecks: []string{}}
	base := filepath.Base(*input)
	if kind == "image" {
		var meta struct {
			Format string `json:"format"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Mode   string `json:"mode"`
			Frames int    `json:"frames"`
		}
		if json.Unmarshal(result.stdout, &meta) != nil || !operationLabel(meta.Format) || !operationLabel(meta.Mode) || meta.Width < 1 || meta.Height < 1 || meta.Width > 100000 || meta.Height > 100000 || meta.Frames < 1 {
			report.IncompleteChecks = append(report.IncompleteChecks, "image metadata was incomplete")
			return emitReport(stdout, *format, *width, report)
		}
		report.CompletedChecks = append(report.CompletedChecks,
			"Image metadata for "+base+". This is not a description of what the picture shows.",
			fmt.Sprintf("Format %s. Width %d. Height %d. Mode %s. Frames %d.", meta.Format, meta.Width, meta.Height, meta.Mode, meta.Frames),
		)
		if meta.Frames > 1 {
			report.CompletedChecks = append(report.CompletedChecks, "Multi-frame image. Only metadata for the opened image was read.")
		}
		return emitReport(stdout, *format, *width, report)
	}
	pages, encrypted, title := pdfInfoFields(result.stdout)
	if pages < 1 {
		report.IncompleteChecks = append(report.IncompleteChecks, "PDF page count was not readable")
		return emitReport(stdout, *format, *width, report)
	}
	report.CompletedChecks = append(report.CompletedChecks, fmt.Sprintf("PDF metadata for %s. Pages %d. Encrypted %s.", base, pages, encrypted))
	if title != "" {
		report.CompletedChecks = append(report.CompletedChecks, "Title: "+title)
	}
	report.CompletedChecks = append(report.CompletedChecks, "PDF metadata is not a description of figures. Text drawn inside images was not read.")
	if encrypted == "yes" {
		report.IncompleteChecks = append(report.IncompleteChecks, "PDF is encrypted. Text was not extracted.")
		return emitReport(stdout, *format, *width, report)
	}
	if *text {
		extract := executeReadOnly(command, *input, "text")
		if extract.err != nil || ansiPattern.Match(extract.stdout) {
			report.IncompleteChecks = append(report.IncompleteChecks, "PDF text extract failed. Extractor diagnostics were withheld.")
			return emitReport(stdout, *format, *width, report)
		}
		lines := strings.Split(strings.ReplaceAll(string(extract.stdout), "\r\n", "\n"), "\n")
		kept := 0
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			kept++
			if kept > 40 {
				report.IncompleteChecks = append(report.IncompleteChecks, "PDF text was truncated to 40 non-empty lines")
				break
			}
			safe := redactLine(line)
			runes := []rune(safe)
			if len(runes) > 200 {
				safe = string(runes[:200])
			}
			report.CompletedChecks = append(report.CompletedChecks, "PDF line "+strconv.Itoa(kept)+": "+safe)
		}
		if kept == 0 {
			report.IncompleteChecks = append(report.IncompleteChecks, "PDF text extract was empty. The pages may be images.")
		}
	}
	return emitReport(stdout, *format, *width, report)
}

func pdfInfoFields(raw []byte) (pages int, encrypted, title string) {
	encrypted = "unknown"
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Pages":
			n, err := strconv.Atoi(value)
			if err == nil {
				pages = n
			}
		case "Encrypted":
			if oneOf(strings.ToLower(value), "yes", "no") {
				encrypted = strings.ToLower(value)
			}
		case "Title":
			if operationLabel(value) && auditText(value) == value {
				title = value
			}
		}
	}
	return pages, encrypted, title
}
