package toolkit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func execute(command string, args []string, input string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(command, args, strings.NewReader(input), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRuleBasedChecks(t *testing.T) {
	tests := []struct {
		command, input, want string
		code                 int
	}{
		{"git-danger-check", "aws ec2 terminate-instances --instance-ids i-123\n", "Destructive infrastructure command", 20},
		{"gha-tool", "steps:\n  - uses: actions/checkout@v4\n", "not pinned to a commit SHA", 10},
		{"ansible-check", "- hosts: all\n  tasks:\n    ansible.builtin.shell: reboot\n", "Play targets all hosts", 20},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			code, stdout, stderr := execute(tt.command, nil, tt.input)
			if code != tt.code || !strings.Contains(stdout, tt.want) || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestPolicySuppressionsRequireOwnershipAndExpiry(t *testing.T) {
	directory := t.TempDir()
	policy := filepath.Join(directory, "policy.json")
	data := `{"suppressions":[{"id_prefix":"MUTABLE","resource":"standard-input","owner":"platform-team","reason":"internal mirrored action","expires":"2099-01-01"}]}`
	if err := os.WriteFile(policy, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("gha-tool", []string{"--policy", policy}, "steps:\n  - uses: actions/checkout@v4\n")
	if code != 0 || !strings.Contains(stdout, "suppressed MUTABLE-001 by platform-team") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCIFormats(t *testing.T) {
	input := "aws ec2 terminate-instances --instance-ids i-123\n"
	code, stdout, _ := execute("git-danger-check", []string{"--format", "github"}, input)
	if code != 20 || !strings.Contains(stdout, "::error title=DELETE-001") {
		t.Fatalf("github code=%d stdout=%q", code, stdout)
	}
	code, stdout, _ = execute("git-danger-check", []string{"--format", "sarif"}, input)
	if code != 20 || !strings.Contains(stdout, `"version": "2.1.0"`) {
		t.Fatalf("sarif code=%d stdout=%q", code, stdout)
	}
}

func TestTofuCheckBlocksDatabaseReplacement(t *testing.T) {
	input := `{"format_version":"1.0","resource_changes":[{"address":"aws_db_instance.main","type":"aws_db_instance","change":{"actions":["delete","create"],"before":{},"after":{}}}]}`
	code, stdout, stderr := execute("tofu-check", nil, input)
	if code != 20 || !strings.Contains(stdout, "Resource will be replaced") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCommonFlagOverridesReachCommands(t *testing.T) {
	code, stdout, stderr := execute("tofu-check", []string{"--format", "json", "--environment", "production"}, `{"format_version":"1.0","resource_changes":[]}`)
	if code != 0 || !strings.Contains(stdout, `"schema_version": "1"`) || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestReadOnlyCollectorsUseOfficialCLIShapes(t *testing.T) {
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	var calls []string
	executeReadOnly = func(name string, args ...string) commandResult {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch name {
		case "tofu":
			return commandResult{stdout: []byte(`{"format_version":"1.0","resource_changes":[]}`)}
		case "gh":
			return commandResult{stdout: []byte(`{"number":1,"title":"x","headRefOid":"abc","mergeable":"MERGEABLE","reviewDecision":"APPROVED","statusCheckRollup":[]}`)}
		default:
			return commandResult{err: os.ErrNotExist}
		}
	}
	code, _, _ := execute("tofu-check", []string{"--plan", "review.tfplan"}, "")
	if code != 0 {
		t.Fatalf("tofu collector code=%d", code)
	}
	code, _, _ = execute("pr-manager", []string{"inspect", "--collect", "42", "--repo", "owner/repo"}, "")
	if code != 0 {
		t.Fatalf("GitHub collector code=%d", code)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "tofu show -json review.tfplan") || !strings.Contains(joined, "gh pr view 42 --json") {
		t.Fatalf("unexpected collector calls:\n%s", joined)
	}
}

func TestCollectorFailureIsIncomplete(t *testing.T) {
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	executeReadOnly = func(name string, args ...string) commandResult {
		return commandResult{stderr: "not authenticated", err: os.ErrPermission}
	}
	code, stdout, _ := execute("spacelift-check", []string{"--stack", "prod", "--run", "r-1"}, "")
	if code != 30 || !strings.Contains(stdout, "REVIEW RESULT: INCOMPLETE") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestSpaceliftAndPRCleanSnapshots(t *testing.T) {
	spacelift := `{"run_id":"r-1","stack_id":"prod","commit_sha":"abc123","state":"finished"}`
	code, stdout, stderr := execute("spacelift-check", []string{"--expect-stack", "prod", "--expect-commit", "abc123"}, spacelift)
	if code != 0 || !strings.Contains(stdout, "REVIEW RESULT: CLEAN") || stderr != "" {
		t.Fatalf("spacelift code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	pr := `{"number":42,"title":"Safe change","headRefOid":"abc123","mergeable":"MERGEABLE","reviewDecision":"APPROVED","statusCheckRollup":[]}`
	code, stdout, stderr = execute("pr-manager", []string{"inspect", "--expect-commit", "abc123"}, pr)
	if code != 0 || !strings.Contains(stdout, "REVIEW RESULT: CLEAN") || stderr != "" {
		t.Fatalf("pr code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestPRCreateDefaultsToPreview(t *testing.T) {
	code, stdout, stderr := execute("pr-manager", []string{"create", "--title", "Safe change", "--body", "Reviewed body", "--base", "main"}, "")
	if code != 0 || !strings.Contains(stdout, "EXECUTION: NOT RUN") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestGHAFormatAndUpdatePreviewDoNotWrite(t *testing.T) {
	code, stdout, stderr := execute("gha-tool", []string{"fmt"}, "name: test  \r\n")
	if code != 0 || stdout != "name: test\n" || stderr != "" {
		t.Fatalf("fmt code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	directory := t.TempDir()
	workflow := filepath.Join(directory, "workflow.yml")
	template := filepath.Join(directory, "template.yml")
	if err := os.WriteFile(workflow, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, []byte("replacement: true  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = execute("gha-tool", []string{"update", "--input", workflow, "--template", template}, "")
	current, _ := os.ReadFile(workflow)
	if code != 0 || !strings.Contains(stdout, "replacement: true") || string(current) != "original\n" || stderr != "" {
		t.Fatalf("update code=%d stdout=%q current=%q stderr=%q", code, stdout, current, stderr)
	}
}

func TestCloudContextMismatchBlocks(t *testing.T) {
	input := `{"cloud":"aws","Account":"222222222222","region":"us-east-1"}`
	args := []string{"--expect-cloud", "aws", "--expect-account", "111111111111", "--expect-region", "us-east-1"}
	code, stdout, _ := execute("cloud-context-check", args, input)
	if code != 20 || !strings.Contains(stdout, "account expected 111111111111; got 222222222222") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestImportantCheckUsesConfiguredPaths(t *testing.T) {
	config := filepath.Join(t.TempDir(), "important.conf")
	if err := os.WriteFile(config, []byte("infra/**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/infra/main.tf b/infra/main.tf\n--- a/infra/main.tf\n+++ b/infra/main.tf\n@@ -1 +0,0 @@\n-resource {}\n"
	code, stdout, stderr := execute("git-isimportant-check", []string{"--config", config}, diff)
	if code != 20 || !strings.Contains(stdout, "Important path changed") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestJSONUpdatePreviewsDeepMerge(t *testing.T) {
	directory := t.TempDir()
	patch := filepath.Join(directory, "patch.json")
	if err := os.WriteFile(patch, []byte(`{"vm":{"size":"large"},"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("git-update-json", []string{"--patch", patch}, `{"vm":{"name":"web","size":"small"}}`)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"name": "web"`) || !strings.Contains(stdout, `"size": "large"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestDeployReviewCombinesFindingReport(t *testing.T) {
	report := map[string]any{
		"status": "blocked",
		"findings": []map[string]any{{
			"id": "X-1", "severity": "high", "title": "Risk", "resource": "prod", "action": "deploy",
			"environment": "production", "reason": "test", "evidence": []string{"evidence"}, "confidence": "high", "remediation": "stop",
		}},
		"incomplete_checks": []string{},
	}
	data, _ := json.Marshal(report)
	code, stdout, stderr := execute("deploy-review", nil, string(data))
	if code != 20 || !strings.Contains(stdout, "INPUT:X-1") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunbookCheckPassesCompleteRunbook(t *testing.T) {
	input := "Owner: SRE on-call\nEnvironment and account region scope\nValidation health check\nRollback steps\nAbort stop condition\n"
	code, stdout, stderr := execute("runbook-check", nil, input)
	if code != 0 || !strings.Contains(stdout, "REVIEW RESULT: CLEAN") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAccessibilityCommands(t *testing.T) {
	code, stdout, stderr := execute("a11y-output-check", []string{"--max-line", "40"}, strings.Repeat("x", 41))
	if code != 10 || !strings.Contains(stdout, "difficult to read when magnified") || stderr != "" {
		t.Fatalf("a11y code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	report := `{"status":"review","findings":[{"id":"A-1","severity":"warning","title":"A long title for a finding","resource":"workflow","action":"review","environment":"ci","reason":"reason","evidence":["evidence"],"confidence":"high","remediation":"Review this item carefully before continuing with deployment."}],"incomplete_checks":[]}`
	code, stdout, stderr = execute("review-brief", []string{"--width", "40"}, report)
	if code != 10 || !strings.Contains(stdout, "REVIEW RESULT: REVIEW") || stderr != "" {
		t.Fatalf("brief code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if len(line) > 40 {
			t.Errorf("brief line exceeds width: %q", line)
		}
	}
}

func TestEvidencePackHashesArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(path, []byte("review"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("evidence-pack", []string{"--file", path, "--commit", "abc123", "--change-id", "PR-42"}, "")
	if code != 0 || !strings.Contains(stdout, `"change_id": "PR-42"`) || !strings.Contains(stdout, `"sha256"`) || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	code, _, stderr := execute("no-such-tool", nil, "")
	if code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestCommandHelpIsLinearWithoutTabs(t *testing.T) {
	code, _, stderr := execute("tofu-check", []string{"--help"}, "")
	if code != 0 {
		t.Fatalf("code=%d, want 0 for flag help", code)
	}
	if strings.ContainsRune(stderr, '\t') || !strings.Contains(stderr, "Option: --format") || !strings.Contains(stderr, "Description:") {
		t.Fatalf("inaccessible help output: %q", stderr)
	}
}
