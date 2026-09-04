// Package finding defines the shared result format used by git-tools checks.
package finding

import (
	"fmt"
	"strings"
)

// SchemaVersion changes when the JSON contract changes incompatibly.
const SchemaVersion = "1"

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

type Status string

const (
	StatusClean      Status = "clean"
	StatusReview     Status = "review"
	StatusBlocked    Status = "blocked"
	StatusIncomplete Status = "incomplete"
)

// Finding is one actionable, evidence-backed review result.
type Finding struct {
	ID          string     `json:"id"`
	Severity    Severity   `json:"severity"`
	Title       string     `json:"title"`
	Resource    string     `json:"resource"`
	Action      string     `json:"action"`
	Environment string     `json:"environment"`
	Reason      string     `json:"reason"`
	Evidence    []string   `json:"evidence"`
	Confidence  Confidence `json:"confidence"`
	Remediation string     `json:"remediation"`
}

func (f Finding) Validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"id", f.ID},
		{"title", f.Title},
		{"resource", f.Resource},
		{"action", f.Action},
		{"environment", f.Environment},
		{"reason", f.Reason},
		{"remediation", f.Remediation},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}

	if !f.Severity.valid() {
		return fmt.Errorf("unknown severity %q", f.Severity)
	}
	if !f.Confidence.valid() {
		return fmt.Errorf("unknown confidence %q", f.Confidence)
	}
	return nil
}

func (s Severity) valid() bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityHigh, SeverityCritical:
		return true
	default:
		return false
	}
}

func (c Confidence) valid() bool {
	switch c {
	case ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	default:
		return false
	}
}

// Report contains findings plus checks that could not be completed.
type Report struct {
	Findings         []Finding `json:"findings"`
	CompletedChecks  []string  `json:"completed_checks"`
	IncompleteChecks []string  `json:"incomplete_checks"`
}

func (r Report) Validate() error {
	seen := make(map[string]struct{}, len(r.Findings))
	for i, finding := range r.Findings {
		if err := finding.Validate(); err != nil {
			return fmt.Errorf("finding %d: %w", i+1, err)
		}
		if _, exists := seen[finding.ID]; exists {
			return fmt.Errorf("finding %d: duplicate id %q", i+1, finding.ID)
		}
		seen[finding.ID] = struct{}{}
	}
	for i, check := range r.IncompleteChecks {
		if strings.TrimSpace(check) == "" {
			return fmt.Errorf("incomplete check %d is empty", i+1)
		}
	}
	for i, check := range r.CompletedChecks {
		if strings.TrimSpace(check) == "" {
			return fmt.Errorf("completed check %d is empty", i+1)
		}
	}
	return nil
}

func (r Report) Status() Status {
	if len(r.IncompleteChecks) > 0 {
		return StatusIncomplete
	}
	status := StatusClean
	for _, finding := range r.Findings {
		switch finding.Severity {
		case SeverityCritical, SeverityHigh:
			return StatusBlocked
		case SeverityWarning, SeverityInfo:
			status = StatusReview
		}
	}
	return status
}

type Summary struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	High     int `json:"high"`
	Warning  int `json:"warning"`
	Info     int `json:"info"`
}

func (r Report) Summary() Summary {
	result := Summary{Total: len(r.Findings)}
	for _, finding := range r.Findings {
		switch finding.Severity {
		case SeverityCritical:
			result.Critical++
		case SeverityHigh:
			result.High++
		case SeverityWarning:
			result.Warning++
		case SeverityInfo:
			result.Info++
		}
	}
	return result
}
