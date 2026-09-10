package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"git-tools/finding"
)

type iacChange struct {
	Actions         []string        `json:"actions"`
	Before          any             `json:"before"`
	After           any             `json:"after"`
	AfterUnknown    any             `json:"after_unknown"`
	BeforeSensitive any             `json:"before_sensitive"`
	AfterSensitive  any             `json:"after_sensitive"`
	ReplacePaths    [][]any         `json:"replace_paths"`
	Importing       json.RawMessage `json:"importing"`
}
type iacResource struct {
	Address         string    `json:"address"`
	PreviousAddress string    `json:"previous_address"`
	Deposed         string    `json:"deposed"`
	Type            string    `json:"type"`
	Mode            string    `json:"mode"`
	ActionReason    string    `json:"action_reason"`
	Change          iacChange `json:"change"`
}
type tofuPlan struct {
	FormatVersion    string               `json:"format_version"`
	TerraformVersion string               `json:"terraform_version"`
	Errored          bool                 `json:"errored"`
	Complete         *bool                `json:"complete"`
	DeferredChanges  []json.RawMessage    `json:"deferred_changes"`
	ResourceChanges  []iacResource        `json:"resource_changes"`
	ResourceDrift    []iacResource        `json:"resource_drift"`
	OutputChanges    map[string]iacChange `json:"output_changes"`
	Checks           []struct {
		Status  string `json:"status"`
		Address struct {
			ToDisplay string `json:"to_display"`
		} `json:"address"`
	} `json:"checks"`
}

type iacLimits struct {
	MaxDeletes        *int     `json:"max_deletes"`
	MaxReplacements   *int     `json:"max_replacements"`
	CriticalResources []string `json:"critical_resources"`
}

