package toolkit

import (
	"bytes"
	"encoding/json"
	"git-tools/finding"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvalidDeployEvidenceCannotSatisfyCoverage(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"schema_version":"1","status":"clean","findings":[],"completed_checks":[],"incomplete_checks":[]}`, `{"schema_version":"1","status":"blocked","findings":[],"completed_checks":["check"],"incomplete_checks":[]}`} {
		code, out, _ := execute("deploy-review", nil, raw)
		if code != 30 || !strings.Contains(out, "INCOMPLETE") {
			t.Fatalf("%s: %d %s", raw, code, out)
		}
	}
}

func TestStaticScanRetainsRiskAndReportsTruncation(t *testing.T) {
	r := scanRules([]byte("terraform destroy\n"+strings.Repeat("x", 4<<20)+"\n"), "script", "test", dangerRules)
	if r.Status() != finding.StatusIncomplete || len(r.Findings) == 0 {
		t.Fatalf("%+v", r)
	}
	var merged finding.Report
	appendScanReport(&merged, r)
	if merged.Status() != finding.StatusIncomplete || len(merged.Findings) == 0 {
		t.Fatal("repository scan dropped coverage")
	}
}

func TestAnsibleSyntaxDoesNotHideHazards(t *testing.T) {
	for _, raw := range []string{
		"- hosts: 'all'\n  tasks:\n    - shell: echo hello\n      ignore_errors: true # yes\n",
		`[{hosts: all, tasks: [{ansible.builtin.shell: echo hello, ignore_errors: yes}]}]`,
		"- hosts: &scope all\n  tasks:\n    - ansible.builtin.command: echo hello\n      ignore_errors: yes\n- hosts: *scope\n",
	} {
		r := scanRules([]byte(raw), "playbook", "test", ansibleRules)
		found := map[string]bool{}
		for _, f := range r.Findings {
			found[strings.Split(f.ID, "-")[0]] = true
		}
		for _, id := range []string{"ALLHOSTS", "SHELL", "IGNORE"} {
			if !found[id] {
				t.Fatalf("missing %s: %+v", id, r)
			}
		}
	}
	for _, raw := range []string{"- hosts: [", "- hosts: web\n  roles: [external]\n", "- hosts: '{{ target }}'\n", "- hosts: web\n  tasks:\n    - include_tasks: tasks.yml\n"} {
		if r := scanRules([]byte(raw), "playbook", "test", ansibleRules); r.Status() != finding.StatusIncomplete {
			t.Fatalf("expected incomplete: %+v", r)
		}
	}
}

func TestParsedAnsibleCredentialsNeverRevealMultilineValues(t *testing.T) {
	r := scanAnsibleYAML([]byte("- hosts: web\n  vars:\n    password: |\n      first-secret\n      second-secret words\n"), "playbook", "test")
	var b bytes.Buffer
	if err := finding.RenderJSON(&b, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "first-secret") || strings.Contains(b.String(), "second-secret") {
		t.Fatal("credential leaked")
	}
}

func TestAnsibleDecompositionCannotDropPlaySections(t *testing.T) {
	for _, section := range []string{"pre_tasks", "post_tasks", "roles", "handlers", "import_playbook"} {
		raw := "- hosts: localhost\n  " + section + ": []\n  tasks:\n    - amazon.aws.ec2_vpc_net:\n        name: demo\n        cidr_block: 10.0.0.0/16\n"
		code, out, err := execute("ansible2aws", []string{"--format", "json"}, raw)
		if code != 30 || !strings.Contains(out, section) {
			t.Fatalf("%s: %d %s %s", section, code, out, err)
		}
	}
}

func TestPRChecksMustFinishAndMatchRequiredNames(t *testing.T) {
	for _, tc := range []struct {
		check string
		merge string
		want  int
	}{
		{`{"name":"ci","status":"IN_PROGRESS","conclusion":null}`, "MERGEABLE", 30},
		{`{"name":"ci","status":"COMPLETED","conclusion":"SUCCESS"}`, "UNKNOWN", 30},
		{`{"name":"ci","status":"COMPLETED","conclusion":"SUCCESS"}`, "MERGEABLE", 0},
		{`{"context":"ci","state":"SUCCESS"}`, "MERGEABLE", 0},
		{`{"name":"ci","status":"COMPLETED","conclusion":"FAILURE"}`, "MERGEABLE", 30},
	} {
		raw := `{"title":"test","headRefOid":"` + strings.Repeat("a", 40) + `","mergeable":"` + tc.merge + `","reviewDecision":"APPROVED","statusCheckRollup":[` + tc.check + `]}`
		code, out, err := execute("pr-manager", []string{"inspect", "--require-check", "ci"}, raw)
		if code != tc.want {
			t.Fatalf("%d want %d: %s %s", code, tc.want, out, err)
		}
	}
}

func TestReviewChangeBindsCleanSourceIdentityAndFreshness(t *testing.T) {
	dir := t.TempDir()
	source := []byte("- hosts: web\n  tasks: []\n")
	input := filepath.Join(dir, "play.yml")
	os.WriteFile(input, source, 0600)
	commit := strings.Repeat("a", 40)
	code, report, err := execute("ansible-check", []string{"--input", input, "--environment", "production", "--change-id", "C1", "--commit", commit, "--format", "json"}, "")
	if code != 0 {
		t.Fatalf("%d %s %s", code, report, err)
	}
	reportPath := filepath.Join(dir, "report.json")
	os.WriteFile(reportPath, []byte(report), 0600)
	manifest := reviewChangeManifest{SchemaVersion: "1", ChangeID: "C1", Commit: commit, Environment: "production", RequiredComponents: []string{"ansible"}, Reports: map[string]string{"ansible": "report.json"}, Sources: map[string]string{"ansible": "play.yml"}}
	path := filepath.Join(dir, "manifest.json")
	run := func(want int) {
		t.Helper()
		data, _ := json.Marshal(manifest)
		os.WriteFile(path, data, 0600)
		c, o, e := execute("review-change", []string{"--manifest", path}, "")
		if c != want {
			t.Fatalf("got %d want %d %s %s", c, want, o, e)
		}
	}
	run(0)
	manifest.Environment = "staging"
	run(30)
	manifest.Environment = "production"
	manifest.Commit = strings.Repeat("b", 40)
	run(30)
	manifest.Commit = commit
	os.WriteFile(input, []byte("- hosts: changed\n"), 0600)
	run(30)
	os.WriteFile(input, source, 0600)
	r, e := decodeSessionReport([]byte(report))
	if e != nil || r.Provenance == nil {
		t.Fatal("provenance lost")
	}
	r.Provenance.CollectedAt = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	var b bytes.Buffer
	finding.RenderJSON(&b, r)
	os.WriteFile(reportPath, b.Bytes(), 0600)
	run(30)
}
