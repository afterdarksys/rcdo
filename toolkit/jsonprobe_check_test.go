package toolkit

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func probeFixture(status string, age time.Duration) string {
	outcome := status
	if status == "error" {
		outcome = "partial"
	}
	return fmt.Sprintf(`{"schema":"missing-utils/jsonprobe/v1","outcome":%q,"collected_at":%q,"collector_version":"test","checks":[{"schema":"missing-utils/jsonprobe/v1","name":"api","type":"http","status":%q}]}`, outcome, time.Now().Add(-age).UTC().Format(time.RFC3339Nano), status)
}
func TestJSONProbeAdapterCoverageFreshnessAndStatus(t *testing.T) {
	cases := []struct {
		name, input, required string
		code                  int
		contains              string
	}{
		{"pass", probeFixture("pass", 0), "api", 0, "clean"},
		{"failed", probeFixture("fail", 0), "api", 20, "Readiness check failed"},
		{"partial", probeFixture("error", 0), "api", 30, "collection could not complete"},
		{"missing", probeFixture("pass", 0), "database", 30, "required jsonprobe check is missing"},
		{"stale", probeFixture("pass", time.Hour), "api", 30, "stale"},
		{"future", probeFixture("pass", -time.Hour), "api", 30, "future"},
		{"empty", "", "api", 30, "unavailable"},
		{"null", "null", "api", 30, "no checks"},
		{"old", `{"schema":"missing-utils/jsonprobe/v1","outcome":"pass","checks":[{"schema":"missing-utils/jsonprobe/v1","name":"api","type":"http","status":"pass"}]}`, "api", 30, "collection time or collector version"},
		{"contradiction", strings.Replace(probeFixture("fail", 0), `"outcome":"fail"`, `"outcome":"pass"`, 1), "api", 30, "outcome disagrees"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, err := execute("jsonprobe-check", []string{"--require", tc.required, "--environment", "demo", "--format", "json"}, tc.input)
			if code != tc.code || err != "" || !strings.Contains(out, tc.contains) {
				t.Fatalf("%d %s %s", code, out, err)
			}
			if !json.Valid([]byte(out)) {
				t.Fatal("output is not a finding report")
			}
		})
	}
}
func TestJSONProbeDoesNotEchoRawDiagnostics(t *testing.T) {
	input := strings.Replace(probeFixture("fail", 0), `"status":"fail"`, `"status":"fail","diagnostic":{"message":"https://user:supersecret@example.invalid"}`, 1)
	code, out, err := execute("jsonprobe-check", []string{"--require", "api", "--environment", "demo"}, input)
	if code != 20 || strings.Contains(out+err, "supersecret") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestJSONProbeRejectsDuplicateChecks(t *testing.T) {
	input := probeFixture("pass", 0)
	var data map[string]any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		t.Fatal(err)
	}
	checks := data["checks"].([]any)
	data["checks"] = append(checks, checks[0])
	encoded, _ := json.Marshal(data)
	code, out, _ := execute("jsonprobe-check", []string{"--require", "api", "--environment", "demo"}, string(encoded))
	if code != 30 || !strings.Contains(out, "duplicate") {
		t.Fatalf("%d %s", code, out)
	}
}
