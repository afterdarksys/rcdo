package finding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	// DefaultTextWidth is the line width RenderText wraps to when the caller
	// does not choose one.
	DefaultTextWidth = 100
	// MinTextWidth matches the floor every other width flag in the toolkit
	// enforces. Below roughly 40 columns a labeled line breaks so often that
	// the label and its value stop reading as one item.
	MinTextWidth = 40
)

// RenderText writes a linear, labeled report suitable for terminals and screen
// readers, wrapped at DefaultTextWidth.
func RenderText(w io.Writer, report Report) error {
	return RenderTextWidth(w, report, DefaultTextWidth)
}

// RenderTextWidth is RenderText at a caller-chosen line width.
//
// Width is a real accessibility control, not cosmetics: this toolkit's users
// read with screen readers OR LARGE PRINT, and at high magnification a
// hundred-column line means panning sideways to read one finding — which is
// exactly where someone loses their place partway down a list of sixteen.
// Every other text-emitting command already honours --width; the review
// commands, which are the ones actually run, rendered at a hardcoded 100 and
// silently ignored the width set in the config file.
func RenderTextWidth(w io.Writer, report Report, width int) error {
	if width < MinTextWidth {
		return fmt.Errorf("text width %d is below the minimum of %d", width, MinTextWidth)
	}
	if err := report.Validate(); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}
	wrapped := &textWrapWriter{destination: w, width: width}
	w = wrapped

	summary := report.Summary()
	if _, err := fmt.Fprintf(w,
		"REVIEW RESULT: %s\nFINDINGS: %d (critical: %d, high: %d, warning: %d, info: %d)\n",
		strings.ToUpper(string(report.Status())),
		summary.Total,
		summary.Critical,
		summary.High,
		summary.Warning,
		summary.Info,
	); err != nil {
		return err
	}

	if len(report.IncompleteChecks) > 0 {
		if _, err := fmt.Fprintf(w, "INCOMPLETE CHECKS: %d\n", len(report.IncompleteChecks)); err != nil {
			return err
		}
		for i, check := range report.IncompleteChecks {
			if _, err := fmt.Fprintf(w, "Incomplete check %d: %s\n", i+1, check); err != nil {
				return err
			}
		}
	}
	if len(report.CompletedChecks) > 0 {
		if _, err := fmt.Fprintf(w, "COMPLETED CHECKS: %d\n", len(report.CompletedChecks)); err != nil {
			return err
		}
		for i, check := range report.CompletedChecks {
			if _, err := fmt.Fprintf(w, "Completed check %d: %s\n", i+1, check); err != nil {
				return err
			}
		}
	}

	ordered := append([]Finding(nil), report.Findings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return severityRank(ordered[i].Severity) > severityRank(ordered[j].Severity)
	})
	for i, finding := range ordered {
		if _, err := fmt.Fprintf(w,
			"\nFinding %d of %d\nSeverity: %s\nID: %s\nTitle: %s\nResource: %s\nAction: %s\nEnvironment: %s\nReason: %s\n",
			i+1,
			len(ordered),
			strings.ToUpper(string(finding.Severity)),
			finding.ID,
			finding.Title,
			finding.Resource,
			finding.Action,
			finding.Environment,
			finding.Reason,
		); err != nil {
			return err
		}
		for evidenceIndex, evidence := range finding.Evidence {
			if _, err := fmt.Fprintf(w, "Evidence %d: %s\n", evidenceIndex+1, evidence); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w,
			"Confidence: %s\nRemediation: %s\n",
			strings.ToUpper(string(finding.Confidence)),
			finding.Remediation,
		); err != nil {
			return err
		}
	}
	return wrapped.Flush()
}

type textWrapWriter struct {
	destination io.Writer
	width       int
	pending     bytes.Buffer
}

func (w *textWrapWriter) Write(data []byte) (int, error) {
	w.pending.Write(data)
	for {
		value := w.pending.String()
		newline := strings.IndexByte(value, '\n')
		if newline < 0 {
			break
		}
		if err := writeTextLine(w.destination, value[:newline], w.width); err != nil {
			return 0, err
		}
		remaining := append([]byte(nil), w.pending.Bytes()[newline+1:]...)
		w.pending.Reset()
		w.pending.Write(remaining)
	}
	return len(data), nil
}

