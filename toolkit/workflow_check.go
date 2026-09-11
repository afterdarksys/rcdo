package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"git-tools/finding"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	"gopkg.in/yaml.v3"
)

type workflowIdentity struct {
	ChangeID    string `json:"change_id"`
	Commit      string `json:"commit"`
	Environment string `json:"environment"`
	Engine      string `json:"engine"`
	Backend     string `json:"backend"`
	Workspace   string `json:"workspace"`
	Account     string `json:"account"`
	Region      string `json:"region"`
}
type workflowMapping struct {
	ID       string `json:"id"`
	Output   string `json:"output"`
	Pointer  string `json:"pointer"`
	Host     string `json:"host"`
	Variable string `json:"variable"`
	Type     string `json:"type"`
}
type workflowManifest struct {
	Origin          *workflowOrigin             `json:"origin,omitempty"`
	SourceGraph     *boundArtifact              `json:"source_graph,omitempty"`
	SchemaVersion   string                      `json:"schema_version"`
	Identity        workflowIdentity            `json:"identity"`
	Outputs         boundArtifact               `json:"outputs"`
	Producer        boundArtifact               `json:"producer"`
	Inventory       boundArtifact               `json:"inventory"`
	InventoryFormat string                      `json:"inventory_format,omitempty"`
	ExtraVars       *boundArtifact              `json:"extra_vars,omitempty"`
	Playbook        boundArtifact               `json:"playbook"`
	Hosts           []string                    `json:"hosts"`
	Mappings        []workflowMapping           `json:"mappings"`
	Execution       *boundArtifact              `json:"execution,omitempty"`
	Events          *boundArtifact              `json:"events,omitempty"`
	Verification    *boundArtifact              `json:"verification,omitempty"`
	RequiredChecks  []workflowHealthRequirement `json:"required_checks,omitempty"`
}

type workflowHealthRequirement struct {
	Host string `json:"host"`
	Name string `json:"name"`
}

// Receipts are supplied by the caller's acquisition/invocation adapter. They
// establish declared local lineage, never authenticated remote attestation.
type workflowProducer struct {
	SchemaVersion string           `json:"schema_version"`
	Identity      workflowIdentity `json:"identity"`
	Outcome       string           `json:"outcome"`
	AppliedAt     string           `json:"applied_at"`
	CollectedAt   string           `json:"collected_at"`
	StateLineage  string           `json:"state_lineage"`
	StateSerial   *uint64          `json:"state_serial"`
	OutputsSHA256 string           `json:"outputs_sha256"`
}
type workflowExecution struct {
	VariableSourcesComplete *bool            `json:"variable_sources_complete"`
	ResolvedInputs          *boundArtifact   `json:"resolved_inputs"`
	SchemaVersion           string           `json:"schema_version"`
	Identity                workflowIdentity `json:"identity"`
	RunID                   string           `json:"run_id"`
	StartedAt               string           `json:"started_at"`
	FinishedAt              string           `json:"finished_at"`
	Outcome                 string           `json:"outcome"`
	CheckMode               *bool            `json:"check_mode"`
	OutputsSHA256           string           `json:"outputs_sha256"`
	InventorySHA256         string           `json:"inventory_sha256"`
	ExtraVarsSHA256         string           `json:"extra_vars_sha256"`
	PlaybookSHA256          string           `json:"playbook_sha256"`
	Hosts                   []string         `json:"hosts"`
}
type workflowVerification struct {
	SchemaVersion   string           `json:"schema_version"`
	Identity        workflowIdentity `json:"identity"`
	RunID           string           `json:"run_id"`
	ExecutionSHA256 string           `json:"execution_sha256"`
	CollectedAt     string           `json:"collected_at"`
	Hosts           []string         `json:"hosts"`
	Checks          []struct {
		Name    string `json:"name"`
		Host    string `json:"host"`
		Outcome string `json:"outcome"`
	} `json:"checks"`
}
type workflowOutput struct {
	Sensitive *bool           `json:"sensitive"`
	Type      json.RawMessage `json:"type"`
	Value     json.RawMessage `json:"value"`
}
type workflowGroup struct {
	Vars     map[string]any            `json:"vars"`
	Hosts    map[string]map[string]any `json:"hosts"`
	Children map[string]workflowGroup  `json:"children"`
}

