package toolkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	"gopkg.in/yaml.v3"
)

type configPathPart struct {
	key     string
	index   int
	isIndex bool
}

func runConfigMutate(mode string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var input, path, syntax, rawValue, expectedHash, expectedValue string
	var write, stringValue, showSecrets bool
	fs := flag.NewFlagSet("config-"+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&input, "input", "", "configuration file to change")
	fs.StringVar(&path, "path", "", "complete or root-relative configuration path")
	fs.StringVar(&syntax, "syntax", "auto", "input syntax: auto, json, yaml, toml, or hcl")
	if mode == "set" {
		fs.StringVar(&rawValue, "value", "", "new value as JSON; use --string for literal text")
		fs.BoolVar(&stringValue, "string", false, "treat --value as a literal string")
	}
	fs.StringVar(&expectedValue, "expect-value", "", "assert the existing scalar value as JSON")
	fs.StringVar(&expectedHash, "expect-sha256", "", "source SHA-256 from the reviewed preview; required for --write")
	fs.BoolVar(&write, "write", false, "atomically replace the input file; default is preview")
	fs.BoolVar(&showSecrets, "show-secrets", false, "show sensitive values in the preview")
	setAccessibleUsage(fs, "config-"+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if input == "" || path == "" {
		return fmt.Errorf("--input and --path are required")
	}
	if mode == "set" && rawValue == "" && !stringValue {
		return fmt.Errorf("--value is required")
	}
	data, err := readConfigSource(input)
	if err != nil {
		return fmt.Errorf("read input %q: %w", input, err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	check := func() error {
		info, err := os.Lstat(input)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("write requires a regular file, not a symlink")
		}
		current, err := readConfigSource(input)
		if err != nil {
			return err
		}
		if expectedHash == "" || fmt.Sprintf("%x", sha256.Sum256(current)) != expectedHash {
			return fmt.Errorf("source version differs or --expect-sha256 is missing; preview again before writing")
		}
		return nil
	}
	if expectedHash != "" && expectedHash != digest {
		return fmt.Errorf("source version differs; preview again")
	}
	if write {
		if err := check(); err != nil {
			return err
		}
	}
	resolved, err := detectConfigSyntax(syntax, input, data)
	if err != nil {
		return err
	}
	if err := validateConfigDocument(resolved, data); err != nil {
		return err
	}
	parts, err := parseConfigPath(path)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("the document root cannot be changed")
	}
	if expectedValue != "" {
		var value any
		decoder := json.NewDecoder(strings.NewReader(expectedValue))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return fmt.Errorf("invalid --expect-value JSON")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return fmt.Errorf("expected value must be one JSON scalar")
		}
		switch value.(type) {
		case map[string]any, []any:
			return fmt.Errorf("expected value must be a scalar")
		}
		canonical := "$"
		for _, part := range parts {
			if part.isIndex {
				canonical += fmt.Sprintf("[%d]", part.index)
			} else {
				canonical = appendConfigPath(canonical, part.key)
			}
		}
		entries, err := explainConfig(resolved, input, data, true, true)
		if err != nil {
			return err
		}
		encoded := []byte(renderConfigValue(canonical, value, true))
		matched := false
		for _, entry := range entries {
			if entry.Path == canonical && entry.Value == string(encoded) {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("expected value mismatch or unsupported scalar representation; no file written")
		}
	}
	before, err := explainConfig(resolved, input, data, true, true)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, entry := range before {
		if seen[entry.Path] {
			return fmt.Errorf("ambiguous duplicate configuration path")
		}
		seen[entry.Path] = true
	}
	var result []byte
	if resolved == "hcl" {
		result, err = mutateHCL(data, input, parts, mode, rawValue, stringValue)
	} else {
		root, decodeErr := decodeMutableConfig(resolved, data)
		if decodeErr != nil {
			return decodeErr
		}
		if mode == "set" {
			var value any
			if stringValue {
				value = rawValue
			} else if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
				return fmt.Errorf("parse --value as JSON: %w; use --string for literal text", err)
			}
			root, err = setConfigPath(root, parts, value)
		} else {
			root, err = removeConfigPath(root, parts)
		}
		if err == nil {
			result, err = encodeMutableConfig(resolved, root)
		}
	}
	if err != nil {
		return err
	}
	beforeEntries, err := explainConfig(resolved, input, data, true, true)
	if err != nil {
		return err
	}
	if err := validateConfigDocument(resolved, result); err != nil {
		return err
	}
	afterEntries, err := explainConfig(resolved, input, result, true, true)
	if err != nil {
		return err
	}
	changes := compareConfigEntries(beforeEntries, afterEntries)
	for i := range changes {
		prepareConfigChange(&changes[i], true, showSecrets)
	}
	summary := map[string]int{"added": 0, "removed": 0, "changed": 0}
	for _, change := range changes {
		summary[change.Change]++
	}
	if len(changes) == 0 {
		fmt.Fprintln(stdout, "MUTATION PREVIEW\nResult: UNCHANGED\nNo file was written.")
		return nil
	}
	if !write {
		fmt.Fprintln(stdout, "MUTATION PREVIEW")
		fmt.Fprintf(stdout, "Source SHA-256: %s\n", digest)
		renderConfigDiffText(stdout, configDiff{SchemaVersion: "1", BeforeFormat: resolved, AfterFormat: resolved, Changed: true, Summary: summary, Changes: changes}, 100)
		fmt.Fprintln(stdout, "File written: no")
		fmt.Fprintln(stdout, "Rerun with --write --expect-sha256 HASH using the Source SHA-256 above.")
		return nil
	}
	return atomicReplaceChecked(input, result, stdout, check)
}