func decodePlan(data []byte) (tofuPlan, error) {
	var p tofuPlan
	if err := validateConfigDocument("json", data); err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("invalid plan JSON structure")
	}
	for _, res := range append(append([]iacResource{}, p.ResourceChanges...), p.ResourceDrift...) {
		if err := validateChangeMasks(res.Change); err != nil {
			return p, err
		}
	}
	for _, c := range p.OutputChanges {
		if err := validateChangeMasks(c); err != nil {
			return p, err
		}
	}
	return p, nil
}
func validateChangeMasks(c iacChange) error {
	for _, pair := range []struct{ mask, value any }{{c.BeforeSensitive, c.Before}, {c.AfterSensitive, c.After}, {c.AfterUnknown, c.After}} {
		if err := validateValueMask(pair.mask, pair.value); err != nil {
			return err
		}
	}
	return nil
}
func validateValueMask(mask, value any) error {
	switch m := mask.(type) {
	case nil, bool:
		return nil
	case map[string]any:
		v, ok := value.(map[string]any)
		if value != nil && !ok {
			return fmt.Errorf("plan mask does not match value shape")
		}
		for k, child := range m {
			if err := validateValueMask(child, v[k]); err != nil {
				return err
			}
		}
	case []any:
		v, ok := value.([]any)
		if value != nil && !ok {
			return fmt.Errorf("plan mask does not match value shape")
		}
		for i, child := range m {
			var item any
			if i < len(v) {
				item = v[i]
			}
			if err := validateValueMask(child, item); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid plan sensitivity or unknown-value mask")
	}
	return nil
}
func addIAC(r *finding.Report, prefix string, severity finding.Severity, title, resource, action, env, evidence string) {
	if resource == "" {
		resource = "plan"
	}
	if action == "" {
		action = "review"
	}
	r.Findings = append(r.Findings, makeFinding(stableFindingID(r, prefix, resource, action, evidence), severity, title, resource, action, env, title, evidence, "Review this item in the source evidence; resolve outstanding requirements before proceeding."))
}
func runTofuCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var planFile, engine, limitsFile string
	_, o, err := parseFlags("tofu-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&planFile, "plan", "", "saved plan to convert with the selected engine")
		fs.StringVar(&engine, "engine", "tofu", "saved-plan reader: tofu or terraform")
		fs.StringVar(&limitsFile, "limits", "", "JSON impact limits and exact critical resource addresses")
		return &o
	})
	if err != nil {
		return err
	}
	if !oneOf(engine, "tofu", "terraform") {
		return fmt.Errorf("--engine must be tofu or terraform")
	}
	if planFile != "" && o.input != "-" {
		return fmt.Errorf("use either --plan or --input")
	}
	var data []byte
	if planFile != "" {
		data, err = collectJSON(engine, "show", "-json", planFile)
	} else {
		data, err = readInput(o.input, stdin)
	}
	if err != nil {
		return emitReportOptions(stdout, o, finding.Report{IncompleteChecks: []string{"plan collection failed; verify reader, file and credentials"}})
	}
	p, err := decodePlan(data)
	if err != nil {
		return err
	}
	var limits iacLimits
	if limitsFile != "" {
		if err := readStrictJSONFile(limitsFile, &limits); err != nil {
			return err
		}
	}
	if (limits.MaxDeletes != nil && *limits.MaxDeletes < 0) || (limits.MaxReplacements != nil && *limits.MaxReplacements < 0) {
		return fmt.Errorf("impact limits must be nonnegative")
	}
	r := reviewIAC(p, o.environment, limits)
	if planFile != "" {
		r.CompletedChecks = append(r.CompletedChecks, "Saved-plan reader: "+engine)
	}
	return emitReportOptions(stdout, o, r)
}
func reviewIAC(p tofuPlan, env string, limits iacLimits) finding.Report {
	r := finding.Report{CompletedChecks: []string{"Terraform/OpenTofu plan review"}, Findings: []finding.Finding{}, IncompleteChecks: []string{}}
	if strings.SplitN(p.FormatVersion, ".", 2)[0] != "1" {
		r.IncompleteChecks = append(r.IncompleteChecks, "missing or unsupported plan format_version")
	}
	if p.TerraformVersion != "" {
		r.CompletedChecks = append(r.CompletedChecks, "Plan producer version: "+p.TerraformVersion+" (version field does not identify the engine)")
	}
	if p.Errored {
		addIAC(&r, "TOFU-ERRORED", finding.SeverityCritical, "Plan reports a planning error", "plan", "plan", env, "errored: true")
	}
	if p.Complete != nil && !*p.Complete || len(p.DeferredChanges) > 0 {
		r.IncompleteChecks = append(r.IncompleteChecks, "Plan is partial or contains deferred changes")
	}
	if p.ResourceChanges == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "resource_changes is missing; provide saved-plan JSON")
		return r
	}
	deletes, replacements := 0, 0
	seen := map[string]bool{}
	for _, res := range p.ResourceChanges {
		key := res.Address + "\x00" + res.Deposed
		if res.Address == "" || seen[key] {
			r.IncompleteChecks = append(r.IncompleteChecks, "missing or duplicate resource identity")
			continue
		}
		seen[key] = true
		c := res.Change
		actions := strings.Join(c.Actions, ",")
		if !oneOf(actions, "no-op", "create", "read", "update", "delete", "delete,create", "create,delete", "forget", "create,forget") {
			r.IncompleteChecks = append(r.IncompleteChecks, res.Address+": missing or unsupported action sequence")
			continue
		}
		critical := containsAny(strings.ToLower(res.Type), "database", "db_", "rds", "bucket", "storage", "disk", "volume", "redis", "cache")
		for _, address := range limits.CriticalResources {
			if address == res.Address {
				critical = true
			}
		}
		severity := finding.SeverityHigh
		if critical {
			severity = finding.SeverityCritical
		}
		if hasAction(c.Actions, "delete") {
			deletes++
			if hasAction(c.Actions, "create") {
				replacements++
				addIAC(&r, "TOFU-REPLACE", severity, "Resource will be replaced", res.Address, actions, env, replacementEvidence(res))
			} else {
				addIAC(&r, "TOFU-DELETE", severity, "Resource will be deleted", res.Address, actions, env, "Verify backups, dependents and retention; reason: "+emptyValue(res.ActionReason))
			}
		}
		if hasAction(c.Actions, "forget") {
			addIAC(&r, "TOFU-FORGET", finding.SeverityHigh, "Resource leaves state management", res.Address, actions, env, "The resource is not necessarily destroyed; ownership and cleanup must be verified")
		}
		if res.PreviousAddress != "" {
			addIAC(&r, "TOFU-MOVE", finding.SeverityInfo, "Resource address moved", res.Address, actions, env, "Previous address: "+res.PreviousAddress)
		}
		if len(c.Importing) > 0 && string(c.Importing) != "null" {
			addIAC(&r, "TOFU-IMPORT", finding.SeverityWarning, "Existing resource will be imported", res.Address, actions, env, "Verify actual ownership and target identity; import identifier withheld")
		}
		unknowns := markerPaths(c.AfterUnknown, "")
		for _, path := range unknowns {
			addIAC(&r, "TOFU-UNKNOWN", finding.SeverityWarning, "Planned value is unknown until apply", res.Address, actions, env, "Attribute: "+path)
		}
		reviewSecurity(&r, res, env)
	}
	if limits.MaxDeletes != nil && deletes > *limits.MaxDeletes {
		addIAC(&r, "TOFU-LIMIT", finding.SeverityCritical, "Deletion limit exceeded", "plan", "delete", env, fmt.Sprintf("%d deletions; limit %d", deletes, *limits.MaxDeletes))
	}
	if limits.MaxReplacements != nil && replacements > *limits.MaxReplacements {
		addIAC(&r, "TOFU-LIMIT", finding.SeverityCritical, "Replacement limit exceeded", "plan", "replace", env, fmt.Sprintf("%d replacements; limit %d", replacements, *limits.MaxReplacements))
	}
	for _, res := range p.ResourceDrift {
		if strings.Join(res.Change.Actions, ",") != "no-op" {
			addIAC(&r, "TOFU-DRIFT", finding.SeverityWarning, "Resource drift detected", res.Address, "drift", env, "Observed actions: "+strings.Join(res.Change.Actions, ","))
		}
	}
	for _, c := range p.Checks {
		switch c.Status {
		case "pass":
		case "fail", "error":
			addIAC(&r, "TOFU-CHECK", finding.SeverityHigh, "Infrastructure check failed", c.Address.ToDisplay, "check", env, "status: "+c.Status)
		default:
			r.IncompleteChecks = append(r.IncompleteChecks, "Check result unavailable: "+emptyValue(c.Address.ToDisplay))
		}
	}
	for _, name := range sortedKeysChanges(p.OutputChanges) {
		c := p.OutputChanges[name]
		if strings.Join(c.Actions, ",") != "no-op" || len(markerPaths(c.AfterUnknown, "")) > 0 {
			addIAC(&r, "TOFU-OUTPUT", finding.SeverityWarning, "Output changes may affect consumers", "output."+name, "output", env, "Actions: "+strings.Join(c.Actions, ",")+"; values withheld; verify downstream consumers")
		}
	}
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("Impact totals: %d deletions including %d replacements", deletes, replacements))
	return r
}
func replacementEvidence(res iacResource) string {
	order := "destroy existing resource, then create replacement"
	if res.Change.Actions[0] == "create" {
		order = "create replacement, then destroy existing resource"
	}
	paths := []string{}
	for _, p := range res.Change.ReplacePaths {
		b, _ := json.Marshal(p)
		paths = append(paths, string(b))
	}
	return "Order: " + order + "; reason: " + emptyValue(res.ActionReason) + "; replacement paths: " + strings.Join(paths, ", ")
}
func sortedKeysChanges(m map[string]iacChange) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func markerPaths(v any, path string) []string {
	out := []string{}
	switch x := v.(type) {
	case bool:
		if x {
			if path == "" {
				path = "$"
			}
			out = append(out, path)
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			out = append(out, markerPaths(x[k], path+"["+fmt.Sprintf("%q", k)+"]")...)
		}
	case []any:
		for i, v := range x {
			out = append(out, markerPaths(v, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return out
}
func maskChild(v any, key string, index int) any {
	switch x := v.(type) {
	case bool:
		return x
	case map[string]any:
		return x[key]
	case []any:
		if index >= 0 && index < len(x) {
			return x[index]
		}
	}
	return nil
}
func isMarked(v any) bool { b, _ := v.(bool); return b }

// Walk raw values for comparison; render only after applying BOTH sensitivity masks.
func changeLines(c iacChange) []string {
	out := []string{}
	var walk func(any, any, any, any, any, string, bool, bool)
	walk = func(b, a, u, bs, as any, path string, bExists, aExists bool) {
		if isMarked(bs) || isMarked(as) || isSensitivePath(path) {
			if !reflect.DeepEqual(b, a) || bExists != aExists || isMarked(u) {
				out = append(out, path+": sensitive value changed [REDACTED]")
			}
			return
		}
		if isMarked(u) {
			out = append(out, path+": unknown until apply")
			return
		}
		bm, bok := b.(map[string]any)
		am, aok := a.(map[string]any)
		if bok || aok {
			keys := map[string]any{}
			for k := range bm {
				keys[k] = true
			}
			for k := range am {
				keys[k] = true
			}
			if um, ok := u.(map[string]any); ok {
				for k := range um {
					keys[k] = true
				}
			}
			for _, k := range sortedKeys(keys) {
				bv, be := bm[k]
				av, ae := am[k]
				walk(bv, av, maskChild(u, k, -1), maskChild(bs, k, -1), maskChild(as, k, -1), path+"["+fmt.Sprintf("%q", k)+"]", be, ae)
			}
			return
		}
		ba, bok := b.([]any)
		aa, aok := a.([]any)
		if bok || aok {
			n := len(ba)
			if len(aa) > n {
				n = len(aa)
			}
			for i := 0; i < n; i++ {
				var bv, av any
				if i < len(ba) {
					bv = ba[i]
				}
				if i < len(aa) {
					av = aa[i]
				}
				walk(bv, av, maskChild(u, "", i), maskChild(bs, "", i), maskChild(as, "", i), fmt.Sprintf("%s[%d]", path, i), i < len(ba), i < len(aa))
			}
			return
		}
		if reflect.DeepEqual(b, a) && bExists == aExists {
			return
		}
		show := func(v any, exists bool) string {
			if !exists {
				return "absent"
			}
			if v == nil {
				return "null"
			}
			data, _ := json.Marshal(v)
			return redactAIText(string(data))
		}
		out = append(out, path+": "+show(b, bExists)+" -> "+show(a, aExists))
	}
	walk(c.Before, c.After, c.AfterUnknown, c.BeforeSensitive, c.AfterSensitive, "$", true, true)
	return out
}
