package toolkit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"git-tools/finding"
)

func runReviewBrief(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_, options, err := parseFlags("review-brief", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		// The brief is a summary meant to be skimmed, so it wraps narrower than
		// the common default. Override rather than re-register: registering a
		// second `width` on the same FlagSet panics.
		overrideIntDefault(fs, &options.width, "width", 80)
		return &options
	})
	if err != nil {
		return err
	}
	if options.format != "text" {
		return fmt.Errorf("review-brief supports text output only")
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	var envelope struct {
		Status           finding.Status    `json:"status"`
		Findings         []finding.Finding `json:"findings"`
		CompletedChecks  []string          `json:"completed_checks"`
		IncompleteChecks []string          `json:"incomplete_checks"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("parse finding report: %w", err)
	}
	report := finding.Report{Findings: envelope.Findings, CompletedChecks: envelope.CompletedChecks, IncompleteChecks: envelope.IncompleteChecks}
	if err := report.Validate(); err != nil {
		return fmt.Errorf("invalid finding report: %w", err)
	}
	fmt.Fprintf(stdout, "REVIEW RESULT: %s\n", strings.ToUpper(string(report.Status())))
	fmt.Fprintf(stdout, "FINDINGS: %d\n", len(report.Findings))
	for index, item := range report.Findings {
		writeWrapped(stdout, fmt.Sprintf("%d. %s %s: %s", index+1, strings.ToUpper(string(item.Severity)), item.ID, item.Title), options.width)
		writeWrapped(stdout, "   Target: "+item.Resource+" in "+item.Environment, options.width)
		writeWrapped(stdout, "   Next: "+item.Remediation, options.width)
	}
	for index, incomplete := range report.IncompleteChecks {
		writeWrapped(stdout, fmt.Sprintf("Incomplete %d: %s", index+1, incomplete), options.width)
	}
	if report.Status() != finding.StatusClean {
		return reportError{status: report.Status()}
	}
	return nil
}

func writeWrapped(w io.Writer, value string, width int) {
	indent := ""
	if strings.HasPrefix(value, "   ") {
		indent = "   "
	}
	words := strings.Fields(value)
	line := ""
	for _, word := range words {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		} else if indent != "" {
			candidate = indent + word
		}
		if len([]rune(candidate)) > width && line != "" {
			fmt.Fprintln(w, line)
			line = ""
			candidate = indent + word
		}
		if len([]rune(candidate)) <= width {
			line = candidate
			continue
		}
		wordRunes := []rune(word)
		available := width - len([]rune(indent))
		for len(wordRunes) > available {
			fmt.Fprintln(w, indent+string(wordRunes[:available]))
			wordRunes = wordRunes[available:]
		}
		if len(wordRunes) > 0 {
			line = indent + string(wordRunes)
		}
	}
	if line != "" {
		fmt.Fprintln(w, line)
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func runA11yOutputCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var maxLine int
	_, options, err := parseFlags("a11y-output-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.IntVar(&maxLine, "max-line", 100, "maximum line length for magnified text")
		return &options
	})
	if err != nil {
		return err
	}
	if maxLine < 40 {
		return fmt.Errorf("--max-line must be at least 40")
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	resource := basename(options.input)
	if resource == "-" {
		resource = "standard-input"
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"CLI output accessibility review"}, IncompleteChecks: []string{}}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if ansiPattern.MatchString(line) {
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "A11Y-ANSI", resource, line), finding.SeverityWarning,
				"Output contains terminal control sequences", resource, "read output", options.environment,
				"Color or cursor controls can create noisy or misleading screen-reader output.", fmt.Sprintf("line %d contains ANSI escape bytes", lineNumber),
				"Provide a plain-text or --no-color mode and never use color as the only signal.",
			))
		}
		if strings.ContainsRune(line, '\t') {
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "A11Y-TAB", resource, line), finding.SeverityWarning,
				"Output contains tab-based alignment", resource, "read output", options.environment,
				"Tab alignment is unpredictable with magnification and speech navigation.", fmt.Sprintf("line %d contains a tab", lineNumber),
				"Use labeled fields on separate lines or ordinary spaces.",
			))
		}
		if len([]rune(line)) > maxLine {
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "A11Y-WIDTH", resource, line), finding.SeverityWarning,
				"Output line is difficult to read when magnified", resource, "read output", options.environment,
				fmt.Sprintf("The line exceeds the configured width of %d characters.", maxLine), fmt.Sprintf("line %d length %d", lineNumber, len([]rune(line))),
				"Wrap prose and put labels and values on predictable lines.",
			))
		}
	}
	return emitReportOptions(stdout, options, report)
}

type evidenceArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

func runEvidencePack(args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var files stringList
	var commit, changeID string
	fs := flag.NewFlagSet("evidence-pack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&files, "file", "review artifact to hash; may be repeated")
	fs.StringVar(&commit, "commit", "", "reviewed commit SHA")
	fs.StringVar(&changeID, "change-id", "", "ticket, pull request, or change identifier")
	setAccessibleUsage(fs, "evidence-pack", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if len(files) == 0 || commit == "" || changeID == "" {
		return fmt.Errorf("--file, --commit, and --change-id are required")
	}
	artifacts := make([]evidenceArtifact, 0, len(files))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read evidence %q: %w", path, err)
		}
		sum := sha256.Sum256(data)
		artifacts = append(artifacts, evidenceArtifact{Path: filepath.Base(path), SHA256: hex.EncodeToString(sum[:]), Bytes: len(data)})
	}
	envelope := struct {
		SchemaVersion string             `json:"schema_version"`
		ChangeID      string             `json:"change_id"`
		Commit        string             `json:"commit"`
		Artifacts     []evidenceArtifact `json:"artifacts"`
	}{"1", changeID, commit, artifacts}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}
