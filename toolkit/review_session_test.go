package toolkit

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git-tools/finding"
)

func sessionFixture(t *testing.T, report finding.Report) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	var data bytes.Buffer
	if err := finding.RenderJSON(&data, report); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(dir, "session.json")
}
func beginSession(t *testing.T, reportPath, sessionPath string, extra ...string) {
	t.Helper()
	args := []string{"start", "--report", reportPath, "--session", sessionPath, "--change-id", "TEST-1", "--commit", "HEAD"}
	args = append(args, extra...)
	code, out, err := execute("review-session", args, "")
	if code != 0 {
		t.Fatalf("start %d: %s %s", code, out, err)
	}
}
func TestSessionRetainsIncompleteCoverage(t *testing.T) {
	report, path := sessionFixture(t, finding.Report{IncompleteChecks: []string{"cloud identity unavailable"}})
	beginSession(t, report, path)
	for _, mode := range []string{"status", "next"} {
		code, out, err := execute("review-session", []string{mode, "--session", path}, "")
		if code != 30 || err != "" || !strings.Contains(out, "cloud identity unavailable") || strings.Contains(out, "REVIEW SESSION COMPLETE") {
			t.Fatalf("%s %d %s %s", mode, code, out, err)
		}
	}
}
func TestSessionInvalidatesChangedInputs(t *testing.T) {
	for _, target := range []string{"report", "artifact", "missing"} {
		t.Run(target, func(t *testing.T) {
			report, path := sessionFixture(t, finding.Report{CompletedChecks: []string{"plan checked"}})
			artifact := filepath.Join(filepath.Dir(path), "plan.json")
			if err := os.WriteFile(artifact, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			beginSession(t, report, path, "--artifact", artifact)
			switch target {
			case "report":
				if err := os.WriteFile(report, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "artifact":
				if err := os.WriteFile(artifact, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(artifact); err != nil {
					t.Fatal(err)
				}
			}
			for _, mode := range []string{"status", "next", "ack"} {
				code, out, err := execute("review-session", []string{mode, "--session", path}, "")
				if code != 30 || err != "" || !strings.Contains(out, "SESSION STALE") {
					t.Fatalf("%s: %d %s %s", mode, code, out, err)
				}
			}
		})
	}
}
func TestSessionRejectsUnversionedOrContradictoryReport(t *testing.T) {
	for _, data := range []string{`{}`, `{"findings":[]}`, `{"schema_version":"1","status":"clean","findings":[],"completed_checks":[],"incomplete_checks":["missing"]}`, `{"schema_version":"1","status":"clean","findings":[],"completed_checks":[],"incomplete_checks":[]}`} {
		report, path := sessionFixture(t, finding.Report{})
		if err := os.WriteFile(report, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		code, _, _ := execute("review-session", []string{"start", "--report", report, "--session", path, "--change-id", "TEST", "--commit", "HEAD"}, "")
		if code != 2 {
			t.Fatalf("accepted %s: %d", data, code)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("created invalid session")
		}
	}
}
func TestSessionRejectsLegacyAndTamperedStorage(t *testing.T) {
	report, path := sessionFixture(t, finding.Report{CompletedChecks: []string{"check"}})
	beginSession(t, report, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s reviewSession
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	s.Acknowledged["not-a-finding"] = reviewAcknowledgement{Note: "read", At: s.CreatedAt}
	if err := writeReviewSession(path, s); err != nil {
		t.Fatal(err)
	}
	code, _, _ := execute("review-session", []string{"status", "--session", path}, "")
	if code != 2 {
		t.Fatal(code)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":"1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, _ = execute("review-session", []string{"next", "--session", path}, "")
	if code != 2 {
		t.Fatal(code)
	}
}
func TestSessionRepositoryBinding(t *testing.T) {
	for _, change := range []string{"commit", "dirty", "untracked"} {
		t.Run(change, func(t *testing.T) {
			repo := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
				if data, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %s %v", data, err)
				}
			}
			git("init", "--quiet")
			git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
			report, path := sessionFixture(t, finding.Report{CompletedChecks: []string{"check"}})
			beginSession(t, report, path, "--repo", repo)
			code, out, err := execute("review-session", []string{"status", "--session", path}, "")
			if code != 0 {
				t.Fatalf("fresh %d %s %s", code, out, err)
			}
			switch change {
			case "commit":
				git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "changed")
			default:
				if err := os.WriteFile(filepath.Join(repo, "change"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if change == "dirty" {
					git("add", "change")
				}
			}
			code, out, err = execute("review-session", []string{"next", "--session", path}, "")
			if code != 30 || !strings.Contains(out, "SESSION STALE") {
				t.Fatalf("stale %d %s %s", code, out, err)
			}
		})
	}
}

func TestSessionExpiryAndWidth(t *testing.T) {
	report, path := sessionFixture(t, finding.Report{IncompleteChecks: []string{strings.Repeat("x", 160)}})
	beginSession(t, report, path)
	code, out, err := execute("review-session", []string{"status", "--session", path, "--width", "40"}, "")
	if code != 30 || err != "" {
		t.Fatalf("%d %s", code, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 40 {
			t.Fatalf("unwrapped: %s", line)
		}
	}
	s, readErr := readReviewSession(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	s.ExpiresAt = "2000-01-01T00:00:00Z"
	if err := writeReviewSession(path, s); err != nil {
		t.Fatal(err)
	}
	code, out, err = execute("review-session", []string{"next", "--session", path}, "")
	if code != 30 || !strings.Contains(out, "session expired") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
