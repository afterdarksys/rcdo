package toolkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func auditFixture(t *testing.T, output string, limit int) (string, string) {
	t.Helper()
	c := defaultAppConfig()
	c.Audit = auditConfig{Enabled: true, File: "audit.jsonl", Output: output, MaxOutputBytes: limit}
	path := writeAppConfigFixture(t, c)
	return path, filepath.Join(filepath.Dir(path), "audit.jsonl")
}

func readAuditFixture(t *testing.T, path string) []auditRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []auditRecord
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var record auditRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func TestAuditInvocationAndOutput(t *testing.T) {
	config, path := auditFixture(t, "redacted", 0)
	args := []string{"log-read", "--config-file", config, "--format", "json"}
	code, out, stderr := execute("rcdo", args, "hello\n")
	if code != 0 || stderr != "" {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	records := readAuditFixture(t, path)
	if len(records) != 2 {
		t.Fatal(records)
	}
	a, b := records[0], records[1]
	if a.Event != "start" || b.Event != "finish" || a.RunID != b.RunID || a.RunID == "" || b.Command != "log-read" || b.ExitCode == nil || *b.ExitCode != 0 {
		t.Fatalf("%+v %+v", a, b)
	}
	if b.Stdout != out || b.Stderr != "" || b.StdoutBytes != int64(len(out)) || b.StdoutTruncated || b.User == "" || b.UID == "" || b.Host == "" || b.PID != os.Getpid() || b.At.Before(a.At) || b.At.Location().String() != "UTC" {
		t.Fatalf("%+v", b)
	}
	if !strings.Contains(strings.Join(b.EffectiveArguments, " "), "--width=100") {
		t.Fatal(b.EffectiveArguments)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("%v %v", info, err)
	}
	// Aliases and commands without configurable defaults pass through the same audit boundary.
	t.Setenv("RCDO_CONFIG", config)
	code, _, _ = execute("git-tools", []string{"version"}, "")
	if code != 0 || len(readAuditFixture(t, path)) != 4 {
		t.Fatal("missing version audit")
	}
}

func TestAuditPreservesFailureAndRedactsArguments(t *testing.T) {
	config, path := auditFixture(t, "redacted", 0)
	code, out, stderr := execute("log-read", []string{"--config-file", config, "--syntax", "jsonl", "--since", "2026-09-10T00:00:00Z"}, "{\"message\":\"hello\"}\n")
	if code != 30 {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	last := readAuditFixture(t, path)[1]
	if *last.ExitCode != 30 || last.Stdout != out {
		t.Fatal(last)
	}
	code, _, stderr = execute("log-read", []string{"--config-file", config, "--not-a-flag"}, "")
	last = readAuditFixture(t, path)[3]
	if code != 2 || *last.ExitCode != 2 || last.Stderr != stderr {
		t.Fatalf("%d %+v", code, last)
	}
	got := strings.Join(auditArguments([]string{"--token", "secret-one", "--password=secret-two", "--value", "secret-three", "--text=secret-four", "--format", "json"}), " ")
	if strings.Contains(got, "secret-") || !strings.Contains(got, "--format json") {
		t.Fatal(got)
	}
	code, _, stderr = execute("config", []string{"credential-set", "--file", config, "--provider", "openai", "--stdin"}, "secret-five\n")
	if code != 0 {
		t.Fatalf("%d %s", code, stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte("secret-five")) {
		t.Fatalf("credential leaked: %v", err)
	}
}

func TestAuditCaptureRedactionLimitsAndMetadataOnly(t *testing.T) {
	for _, value := range []string{`{"token":{"nested":"secret-value"}}`, `{"credentials":["secret-value"]}`, "https://user:secret-value@example.com"} {
		if strings.Contains(auditText(value), "secret-value") {
			t.Fatalf("secret leaked: %s", value)
		}
	}
	var destination bytes.Buffer
	w := &auditCapture{destination: &destination, limit: 4096}
	for _, chunk := range []string{"token=", "secret-value\n", "-----BEGIN PRIVATE KEY-----\n", "private-material\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	text, size, truncated := w.snapshot()
	if size != int64(destination.Len()) || truncated || strings.Contains(text, "secret-value") || strings.Contains(text, "private-material") || !strings.Contains(destination.String(), "secret-value") {
		t.Fatalf("%s %d %v", text, size, truncated)
	}
	config, path := auditFixture(t, "redacted", 32)
	code, out, _ := execute("log-read", []string{"--config-file", config}, "a very long log message that must not truncate the actual command output\n")
	r := readAuditFixture(t, path)[1]
	if code != 0 || !r.StdoutTruncated || r.StdoutBytes != int64(len(out)) || len(r.Stdout) > 32 || !strings.Contains(strings.Join(strings.Fields(out), " "), "actual command output") {
		t.Fatalf("%d %s %+v", code, out, r)
	}
	config, path = auditFixture(t, "none", 0)
	code, out, _ = execute("log-read", []string{"--config-file", config}, "hello\n")
	r = readAuditFixture(t, path)[1]
	if code != 0 || r.OutputMode != "none" || r.Stdout != "" || r.StdoutTruncated || r.StdoutBytes != int64(len(out)) {
		t.Fatal(r)
	}
}

func TestAuditStartFailurePreventsMutation(t *testing.T) {
	for _, kind := range []string{"directory", "public", "symlink", "partial"} {
		t.Run(kind, func(t *testing.T) {
			config, path := auditFixture(t, "redacted", 0)
			switch kind {
			case "partial":
				if err := os.WriteFile(path, []byte(`{"event":`), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := writeFixture(t, t.TempDir(), "target", "unchanged")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			state := filepath.Join(t.TempDir(), "incident.json")
			code, _, stderr := execute("incident", []string{"start", "--config-file", config, "--state", state, "--title", "Test"}, "")
			if _, err := os.Stat(state); code != 2 || !os.IsNotExist(err) || !strings.Contains(stderr, "command not run") {
				t.Fatalf("%d %s %v", code, stderr, err)
			}
		})
	}
}

type auditFinishFailureWriter struct {
	bytes.Buffer
	once sync.Once
	fail func()
}

func (w *auditFinishFailureWriter) Write(p []byte) (int, error) {
	w.once.Do(w.fail)
	return w.Buffer.Write(p)
}

func TestAuditCompletionFailureReportsExecutedCommand(t *testing.T) {
	config, path := auditFixture(t, "redacted", 0)
	output := &auditFinishFailureWriter{fail: func() {
		if err := os.Rename(path, path+".saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}}
	var stderr bytes.Buffer
	code := Run("log-read", []string{"--config-file", config}, strings.NewReader("hello\n"), output, &stderr)
	if code != 2 || !strings.Contains(output.String(), "hello") || !strings.Contains(stderr.String(), "command exited 0 but audit completion could not be saved") {
		t.Fatalf("%d %s %s", code, output.String(), stderr.String())
	}
	r := readAuditFixture(t, path+".saved")
	if len(r) != 1 || r[0].Event != "start" {
		t.Fatal(r)
	}
}

func TestAuditSeparateFilesAndConcurrentRecords(t *testing.T) {
	config, path := auditFixture(t, "redacted", 0)
	code, _, stderr := execute("to-markdown", []string{"--config-file", config, "--from", "csv", "--output", path}, "a,b\n1,2\n")
	if code != 2 || !strings.Contains(stderr, "separate") {
		t.Fatalf("%d %s", code, stderr)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errors <- appendAudit(path, auditRecord{SchemaVersion: "1", RunID: fmt.Sprint(i), Event: "start"})
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(readAuditFixture(t, path)) != 12 {
		t.Fatal("lost records")
	}
}

func TestAuditConfigurationValidationAndDefaults(t *testing.T) {
	c := defaultAppConfig()
	if c.Audit.Enabled {
		t.Fatal("audit should be opt-in")
	}
	for _, audit := range []auditConfig{{Output: "raw"}, {MaxOutputBytes: -1}, {MaxOutputBytes: (1 << 20) + 1}, {File: "bad\npath"}} {
		c.Audit = audit
		if validateAppConfig(c) == nil {
			t.Fatalf("accepted %+v", audit)
		}
	}
	config, _ := auditFixture(t, "redacted", 0)
	code, _, stderr := execute("config", []string{"set", "--file", config, "--key", "audit.max_output_bytes", "--value", "2048"}, "")
	if code != 0 {
		t.Fatalf("%d %s", code, stderr)
	}
	loaded, _, err := loadAppConfig(config)
	if err != nil || loaded.Audit.MaxOutputBytes != 2048 {
		t.Fatalf("%+v %v", loaded.Audit, err)
	}
}
