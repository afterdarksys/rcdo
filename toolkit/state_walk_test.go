package toolkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stateFixture = `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"aws_instance.web[\"blue\"]","values":{"name":"visible","password":"never-show","nested":{"marked":"secret-value","revision":9007199254740993}},"sensitive_values":{"nested":{"marked":true}}}]},"outputs":{"token":{"value":"secret-output","sensitive":true}}}}`

func TestStateWalkRedactionNavigationAndStaleness(t *testing.T) {
	dir := t.TempDir()
	input := writeFixture(t, dir, "show.json", stateFixture)
	state := filepath.Join(dir, "nav.json")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("state-walk", append([]string{mode, "--state", state}, args...), "")
	}
	code, out, e := run("start", "--input", input)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	code, out, e = run("goto", "--id", `resource:aws_instance.web["blue"]#/nested`, "--format", "json")
	if code != 0 || strings.Contains(out, "secret-value") || !strings.Contains(out, "9007199254740993") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	run("bookmark", "--name", "nested")
	run("parent")
	code, out, e = run("goto", "--name", "nested")
	if code != 0 || !strings.Contains(out, "/nested") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	before, _ := os.ReadFile(state)
	_ = os.WriteFile(input, []byte(stateFixture+"\n"), 0600)
	code, _, _ = run("next")
	after, _ := os.ReadFile(state)
	if code != 30 || string(before) != string(after) {
		t.Fatal("stale navigation changed", code)
	}
	if strings.Contains(string(before), "secret-value") {
		t.Fatal("navigation saved raw values")
	}
}
func TestStateWalkFailsClosedWithoutSensitivity(t *testing.T) {
	raw := strings.Replace(stateFixture, `"sensitive_values":{"nested":{"marked":true}}`, `"sensitive_values":null`, 1)
	nodes, gaps, e := buildStateTree([]byte(raw))
	if e != nil || len(gaps) == 0 {
		t.Fatal(gaps, e)
	}
	for _, n := range nodes {
		if strings.Contains(jsonFixture(n), "secret-value") {
			t.Fatal("missing mask leaked value")
		}
	}
	for _, raw := range []string{`{"version":4,"resources":[]}`, `{"format_version":"1.0","values":{},"planned_values":{}}`} {
		if _, _, e := buildStateTree([]byte(raw)); e == nil {
			t.Fatal("unsupported state accepted")
		}
	}
}

func TestStateWalkMissingResourceValuesAreIncomplete(t *testing.T) {
	_, gaps, e := buildStateTree([]byte(`{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x.y","sensitive_values":{}}]}}}`))
	if e != nil || len(gaps) == 0 {
		t.Fatal(gaps, e)
	}
}
