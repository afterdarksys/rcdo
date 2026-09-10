package toolkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownViewerReading(t *testing.T) {
	input := "Intro text.\n\n# Heading **one**\n\nA [link](https://example.test) and ![diagram](diagram.png).\n\n- [x] Completed\n- [ ] Pending\n\n```sh\n  echo '<x>'\n# not a heading\n```\n\n## Data\n\n| Name | Value |\n| --- | --- |\n| api | 4 |\n\n> Quoted text\n"
	code, out, err := execute("markdown-view", nil, input)
	if code != 0 || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	for _, want := range []string{"Section 1 of 3", "Heading one", "link: https://example.test", "Image: diagram", "Task completed:", "Task not completed:", "  echo '<x>'", "# not a heading", "Name (column 1): api", "Value (column 2): 4", "Quote:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "**one**") || strings.Contains(out, "| --- |") {
		t.Fatal(out)
	}
	code, out, err = execute("markdown-view", []string{"--toc"}, input)
	if code != 0 || !strings.Contains(out, "Heading one") || strings.Contains(out, "echo") || strings.Contains(out, "Quoted") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("markdown-view", []string{"--section", "3", "--format", "json"}, input)
	var doc markdownDocument
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || len(doc.Sections) != 1 || doc.Sections[0].Title != "Data" {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestMarkdownSearchAndSetext(t *testing.T) {
	input := "Repeated\n========\n\nFirst &lt;entry&gt;.\n\n# Repeated\n\nSecond content.\n"
	code, out, err := execute("markdown-view", []string{"--find", "<ENTRY>"}, input)
	if code != 0 || !strings.Contains(out, "First <entry>") || strings.Contains(out, "Second content") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, _ = execute("markdown-view", []string{"--find", "absent"}, input)
	if code != 0 || !strings.Contains(out, "No matching sections") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = execute("markdown-view", []string{"--section", "2"}, input)
	if code != 0 || !strings.Contains(out, "Second content") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestMarkdownTerminalSafetyAndCode(t *testing.T) {
	input := "# Title\x1b[31m\n\n```\n\t<literal>&amp;\\*\x1b]52;c;evil\a\n```\n\n<script>alert('x')</script>\n"
	code, out, err := execute("markdown-view", nil, input)
	if code != 0 || strings.ContainsAny(out, "\x1b\a") || !strings.Contains(out, "\t<literal>&amp;\\*") || !strings.Contains(out, "HTML source (not rendered)") {
		t.Fatalf("%d %q %s", code, out, err)
	}
}
func TestMarkdownNavigationAndFreshness(t *testing.T) {
	dir := t.TempDir()
	source := writeFixture(t, dir, "source.md", "# First\n\nOne.\n\n# Second\n\nTwo.\n")
	state := filepath.Join(dir, "reading.json")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("markdown-view", append([]string{mode, "--state", state}, args...), "")
	}
	code, out, err := run("start", "--input", source)
	if code != 0 || !strings.Contains(out, "First") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = run("next")
	if code != 0 || !strings.Contains(out, "Second") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = run("bookmark", "--name", "data")
	if code != 0 || !strings.Contains(out, "Bookmark saved") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, _, _ = run("previous")
	if code != 0 {
		t.Fatal(code)
	}
	code, out, err = run("goto", "--name", "data")
	if code != 0 || !strings.Contains(out, "Second") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, _ = run("next")
	if code != 0 || !strings.Contains(out, "End of document") {
		t.Fatalf("%d %s", code, out)
	}
	before, _ := os.ReadFile(state)
	if e := os.WriteFile(source, []byte("# Changed\n"), 0600); e != nil {
		t.Fatal(e)
	}
	code, out, err = run("show")
	if code != 30 || out != "" || !strings.Contains(err, "source changed") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	after, _ := os.ReadFile(state)
	if string(before) != string(after) {
		t.Fatal("stale read modified state")
	}
}
func TestMarkdownNavigationRejectsOverwriteAndAliases(t *testing.T) {
	dir := t.TempDir()
	source := writeFixture(t, dir, "source.md", "# First\n")
	state := filepath.Join(dir, "state.json")
	code, _, err := execute("markdown-view", []string{"start", "--input", source, "--state", source}, "")
	if code != 2 {
		t.Fatal(code, err)
	}
	link := filepath.Join(dir, "hardlink.md")
	if e := os.Link(source, link); e != nil {
		t.Fatal(e)
	}
	code, _, _ = execute("markdown-view", []string{"start", "--input", source, "--state", link}, "")
	if code != 2 {
		t.Fatal(code)
	}
	code, _, _ = execute("markdown-view", []string{"start", "--input", source, "--state", state}, "")
	if code != 0 {
		t.Fatal(code)
	}
	code, _, _ = execute("markdown-view", []string{"start", "--input", source, "--state", state}, "")
	if code != 2 {
		t.Fatal("overwrote state", code)
	}
	code, _, _ = execute("markdown-view", []string{"goto", "--state", state, "--section", "99"}, "")
	if code != 2 {
		t.Fatal(code)
	}
	code, out, _ := execute("markdown-view", []string{"show", "--state", state}, "")
	if code != 0 || !strings.Contains(out, "First") {
		t.Fatal(code, out)
	}
}
func TestMarkdownConversionPipeline(t *testing.T) {
	code, md, err := execute("to-markdown", []string{"--from", "csv"}, "Name,Value\nservice,4\n")
	if code != 0 {
		t.Fatal(code, err)
	}
	code, out, err := execute("markdown-view", nil, md)
	if code != 0 || !strings.Contains(out, "Name (column 1): service") || !strings.Contains(out, "Value (column 2): 4") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestMarkdownViewerWidthAndExactHash(t *testing.T) {
	input := "# Heading\r\n\r\n" + strings.Repeat("readable prose ", 30) + "\r\n"
	code, out, err := execute("markdown-view", []string{"--width", "40"}, input)
	if code != 0 {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 40 {
			t.Fatal(line)
		}
	}
	doc, parseErr := parseMarkdown([]byte(input))
	if parseErr != nil || doc.SourceSHA256 != digestBytes([]byte(input)) {
		t.Fatal("fingerprint did not bind exact source bytes")
	}
}
