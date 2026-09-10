package toolkit

import (
	"strings"
	"testing"
	"time"
)

func TestContextSummaryCoverageMismatchAndChange(t *testing.T) {
	dir := t.TempDir()
	expect := writeFixture(t, dir, "expect.json", `{"schema_version":"1","name":"production","contexts":{"cloud":{"kind":"aws","values":{"account":"123","region":"us-east-1"}},"kube":{"kind":"kubernetes","values":{"cluster":"prod","namespace":"api"}}}}`)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	b := contextBundle{SchemaVersion: "1", Complete: boolPtr(true), Contexts: map[string]contextObservation{"cloud": {Kind: "aws", Values: map[string]string{"account": "123", "region": "us-east-1"}, CollectedAt: now, Source: "sts", Outcome: "pass"}, "kube": {Kind: "kubernetes", Values: map[string]string{"cluster": "prod", "namespace": "api"}, CollectedAt: now, Source: "adapter", Outcome: "pass"}}}
	code, out, err := execute("context-summary", []string{"--expect", expect}, jsonFixture(b))
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	old := writeFixture(t, dir, "old.json", jsonFixture(b))
	b.Contexts["kube"].Values["namespace"] = "other"
	code, out, err = execute("context-summary", []string{"--expect", expect, "--before", old}, jsonFixture(b))
	if code != 20 || !strings.Contains(out, "context changed") || !strings.Contains(out, "namespace") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	delete(b.Contexts, "kube")
	code, out, _ = execute("context-summary", []string{"--expect", expect}, jsonFixture(b))
	if code != 30 || !strings.Contains(out, "Required context missing") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestContextSummaryExpiryAndStaleness(t *testing.T) {
	expect := writeFixture(t, t.TempDir(), "expect.json", `{"schema_version":"1","name":"work","contexts":{"tf":{"kind":"tofu","values":{"workspace":"prod"}}}}`)
	b := contextBundle{SchemaVersion: "1", Complete: boolPtr(true), Contexts: map[string]contextObservation{"tf": {Kind: "tofu", Values: map[string]string{"workspace": "prod"}, Source: "adapter", Outcome: "pass", CollectedAt: time.Now().Add(-time.Hour).Format(time.RFC3339), ExpiresAt: time.Now().Add(-time.Minute).Format(time.RFC3339)}}}
	code, out, _ := execute("context-summary", []string{"--expect", expect}, jsonFixture(b))
	if code != 30 || !strings.Contains(out, "expired") || !strings.Contains(out, "stale") {
		t.Fatalf("%d %s", code, out)
	}
}
