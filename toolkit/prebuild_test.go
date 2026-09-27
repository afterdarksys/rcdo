package toolkit

import (
	"strings"
	"testing"
)

func TestPrebuildListsPlanAndPlaybookWithoutApplying(t *testing.T) {
	dir := t.TempDir()
	plan := writeFixture(t, dir, "plan.json", `{"format_version":"1.2","terraform_version":"1.9.0","resource_changes":[{"address":"aws_instance.web","type":"aws_instance","change":{"actions":["create"]}},{"address":"aws_security_group.old","type":"aws_security_group","change":{"actions":["delete"]}}]}`)
	playbook := writeFixture(t, dir, "site.yml", "- hosts: web\n  tasks:\n    - name: remove file\n      ansible.builtin.file:\n        path: /var/app/old\n        state: absent\n    - name: note\n      ansible.builtin.debug:\n        msg: SECRET-PLAY\n")
	code, out, err := execute("prebuild", []string{"--plan", plan, "--playbook", playbook, "--color", "always", "--format", "text"}, "")
	if code != 10 || !strings.Contains(out, "Nothing was applied") || !strings.Contains(out, "create") || !strings.Contains(out, "[vm]") || !strings.Contains(out, "aws_instance.web") || !strings.Contains(out, "delete") || !strings.Contains(out, "[filter]") || !strings.Contains(out, "\x1b[31m") || strings.Contains(out, "SECRET-PLAY") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("prebuild", []string{"--plan", plan, "--playbook", playbook, "--color", "never", "--format", "json"}, "")
	if code != 10 || strings.Contains(out, "\x1b") || !strings.Contains(out, `"applied": false`) || !strings.Contains(out, `"class": "vm"`) || err != "" {
		t.Fatalf("json %d %s %s", code, out, err)
	}
	code, out, err = execute("prebuild", []string{"--plan", plan, "--color", "never"}, "")
	if code != 30 || !strings.Contains(out, "playbook was not supplied") || err != "" {
		t.Fatalf("one side %d %s %s", code, out, err)
	}
	code, _, err = execute("prebuild", []string{"--plan", plan, "--playbook", playbook, "--color-flag", "vm=not-a-color"}, "")
	if code != 2 || !strings.Contains(err, "color flag") {
		t.Fatalf("flag %d %s", code, err)
	}
	code, out, err = execute("prebuild", []string{"--plan", plan, "--playbook", playbook, "--color", "always", "--color-flag", "action-delete=blue"}, "")
	if code != 10 || !strings.Contains(out, "\x1b[34mdelete") || err != "" {
		t.Fatalf("tune %d %s %s", code, out, err)
	}
}

func TestPrebuildEnsureTaskStaysUnproven(t *testing.T) {
	dir := t.TempDir()
	plan := writeFixture(t, dir, "plan.json", `{"format_version":"1.2","terraform_version":"1.9.0","resource_changes":[{"address":"aws_s3_bucket.logs","type":"aws_s3_bucket","change":{"actions":["no-op"]}}]}`)
	playbook := writeFixture(t, dir, "site.yml", "- hosts: web\n  tasks:\n    - name: config\n      ansible.builtin.template:\n        dest: /etc/app.conf\n")
	code, out, err := execute("prebuild", []string{"--plan", plan, "--playbook", playbook, "--color", "never"}, "")
	if code != 30 || !strings.Contains(out, "not compared with the host") || !strings.Contains(out, "[fs]") || !strings.Contains(out, "path /etc/app.conf") || strings.Contains(out, "\x1b") || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
