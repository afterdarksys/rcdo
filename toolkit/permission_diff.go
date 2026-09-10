package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"net/netip"
	"sort"
)

func permissionStatements(raw []byte) (map[string]map[string]any, error) {
	if validateConfigDocument("json", raw) != nil {
		return nil, fmt.Errorf("invalid policy JSON")
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&document) != nil {
		return nil, fmt.Errorf("policy must be an object")
	}
	for key := range document {
		if !oneOf(key, "Version", "Id", "Statement") {
			return nil, fmt.Errorf("unsupported policy element %s", key)
		}
	}
	value, ok := document["Statement"]
	if !ok {
		return nil, fmt.Errorf("policy requires Statement")
	}
	list, ok := value.([]any)
	if !ok {
		list = []any{value}
	}
	if len(list) > 1000 {
		return nil, fmt.Errorf("policy exceeds 1000 statements")
	}
	result := map[string]map[string]any{}
	for _, value := range list {
		s, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid policy statement")
		}
		for key := range s {
			if !oneOf(key, "Sid", "Effect", "Action", "NotAction", "Resource", "NotResource", "Principal", "NotPrincipal", "Condition") {
				return nil, fmt.Errorf("unsupported statement element %s", key)
			}
		}
		effect, _ := s["Effect"].(string)
		if !oneOf(effect, "Allow", "Deny") {
			return nil, fmt.Errorf("statement requires Allow or Deny")
		}
		if (s["Action"] == nil) == (s["NotAction"] == nil) {
			return nil, fmt.Errorf("statement requires exactly one of Action or NotAction")
		}
		if s["Resource"] != nil && s["NotResource"] != nil || s["Principal"] != nil && s["NotPrincipal"] != nil {
			return nil, fmt.Errorf("conflicting policy scope")
		}
		if s["Resource"] == nil && s["NotResource"] == nil && s["Principal"] == nil && s["NotPrincipal"] == nil {
			return nil, fmt.Errorf("statement requires a resource or trust principal")
		}
		for _, key := range []string{"Action", "NotAction", "Resource", "NotResource"} {
			if s[key] != nil {
				v, e := permissionStringSet(s[key])
				if e != nil {
					return nil, e
				}
				s[key] = v
			}
		}
		if c, ok := s["Condition"]; ok {
			if _, ok := c.(map[string]any); !ok {
				return nil, fmt.Errorf("Condition must be an object")
			}
		}
		for _, key := range []string{"Principal", "NotPrincipal"} {
			if value, exists := s[key]; exists {
				switch p := value.(type) {
				case string:
					if !operationLabel(p) {
						return nil, fmt.Errorf("invalid principal")
					}
				case map[string]any:
					if len(p) == 0 {
						return nil, fmt.Errorf("empty principal")
					}
					for kind, value := range p {
						normalized, err := permissionStringSet(value)
						if err != nil || !operationLabel(kind) {
							return nil, fmt.Errorf("invalid principal scope")
						}
						p[kind] = normalized
					}
				default:
					return nil, fmt.Errorf("principal requires a string or typed principal object")
				}
			}
		}
		delete(s, "Sid") // Labels do not change permission scope.
		b, _ := json.Marshal(s)
		result[string(b)] = s
	}
	return result, nil
}
func permissionJSON(value any) []byte {
	if value == nil {
		return []byte(`{"Statement":[]}`)
	}
	if s, ok := value.(string); ok {
		return []byte(s)
	}
	raw, _ := json.Marshal(value)
	return raw
}
func permissionStringSet(value any) ([]string, error) {
	list, ok := value.([]any)
	if !ok {
		list = []any{value}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("empty policy scope")
	}
	set := map[string]bool{}
	for _, v := range list {
		s, ok := v.(string)
		if !ok || !operationLabel(s) {
			return nil, fmt.Errorf("invalid policy scope value")
		}
		set[s] = true
	}
	out := []string{}
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}
func explainPermissionDelta(r *finding.Report, before, after []byte, resource, env string) error {
	b, err := permissionStatements(before)
	if err != nil {
		return err
	}
	a, err := permissionStatements(after)
	if err != nil {
		return err
	}
	for _, side := range []struct {
		current, other map[string]map[string]any
		added          bool
	}{{a, b, true}, {b, a, false}} {
		keys := []string{}
		for key := range side.current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, ok := side.other[key]; ok {
				continue
			}
			s := side.current[key]
			effect := s["Effect"].(string)
			title := "Allow clause removed: this grant no longer appears"
			severity := finding.SeverityWarning
			if side.added && effect == "Allow" {
				title = "Allow clause added: potential permission grant"
				severity = finding.SeverityHigh
			}
			if side.added && effect == "Deny" {
				title = "Deny clause added: explicit restriction introduced"
			}
			if !side.added && effect == "Deny" {
				title = "Deny clause removed: explicit restriction lifted"
				severity = finding.SeverityHigh
			}
			scope := "Statement scope: " + auditText(key) + ". Other policies and runtime conditions determine effective access."
			addIAC(r, "PERMISSION", severity, title, resource, "compare", env, scope)
			if s["Condition"] != nil || s["NotAction"] != nil || s["NotResource"] != nil || s["NotPrincipal"] != nil {
				r.IncompleteChecks = append(r.IncompleteChecks, "Conditional or complemented scope changed; effective access cannot be established from these documents")
			}
		}
	}
	return nil
}

