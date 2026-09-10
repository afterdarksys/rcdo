package toolkit

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gzipLogFixture(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCompressedLogReading(t *testing.T) {
	plain := "ok\nerror\nother\nerror\n"
	// Fixed bzip2 fixture keeps tests independent of an external compressor.
	bz, err := base64.StdEncoding.DecodeString("QlpoOTFBWSZTWbaXJa8AAATBgAAQAkiUACAAISjE0IYDppGWqG1jELxdyRThQkLaXJa8")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"gzip": gzipLogFixture(t, plain), "bzip2": bz} {
		t.Run(name, func(t *testing.T) {
			code, out, stderr := execute("log-read", []string{"--query", "error", "--format", "json"}, string(data))
			if code != 0 || !strings.Contains(out, `"matched_events":2`) || !strings.Contains(out, digestBytes(data)) {
				t.Fatalf("stdin: %d %s %s", code, out, stderr)
			}
			dir := t.TempDir()
			// Detection uses bytes, including when the filename has no suffix.
			source := writeFixture(t, dir, "log", string(data))
			state := filepath.Join(dir, "state.json")
			run := func(mode string, args ...string) (int, string, string) {
				return execute("log-read", append([]string{mode, "--state", state, "--context", "0"}, args...), "")
			}
			for _, step := range [][]string{
				{"start", "--input", source, "--query", "error"},
				{"next"}, {"bookmark", "--name", "second"},
				{"previous"}, {"goto", "--name", "second"}, {"show"},
			} {
				code, out, stderr = run(step[0], step[1:]...)
				if code != 0 {
					t.Fatalf("%v: %d %s %s", step, code, out, stderr)
				}
			}
			if !strings.Contains(out, "Line 4.") {
				t.Fatal(out)
			}
			before, err := os.ReadFile(state)
			if err != nil {
				t.Fatal(err)
			}
			// Same text with different encoding must still invalidate the binding.
			if err := os.WriteFile(source, []byte(plain), 0600); err != nil {
				t.Fatal(err)
			}
			code, out, _ = run("next")
			after, err := os.ReadFile(state)
			if err != nil {
				t.Fatal(err)
			}
			if code != 30 || out != "" || !bytes.Equal(before, after) {
				t.Fatalf("rotation: %d %s", code, out)
			}
			for _, invalid := range [][]byte{data[:len(data)-3], append(append([]byte{}, data...), []byte("garbage")...)} {
				code, out, _ := execute("log-read", nil, string(invalid))
				if code != 2 || out != "" {
					t.Fatalf("invalid stream: %d %s", code, out)
				}
			}
			joined := append(append([]byte{}, data...), data...)
			code, out, stderr = execute("log-read", []string{"--format", "json"}, string(joined))
			if code != 0 || !strings.Contains(out, `"total_events":8`) {
				t.Fatalf("concatenation: %d %s %s", code, out, stderr)
			}
		})
	}
}

func TestCompressedLogValidation(t *testing.T) {
	data := gzipLogFixture(t, "{\"request_id\":\"r1\",\"message\":\"token=secret-value\"}\n")
	code, out, stderr := execute("log-read", []string{"--syntax", "jsonl", "--request", "r1"}, string(data))
	if code != 0 || !strings.Contains(out, "1 match") || strings.Contains(out, "secret-value") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	corrupt := append([]byte{}, data...)
	corrupt[len(corrupt)-8] ^= 1 // gzip checksum
	bzOversized, err := base64.StdEncoding.DecodeString("QlpoOTFBWSZTWQyrD+QAQEAAgIBCAAggADDMBSmmAQDYgIB4u5IpwoSAZVh/IA==")
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{corrupt, {0x1f, 0x8b}, []byte("BZh0"), bzOversized, gzipLogFixture(t, strings.Repeat("x", (8<<20)+1)), gzipLogFixture(t, strings.Repeat("x", 65537)), bytes.Repeat([]byte("x"), (16<<20)+1)} {
		code, out, _ := execute("log-read", nil, string(data))
		if code != 2 || out != "" {
			t.Fatalf("invalid log: %d %s", code, out)
		}
	}
	dir := t.TempDir()
	source := writeFixture(t, dir, "broken.gz", string(corrupt))
	state := filepath.Join(dir, "state.json")
	code, out, _ = execute("log-read", []string{"start", "--input", source, "--state", state}, "")
	if _, err := os.Stat(state); code != 2 || out != "" || !os.IsNotExist(err) {
		t.Fatalf("invalid source created state: code=%d stat=%v", code, err)
	}
}

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
