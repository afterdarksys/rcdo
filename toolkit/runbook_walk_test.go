package toolkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunbookProgressRequiresBoundVerification(t *testing.T) {
	dir := t.TempDir()
	book := writeFixture(t, dir, "book.json", `{"schema_version":"1","title":"Recover API","targets":{"environment":"staging"},"steps":[{"id":"health","title":"Check health","instruction":"Inspect the health probe","expected":"Probe passes","stop_conditions":["Probe unreachable"],"checks":["api"]},{"id":"finish","title":"Confirm recovery","instruction":"Check traffic","expected":"Traffic normal","stop_conditions":[],"checks":["traffic"]}]}`)
	state := filepath.Join(dir, "progress.json")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("runbook", append([]string{mode, "--state", state}, args...), "")
	}
	for _, args := range [][]string{{"start", "--input", book}, {"read"}, {"attempt"}, {"complete"}} {
		code, out, err := run(args[0], args[1:]...)
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, err)
		}
	}
	code, _, _ := run("next")
	if code != 2 {
		t.Fatal("advanced without verification", code)
	}
	bytes, _ := os.ReadFile(book)
	v := map[string]any{"schema_version": "1", "runbook_sha256": digestBytes(bytes), "step_id": "health", "targets": map[string]string{"environment": "staging"}, "source": "probe adapter", "collected_at": time.Now().UTC().Format(time.RFC3339Nano), "complete": true, "checks": []map[string]string{{"id": "api", "status": "fail", "evidence": "probe-1"}}}
	evidence := writeFixture(t, dir, "check.json", jsonFixture(v))
	code, out, err := run("verify", "--input", evidence)
	if code != 20 || !strings.Contains(out, "check failed") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	v["checks"] = []map[string]string{{"id": "api", "status": "pass", "evidence": "probe-2"}}
	os.WriteFile(evidence, []byte(jsonFixture(v)), 0600)
	code, out, err = run("verify", "--input", evidence)
	if code != 0 || !strings.Contains(out, "verified") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = run("next")
	if code != 0 || !strings.Contains(out, "Confirm recovery") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	os.WriteFile(evidence, []byte("changed"), 0600)
	code, out, _ = run("handoff")
	if code != 30 || !strings.Contains(out, "changed or missing") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestRunbookRejectsMissingChecksAndWrongTargets(t *testing.T) {
	b := executableRunbook{SchemaVersion: "1", Title: "Test", Targets: map[string]string{"account": "123"}, Steps: []runbookStep{{ID: "s", Title: "Step", Instruction: "Inspect", Expected: "Healthy", Checks: []string{"health"}}}}
	s := runbookState{Source: boundArtifact{SHA256: strings.Repeat("a", 64)}}
	raw := []byte(`{"schema_version":"1","runbook_sha256":"wrong","step_id":"s","targets":{"account":"456"},"source":"adapter","collected_at":"2000-01-01T00:00:00Z","complete":true,"checks":[]}`)
	r, err := verifyRunbookStep(raw, b, s, 0, time.Minute)
	if err != nil || len(r.Findings) == 0 || len(r.IncompleteChecks) < 2 {
		t.Fatal(r, err)
	}
}
