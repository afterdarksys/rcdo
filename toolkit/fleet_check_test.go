package toolkit

import (
	"strings"
	"testing"
	"time"
)

func TestFleetCoverageAndExceptions(t *testing.T) {
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	b := fleetBundle{SchemaVersion: "1", Complete: boolPtr(true), Hosts: []fleetHost{{"a", "linux", "web"}, {"b", "linux", "web"}, {"c", "linux", "web"}}, Baselines: map[string]fleetBaseline{"web": {Platform: "linux", Values: map[string]any{"version": "1", "service": true}, Source: "approved inventory", CollectedAt: stamp}}, Observations: []fleetObservation{{Host: "a", Platform: "linux", Outcome: "pass", Values: map[string]any{"version": "1", "service": true}, Source: "probe", CollectedAt: stamp}, {Host: "b", Outcome: "unreachable"}, {Host: "c", Platform: "linux", Outcome: "pass", Values: map[string]any{"version": "2"}, Source: "probe", CollectedAt: stamp}}}
	code, out, e := execute("fleet-check", nil, jsonFixture(b))
	if code != 30 || !strings.Contains(out, "unreachable") || !strings.Contains(out, "missing required field") || !strings.Contains(out, "differs from baseline") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	b.Hosts = b.Hosts[:1]
	b.Observations = b.Observations[:1]
	code, out, e = execute("fleet-check", nil, jsonFixture(b))
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	b.Observations[0].CollectedAt = now.Add(-time.Hour).Format(time.RFC3339)
	code, out, _ = execute("fleet-check", nil, jsonFixture(b))
	if code != 30 || !strings.Contains(out, "0 matching") {
		t.Fatalf("stale host counted clean: %d %s", code, out)
	}
}
func TestFleetRejectsDuplicatesAndMissingBaseline(t *testing.T) {
	b := fleetBundle{SchemaVersion: "1", Hosts: []fleetHost{{"a", "linux", "missing"}}, Baselines: map[string]fleetBaseline{}, Observations: []fleetObservation{}}
	r, e := compareFleet(b, time.Minute, time.Hour, time.Now())
	if e != nil || len(r.IncompleteChecks) < 2 {
		t.Fatalf("%+v %v", r, e)
	}
	b.Hosts = append(b.Hosts, b.Hosts[0])
	if _, e = compareFleet(b, time.Minute, time.Hour, time.Now()); e == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestFleetPreservesLargeIntegerDifferences(t *testing.T) {
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	data := `{"schema_version":"1","complete":true,"hosts":[{"id":"a","platform":"linux","baseline":"b"}],"baselines":{"b":{"platform":"linux","source":"test","collected_at":"` + stamp + `","values":{"revision":9007199254740992}}},"observations":[{"host":"a","platform":"linux","outcome":"pass","source":"test","collected_at":"` + stamp + `","values":{"revision":9007199254740993}}]}`
	code, out, e := execute("fleet-check", nil, data)
	if code != 10 {
		t.Fatalf("%d %s %s", code, out, e)
	}
}
