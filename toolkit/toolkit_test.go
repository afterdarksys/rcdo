package toolkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

func TestRCDOPrimaryCommandAndLegacyName(t *testing.T) {
	for _, command := range []string{"rcdo", "git-tools"} {
		code, stdout, stderr := execute(command, []string{"version"}, "")
		if code != 0 || stderr != "" || strings.TrimSpace(stdout) != "rcdo 1.3.0-beta.1" {
			t.Fatalf("%s code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
}

func TestRCDODiffWalkIsIntegrated(t *testing.T) {
	input := "diff --git a/app.yml b/app.yml\n--- a/app.yml\n+++ b/app.yml\n@@ -1 +1 @@\n-replicas: 2\n+replicas: 3\n"
	for _, command := range []string{"diff-walk", "git-diff-walker"} {
		code, stdout, stderr := execute(command, []string{"--summary-only"}, input)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "DIFF SUMMARY") || !strings.Contains(stdout, "Additions: 1") {
			t.Fatalf("%s code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
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
	if code != 0 || !strings.Contains(stdout, "suppressed MUTABLE-") || !strings.Contains(stdout, "by platform-team") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCIFormats(t *testing.T) {
	input := "aws ec2 terminate-instances --instance-ids i-123\n"
	code, stdout, _ := execute("git-danger-check", []string{"--format", "github"}, input)
	if code != 20 || !strings.Contains(stdout, "::error title=DELETE-") {
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

func TestConfigExplainReadsStructuredFormats(t *testing.T) {
	tests := []struct {
		name, syntax, input, wantPath, wantValue string
	}{
		{"json", "json", `{"service":{"ports":[80,443]}}`, "$.service.ports[1]", "443"},
		{"yaml", "yaml", "service:\n  enabled: true\n", "$.service.enabled", "true"},
		{"toml", "toml", "[service]\nname = \"api\"\n", "$.service.name", `"api"`},
		{"hcl", "hcl", "resource \"aws_instance\" \"web\" {\n  instance_type = \"t3.micro\"\n}\n", "$.resource.aws_instance.web.instance_type", `"t3.micro"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := execute("config-explain", []string{"--syntax", tt.syntax}, tt.input)
			if code != 0 || !strings.Contains(stdout, "Path: "+tt.wantPath) || !strings.Contains(stdout, "Value: "+tt.wantValue) || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestConfigExplainRedactsAndFilters(t *testing.T) {
	input := `{"database":{"host":"db.internal","password":"do-not-print"},"region":"us-east-1"}`
	code, stdout, stderr := execute("config-explain", []string{"--path", "database", "--format", "json"}, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"path": "$.database.host"`) ||
		!strings.Contains(stdout, `"value": "[REDACTED]"`) || strings.Contains(stdout, "do-not-print") || strings.Contains(stdout, "us-east-1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = execute("config-explain", []string{"--show-secrets", "--path", "database.password"}, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `Value: "do-not-print"`) {
		t.Fatalf("show secrets code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigExplainAutoDetectsFileExtension(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "inventory.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n  - web-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-explain", []string{"--input", path}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Format: yaml") || !strings.Contains(stdout, "Path: $.hosts[0]") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigExplainRecognizesTerraformVariablesAsHCL(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "production.tfvars")
	if err := os.WriteFile(path, []byte("instance_type = \"t3.micro\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-explain", []string{"--input", path}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Format: hcl") || !strings.Contains(stdout, "Path: $.instance_type") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigExplainHonorsTextWidthForLongPaths(t *testing.T) {
	input := `{"this_is_a_very_long_configuration_key_that_exceeds_width":{"another_really_long_nested_configuration_key":true}}`
	code, stdout, stderr := execute("config-explain", []string{"--width", "40"}, input)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for lineNumber, line := range strings.Split(stdout, "\n") {
		if len([]rune(line)) > 40 {
			t.Fatalf("line %d exceeds width: %q", lineNumber+1, line)
		}
	}
}

func TestConfigDiffReportsSemanticChangesWithoutContainerNoise(t *testing.T) {
	directory := t.TempDir()
	before := filepath.Join(directory, "before.json")
	after := filepath.Join(directory, "after.json")
	if err := os.WriteFile(before, []byte(`{"service":{"image":"api:v1","replicas":2},"obsolete":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(`{"service":{"image":"api:v2","replicas":2,"port":8080}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-diff", []string{"--before", before, "--after", after, "--check"}, "")
	if code != 10 || stderr != "" || !strings.Contains(stdout, "Path: $.service.image") ||
		!strings.Contains(stdout, "Before value: \"api:v1\"") || !strings.Contains(stdout, "After value: \"api:v2\"") ||
		!strings.Contains(stdout, "Path: $.service.port") || !strings.Contains(stdout, "Path: $.obsolete") ||
		strings.Contains(stdout, "Path: $.service\n") || !strings.Contains(stdout, "Changes: 3") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigDiffDetectsButRedactsSecretChanges(t *testing.T) {
	directory := t.TempDir()
	before := filepath.Join(directory, "before.yaml")
	after := filepath.Join(directory, "after.yaml")
	if err := os.WriteFile(before, []byte("database:\n  password: old-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte("database:\n  password: new-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-diff", []string{"--before", before, "--after", after, "--format", "json"}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"changed": true`) ||
		!strings.Contains(stdout, `"path": "$.database.password"`) || strings.Count(stdout, "[REDACTED]") != 2 ||
		strings.Contains(stdout, "old-secret") || strings.Contains(stdout, "new-secret") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigDiffComparesEquivalentJSONAndYAML(t *testing.T) {
	directory := t.TempDir()
	before := filepath.Join(directory, "before.json")
	after := filepath.Join(directory, "after.yaml")
	if err := os.WriteFile(before, []byte(`{"enabled":true,"ports":[80,443]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte("ports:\n  - 80\n  - 443\nenabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-diff", []string{"--before", before, "--after", after, "--check"}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Result: UNCHANGED") || !strings.Contains(stdout, "Changes: 0") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigDiffPathFilterCanSelectUnchangedSubtree(t *testing.T) {
	directory := t.TempDir()
	before := filepath.Join(directory, "before.toml")
	after := filepath.Join(directory, "after.toml")
	if err := os.WriteFile(before, []byte("[service]\nname = \"api\"\nport = 80\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte("[service]\nname = \"api\"\nport = 443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-diff", []string{"--before", before, "--after", after, "--path", "service.name", "--check"}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Result: UNCHANGED") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigDiffOrdersArrayIndexesNumerically(t *testing.T) {
	directory := t.TempDir()
	before := filepath.Join(directory, "before.json")
	after := filepath.Join(directory, "after.json")
	if err := os.WriteFile(before, []byte(`{"items":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(`{"items":[0,1,2,3,4,5,6,7,8,9,10]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-diff", []string{"--before", before, "--after", after}, "")
	if code != 0 || stderr != "" || strings.Index(stdout, "Path: $.items[2]") > strings.Index(stdout, "Path: $.items[10]") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigSetPreviewsThenWritesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"service":{"replicas":2,"password":"old"}}`), 0o640); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-set", []string{"--input", path, "--path", "service.replicas", "--value", "4"}, "")
	current, _ := os.ReadFile(path)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Before value: 2") || !strings.Contains(stdout, "After value: 4") || !strings.Contains(stdout, "File written: no") || !strings.Contains(string(current), `"replicas":2`) {
		t.Fatalf("preview code=%d stdout=%q current=%q stderr=%q", code, stdout, current, stderr)
	}
	code, stdout, stderr = execute("config-set", []string{"--input", path, "--path", "service.password", "--value", "new", "--string", "--write", "--expect-sha256", fmt.Sprintf("%x", sha256.Sum256(current))}, "")
	current, _ = os.ReadFile(path)
	info, _ := os.Stat(path)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "UPDATED:") || strings.Contains(stdout, "new") || !strings.Contains(string(current), `"new"`) || info.Mode().Perm() != 0o640 {
		t.Fatalf("write code=%d stdout=%q current=%q mode=%o stderr=%q", code, stdout, current, info.Mode().Perm(), stderr)
	}
}

func TestConfigRemoveSupportsArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("ports:\n  - 80\n  - 443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("config-remove", []string{"--input", path, "--path", "ports[0]"}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Action: CHANGED") || !strings.Contains(stdout, "Before value: 80") || !strings.Contains(stdout, "After value: 443") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigSetMutatesHCLAttributeWithPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.tf")
	input := "resource \"aws_instance\" \"web\" {\n  instance_type = \"t3.micro\"\n}\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--input", path, "--path", "resource.aws_instance.web.instance_type", "--value", "t3.large", "--string"}
	code, stdout, stderr := execute("config-set", args, "")
	current, _ := os.ReadFile(path)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `Before value: "t3.micro"`) || !strings.Contains(stdout, `After value: "t3.large"`) || string(current) != input {
		t.Fatalf("preview code=%d stdout=%q current=%q stderr=%q", code, stdout, current, stderr)
	}
	code, stdout, stderr = execute("config-set", append(args, "--write", "--expect-sha256", fmt.Sprintf("%x", sha256.Sum256(current))), "")
	current, _ = os.ReadFile(path)
	if code != 0 || stderr != "" || !strings.Contains(string(current), `instance_type = "t3.large"`) {
		t.Fatalf("write code=%d stdout=%q current=%q stderr=%q", code, stdout, current, stderr)
	}
}

func TestErrorExplainClassifiesAndRedacts(t *testing.T) {
	input := "Error: AccessDenied: not authorized to perform action token=\"abc 123\" Authorization: Bearer eyJ.secret\nrequest failed\n"
	code, stdout, stderr := execute("error-explain", nil, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Summary: Permission denied") || !strings.Contains(stdout, "token=[REDACTED]") || strings.Contains(stdout, "abc 123") || strings.Contains(stdout, "eyJ.secret") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestOperationalPolicyBlocksPublicAccess(t *testing.T) {
	code, stdout, stderr := execute("ops-policy-check", []string{"--environment", "production"}, `{"ingress_cidr":"0.0.0.0/0"}`)
	if code != 20 || stderr != "" || !strings.Contains(stdout, "Public network access configured") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRepositoryPolicyChecksCriticalAndCompanionPaths(t *testing.T) {
	directory := t.TempDir()
	policy := filepath.Join(directory, "policy.yaml")
	policyData := "version: \"1\"\ncritical_paths:\n  - pattern: infra/**\n    owner: platform\n    severity: high\n    reason: production infrastructure\nrequired_companions:\n  - when: infra/**\n    require: [runbooks/**]\n    reason: infrastructure changes need rollback instructions\n"
	if err := os.WriteFile(policy, []byte(policyData), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("repo-policy-check", []string{"--repo-policy", policy}, "M\tinfra/main.tf\n")
	if code != 20 || stderr != "" || !strings.Contains(stdout, "Repository critical path changed") || !strings.Contains(stdout, "Required companion change is missing") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestUnifiedReviewUsesReadOnlyGitAndPolicyChecks(t *testing.T) {
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	executeReadOnly = func(name string, args ...string) commandResult {
		joined := strings.Join(args, " ")
		switch {
		case name == "git" && strings.Contains(joined, "--name-status"):
			return commandResult{stdout: []byte("M\tinfra/main.tf\n")}
		case name == "git" && args[0] == "diff":
			return commandResult{stdout: []byte("+ cidr = \"0.0.0.0/0\"\n")}
		case name == "git" && args[0] == "show" && strings.Contains(args[1], "base:"):
			return commandResult{stdout: []byte("cidr = \"10.0.0.0/8\"\n")}
		case name == "git" && args[0] == "show":
			return commandResult{stdout: []byte("cidr = \"0.0.0.0/0\"\n")}
		default:
			return commandResult{err: os.ErrNotExist}
		}
	}
	code, stdout, stderr := execute("review", []string{"--base", "base", "--head", "head", "--repo-policy", ""}, "")
	if code != 20 || stderr != "" || !strings.Contains(stdout, "Structured configuration changed") || !strings.Contains(stdout, "Public network access configured") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestReviewSessionCanResumeAndAcknowledge(t *testing.T) {
	directory := t.TempDir()
	reportPath := filepath.Join(directory, "report.json")
	sessionPath := filepath.Join(directory, "session.json")
	report := `{"schema_version":"1","status":"blocked","completed_checks":["risk check"],"incomplete_checks":[],"findings":[{"id":"RISK-1","severity":"high","title":"Risk","resource":"prod","action":"review","environment":"production","reason":"danger","evidence":["x"],"confidence":"high","remediation":"stop"}]}`
	if err := os.WriteFile(reportPath, []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("review-session", []string{"start", "--report", reportPath, "--session", sessionPath, "--change-id", "PR-1", "--commit", "abc"}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "REVIEW SESSION STARTED") {
		t.Fatalf("start code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = execute("review-session", []string{"next", "--session", sessionPath}, "")
	if code != 20 || !strings.Contains(stdout, "ID: RISK-1") {
		t.Fatalf("next code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = execute("review-session", []string{"ack", "--session", sessionPath, "--id", "RISK-1", "--note", "reviewed with platform"}, "")
	if code != 0 || !strings.Contains(stdout, "ACKNOWLEDGED") {
		t.Fatalf("ack code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = execute("review-session", []string{"next", "--session", sessionPath}, "")
	if code != 20 || !strings.Contains(stdout, "READING COMPLETE") || !strings.Contains(stdout, "REVIEW RESULT: BLOCKED") {
		t.Fatalf("complete code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAppConfigDefaultsAndCLIOverride(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	code, stdout, stderr := execute("config", []string{"init", "--file", configPath}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "CONFIGURATION CREATED") {
		t.Fatalf("init code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = execute("config", []string{"set", "--file", configPath, "--key", "commands.config-explain.width", "--value", "40"}, "")
	if code != 0 || stderr != "" {
		t.Fatalf("set code=%d stderr=%q", code, stderr)
	}
	input := `{"this_is_a_very_long_configuration_key_that_needs_wrapping":true}`
	code, stdout, stderr = execute("config-explain", []string{"--config-file", configPath}, input)
	if code != 0 || stderr != "" {
		t.Fatalf("configured command code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if len([]rune(line)) > 40 {
			t.Fatalf("configured width not applied: %q", line)
		}
	}
	code, stdout, stderr = execute("config-explain", []string{"--config-file", configPath, "--width", "80"}, input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "this_is_a_very_long_configuration_key_that_needs_wrapping") {
		t.Fatalf("CLI override code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCredentialStoreIsPrivateRedactedAndRouted(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	if code, _, stderr := execute("config", []string{"init", "--file", configPath}, ""); code != 0 {
		t.Fatalf("init code=%d stderr=%q", code, stderr)
	}
	secret := "sk-test-do-not-print"
	code, stdout, stderr := execute("config", []string{"credential-set", "--file", configPath, "--provider", "openai", "--stdin"}, secret+"\n")
	if code != 0 || stderr != "" || strings.Contains(stdout, secret) || !strings.Contains(stdout, "[REDACTED]") {
		t.Fatalf("credential set code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	credentialPath := filepath.Join(directory, "credentials.json")
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode=%04o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(credentialPath)
	if !strings.Contains(string(data), secret) {
		t.Fatal("credential was not stored")
	}
	code, stdout, stderr = execute("config", []string{"ai-status", "--file", configPath}, "")
	if code != 0 || stderr != "" || strings.Contains(stdout, secret) || !strings.Contains(stdout, "Slot: primary\nProvider: openai\nAvailable: yes\nCredential source: credential store") {
		t.Fatalf("status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = execute("config", []string{"ai-order", "--file", configPath, "--primary", "anthropic", "--backup", "openrouter", "--tertiary", "openai"}, "")
	if code != 0 || stderr != "" {
		t.Fatalf("order code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = execute("config", []string{"ai-status", "--file", configPath}, "")
	if code != 0 || !strings.Contains(stdout, "Slot: primary\nProvider: anthropic") || !strings.Contains(stdout, "Slot: tertiary\nProvider: openai\nAvailable: yes") {
		t.Fatalf("reordered status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = execute("config", []string{"ai-resolve", "--file", configPath}, "")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Selected slot: tertiary\nProvider: openai") || strings.Contains(stdout, secret) {
		t.Fatalf("resolve code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAppConfigRejectsAPIKeyInOrdinaryConfig(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	if code, _, stderr := execute("config", []string{"init", "--file", configPath}, ""); code != 0 {
		t.Fatalf("init code=%d stderr=%q", code, stderr)
	}
	code, _, stderr := execute("config", []string{"set", "--file", configPath, "--key", "providers.openai.api_key", "--value", "do-not-store-here"}, "")
	if code != 2 || !strings.Contains(stderr, "credential-set") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), "do-not-store-here") {
		t.Fatal("secret leaked into ordinary config")
	}
}

func TestCredentialStoreRejectsLoosePermissions(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	if code, _, stderr := execute("config", []string{"init", "--file", configPath}, ""); code != 0 {
		t.Fatalf("init code=%d stderr=%q", code, stderr)
	}
	credentialPath := filepath.Join(directory, "credentials.json")
	if err := os.WriteFile(credentialPath, []byte(`{"schema_version":"1","providers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := execute("config", []string{"ai-status", "--file", configPath}, "")
	if code != 2 || !strings.Contains(stderr, "require 0600") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
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

func TestDeployReviewRequiresNamedComponents(t *testing.T) {
	directory := t.TempDir()
	report := filepath.Join(directory, "tofu.json")
	if err := os.WriteFile(report, []byte(`{"findings":[],"completed_checks":["plan"],"incomplete_checks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--report", "opentofu=" + report, "--require", "opentofu", "--require", "cloud-context"}
	code, stdout, stderr := execute("deploy-review", args, "")
	if code != 30 || !strings.Contains(stdout, "required component cloud-context has no valid report") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestReviewChangeManifestEnforcesCoverage(t *testing.T) {
	directory := t.TempDir()
	report := filepath.Join(directory, "tofu.json")
	if err := os.WriteFile(report, []byte(`{"schema_version":"1","status":"clean","findings":[],"completed_checks":["plan"],"incomplete_checks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(directory, "review-change.json")
	manifestData := `{
  "schema_version":"1",
  "change_id":"PR-42",
  "commit":"abc123",
  "environment":"production",
  "required_components":["opentofu","cloud-context"],
  "reports":{"opentofu":"tofu.json"}
}`
	if err := os.WriteFile(manifest, []byte(manifestData), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := execute("review-change", []string{"--manifest", manifest}, "")
	if code != 30 || !strings.Contains(stdout, "change manifest PR-42; commit abc123; environment production") ||
		!strings.Contains(stdout, "required component cloud-context has no valid report") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	cloudReport := filepath.Join(directory, "cloud.json")
	if err := os.WriteFile(cloudReport, []byte(`{"schema_version":"1","status":"clean","findings":[],"completed_checks":["identity"],"incomplete_checks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestData = strings.Replace(manifestData, `"reports":{"opentofu":"tofu.json"}`, `"reports":{"opentofu":"tofu.json","cloud-context":"cloud.json"}`, 1)
	if err := os.WriteFile(manifest, []byte(manifestData), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = execute("review-change", []string{"--manifest", manifest}, "")
	if code != 0 || !strings.Contains(stdout, "REVIEW RESULT: CLEAN") || stderr != "" {
		t.Fatalf("clean code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestFindingIDsRemainStableWhenEarlierLinesAreInserted(t *testing.T) {
	input := "aws ec2 terminate-instances --instance-ids i-123\n"
	_, first, _ := execute("git-danger-check", nil, input)
	_, second, _ := execute("git-danger-check", nil, "# unrelated comment\n"+input)
	firstID := textField(first, "ID: ")
	secondID := textField(second, "ID: ")
	if firstID == "" || firstID != secondID {
		t.Fatalf("finding IDs changed: first=%q second=%q", firstID, secondID)
	}
}

func textField(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
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