func workflowJSON(data []byte, value any) error {
	if validateConfigDocument("json", data) != nil {
		return fmt.Errorf("invalid or ambiguous JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return fmt.Errorf("invalid workflow artifact schema")
	}
	return nil
}
func workflowDocument(data []byte, value any) error {
	if json.Valid(data) {
		return workflowJSON(data, value)
	}
	if validateConfigDocument("yaml", data) != nil {
		return fmt.Errorf("invalid or ambiguous YAML")
	}
	var raw any
	if yaml.Unmarshal(data, &raw) != nil {
		return fmt.Errorf("invalid YAML")
	}
	b, err := json.Marshal(normalizeConfigMaps(raw))
	if err != nil {
		return fmt.Errorf("unsupported YAML values")
	}
	return workflowJSON(b, value)
}
func workflowLabels(id workflowIdentity) bool {
	for _, s := range []string{id.ChangeID, id.Commit, id.Environment, id.Backend, id.Workspace, id.Account, id.Region} {
		if !operationLabel(s) || safeReportText(s) != s || s == "unknown" {
			return false
		}
	}
	p := finding.Provenance{SchemaVersion: "1", Tool: "workflow-check", ChangeID: id.ChangeID, Commit: id.Commit, Environment: id.Environment, SourceSHA256: strings.Repeat("a", 64), CollectedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return p.Validate() == nil && oneOf(id.Engine, "terraform", "tofu")
}
func workflowSameHosts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string{}, a...)
	bb := append([]string{}, b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] || i > 0 && aa[i] == aa[i-1] {
			return false
		}
	}
	return true
}
func workflowPointer(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	for _, token := range strings.Split(pointer[1:], "/") {
		for i := 0; i < len(token); i++ {
			if token[i] == '~' {
				if i+1 >= len(token) || token[i+1] != '0' && token[i+1] != '1' {
					return nil, false
				}
				i++
			}
		}
		key := strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch v := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = v[key]
			if !ok {
				return nil, false
			}
		case []any:
			n, e := strconv.Atoi(key)
			if e != nil || strconv.Itoa(n) != key || n < 0 || n >= len(v) {
				return nil, false
			}
			value = v[n]
		default:
			return nil, false
		}
	}
	return value, true
}
func workflowValueType(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "null"
	}
}
func workflowTemplated(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, "{{") || strings.Contains(x, "{%")
	case map[string]any:
		for _, child := range x {
			if workflowTemplated(child) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if workflowTemplated(child) {
				return true
			}
		}
	}
	return false
}

func runWorkflowCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var manifestPath, stage string
	var age time.Duration
	_, o, err := parseFlags("workflow-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&manifestPath, "manifest", "", "workflow manifest with source hashes and mappings")
		fs.StringVar(&stage, "stage", "inputs", "required evidence stage: inputs, execution, or verified")
		fs.DurationVar(&age, "max-age", 24*time.Hour, "maximum age of producer and run evidence")
		return &o
	})
	if err != nil {
		return err
	}
	if manifestPath == "" || !oneOf(stage, "inputs", "execution", "verified") || age <= 0 || o.policy != "" {
		return fmt.Errorf("manifest, valid stage and positive max-age required; workflow checks cannot be suppressed")
	}
	data, err := readConfigSource(manifestPath)
	if err != nil {
		return err
	}
	var m workflowManifest
	if workflowJSON(data, &m) != nil || m.SchemaVersion != "1" || !workflowLabels(m.Identity) || len(m.Hosts) == 0 || len(m.Mappings) == 0 || len(m.Mappings) > 10000 || len(m.Hosts) > 10000 {
		return fmt.Errorf("invalid workflow manifest identity, version, hosts or mappings")
	}
	o.environment = m.Identity.Environment
	r := finding.Report{CompletedChecks: []string{"Workflow stage: " + stage, "Local artifact lineage only; receipts are caller-supplied evidence, not authenticated execution attestation", "Values withheld from workflow narration, including nonsensitive values"}}
	o.changeID = m.Identity.ChangeID
	o.commit = m.Identity.Commit
	o.input = manifestPath
	bindReportSource(&r, o, "workflow-check", data)
	base := filepath.Dir(manifestPath)
	load := func(label string, a boundArtifact) []byte {
		if !filepath.IsAbs(a.Path) {
			a.Path = filepath.Join(base, a.Path)
			a.Path, _ = filepath.Abs(a.Path)
		}
		b, e := readBoundArtifact(a)
		if e != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, label+": artifact missing, changed or invalid binding")
			return nil
		}
		r.Provenance.Artifacts = append(r.Provenance.Artifacts, finding.ProvenanceArtifact{Path: a.Path, SHA256: a.SHA256})
		return b
	}
	rawOutputs := load("Terraform outputs", m.Outputs)
	rawProducer := load("Producer receipt", m.Producer)
	rawInventory := load("Ansible inventory", m.Inventory)
	rawPlaybook := load("Ansible playbook", m.Playbook)
	var producer workflowProducer
	if workflowJSON(rawProducer, &producer) != nil || producer.SchemaVersion != "1" || producer.Identity != m.Identity || producer.Outcome != "succeeded" || producer.OutputsSHA256 != m.Outputs.SHA256 || !operationLabel(producer.StateLineage) || producer.StateSerial == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Producer identity, successful apply, state lineage/serial and output binding are required")
	}
	now := time.Now().UTC()
	checkFresh(&r, "Output acquisition", producer.CollectedAt, age, now)
	applied, ae := time.Parse(time.RFC3339Nano, producer.AppliedAt)
	collected, ce := time.Parse(time.RFC3339Nano, producer.CollectedAt)
	if ae != nil || ce != nil || applied.After(collected) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Output acquisition must follow the successful apply")
	}
	var outputs map[string]workflowOutput
	if workflowJSON(rawOutputs, &outputs) != nil || len(outputs) == 0 {
		r.IncompleteChecks = append(r.IncompleteChecks, "Expected Terraform root output JSON with type, sensitivity and value metadata")
	}
	var inventory map[string]workflowGroup
	if !oneOf(m.InventoryFormat, "", "static", "resolved") {
		return fmt.Errorf("inventory_format must be static or resolved")
	}
	if m.InventoryFormat != "resolved" && (workflowDocument(rawInventory, &inventory) != nil || len(inventory) != 1) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Inventory must be a static all-root inventory; resolve dynamic inventory through an explicit adapter")
	}
	all, ok := inventory["all"]
	if !ok && m.InventoryFormat != "resolved" {
		r.IncompleteChecks = append(r.IncompleteChecks, "Static inventory all group is required")
	}
	effective := map[string]map[string]any{}
	origins := map[string]map[string]string{}
	groups := map[string]map[string]bool{}
	var collectGroup func(string, workflowGroup, map[string]any, map[string]string, int) map[string]bool
	collectGroup = func(name string, g workflowGroup, parent map[string]any, parentOrigins map[string]string, depth int) map[string]bool {
		members := map[string]bool{}
		if depth > 32 {
			r.IncompleteChecks = append(r.IncompleteChecks, "Inventory nesting exceeds 32 groups")
			return members
		}
		if _, exists := groups[name]; exists {
			r.IncompleteChecks = append(r.IncompleteChecks, "Repeated inventory group requires native precedence resolution: "+safeReportText(name))
		}
		vars := map[string]any{}
		where := map[string]string{}
		for k, v := range parent {
			vars[k] = v
			where[k] = parentOrigins[k]
		}
		for k, v := range g.Vars {
			vars[k] = v
			where[k] = "group " + name
		}
		if _, present := vars["ansible_group_priority"]; present {
			r.IncompleteChecks = append(r.IncompleteChecks, "Inventory group priority requires native precedence resolution")
		}
		for host, hv := range g.Hosts {
			members[host] = true
			if !operationLabel(host) || safeReportText(host) != host {
				r.IncompleteChecks = append(r.IncompleteChecks, "Unsafe inventory host identity")
				continue
			}
			values := map[string]any{}
			sources := map[string]string{}
			for k, v := range vars {
				values[k] = v
				sources[k] = where[k]
			}
			for k, v := range hv {
				values[k] = v
				sources[k] = "host " + host
			}
			if effective[host] == nil {
				effective[host] = values
				origins[host] = sources
			} else {
				for k, v := range values {
					if old, exists := effective[host][k]; exists && !reflect.DeepEqual(old, v) {
						r.IncompleteChecks = append(r.IncompleteChecks, "Conflicting inventory memberships for host "+host+"; resolve native precedence")
					}
					effective[host][k] = v
					origins[host][k] = sources[k]
				}
			}
		}
		names := make([]string, 0, len(g.Children))
		for n := range g.Children {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for h := range collectGroup(n, g.Children[n], vars, where, depth+1) {
				members[h] = true
			}
		}
		groups[name] = members
		return members
	}
	if m.InventoryFormat == "resolved" {
		resolved, resolvedGroups, e := workflowResolved(rawInventory)
		if e != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Resolved inventory snapshot is incomplete or ambiguous")
		} else {
			effective = resolved
			groups = resolvedGroups
			for h, vars := range effective {
				origins[h] = map[string]string{}
				for k := range vars {
					origins[h][k] = "resolved inventory snapshot"
				}
			}
		}
	} else {
		collectGroup("all", all, nil, nil, 0)
	}
	extra := map[string]any{}
	if m.ExtraVars != nil {
		if workflowJSON(load("Extra variables", *m.ExtraVars), &extra) != nil || extra == nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Extra variables must be a JSON object")
		}
	}
	wanted := map[string]bool{}
	for _, host := range m.Hosts {
		if !operationLabel(host) || safeReportText(host) != host || wanted[host] {
			return fmt.Errorf("invalid or duplicate required host")
		}
		wanted[host] = true
		if effective[host] == nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required host absent from inventory: "+host)
			continue
		}
		for k, v := range extra {
			effective[host][k] = v
			origins[host][k] = "extra-vars (overrides inventory)"
		}
	}
	// Only the explicit static variable model is certified. Runtime variable
	// sources, delegated targets and selected task subsets remain unresolved.
	var plays []map[string]any
	if workflowDocument(rawPlaybook, &plays) != nil || len(plays) == 0 {
		r.IncompleteChecks = append(r.IncompleteChecks, "Playbook must be a nonempty sequence of static plays")
	}
	reached := map[string]bool{}
	for _, play := range plays {
		pattern, valid := play["hosts"].(string)
		if !valid || (groups[pattern] == nil && effective[pattern] == nil) {
			r.IncompleteChecks = append(r.IncompleteChecks, "Play host expression cannot be resolved by static inventory model")
		} else {
			for h := range wanted {
				if h == pattern || groups[pattern][h] {
					reached[h] = true
				}
			}
		}
	}
	for h := range wanted {
		if !reached[h] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required host is not selected by any play: "+h)
		}
	}
	var inspect func(any)
	inspect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				short := strings.TrimPrefix(k, "ansible.builtin.")
				if oneOf(short, "vars", "vars_files", "include_vars", "set_fact", "register", "roles", "include_role", "import_role", "include_tasks", "import_tasks", "import_playbook", "include", "delegate_to", "add_host", "group_by", "environment", "module_defaults", "tags", "when", "loop", "with_items", "action", "local_action", "connection", "remote_user", "become", "become_user", "become_method", "become_flags", "port") {
					r.IncompleteChecks = append(r.IncompleteChecks, "Playbook "+safeReportText(short)+" requires runtime resolution beyond static output mapping")
				}
				inspect(child)
			}
		case []any:
			for _, child := range x {
				inspect(child)
			}
		}
	}
	for _, p := range plays {
		inspect(p)
	}
	seenIDs := map[string]bool{}
	seenTargets := map[string]bool{}
	hostAddress := map[string]bool{}
	for _, mapping := range m.Mappings {
		target := mapping.Host + "/" + mapping.Variable
		if !operationLabel(mapping.ID) || safeReportText(mapping.ID) != mapping.ID || seenIDs[mapping.ID] || seenTargets[target] || !wanted[mapping.Host] || !operationLabel(mapping.Variable) || safeReportText(mapping.Variable) != mapping.Variable || !operationLabel(mapping.Output) || safeReportText(mapping.Output) != mapping.Output || safeReportText(mapping.Pointer) != mapping.Pointer || !oneOf(mapping.Type, "string", "number", "boolean", "array", "object") {
			return fmt.Errorf("invalid, duplicate or out-of-scope workflow mapping")
		}
		seenIDs[mapping.ID] = true
		seenTargets[target] = true
		if mapping.Variable == "ansible_host" {
			hostAddress[mapping.Host] = true
		}
		source, exists := outputs[mapping.Output]
		var value any
		if !exists || source.Sensitive == nil || workflowJSON(source.Value, &value) != nil || value == nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Mapping "+mapping.ID+": output value or sensitivity metadata missing")
			continue
		}
		typ, e := ctyjson.UnmarshalType(source.Type)
		if e != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Mapping "+mapping.ID+": invalid Terraform output type")
			continue
		}
		if _, e = ctyjson.Unmarshal(source.Value, typ); e != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Mapping "+mapping.ID+": output value violates declared Terraform type")
			continue
		}
		value, exists = workflowPointer(value, mapping.Pointer)
		actual, present := effective[mapping.Host][mapping.Variable]
		evidence := fmt.Sprintf("Producer: output.%s%s; consumer: host %s variable %s; effective source: %s; expected type: %s; sensitive: %t; values withheld", mapping.Output, mapping.Pointer, mapping.Host, mapping.Variable, origins[mapping.Host][mapping.Variable], mapping.Type, *source.Sensitive)
		if !exists || !present || value == nil || actual == nil || workflowTemplated(actual) {
			r.IncompleteChecks = append(r.IncompleteChecks, "Mapping "+mapping.ID+": path, input or resolved value unavailable")
			continue
		}
		if workflowValueType(value) != mapping.Type || workflowValueType(actual) != mapping.Type || !workflowEqual(value, actual) {
			addIAC(&r, "WF-MISMATCH", finding.SeverityHigh, "Terraform output does not match effective Ansible input", mapping.ID, "verify mapping", o.environment, evidence)
		} else {
			r.CompletedChecks = append(r.CompletedChecks, "Mapping "+mapping.ID+" matched. "+evidence)
		}
	}
	for _, h := range m.Hosts {
		if !hostAddress[h] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Host requires an explicit ansible_host output mapping: "+h)
		}
	}
	if stage != "inputs" {
		reviewWorkflowExecution(&r, m, stage, age, collected, effective, load)
	} else {
		r.CompletedChecks = append(r.CompletedChecks, "Inputs stage only: selected hosts are the declared limit; no Ansible execution or remote health claim")
	}
	if m.SourceGraph != nil {
		var graph workflowSourceGraph
		if workflowJSON(load("Consumer source graph", *m.SourceGraph), &graph) != nil || graph.SchemaVersion != "1" || graph.Root.SHA256 != m.Playbook.SHA256 || len(graph.Files) > 128 {
			r.IncompleteChecks = append(r.IncompleteChecks, "Consumer graph is invalid or belongs to another playbook")
		} else {
			for _, source := range graph.Files {
				load("Consumer source", source)
			}
			r.IncompleteChecks = append(r.IncompleteChecks, graph.Gaps...)
			for _, mapping := range m.Mappings {
				found := false
				for _, use := range graph.Uses {
					if use.Variable == mapping.Variable && oneOf(use.Kind, "task", "template") && use.Line > 0 {
						found = true
						r.CompletedChecks = append(r.CompletedChecks, "Consumer for mapping "+mapping.ID+": "+safeReportText(use.File)+":"+fmt.Sprint(use.Line)+"; task "+auditText(use.Task)+"; via "+safeReportText(strings.Join(use.Via, " -> ")))
					}
				}
				if !found && mapping.Variable != "ansible_host" {
					r.IncompleteChecks = append(r.IncompleteChecks, "Mapping "+mapping.ID+" has no resolved task/template consumer")
				}
			}
		}
	}
	r.IncompleteChecks = uniqueStrings(r.IncompleteChecks)
	return emitReportOptions(stdout, o, r)
}

