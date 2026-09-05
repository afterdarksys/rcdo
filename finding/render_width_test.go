package finding

import (
	"bytes"
	"strings"
	"testing"
)

// widthReport builds a report whose lines are comfortably longer than any
// width under test, so a failure to wrap is visible rather than incidental.
func widthReport(t *testing.T) Report {
	t.Helper()
	return Report{
		Findings: []Finding{{
			ID:          "RCDO-WIDTH-001",
			Severity:    SeverityHigh,
			Title:       "security group ingress opens a very wide port range to the public internet",
			Resource:    "aws_security_group.production_ingress_rules_for_the_payments_service",
			Action:      "update",
			Environment: "production",
			Reason:      "opening the payments ingress to the public internet exposes the service to the whole world",
			Evidence:    []string{"cidr_blocks changed from 10.0.0.0/8 to 0.0.0.0/0 across every listed ingress rule"},
			Confidence:  ConfidenceHigh,
			Remediation: "restrict cidr_blocks to the corporate range and re-run the plan before approving this change",
		}},
	}
}

func longestLine(s string) int {
	longest := 0
	for _, line := range strings.Split(s, "\n") {
		if n := len([]rune(line)); n > longest {
			longest = n
		}
	}
	return longest
}

// The regression this file exists for: review commands rendered at a hardcoded
// 100 columns and ignored --width entirely, while every other text command
// honoured it. A large-print user who set a narrow width still got 100.
func TestRenderTextWidthWrapsToRequestedWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100} {
		var buf bytes.Buffer
		if err := RenderTextWidth(&buf, widthReport(t), width); err != nil {
			t.Fatalf("width %d: %v", width, err)
		}
		if got := longestLine(buf.String()); got > width {
			t.Errorf("width %d: longest line was %d columns", width, got)
		}
	}
}

func TestRenderTextWidthNarrowerWidthActuallyChangesOutput(t *testing.T) {
	var wide, narrow bytes.Buffer
	if err := RenderTextWidth(&wide, widthReport(t), 100); err != nil {
		t.Fatal(err)
	}
	if err := RenderTextWidth(&narrow, widthReport(t), 40); err != nil {
		t.Fatal(err)
	}
	// Guards against a wrapper that validates the width and then ignores it.
	if wide.String() == narrow.String() {
		t.Fatal("width had no effect on the rendered report")
	}
	if longestLine(narrow.String()) >= longestLine(wide.String()) {
		t.Fatal("narrow width did not produce shorter lines")
	}
}

// Fail closed rather than silently rendering something unreadable.
func TestRenderTextWidthRejectsWidthBelowMinimum(t *testing.T) {
	var buf bytes.Buffer
	err := RenderTextWidth(&buf, widthReport(t), MinTextWidth-1)
	if err == nil {
		t.Fatal("expected an error for a width below the minimum")
	}
	if buf.Len() != 0 {
		t.Fatalf("a rejected width must render nothing, wrote %d bytes", buf.Len())
	}
}

// RenderText is the pre-existing entry point; its behaviour must not move.
func TestRenderTextStillDefaultsToOneHundred(t *testing.T) {
	var viaDefault, viaExplicit bytes.Buffer
	if err := RenderText(&viaDefault, widthReport(t)); err != nil {
		t.Fatal(err)
	}
	if err := RenderTextWidth(&viaExplicit, widthReport(t), DefaultTextWidth); err != nil {
		t.Fatal(err)
	}
	if viaDefault.String() != viaExplicit.String() {
		t.Fatal("RenderText no longer matches RenderTextWidth at DefaultTextWidth")
	}
}

// Wrapping must not eat the label that makes a line readable out of context —
// a screen reader hears these one line at a time.
func TestRenderTextWidthKeepsLabelsIntact(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderTextWidth(&buf, widthReport(t), 40); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, label := range []string{"REVIEW RESULT:", "FINDINGS:"} {
		if !strings.Contains(out, label) {
			t.Errorf("label %q did not survive wrapping at 40 columns", label)
		}
	}
}
