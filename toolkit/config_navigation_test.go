package toolkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityBookmarkSurvivesReorderingAndRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	source := writeFixture(t, dir, "config.json", `{"servers":[{"id":"one","port":80},{"id":"two","port":443}]}`)
	state := filepath.Join(dir, "state.json")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("config-walk", append([]string{mode, "--state", state, "--format", "json"}, args...), "")
	}
	for _, step := range [][]string{{"start", "--input", source}, {"goto", "--path", "$.servers[1].port"}, {"bookmark", "--name", "https", "--identity-key", "id"}} {
		code, out, err := run(step[0], step[1:]...)
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, err)
		}
	}
	if err := os.WriteFile(source, []byte(`{"servers":[{"id":"two","port":443},{"id":"one","port":80}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, err := run("goto", "--name", "https")
	if code != 0 || !strings.Contains(out, `"cursor":"$.servers[0].port"`) {
		t.Fatalf("%d %s %s", code, out, err)
	}
	before, _ := os.ReadFile(state)
	if err := os.WriteFile(source, []byte(`{"servers":[{"id":"two","port":443},{"id":"two","port":80}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, _ = run("goto", "--name", "https")
	after, _ := os.ReadFile(state)
	if code != 30 || string(before) != string(after) {
		t.Fatal("ambiguous identity moved cursor")
	}
}
func TestHCLReferenceNavigation(t *testing.T) {
	dir := t.TempDir()
	source := writeFixture(t, dir, "main.tf", "variable \"image\" { default = \"ami-test\" }\nresource \"aws_instance\" \"web\" { ami = var.image }\n")
	state := filepath.Join(dir, "state.json")
	run := func(mode string, args ...string) (int, string, string) {
		return execute("config-walk", append([]string{mode, "--state", state, "--format", "json"}, args...), "")
	}
	for _, step := range [][]string{{"start", "--input", source}, {"goto", "--path", "$.resource.aws_instance.web.ami"}, {"references"}} {
		code, out, err := run(step[0], step[1:]...)
		if code != 0 {
			t.Fatalf("%d %s %s", code, out, err)
		}
	}
	code, out, err := run("follow", "--reference", "1")
	if code != 0 || !strings.Contains(out, `"cursor":"$.variable.image"`) {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
