package toolkit

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"git-tools/finding"
)

func TestRegoDecision(t *testing.T) {
	cases := []struct {
		input   string
		status  finding.Status
		invalid bool
	}{
		{`{"allow":true}`, finding.StatusClean, false},
		{`{"allow":false}`, finding.StatusBlocked, false},
		{`{"allow":true,"incomplete":["Missing data"]}`, finding.StatusIncomplete, false},
		{`{"allow":true,"findings":[{"id":"x","severity":"warning","title":"Review","resource":"x","reason":"Scope","remediation":"Check"}]}`, finding.StatusReview, false},
		{`true`, "", true}, {`{}`, "", true}, {`{"allow":null}`, "", true},
		{`{"allow":true,"typo":1}`, "", true},
		{`{"allow":true,"findings":[{"id":"x"}]}`, "", true},
		{`{"allow":true,"incomplete":[""]}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			r, err := regoDecision([]byte(tc.input), "test")
			if (err != nil) != tc.invalid {
				t.Fatalf("error %v", err)
			}
			if err == nil && r.Status() != tc.status {
				t.Fatalf("status %s", r.Status())
			}
		})
	}
}

func TestRegoIntegration(t *testing.T) {
	if _, err := exec.LookPath("opa"); err != nil {
		t.Skip("OPA not installed")
	}
	dir := t.TempDir()
	cases := []struct {
		name, policy, input string
		code                int
	}{
		{"clean", `package rcdo
import rego.v1
decision := {"allow": input.ok}`, `{"ok":true}`, 0},
		{"deny", `package rcdo
import rego.v1
decision := {"allow": input.ok}`, `{"ok":false}`, 20},
		{"undefined", `package rcdo
import rego.v1
decision := {"allow": input.missing}`, `{}`, 30},
		{"syntax", `package rcdo
this is invalid`, `{}`, 30},
		{"http", `package rcdo
import rego.v1
decision := {"allow": http.send({"method":"GET","url":"http://127.0.0.1"}).status_code == 200}`, `{}`, 30},
		{"runtime", `package rcdo
import rego.v1
decision := {"allow": count(opa.runtime()) > 0}`, `{}`, 30},
		{"builtin-error", `package rcdo
import rego.v1
decision := {"allow": to_number(input.value) == 1}`, `{"value":"not-a-number"}`, 30},
		{"duplicate-input", `package rcdo
decision := {"allow": true}`, `{"x":1,"x":2}`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFixture(t, dir, tc.name+".rego", tc.policy)
			code, out, err := execute("rego-check", []string{"--rego", p, "--format", "json"}, tc.input)
			if code != tc.code {
				t.Fatalf("code %d want %d: %s %s", code, tc.code, out, err)
			}
		})
	}
	code, out, err := execute("rego-check", []string{"--rego", "../examples/rego/terraform.rego", "--input", "../examples/rego/plan.json", "--format", "json"}, "")
	if code != 20 || !strings.Contains(out, "aws_s3_bucket.archive") {
		t.Fatalf("example %d %s %s", code, out, err)
	}
	code, out, err = execute("rego-check", []string{"--rego", "../examples/rego/terraform.rego", "--format", "json"}, `{}`)
	if code != 30 {
		t.Fatalf("invalid plan %d %s %s", code, out, err)
	}
}

func TestRegoExecutionFailure(t *testing.T) {
	if _, err := exec.LookPath("opa"); err != nil {
		t.Skip("OPA not installed")
	}
	old := regoInvoke
	t.Cleanup(func() { regoInvoke = old })
	regoInvoke = func(context.Context, string, []string) ([]byte, error) { return []byte(`{}`), nil }
	p := writeFixture(t, t.TempDir(), "policy.rego", "package rcdo\ndecision := {\"allow\":true}")
	code, out, err := execute("rego-check", []string{"--rego", p}, `{}`)
	if code != 30 {
		t.Fatalf("%d %s %s", code, out, err)
	}
}

func TestRegoMissingOPA(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := writeFixture(t, t.TempDir(), "rules.rego", "package rcdo\ndecision := {\"allow\":true}")
	code, out, err := execute("rego-check", []string{"--rego", p}, `{}`)
	if code != 30 || !strings.Contains(out, "OPA executable unavailable") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}

func TestRegoMultipleModules(t *testing.T) {
	if _, err := exec.LookPath("opa"); err != nil {
		t.Skip("OPA not installed")
	}
	dir := t.TempDir()
	a := writeFixture(t, dir, "a.rego", "package team\nimport rego.v1\ndecision := {\"allow\": permitted}")
	b := writeFixture(t, dir, "b.rego", "package team\nimport rego.v1\npermitted := input.n == 9007199254740993")
	code, out, err := execute("rego-check", []string{"--rego", a, "--rego", b, "--query", "data.team.decision"}, `{"n":9007199254740993}`)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
}

func TestRegoCanceledProcess(t *testing.T) {
	opa, err := exec.LookPath("opa")
	if err != nil {
		t.Skip("OPA not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = regoInvoke(ctx, opa, []string{"capabilities", "--current"})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout, got %v", err)
	}
}
