package toolkit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type monitorAppendWriter struct {
	bytes.Buffer
	appendOnce func()
}

func (w *monitorAppendWriter) Write(p []byte) (int, error) {
	if w.appendOnce != nil {
		f := w.appendOnce
		w.appendOnce = nil
		f()
	}
	return w.Buffer.Write(p)
}
func TestMonitorFollowReadsNewAppend(t *testing.T) {
	dir := t.TempDir()
	source := writeFixture(t, dir, "live.log", "old\n")
	state := filepath.Join(dir, "state.json")
	if code, _, err := execute("monitor", []string{"start", "--input", source, "--state", state}, ""); code != 0 {
		t.Fatal(err)
	}
	writer := &monitorAppendWriter{appendOnce: func() {
		f, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.WriteString("new-event\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}}
	var errors bytes.Buffer
	code := Run("monitor", []string{"follow", "--state", state, "--interval", "100ms", "--duration", "300ms"}, strings.NewReader(""), writer, &errors)
	if code != 0 || !strings.Contains(writer.String(), "new-event") {
		t.Fatalf("%d %s %s", code, writer.String(), errors.String())
	}
}

func TestMonitorPauseBufferAndGap(t *testing.T) {
	dir := t.TempDir()
	source := writeFixture(t, dir, "events", "one\npartial")
	state := filepath.Join(dir, "monitor.json")
	run := func(mode string, more ...string) (int, string, string) {
		return execute("monitor", append([]string{mode, "--state", state, "--max-queue", "2", "--batch", "1"}, more...), "")
	}
	code, out, err := run("start", "--input", source)
	if code != 0 || !strings.Contains(out, "one") || strings.Contains(out, "partial") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	if code, _, _ := run("pause"); code != 0 {
		t.Fatal(code)
	}
	if err := os.WriteFile(source, []byte("one\npartial end\nthree\nfour\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, err = run("poll")
	if code != 30 || !strings.Contains(out, "Dropped: 1") || strings.Contains(out, "Line") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = run("resume")
	if code != 30 || !strings.Contains(out, "three") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	if err := os.WriteFile(source, []byte("rotated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, err = run("poll")
	if code != 30 || !strings.Contains(out, "Gap:") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = run("poll", "--accept-gap")
	if !strings.Contains(out, "Gap accepted") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestMonitorAnsibleNoLog(t *testing.T) {
	value, err := monitorLine([]byte(`{"schema_version":"1","event":"result","run_id":"r","sequence":1,"at":"2026-09-10T00:00:00Z","task":"private-value","no_log":true,"host":"web1","outcome":"ok"}`), "ansible")
	if err != nil || strings.Contains(value, "private-value") || !strings.Contains(value, "no_log") {
		t.Fatalf("%s %v", value, err)
	}
}
