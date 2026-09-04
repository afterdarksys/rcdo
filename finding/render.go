package finding

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderText writes a linear, labeled report suitable for terminals and screen readers.
func RenderText(w io.Writer, report Report) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}

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
	SchemaVersion    string    `json:"schema_version"`
	Status           Status    `json:"status"`
	Summary          Summary   `json:"summary"`
	Findings         []Finding `json:"findings"`
	CompletedChecks  []string  `json:"completed_checks"`
	IncompleteChecks []string  `json:"incomplete_checks"`
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
			"tool":    map[string]any{"driver": map[string]any{"name": "git-tools", "version": SchemaVersion}},
			"results": results,
		}},
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}
