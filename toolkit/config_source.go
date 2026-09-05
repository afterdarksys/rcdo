package toolkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
)

func readConfigSource(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open configuration")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
	if err != nil || len(data) > 16<<20 {
		return nil, fmt.Errorf("configuration exceeds 16 MiB or cannot be read")
	}
	return data, nil
}

// Reject ambiguity before flattening can silently merge duplicate keys or follow
// cyclic aliases. Errors deliberately exclude configuration values.
func validateConfigDocument(syntax string, data []byte) error {
	if syntax == "json" {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value func(int) error
		value = func(depth int) error {
			if depth > 128 {
				return fmt.Errorf("configuration nesting exceeds 128")
			}
			token, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON document")
			}
			delimiter, ok := token.(json.Delim)
			if !ok {
				return nil
			}
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return fmt.Errorf("invalid JSON key")
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return fmt.Errorf("duplicate or invalid JSON key")
					}
					seen[name] = true
					if err := value(depth + 1); err != nil {
						return err
					}
				}
				end, err := decoder.Token()
				if err != nil || end != json.Delim('}') {
					return fmt.Errorf("invalid JSON object")
				}
			case '[':
				for decoder.More() {
					if err := value(depth + 1); err != nil {
						return err
					}
				}
				end, err := decoder.Token()
				if err != nil || end != json.Delim(']') {
					return fmt.Errorf("invalid JSON array")
				}
			default:
				return fmt.Errorf("invalid JSON delimiter")
			}
			return nil
		}
		if err := value(0); err != nil {
			return err
		}
		if _, err := decoder.Token(); err != io.EOF {
			return fmt.Errorf("configuration must contain one JSON document")
		}
	}
	if syntax == "yaml" {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		var document yaml.Node
		if decoder.Decode(&document) != nil || decoder.Decode(new(yaml.Node)) != io.EOF {
			return fmt.Errorf("configuration must contain one YAML document")
		}
		ancestors := map[*yaml.Node]bool{}
		visits := 0
		var walk func(*yaml.Node, int) error
		walk = func(node *yaml.Node, depth int) error {
			visits++
			if visits > 100000 {
				return fmt.Errorf("YAML expansion exceeds 100000 nodes")
			}
			if node == nil || depth > 128 || ancestors[node] {
				return fmt.Errorf("cyclic alias or excessive YAML nesting")
			}
			ancestors[node] = true
			defer delete(ancestors, node)
			if node.Kind == yaml.AliasNode {
				return walk(node.Alias, depth+1)
			}
			if node.Kind == yaml.MappingNode {
				seen := map[string]bool{}
				for i := 0; i+1 < len(node.Content); i += 2 {
					key := node.Content[i]
					if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
						return fmt.Errorf("YAML requires unique string mapping keys")
					}
					seen[key.Value] = true
				}
			}
			for _, child := range node.Content {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		return walk(&document, 0)
	}
	return nil
}