func mutateHCL(data []byte, name string, parts []configPathPart, mode, rawValue string, stringValue bool) ([]byte, error) {
	for _, part := range parts {
		if part.isIndex {
			return nil, fmt.Errorf("HCL mutation paths address blocks and attributes, not expression indexes")
		}
	}
	file, diagnostics := hclwrite.ParseConfig(data, name, hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parse HCL: %s", diagnostics.Error())
	}
	body := file.Body()
	remaining := parts
	for len(remaining) > 1 {
		matched := false
		for _, block := range body.Blocks() {
			signature := append([]string{block.Type()}, block.Labels()...)
			if len(remaining) <= len(signature) || !configPartsMatch(remaining, signature) {
				continue
			}
			body = block.Body()
			remaining = remaining[len(signature):]
			matched = true
			break
		}
		if !matched {
			break
		}
	}
	if len(remaining) != 1 {
		return nil, fmt.Errorf("HCL block path was not found before attribute %q", remaining[len(remaining)-1].key)
	}
	attribute := remaining[0].key
	if mode == "remove" {
		if body.GetAttribute(attribute) == nil {
			return nil, fmt.Errorf("HCL attribute %q was not found", attribute)
		}
		body.RemoveAttribute(attribute)
	} else {
		var value cty.Value
		if stringValue {
			value = cty.StringVal(rawValue)
		} else {
			var simple ctyjson.SimpleJSONValue
			if err := json.Unmarshal([]byte(rawValue), &simple); err != nil {
				return nil, fmt.Errorf("parse --value as JSON: %w; use --string for literal text", err)
			}
			value = simple.Value
		}
		body.SetAttributeValue(attribute, value)
	}
	return hclwrite.Format(file.Bytes()), nil
}

func configPartsMatch(parts []configPathPart, values []string) bool {
	if len(parts) < len(values) {
		return false
	}
	for index, value := range values {
		if parts[index].isIndex || parts[index].key != value {
			return false
		}
	}
	return true
}

func decodeMutableConfig(syntax string, data []byte) (any, error) {
	var value any
	switch syntax {
	case "json":
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("parse JSON: %w", err)
		}
	case "yaml":
		if err := yaml.Unmarshal(data, &value); err != nil {
			return nil, fmt.Errorf("parse YAML: %w", err)
		}
	case "toml":
		var object map[string]any
		if _, err := toml.Decode(string(data), &object); err != nil {
			return nil, fmt.Errorf("parse TOML: %w", err)
		}
		value = object
	default:
		return nil, fmt.Errorf("config mutation supports json, yaml, and toml; got %s", syntax)
	}
	return normalizeConfigMaps(value), nil
}

func normalizeConfigMaps(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeConfigMaps(child)
		}
		return typed
	case map[any]any:
		result := map[string]any{}
		for key, child := range typed {
			result[fmt.Sprint(key)] = normalizeConfigMaps(child)
		}
		return result
	case []any:
		for i := range typed {
			typed[i] = normalizeConfigMaps(typed[i])
		}
		return typed
	default:
		return value
	}
}

