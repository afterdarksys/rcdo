package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"git-tools/finding"
)

type stateWalkNode struct {
	ID       string   `json:"id"`
	Parent   string   `json:"parent,omitempty"`
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	Value    any      `json:"value,omitempty"`
	Children []string `json:"children"`
}
type stateWalkSession struct {
	SchemaVersion string            `json:"schema_version"`
	Source        boundArtifact     `json:"source"`
	Cursor        string            `json:"cursor"`
	Bookmarks     map[string]string `json:"bookmarks"`
}
type showStateResource struct {
	Address   string         `json:"address"`
	Values    map[string]any `json:"values"`
	Sensitive any            `json:"sensitive_values"`
}
type showStateModule struct {
	Address   string              `json:"address"`
	Resources []showStateResource `json:"resources"`
	Children  []showStateModule   `json:"child_modules"`
}

func statePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func buildStateTree(data []byte) (map[string]*stateWalkNode, []string, error) {
	nodes := map[string]*stateWalkNode{}
	gaps := []string{}
	if len(data) > 16<<20 || validateConfigDocument("json", data) != nil {
		return nodes, gaps, fmt.Errorf("invalid or oversized show JSON")
	}
	var shape map[string]json.RawMessage
	_ = json.Unmarshal(data, &shape)
	for _, key := range []string{"planned_values", "resource_changes", "prior_state"} {
		if _, ok := shape[key]; ok {
			return nodes, gaps, fmt.Errorf("state-walk requires state show JSON, not plan JSON")
		}
	}
	var s struct {
		Format string `json:"format_version"`
		Values *struct {
			Root    showStateModule `json:"root_module"`
			Outputs map[string]struct {
				Value     any   `json:"value"`
				Sensitive *bool `json:"sensitive"`
			} `json:"outputs"`
		} `json:"values"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&s) != nil || strings.Split(s.Format, ".")[0] != "1" || s.Values == nil {
		return nodes, gaps, fmt.Errorf("expected show JSON format 1 with values; raw tfstate is unsupported")
	}
	add := func(id, parent, kind, label string, value any) error {
		if len(nodes) >= 50000 || nodes[id] != nil {
			return fmt.Errorf("duplicate state identity or 50000-node limit")
		}
		if !operationLabel(id) || markdownSafe(id) != id {
			return fmt.Errorf("unsafe state address/path")
		}
		n := &stateWalkNode{ID: id, Parent: parent, Kind: kind, Label: label, Value: value, Children: []string{}}
		nodes[id] = n
		if parent != "" {
			nodes[parent].Children = append(nodes[parent].Children, id)
		}
		return nil
	}
	var walkValue func(string, string, string, any, any, int) error
	walkValue = func(id, parent, label string, value, mask any, depth int) error {
		if depth > 64 {
			return fmt.Errorf("state nesting exceeds 64")
		}
		if flag, ok := mask.(bool); ok && flag || isSensitivePath(label) {
			return add(id, parent, "redacted", label, "[REDACTED]")
		}
		switch v := value.(type) {
		case map[string]any:
			if err := add(id, parent, "object", label, nil); err != nil {
				return err
			}
			m, _ := mask.(map[string]any)
			for _, key := range sortedKeys(v) {
				if err := walkValue(id+"/"+statePointer(key), id, key, v[key], m[key], depth+1); err != nil {
					return err
				}
			}
		case []any:
			if err := add(id, parent, "array", label, nil); err != nil {
				return err
			}
			m, _ := mask.([]any)
			for i, item := range v {
				var child any
				if i < len(m) {
					child = m[i]
				}
				if err := walkValue(id+"/"+strconv.Itoa(i), id, strconv.Itoa(i), item, child, depth+1); err != nil {
					return err
				}
			}
		default:
			kind := "scalar"
			if v == nil {
				kind = "null"
				value = "null"
			}
			if b, _ := json.Marshal(value); len(b) > 4096 {
				value = "[value exceeds 4096-byte display limit]"
			}
			return add(id, parent, kind, label, value)
		}
		return nil
	}
	var walkModule func(showStateModule, string, int) error
	walkModule = func(m showStateModule, parent string, depth int) error {
		if depth > 64 {
			return fmt.Errorf("module nesting exceeds 64")
		}
		id := "module:" + m.Address
		label := m.Address
		if parent == "" {
			id = "module:"
			label = "root module"
		} else if m.Address == "" {
			return fmt.Errorf("child module address missing")
		}
		if err := add(id, parent, "module", label, nil); err != nil {
			return err
		}
		for _, r := range m.Resources {
			if !operationLabel(r.Address) {
				return fmt.Errorf("resource address missing")
			}
			rid := "resource:" + r.Address
			if err := add(rid, id, "resource", r.Address, nil); err != nil {
				return err
			}
			mask := r.Sensitive
			if r.Values == nil {
				gaps = append(gaps, "Resource attributes missing for "+r.Address)
				mask = true
			}
			if mask == nil || validateValueMask(mask, r.Values) != nil {
				mask = true
				gaps = append(gaps, "Sensitivity metadata missing or invalid for "+r.Address+"; values withheld")
			}
			if err := walkValue(rid+"#", rid, "attributes", r.Values, mask, 0); err != nil {
				return err
			}
		}
		for _, child := range m.Children {
			if err := walkModule(child, id, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walkModule(s.Values.Root, "", 0); err != nil {
		return nodes, gaps, err
	}
	outputNames := []string{}
	for k := range s.Values.Outputs {
		outputNames = append(outputNames, k)
	}
	sort.Strings(outputNames)
	for _, name := range outputNames {
		o := s.Values.Outputs[name]
		sensitive := true
		if o.Sensitive != nil {
			sensitive = *o.Sensitive
		} else {
			gaps = append(gaps, "Output sensitivity missing: "+name)
		}
		if err := walkValue("output:"+statePointer(name), "module:", name, o.Value, sensitive, 0); err != nil {
			return nodes, gaps, err
		}
	}
	for _, n := range nodes {
		sort.Strings(n.Children)
	}
	return nodes, gaps, nil
}
func runStateWalk(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("state-walk requires start, show, child, parent, next, previous, goto, find or bookmark")
	}
	mode, args := args[0], args[1:]
	if !oneOf(mode, "start", "show", "child", "parent", "next", "previous", "goto", "find", "bookmark") {
		return fmt.Errorf("unknown state navigation operation")
	}
	fs := flag.NewFlagSet("state-walk "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state", ".rcdo-state-walk.json", "navigation file")
	input := fs.String("input", "", "state show JSON for start")
	id := fs.String("id", "", "exact node ID for goto")
	address := fs.String("address", "", "exact resource address for goto")
	name := fs.String("name", "", "bookmark name")
	query := fs.String("query", "", "find in addresses and paths, never values")
	index := fs.Int("index", 1, "one-based child number")
	limit := fs.Int("limit", 50, "max displayed children/search matches, 1..1000")
	width := fs.Int("width", 72, "minimum 40")
	format := fs.String("format", "text", "text or json")
	setAccessibleUsage(fs, "state-walk "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *width < 40 || *limit < 1 || *limit > 1000 || !oneOf(*format, "text", "json") {
		return fmt.Errorf("invalid navigation options")
	}
	var s stateWalkSession
	var old, raw []byte
	var err error
	if mode == "start" {
		if *input == "" {
			return fmt.Errorf("start requires --input")
		}
		s.Source, raw, err = captureArtifact(*input)
		s.SchemaVersion = "1"
		s.Cursor = "module:"
		s.Bookmarks = map[string]string{}
	} else {
		old, err = readConfigSource(*statePath)
		if err == nil {
			err = strictJSON(old, &s)
		}
		if err != nil || s.SchemaVersion != "1" || s.Bookmarks == nil {
			return fmt.Errorf("invalid state navigation file")
		}
		raw, err = readBoundArtifact(s.Source)
		if err != nil {
			fmt.Fprintln(stderr, "State evidence changed or is unavailable; start a new navigation file for the new version.")
			return reportError{status: finding.StatusIncomplete}
		}
	}
	if err != nil {
		return err
	}
	if err = separateArtifact(*statePath, s.Source); err != nil {
		return err
	}
	nodes, gaps, err := buildStateTree(raw)
	if err != nil {
		return err
	}
	current := nodes[s.Cursor]
	if current == nil {
		return fmt.Errorf("saved cursor is unavailable")
	}
	switch mode {
	case "child":
		if *index < 1 || *index > len(current.Children) {
			return fmt.Errorf("child number out of range")
		}
		s.Cursor = current.Children[*index-1]
	case "parent":
		if current.Parent != "" {
			s.Cursor = current.Parent
		}
	case "next", "previous":
		if current.Parent != "" {
			siblings := nodes[current.Parent].Children
			for i, x := range siblings {
				if x == s.Cursor {
					if mode == "next" && i+1 < len(siblings) {
						s.Cursor = siblings[i+1]
					}
					if mode == "previous" && i > 0 {
						s.Cursor = siblings[i-1]
					}
					break
				}
			}
		}
	case "goto":
		choices := 0
		target := ""
		if *id != "" {
			choices++
			target = *id
		}
		if *address != "" {
			choices++
			target = "resource:" + *address
		}
		if *name != "" {
			choices++
			target = s.Bookmarks[*name]
		}
		if choices != 1 || nodes[target] == nil {
			return fmt.Errorf("goto requires one existing id, address or bookmark")
		}
		s.Cursor = target
	case "bookmark":
		if !operationLabel(*name) || len(s.Bookmarks) >= 1000 {
			return fmt.Errorf("valid bookmark name required; maximum 1000")
		}
		s.Bookmarks[*name] = s.Cursor
	case "find":
		if strings.TrimSpace(*query) == "" {
			return fmt.Errorf("find requires --query")
		}
	}
	if !oneOf(mode, "show", "find") {
		if err = saveWorkState(*statePath, s, old, func() error { _, e := readBoundArtifact(s.Source); return e }); err != nil {
			return err
		}
	}
	current = nodes[s.Cursor]
	selected := []*stateWalkNode{}
	total := 0
	ids := current.Children
	if mode == "find" {
		ids = []string{}
		for key := range nodes {
			if strings.Contains(strings.ToLower(key), strings.ToLower(*query)) {
				ids = append(ids, key)
			}
		}
		sort.Strings(ids)
	}
	total = len(ids)
	for _, key := range ids {
		if len(selected) == *limit {
			break
		}
		selected = append(selected, nodes[key])
	}
	if *format == "json" {
		err = json.NewEncoder(stdout).Encode(struct {
			SchemaVersion string           `json:"schema_version"`
			Source        boundArtifact    `json:"source"`
			Current       *stateWalkNode   `json:"current"`
			Entries       []*stateWalkNode `json:"entries"`
			Omitted       int              `json:"omitted"`
			Gaps          []string         `json:"incomplete_checks"`
		}{"1", s.Source, current, selected, total - len(selected), gaps})
	} else {
		writeWrapped(stdout, "State snapshot: "+s.Source.SHA256, *width)
		writeWrapped(stdout, "Current: "+safeReportText(current.ID)+" ("+current.Kind+")", *width)
		if current.Value != nil {
			b, _ := json.Marshal(current.Value)
			writeWrapped(stdout, "Value: "+safeReportText(string(b)), *width)
		}
		for i, n := range selected {
			writeWrapped(stdout, fmt.Sprintf("%d. %s (%s)", i+1, safeReportText(n.ID), n.Kind), *width)
		}
		writeWrapped(stdout, fmt.Sprintf("%d entries omitted. State records are not live health observations.", total-len(selected)), *width)
		for _, gap := range gaps {
			writeWrapped(stdout, "Incomplete: "+safeReportText(gap), *width)
		}
	}
	if err != nil {
		return err
	}
	if len(gaps) > 0 {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
