package toolkit

import (
	"strings"
	"testing"
)

func TestPolicyReview(t *testing.T) {
	requireOPA(t)
	dir := t.TempDir()
	allow := writeFixture(t, dir, "allow.rego", `package rcdo
decision := {"allow":true}`)
	deny := writeFixture(t, dir, "deny.rego", `package rcdo
decision := {"allow":false}`)
	missing := writeFixture(t, dir, "missing.rego", `package rcdo
decision := input.missing`)
	clean := `{"format_version":"1.2","resource_changes":[]}`
	deletion := `{"format_version":"1.2","resource_changes":[{"address":"aws_s3_bucket.x","type":"aws_s3_bucket","mode":"managed","change":{"actions":["delete"]}}]}`
	for _, tc := range []struct {
		name, policy, input, want string
		code                      int
	}{
		{"clean", allow, clean, "Check rego: status=clean", 0},
		{"iac-denied", allow, deletion, "iac/TOFU-DELETE", 20},
		{"rego-denied", deny, clean, "rego/rego-denied", 20},
		{"both-denied", deny, deletion, "Check iac: status=blocked", 20},
		{"iac-incomplete", deny, `{}`, "rego/rego-denied", 30},
		{"rego-incomplete", missing, deletion, "iac/TOFU-DELETE", 30},
		{"malformed-shape", allow, `{"format_version":"1.2","resource_changes":42}`, "Check rego: status=clean", 30},
		{"invalid-json", allow, `{"a":1,"a":2}`, "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, err := execute("policy-review", []string{"--rego", tc.policy, "--format", "json"}, tc.input)
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("%d %s %s", code, out, err)
			}
		})
	}
}
func TestPolicyReviewAuditAndReportReader(t *testing.T) {
	requireOPA(t)
	config, path := auditFixture(t, "redacted", 65536)
	code, out, err := execute("policy-review", []string{"--rego", "../examples/rego/terraform.rego", "--input", "../examples/rego/plan.json", "--format", "json", "--config-file", config}, "")
	if code != 20 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	records := readAuditFixture(t, path)
	if len(records) != 2 || records[1].Command != "policy-review" || records[1].ExitCode == nil || *records[1].ExitCode != 20 || !strings.Contains(records[1].Stdout, "Check rego:") {
		t.Fatalf("bad audit: %+v", records)
	}
	report := writeFixture(t, t.TempDir(), "report.json", out)
	code, out, err = execute("report-read", []string{"--input", report}, "")
	if code != 20 {
		t.Fatalf("reader %d %s %s", code, out, err)
	}
}
func TestPolicyReviewMissingOPA(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := writeFixture(t, t.TempDir(), "p.rego", "package rcdo\ndecision := {\"allow\":true}")
	code, out, err := execute("policy-review", []string{"--rego", p, "--input", "../examples/rego/plan.json", "--format", "json"}, "")
	if code != 30 || !strings.Contains(out, "iac/TOFU-DELETE") || !strings.Contains(out, "OPA executable unavailable") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
