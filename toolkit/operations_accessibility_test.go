package toolkit

import (
	"encoding/json"
	"git-tools/finding"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNamedCloudContext(t *testing.T) {
	for _, cloud := range []string{"aws", "alicloud"} {
		t.Run(cloud, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "context.yaml")
			expected := "schema_version: '1'\nname: payments\nenvironment: staging\ncloud: " + cloud + "\nprofile: work\nregion: test-region\naccount: '123'\n"
			if err := os.WriteFile(path, []byte(expected), 0600); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				account string
				age     time.Duration
				code    int
			}{{"123", 0, 0}, {"456", 0, 20}, {"123", time.Hour, 30}} {
				source := map[string]any{"schema": "missing-utils/contextsnap/v1", "source": "provided", "cloud": cloud, "profile": "work", "region": "test-region", "account": tc.account, "principal": "role/test", "collected_at": time.Now().Add(-tc.age), "collector_version": "test", "outcome": "pass", "diagnostics": []string{}}
				raw, _ := json.Marshal(source)
				code, out, err := execute("context", []string{"--expect", path, "--format", "text", "--width", "40"}, string(raw))
				if code != tc.code || err != "" {
					t.Fatalf("%d %s %s", code, out, err)
				}
				if !strings.Contains(out, "live identity") {
					t.Fatal("supplied observation mislabeled")
				}
				for _, line := range strings.Split(out, "\n") {
					if len([]rune(line)) > 40 {
						t.Fatalf("long line: %s", line)
					}
				}
			}
		})
	}
}
func TestNavigationPreservesBlockersAndHistory(t *testing.T) {
	a := makeFinding("A", finding.SeverityHigh, "First", "service-a", "review", "test", "reason", "evidence", "check")
	b := makeFinding("B", finding.SeverityWarning, "Second", "service-b", "review", "test", "reason", "evidence", "check")
	report, path := sessionFixture(t, finding.Report{Findings: []finding.Finding{a, b}, CompletedChecks: []string{"fixture check"}})
	beginSession(t, report, path)
	call := func(mode string, extra ...string) (int, string) {
		t.Helper()
		args := append([]string{mode, "--session", path, "--width", "40"}, extra...)
		code, out, err := execute("review-session", args, "")
		if err != "" {
			t.Fatal(err)
		}
		return code, out
	}
	if code, out := call("forward"); code != 20 || !strings.Contains(out, "ID: B") {
		t.Fatalf("%d %s", code, out)
	}
	if code, _ := call("bookmark", "--name", "return-here"); code != 0 {
		t.Fatal(code)
	}
	if code, _ := call("note", "--note", "Check upstream targets"); code != 0 {
		t.Fatal(code)
	}
	call("back")
	if code, out := call("goto", "--name", "return-here"); code != 20 || !strings.Contains(out, "ID: B") {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := call("resume"); code != 20 || !strings.Contains(out, "Check upstream targets") {
		t.Fatalf("%d %s", code, out)
	}
	saved, err := readReviewSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Acknowledged) != 0 || saved.Cursor != "B" {
		t.Fatal("navigation acknowledged or lost position")
	}
	code, out, _ := execute("handoff", []string{"--session", path, "--width", "40"}, "")
	if code != 20 || !strings.Contains(out, "Unresolved finding: A") {
		t.Fatalf("%d %s", code, out)
	}
	_, again, _ := execute("handoff", []string{"--session", path, "--width", "40"}, "")
	if out != again {
		t.Fatal("handoff is not deterministic")
	}
	if err := os.WriteFile(report, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := call("resume"); code != 30 || !strings.Contains(out, "HISTORICAL EVIDENCE") || !strings.Contains(out, "ID: B") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = execute("handoff", []string{"--session", path, "--width", "40"}, "")
	if code != 30 || !strings.Contains(out, "STORED REVIEW RESULT: BLOCKED") {
		t.Fatalf("%d %s", code, out)
	}
	if code, _ := call("note", "--note", "must not write"); code != 30 {
		t.Fatal(code)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 40 {
			t.Fatalf("long line: %s", line)
		}
	}
}
func TestWatchRanksAndDisclosesTruncation(t *testing.T) {
	raw := `{"schema":"missing-utils/eventwhy/v1","source":"provided","outcome":"pass","events":4,"ignored":0,"groups":[{"resource":"a","action":"start","count":1,"first":"2026-01-01T00:00:00Z","last":"2026-01-01T00:00:00Z"},{"resource":"b","action":"die","exit_code":"137","count":3,"first":"2026-01-01T00:00:00Z","last":"2026-01-01T00:00:10Z"}]}`
	code, out, err := execute("watch", []string{"--max-groups", "1", "--environment", "test"}, raw)
	if code != 30 || err != "" || !strings.Contains(out, "Container event: die") || !strings.Contains(out, "does not prove OOM") || !strings.Contains(out, "additional matching groups omitted") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("watch", []string{"--resource", "missing"}, raw)
	if code != 30 || !strings.Contains(out, "current state is unknown") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
