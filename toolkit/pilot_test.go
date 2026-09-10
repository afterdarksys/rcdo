package toolkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPilotNeverConvertsUnrunTasksIntoPass(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "pilot.json")
	evidence := writeFixture(t, dir, "observations.txt", "test fixture only\n")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("pilot", append([]string{mode, "--state", state}, args...), "")
	}
	code, out, err := run("start", "--operator", "test operator", "--setup", "synthetic unit test, no AT acceptance")
	if code != 30 || !strings.Contains(out, "pending") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = run("record", "--task", "log-bookmark", "--outcome", "pass", "--notes", "synthetic test observation", "--evidence", evidence)
	if code != 30 || !strings.Contains(out, "log-bookmark: pass") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	if err := os.WriteFile(evidence, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, err = run("show")
	if code != 30 || !strings.Contains(out, "evidence_stale") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
