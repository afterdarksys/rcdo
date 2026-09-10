package toolkit

import (
	"fmt"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"sort"
	"strings"
)

type configIdentityBookmark struct {
	ArrayPath   string `json:"array_path"`
	Key         string `json:"key"`
	ValueSHA256 string `json:"value_sha256"`
	Suffix      string `json:"suffix"`
}

func configPartsPath(parts []configPathPart) string {
	path := "$"
	for _, part := range parts {
		if part.isIndex {
			path += fmt.Sprintf("[%d]", part.index)
		} else {
			path = appendConfigPath(path, part.key)
		}
	}
	return path
}
func makeIdentityBookmark(cursor, key string, entries map[string]configEntry) (configIdentityBookmark, error) {
	parts, err := parseConfigPath(cursor)
	if err != nil {
		return configIdentityBookmark{}, err
	}
	index := -1
	for i, p := range parts {
		if p.isIndex {
			if index >= 0 {
				return configIdentityBookmark{}, fmt.Errorf("identity bookmarks currently support one array level")
			}
			index = i
		}
	}
	if index < 0 || !operationLabel(key) || isSensitivePath(key) {
		return configIdentityBookmark{}, fmt.Errorf("select an array item and a nonsensitive identity key")
	}
	item := configPartsPath(parts[:index+1])
	value, ok := entries[appendConfigPath(item, key)]
	if !ok || !oneOf(value.Kind, "string", "number", "boolean") || value.Value == "" || strings.Contains(value.Value, "[REDACTED]") {
		return configIdentityBookmark{}, fmt.Errorf("identity key must identify a visible scalar")
	}
	mark := configIdentityBookmark{ArrayPath: configPartsPath(parts[:index]), Key: key, ValueSHA256: digestBytes([]byte(value.Kind + "\x00" + value.Value)), Suffix: strings.TrimPrefix(cursor, item)}
	if _, err := resolveIdentityBookmark(mark, entries); err != nil {
		return mark, err
	}
	return mark, nil
}
func resolveIdentityBookmark(mark configIdentityBookmark, entries map[string]configEntry) (string, error) {
	if len(mark.ValueSHA256) != 64 || isSensitivePath(mark.Key) {
		return "", fmt.Errorf("invalid identity bookmark")
	}
	base, err := parseConfigPath(mark.ArrayPath)
	if err != nil {
		return "", err
	}
	matches := []string{}
	for path := range entries {
		if !strings.HasPrefix(path, mark.ArrayPath+"[") {
			continue
		}
		parts, err := parseConfigPath(path)
		if err != nil || len(parts) != len(base)+1 || !parts[len(parts)-1].isIndex {
			continue
		}
		value, ok := entries[appendConfigPath(path, mark.Key)]
		if ok && digestBytes([]byte(value.Kind+"\x00"+value.Value)) == mark.ValueSHA256 {
			matches = append(matches, path+mark.Suffix)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("identity bookmark is missing or ambiguous; no position changed")
	}
	if _, ok := entries[matches[0]]; !ok {
		return "", fmt.Errorf("bookmarked child path no longer exists")
	}
	return matches[0], nil
}

type configReference struct {
	Expression string `json:"expression"`
	Target     string `json:"target,omitempty"`
	Line       int    `json:"line"`
	Status     string `json:"status"`
}

func configReferences(raw []byte, source, cursor string, entries map[string]configEntry) ([]configReference, error) {
	file, diags := hclsyntax.ParseConfig(raw, source, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("invalid HCL source")
	}
	var expression hclsyntax.Expression
	var walk func(*hclsyntax.Body, string)
	walk = func(body *hclsyntax.Body, path string) {
		for name, a := range body.Attributes {
			if appendConfigPath(path, name) == cursor {
				expression = a.Expr
			}
		}
		for _, b := range body.Blocks {
			p := appendConfigPath(path, b.Type)
			for _, label := range b.Labels {
				p = appendConfigPath(p, label)
			}
			walk(b.Body, p)
		}
	}
	walk(file.Body.(*hclsyntax.Body), "$")
	if expression == nil {
		return nil, fmt.Errorf("select an HCL expression before listing or following references")
	}
	result := []configReference{}
	seen := map[string]bool{}
	for _, traversal := range expression.Variables() {
		rng := traversal.SourceRange()
		text := auditText(string(raw[rng.Start.Byte:rng.End.Byte]))
		if seen[text] {
			continue
		}
		seen[text] = true
		root := traversal.RootName()
		path := "$"
		switch root {
		case "var":
			path = appendConfigPath(path, "variable")
		case "local":
			path = appendConfigPath(path, "locals")
		case "data", "module":
			path = appendConfigPath(path, root)
		case "each", "count", "path", "terraform", "self":
			path = ""
		default:
			path = appendConfigPath(appendConfigPath(path, "resource"), root)
		}
		target := ""
		if path != "" {
			for _, step := range traversal[1:] {
				a, ok := step.(hcl.TraverseAttr)
				if !ok {
					break
				}
				path = appendConfigPath(path, a.Name)
				if _, ok := entries[path]; ok {
					target = path
				}
			}
		}
		status := "unresolved in this file"
		if target != "" {
			status = "configuration declaration; runtime attribute value not evaluated"
		}
		result = append(result, configReference{Expression: text, Target: target, Line: rng.Start.Line, Status: status})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Expression < result[j].Expression })
	return result, nil
}
