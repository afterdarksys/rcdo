package toolkit

import (
	"strings"
	"testing"
)

func TestRegoDiff(t *testing.T) {
	requireOPA(t)
	dir := t.TempDir()
	suite := writeFixture(t, dir, "suite.json", `{"schema_version":"1","cases":[{"name":"sample","input":{}}]}`)
	allow := writeFixture(t, dir, "allow.rego", `package rcdo
decision := {"allow":true}`)
	deny := writeFixture(t, dir, "deny.rego", `package rcdo
decision := {"allow":false}`)
	missing := writeFixture(t, dir, "missing.rego", `package rcdo
decision := input.missing`)
	warning := writeFixture(t, dir, "warn.rego", `package rcdo
decision := {"allow":true,"findings":[{"id":"x","severity":"warning","title":"Warning","resource":"x","reason":"review","remediation":"check"}]}`)
	warning2 := writeFixture(t, dir, "warn2.rego", `package rcdo
decision := {"allow":true,"findings":[{"id":"x","severity":"warning","title":"Warning","resource":"x","reason":"changed reason","remediation":"check"}]}`)
	for _, tc := range []struct {
		name, before, after, want string
		code                      int
	}{
		{"same", deny, deny, "unchanged", 0},
		{"new-deny", allow, deny, "allow=false", 20},
		{"new-allow", deny, allow, "Removed finding", 10},
		{"new-warning", allow, warning, "Added finding", 10},
		{"changed-detail", warning, warning2, "changed reason", 10},
		{"undefined", allow, missing, "undefined", 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, err := execute("rego-diff", []string{"--before", tc.before, "--after", tc.after, "--suite", suite, "--format", "json"}, "")
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("%d %s %s", code, out, err)
			}
		})
	}
}
