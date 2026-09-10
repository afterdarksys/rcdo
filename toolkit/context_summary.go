package toolkit

import (
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"reflect"
	"strings"
	"time"
)

type contextObservation struct {
	Provenance  []string          `json:"provenance,omitempty"`
	Kind        string            `json:"kind"`
	Values      map[string]string `json:"values"`
	CollectedAt string            `json:"collected_at"`
	Source      string            `json:"source"`
	Outcome     string            `json:"outcome"`
	ExpiresAt   string            `json:"expires_at,omitempty"`
}
type contextBundle struct {
	SchemaVersion string                        `json:"schema_version"`
	Complete      *bool                         `json:"complete"`
	Contexts      map[string]contextObservation `json:"contexts"`
}
type contextExpectations struct {
	SchemaVersion string `json:"schema_version"`
	Name          string `json:"name"`
	Contexts      map[string]struct {
		Kind   string            `json:"kind"`
		Values map[string]string `json:"values"`
	} `json:"contexts"`
}

var contextFields = map[string][]string{
	"aws": {"account", "region", "profile", "principal"}, "alicloud": {"account", "region", "profile", "principal"},
	"kubernetes": {"cluster", "namespace", "server", "user"}, "terraform": {"workspace", "backend", "backend_key", "engine_version", "backend_bucket", "backend_region", "backend_container", "backend_account", "backend_prefix"}, "tofu": {"workspace", "backend", "backend_key", "engine_version", "backend_bucket", "backend_region", "backend_container", "backend_account", "backend_prefix"},
	"docker": {"endpoint", "daemon_id"}, "spacelift": {"account", "stack", "run", "commit", "endpoint", "principal", "state", "needs_approval", "is_most_recent"}, "ansible": {"inventory_sha256", "limit", "user", "inventory_hosts_sha256", "host_count"},
}

func contextValuesValid(kind string, values map[string]string) bool {
	allowed, ok := contextFields[kind]
	if !ok || len(values) == 0 {
		return false
	}
	for key, value := range values {
		if !oneOf(key, allowed...) || !operationLabel(value) {
			return false
		}
	}
	return true
}
func readContextBundle(data []byte) (contextBundle, error) {
	var b contextBundle
	if strictJSON(data, &b) != nil || b.SchemaVersion != "1" || b.Contexts == nil {
		return b, fmt.Errorf("invalid context bundle")
	}
	for name, o := range b.Contexts {
		if !operationLabel(name) || !contextValuesValid(o.Kind, o.Values) {
			return b, fmt.Errorf("invalid context observation fields")
		}
	}
	return b, nil
}
func runContextSummary(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var expected, before string
	var age time.Duration
	_, o, err := parseFlags("context-summary", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&expected, "expect", "", "JSON required cross-tool contexts")
		fs.StringVar(&before, "before", "", "previous context bundle to detect changes")
		fs.DurationVar(&age, "max-age", 5*time.Minute, "maximum observation age")
		return &o
	})
	if err != nil {
		return err
	}
	if expected == "" || age <= 0 || o.policy != "" {
		return fmt.Errorf("--expect and positive --max-age are required; identity checks cannot be suppressed")
	}
	var want contextExpectations
	if err := readStrictJSONFile(expected, &want); err != nil {
		return err
	}
	if want.SchemaVersion != "1" || !operationLabel(want.Name) || len(want.Contexts) == 0 {
		return fmt.Errorf("expectations require version 1, a name and required contexts")
	}
	raw, err := readInput(o.input, stdin)
	if err != nil {
		return err
	}
	got, err := readContextBundle(raw)
	if err != nil {
		return err
	}
	r := finding.Report{CompletedChecks: []string{"Cross-tool context: " + want.Name, "Supplied observation comparison; this invocation does not switch contexts or independently acquire identities", "Source SHA-256: " + digestBytes(raw)}}
	now := time.Now().UTC()
	if got.Complete == nil || !*got.Complete {
		r.IncompleteChecks = append(r.IncompleteChecks, "Context collection is incomplete")
	}
	for _, name := range sortedContextNames(want) {
		w := want.Contexts[name]
		if !operationLabel(name) || !contextValuesValid(w.Kind, w.Values) {
			return fmt.Errorf("invalid context expectation fields")
		}
		g, ok := got.Contexts[name]
		if !ok {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required context missing: "+name)
			continue
		}
		checkFresh(&r, name, g.CollectedAt, age, now)
		if !operationLabel(g.Source) || g.Outcome != "pass" {
			r.IncompleteChecks = append(r.IncompleteChecks, "Context collector did not complete: "+name)
		}
		if g.ExpiresAt != "" {
			expires, e := time.Parse(time.RFC3339Nano, g.ExpiresAt)
			if e != nil {
				r.IncompleteChecks = append(r.IncompleteChecks, "Invalid credential expiry: "+name)
			} else if !expires.After(now) {
				addIAC(&r, "CTX-EXPIRED", finding.SeverityCritical, "Observed credentials have expired", name, "verify", o.environment, "Expiry: "+g.ExpiresAt)
			} else {
				r.CompletedChecks = append(r.CompletedChecks, name+" credentials expire: "+g.ExpiresAt)
			}
		}
		if g.Kind != w.Kind {
			addIAC(&r, "CTX-KIND", finding.SeverityCritical, "Context kind mismatch", name, "verify", o.environment, "Expected "+w.Kind+"; observed "+g.Kind)
		}
		for _, key := range sortedStringValues(w.Values) {
			value, ok := g.Values[key]
			if !ok {
				r.IncompleteChecks = append(r.IncompleteChecks, name+": required field missing: "+key)
			} else if value != w.Values[key] {
				addIAC(&r, "CTX-MISMATCH", finding.SeverityCritical, "Context does not match expected "+key, name, "verify", o.environment, "Expected: "+w.Values[key]+"; observed: "+value)
			} else {
				r.CompletedChecks = append(r.CompletedChecks, name+" "+key+": "+value)
			}
		}
	}
	if before != "" {
		previous, err := readConfigSource(before)
		if err != nil {
			return err
		}
		old, err := readContextBundle(previous)
		if err != nil {
			return err
		}
		for _, name := range sortedContextNames(want) {
			a, aok := old.Contexts[name]
			b, bok := got.Contexts[name]
			if aok != bok || a.Kind != b.Kind || !reflect.DeepEqual(a.Values, b.Values) {
				addIAC(&r, "CTX-CHANGED", finding.SeverityHigh, "Active context changed since previous observation", name, "compare", o.environment, "Review changed identity fields before continuing; previous evidence is historical")
			}
		}
	}
	for name := range got.Contexts {
		if _, ok := want.Contexts[name]; !ok {
			r.CompletedChecks = append(r.CompletedChecks, "Unrequired context not checked: "+strings.TrimSpace(name))
		}
	}
	return emitReportOptions(stdout, o, r)
}
func sortedContextNames(e contextExpectations) []string {
	m := map[string]any{}
	for k := range e.Contexts {
		m[k] = true
	}
	return sortedKeys(m)
}
