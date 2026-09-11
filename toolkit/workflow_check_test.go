package toolkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type workflowFixture struct {
	dir       string
	manifest  workflowManifest
	producer  workflowProducer
	outputs   string
	inventory string
	playbook  string
}

func newWorkflowFixture(t *testing.T) *workflowFixture {
	t.Helper()
	id := workflowIdentity{ChangeID: "C1", Commit: strings.Repeat("a", 40), Environment: "staging", Engine: "terraform", Backend: "s3:states/web", Workspace: "staging", Account: "123456789012", Region: "us-east-1"}
	serial := uint64(3)
	return &workflowFixture{dir: t.TempDir(), manifest: workflowManifest{SchemaVersion: "1", Identity: id, Hosts: []string{"blue"}, Mappings: []workflowMapping{{ID: "blue-address", Output: "hosts", Pointer: "/blue/private_ip", Host: "blue", Variable: "ansible_host", Type: "string"}, {ID: "database", Output: "db", Host: "blue", Variable: "db_host", Type: "string"}}},
		producer:  workflowProducer{SchemaVersion: "1", Identity: id, Outcome: "succeeded", AppliedAt: time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano), CollectedAt: time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano), StateLineage: "lineage-1", StateSerial: &serial},
		outputs:   `{"hosts":{"sensitive":false,"type":["map",["object",{"private_ip":"string"}]],"value":{"blue":{"private_ip":"10.0.0.4"}}},"db":{"sensitive":true,"type":"string","value":"PRIVATE-DB-ENDPOINT"}}`,
		inventory: "all:\n  children:\n    web:\n      vars:\n        db_host: PRIVATE-DB-ENDPOINT\n      hosts:\n        blue:\n          ansible_host: 10.0.0.4\n",
		playbook:  "- hosts: web\n  tasks:\n    - name: Configure app\n      ansible.builtin.template:\n        src: app.j2\n        dest: /etc/app.conf\n"}
}
func (f *workflowFixture) artifact(t *testing.T, name string, data []byte) boundArtifact {
	t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return boundArtifact{Path: name, SHA256: digestBytes(data)}
}
func (f *workflowFixture) jsonArtifact(t *testing.T, name string, value any) boundArtifact {
	t.Helper()
	b, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return f.artifact(t, name, b)
}
func (f *workflowFixture) save(t *testing.T) string {
	t.Helper()
	f.manifest.Outputs = f.artifact(t, "outputs.json", []byte(f.outputs))
	f.producer.OutputsSHA256 = f.manifest.Outputs.SHA256
	f.manifest.Producer = f.jsonArtifact(t, "producer.json", f.producer)
	f.manifest.Inventory = f.artifact(t, "inventory.yml", []byte(f.inventory))
	f.manifest.Playbook = f.artifact(t, "playbook.yml", []byte(f.playbook))
	f.jsonArtifact(t, "workflow.json", f.manifest)
	return filepath.Join(f.dir, "workflow.json")
}
func TestWorkflowMapsOutputsAndRedactsValues(t *testing.T) {
	f := newWorkflowFixture(t)
	path := f.save(t)
	for _, format := range []string{"text", "json", "github", "sarif"} {
		code, out, err := execute("workflow-check", []string{"--manifest", path, "--format", format, "--width", "40"}, "")
		if code != 0 || err != "" || strings.Contains(out, "PRIVATE-DB-ENDPOINT") || strings.Contains(out, "10.0.0.4") {
			t.Fatalf("%s %d %s %s", format, code, out, err)
		}
		if format == "text" {
			for _, line := range strings.Split(out, "\n") {
				if len([]rune(line)) > 40 {
					t.Fatal("width violation", line)
				}
			}
		}
		if format == "json" {
			r, e := decodeSessionReport([]byte(out))
			if e != nil || r.Provenance == nil {
				t.Fatalf("report contract: %v", e)
			}
		}
	}
}
func TestWorkflowRejectsBrokenHandoffs(t *testing.T) {
	cases := []struct {
		name   string
		change func(*workflowFixture)
		want   int
	}{
		{"wrong endpoint", func(f *workflowFixture) { f.inventory = strings.ReplaceAll(f.inventory, "10.0.0.4", "10.0.0.9") }, 20},
		{"missing output", func(f *workflowFixture) { f.manifest.Mappings[1].Output = "missing" }, 30},
		{"null", func(f *workflowFixture) {
			f.outputs = strings.ReplaceAll(f.outputs, `"value":"PRIVATE-DB-ENDPOINT"`, `"value":null`)
		}, 30},
		{"missing sensitivity", func(f *workflowFixture) { f.outputs = strings.ReplaceAll(f.outputs, `"sensitive":true,`, "") }, 30},
		{"incorrect declared type", func(f *workflowFixture) {
			f.outputs = strings.ReplaceAll(f.outputs, `"type":"string"`, `"type":"number"`)
		}, 30},
		{"type coercion", func(f *workflowFixture) { f.manifest.Mappings[1].Type = "number" }, 20},
		{"unknown pointer", func(f *workflowFixture) { f.manifest.Mappings[0].Pointer = "/missing" }, 30},
		{"duplicate mapping", func(f *workflowFixture) { f.manifest.Mappings = append(f.manifest.Mappings, f.manifest.Mappings[0]) }, 2},
		{"missing host", func(f *workflowFixture) { f.inventory = strings.ReplaceAll(f.inventory, "blue:", "green:") }, 30},
		{"missing address mapping", func(f *workflowFixture) { f.manifest.Mappings = f.manifest.Mappings[1:] }, 30},
		{"stale outputs", func(f *workflowFixture) {
			f.producer.CollectedAt = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
		}, 30},
		{"wrong workspace", func(f *workflowFixture) { f.producer.Identity.Workspace = "production" }, 30},
		{"failed apply", func(f *workflowFixture) { f.producer.Outcome = "failed" }, 30},
		{"missing state serial", func(f *workflowFixture) { f.producer.StateSerial = nil }, 30},
		{"runtime override", func(f *workflowFixture) { f.playbook += "  vars:\n    db_host: other\n" }, 30},
		{"dynamic hosts", func(f *workflowFixture) {
			f.playbook = strings.ReplaceAll(f.playbook, "hosts: web", "hosts: '{{ hosts }}'")
		}, 30},
		{"jinja inventory", func(f *workflowFixture) { f.inventory = strings.ReplaceAll(f.inventory, "10.0.0.4", "'{{ address }}'") }, 30},
		{"external role", func(f *workflowFixture) { f.playbook += "  roles: [external]\n" }, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			tc.change(f)
			path := f.save(t)
			c, o, e := execute("workflow-check", []string{"--manifest", path}, "")
			if c != tc.want || strings.Contains(o, "PRIVATE-DB-ENDPOINT") {
				t.Fatalf("got %d want %d %s %s", c, tc.want, o, e)
			}
		})
	}
}
func TestWorkflowExtraVarsPrecedenceAndSourceChanges(t *testing.T) {
	f := newWorkflowFixture(t)
	extra := f.artifact(t, "extra.json", []byte(`{"db_host":"wrong"}`))
	f.manifest.ExtraVars = &extra
	path := f.save(t)
	code, out, _ := execute("workflow-check", []string{"--manifest", path}, "")
	if code != 20 || !strings.Contains(out, "extra-vars") {
		t.Fatalf("%d %s", code, out)
	}
	os.WriteFile(filepath.Join(f.dir, "inventory.yml"), []byte("all: {}\n"), 0600)
	code, out, _ = execute("workflow-check", []string{"--manifest", path}, "")
	if code != 30 {
		t.Fatalf("%d %s", code, out)
	}
}
func (f *workflowFixture) execution(t *testing.T) workflowExecution {
	f.save(t)
	start := time.Now().Add(-time.Minute).UTC()
	finish := time.Now().Add(-30 * time.Second).UTC()
	check := false
	receipt := workflowExecution{SchemaVersion: "1", Identity: f.manifest.Identity, RunID: "run-1", StartedAt: start.Format(time.RFC3339Nano), FinishedAt: finish.Format(time.RFC3339Nano), Outcome: "succeeded", CheckMode: &check, OutputsSHA256: f.manifest.Outputs.SHA256, InventorySHA256: f.manifest.Inventory.SHA256, PlaybookSHA256: f.manifest.Playbook.SHA256, Hosts: []string{"blue"}}
	complete := true
	receipt.VariableSourcesComplete = &complete
	inputs := f.artifact(t, "resolved.json", []byte(`{"_meta":{"hostvars":{"blue":{"ansible_host":"10.0.0.4","db_host":"PRIVATE-DB-ENDPOINT"}}},"all":{"children":["web"]},"web":{"hosts":["blue"]}}`))
	receipt.ResolvedInputs = &inputs
	a := f.jsonArtifact(t, "execution.json", receipt)
	f.manifest.Execution = &a
	records := []ansibleEvent{{SchemaVersion: "1", Event: "start", RunID: "run-1", Sequence: 0, At: start.Format(time.RFC3339Nano), CheckMode: &check}, {SchemaVersion: "1", Event: "result", RunID: "run-1", Sequence: 1, At: start.Add(time.Second).Format(time.RFC3339Nano), CheckMode: &check, Host: "blue", TaskID: "task-1", Task: "Configure", Outcome: "ok"}, {SchemaVersion: "1", Event: "finish", RunID: "run-1", Sequence: 2, At: finish.Format(time.RFC3339Nano)}}
	raw := []byte{}
	for _, r := range records {
		b, _ := json.Marshal(r)
		raw = append(raw, b...)
		raw = append(raw, '\n')
	}
	eventArtifact := f.artifact(t, "events.jsonl", raw)
	f.manifest.Events = &eventArtifact
	return receipt
}
func TestWorkflowExecutionRequiresExactEvidence(t *testing.T) {
	f := newWorkflowFixture(t)
	path := f.save(t)
	code, _, _ := execute("workflow-check", []string{"--manifest", path, "--stage", "execution"}, "")
	if code != 30 {
		t.Fatal(code)
	}
	receipt := f.execution(t)
	f.jsonArtifact(t, "workflow.json", f.manifest)
	code, out, err := execute("workflow-check", []string{"--manifest", path, "--stage", "execution"}, "")
	if code != 10 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	// The existing finding contract returns REVIEW for informational task results.
	receipt.InventorySHA256 = strings.Repeat("b", 64)
	a := f.jsonArtifact(t, "execution.json", receipt)
	f.manifest.Execution = &a
	f.jsonArtifact(t, "workflow.json", f.manifest)
	code, out, _ = execute("workflow-check", []string{"--manifest", path, "--stage", "execution"}, "")
	if code != 30 {
		t.Fatalf("%d %s", code, out)
	}
}
func TestWorkflowVerifiedStageRequiresPostRunHealth(t *testing.T) {
	f := newWorkflowFixture(t)
	receipt := f.execution(t)
	f.manifest.RequiredChecks = []workflowHealthRequirement{{Host: "blue", Name: "http-health"}}
	path := filepath.Join(f.dir, "workflow.json")
	f.jsonArtifact(t, "workflow.json", f.manifest)
	code, _, _ := execute("workflow-check", []string{"--manifest", path, "--stage", "verified"}, "")
	if code != 30 {
		t.Fatal(code)
	}
	v := map[string]any{"schema_version": "1", "identity": f.manifest.Identity, "run_id": receipt.RunID, "execution_sha256": f.manifest.Execution.SHA256, "collected_at": time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), "hosts": []string{"blue"}, "checks": []map[string]string{{"name": "http-health", "host": "blue", "outcome": "pass"}}}
	a := f.jsonArtifact(t, "health.json", v)
	f.manifest.Verification = &a
	f.jsonArtifact(t, "workflow.json", f.manifest)
	code, out, err := execute("workflow-check", []string{"--manifest", path, "--stage", "verified"}, "")
	if code != 10 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	v["collected_at"] = receipt.StartedAt
	a = f.jsonArtifact(t, "health.json", v)
	f.manifest.Verification = &a
	f.jsonArtifact(t, "workflow.json", f.manifest)
	code, out, _ = execute("workflow-check", []string{"--manifest", path, "--stage", "verified"}, "")
	if code != 30 {
		t.Fatalf("%d %s", code, out)
	}
}

