package finding

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func validFinding() Finding {
	return Finding{
		ID:          "TF-001",
		Severity:    SeverityCritical,
		Title:       "Database will be replaced",
		Resource:    "aws_db_instance.primary",
		Action:      "replace",
		Environment: "production",
		Reason:      "The storage encryption key changed.",
		Evidence:    []string{"-/+ aws_db_instance.primary"},
		Confidence:  ConfidenceHigh,
		Remediation: "Confirm the migration and tested rollback plan.",
	}
}

func TestFindingValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Finding)
		wantErr string
	}{
		{name: "valid", mutate: func(*Finding) {}},
		{name: "missing ID", mutate: func(f *Finding) { f.ID = "" }, wantErr: "id is required"},
		{name: "unknown severity", mutate: func(f *Finding) { f.Severity = "urgent" }, wantErr: "unknown severity"},
		{name: "missing title", mutate: func(f *Finding) { f.Title = "" }, wantErr: "title is required"},
		{name: "missing resource", mutate: func(f *Finding) { f.Resource = "" }, wantErr: "resource is required"},
		{name: "missing action", mutate: func(f *Finding) { f.Action = "" }, wantErr: "action is required"},
		{name: "missing environment", mutate: func(f *Finding) { f.Environment = "" }, wantErr: "environment is required"},
		{name: "missing reason", mutate: func(f *Finding) { f.Reason = "" }, wantErr: "reason is required"},
		{name: "unknown confidence", mutate: func(f *Finding) { f.Confidence = "certain" }, wantErr: "unknown confidence"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validFinding()
			tt.mutate(&f)
			err := f.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestReportStatus(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		want   Status
	}{
		{name: "clean", report: Report{}, want: StatusClean},
		{name: "review", report: Report{Findings: []Finding{{Severity: SeverityWarning}}}, want: StatusReview},
		{name: "blocked high", report: Report{Findings: []Finding{{Severity: SeverityHigh}}}, want: StatusBlocked},
		{name: "blocked critical", report: Report{Findings: []Finding{{Severity: SeverityCritical}}}, want: StatusBlocked},
		{name: "incomplete takes precedence", report: Report{Findings: []Finding{{Severity: SeverityCritical}}, IncompleteChecks: []string{"spacelift"}}, want: StatusIncomplete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.report.Status(); got != tt.want {
				t.Fatalf("Status() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderTextIsAccessibleAndSeverityOrdered(t *testing.T) {
	warning := validFinding()
	warning.ID = "GHA-002"
	warning.Severity = SeverityWarning
	warning.Title = "Action uses a mutable tag"
	warning.Resource = "actions/checkout@v4"
	warning.Action = "execute"
	warning.Environment = "continuous-integration"

	report := Report{Findings: []Finding{warning, validFinding()}}
	var out bytes.Buffer
	if err := RenderText(&out, report); err != nil {
		t.Fatalf("RenderText() error = %v", err)
	}

	got := out.String()
	wantParts := []string{
		"REVIEW RESULT: BLOCKED\n",
		"FINDINGS: 2 (critical: 1, high: 0, warning: 1, info: 0)\n",
		"Finding 1 of 2\nSeverity: CRITICAL\nID: TF-001\n",
		"Title: Database will be replaced\n",
		"Resource: aws_db_instance.primary\n",
		"Action: replace\n",
		"Environment: production\n",
		"Reason: The storage encryption key changed.\n",
		"Evidence 1: -/+ aws_db_instance.primary\n",
		"Confidence: HIGH\n",
		"Remediation: Confirm the migration and tested rollback plan.\n",
		"Finding 2 of 2\nSeverity: WARNING\nID: GHA-002\n",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Errorf("RenderText() missing %q\noutput:\n%s", part, got)
		}
	}
	if strings.Index(got, "ID: TF-001") > strings.Index(got, "ID: GHA-002") {
		t.Errorf("critical finding was not rendered before warning finding:\n%s", got)
	}
}

func TestRenderTextReportsIncompleteChecks(t *testing.T) {
	report := Report{IncompleteChecks: []string{"Spacelift API unavailable", "AWS identity not verified"}}
	var out bytes.Buffer
	if err := RenderText(&out, report); err != nil {
		t.Fatalf("RenderText() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"REVIEW RESULT: INCOMPLETE",
		"INCOMPLETE CHECKS: 2",
		"Incomplete check 1: Spacelift API unavailable",
		"Incomplete check 2: AWS identity not verified",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderText() missing %q\noutput:\n%s", want, got)
		}
	}
}

func TestRenderJSONIncludesSchemaSummaryAndFindings(t *testing.T) {
	report := Report{Findings: []Finding{validFinding()}}
	var out bytes.Buffer
	if err := RenderJSON(&out, report); err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}

	var got struct {
		SchemaVersion string    `json:"schema_version"`
		Status        Status    `json:"status"`
		Summary       Summary   `json:"summary"`
		Findings      []Finding `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("JSON output is invalid: %v", err)
	}
	if got.SchemaVersion != SchemaVersion || got.Status != StatusBlocked || got.Summary.Total != 1 || got.Summary.Critical != 1 || len(got.Findings) != 1 {
		t.Fatalf("unexpected JSON report: %+v", got)
	}
}

func TestRenderersRejectInvalidReport(t *testing.T) {
	report := Report{Findings: []Finding{{ID: "broken"}}}
	if err := RenderText(&bytes.Buffer{}, report); err == nil {
		t.Fatal("RenderText() error = nil, want validation error")
	}
	if err := RenderJSON(&bytes.Buffer{}, report); err == nil {
		t.Fatal("RenderJSON() error = nil, want validation error")
	}
}

func TestCIRenderers(t *testing.T) {
	report := Report{Findings: []Finding{validFinding()}, IncompleteChecks: []string{}}
	var github bytes.Buffer
	if err := RenderGitHub(&github, report); err != nil || !strings.Contains(github.String(), "::error title=TF-001") {
		t.Fatalf("GitHub output=%q error=%v", github.String(), err)
	}
	var sarif bytes.Buffer
	if err := RenderSARIF(&sarif, report); err != nil || !strings.Contains(sarif.String(), `"version": "2.1.0"`) {
		t.Fatalf("SARIF output=%q error=%v", sarif.String(), err)
	}
}
