package toolkit

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"git-tools/finding"
	"gopkg.in/yaml.v3"
)

type workflowUse struct {
	Variable string   `json:"variable"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Task     string   `json:"task"`
	Kind     string   `json:"kind"`
	Via      []string `json:"via,omitempty"`
}
type workflowSourceGraph struct {
	SchemaVersion string          `json:"schema_version"`
	Root          boundArtifact   `json:"root"`
	Files         []boundArtifact `json:"files"`
	Uses          []workflowUse   `json:"uses"`
	Gaps          []string        `json:"gaps"`
}

var simpleJinjaReference = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(?:\.[A-Za-z_][A-Za-z0-9_]*|\[(?:[0-9]+|"[^"\r\n]+"|'[^'\r\n]+')\])*$`)
var jinjaExpressions = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)

type workflowTracer struct {
	root    string
	graph   workflowSourceGraph
	stack   map[string]bool
	seen    map[string]bool
	aliases map[string][]string
	bytes   int
}

func (t *workflowTracer) gap(message string) {
	t.graph.Gaps = append(t.graph.Gaps, safeReportText(message))
}
func (t *workflowTracer) references(value, file, task, kind string, line int) []string {
	refs := []string{}
	if strings.Contains(value, "{%") {
		t.gap(fmt.Sprintf("%s:%d contains Jinja control flow requiring runtime evaluation", file, line))
	}
	matches := jinjaExpressions.FindAllStringSubmatch(value, -1)
	if strings.Contains(value, "{{") && len(matches) == 0 {
		t.gap(fmt.Sprintf("%s:%d contains an incomplete template expression", file, line))
	}
	for _, m := range matches {
		if len(t.graph.Uses) >= 10000 {
			t.gap("Consumer tracing reached 10000 reference limit")
			break
		}
		ref := simpleJinjaReference.FindStringSubmatch(strings.TrimSpace(m[1]))
		if len(ref) == 0 {
			t.gap(fmt.Sprintf("%s:%d contains a transformation or lookup requiring an adapter", file, line))
			continue
		}
		if oneOf(ref[1], "true", "false", "none", "True", "False", "None") {
			continue
		}
		refs = append(refs, ref[1])
		t.graph.Uses = append(t.graph.Uses, workflowUse{Variable: ref[1], File: file, Line: line, Task: task, Kind: kind})
	}
	return refs
}
func (t *workflowTracer) path(path string) (string, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	resolved, e := filepath.EvalSymlinks(absolute)
	if e != nil {
		return "", e
	}
	relative, e := filepath.Rel(t.root, resolved)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source escapes project root")
	}
	return resolved, nil
}
func (t *workflowTracer) load(path, role, kind string, depth int) {
	if depth > 32 || len(t.seen) >= 128 {
		t.gap("Source traversal reached depth/file limit")
		return
	}
	path, e := t.path(path)
	if e != nil {
		t.gap("Referenced source is missing or outside project root")
		return
	}
	relative, _ := filepath.Rel(t.root, path)
	if t.stack[path] {
		t.gap("Source inclusion cycle: " + relative)
		return
	}
	if t.seen[path] {
		return
	}
	t.seen[path] = true
	t.stack[path] = true
	defer delete(t.stack, path)
	binding, data, e := captureArtifact(path)
	if e != nil {
		t.gap("Source unreadable: " + relative)
		return
	}
	t.bytes += len(data)
	if t.bytes > 32<<20 {
		t.gap("Source traversal exceeds 32 MiB")
		return
	}
	t.graph.Files = append(t.graph.Files, binding)
	if kind == "template" {
		for line, text := range strings.Split(string(data), "\n") {
			t.references(text, relative, "template", "template", line+1)
		}
		return
	}
	if validateConfigDocument("yaml", data) != nil {
		t.gap("Ambiguous or invalid YAML: " + relative)
		return
	}
	var node yaml.Node
	if yaml.Unmarshal(data, &node) != nil {
		t.gap("Invalid YAML: " + relative)
		return
	}
	if len(node.Content) != 1 || (oneOf(kind, "playbook", "tasks", "handlers") && node.Content[0].Kind != yaml.SequenceNode) || (oneOf(kind, "vars", "defaults") && node.Content[0].Kind != yaml.MappingNode) {
		t.gap("Unexpected source structure: " + relative)
		return
	}
	t.walk(&node, path, relative, role, "", kind, depth, 0)
}
func yamlField(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func (t *workflowTracer) role(name string, depth int) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(name) {
		t.gap("Dynamic or collection role needs an explicit adapter")
		return
	}
	base := filepath.Join(t.root, "roles", name)
	for _, suffix := range []string{"yml", "yaml"} {
		if _, e := os.Stat(filepath.Join(base, "meta", "main."+suffix)); e == nil {
			t.gap("Role metadata dependencies require explicit resolution: " + name)
		}
	}
	for _, sub := range []string{"defaults", "vars", "handlers"} {
		p := filepath.Join(base, sub, "main.yml")
		if _, e := os.Stat(p); os.IsNotExist(e) {
			p = filepath.Join(base, sub, "main.yaml")
		}
		if _, e := os.Stat(p); e == nil {
			t.load(p, base, sub, depth+1)
		}
	}
	tasks := filepath.Join(base, "tasks", "main.yml")
	if _, e := os.Stat(tasks); os.IsNotExist(e) {
		tasks = filepath.Join(base, "tasks", "main.yaml")
	}
	t.load(tasks, base, "tasks", depth+1)
}
func (t *workflowTracer) walk(n *yaml.Node, path, file, role, task, context string, depth, nesting int) {
	if nesting > 64 {
		t.gap("YAML source depth exceeded")
		return
	}
	if n.Kind == yaml.AliasNode {
		t.gap(file + ": YAML aliases need runtime merge resolution")
		return
	}
	if n.Kind == yaml.MappingNode {
		if name := yamlField(n, "name"); name != nil {
			task = auditText(name.Value)
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			short := strings.TrimPrefix(key.Value, "ansible.builtin.")
			switch short {
			case "import_tasks", "include_tasks", "import_playbook", "include":
				target := value.Value
				if value.Kind == yaml.MappingNode {
					if f := yamlField(value, "file"); f != nil {
						target = f.Value
					}
				}
				if target == "" || workflowTemplated(target) {
					t.gap(file + ": dynamic task inclusion")
				} else {
					t.load(filepath.Join(filepath.Dir(path), target), role, "tasks", depth+1)
				}
			case "roles":
				for _, r := range value.Content {
					name := r.Value
					if r.Kind == yaml.MappingNode {
						if v := yamlField(r, "role"); v != nil {
							name = v.Value
						}
					}
					t.role(name, depth+1)
				}
			case "include_role", "import_role":
				if name := yamlField(value, "name"); name != nil {
					t.role(name.Value, depth+1)
				} else {
					t.gap(file + ": unresolved role name")
				}
				if yamlField(value, "tasks_from") != nil {
					t.gap(file + ": nondefault role entrypoint requires explicit resolution")
				}
			case "template":
				src := yamlField(value, "src")
				if src == nil || workflowTemplated(src.Value) {
					t.gap(file + ": unresolved template source")
				} else {
					base := filepath.Dir(path)
					if role != "" {
						base = role
					}
					candidate := filepath.Join(base, "templates", src.Value)
					if _, e := os.Stat(candidate); e != nil {
						candidate = filepath.Join(filepath.Dir(path), src.Value)
					}
					t.load(candidate, role, "template", depth+1)
				}
			case "vars_files":
				files := value.Content
				if value.Kind == yaml.ScalarNode {
					files = []*yaml.Node{value}
				}
				for _, v := range files {
					if v.Kind != yaml.ScalarNode || workflowTemplated(v.Value) {
						t.gap(file + ": dynamic vars file")
					} else {
						t.load(filepath.Join(filepath.Dir(path), v.Value), role, "vars", depth+1)
					}
				}
			case "include_vars":
				target := value.Value
				if value.Kind == yaml.MappingNode {
					if v := yamlField(value, "file"); v != nil {
						target = v.Value
					}
				}
				if target == "" || workflowTemplated(target) {
					t.gap(file + ": unresolved include_vars")
				} else {
					t.load(filepath.Join(filepath.Dir(path), target), role, "vars", depth+1)
				}
			case "lookup", "register", "loop", "with_items", "delegate_to", "when", "until", "failed_when", "changed_when":
				t.gap(file + ": " + short + " requires runtime consumer resolution")
			}
			if value.Kind == yaml.ScalarNode {
				kind := "task"
				if oneOf(context, "vars", "defaults", "set_fact") {
					kind = "variable-assignment"
				}
				refs := t.references(value.Value, file, task, kind, value.Line)
				// A direct assignment can relay a variable into later template consumers.
				matches := jinjaExpressions.FindAllStringSubmatch(strings.TrimSpace(value.Value), -1)
				if oneOf(context, "vars", "defaults", "set_fact") && len(refs) == 1 && len(matches) == 1 && matches[0][0] == strings.TrimSpace(value.Value) && strings.TrimSpace(matches[0][1]) == refs[0] {
					t.aliases[key.Value] = append(t.aliases[key.Value], refs[0])
				}
			} else {
				t.walk(value, path, file, role, task, short, depth, nesting+1)
			}
		}
		return
	}
	for _, child := range n.Content {
		t.walk(child, path, file, role, task, context, depth, nesting+1)
	}
}

