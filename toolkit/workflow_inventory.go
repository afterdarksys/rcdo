package toolkit

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
)

func workflowEqual(a, b any) bool {
	if workflowValueType(a) != workflowValueType(b) {
		return false
	}
	switch x := a.(type) {
	case json.Number:
		left, lok := new(big.Rat).SetString(string(x))
		right, rok := new(big.Rat).SetString(string(b.(json.Number)))
		return lok && rok && left.Cmp(right) == 0
	case map[string]any:
		y := b.(map[string]any)
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			other, ok := y[k]
			if !ok || !workflowEqual(v, other) {
				return false
			}
		}
		return true
	case []any:
		y := b.([]any)
		if len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !workflowEqual(v, y[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// Consume saved ansible-inventory --list output, including dynamic plugin
// results, without running the plugin or exposing host values.
func workflowResolved(data []byte) (map[string]map[string]any, map[string]map[string]bool, error) {
	var raw map[string]json.RawMessage
	if workflowJSON(data, &raw) != nil {
		return nil, nil, fmt.Errorf("invalid resolved inventory")
	}
	var meta struct {
		Hostvars map[string]map[string]any `json:"hostvars"`
		Profile  string                    `json:"profile,omitempty"`
	}
	if workflowJSON(raw["_meta"], &meta) != nil || meta.Hostvars == nil {
		return nil, nil, fmt.Errorf("resolved inventory hostvars required")
	}
	type group struct {
		Hosts    []string       `json:"hosts"`
		Children []string       `json:"children"`
		Vars     map[string]any `json:"vars"`
	}
	parsed := map[string]group{}
	for name, b := range raw {
		if name != "_meta" {
			var g group
			if workflowJSON(b, &g) != nil || len(g.Vars) > 0 {
				return nil, nil, fmt.Errorf("resolved snapshot still contains unresolved group variables")
			}
			parsed[name] = g
		}
	}
	groups := map[string]map[string]bool{}
	active := map[string]bool{}
	var visit func(string, int) (map[string]bool, error)
	visit = func(name string, depth int) (map[string]bool, error) {
		if active[name] || depth > 32 {
			return nil, fmt.Errorf("inventory group cycle or depth limit")
		}
		if members, ok := groups[name]; ok {
			return members, nil
		}
		g, ok := parsed[name]
		if !ok && name == "ungrouped" {
			g = group{}
			ok = true
		}
		if !ok {
			return nil, fmt.Errorf("missing inventory child group")
		}
		active[name] = true
		defer delete(active, name)
		members := map[string]bool{}
		for _, h := range g.Hosts {
			if !operationLabel(h) || members[h] {
				return nil, fmt.Errorf("invalid or duplicate inventory host")
			}
			members[h] = true
			if meta.Hostvars[h] == nil {
				meta.Hostvars[h] = map[string]any{}
			}
		}
		for _, child := range g.Children {
			nested, e := visit(child, depth+1)
			if e != nil {
				return nil, e
			}
			for h := range nested {
				members[h] = true
			}
		}
		groups[name] = members
		return members, nil
	}
	for name := range parsed {
		if _, e := visit(name, 0); e != nil {
			return nil, nil, e
		}
	}
	if groups["all"] == nil {
		return nil, nil, fmt.Errorf("resolved all group required")
	}
	for h := range meta.Hostvars {
		if !groups["all"][h] {
			return nil, nil, fmt.Errorf("host metadata outside all group")
		}
	}
	return meta.Hostvars, groups, nil
}
