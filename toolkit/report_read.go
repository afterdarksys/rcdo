package toolkit

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"git-tools/finding"
)

// speakIdentifier describes case and punctuation without normalizing the identifier.
func speakIdentifier(s string) string {
	names := map[rune]string{'-': "hyphen", '_': "underscore", '.': "dot", '/': "slash", ':': "colon", '@': "at", ' ': "space", '[': "left bracket", ']': "right bracket", '(': "left parenthesis", ')': "right parenthesis", '"': "double quote", '\\': "backslash", '=': "equals", '+': "plus"}
	parts := []string{}
	for _, r := range s {
		if n, ok := names[r]; ok {
			parts = append(parts, n)
		} else if r >= 'A' && r <= 'Z' {
			parts = append(parts, "capital "+string(r))
		} else if r >= 'a' && r <= 'z' {
			parts = append(parts, "lowercase "+string(r))
		} else if r >= '0' && r <= '9' {
			parts = append(parts, "digit "+string(r))
		} else {
			parts = append(parts, fmt.Sprintf("Unicode U+%04X", r))
		}
	}
	return strings.Join(parts, ", ")
}
func safeReportText(s string) string { return markdownSafe(redactAIText(s)) }
func runReportRead(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report-read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "-", "versioned RCDO report JSON")
	layout := fs.String("layout", "plain", "plain, speech or braille")
	id := fs.String("id", "", "exact finding ID to read; full status and gaps remain visible")
	spell := fs.String("spell", "", "spell selected finding's id or resource; requires --id")
	width := fs.Int("width", 0, "line width; defaults 72 plain, 80 speech, 40 braille; minimum 40")
	setAccessibleUsage(fs, "report-read", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(*layout, "plain", "speech", "braille") || (*spell != "" && (!oneOf(*spell, "id", "resource") || *id == "")) {
		return fmt.Errorf("invalid report reading options")
	}
	if *width == 0 {
		*width = 72
		if *layout == "speech" {
			*width = 80
		}
		if *layout == "braille" {
			*width = 40
		}
	}
	if *width < 40 {
		return fmt.Errorf("width must be at least 40")
	}
	var data []byte
	var err error
	if *input == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 16<<20+1))
	} else {
		data, err = readConfigSource(*input)
	}
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("report exceeds 16 MiB")
	}
	var envelope struct {
		SchemaVersion string            `json:"schema_version"`
		Status        finding.Status    `json:"status"`
		Summary       finding.Summary   `json:"summary"`
		Findings      []finding.Finding `json:"findings"`
		Completed     []string          `json:"completed_checks"`
		Incomplete    []string          `json:"incomplete_checks"`
	}
	if strictJSON(data, &envelope) != nil {
		return fmt.Errorf("invalid report JSON")
	}
	r, err := decodeSessionReport(data)
	if err != nil {
		return err
	}
	selected := []finding.Finding{}
	for _, f := range r.Findings {
		if *id == "" || f.ID == *id {
			selected = append(selected, f)
		}
	}
	if *id != "" && len(selected) == 0 {
		return fmt.Errorf("finding ID not found")
	}
	rank := map[finding.Severity]int{finding.SeverityCritical: 4, finding.SeverityHigh: 3, finding.SeverityWarning: 2, finding.SeverityInfo: 1}
	sort.SliceStable(selected, func(i, j int) bool { return rank[selected[i].Severity] > rank[selected[j].Severity] })
	var output bytes.Buffer
	line := func(s string) { writeWrapped(&output, safeReportText(s), *width) }
	line("Report status: " + string(r.Status()))
	summary := r.Summary()
	line(fmt.Sprintf("Findings: %d total; %d critical; %d high; %d warning; %d info. Reading %d findings.", summary.Total, summary.Critical, summary.High, summary.Warning, summary.Info, len(selected)))
	for i, gap := range r.IncompleteChecks {
		line(fmt.Sprintf("Missing evidence %d: %s", i+1, gap))
	}
	for i, f := range selected {
		line(fmt.Sprintf("Finding %d of %d. Severity: %s", i+1, len(selected), f.Severity))
		if *layout == "speech" {
			line("Finding identifier: " + f.ID)
			line(f.Title)
			line("Affected resource: " + f.Resource)
			line("Action: " + f.Action + ". Environment: " + f.Environment)
			line("Reason: " + f.Reason)
			line("Confidence: " + string(f.Confidence))
			line("Next action: " + f.Remediation)
		} else {
			for _, field := range []struct{ label, value string }{{"ID", f.ID}, {"Title", f.Title}, {"Resource", f.Resource}, {"Action", f.Action}, {"Environment", f.Environment}, {"Reason", f.Reason}, {"Confidence", string(f.Confidence)}, {"Next", f.Remediation}} {
				if *layout == "braille" {
					line(field.label + ":")
					line(field.value)
				} else {
					line(field.label + ": " + field.value)
				}
			}
		}
		for n, e := range f.Evidence {
			line(fmt.Sprintf("Evidence %d: %s", n+1, e))
		}
		if *spell != "" {
			value := f.ID
			if *spell == "resource" {
				value = f.Resource
			}
			if len([]rune(value)) > 2048 {
				return fmt.Errorf("identifier exceeds spelling limit")
			}
			if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
				line("Identifier includes control or format characters; spelling uses Unicode code points")
			}
			line("Exact " + *spell + " spelling: " + speakIdentifier(value))
		}
	}
	for i, check := range r.CompletedChecks {
		line(fmt.Sprintf("Completed check %d: %s", i+1, check))
	}
	line("End of report. Status: " + string(r.Status()))
	if _, err = io.Copy(stdout, &output); err != nil {
		return err
	}
	if r.Status() != finding.StatusClean {
		return reportError{status: r.Status()}
	}
	return nil
}