func runWorkflowTrace(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var root, graphOut, variable string
	_, o, err := parseFlags("workflow-trace", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&root, "root", ".", "project root; external source paths are rejected")
		fs.StringVar(&graphOut, "graph-out", "", "create a bound source graph artifact; refuses overwrite")
		fs.StringVar(&variable, "variable", "", "show consumers of one variable")
		return &o
	})
	if err != nil {
		return err
	}
	if o.input == "-" {
		return fmt.Errorf("trace requires a named --input playbook")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	binding, data, err := captureArtifact(o.input)
	if err != nil {
		return err
	}
	t := workflowTracer{root: root, graph: workflowSourceGraph{SchemaVersion: "1", Root: binding, Files: []boundArtifact{}, Uses: []workflowUse{}, Gaps: []string{}}, stack: map[string]bool{}, seen: map[string]bool{}, aliases: map[string][]string{}}
	t.load(o.input, "", "playbook", 0)
	original := append([]workflowUse{}, t.graph.Uses...)
	for _, use := range original {
		seen := map[string]bool{use.Variable: true}
		queue := []workflowUse{use}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, source := range t.aliases[current.Variable] {
				if len(t.graph.Uses) >= 10000 || len(current.Via) >= 64 {
					t.gap("Alias tracing reached reference or depth limit")
					break
				}
				if seen[source] {
					if source == current.Variable || oneOf(source, current.Via...) {
						t.gap("Variable alias cycle requires runtime resolution")
					}
					continue
				}
				seen[source] = true
				next := current
				next.Variable = source
				next.Via = append(append([]string{}, current.Via...), current.Variable)
				t.graph.Uses = append(t.graph.Uses, next)
				queue = append(queue, next)
			}
		}
	}
	sort.SliceStable(t.graph.Uses, func(i, j int) bool {
		a, b := t.graph.Uses[i], t.graph.Uses[j]
		if a.Variable != b.Variable {
			return a.Variable < b.Variable
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	t.graph.Gaps = uniqueStrings(t.graph.Gaps)
	r := finding.Report{CompletedChecks: []string{"Static variable consumer tracing; source relationships do not establish runtime values or variable precedence"}, IncompleteChecks: t.graph.Gaps}
	bindReportSource(&r, o, "workflow-trace", data)
	for _, f := range t.graph.Files {
		r.Provenance.Artifacts = append(r.Provenance.Artifacts, finding.ProvenanceArtifact{Path: f.Path, SHA256: f.SHA256})
	}
	for _, use := range t.graph.Uses {
		if variable != "" && variable != use.Variable {
			continue
		}
		addIAC(&r, "WF-USE", finding.SeverityInfo, "Variable consumed by "+use.Kind, use.File+":"+fmt.Sprint(use.Line), "trace", o.environment, "Variable: "+use.Variable+"; task: "+use.Task+"; via: "+strings.Join(use.Via, " -> ")+"; values withheld")
	}
	if variable != "" && len(r.Findings) == 0 {
		r.IncompleteChecks = append(r.IncompleteChecks, "No statically resolved consumer for requested variable")
	}
	if graphOut != "" {
		if err := saveWorkState(graphOut, t.graph, nil, func() error { return checkProvenanceArtifacts(r.Provenance) }); err != nil {
			return err
		}
	}
	return emitReportOptions(stdout, o, r)
}
