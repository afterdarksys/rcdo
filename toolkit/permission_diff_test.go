package toolkit

import (
	"git-tools/finding"
	"strings"
	"testing"
)

func TestPermissionDenyRemovalAndCanonicalActions(t *testing.T) {
	before := []byte(`{"Statement":[{"Sid":"old","Effect":"Allow","Action":["s3:ListBucket","s3:GetObject"],"Resource":"*"},{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`)
	after := []byte(`{"Statement":[{"Sid":"new","Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":["*"]}]}`)
	var r finding.Report
	if err := explainPermissionDelta(&r, before, after, "policy", "test"); err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Title, "Deny clause removed") || r.Findings[0].Severity != finding.SeverityHigh {
		t.Fatalf("%+v", r)
	}
}
func TestPermissionConditionalNotActionIsIncomplete(t *testing.T) {
	var r finding.Report
	err := explainPermissionDelta(&r, []byte(`{"Statement":[]}`), []byte(`{"Statement":{"Effect":"Allow","NotAction":"iam:*","Resource":"*","Condition":{"Bool":{"aws:MultiFactorAuthPresent":"true"}}}}`), "policy", "test")
	if err != nil || len(r.IncompleteChecks) == 0 {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestNetworkScopeComparison(t *testing.T) {
	b := []byte(`{"schema_version":"1","rules":[{"id":"ssh","direction":"ingress","protocol":"tcp","from_port":22,"to_port":22,"cidr":"10.0.0.0/24"}]}`)
	a := []byte(`{"schema_version":"1","rules":[{"id":"ssh","direction":"ingress","protocol":"tcp","from_port":0,"to_port":65535,"cidr":"0.0.0.0/0"}]}`)
	var r finding.Report
	if err := explainNetworkDelta(&r, b, a, "test"); err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Title, "broadened") {
		t.Fatalf("%+v", r)
	}
}

func TestPermissionPreservesExactConditionNumbers(t *testing.T) {
	before := []byte(`{"Statement":{"Effect":"Allow","Action":"s3:*","Resource":"*","Condition":{"NumericEquals":{"x":9007199254740992}}}}`)
	after := []byte(strings.ReplaceAll(string(before), "9007199254740992", "9007199254740993"))
	var r finding.Report
	if err := explainPermissionDelta(&r, before, after, "policy", "test"); err != nil || len(r.Findings) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}
