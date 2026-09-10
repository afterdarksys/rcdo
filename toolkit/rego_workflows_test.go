package toolkit

import (
	"os/exec"
	"strings"
	"testing"
)

func requireOPA(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("opa"); err != nil {
		t.Skip("OPA not installed")
	}
}
func TestRegoFixtureRunner(t *testing.T) {
	requireOPA(t)
	p := writeFixture(t, t.TempDir(), "rules.rego", `package rcdo
import rego.v1
decision := {"allow": input.ok}`)
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"pass", `{"schema_version":"1","cases":[{"name":"denied","input":{"ok":false},"expect":{"status":"blocked","finding_ids":["rego-denied"]}},{"name":"allowed","input":{"ok":true},"expect":{"status":"clean","finding_ids":[]}}]}`, 0},
		{"wrong-status", `{"schema_version":"1","cases":[{"name":"x","input":{"ok":false},"expect":{"status":"clean"}}]}`, 20},
		{"wrong-ids", `{"schema_version":"1","cases":[{"name":"x","input":{"ok":false},"expect":{"status":"blocked","finding_ids":[]}}]}`, 20},
		{"undefined", `{"schema_version":"1","cases":[{"name":"x","input":{},"expect":{"status":"clean"}}]}`, 30},
		{"empty", `{"schema_version":"1","cases":[]}`, 2},
		{"incomplete-expectation", `{"schema_version":"1","cases":[{"name":"x","input":{},"expect":{"status":"incomplete"}}]}`, 2},
		{"duplicate", `{"schema_version":"1","cases":[{"name":"x","input":{},"expect":{"status":"clean"}},{"name":"x","input":{},"expect":{"status":"clean"}}]}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suite := writeFixture(t, t.TempDir(), "suite.json", tc.body)
			code, out, err := execute("rego-test", []string{"--rego", p, "--suite", suite, "--format", "json"}, "")
			if code != tc.code {
				t.Fatalf("%d want %d: %s %s", code, tc.code, out, err)
			}
		})
	}
}
func TestRegoStarterSuite(t *testing.T) {
	requireOPA(t)
	base := "../examples/rego/starter/"
	args := []string{"--suite", base + "suite.json"}
	for _, name := range []string{"base", "tags", "public", "regions", "deletion", "production"} {
		args = append(args, "--rego", base+name+".rego")
	}
	code, out, err := execute("rego-test", args, "")
	if code != 0 || strings.Count(out, "PASS.") != 6 {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
