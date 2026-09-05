package toolkit

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardedMutationRefusesStaleSource(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	old := []byte(`{"a":1}`)
	os.WriteFile(p, old, 0600)
	hash := fmt.Sprintf("%x", sha256.Sum256(old))
	os.WriteFile(p, []byte(`{"a":2}`), 0600)
	for _, extra := range [][]string{{}, {"--expect-sha256", hash}} {
		args := append([]string{"--input", p, "--path", "a", "--value", "3", "--write"}, extra...)
		code, _, _ := execute("config-set", args, "")
		got, _ := os.ReadFile(p)
		if code == 0 || string(got) != `{"a":2}` {
			t.Fatal("stale or unbound edit accepted")
		}
	}
}
func TestConfigWalkPersistsAndDetectsMissingPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	state := filepath.Join(dir, "state.json")
	os.WriteFile(p, []byte(`{"service":{"port":80,"password":"private"}}`), 0600)
	run := func(args ...string) (int, string) {
		t.Helper()
		args = append(args, "--state", state)
		c, o, e := execute("config-walk", args, "")
		if e != "" {
			t.Log(e)
		}
		if strings.Contains(o, "private") {
			t.Fatal("secret leaked")
		}
		return c, o
	}
	if c, _ := run("start", "--input", p); c != 0 {
		t.Fatal(c)
	}
	if c, o := run("goto", "--path", "$.service.port"); c != 0 || !strings.Contains(o, "$.service.port") {
		t.Fatal(c, o)
	}
	if c, o := run("show"); c != 0 || !strings.Contains(o, "$.service.port") {
		t.Fatal(c, o)
	}
	os.WriteFile(p, []byte(`{"service":{}}`), 0600)
	if c, o := run("show"); c != 30 || !strings.Contains(o, "path_missing") {
		t.Fatal(c, o)
	}
	if c, _ := run("goto", "--path", "$"); c != 0 {
		t.Fatal(c)
	}
}
func TestAmbiguousConfigurationRejected(t *testing.T) {
	for _, tc := range []struct{ syntax, data string }{{"json", `{"a":1,"a":2}`}, {"json", `{} {}`}, {"yaml", "a: 1\na: 2\n"}, {"yaml", "a: &a [*a]\n"}} {
		if validateConfigDocument(tc.syntax, []byte(tc.data)) == nil {
			t.Fatal(tc)
		}
	}
}
func TestGuardedEditsSupportedSyntaxes(t *testing.T) {
	for _, tc := range []struct{ ext, data, path string }{{"json", `{"a]b":1}`, `$["a]b"]`}, {"yaml", "a: 1\n", "a"}, {"toml", "a = 1\n", "a"}, {"tf", "a = 1\n", "a"}} {
		t.Run(tc.ext, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x."+tc.ext)
			os.WriteFile(p, []byte(tc.data), 0600)
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(tc.data)))
			args := []string{"--input", p, "--path", tc.path, "--value", "2", "--write", "--expect-sha256", hash, "--expect-value"}
			c, _, _ := execute("config-set", append(args, "9"), "")
			b, _ := os.ReadFile(p)
			if c == 0 || string(b) != tc.data {
				t.Fatal("mismatch wrote input")
			}
			c, o, e := execute("config-set", append(args, "1"), "")
			if c != 0 {
				t.Fatal(c, o, e)
			}
		})
	}
}
func TestReceiptReviewNeverHidesEarlyFailure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "receipt.json")
	for _, tc := range []struct {
		state, stages string
		code          int
	}{{"completed", `[{"index":1,"state":"exited","exit_code":7},{"index":2,"state":"exited","exit_code":0}]`, 10}, {"running", `[{"index":1,"state":"not_started","exit_code":null}]`, 30}, {"completed", `[{"index":1,"state":"exited","exit_code":0}]`, 0}} {
		os.WriteFile(p, []byte(fmt.Sprintf(`{"schema":"missing-utils/runreceipt/v1","scope":"local_process_only","state":%q,"stages":%s}`, tc.state, tc.stages)), 0600)
		c, o, e := execute("receipt-review", []string{"--input", p}, "")
		if c != tc.code || !strings.Contains(o, "Outcome verified: no") {
			t.Fatal(c, o, e)
		}
	}
}
