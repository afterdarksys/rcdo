package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"git-tools/finding"
)

type pluginConfig struct {
	Enabled bool                   `json:"enabled" yaml:"enabled"`
	Entries map[string]pluginEntry `json:"entries" yaml:"entries"`
}
type pluginEntry struct {
	Enabled        bool   `json:"enabled" yaml:"enabled"`
	Path           string `json:"path" yaml:"path"`
	Description    string `json:"description,omitempty" yaml:"description,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	MaxOutputBytes int    `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
}
type pluginRequest struct {
	SchemaVersion string   `json:"schema_version"`
	Plugin        string   `json:"plugin"`
	Args          []string `json:"args"`
	Input         string   `json:"input"`
}

var pluginNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validatePluginConfig(c pluginConfig) error {
	if len(c.Entries) > 128 {
		return fmt.Errorf("at most 128 plugins may be registered")
	}
	for name, p := range c.Entries {
		if !pluginNamePattern.MatchString(name) || !filepath.IsAbs(p.Path) || !operationLabel(p.Path) {
			return fmt.Errorf("plugin names require lowercase letters/digits/hyphens and an absolute executable path")
		}
		if p.Description != "" && !operationLabel(p.Description) {
			return fmt.Errorf("invalid plugin description")
		}
		if p.TimeoutSeconds < 0 || p.TimeoutSeconds > 300 || p.MaxOutputBytes < 0 || p.MaxOutputBytes > 16<<20 {
			return fmt.Errorf("plugin limits: timeout 1..300 seconds and output 1..16777216 bytes; zero uses defaults")
		}
	}
	return nil
}

func runPlugin(args []string, configPath string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || oneOf(args[0], "help", "--help", "-help", "-h") {
		fmt.Fprintln(stdout, "plugin: explicitly registered executable extensions\nUsage: rcdo plugin list [--format text|json]\nUsage: rcdo plugin run --name NAME [--input FILE] [--format text|json] [-- ARGS]\nSelect configuration with --config-file PATH. Plugins default to disabled.\nUse config set for plugins.enabled and plugins.entries.NAME.enabled.")
		return nil
	}
	mode, args := args[0], args[1:]
	if !oneOf(mode, "list", "run") {
		return fmt.Errorf("plugin requires list or run")
	}
	fs := flag.NewFlagSet("plugin "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o commonOptions
	addCommonFlags(fs, &o)
	name := fs.String("name", "", "registered plugin name")
	setAccessibleUsage(fs, "plugin "+mode, stderr)
	if e := fs.Parse(args); e != nil {
		return e
	}
	if !oneOf(o.format, "text", "json", "github", "sarif") || o.width < 40 || o.policy != "" {
		return fmt.Errorf("invalid plugin rendering options; plugin reports cannot be suppressed by policy")
	}
	if mode == "list" && (len(fs.Args()) > 0 || *name != "" || !oneOf(o.format, "text", "json")) {
		return fmt.Errorf("plugin list accepts text or json and no plugin arguments")
	}
	if configPath == "" {
		var e error
		configPath, e = defaultAppConfigPath()
		if e != nil {
			return e
		}
	}
	config, found, e := loadAppConfig(configPath)
	if e != nil {
		return e
	}
	if !found {
		return fmt.Errorf("plugin configuration missing; run rcdo config init")
	}
	if mode == "list" {
		if o.format == "json" {
			return json.NewEncoder(stdout).Encode(config.Plugins)
		}
		var b bytes.Buffer
		fmt.Fprintf(&b, "PLUGIN SUPPORT ENABLED: %t\n", config.Plugins.Enabled)
		names := []string{}
		for n := range config.Plugins.Entries {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) == 0 {
			fmt.Fprintln(&b, "No plugins registered.")
		}
		for _, n := range names {
			p := config.Plugins.Entries[n]
			fmt.Fprintf(&b, "Plugin: %s\nEnabled: %t\nEffective enabled: %t\nExecutable: %s\nDescription: %s\n", n, p.Enabled, config.Plugins.Enabled && p.Enabled, p.Path, p.Description)
		}
		var rendered bytes.Buffer
		for _, line := range strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n") {
			writeWrapped(&rendered, line, o.width)
		}
		_, e := stdout.Write(rendered.Bytes())
		return e
	}
	if !pluginNamePattern.MatchString(*name) {
		return fmt.Errorf("plugin run requires --name")
	}
	if !config.Plugins.Enabled {
		return fmt.Errorf("plugin support is disabled in configuration")
	}
	p, ok := config.Plugins.Entries[*name]
	if !ok {
		return fmt.Errorf("plugin is not registered")
	}
	if !p.Enabled {
		return fmt.Errorf("plugin is disabled in configuration")
	}
	info, e := os.Stat(p.Path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("plugin executable is unavailable or not executable")
	}
	var input io.Reader = stdin
	if o.input != "-" {
		f, e := os.Open(o.input)
		if e != nil {
			return e
		}
		defer f.Close()
		input = f
	}
	data, e := io.ReadAll(io.LimitReader(input, (1<<20)+1))
	if e != nil {
		return e
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("plugin input exceeds 1 MiB")
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("plugin input must be UTF-8 text")
	}
	pluginArgs := append([]string{}, fs.Args()...)
	request, e := json.Marshal(pluginRequest{SchemaVersion: "1", Plugin: *name, Args: pluginArgs, Input: string(data)})
	if e != nil {
		return e
	}
	if len(request) > 8<<20 {
		return fmt.Errorf("plugin request exceeds 8 MiB")
	}
	timeout := p.TimeoutSeconds
	if timeout == 0 {
		timeout = 30
	}
	limit := p.MaxOutputBytes
	if limit == 0 {
		limit = 1 << 20
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, p.Path, "rcdo-plugin-v1")
	command.WaitDelay = time.Second
	command.Stdin = bytes.NewReader(request)
	output := limitedCommandBuffer{limit: limit}
	diagnostics := limitedCommandBuffer{limit: 64 << 10}
	command.Stdout = &output
	command.Stderr = &diagnostics
	processErr := command.Run()
	r := finding.Report{}
	switch {
	case ctx.Err() != nil:
		r.IncompleteChecks = []string{"Plugin timed out; execution outcome is unknown"}
	case output.exceeded || diagnostics.exceeded:
		r.IncompleteChecks = []string{"Plugin output exceeded configured limits"}
	case processErr != nil:
		r.IncompleteChecks = []string{"Plugin process failed; diagnostics withheld"}
	default:
		r, e = decodeSessionReport(output.Bytes())
		if e != nil {
			r = finding.Report{IncompleteChecks: []string{"Plugin returned an invalid or inconsistent report"}}
		}
	}
	// A plugin cannot claim to be a built-in checker or carry another tool's provenance.
	r.Provenance = nil
	for i := range r.Findings {
		r.Findings[i].ID = *name + "/" + r.Findings[i].ID
	}
	r.CompletedChecks = append(r.CompletedChecks, "Plugin source: "+*name+"; external executable, protocol version 1")
	return emitReportOptions(stdout, o, r)
}
