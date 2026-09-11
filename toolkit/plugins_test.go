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

	"gopkg.in/yaml.v3"
)

func pluginFixture(t *testing.T, body string) (string, string, appConfig) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "plugin with spaces")
	if e := os.WriteFile(exe, []byte("#!/bin/sh\n"+body), 0700); e != nil {
		t.Fatal(e)
	}
	c := defaultAppConfig()
	c.Plugins = pluginConfig{Enabled: true, Entries: map[string]pluginEntry{"example": {Enabled: true, Path: exe}}}
	path := filepath.Join(dir, "config.yaml")
	savePluginConfig(t, path, c)
	return path, exe, c
}
func savePluginConfig(t *testing.T, path string, c appConfig) {
	t.Helper()
	data, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
}

const cleanPluginJSON = `{"schema_version":"1","status":"clean","findings":[],"completed_checks":["Example check completed"],"incomplete_checks":[]}`

func TestPluginDisabledNeverExecutesOrReadsInput(t *testing.T) {
	path, exe, c := pluginFixture(t, "exit 99\n")
	// A nonexistent executable and failing reader must remain untouched by switches.
	os.Remove(exe)
	for _, global := range []bool{false, true} {
		c.Plugins.Enabled = global
		p := c.Plugins.Entries["example"]
		p.Enabled = !global
		c.Plugins.Entries["example"] = p
		savePluginConfig(t, path, c)
		code, _, err := execute("plugin", []string{"run", "--config-file", path, "--name", "example", "--input", "/nonexistent-input"}, "")
		if code != 2 || !strings.Contains(err, "disabled") {
			t.Fatalf("%d %s", code, err)
		}
		code, out, err := execute("plugin", []string{"list", "--config-file", path}, "")
		if code != 0 || !strings.Contains(out, "Effective enabled: false") {
			t.Fatalf("%d %s %s", code, out, err)
		}
	}
}

func TestPluginProtocolAndRendering(t *testing.T) {
	path, exe, _ := pluginFixture(t, "cat > \"$0.request\"\nprintf '%s' '"+cleanPluginJSON+"'\n")
	code, out, err := execute("plugin", []string{"run", "--config-file", path, "--name", "example", "--format", "json", "--", "$(touch unwanted)", "--width=1", "--config-file=/nonexistent"}, "sample input")
	if code != 0 || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	r, e := decodeSessionReport([]byte(out))
	if e != nil || r.Provenance != nil {
		t.Fatal(e, out)
	}
	data, e := os.ReadFile(exe + ".request")
	if e != nil {
		t.Fatal(e)
	}
	var request pluginRequest
	if json.Unmarshal(data, &request) != nil || request.SchemaVersion != "1" || request.Plugin != "example" || request.Input != "sample input" || len(request.Args) != 3 || request.Args[0] != "$(touch unwanted)" || request.Args[2] != "--config-file=/nonexistent" {
		t.Fatal(string(data))
	}
}

func TestPluginStatusNamespaceAndProvenance(t *testing.T) {
	for _, tc := range []struct {
		severity finding.Severity
		code     int
	}{{finding.SeverityWarning, 10}, {finding.SeverityHigh, 20}} {
		r := finding.Report{CompletedChecks: []string{"External check completed"}, Provenance: &finding.Provenance{SchemaVersion: "1", Tool: "tofu-check", Environment: "staging", SourceSHA256: strings.Repeat("a", 64), CollectedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
		addIAC(&r, "EXT", tc.severity, "External finding", "resource", "check", "staging", "Observed result")
		var b bytes.Buffer
		if e := finding.RenderJSON(&b, r); e != nil {
			t.Fatal(e)
		}
		path, _, _ := pluginFixture(t, "cat <<'REPORT'\n"+b.String()+"\nREPORT\n")
		code, out, err := execute("plugin", []string{"run", "--config-file", path, "--name", "example", "--format", "json"}, "")
		if code != tc.code {
			t.Fatalf("%d %s %s", code, out, err)
		}
		parsed, e := decodeSessionReport([]byte(out))
		if e != nil || parsed.Provenance != nil || !strings.HasPrefix(parsed.Findings[0].ID, "example/") {
			t.Fatal(e, out)
		}
	}
}

func TestPluginRejectsInvalidFailedAndOversizedResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		limit      int
	}{
		{"empty", "exit 0", 0},
		{"unversioned", "printf '{}'", 0},
		{"inconsistent", "printf '%s' '" + strings.Replace(cleanPluginJSON, "clean", "blocked", 1) + "'", 0},
		{"nonzero", "printf '%s' '" + cleanPluginJSON + "'; printf secret-diagnostic >&2; exit 4", 0},
		{"oversized", "printf '%s' '" + cleanPluginJSON + "'", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _, c := pluginFixture(t, tc.body)
			p := c.Plugins.Entries["example"]
			p.MaxOutputBytes = tc.limit
			c.Plugins.Entries["example"] = p
			savePluginConfig(t, path, c)
			code, out, err := execute("plugin", []string{"run", "--config-file", path, "--name", "example"}, "")
			if code != 30 || strings.Contains(out+err, "secret-diagnostic") {
				t.Fatalf("%d %s %s", code, out, err)
			}
		})
	}
}

func TestPluginTimeout(t *testing.T) {
	path, _, c := pluginFixture(t, "exec sleep 5\n")
	p := c.Plugins.Entries["example"]
	p.TimeoutSeconds = 1
	c.Plugins.Entries["example"] = p
	savePluginConfig(t, path, c)
	code, out, err := execute("plugin", []string{"run", "--config-file", path, "--name", "example"}, "")
	if code != 30 || !strings.Contains(out, "timed out") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}

func TestPluginConfigSwitchesPersistAndLegacyDefaultsOff(t *testing.T) {
	path, _, c := pluginFixture(t, "printf '%s' '"+cleanPluginJSON+"'")
	c.Plugins = pluginConfig{}
	savePluginConfig(t, path, c)
	loaded, _, e := loadAppConfig(path)
	if e != nil || loaded.Plugins.Enabled {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e := setAppConfigValue(path, "plugins.entries.example", `{"enabled":false,"path":"/opt/example"}`, &out); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"plugins.enabled", "plugins.entries.example.enabled"} {
		if e := setAppConfigValue(path, key, "true", &out); e != nil {
			t.Fatal(e)
		}
	}
	loaded, _, e = loadAppConfig(path)
	if e != nil || !loaded.Plugins.Enabled || !loaded.Plugins.Entries["example"].Enabled {
		t.Fatal(e)
	}
	if e := setAppConfigValue(path, "plugins.enabled", "false", &out); e != nil {
		t.Fatal(e)
	}
}

func TestPluginConfigRejectsUnsafeShapes(t *testing.T) {
	for _, p := range []pluginEntry{{Path: "relative"}, {Path: "/bin/true", TimeoutSeconds: 301}, {Path: "/bin/true", MaxOutputBytes: -1}} {
		if validatePluginConfig(pluginConfig{Entries: map[string]pluginEntry{"example": p}}) == nil {
			t.Fatal(p)
		}
	}
	if validatePluginConfig(pluginConfig{Entries: map[string]pluginEntry{"../bad": {Path: "/bin/true"}}}) == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestPluginConfigRejectsConflictingDocuments(t *testing.T) {
	path, _, _ := pluginFixture(t, "exit 99")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, append(data, []byte("\n---\nplugins:\n  enabled: false\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = loadAppConfig(path); e == nil {
		t.Fatal("accepted ambiguous config switches")
	}
}