func (w *textWrapWriter) Flush() error {
	if w.pending.Len() == 0 {
		return nil
	}
	value := w.pending.String()
	w.pending.Reset()
	return writeTextLine(w.destination, value, w.width)
}

func writeTextLine(destination io.Writer, line string, width int) error {
	if len([]rune(line)) <= width {
		_, err := fmt.Fprintln(destination, line)
		return err
	}
	words := strings.Fields(line)
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if len([]rune(candidate)) <= width {
			current = candidate
			continue
		}
		if current != "" {
			if _, err := fmt.Fprintln(destination, current); err != nil {
				return err
			}
		}
		runes := []rune(word)
		for len(runes) > width {
			if _, err := fmt.Fprintln(destination, string(runes[:width])); err != nil {
				return err
			}
			runes = runes[width:]
		}
		current = string(runes)
	}
	if current != "" {
		_, err := fmt.Fprintln(destination, current)
		return err
	}
	return nil
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

type jsonReport struct {
	Provenance       *Provenance `json:"provenance,omitempty"`
	SchemaVersion    string      `json:"schema_version"`
	Status           Status      `json:"status"`
	Summary          Summary     `json:"summary"`
	Findings         []Finding   `json:"findings"`
	CompletedChecks  []string    `json:"completed_checks"`
	IncompleteChecks []string    `json:"incomplete_checks"`
}

// RenderJSON writes the versioned machine-readable report contract.
func RenderJSON(w io.Writer, report Report) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}

	findings := report.Findings
	if findings == nil {
		findings = []Finding{}
	}
	incompleteChecks := report.IncompleteChecks
	if incompleteChecks == nil {
		incompleteChecks = []string{}
	}
	completedChecks := report.CompletedChecks
	if completedChecks == nil {
		completedChecks = []string{}
	}

	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(jsonReport{
		Provenance:       report.Provenance,
		SchemaVersion:    SchemaVersion,
		Status:           report.Status(),
		Summary:          report.Summary(),
		Findings:         findings,
		CompletedChecks:  completedChecks,
		IncompleteChecks: incompleteChecks,
	})
}

// RenderGitHub writes workflow command annotations with escaped untrusted values.
func RenderGitHub(w io.Writer, report Report) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}
	for _, item := range report.Findings {
		level := "warning"
		if item.Severity == SeverityCritical || item.Severity == SeverityHigh {
			level = "error"
		}
		if _, err := fmt.Fprintf(w, "::%s title=%s,file=%s::%s: %s. Next: %s\n",
			level, githubEscape(item.ID+" "+item.Title), githubEscape(item.Resource), githubEscape(strings.ToUpper(string(item.Severity))),
			githubEscape(item.Reason), githubEscape(item.Remediation)); err != nil {
			return err
		}
	}
	for _, check := range report.IncompleteChecks {
		if _, err := fmt.Fprintf(w, "::error title=INCOMPLETE REVIEW::%s\n", githubEscape(check)); err != nil {
			return err
		}
	}
	return nil
}

func githubEscape(value string) string {
	replacer := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
	return replacer.Replace(value)
}

// RenderSARIF emits a minimal SARIF 2.1.0 report for code scanning systems.
func RenderSARIF(w io.Writer, report Report) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}
	type message struct {
		Text string `json:"text"`
	}
	type result struct {
		RuleID  string  `json:"ruleId"`
		Level   string  `json:"level"`
		Message message `json:"message"`
	}
	results := make([]result, 0, len(report.Findings)+len(report.IncompleteChecks))
	for _, item := range report.Findings {
		level := "warning"
		if item.Severity == SeverityCritical || item.Severity == SeverityHigh {
			level = "error"
		} else if item.Severity == SeverityInfo {
			level = "note"
		}
		results = append(results, result{RuleID: item.ID, Level: level, Message: message{Text: item.Title + ": " + item.Reason + " Next: " + item.Remediation}})
	}
	for index, check := range report.IncompleteChecks {
		results = append(results, result{RuleID: fmt.Sprintf("INCOMPLETE-%03d", index+1), Level: "error", Message: message{Text: check}})
	}
	envelope := map[string]any{
		"version": "2.1.0",
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"runs": []any{map[string]any{
			"tool":    map[string]any{"driver": map[string]any{"name": "rcdo", "version": SchemaVersion}},
			"results": results,
		}},
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}
