package toolkit

import (
	"bytes"
	"git-tools/finding"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTasksResumePositionWithoutMutatingWorkflow(t *testing.T) {
	dir := t.TempDir()
	incident := filepath.Join(dir, "incident.json")
	registry := filepath.Join(dir, "tasks.json")
	for _, args := range [][]string{{"start", "--title", "API incident"}, {"note", "--text", "inspect networking"}, {"previous"}, {"next-action", "--text", "Check DNS"}} {
		code, out, e := execute("incident", append(args, "--state", incident), "")
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, e)
		}
	}
	code, out, e := execute("tasks", []string{"add", "--registry", registry, "--name", "api", "--kind", "incident", "--input", incident}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	before, _ := os.ReadFile(incident)
	code, out, e = execute("tasks", []string{"resume", "--registry", registry, "--name", "api"}, "")
	after, _ := os.ReadFile(incident)
	if code != 0 || !bytes.Equal(before, after) || !strings.Contains(out, "Check DNS") || !strings.Contains(out, "Saved position") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	code, _, _ = execute("tasks", []string{"remove", "--registry", registry, "--name", "api"}, "")
	if code != 0 {
		t.Fatal(code)
	}
	after, _ = os.ReadFile(incident)
	if !bytes.Equal(before, after) {
		t.Fatal("remove changed workflow")
	}
}
func TestTasksDetectsReplacementAndMissingFile(t *testing.T) {
	dir := t.TempDir()
	incident := filepath.Join(dir, "incident.json")
	registry := filepath.Join(dir, "tasks.json")
	execute("incident", []string{"start", "--state", incident, "--title", "one"}, "")
	execute("tasks", []string{"add", "--registry", registry, "--name", "one", "--kind", "incident", "--input", incident}, "")
	raw, _ := os.ReadFile(incident)
	_ = os.WriteFile(incident, bytes.ReplaceAll(raw, []byte("one"), []byte("two")), 0600)
	code, out, e := execute("tasks", []string{"resume", "--registry", registry, "--name", "one"}, "")
	if code != 30 || !strings.Contains(out, "identity changed") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	_ = os.Remove(incident)
	code, out, _ = execute("tasks", []string{"list", "--registry", registry}, "")
	if code != 30 || !strings.Contains(out, "unavailable") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestTasksReviewPreservesAcknowledgementsAndRisk(t *testing.T) {
	dir := t.TempDir()
	r := finding.Report{CompletedChecks: []string{"parsed"}, Findings: []finding.Finding{makeFinding("F1", finding.SeverityHigh, "Risk", "api", "review", "test", "risk", "record", "inspect")}}
	var b bytes.Buffer
	_ = finding.RenderJSON(&b, r)
	report := writeFixture(t, dir, "report.json", b.String())
	session := filepath.Join(dir, "review.json")
	registry := filepath.Join(dir, "tasks.json")
	code, out, e := execute("review-session", []string{"start", "--report", report, "--session", session, "--change-id", "demo", "--commit", strings.Repeat("a", 40)}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	code, _, _ = execute("tasks", []string{"add", "--registry", registry, "--name", "review", "--kind", "review", "--input", session}, "")
	if code != 20 {
		t.Fatal(code)
	}
	before, _ := os.ReadFile(session)
	code, out, e = execute("tasks", []string{"resume", "--registry", registry, "--name", "review"}, "")
	after, _ := os.ReadFile(session)
	if code != 20 || !bytes.Equal(before, after) || !strings.Contains(out, "F1") {
		t.Fatalf("%d %s %s", code, out, e)
	}
}

func TestTasksRunbookAndStateNavigation(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, "tasks.json")
	source := writeFixture(t, dir, "state.json", stateFixture)
	nav := filepath.Join(dir, "nav.json")
	code, out, e := execute("state-walk", []string{"start", "--input", source, "--state", nav}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	book := writeFixture(t, dir, "book.json", `{"schema_version":"1","title":"Recover API","targets":{"environment":"test"},"steps":[{"id":"probe","title":"Read probe","instruction":"Inspect health evidence","expected":"Healthy","checks":["health"],"stop_conditions":[]}]}`)
	progress := filepath.Join(dir, "progress.json")
	code, out, e = execute("runbook", []string{"start", "--input", book, "--state", progress}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	for _, entry := range []struct{ name, kind, path string }{{"state", "state", nav}, {"book", "runbook", progress}} {
		code, out, e = execute("tasks", []string{"add", "--registry", registry, "--name", entry.name, "--kind", entry.kind, "--input", entry.path}, "")
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, e)
		}
		before, _ := os.ReadFile(entry.path)
		code, out, e = execute("tasks", []string{"resume", "--registry", registry, "--name", entry.name}, "")
		after, _ := os.ReadFile(entry.path)
		if code != 0 || !bytes.Equal(before, after) {
			t.Fatalf("%d %s %s", code, out, e)
		}
	}
	code, out, e = execute("tasks", []string{"list", "--registry", registry}, "")
	if code != 0 || !strings.Contains(out, "Inspect health evidence") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	code, _, _ = execute("tasks", []string{""}, "")
	if code != 2 {
		t.Fatal("empty mode accepted")
	}
}
