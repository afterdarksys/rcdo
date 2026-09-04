package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"gopkg.in/yaml.v3"
)

type configEntry struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
	Line  int    `json:"line,omitempty"`
}

type configExplanation struct {
	SchemaVersion string        `json:"schema_version"`
	SourceFormat  string        `json:"source_format"`
	Entries       []configEntry `json:"entries"`
}

var (
	configIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	sensitiveKey     = regexp.MustCompile(`(?i)(^|[-_.])(password|passwd|secret|token|api[-_]?key|private[-_]?key|client[-_]?secret|access[-_]?key|credential)s?($|[-_.])`)
	hclHint          = regexp.MustCompile(`(?m)^\s*(resource|data|module|provider|terraform|variable|output|locals|moved|import|check)\s+("|\{)`)
	tomlHint         = regexp.MustCompile(`(?m)^\s*(\[\[?[^]]+\]\]?|[A-Za-z0-9_.-]+\s*=)`)
)

func runConfigExplain(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var input, outputFormat, syntax, pathFilter string
	var width int
	var showSecrets, showValues bool
	fs := flag.NewFlagSet("config-explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&input, "input", "-", "configuration file; use - for standard input")
	fs.StringVar(&outputFormat, "format", "text", "output format: text or json")
	fs.StringVar(&syntax, "syntax", "auto", "input syntax: auto, json, yaml, toml, or hcl")
	fs.StringVar(&pathFilter, "path", "", "show only this path and its descendants")
	fs.IntVar(&width, "width", 100, "maximum text line width; minimum 40")
	fs.BoolVar(&showValues, "values", true, "include scalar values")
	fs.BoolVar(&showSecrets, "show-secrets", false, "show values whose paths look sensitive")
	setAccessibleUsage(fs, "config-explain", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if outputFormat != "text" && outputFormat != "json" {
		return fmt.Errorf("unknown format %q; expected text or json", outputFormat)
	}
	if width < 40 {
		return fmt.Errorf("--width must be at least 40")
	}
	data, err := readInput(input, stdin)
	if err != nil {
		return err
	}
	resolved, err := detectConfigSyntax(syntax, input, data)
	if err != nil {
		return err
	}
	entries, err := explainConfig(resolved, input, data, showValues, showSecrets)
	if err != nil {
		return err
	}
	if pathFilter != "" {
		entries = filterConfigEntries(entries, pathFilter)
		if len(entries) == 0 {
			return fmt.Errorf("path %q was not found", pathFilter)
		}
	}

	explanation := configExplanation{SchemaVersion: "1", SourceFormat: resolved, Entries: entries}
	if outputFormat == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(explanation)
	}
	fmt.Fprintln(stdout, "CONFIGURATION EXPLANATION")
	fmt.Fprintf(stdout, "Format: %s\n", resolved)
	fmt.Fprintf(stdout, "Entries: %d\n", len(entries))
	for index, entry := range entries {
		fmt.Fprintf(stdout, "\nEntry %d\n", index+1)
		writeWrapped(stdout, "Path: "+entry.Path, width)
		fmt.Fprintf(stdout, "Kind: %s\n", entry.Kind)
		if entry.Line > 0 {
			fmt.Fprintf(stdout, "Line: %d\n", entry.Line)
		}
		if entry.Value != "" {
			writeWrapped(stdout, "Value: "+entry.Value, width)
		}
	}
	return nil
}

func detectConfigSyntax(requested, input string, data []byte) (string, error) {
	requested = strings.ToLower(requested)
	valid := map[string]bool{"auto": true, "json": true, "yaml": true, "toml": true, "hcl": true}
	if !valid[requested] {
		return "", fmt.Errorf("unknown syntax %q; expected auto, json, yaml, toml, or hcl", requested)
	}
	if requested != "auto" {
		return requested, nil
	}
	switch strings.ToLower(filepath.Ext(input)) {
	case ".json", ".tf.json":
		return "json", nil
	case ".yaml", ".yml":
		return "yaml", nil
	case ".toml":
		return "toml", nil
	case ".hcl", ".tf", ".tfvars", ".tofu":
		return "hcl", nil
	}
	var jsonValue any
	if json.Unmarshal(data, &jsonValue) == nil {
		return "json", nil
	}
	if hclHint.Match(data) {
		return "hcl", nil
	}
	if tomlHint.Match(data) {
		return "toml", nil
	}
	return "yaml", nil
}

func explainConfig(syntax, input string, data []byte, showValues, showSecrets bool) ([]configEntry, error) {
	switch syntax {
	case "json":
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
		var entries []configEntry
		flattenConfigValue(&entries, "$", value, showValues, showSecrets)
		return entries, nil
	case "yaml":
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parse YAML: %w", err)
		}
		var entries []configEntry
		if len(document.Content) == 0 {
			return nil, fmt.Errorf("parse YAML: document is empty")
		}
		flattenYAMLNode(&entries, "$", document.Content[0], showValues, showSecrets)
		return entries, nil
	case "toml":
		var value map[string]any
		if _, err := toml.Decode(string(data), &value); err != nil {
			return nil, fmt.Errorf("parse TOML: %w", err)
		}
		var entries []configEntry
		flattenConfigValue(&entries, "$", value, showValues, showSecrets)
		return entries, nil
	case "hcl":
		name := input
		if name == "-" {
			name = "standard-input.hcl"
		}
		file, diagnostics := hclsyntax.ParseConfig(data, name, hcl.Pos{Line: 1, Column: 1})
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("parse HCL: %s", diagnostics.Error())
		}
		var entries []configEntry
		flattenHCLBody(&entries, "$", file.Body.(*hclsyntax.Body), data, showValues, showSecrets)
		return entries, nil
	default:
		return nil, fmt.Errorf("unsupported syntax %q", syntax)
	}
}

