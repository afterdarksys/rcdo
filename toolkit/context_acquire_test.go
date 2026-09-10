package toolkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireDockerBinding(t *testing.T) {
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	calls := 0
	executeReadOnly = func(name string, args ...string) commandResult {
		calls++
		if name != "docker" || args[0] != "--context" || args[1] != "test" {
			t.Fatalf("%s %v", name, args)
		}
		value := `"unix:///test/docker.sock"`
		if args[2] == "info" {
			value = `"daemon-id"`
		}
		return commandResult{stdout: []byte(value)}
	}
	code, out, err := execute("context-acquire", []string{"--native", "--kind", "docker", "--docker-context", "test", "--name", "engine"}, "")
	if code != 0 || calls != 3 || !strings.Contains(out, "daemon-id") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	if _, err := readContextBundle([]byte(out)); err != nil {
		t.Fatal(err)
	}
}
func TestAcquireIACWithholdsBackendSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".terraform"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, ".terraform"), "terraform.tfstate", `{"backend":{"type":"s3","config":{"bucket":"states","key":"infra.tfstate","secret_key":"private-value"}}}`)
	t.Setenv("TF_DATA_DIR", "")
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	executeReadOnly = func(name string, args ...string) commandResult {
		if name != "tofu" {
			t.Fatal(name)
		}
		if args[0] == "version" {
			return commandResult{stdout: []byte(`{"terraform_version":"1.9.0"}`)}
		}
		return commandResult{stdout: []byte("dev\n")}
	}
	code, out, err := execute("context-acquire", []string{"--native", "--kind", "tofu", "--directory", dir, "--name", "iac"}, "")
	if code != 0 || strings.Contains(out, "private-value") || !strings.Contains(out, "env:/dev/infra.tfstate") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
func TestAcquireSpaceEndpointAndApproval(t *testing.T) {
	original := executeReadOnly
	t.Cleanup(func() { executeReadOnly = original })
	apiCalls := 0
	executeReadOnly = func(name string, args ...string) commandResult {
		if name != "spacectl" {
			t.Fatal(name)
		}
		if args[0] == "whoami" {
			return commandResult{stdout: []byte(`{"id":"user","endpoint":"https://test.app.spacelift.io"}`)}
		}
		apiCalls++
		return commandResult{stdout: []byte(`{"data":{"stack":{"id":"stack","run":{"id":"run","state":"UNCONFIRMED","needsApproval":true,"isMostRecent":false,"commit":{"hash":"` + strings.Repeat("a", 40) + `"}}}}}`)}
	}
	base := []string{"--native", "--kind", "spacelift", "--name", "space", "--stack", "stack", "--run", "run", "--expect-endpoint"}
	code, _, _ := execute("context-acquire", append(base, "https://wrong.app.spacelift.io"), "")
	if code != 30 || apiCalls != 0 {
		t.Fatal("wrong account queried")
	}
	code, out, err := execute("context-acquire", append(base, "https://test.app.spacelift.io"), "")
	if code != 0 || !strings.Contains(out, `"needs_approval": "true"`) || !strings.Contains(out, `"is_most_recent": "false"`) {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
