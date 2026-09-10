package toolkit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsIndependentGapsAndKeepsSettingsPrivate(t *testing.T) {
	oldLook, oldVersion := doctorLookPath, doctorVersion
	defer func() { doctorLookPath = oldLook; doctorVersion = oldVersion }()
	doctorLookPath = func(name string) (string, error) {
		if name == "git" {
			return "/test/git", nil
		}
		return "", errors.New("missing")
	}
	calls := 0
	doctorVersion = func(name string, args ...string) commandResult {
		calls++
		if name != "/test/git" || len(args) != 1 || args[0] != "--version" {
			t.Fatalf("unexpected invocation %s %v", name, args)
		}
		return commandResult{stdout: []byte("git version test")}
	}
	t.Setenv("PAGER", "private-pager-value")
	code, out, e := execute("doctor", []string{"--sample", "--evidence-dir", filepath.Join(t.TempDir(), "missing")}, "")
	if code != 30 || !strings.Contains(out, "PAGER") || !strings.Contains(out, "Optional CLI") || strings.Contains(out, "private-pager-value") || calls != 0 {
		t.Fatalf("%d %s %s calls=%d", code, out, e, calls)
	}
	dir := t.TempDir()
	code, out, e = execute("doctor", []string{"--versions", "--evidence-dir", dir}, "")
	if code != 10 || calls != 1 || !strings.Contains(out, "git version test") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("probe left files")
	}
}
func TestDoctorFailedVersionIsIncomplete(t *testing.T) {
	oldLook, oldVersion := doctorLookPath, doctorVersion
	defer func() { doctorLookPath = oldLook; doctorVersion = oldVersion }()
	doctorLookPath = func(name string) (string, error) { return name, nil }
	doctorVersion = func(string, ...string) commandResult {
		return commandResult{err: errors.New("failed"), stderr: "secret error"}
	}
	code, out, e := execute("doctor", []string{"--versions"}, "")
	if code != 30 || strings.Contains(out, "secret error") {
		t.Fatalf("%d %s %s", code, out, e)
	}
}
