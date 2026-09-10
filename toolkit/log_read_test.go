package toolkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogGroupingAndFilterCoverage(t *testing.T) {
	input := "{\"timestamp\":\"2026-09-10T12:00:00Z\",\"request_id\":\"r1\",\"message\":\"error\"}\n{\"timestamp\":\"2026-09-10T12:01:00Z\",\"request_id\":\"r1\",\"message\":\"error\"}\n{\"message\":\"unknown\"}\n"
	code, out, err := execute("log-read", []string{"--syntax", "jsonl"}, input)
	if code != 0 || !strings.Contains(out, "Count 2") || !strings.Contains(out, "Last line 2") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("log-read", []string{"--syntax", "jsonl", "--since", "2026-09-10T12:00:00Z", "--format", "json"}, input)
	if code != 30 || !strings.Contains(out, `"unknown_time_events":1`) {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, _ = execute("log-read", []string{"--syntax", "jsonl", "--request", "r1", "--query", "error"}, input)
	if code != 0 || !strings.Contains(out, "2 match") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestLogNavigationAndRotation(t *testing.T) {
	dir := t.TempDir()
	source := writeFixture(t, dir, "app.log", "ok\nerror\nother\nerror\n")
	state := filepath.Join(dir, "state.json")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("log-read", append([]string{mode, "--state", state}, args...), "")
	}
	code, out, err := run("start", "--input", source, "--query", "error")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = run("next", "--context", "0")
	if code != 0 || !strings.Contains(out, "Line 4.") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, _, _ = run("bookmark", "--name", "error2")
	if code != 0 {
		t.Fatal(code)
	}
	run("previous")
	code, out, err = run("goto", "--name", "error2", "--context", "0")
	if code != 0 || !strings.Contains(out, "Line 4.") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	before, _ := os.ReadFile(state)
	os.WriteFile(source, []byte("rotated\n"), 0600)
	code, out, err = run("next")
	if code != 30 || out != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	after, _ := os.ReadFile(state)
	if string(before) != string(after) {
		t.Fatal("stale state changed")
	}
}
func TestLogLimitsAndRedaction(t *testing.T) {
	code, out, _ := execute("log-read", nil, "token=secret-value\n\x1b[31merror\n")
	if code != 0 || strings.Contains(out, "secret-value") || strings.Contains(out, "\x1b") {
		t.Fatalf("%d %q", code, out)
	}
	code, _, _ = execute("log-read", nil, strings.Repeat("x", 65537))
	if code != 2 {
		t.Fatal(code)
	}
	code, _, _ = execute("log-read", []string{"--syntax", "jsonl"}, `{"message":"x","message":"y"}`)
	if code != 2 {
		t.Fatal(code)
	}
}
