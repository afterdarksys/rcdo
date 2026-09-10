package toolkit

import (
	"strings"
	"testing"
	"time"
)

func TestResourceWalkDirectionsCyclesAndCoverage(t *testing.T) {
	g := relationGraph{SchemaVersion: "1", Complete: boolPtr(true), CollectedAt: time.Now().UTC().Format(time.RFC3339), Source: "fixture", Scopes: []relationScope{{Account: "123", Region: "us-east-1", Complete: boolPtr(true), Outcome: "pass"}}, Nodes: []relationNode{{"lb", "load-balancer", "123", "us-east-1"}, {"instance", "compute", "123", "us-east-1"}, {"sg", "security-group", "123", "us-east-1"}}, Edges: []relationEdge{{"lb", "instance", "observed", "cloud API"}, {"instance", "sg", "configuration", "main.tf"}}}
	code, out, err := execute("resource-walk", []string{"--resource", "lb", "--depth", "2"}, jsonFixture(g))
	if code != 10 || !strings.Contains(out, "Choice 2: sg") || !strings.Contains(out, "configuration") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("resource-walk", []string{"--resource", "sg", "--direction", "dependents"}, jsonFixture(g))
	if code != 10 || !strings.Contains(out, "Choice 1: instance") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	g.Edges = append(g.Edges, relationEdge{"sg", "lb", "inferred", "operator"})
	code, out, _ = execute("resource-walk", []string{"--resource", "lb", "--depth", "10"}, jsonFixture(g))
	if code != 10 || !strings.Contains(out, "cycle") {
		t.Fatalf("%d %s", code, out)
	}
	code, out, _ = execute("resource-walk", []string{"--resource", "lb", "--require-scope", "123/us-west-2"}, jsonFixture(g))
	if code != 30 || !strings.Contains(out, "Required scope missing") {
		t.Fatalf("%d %s", code, out)
	}
	g.Edges = append(g.Edges, relationEdge{"sg", "missing", "observed", "API"})
	code, out, _ = execute("resource-walk", []string{"--resource", "lb"}, jsonFixture(g))
	if code != 30 {
		t.Fatalf("%d %s", code, out)
	}
}
