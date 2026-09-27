package toolkit

import (
	"strings"
	"testing"
	"time"
)

func TestAnsibleScopeExpandsLiteralLimitAndWithholdsVars(t *testing.T) {
	dir := t.TempDir()
	playbook := writeFixture(t, dir, "site.yml", "- hosts: web\n  remote_user: deploy\n  tasks:\n    - name: ping\n      ansible.builtin.ping:\n")
	inventory := writeFixture(t, dir, "inv.json", `{"web":{"hosts":["b","a"]},"_meta":{"hostvars":{"a":{"password":"SECRET-PLAY"}}}}`)
	code, out, err := execute("ansible-scope", []string{"--playbook", playbook, "--inventory", inventory, "--format", "text"}, "")
	if code != 30 || !strings.Contains(out, "a, b") || !strings.Contains(out, "remote_user: deploy") || !strings.Contains(out, "not the effective user") || strings.Contains(out, "SECRET-PLAY") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	templated := writeFixture(t, dir, "template.yml", "- hosts: \"{{ target }}\"\n  remote_user: \"{{ user }}\"\n")
	code, out, err = execute("ansible-scope", []string{"--playbook", templated, "--inventory", inventory}, "")
	if code != 30 || !strings.Contains(out, "templated") || strings.Contains(out, "SECRET-PLAY") || err != "" {
		t.Fatalf("template %d %s %s", code, out, err)
	}
}

func TestPermissionCheckFailsClosedOnConditionsAndDeny(t *testing.T) {
	dir := t.TempDir()
	allow := writeFixture(t, dir, "allow.json", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"},{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`)
	code, out, err := execute("permission-check", []string{"--document", allow, "--action", "s3:GetObject", "--resource", "arn:aws:s3:::bucket/key", "--format", "text"}, "")
	if code != 10 || !strings.Contains(out, "matching Allow") || !strings.Contains(out, "Effective access is not established") || err != "" {
		t.Fatalf("allow %d %s %s", code, out, err)
	}
	code, out, err = execute("permission-check", []string{"--document", allow, "--action", "s3:DeleteObject", "--resource", "arn:aws:s3:::bucket/key"}, "")
	if code != 20 || !strings.Contains(out, "explicitly denies") || err != "" {
		t.Fatalf("deny %d %s %s", code, out, err)
	}
	conditional := writeFixture(t, dir, "cond.json", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*","Condition":{"StringEquals":{"aws:RequestedRegion":"us-east-1"}}}]}`)
	code, out, err = execute("permission-check", []string{"--document", conditional, "--action", "s3:GetObject", "--resource", "arn:aws:s3:::bucket/key"}, "")
	if code != 30 || !strings.Contains(out, "condition") || err != "" {
		t.Fatalf("condition %d %s %s", code, out, err)
	}
}

func TestServiceExportWithholdsUnknownControls(t *testing.T) {
	body := `{"schema":"rcdo/service-export/v1","deployment":"aws-east","cloud":"aws","service":"s3","key":"uploads","id":"uploads","account":"123456789012","location":"us-east-1","source":"redacted fixture","collected_at":"2026-09-27T12:00:00Z","observed":{"block_public_acls":true,"ignore_public_acls":true,"block_public_policy":true,"restrict_public_buckets":true,"anonymous_read":true,"versioning":"Enabled","encryption":"aws:kms"}}`
	code, out, err := execute("service-export", []string{"--format", "json"}, body)
	if code != 0 || !strings.Contains(out, `"public_access": false`) || !strings.Contains(out, `"customer_managed_key": true`) || !strings.Contains(out, `"complete": true`) || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	partial := strings.Replace(body, `"versioning":"Enabled",`, "", 1)
	code, out, err = execute("service-export", []string{"--format", "json"}, partial)
	if code != 30 || !strings.Contains(out, `"complete": false`) || strings.Contains(out, "versioning") || err != "" {
		t.Fatalf("partial %d %s %s", code, out, err)
	}
	gcp := `{"schema":"rcdo/service-export/v1","deployment":"gcp-disk","cloud":"gcp","service":"persistent-disk","key":"data","id":"data","account":"rcdo-test","location":"us-central1","source":"redacted fixture","collected_at":"2026-09-27T12:00:00Z","observed":{"size_gb":10,"encrypted":true}}`
	code, out, err = execute("service-export", []string{"--format", "text"}, gcp)
	if code != 30 || !strings.Contains(out, "size_gib was not inferred") || err != "" {
		t.Fatalf("gcp %d %s %s", code, out, err)
	}
}

func TestSpaceliftHistoryKeepsEarlierDenial(t *testing.T) {
	s := cleanSpaceFixture()
	first := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	second := time.Now().UTC().Format(time.RFC3339Nano)
	policies := []spacePolicy{{ID: "security", Type: "PLAN", Decision: "pass", History: []spacePolicyEvent{{At: first, Decision: "deny"}, {At: second, Decision: "pass"}}}}
	s.Policies = &policies
	code, out, err := execute("spacelift-check", nil, jsonFixture(s))
	if code != 20 || !strings.Contains(out, "earlier policy decision denied") || !strings.Contains(out, "history 1: deny") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
}

func TestGCPSharedVPCReferenceIsKeptAndHostIsNotInventoried(t *testing.T) {
	shared := strings.ReplaceAll(gcpInstanceFixture, "projects/rcdo-test/global/networks/app", "projects/host-vpc/global/networks/app")
	shared = strings.ReplaceAll(shared, "projects/rcdo-test/regions/us-central1/subnetworks/app", "projects/host-vpc/regions/us-central1/subnetworks/app")
	mockGCP(t, gcpConfigFixture, "["+shared+"]")
	code, out, err := execute("collect", gcpCollectArgs("relations"), "")
	if code != 0 || !strings.Contains(out, "host-vpc") || !strings.Contains(out, "were not inventoried") || strings.Contains(out, "PRIVATE-VM-METADATA") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
