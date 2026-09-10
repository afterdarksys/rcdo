package toolkit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditReadRotateRetainAndPrune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	archive := filepath.Join(dir, "old.gz")
	zero := 0
	at := time.Now().UTC().Add(-48 * time.Hour)
	records := []auditRecord{{SchemaVersion: "1", RunID: "a", Event: "start", At: at, Command: "log-read", User: "alice"}, {SchemaVersion: "1", RunID: "b", Event: "start", At: at, Command: "watch", User: "bob"}, {SchemaVersion: "1", RunID: "a", Event: "finish", At: at, Command: "log-read", User: "alice", ExitCode: &zero, Stdout: "token=private-value"}}
	for _, r := range records {
		if err := appendAudit(path, r); err != nil {
			t.Fatal(err)
		}
	}
	code, out, err := execute("audit", []string{"--input", path, "--user", "alice", "--show-output", "--format", "json"}, "")
	if code != 0 || strings.Contains(out, "private-value") || !strings.Contains(out, `"matched":1`) {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, _, _ = execute("audit", []string{"--input", path, "--unfinished"}, "")
	if code != 30 {
		t.Fatal(code)
	}
	var buf bytes.Buffer
	if err := rotateAudit(path, archive, false, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("preview wrote archive")
	}
	if err := rotateAudit(path, archive, true, &buf); err != nil {
		t.Fatal(err)
	}
	_, live, e := readAuditRecords(path)
	if e != nil || len(live) != 1 || live[0].RunID != "b" {
		t.Fatalf("%+v %v", live, e)
	}
	_, old, e := readAuditRecords(archive)
	if e != nil || len(old) != 2 {
		t.Fatalf("%+v %v", old, e)
	}
	if err := pruneAudit(archive, 72*time.Hour, true, &buf); err == nil {
		t.Fatal("pruned recent archive")
	}
	if err := pruneAudit(archive, 24*time.Hour, true, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("archive not removed")
	}
}

func TestAuditReaderRejectsBrokenPair(t *testing.T) {
	r := auditRecord{SchemaVersion: "1", RunID: "x", Event: "finish", At: time.Now().UTC()}
	b, _ := json.Marshal(r)
	path := writeFixture(t, t.TempDir(), "audit", string(b)+"\n")
	if _, _, err := readAuditRecords(path); err == nil {
		t.Fatal("accepted orphan finish")
	}
}