func encodeMutableConfig(syntax string, value any) ([]byte, error) {
	switch syntax {
	case "json":
		data, err := json.MarshalIndent(value, "", "  ")
		return append(data, '\n'), err
	case "yaml":
		return yaml.Marshal(value)
	case "toml":
		var output bytes.Buffer
		err := toml.NewEncoder(&output).Encode(value)
		return output.Bytes(), err
	default:
		return nil, fmt.Errorf("unsupported mutable syntax %s", syntax)
	}
}

func parseConfigPath(path string) ([]configPathPart, error) {
	path = strings.TrimSpace(path)
	if path == "$" {
		return nil, nil
	}
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	var parts []configPathPart
	for len(path) > 0 {
		if path[0] == '.' {
			path = path[1:]
			continue
		}
		if path[0] == '[' {
			if strings.HasPrefix(path, `["`) {
				decoder := json.NewDecoder(strings.NewReader(path[1:]))
				var key string
				if decoder.Decode(&key) != nil {
					return nil, fmt.Errorf("invalid quoted path key")
				}
				end := 1 + int(decoder.InputOffset())
				if end >= len(path) || path[end] != ']' {
					return nil, fmt.Errorf("invalid quoted path boundary")
				}
				parts = append(parts, configPathPart{key: key})
				path = path[end+1:]
				continue
			}
			end := strings.IndexByte(path, ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid path: missing ]")
			}
			token := path[1:end]
			if strings.HasPrefix(token, `"`) {
				var key string
				if err := json.Unmarshal([]byte(token), &key); err != nil {
					return nil, fmt.Errorf("invalid quoted path key: %w", err)
				}
				parts = append(parts, configPathPart{key: key})
			} else {
				index, err := strconv.Atoi(token)
				if err != nil || index < 0 {
					return nil, fmt.Errorf("invalid array index %q", token)
				}
				parts = append(parts, configPathPart{index: index, isIndex: true})
			}
			path = path[end+1:]
			continue
		}
		end := len(path)
		if dot := strings.IndexAny(path, ".[ "); dot >= 0 {
			end = dot
		}
		key := path[:end]
		if key == "" {
			return nil, fmt.Errorf("invalid empty path key")
		}
		parts = append(parts, configPathPart{key: key})
		path = path[end:]
	}
	return parts, nil
}

func setConfigPath(current any, parts []configPathPart, value any) (any, error) {
	part := parts[0]
	if part.isIndex {
		list, ok := current.([]any)
		if !ok {
			return nil, fmt.Errorf("path expects array index %d", part.index)
		}
		if part.index > len(list) {
			return nil, fmt.Errorf("array index %d is beyond append position %d", part.index, len(list))
		}
		if len(parts) == 1 {
			if part.index == len(list) {
				return append(list, value), nil
			}
			list[part.index] = value
			return list, nil
		}
		if part.index == len(list) {
			return nil, fmt.Errorf("cannot create nested value beyond array end")
		}
		child, err := setConfigPath(list[part.index], parts[1:], value)
		if err != nil {
			return nil, err
		}
		list[part.index] = child
		return list, nil
	}
	object, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("path expects object key %q", part.key)
	}
	if len(parts) == 1 {
		object[part.key] = value
		return object, nil
	}
	child, exists := object[part.key]
	if !exists {
		if parts[1].isIndex {
			return nil, fmt.Errorf("cannot create missing array %q", part.key)
		}
		child = map[string]any{}
	}
	updated, err := setConfigPath(child, parts[1:], value)
	if err != nil {
		return nil, err
	}
	object[part.key] = updated
	return object, nil
}

func removeConfigPath(current any, parts []configPathPart) (any, error) {
	part := parts[0]
	if part.isIndex {
		list, ok := current.([]any)
		if !ok || part.index >= len(list) {
			return nil, fmt.Errorf("array index %d was not found", part.index)
		}
		if len(parts) == 1 {
			return append(list[:part.index], list[part.index+1:]...), nil
		}
		child, err := removeConfigPath(list[part.index], parts[1:])
		if err != nil {
			return nil, err
		}
		list[part.index] = child
		return list, nil
	}
	object, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("path expects object key %q", part.key)
	}
	child, exists := object[part.key]
	if !exists {
		return nil, fmt.Errorf("path %q was not found", part.key)
	}
	if len(parts) == 1 {
		delete(object, part.key)
		return object, nil
	}
	updated, err := removeConfigPath(child, parts[1:])
	if err != nil {
		return nil, err
	}
	object[part.key] = updated
	return object, nil
}
