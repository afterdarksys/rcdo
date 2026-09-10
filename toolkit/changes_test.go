package toolkit

import (
	"bytes"
	"git-tools/finding"
	"strings"
	"testing"
	"time"
)

func TestChangesDoesNotInferRecoveryOrHideRisk(t *testing.T) {
	before := finding.Report{CompletedChecks: []string{"reviewed"}, Findings: []finding.Finding{makeFinding("a", finding.SeverityHigh, "Failure", "api", "check", "prod", "failed", "probe", "inspect")}}
	after := finding.Report{CompletedChecks: []string{"reviewed"}}
	r := reportChanges(before, after)
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Title, "recovery not verified") {
		t.Fatal(r)
	}
	after.Findings = before.Findings
	r = reportChanges(before, after)
	if r.Status() != finding.StatusBlocked || !strings.Contains(r.Findings[0].Title, "Still reported") {
		t.Fatal(r)
	}
	after.IncompleteChecks = []string{"probe unavailable"}
	r = reportChanges(before, after)
	if r.Status() != finding.StatusIncomplete {
		t.Fatal(r)
	}
	var a, b bytes.Buffer
	_ = finding.RenderJSON(&a, before)
	_ = finding.RenderJSON(&b, after)
	dir := t.TempDir()
	old := writeFixture(t, dir, "before.json", a.String())
	now := writeFixture(t, dir, "after.json", b.String())
	code, out, e := execute("changes", []string{"--before", old, "--after", now}, "")
	if code != 30 || !strings.Contains(out, "probe unavailable") {
		t.Fatalf("%d %s %s", code, out, e)
	}
}
func TestFleetChangesCoverageAndBaselineIdentity(t *testing.T) {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	b := fleetBundle{SchemaVersion: "1", Complete: boolPtr(true), Hosts: []fleetHost{{"h", "linux", "b"}}, Baselines: map[string]fleetBaseline{"b": {Platform: "linux", Values: map[string]any{"service": true}, Source: "test", CollectedAt: stamp}}, Observations: []fleetObservation{{Host: "h", Platform: "linux", Outcome: "pass", Source: "probe", CollectedAt: stamp, Values: map[string]any{"service": false}}}}
	a := b
	a.Observations = []fleetObservation{{Host: "h", Platform: "linux", Outcome: "pass", Source: "probe", CollectedAt: stamp, Values: map[string]any{"service": true}}}
	r, e := fleetChanges(b, a, time.Hour, time.Hour)
	if e != nil || !strings.Contains(r.Findings[0].Title, "different to matching") {
		t.Fatal(r, e)
	}
	a.Observations = []fleetObservation{}
	r, e = fleetChanges(b, a, time.Hour, time.Hour)
	if e != nil || r.Status() != finding.StatusIncomplete {
		t.Fatal(r, e)
	}
	a.Hosts = []fleetHost{{"new", "linux", "b"}}
	r, e = fleetChanges(b, a, time.Hour, time.Hour)
	if e != nil || len(r.IncompleteChecks) == 0 {
		t.Fatal(r, e)
	}
	a.Hosts = []fleetHost{{"h", "other", "b"}}
	if _, e = fleetChanges(b, a, time.Hour, time.Hour); e == nil {
		t.Fatal("platform mismatch accepted")
	}
}

func TestChangesTextSanitizesFindingIdentifiers(t *testing.T) {
	r := finding.Report{CompletedChecks: []string{"parsed"}, Findings: []finding.Finding{makeFinding("bad\x1b[2Jid", finding.SeverityHigh, "Failure", "api", "read", "test", "failure", "probe", "inspect")}}
	var b bytes.Buffer
	_ = finding.RenderJSON(&b, r)
	dir := t.TempDir()
	path := writeFixture(t, dir, "report.json", b.String())
	code, out, e := execute("changes", []string{"--before", path, "--after", path}, "")
	if code != 20 || strings.Contains(out, "\x1b") {
		t.Fatalf("%d %q %s", code, out, e)
	}
}