func flattenConfigValue(entries *[]configEntry, path string, value any, showValues, showSecrets bool) {
	switch typed := value.(type) {
	case map[string]any:
		*entries = append(*entries, configEntry{Path: path, Kind: "object", Value: countValue(len(typed), "key")})
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenConfigValue(entries, appendConfigPath(path, key), typed[key], showValues, showSecrets)
		}
	case []any:
		*entries = append(*entries, configEntry{Path: path, Kind: "array", Value: countValue(len(typed), "item")})
		for index, item := range typed {
			flattenConfigValue(entries, fmt.Sprintf("%s[%d]", path, index), item, showValues, showSecrets)
		}
	default:
		entry := configEntry{Path: path, Kind: configValueKind(typed)}
		if showValues {
			entry.Value = renderConfigValue(path, typed, showSecrets)
		}
		*entries = append(*entries, entry)
	}
}

func flattenYAMLNode(entries *[]configEntry, path string, node *yaml.Node, showValues, showSecrets bool) {
	if node.Kind == yaml.AliasNode {
		flattenYAMLNode(entries, path, node.Alias, showValues, showSecrets)
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		*entries = append(*entries, configEntry{Path: path, Kind: "object", Value: countValue(len(node.Content)/2, "key"), Line: node.Line})
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			entryIndex := len(*entries)
			flattenYAMLNode(entries, appendConfigPath(path, key.Value), value, showValues, showSecrets)
			if len(*entries) > entryIndex {
				(*entries)[entryIndex].Line = key.Line
			}
		}
	case yaml.SequenceNode:
		*entries = append(*entries, configEntry{Path: path, Kind: "array", Value: countValue(len(node.Content), "item"), Line: node.Line})
		for index, item := range node.Content {
			flattenYAMLNode(entries, fmt.Sprintf("%s[%d]", path, index), item, showValues, showSecrets)
		}
	case yaml.ScalarNode:
		entry := configEntry{Path: path, Kind: yamlValueKind(node), Line: node.Line}
		if showValues {
			if isSensitivePath(path) && !showSecrets {
				entry.Value = "[REDACTED]"
			} else if node.Tag == "!!str" || node.Tag == "" {
				entry.Value = strconv.Quote(node.Value)
			} else {
				entry.Value = node.Value
			}
			if node.Tag == "!!null" {
				entry.Value = "null"
			}
		}
		*entries = append(*entries, entry)
	}
}

func flattenHCLBody(entries *[]configEntry, path string, body *hclsyntax.Body, source []byte, showValues, showSecrets bool) {
	*entries = append(*entries, configEntry{Path: path, Kind: "body", Value: countValue(len(body.Attributes)+len(body.Blocks), "member"), Line: body.Range().Start.Line})
	attributes := make([]*hclsyntax.Attribute, 0, len(body.Attributes))
	for _, attribute := range body.Attributes {
		attributes = append(attributes, attribute)
	}
	sort.Slice(attributes, func(i, j int) bool { return attributes[i].Range().Start.Byte < attributes[j].Range().Start.Byte })
	for _, attribute := range attributes {
		attributePath := appendConfigPath(path, attribute.Name)
		entry := configEntry{Path: attributePath, Kind: "expression", Line: attribute.Range().Start.Line}
		if showValues {
			if isSensitivePath(attributePath) && !showSecrets {
				entry.Value = "[REDACTED]"
			} else {
				rangeValue := attribute.Expr.Range()
				entry.Value = strings.Join(strings.Fields(string(source[rangeValue.Start.Byte:rangeValue.End.Byte])), " ")
			}
		}
		*entries = append(*entries, entry)
	}
	for _, block := range body.Blocks {
		blockPath := appendConfigPath(path, block.Type)
		for _, label := range block.Labels {
			blockPath = appendConfigPath(blockPath, label)
		}
		flattenHCLBody(entries, blockPath, block.Body, source, showValues, showSecrets)
	}
}

func filterConfigEntries(entries []configEntry, filter string) []configEntry {
	filter = normalizeConfigPath(filter)
	var filtered []configEntry
	for _, entry := range entries {
		if configPathWithin(entry.Path, filter) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func appendConfigPath(path, key string) string {
	if configIdentifier.MatchString(key) {
		return path + "." + key
	}
	encoded, _ := json.Marshal(key)
	return path + "[" + string(encoded) + "]"
}

func configValueKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number, int64, float64:
		return "number"
	case string:
		return "string"
	default:
		return "scalar"
	}
}

func yamlValueKind(node *yaml.Node) string {
	switch node.Tag {
	case "!!null":
		return "null"
	case "!!bool":
		return "boolean"
	case "!!int", "!!float":
		return "number"
	default:
		return "string"
	}
}

func renderConfigValue(path string, value any, showSecrets bool) string {
	if isSensitivePath(path) && !showSecrets {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func isSensitivePath(path string) bool {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '.' || r == '[' || r == ']' || r == '"'
	})
	for _, part := range parts {
		if sensitiveKey.MatchString(part) {
			return true
		}
	}
	return false
}

func countValue(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", count, singular)
}