type networkRule struct {
	ID        string `json:"id"`
	Direction string `json:"direction"`
	Protocol  string `json:"protocol"`
	From      int    `json:"from_port"`
	To        int    `json:"to_port"`
	CIDR      string `json:"cidr"`
}

func permissionNetwork(raw []byte) (map[string]networkRule, error) {
	var doc struct {
		SchemaVersion string        `json:"schema_version"`
		Rules         []networkRule `json:"rules"`
	}
	if strictJSON(raw, &doc) != nil || doc.SchemaVersion != "1" || doc.Rules == nil || len(doc.Rules) > 10000 {
		return nil, fmt.Errorf("invalid normalized network rules")
	}
	var presence struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if json.Unmarshal(raw, &presence) != nil {
		return nil, fmt.Errorf("invalid rule fields")
	}
	for _, fields := range presence.Rules {
		for _, key := range []string{"from_port", "to_port"} {
			if len(fields[key]) == 0 || string(fields[key]) == "null" {
				return nil, fmt.Errorf("network rules require explicit port bounds")
			}
		}
	}
	out := map[string]networkRule{}
	for _, r := range doc.Rules {
		p, err := netip.ParsePrefix(r.CIDR)
		if err != nil || !operationLabel(r.ID) || !oneOf(r.Direction, "ingress", "egress") || !oneOf(r.Protocol, "tcp", "udp", "all") || r.From < 0 || r.To > 65535 || r.From > r.To {
			return nil, fmt.Errorf("invalid rule; supported protocols are tcp, udp, all")
		}
		if r.Protocol == "all" && (r.From != 0 || r.To != 65535) {
			return nil, fmt.Errorf("all protocol requires port bounds 0..65535")
		}
		if _, ok := out[r.ID]; ok {
			return nil, fmt.Errorf("duplicate rule identity")
		}
		r.CIDR = p.Masked().String()
		out[r.ID] = r
	}
	return out, nil
}
func explainNetworkDelta(r *finding.Report, before, after []byte, env string) error {
	b, err := permissionNetwork(before)
	if err != nil {
		return err
	}
	a, err := permissionNetwork(after)
	if err != nil {
		return err
	}
	ids := map[string]any{}
	for id := range a {
		ids[id] = true
	}
	for id := range b {
		ids[id] = true
	}
	for _, id := range sortedKeys(ids) {
		old, bok := b[id]
		now, aok := a[id]
		if bok && aok && old == now {
			continue
		}
		title := "Network rule removed: this path is no longer allowed by this rule"
		severity := finding.SeverityWarning
		if aok {
			title = "Network rule added or changed: review reachable scope"
			severity = finding.SeverityHigh
		}
		if aok && bok && old.Direction == now.Direction && old.Protocol == now.Protocol {
			bp, _ := netip.ParsePrefix(old.CIDR)
			ap, _ := netip.ParsePrefix(now.CIDR)
			if ap.Addr().BitLen() == bp.Addr().BitLen() && ap.Contains(bp.Addr()) && ap.Bits() <= bp.Bits() && now.From <= old.From && now.To >= old.To {
				title = "Network rule broadened: address or port scope expanded"
			}
			if ap.Addr().BitLen() == bp.Addr().BitLen() && bp.Contains(ap.Addr()) && bp.Bits() <= ap.Bits() && old.From <= now.From && old.To >= now.To {
				title = "Network rule narrowed: address or port scope reduced"
				severity = finding.SeverityWarning
			}
		}
		format := func(v networkRule, exists bool) string {
			if !exists {
				return "absent"
			}
			return fmt.Sprintf("%s %s ports %d..%d CIDR %s", v.Direction, v.Protocol, v.From, v.To, v.CIDR)
		}
		addIAC(r, "NETWORK-SCOPE", severity, title, id, "compare", env, "Before: "+format(old, bok)+". After: "+format(now, aok)+". Other rules, routing and firewalls can change effective reachability.")
	}
	return nil
}
func runPermissionDiff(args []string, stdout, stderr io.Writer) error {
	var before, after, kind string
	_, o, err := parseFlags("permission-diff", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&before, "before", "", "previous policy or normalized rules")
		fs.StringVar(&after, "after", "", "new policy or normalized rules")
		fs.StringVar(&kind, "kind", "aws", "aws, ram or network")
		return &o
	})
	if err != nil {
		return err
	}
	if before == "" || after == "" || !oneOf(kind, "aws", "ram", "network") || o.input != "-" || o.policy != "" {
		return fmt.Errorf("requires --before, --after and kind aws/ram/network; suppression is unsupported")
	}
	b, err := readConfigSource(before)
	if err != nil {
		return err
	}
	a, err := readConfigSource(after)
	if err != nil {
		return err
	}
	r := finding.Report{CompletedChecks: []string{"Document-level permission scope comparison; no provider call or effective-access simulation", "Before SHA-256: " + digestBytes(b), "After SHA-256: " + digestBytes(a)}}
	if kind == "network" {
		err = explainNetworkDelta(&r, b, a, o.environment)
	} else {
		err = explainPermissionDelta(&r, b, a, kind+" policy", o.environment)
	}
	if err != nil {
		return err
	}
	for i := range r.Findings {
		r.Findings[i].Reason = auditText(r.Findings[i].Reason)
		for j := range r.Findings[i].Evidence {
			r.Findings[i].Evidence[j] = auditText(r.Findings[i].Evidence[j])
		}
	}
	return emitReportOptions(stdout, o, r)
}
