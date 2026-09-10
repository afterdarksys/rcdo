package toolkit

import (
	"strings"
	"testing"
	"time"
)

func ansibleFixture(outcome string, finish bool, noLog bool) string {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	events := []ansibleEvent{{SchemaVersion: "1", Event: "start", RunID: "run1", Sequence: 0, At: now, CheckMode: boolPtr(true)}, {SchemaVersion: "1", Event: "result", RunID: "run1", Sequence: 1, At: now, CheckMode: boolPtr(true), Host: "web", TaskID: "task1", Task: "sensitive-task-name", Outcome: outcome, NoLog: noLog}}
	if finish {
		events = append(events, ansibleEvent{SchemaVersion: "1", Event: "finish", RunID: "run1", Sequence: 2, At: now})
	}
	s := ""
	for _, e := range events {
		s += jsonFixture(e) + "\n"
	}
	return s
}
func TestAnsibleResultModesAndCoverage(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		finish  bool
		want    int
	}{{"ok", true, 10}, {"changed", true, 10}, {"failed", true, 20}, {"unreachable", true, 30}, {"skipped", true, 10}, {"ok", false, 30}} {
		code, out, e := execute("ansible-watch", []string{"--require-host", "web"}, ansibleFixture(tc.outcome, tc.finish, true))
		if code != tc.want || strings.Contains(out, "sensitive-task-name") || !strings.Contains(out, "check mode") {
			t.Fatalf("%s %d %s %s", tc.outcome, code, out, e)
		}
	}
	code, out, _ := execute("ansible-watch", []string{"--require-host", "other"}, ansibleFixture("ok", true, false))
	if code != 30 || !strings.Contains(out, "no task results") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestAnsibleRejectsAmbiguousReceipts(t *testing.T) {
	input := ansibleFixture("ok", true, false)
	for _, bad := range []string{strings.Replace(input, `"sequence":1`, `"sequence":4`, 1), input + input, strings.Replace(input, `"run_id":"run1"`, `"run_id":"other"`, 1)} {
		code, _, _ := execute("ansible-watch", []string{"--require-host", "web"}, bad)
		if code != 2 {
			t.Fatal(code, bad)
		}
	}
}
