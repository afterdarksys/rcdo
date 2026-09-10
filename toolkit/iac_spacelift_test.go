package toolkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func boolPtr(v bool) *bool { return &v }
func cleanSpaceFixture() spaceSnapshot {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	policies := []spacePolicy{}
	deps := []spaceDependency{}
	downstream := []string{}
	return spaceSnapshot{SchemaVersion: "1", Account: "example", StackID: "prod", RunID: "r-1", CommitSHA: strings.Repeat("a", 40), State: "FINISHED", RunType: "TRACKED", CollectedAt: now, Source: "fixture", LatestRunID: "r-1", Policies: &policies, Approval: &spaceApproval{Satisfied: boolPtr(true)}, Dependencies: &deps, Downstream: &downstream, Drift: &spaceDrift{Enabled: boolPtr(true), LastSuccess: now, Detected: boolPtr(false), Reconcile: boolPtr(false)}}
}
func jsonFixture(v any) string { b, _ := json.Marshal(v); return string(b) }
func writeFixture(t *testing.T, dir, name, data string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSpaceCoverageAndIdentity(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*spaceSnapshot)
		args     []string
		want     int
		contains string
	}{
		{"clean", func(s *spaceSnapshot) {}, nil, 0, "CLEAN"},
		{"pending", func(s *spaceSnapshot) { s.State = "PLANNING" }, nil, 10, "outstanding work"},
		{"unknown", func(s *spaceSnapshot) { s.State = "NEW_UPSTREAM_STATE" }, nil, 30, "Unknown"},
		{"policies missing", func(s *spaceSnapshot) { s.Policies = nil }, nil, 30, "Policy evidence"},
		{"denied", func(s *spaceSnapshot) {
			p := []spacePolicy{{ID: "security", Type: "PLAN", Decision: "deny"}}
			s.Policies = &p
		}, nil, 20, "security"},
		{"approval missing", func(s *spaceSnapshot) { s.Approval = nil }, nil, 30, "Approval"},
		{"approval outstanding", func(s *spaceSnapshot) { s.Approval.Outstanding = []string{"platform owner"} }, nil, 20, "platform owner"},
		{"superseded", func(s *spaceSnapshot) { s.LatestRunID = "r-2" }, nil, 20, "supersedes"},
		{"stale", func(s *spaceSnapshot) { s.CollectedAt = time.Now().Add(-time.Hour).Format(time.RFC3339) }, nil, 30, "stale"},
		{"prefix rejected", func(s *spaceSnapshot) {}, []string{"--expect-commit", strings.Repeat("b", 40)}, 20, "Unexpected run commit"},
		{"account mismatch", func(s *spaceSnapshot) {}, []string{"--expect-account", "other"}, 20, "Unexpected run account"},
		{"missing run", func(s *spaceSnapshot) { s.RunID = "" }, nil, 30, "Missing run run"},
		{"preview", func(s *spaceSnapshot) { s.RunType = "PROPOSED" }, nil, 0, "not applied"},
		{"nested lookalike", func(s *spaceSnapshot) { s.State = ""; s.Config = map[string]any{"state": "FINISHED"} }, nil, 30, "Missing run state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cleanSpaceFixture()
			tc.mutate(&s)
			code, out, err := execute("spacelift-check", tc.args, jsonFixture(s))
			if code != tc.want || !strings.Contains(out, tc.contains) || err != "" {
				t.Fatalf("code=%d out=%s err=%s", code, out, err)
			}
		})
	}
}
func TestSpaceCollectorUsesRunQuery(t *testing.T) {
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	executeReadOnly = func(name string, args ...string) commandResult {
		joined := strings.Join(args, " ")
		if name != "spacectl" || !strings.Contains(joined, "api --raw --variables") || !strings.Contains(joined, "run(id: $run)") {
			t.Fatalf("wrong collector: %s %v", name, args)
		}
		return commandResult{stdout: []byte(`{"data":{"stack":{"id":"prod","run":{"id":"r-1","state":"FINISHED","type":"TRACKED","commit":{"hash":"abc"}}}}}`)}
	}
	code, out, _ := execute("spacelift-check", []string{"--stack", "prod", "--run", "r-1"}, "")
	if code != 30 || !strings.Contains(out, "Policy evidence unavailable") {
		t.Fatalf("%d %s", code, out)
	}
	executeReadOnly = func(string, ...string) commandResult {
		return commandResult{stdout: []byte(`{"errors":[{"message":"secret-value"}],"data":{"stack":{"id":"prod"}}}`)}
	}
	code, out, _ = execute("spacelift-check", []string{"--stack", "prod", "--run", "r-1"}, "")
	if code != 30 || strings.Contains(out, "secret-value") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestSpaceOperationsAndBinding(t *testing.T) {
	dir := t.TempDir()
	p := writeFixture(t, dir, "plan.json", `{"format_version":"1.0","resource_changes":[]}`)
	s := cleanSpaceFixture()
	data, _ := os.ReadFile(p)
	s.Plan = &spacePlanBinding{JSONSHA256: digestBytes(data), RunID: s.RunID, CommitSHA: s.CommitSHA, Source: "run artifact download"}
	code, out, err := execute("spacelift-check", []string{"--plan-json", p}, jsonFixture(s))
	if code != 0 || !strings.Contains(out, "acquisition binding") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	s.Plan.JSONSHA256 = strings.Repeat("0", 64)
	code, out, _ = execute("spacelift-check", []string{"--plan-json", p}, jsonFixture(s))
	if code != 20 || !strings.Contains(out, "does not match") {
		t.Fatalf("%d %s", code, out)
	}
	s = cleanSpaceFixture()
	deps := []spaceDependency{{StackID: "network", RunID: "n-1", State: "FAILED", CollectedAt: s.CollectedAt, DependsOn: []string{"network"}}}
	s.Dependencies = &deps
	s.Drift.Detected = boolPtr(true)
	expected := writeFixture(t, dir, "expected.json", `{"branch":"main","autodeploy":false}`)
	s.Config = map[string]any{"branch": "dev", "autodeploy": true}
	code, out, _ = execute("spacelift-check", []string{"--expect-config", expected}, jsonFixture(s))
	for _, want := range []string{"cycle", "Upstream", "drift detected", "configuration differs"} {
		if code != 20 || !strings.Contains(out, want) {
			t.Fatalf("%d missing %s in %s", code, want, out)
		}
	}
}
func TestPlanUnknownSensitiveAndReplacement(t *testing.T) {
	input := `{"format_version":"1.0","terraform_version":"1.10.0","resource_changes":[{"address":"aws_db_instance.main","type":"aws_db_instance","action_reason":"replace_because_cannot_update","change":{"actions":["create","delete"],"replace_paths":[["engine_version"]],"before":{"value":"old-secret","nullable":null,"unknown":"old"},"after":{"value":"new-secret","nullable":"known"},"after_sensitive":{"value":true},"after_unknown":{"unknown":true,"publicly_accessible":true}}}]}`
	for _, command := range []string{"tofu-check", "plan-explain"} {
		code, out, err := execute(command, []string{"--format", "json"}, input)
		if code != 30 || err != "" {
			t.Fatalf("%d %s %s", code, out, err)
		}
		for _, secret := range []string{"old-secret", "new-secret"} {
			if strings.Contains(out, secret) {
				t.Fatal("leaked", secret)
			}
		}
		for _, want := range []string{"engine_version", "create replacement, then destroy", "unknown"} {
			if !strings.Contains(out, want) {
				t.Fatal("missing", want, out)
			}
		}
	}
}
func TestPlanStrictnessAndImpact(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{`{"format_version":"1.0","resource_changes":[],"complete":false}`, 30},
		{`{"format_version":"1.0","resource_changes":[],"deferred_changes":[{}]}`, 30},
		{`{"format_version":"1.0","resource_changes":[{"address":"x.y","change":{"actions":["new-action"]}}]}`, 30},
		{`{"format_version":"1.0","resource_changes":[{"address":"x.y","change":{"actions":["update"]}},{"address":"x.y","change":{"actions":["update"]}}]}`, 30},
		{`{"format_version":"1.0","resource_changes":[],"checks":[{"status":"unknown"}]}`, 30},
		{`{"format_version":"1.0","resource_changes":[],"resource_changes":[]}`, 2},
		{`{"format_version":"1.0","resource_changes":[]} {}`, 2},
		{`{"format_version":"2.0","resource_changes":[]}`, 30},
	}
	for _, tc := range cases {
		code, out, err := execute("tofu-check", nil, tc.input)
		if code != tc.want {
			t.Fatalf("%d want %d %s %s", code, tc.want, out, err)
		}
	}
	limits := writeFixture(t, t.TempDir(), "limits.json", `{"max_deletes":0,"critical_resources":["aws_instance.main"]}`)
	code, out, _ := execute("tofu-check", []string{"--limits", limits}, `{"format_version":"1.0","resource_changes":[{"address":"aws_instance.main","type":"aws_instance","change":{"actions":["delete"]}}]}`)
	if code != 20 || !strings.Contains(out, "Deletion limit exceeded") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestPlanDiffRedactsAcrossBothVersions(t *testing.T) {
	dir := t.TempDir()
	base := `{"format_version":"1.0","resource_changes":[{"address":"x.y","change":{"actions":["update"],"before":{},"after":{"value":"%s"},"after_sensitive":{"value":%t}}}]}`
	b := writeFixture(t, dir, "before.json", fmt.Sprintf(base, "old-secret", true))
	a := writeFixture(t, dir, "after.json", fmt.Sprintf(base, "new-secret", false))
	code, out, err := execute("plan-diff", []string{"--before", b, "--after", a}, "")
	if code != 10 || strings.Contains(out, "old-secret") || strings.Contains(out, "new-secret") || !strings.Contains(out, "REDACTED") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestIACNativeAndEngine(t *testing.T) {
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	executeReadOnly = func(name string, args ...string) commandResult {
		if name != "terraform" {
			t.Fatalf("wrong engine %s", name)
		}
		if args[0] == "show" {
			return commandResult{stdout: []byte(`{"format_version":"1.0","resource_changes":[]}`)}
		}
		if !strings.Contains(strings.Join(args, " "), "validate -json") {
			t.Fatal(args)
		}
		return commandResult{stdout: []byte(`{"format_version":"1.0","valid":false,"error_count":1,"warning_count":0,"diagnostics":[{"severity":"error","summary":"unmarked-secret","range":{"filename":"main.tf","start":{"line":7}}}]}`), err: fmt.Errorf("exit 1")}
	}
	code, out, err := execute("tofu-check", []string{"--engine", "terraform", "--plan", "plan.bin"}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("iac-validate", []string{"--engine", "terraform", "--native"}, "")
	if code != 20 || strings.Contains(out, "unmarked-secret") || !strings.Contains(out, "main.tf:7") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, _ = execute("iac-validate", nil, `{"format_version":"1.0","valid":true,"error_count":1,"warning_count":0,"diagnostics":[]}`)
	if code != 30 {
		t.Fatalf("%d %s", code, out)
	}
}
func TestSecurityChanges(t *testing.T) {
	for _, tc := range []struct {
		before string
		want   int
		title  string
	}{{`"0.0.0.0/0"`, 10, "Existing public access"}, {`"10.0.0.0/8"`, 20, "Public access introduced"}} {
		input := fmt.Sprintf(`{"format_version":"1.0","resource_changes":[{"address":"aws_security_group.web","type":"aws_security_group","change":{"actions":["update"],"before":{"cidr":%s},"after":{"cidr":"0.0.0.0/0"}}}]}`, tc.before)
		code, out, _ := execute("tofu-check", nil, input)
		if code != tc.want || !strings.Contains(out, tc.title) {
			t.Fatalf("%d %s", code, out)
		}
	}
	policy := `{"Statement":[{"Effect":"Deny","Action":"s3:*","Resource":"*"}]}`
	input := map[string]any{"format_version": "1.0", "resource_changes": []any{map[string]any{"address": "aws_iam_policy.main", "type": "aws_iam_policy", "change": map[string]any{"actions": []string{"create"}, "after": map[string]any{"policy": policy}}}}}
	code, out, _ := execute("tofu-check", nil, jsonFixture(input))
	if code != 20 || !strings.Contains(out, "Policy clause added") || !strings.Contains(out, "Deny") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestCommandGenerationNeverExecutes(t *testing.T) {
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	executeReadOnly = func(string, ...string) commandResult {
		t.Fatal("generation executed a process")
		return commandResult{}
	}
	malicious := "stack'$(touch /tmp/should-never-exist)"
	code, out, err := execute("command-gen", []string{"--to", "spacelift", "--action", "logs", "--format", "json"}, jsonFixture(map[string]string{"stack_id": malicious, "run_id": "r-1"}))
	if code != 0 || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	var recipe commandRecipe
	if json.Unmarshal([]byte(out), &recipe) != nil || recipe.Executed || recipe.Argv[4] != malicious || !strings.Contains(recipe.Command, `'"'"'`) {
		t.Fatal(out)
	}
	code, out, _ = execute("command-gen", []string{"--to", "spacelift", "--action", "deploy"}, `{"stack_id":"prod","commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if code != 0 || !strings.Contains(out, "NOT RUN") {
		t.Fatalf("%d %s", code, out)
	}
	code, _, _ = execute("command-gen", []string{"--to", "spacelift", "--action", "logs"}, `{"stack_id":"prod"}`)
	if code != 2 {
		t.Fatal(code)
	}
	code, out, err = execute("command-gen", []string{"--to", "terraform", "--directory", ".", "--action", "plan"}, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
	if code != 0 || !strings.Contains(out, "review.tfplan") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("command-gen", []string{"--to", "aws", "--from", "hcl", "--region", "us-east-1"}, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
	if code != 0 || !strings.Contains(out, "create-vpc") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	if strings.HasPrefix(shellQuote("$RCDO_X$(whoami)"), `"`) {
		t.Fatal("unsafe variable interpolation")
	}
}
func TestConfigDependenciesAndContext(t *testing.T) {
	input := `terraform { required_version = ">= 1.6" }
resource "aws_subnet" "main" {
 vpc_id = aws_vpc.main.id
}
module "external" { source = "org/name/aws" }
`
	code, out, err := execute("iac-config-check", nil, input)
	if code != 10 || !strings.Contains(out, "Configuration-derived dependency") || !strings.Contains(out, "revision pin") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	expected := writeFixture(t, t.TempDir(), "context.json", `{"workspace":"prod","backend":"s3"}`)
	snapshot := map[string]any{"schema_version": "1", "collected_at": time.Now().UTC().Format(time.RFC3339), "source": "fixture", "values": map[string]string{"workspace": "dev", "backend": "s3"}}
	code, out, _ = execute("iac-context", []string{"--expect", expected}, jsonFixture(snapshot))
	if code != 20 || !strings.Contains(out, "context mismatch") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestSpaceRunUtilities(t *testing.T) {
	s := cleanSpaceFixture()
	input := jsonFixture(map[string]any{"schema_version": "1", "complete": true, "runs": []spaceSnapshot{s}})
	code, out, err := execute("spacelift-runs", []string{"--type", "TRACKED"}, input)
	if code != 10 || !strings.Contains(out, "Run observation") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	dir := t.TempDir()
	b := writeFixture(t, dir, "before.json", jsonFixture(s))
	s.State = "FAILED"
	a := writeFixture(t, dir, "after.json", jsonFixture(s))
	code, out, err = execute("spacelift-diff", []string{"--before", b, "--after", a}, "")
	if code != 20 || !strings.Contains(out, "state changed") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	calls := 0
	executeReadOnly = func(string, ...string) commandResult {
		calls++
		return commandResult{stdout: []byte(`{"data":{"stack":{"id":"prod","run":{"id":"r-1","state":"FINISHED","type":"TRACKED","commit":{"hash":"abc"}}}}}`)}
	}
	code, out, err = execute("spacelift-watch", []string{"--stack", "prod", "--run", "r-1", "--samples", "3", "--format", "json"}, "")
	if code != 30 || calls != 1 || !json.Valid([]byte(out)) {
		t.Fatalf("%d calls=%d %s %s", code, calls, out, err)
	}
}
func TestPlanFilterPreservesOtherBlockers(t *testing.T) {
	input := `{"format_version":"1.0","resource_changes":[{"address":"aws_vpc.safe","type":"aws_vpc","change":{"actions":["no-op"],"before":{},"after":{}}},{"address":"aws_db_instance.main","type":"aws_db_instance","change":{"actions":["delete"]}}]}`
	code, out, _ := execute("plan-explain", []string{"--resource", "aws_vpc.safe"}, input)
	if code != 20 || !strings.Contains(out, "aws_db_instance.main") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestUnknownNullAndSensitiveArray(t *testing.T) {
	c := iacChange{Before: map[string]any{"list": []any{"secret-before"}, "nullable": nil}, After: map[string]any{"list": []any{"secret-after"}, "nullable": "known"}, AfterSensitive: map[string]any{"list": []any{true}}, AfterUnknown: map[string]any{"endpoint": true}}
	out := strings.Join(changeLines(c), "\n")
	if strings.Contains(out, "secret-before") || strings.Contains(out, "secret-after") || !strings.Contains(out, "null ->") || !strings.Contains(out, "unknown until apply") {
		t.Fatal(out)
	}
}
func TestCollectorOutputIsBounded(t *testing.T) {
	b := limitedCommandBuffer{limit: 3}
	n, err := b.Write([]byte("abcdef"))
	if err != nil || n != 6 || b.String() != "abc" || !b.exceeded {
		t.Fatalf("%d %s %v", n, b.String(), err)
	}
	n, err = b.Write([]byte("more"))
	if n != 4 || err != nil || b.String() != "abc" {
		t.Fatalf("%d %s %v", n, b.String(), err)
	}
}

func TestMalformedMasksCannotExposeValues(t *testing.T) {
	for _, mask := range []string{`"true"`, `{"nested":true}`, `[true]`} {
		input := `{"format_version":"1.0","resource_changes":[{"address":"x.y","change":{"actions":["update"],"before":null,"after":"unmarked-secret","after_sensitive":` + mask + `}}]}`
		code, out, err := execute("plan-explain", nil, input)
		if code != 2 || strings.Contains(out+err, "unmarked-secret") {
			t.Fatalf("%d %s %s", code, out, err)
		}
	}
}
