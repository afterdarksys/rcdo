package diffwalk

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/main.tf b/main.tf
index 1111111..2222222 100644
--- a/main.tf
+++ b/main.tf
@@ -1,3 +1,4 @@ resource "aws_instance" "web" {
   ami = "ami-old"
-  instance_type = "t3.small"
+  instance_type = "t3.large"
+  monitoring = true
 }
diff --git a/old.yml b/new.yml
similarity index 91%
rename from old.yml
rename to new.yml
--- a/old.yml
+++ b/new.yml
@@ -10,2 +10,2 @@ tasks:
-- name: restart all servers
+- name: restart web servers
   service: nginx
diff --git a/obsolete.txt b/obsolete.txt
deleted file mode 100644
--- a/obsolete.txt
+++ /dev/null
@@ -1 +0,0 @@
-retire me
diff --git a/logo.png b/logo.png
index 3333333..4444444 100644
Binary files a/logo.png and b/logo.png differ
`

func TestParseSummarizesAndClassifiesDiff(t *testing.T) {
	diff, err := Parse(strings.NewReader(sampleDiff))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(diff.Files) != 4 {
		t.Fatalf("len(Files) = %d, want 4", len(diff.Files))
	}
	wantTypes := []ChangeType{Modified, Renamed, Deleted, Modified}
	for i, want := range wantTypes {
		if diff.Files[i].Change != want {
			t.Errorf("Files[%d].Change = %q, want %q", i, diff.Files[i].Change, want)
		}
	}
	if !diff.Files[3].Binary {
		t.Error("binary file was not identified")
	}
	want := Summary{Files: 4, Hunks: 3, Additions: 3, Deletions: 3, BinaryFiles: 1}
	if got := diff.Summary(); got != want {
		t.Fatalf("Summary() = %+v, want %+v", got, want)
	}
}

func TestParseTracksLineNumbersAndKinds(t *testing.T) {
	diff, err := Parse(strings.NewReader(sampleDiff))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	lines := diff.Files[0].Hunks[0].Lines
	wants := []Line{
		{Kind: ContextLine, OldNumber: 1, NewNumber: 1, Content: `  ami = "ami-old"`},
		{Kind: RemovedLine, OldNumber: 2, Content: `  instance_type = "t3.small"`},
		{Kind: AddedLine, NewNumber: 2, Content: `  instance_type = "t3.large"`},
		{Kind: AddedLine, NewNumber: 3, Content: `  monitoring = true`},
		{Kind: ContextLine, OldNumber: 3, NewNumber: 4, Content: `}`},
	}
	if len(lines) != len(wants) {
		t.Fatalf("len(Lines) = %d, want %d", len(lines), len(wants))
	}
	for i, want := range wants {
		if lines[i] != want {
			t.Errorf("Lines[%d] = %+v, want %+v", i, lines[i], want)
		}
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "empty", input: "", wantErr: "diff is empty"},
		{name: "not a diff", input: "hello\n", wantErr: "no file changes"},
		{name: "bad hunk header", input: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ nope\n", wantErr: "malformed hunk header"},
		{name: "bad hunk body", input: "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n?bad\n", wantErr: "unexpected hunk line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.input))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestSelectFileAndHunk(t *testing.T) {
	diff, err := Parse(strings.NewReader(sampleDiff))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := diff.Select(2, 1)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if len(selected.Files) != 1 || selected.Files[0].Path != "new.yml" || len(selected.Files[0].Hunks) != 1 {
		t.Fatalf("Select() = %+v", selected)
	}
	for _, tc := range []struct{ file, hunk int }{{5, 0}, {0, 1}, {1, 9}, {-1, 0}} {
		if _, err := diff.Select(tc.file, tc.hunk); err == nil {
			t.Errorf("Select(%d, %d) error = nil", tc.file, tc.hunk)
		}
	}
}

func TestSearchChangedLinesOnly(t *testing.T) {
	diff, err := Parse(strings.NewReader(sampleDiff))
	if err != nil {
		t.Fatal(err)
	}
	got, err := diff.Search("LARGE")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "main.tf" || len(got.Files[0].Hunks) != 1 {
		t.Fatalf("Search() = %+v", got)
	}
	contextOnly, err := diff.Search("ami-old")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(contextOnly.Files) != 0 {
		t.Fatalf("context-only search returned %d files, want 0", len(contextOnly.Files))
	}
	if _, err := diff.Search("   "); err == nil {
		t.Fatal("Search(blank) error = nil")
	}
}

func TestRenderTextIsLinearAndLabeled(t *testing.T) {
	diff, err := Parse(strings.NewReader(sampleDiff))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := diff.Select(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RenderText(&out, selected, false); err != nil {
		t.Fatalf("RenderText() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"DIFF SUMMARY\n",
		"Files: 1\nHunks: 1\nAdditions: 2\nDeletions: 1\n",
		"File 1 of 1\nPath: main.tf\nChange: MODIFIED\n",
		"Hunk 1 of 1\nLocation: old line 1, new line 1\n",
		"Context old 1 new 1:   ami = \"ami-old\"\n",
		"Removed old 2:   instance_type = \"t3.small\"\n",
		"Added new 2:   instance_type = \"t3.large\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderText() missing %q\noutput:\n%s", want, got)
		}
	}
}

func TestRenderTextSummaryOnly(t *testing.T) {
	diff, err := Parse(strings.NewReader(sampleDiff))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RenderText(&out, diff, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "File 1 of") {
		t.Fatalf("summary-only output included details:\n%s", out.String())
	}
}

func TestRenderJSONIsStructured(t *testing.T) {
	diff, err := Parse(strings.NewReader(sampleDiff))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RenderJSON(&out, diff); err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}
	var got struct {
		SchemaVersion string  `json:"schema_version"`
		Summary       Summary `json:"summary"`
		Files         []File  `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.SchemaVersion != SchemaVersion || got.Summary.Files != 4 || len(got.Files) != 4 {
		t.Fatalf("unexpected JSON: %+v", got)
	}
}
