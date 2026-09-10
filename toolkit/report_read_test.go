package toolkit

import (
	"bytes"
	"git-tools/finding"
	"strings"
	"testing"
)

func TestReportLayoutsPreserveRiskAndCoverage(t *testing.T) {
	r := finding.Report{CompletedChecks: []string{"parsed"}, IncompleteChecks: []string{"region coverage missing"}, Findings: []finding.Finding{makeFinding("F-1", finding.SeverityCritical, "Replace database", "Db_1", "replace", "production", "data loss possible", "plan evidence", "take snapshot"), makeFinding("F-2", finding.SeverityInfo, "Info", "other", "read", "test", "note", "record", "review")}}
	var input bytes.Buffer
	if e := finding.RenderJSON(&input, r); e != nil {
		t.Fatal(e)
	}
	for _, layout := range []string{"plain", "speech", "braille"} {
		code, out, e := execute("report-read", []string{"--layout", layout, "--id", "F-1", "--spell", "resource"}, input.String())
		if code != 30 {
			t.Fatalf("%s %d %s %s", layout, code, out, e)
		}
		for _, want := range []string{"critical", "production", "region coverage missing", "data loss possible", "take snapshot", "capital D", "lowercase b", "underscore", "digit 1"} {
			if !strings.Contains(strings.Join(strings.Fields(out), " "), want) {
				t.Errorf("%s missing %q: %s", layout, want, out)
			}
		}
		if strings.Contains(out, "Resource: other") {
			t.Fatal("selection ignored")
		}
		if layout == "braille" {
			for _, l := range strings.Split(out, "\n") {
				if len([]rune(l)) > 40 {
					t.Fatal("braille width exceeded")
				}
			}
		}
	}
}
func TestReportReadingStripsTerminalControls(t *testing.T) {
	r := finding.Report{CompletedChecks: []string{"source\x1b[2J\r\u202Epayload"}}
	var b bytes.Buffer
	_ = finding.RenderJSON(&b, r)
	code, out, e := execute("report-read", nil, b.String())
	if code != 0 || strings.ContainsAny(out, "\x1b\r\u202e") {
		t.Fatalf("%d %q %s", code, out, e)
	}
	code, _, _ = execute("report-read", []string{"--id", "missing"}, b.String())
	if code != 2 {
		t.Fatal("missing selection accepted")
	}
	code, _, _ = execute("report-read", nil, strings.Replace(b.String(), `"schema_version": "1"`, `"schema_version": "1", "schema_version": "1"`, 1))
	if code != 2 {
		t.Fatal("duplicate keys accepted")
	}
}
