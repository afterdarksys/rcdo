package toolkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeAppConfigFixture(t *testing.T, c appConfig) string {
	t.Helper()
	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return writeFixture(t, t.TempDir(), "config.yaml", string(data))
}

func TestNewCommandConfiguredFlagsExist(t *testing.T) {
	modes := map[string]string{"log-read": "show", "markdown-view": "show", "incident": "show", "runbook": "show", "state-walk": "show", "tasks": "list", "config-walk": "show"}
	for _, command := range []string{"log-read", "markdown-view", "to-markdown", "context-summary", "resource-walk", "incident", "runbook", "fleet-check", "report-read", "doctor", "collect", "changes", "kube-explain", "ansible-watch", "state-walk", "network-check", "tasks", "config-walk"} {
		t.Run(command, func(t *testing.T) {
			args := []string{"--help"}
			if mode := modes[command]; mode != "" {
				args = append([]string{mode}, args...)
			}
			code, out, stderr := execute(command, args, "")
			if code != 0 {
				t.Fatalf("%d %s %s", code, out, stderr)
			}
			for key := range configurableFlags[command] {
				if !strings.Contains(out+stderr, "Option: --"+key+"\n") {
					t.Errorf("missing flag %s", key)
				}
			}
		})
	}
}

func TestConfiguredLogNavigationAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	c := defaultAppConfig()
	c.Commands["log-read"] = map[string]any{"format": "json", "context": 0, "state": state}
	config := writeAppConfigFixture(t, c)
	source := writeFixture(t, dir, "app.log", "first\nsecond\n")
	for _, step := range [][]string{{"start", "--input", source}, {"next"}, {"show"}} {
		code, out, stderr := execute("log-read", append(step, "--config-file", config), "")
		var v struct {
			Cursor int
			Events []logEvent
		}
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil || len(v.Events) != 1 {
			t.Fatalf("%v: %d %s %s", step, code, out, stderr)
		}
	}
	code, out, stderr := execute("log-read", []string{"show", "--config-file", config, "-format=text"}, "")
	if code != 0 || !strings.Contains(out, "Line 2.") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal(err)
	}
	// Config cannot silently choose a network target or enable an action.
	for _, tc := range []struct{ command, key string }{{"kube-explain", "native"}, {"network-check", "url"}, {"collect", "expect-account"}, {"doctor", "versions"}, {"incident", "text"}} {
		c.Commands[tc.command] = map[string]any{tc.key: "unsafe"}
		if validateAppConfig(c) == nil {
			t.Errorf("accepted %s.%s", tc.command, tc.key)
		}
		delete(c.Commands, tc.command)
	}
}
