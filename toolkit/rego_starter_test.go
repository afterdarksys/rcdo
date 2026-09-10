package toolkit

import (
	"os/exec"
	"testing"
)

func TestRegoStarterPacks(t *testing.T) {
	if _, err := exec.LookPath("opa"); err != nil {
		t.Skip("OPA not installed")
	}
	base := "../examples/rego/starter/"
	for _, rule := range []string{"tags", "public", "regions", "deletion", "production"} {
		for _, tc := range []struct {
			file string
			code int
		}{{"pass.json", 0}, {rule + "-fail.json", 20}} {
			t.Run(rule+tc.file, func(t *testing.T) {
				code, out, err := execute("rego-check", []string{"--rego", base + "base.rego", "--rego", base + rule + ".rego", "--input", base + tc.file}, "")
				if code != tc.code {
					t.Fatalf("%d %s %s", code, out, err)
				}
			})
		}
	}
	code, out, err := execute("rego-check", []string{"--rego", base + "base.rego", "--rego", base + "tags.rego"}, `{"environment":"dev","approved":false,"resources":[{}]}`)
	if code != 30 {
		t.Fatalf("missing facts %d %s %s", code, out, err)
	}
}
