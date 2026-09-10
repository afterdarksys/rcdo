package toolkit

import (
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"sort"
	"time"
)

type relationNode struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Account string `json:"account"`
	Region  string `json:"region"`
}
type relationEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}
type relationScope struct {
	Account  string `json:"account"`
	Region   string `json:"region"`
	Complete *bool  `json:"complete"`
	Outcome  string `json:"outcome"`
}
type relationGraph struct {
	SchemaVersion string          `json:"schema_version"`
	Complete      *bool           `json:"complete"`
	CollectedAt   string          `json:"collected_at"`
	Source        string          `json:"source"`
	Nodes         []relationNode  `json:"nodes"`
	Edges         []relationEdge  `json:"edges"`
	Scopes        []relationScope `json:"scopes"`
}

func runResourceWalk(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var resource, direction string
	var depth int
	var age time.Duration
	var required sessionPaths
	_, o, err := parseFlags("resource-walk", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&resource, "resource", "", "exact starting resource ID")
		fs.StringVar(&direction, "direction", "dependencies", "dependencies or dependents")
		fs.IntVar(&depth, "depth", 1, "traversal depth, 1 to 10")
		fs.DurationVar(&age, "max-age", 15*time.Minute, "maximum graph observation age")
		fs.Var(&required, "require-scope", "required account/region; repeatable")
		return &o
	})
	if err != nil {
		return err
	}
	if resource == "" || !oneOf(direction, "dependencies", "dependents") || depth < 1 || depth > 10 || age <= 0 {
		return fmt.Errorf("--resource, valid direction/depth and positive max-age are required")
	}
	raw, err := readInput(o.input, stdin)
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return fmt.Errorf("graph exceeds 16 MiB")
	}
	var g relationGraph
	if strictJSON(raw, &g) != nil || g.SchemaVersion != "1" || g.Nodes == nil || g.Edges == nil || len(g.Nodes) > 10000 || len(g.Edges) > 50000 {
		return fmt.Errorf("invalid or oversized graph")
	}
	r := finding.Report{CompletedChecks: []string{"Resource relationship navigation: " + direction, "Edges point from a dependent resource to its dependency", "Graph SHA-256: " + digestBytes(raw)}}
	if g.Complete == nil || !*g.Complete || !operationLabel(g.Source) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Graph collection is incomplete or has no source")
	}
	checkFresh(&r, "Graph", g.CollectedAt, age, time.Now().UTC())
	scopes := map[string]bool{}
	for _, s := range g.Scopes {
		key := s.Account + "/" + s.Region
		if !operationLabel(s.Account) || !operationLabel(s.Region) {
			return fmt.Errorf("invalid graph scope")
		}
		if _, ok := scopes[key]; ok {
			return fmt.Errorf("duplicate graph scope")
		}
		scopes[key] = s.Complete != nil && *s.Complete && s.Outcome == "pass"
		if !scopes[key] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Scope collection incomplete: "+key)
		}
	}
	for _, key := range required {
		if !scopes[key] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required scope missing or incomplete: "+key)
		}
	}
	nodes := map[string]relationNode{}
	for _, n := range g.Nodes {
		if !operationLabel(n.ID) || !operationLabel(n.Type) || !operationLabel(n.Account) || !operationLabel(n.Region) {
			return fmt.Errorf("resource identity is missing or unsafe")
		}
		if _, ok := nodes[n.ID]; ok {
			return fmt.Errorf("duplicate resource ID")
		}
		nodes[n.ID] = n
		if !scopes[n.Account+"/"+n.Region] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Node lacks complete scope coverage: "+n.ID)
		}
	}
	if _, ok := nodes[resource]; !ok {
		return fmt.Errorf("starting resource not found")
	}
	adjacency := map[string][]relationEdge{}
	all := map[string][]string{}
	seenEdge := map[string]bool{}
	for _, e := range g.Edges {
		if !oneOf(e.Kind, "observed", "configuration", "inferred") || !operationLabel(e.Source) {
			return fmt.Errorf("edge kind/source missing or unsupported")
		}
		key := e.From + "\x00" + e.To + "\x00" + e.Kind
		if seenEdge[key] {
			return fmt.Errorf("duplicate graph edge")
		}
		seenEdge[key] = true
		_, fromOK := nodes[e.From]
		_, toOK := nodes[e.To]
		if !fromOK || !toOK {
			r.IncompleteChecks = append(r.IncompleteChecks, "Edge references an unavailable resource: "+markdownSafe(e.From)+" -> "+markdownSafe(e.To))
			continue
		}
		all[e.From] = append(all[e.From], e.To)
		if direction == "dependents" {
			e.From, e.To = e.To, e.From
		}
		adjacency[e.From] = append(adjacency[e.From], e)
	}
	visited, active := map[string]bool{}, map[string]bool{}
	var cycle func(string) bool
	cycle = func(id string) bool {
		if active[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visited[id] = true
		active[id] = true
		for _, next := range all[id] {
			if cycle(next) {
				return true
			}
		}
		active[id] = false
		return false
	}
	for _, id := range sortedRelationNodes(nodes) {
		if cycle(id) {
			addIAC(&r, "REL-CYCLE", finding.SeverityWarning, "Dependency graph contains a cycle", id, "navigate", o.environment, "Traversal is bounded; inspect the source relationships")
			break
		}
	}
	type queued struct {
		id    string
		level int
	}
	queue := []queued{{resource, 0}}
	seen := map[string]bool{resource: true}
	number := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		edges := adjacency[current.id]
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].To == edges[j].To {
				return edges[i].Kind < edges[j].Kind
			}
			return edges[i].To < edges[j].To
		})
		if current.level == depth {
			if len(edges) > 0 {
				r.CompletedChecks = append(r.CompletedChecks, "Depth limit reached at "+current.id+"; further relationships were not expanded")
			}
			continue
		}
		for _, e := range edges {
			number++
			n := nodes[e.To]
			item := makeFinding(stableFindingID(&r, "REL-EDGE", e.From, e.To, e.Kind), finding.SeverityInfo, fmt.Sprintf("Choice %d: %s", number, n.ID), n.ID, "navigate", o.environment, "Relationship evidence: "+e.Kind, fmt.Sprintf("From: %s; depth: %d; type: %s; account: %s; region: %s; source: %s", e.From, current.level+1, n.Type, n.Account, n.Region, e.Source), "To continue, run resource-walk against the same artifact with --resource set to this exact resource ID.")
			if e.Kind == "observed" {
				item.Confidence = finding.ConfidenceHigh
			} else if e.Kind == "inferred" {
				item.Confidence = finding.ConfidenceLow
			}
			r.Findings = append(r.Findings, item)
			if !seen[e.To] {
				seen[e.To] = true
				queue = append(queue, queued{e.To, current.level + 1})
			}
		}
	}
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("Displayed %d relationships from %s", number, resource))
	return emitReportOptions(stdout, o, r)
}
func sortedRelationNodes(nodes map[string]relationNode) []string {
	m := map[string]any{}
	for k := range nodes {
		m[k] = true
	}
	return sortedKeys(m)
}
