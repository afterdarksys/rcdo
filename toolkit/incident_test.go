package toolkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIncidentResumeEvidenceAndHandoff(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "incident.json")
	evidence := writeFixture(t, dir, "log.txt", "original evidence\n")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("incident", append([]string{mode, "--state", state}, args...), "")
	}
	for _, args := range [][]string{{"start", "--title", "Database outage"}, {"hypothesis", "--text", "Connection limit reached"}, {"next-action", "--text", "Inspect active connections"}, {"evidence", "--name", "logs", "--input", evidence}, {"action", "--text", "Collected connection count", "--status", "completed"}} {
		code, out, err := run(args[0], args[1:]...)
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, err)
		}
	}
	code, out, err := run("resume")
	if code != 0 || !strings.Contains(out, "Connection limit reached") || !strings.Contains(out, "Inspect active connections") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	os.WriteFile(evidence, []byte("new evidence\n"), 0600)
	code, out, err = run("handoff")
	if code != 30 || !strings.Contains(out, "Stale or unavailable") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, _, _ = run("note", "--text", "Need refreshed evidence")
	if code != 30 {
		t.Fatal(code)
	}
	code, out, err = run("evidence", "--name", "logs", "--input", evidence)
	if code != 0 || !strings.Contains(out, "attached: logs") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, _, _ = run("resolve", "--id", "1", "--status", "rejected")
	if code != 0 {
		t.Fatal(code)
	}
	code, out, _ = run("show")
	if code != 0 || !strings.Contains(out, "rejected") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestIncidentNoOverwriteAndNoImpliedVerification(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	execute("incident", []string{"start", "--state", state, "--title", "Test"}, "")
	code, _, _ := execute("incident", []string{"start", "--state", state, "--title", "Other"}, "")
	if code != 2 {
		t.Fatal(code)
	}
	code, _, _ = execute("incident", []string{"action", "--state", state, "--text", "Ran command", "--status", "verified"}, "")
	if code != 2 {
		t.Fatal(code)
	}
}
