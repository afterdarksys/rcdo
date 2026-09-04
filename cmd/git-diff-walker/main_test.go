package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliDiff = `diff --git a/app.yml b/app.yml
--- a/app.yml
+++ b/app.yml
@@ -1 +1 @@
-replicas: 2
+replicas: 20
`

func TestRunReadsStdinAndRendersText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, strings.NewReader(cliDiff), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{"DIFF SUMMARY", "Path: app.yml", "Removed old 1: replicas: 2", "Added new 1: replicas: 20"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunReadsNamedFileAndRendersJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "change.diff")
	if err := os.WriteFile(path, []byte(cliDiff), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--input", path, "--format", "json"}, strings.NewReader("ignored"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"schema_version": "1"`) || !strings.Contains(stdout.String(), `"path": "app.yml"`) {
		t.Fatalf("unexpected JSON output:\n%s", stdout.String())
	}
}

func TestRunSupportsSummarySelectionAndSearch(t *testing.T) {
	for _, args := range [][]string{
		{"--summary-only"},
		{"--file", "1", "--hunk", "1"},
		{"--search", "REPLICAS"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(cliDiff), &stdout, &stderr); code != 0 {
			t.Errorf("run(%v) = %d, stderr = %s", args, code, stderr.String())
		}
	}
}

func TestRunRejectsBadOptionsAndInput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		input   string
		wantErr string
	}{
		{name: "format", args: []string{"--format", "yaml"}, input: cliDiff, wantErr: "unknown format"},
		{name: "hunk without file", args: []string{"--hunk", "1"}, input: cliDiff, wantErr: "--hunk requires --file"},
		{name: "out of range file", args: []string{"--file", "9"}, input: cliDiff, wantErr: "out of range"},
		{name: "empty input", input: "", wantErr: "diff is empty"},
		{name: "unexpected argument", args: []string{"surprise"}, input: cliDiff, wantErr: "unexpected arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, strings.NewReader(tt.input), &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("run() = %d, stderr = %q; want code 2 and %q", code, stderr.String(), tt.wantErr)
			}
		})
	}
}

func TestRunVersionDoesNotReadInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("version output = %q, want %q", stdout.String(), version)
	}
}

func TestHelpIsLinearWithoutTabs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if strings.ContainsRune(stderr.String(), '\t') || !strings.Contains(stderr.String(), "Option: --format") {
		t.Fatalf("inaccessible help output: %q", stderr.String())
	}
}
