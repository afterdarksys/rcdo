package toolkit

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func workflowZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestWorkflowCollectionRoundTrip(t *testing.T) {
	f := newWorkflowFixture(t)
	origin := workflowOrigin{Provider: "local", Project: "practice", RunID: "1", Attempt: 1}
	f.manifest.Origin = &origin
	f.save(t)
	files := map[string][]byte{}
	for _, name := range []string{"workflow.json", "outputs.json", "producer.json", "inventory.yml", "playbook.yml"} {
		data, e := os.ReadFile(filepath.Join(f.dir, name))
		if e != nil {
			t.Fatal(e)
		}
		files[name] = data
	}
	bundle := f.artifact(t, "bundle.zip", workflowZip(t, files))
	p := workflowProfile{SchemaVersion: "1", Identity: f.manifest.Identity, Origin: origin, Bundle: &bundle, Stage: "inputs"}
	f.jsonArtifact(t, "profile.json", p)
	outdir := filepath.Join(f.dir, "collected")
	args := []string{"--profile", filepath.Join(f.dir, "profile.json"), "--output-dir", outdir, "--format", "json"}
	c, o, e := execute("workflow-collect", args, "")
	if c != 0 || strings.Contains(o, "PRIVATE-DB-ENDPOINT") {
		t.Fatalf("%d %s %s", c, o, e)
	}
	r, e2 := decodeSessionReport([]byte(o))
	if e2 != nil || r.Provenance == nil || len(r.Provenance.Artifacts) < 5 {
		t.Fatalf("missing bindings %s %v", o, e2)
	}
	if c, _, _ := execute("workflow-collect", args, ""); c != 2 {
		t.Fatal("overwrote existing bundle", c)
	}
	if err := os.WriteFile(filepath.Join(outdir, "inventory.yml"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if checkProvenanceArtifacts(r.Provenance) == nil {
		t.Fatal("changed collected source still valid")
	}
	p.Identity.Workspace = "production"
	f.jsonArtifact(t, "wrong-profile.json", p)
	if c, _, _ := execute("workflow-collect", []string{"--profile", filepath.Join(f.dir, "wrong-profile.json"), "--output-dir", filepath.Join(f.dir, "wrong")}, ""); c != 30 {
		t.Fatal("accepted wrong identity", c)
	}
	if _, e := os.Stat(filepath.Join(f.dir, "wrong")); !os.IsNotExist(e) {
		t.Fatal("created output before validating identity")
	}
}

func TestWorkflowArchiveRejectsTraversalAndDuplicates(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../escape", "a\\b"} {
		if _, e := readWorkflowArchive(workflowZip(t, map[string][]byte{"workflow.json": []byte(`{}`), name: []byte("x")})); e == nil {
			t.Fatal("accepted", name)
		}
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 2; i++ {
		w, _ := z.Create("workflow.json")
		w.Write([]byte(`{}`))
	}
	z.Close()
	if _, e := readWorkflowArchive(b.Bytes()); e == nil {
		t.Fatal("accepted duplicate")
	}
}

func TestGithubWorkflowCollectorChecksRunAfterDownload(t *testing.T) {
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	p := workflowProfile{Identity: newWorkflowFixture(t).manifest.Identity, Origin: workflowOrigin{Provider: "github", Project: "owner/repo", RunID: "12", Attempt: 2}, ArtifactID: "34"}
	data := []byte("archive")
	for _, changed := range []bool{false, true} {
		runs := 0
		executeReadOnly = func(name string, args ...string) commandResult {
			if name != "gh" {
				t.Fatal(name)
			}
			route := args[1]
			if strings.HasSuffix(route, "/runs/12") {
				runs++
				commit := p.Identity.Commit
				if changed && runs == 2 {
					commit = strings.Repeat("b", 40)
				}
				return commandResult{stdout: []byte(`{"id":12,"head_sha":"` + commit + `","status":"completed","conclusion":"success","run_attempt":2}`)}
			}
			if strings.HasSuffix(route, "/zip") {
				return commandResult{stdout: data}
			}
			return commandResult{stdout: []byte(`{"id":34,"expired":false,"size_in_bytes":7,"digest":"sha256:` + digestBytes(data) + `","workflow_run":{"id":12,"head_sha":"` + p.Identity.Commit + `"}}`)}
		}
		_, e := collectGithubBundle(p)
		if (e != nil) != changed || runs != 2 {
			t.Fatalf("changed %v: %v, runs %d", changed, e, runs)
		}
	}
}

func TestWorkflowCustomAdapterIdentity(t *testing.T) {
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	p := workflowProfile{Identity: newWorkflowFixture(t).manifest.Identity, Origin: workflowOrigin{Provider: "custom", Project: "staging", RunID: "r1", Attempt: 1}, Adapter: "/opt/kit/export"}
	data := []byte("zip")
	envelope := workflowExport{SchemaVersion: "1", Identity: p.Identity, Origin: p.Origin, Archive: base64.StdEncoding.EncodeToString(data), SHA256: digestBytes(data)}
	executeReadOnly = func(name string, args ...string) commandResult {
		if name != p.Adapter || strings.Join(args, " ") != "rcdo-export --project staging --run r1" {
			t.Fatal(name, args)
		}
		b, _ := json.Marshal(envelope)
		return commandResult{stdout: b}
	}
	if _, e := collectAdapterBundle(p); e != nil {
		t.Fatal(e)
	}
	envelope.Origin.RunID = "another"
	if _, e := collectAdapterBundle(p); e == nil {
		t.Fatal("accepted another run")
	}
}

func TestWorkflowTraceRolesTemplatesAndAliases(t *testing.T) {
	dir := t.TempDir()
	for path, data := range map[string]string{
		"play.yml":                    "- hosts: web\n  roles: [app]\n",
		"roles/app/tasks/main.yml":    "- name: Render config\n  ansible.builtin.template:\n    src: app.j2\n    dest: /etc/app\n",
		"roles/app/defaults/main.yml": "endpoint: '{{db_host}}'\n",
		"roles/app/templates/app.j2":  "database={{ endpoint }}\n",
	} {
		p := filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(p), 0700)
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	graphPath := filepath.Join(dir, "graph.json")
	c, o, e := execute("workflow-trace", []string{"--root", dir, "--input", filepath.Join(dir, "play.yml"), "--variable", "db_host", "--graph-out", graphPath}, "")
	if c != 10 || !strings.Contains(o, "endpoint") {
		t.Fatalf("%d %s %s", c, o, e)
	}
	var g workflowSourceGraph
	b, _ := os.ReadFile(graphPath)
	if json.Unmarshal(b, &g) != nil || len(g.Files) != 4 {
		t.Fatal(string(b))
	}
	found := false
	for _, u := range g.Uses {
		if u.Variable == "db_host" && u.Kind == "template" && len(u.Via) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("missing template alias chain")
	}
	os.WriteFile(filepath.Join(dir, "roles/app/templates/app.j2"), []byte("{{ endpoint | default('private') }}"), 0600)
	c, o, e = execute("workflow-trace", []string{"--root", dir, "--input", filepath.Join(dir, "play.yml")}, "")
	if c != 30 || strings.Contains(o, "private") {
		t.Fatalf("unresolved expression %d %s %s", c, o, e)
	}
}

func TestProvenanceDoesNotPromotePolicyToEvidence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	os.WriteFile(path, []byte(`{}`), 0600)
	o := commonOptions{changeID: "C1", commit: strings.Repeat("a", 40), environment: "staging"}
	if e := prepareReportProvenance("some-check", []string{"--policy", path}, &o); e != nil {
		t.Fatal(e)
	}
	if o.provenance != nil {
		t.Fatal("policy became primary evidence")
	}
	if e := prepareReportProvenance("some-check", []string{"--input", path}, &o); e != nil {
		t.Fatal(e)
	}
	if o.provenance == nil || o.provenance.SourceSHA256 != digestBytes([]byte(`{}`)) {
		t.Fatal("snapshot not bound")
	}
	os.WriteFile(path, []byte(`{"changed":true}`), 0600)
	if checkProvenanceArtifacts(o.provenance) == nil {
		t.Fatal("accepted changed snapshot")
	}
}

func TestWorkflowPilotStartsPending(t *testing.T) {
	c, o, e := execute("pilot", []string{"start", "--suite", "workflow", "--state", filepath.Join(t.TempDir(), "pilot.json"), "--operator", "Test operator", "--setup", "Test fixture only"}, "")
	if c != 30 || !strings.Contains(o, "trace-output-consumer") || e != "" {
		t.Fatalf("%d %s %s", c, o, e)
	}
}

func TestSpaceWorkflowOriginRejectsApprovalAndWrongCommit(t *testing.T) {
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	p := workflowProfile{Identity: newWorkflowFixture(t).manifest.Identity, Origin: workflowOrigin{Provider: "spacelift", Project: "stack", RunID: "run", Attempt: 1}, Endpoint: "https://test.app.spacelift.io"}
	for _, valid := range []bool{false, true} {
		approval := "true"
		if valid {
			approval = "false"
		}
		executeReadOnly = func(name string, args ...string) commandResult {
			if name != "spacectl" {
				t.Fatal(name)
			}
			if args[0] == "whoami" {
				return commandResult{stdout: []byte(`{"id":"user","endpoint":"https://test.app.spacelift.io"}`)}
			}
			return commandResult{stdout: []byte(`{"data":{"stack":{"id":"stack","run":{"id":"run","state":"FINISHED","needsApproval":` + approval + `,"isMostRecent":true,"commit":{"hash":"` + p.Identity.Commit + `"}}}}}`)}
		}
		if e := inspectSpaceOrigin(p); (e == nil) != valid {
			t.Fatalf("valid %v: %v", valid, e)
		}
	}
}

func TestCommonSnapshotProvenanceBindsExpectations(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "context.json")
	expect := filepath.Join(dir, "expect.json")
	data, _ := json.Marshal(map[string]any{"schema_version": "1", "collected_at": time.Now().UTC().Format(time.RFC3339Nano), "source": "fixture", "values": map[string]string{"workspace": "staging"}})
	os.WriteFile(input, data, 0600)
	os.WriteFile(expect, []byte(`{"workspace":"staging"}`), 0600)
	c, o, e := execute("iac-context", []string{"--input", input, "--expect", expect, "--environment", "staging", "--change-id", "C1", "--commit", strings.Repeat("a", 40), "--format", "json"}, "")
	if c != 0 {
		t.Fatalf("%d %s %s", c, o, e)
	}
	r, err := decodeSessionReport([]byte(o))
	if err != nil || r.Provenance == nil || len(r.Provenance.Artifacts) != 2 {
		t.Fatalf("%v %s", err, o)
	}
	os.WriteFile(expect, []byte(`{"workspace":"production"}`), 0600)
	if checkProvenanceArtifacts(r.Provenance) == nil {
		t.Fatal("expectation changes did not invalidate report")
	}
}
