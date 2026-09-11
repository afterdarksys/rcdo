// Package finding defines the shared result format used by RCDO checks.
package finding

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
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
	Provenance       *Provenance `json:"provenance,omitempty"`
	Findings         []Finding   `json:"findings"`
	CompletedChecks  []string    `json:"completed_checks"`
	IncompleteChecks []string    `json:"incomplete_checks"`
}

// Provenance version 1 binds a review to exact source bytes and explicit change
// labels. It is local integrity evidence, not signed execution attestation.
type Provenance struct {
	Artifacts     []ProvenanceArtifact `json:"artifacts,omitempty"`
	SchemaVersion string               `json:"schema_version"`
	Tool          string               `json:"tool"`
	ChangeID      string               `json:"change_id"`
	Commit        string               `json:"commit"`
	Environment   string               `json:"environment"`
	SourceSHA256  string               `json:"source_sha256"`
	CollectedAt   string               `json:"collected_at"`
}

type ProvenanceArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func (p Provenance) Validate() error {
	if len(p.Artifacts) > 512 {
		return fmt.Errorf("too many provenance artifacts")
	}
	for _, a := range p.Artifacts {
		b, e := hex.DecodeString(a.SHA256)
		if !filepath.IsAbs(a.Path) || e != nil || len(b) != 32 {
			return fmt.Errorf("invalid provenance artifact")
		}
	}
	if p.SchemaVersion != "1" || strings.TrimSpace(p.Tool) == "" || strings.TrimSpace(p.Environment) == "" {
		return fmt.Errorf("invalid provenance identity or version")
	}
	if b, err := hex.DecodeString(p.SourceSHA256); err != nil || len(b) != 32 {
		return fmt.Errorf("invalid provenance source SHA-256")
	}
	if _, err := time.Parse(time.RFC3339Nano, p.CollectedAt); err != nil {
		return fmt.Errorf("invalid provenance timestamp")
	}
	if (p.ChangeID == "") != (p.Commit == "") {
		return fmt.Errorf("provenance change ID and commit must be supplied together")
	}
	if p.Commit != "" {
		b, err := hex.DecodeString(p.Commit)
		if err != nil || (len(b) != 20 && len(b) != 32) {
			return fmt.Errorf("provenance requires a full Git commit hash")
		}
	}
	return nil
}

func (r Report) Validate() error {
	if r.Provenance != nil {
		if err := r.Provenance.Validate(); err != nil {
			return err
		}
	}
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