func TestWorkflowPointerEscapingAndExactNumbers(t *testing.T) {
	var value any
	if workflowJSON([]byte(`{"a/b":{"~key":[9007199254740993]}}`), &value) != nil {
		t.Fatal("decode")
	}
	got, ok := workflowPointer(value, "/a~1b/~0key/0")
	if !ok || got != json.Number("9007199254740993") {
		t.Fatal(got, ok)
	}
	for _, pointer := range []string{"/a~2b", "/a~1b/~0key/00", "/a~1b/~0key/-1"} {
		if _, ok := workflowPointer(value, pointer); ok {
			t.Fatal(pointer)
		}
	}
	if !workflowEqual(json.Number("1.0"), json.Number("1")) || workflowEqual(json.Number("9007199254740993"), json.Number("9007199254740992")) {
		t.Fatal("numeric precision")
	}
}

func TestWorkflowResolvedInventory(t *testing.T) {
	f := newWorkflowFixture(t)
	f.manifest.InventoryFormat = "resolved"
	f.inventory = `{"_meta":{"profile":"inventory_legacy","hostvars":{"blue":{"ansible_host":"10.0.0.4","db_host":"PRIVATE-DB-ENDPOINT"}}},"all":{"children":["ungrouped","web"]},"web":{"hosts":["blue"]}}`
	path := f.save(t)
	code, out, err := execute("workflow-check", []string{"--manifest", path}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	f.inventory = `{"_meta":{"hostvars":{}},"all":{"children":["web"]},"web":{"children":["all"]}}`
	f.save(t)
	code, out, _ = execute("workflow-check", []string{"--manifest", path}, "")
	if code != 30 {
		t.Fatalf("%d %s", code, out)
	}
}

func TestWorkflowDetectsInvocationOverrides(t *testing.T) {
	f := newWorkflowFixture(t)
	receipt := f.execution(t)
	inputs := f.artifact(t, "resolved.json", []byte(`{"_meta":{"hostvars":{"blue":{"ansible_host":"10.0.0.9","db_host":"PRIVATE-DB-ENDPOINT"}}},"all":{"hosts":["blue"]}}`))
	receipt.ResolvedInputs = &inputs
	a := f.jsonArtifact(t, "execution.json", receipt)
	f.manifest.Execution = &a
	f.jsonArtifact(t, "workflow.json", f.manifest)
	code, out, err := execute("workflow-check", []string{"--manifest", filepath.Join(f.dir, "workflow.json"), "--stage", "execution"}, "")
	if code != 20 || !strings.Contains(out, "consumed different") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}

func TestWorkflowDependenciesInvalidateSavedAndCombinedReview(t *testing.T) {
	f := newWorkflowFixture(t)
	path := f.save(t)
	code, out, err := execute("workflow-check", []string{"--manifest", path, "--format", "json"}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	f.artifact(t, "report.json", []byte(out))
	manifest := reviewChangeManifest{SchemaVersion: "1", ChangeID: f.manifest.Identity.ChangeID, Commit: f.manifest.Identity.Commit, Environment: f.manifest.Identity.Environment, RequiredComponents: []string{"workflow"}, Reports: map[string]string{"workflow": "report.json"}, Sources: map[string]string{"workflow": "workflow.json"}}
	f.jsonArtifact(t, "change.json", manifest)
	code, out, err = execute("review-change", []string{"--manifest", filepath.Join(f.dir, "change.json"), "--format", "json"}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	f.artifact(t, "combined.json", []byte(out))
	sessionPath := filepath.Join(f.dir, "session.json")
	code, out, err = execute("review-session", []string{"start", "--report", filepath.Join(f.dir, "combined.json"), "--session", sessionPath, "--change-id", f.manifest.Identity.ChangeID, "--commit", f.manifest.Identity.Commit}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, err)
	}
	f.artifact(t, "inventory.yml", []byte("all: {}\n"))
	code, out, err = execute("review-session", []string{"status", "--session", sessionPath}, "")
	if code != 30 {
		t.Fatalf("stale session: %d %s %s", code, out, err)
	}
	code, out, err = execute("review-change", []string{"--manifest", filepath.Join(f.dir, "change.json")}, "")
	if code != 30 {
		t.Fatalf("stale aggregate: %d %s %s", code, out, err)
	}
	code, out, err = execute("report-read", []string{"--input", filepath.Join(f.dir, "report.json")}, "")
	if code != 30 {
		t.Fatalf("stale reader: %d %s %s", code, out, err)
	}
}