func reviewWorkflowExecution(r *finding.Report, m workflowManifest, stage string, age time.Duration, collected time.Time, effective map[string]map[string]any, load func(string, boundArtifact) []byte) {
	if m.Execution == nil || m.Events == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Execution stage requires invocation receipt and callback events")
		return
	}
	var execution workflowExecution
	if workflowJSON(load("Invocation receipt", *m.Execution), &execution) != nil || execution.SchemaVersion != "1" {
		r.IncompleteChecks = append(r.IncompleteChecks, "Invalid invocation receipt")
		return
	}
	if execution.VariableSourcesComplete == nil || !*execution.VariableSourcesComplete || execution.ResolvedInputs == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Invocation requires a resolved input snapshot and complete variable-source declaration")
	} else {
		resolved, _, e := workflowResolved(load("Invocation resolved inputs", *execution.ResolvedInputs))
		if e != nil {
			r.IncompleteChecks = append(r.IncompleteChecks, "Invocation resolved input snapshot is invalid")
		} else {
			for _, mapping := range m.Mappings {
				v, present := resolved[mapping.Host][mapping.Variable]
				if !present || !workflowEqual(v, effective[mapping.Host][mapping.Variable]) {
					addIAC(r, "WF-CONSUMED", finding.SeverityHigh, "Invocation consumed different Ansible inputs", mapping.ID, "verify invocation", m.Identity.Environment, "Resolved variable differs from the reviewed mapping; values withheld")
				}
			}
		}
	}
	extraHash := ""
	if m.ExtraVars != nil {
		extraHash = m.ExtraVars.SHA256
	}
	if execution.Identity != m.Identity || execution.OutputsSHA256 != m.Outputs.SHA256 || execution.InventorySHA256 != m.Inventory.SHA256 || execution.PlaybookSHA256 != m.Playbook.SHA256 || execution.ExtraVarsSHA256 != extraHash || !workflowSameHosts(execution.Hosts, m.Hosts) || execution.CheckMode == nil || *execution.CheckMode || execution.Outcome != "succeeded" || !operationLabel(execution.RunID) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Invocation identity, applied mode, success, host scope or consumed artifact hashes do not match")
	}
	started, se := time.Parse(time.RFC3339Nano, execution.StartedAt)
	finished, fe := time.Parse(time.RFC3339Nano, execution.FinishedAt)
	if se != nil || fe != nil || started.Before(collected) || finished.Before(started) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Invocation must follow output acquisition and finish after it starts")
	}
	checkFresh(r, "Invocation", execution.FinishedAt, age, time.Now().UTC())
	events := load("Callback events", *m.Events)
	callback, err := reviewAnsibleEvents(events, m.Hosts, execution.RunID, age)
	if err != nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Invalid Ansible callback evidence")
	} else {
		appendScanReport(r, callback)
		r.CompletedChecks = append(r.CompletedChecks, callback.CompletedChecks...)
	}
	// A check-mode or skipped-only receipt cannot prove applied work. Every
	// selected host needs a non-skipped execution-mode result within this run.
	observed := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(events), []byte("\n")) {
		var event ansibleEvent
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		at, e := time.Parse(time.RFC3339Nano, event.At)
		if e != nil || at.Before(started) || at.After(finished) {
			r.IncompleteChecks = append(r.IncompleteChecks, "Callback event lies outside invocation time bounds")
		}
		if event.CheckMode != nil && *event.CheckMode {
			r.IncompleteChecks = append(r.IncompleteChecks, "Check-mode callback cannot prove applied execution")
		}
		if event.Event == "result" && oneOf(event.Outcome, "ok", "changed") && event.CheckMode != nil && !*event.CheckMode {
			observed[event.Host] = true
		}
	}
	for _, h := range m.Hosts {
		if !observed[h] {
			r.IncompleteChecks = append(r.IncompleteChecks, "No applied task result for host "+h)
		}
	}
	if stage != "verified" {
		r.CompletedChecks = append(r.CompletedChecks, "Execution receipts checked; remote service health has not been verified")
		return
	}
	if m.Verification == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Verified stage requires independent host outcome checks")
		return
	}
	var v workflowVerification
	if workflowJSON(load("Outcome verification", *m.Verification), &v) != nil || v.SchemaVersion != "1" || v.Identity != m.Identity || v.RunID != execution.RunID || v.ExecutionSHA256 != m.Execution.SHA256 || !workflowSameHosts(v.Hosts, m.Hosts) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Outcome verification has invalid identity, run or host binding")
		return
	}
	checkFresh(r, "Outcome verification", v.CollectedAt, age, time.Now().UTC())
	at, e := time.Parse(time.RFC3339Nano, v.CollectedAt)
	if e != nil || at.Before(finished) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Outcome checks must follow invocation completion")
	}
	checked := map[string]bool{}
	passed := map[string]bool{}
	seen := map[string]bool{}
	for _, check := range v.Checks {
		key := check.Host + "/" + check.Name
		if !operationLabel(check.Name) || seen[key] || !containsString(m.Hosts, check.Host) {
			r.IncompleteChecks = append(r.IncompleteChecks, "Invalid, duplicate or out-of-scope outcome check")
			continue
		}
		seen[key] = true
		if check.Outcome == "pass" {
			checked[check.Host] = true
			passed[key] = true
		} else if check.Outcome == "fail" {
			addIAC(r, "WF-HEALTH", finding.SeverityHigh, "Post-run outcome check failed", safeReportText(key), "verify", m.Identity.Environment, "Independent supplied outcome is fail")
		} else {
			r.IncompleteChecks = append(r.IncompleteChecks, "Outcome check is unresolved: "+safeReportText(key))
		}
	}
	for _, h := range m.Hosts {
		if !checked[h] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required host lacks successful outcome verification: "+h)
		}
	}
	requiredHosts := map[string]bool{}
	requiredKeys := map[string]bool{}
	for _, check := range m.RequiredChecks {
		key := check.Host + "/" + check.Name
		if !containsString(m.Hosts, check.Host) || !operationLabel(check.Name) || requiredKeys[key] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Invalid or duplicate required outcome check")
			continue
		}
		requiredKeys[key] = true
		requiredHosts[check.Host] = true
		if !passed[key] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required outcome check has not passed: "+safeReportText(key))
		}
	}
	for _, host := range m.Hosts {
		if !requiredHosts[host] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Declare at least one required outcome check for host "+host)
		}
	}
	r.CompletedChecks = append(r.CompletedChecks, "Supplied independent outcome checks evaluated for this invocation; no live probes were executed")
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
