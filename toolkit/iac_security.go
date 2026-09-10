package toolkit

import (
	"encoding/json"
	"fmt"
	"git-tools/finding"
	"net/netip"
	"reflect"
	"strings"
)

func reviewSecurity(r *finding.Report, res iacResource, env string) {
	c := res.Change
	actions := strings.Join(c.Actions, ",")
	if actions == "delete" || actions == "forget" {
		return
	}
	var walk func(any, any, any, any, any, string)
	walk = func(b, a, u, bs, as any, path string) {
		security := containsAny(strings.ToLower(path), "cidr", "public", "ingress", "egress", "port", "protocol", "encrypt", "deletion_protection", "policy", "assume_role", "trust")
		if isMarked(u) {
			if security || path == "" {
				r.IncompleteChecks = append(r.IncompleteChecks, res.Address+": security evaluation requires unknown attribute "+path)
			}
			return
		}
		// Policy evaluation can compare sensitive values, but never emits their values.
		am, ok := a.(map[string]any)
		if ok {
			bm, _ := b.(map[string]any)
			for _, k := range sortedKeys(am) {
				walk(bm[k], am[k], maskChild(u, k, -1), maskChild(bs, k, -1), maskChild(as, k, -1), path+"."+k)
			}
			return
		}
		aa, ok := a.([]any)
		if ok {
			ba, _ := b.([]any)
			for i, v := range aa {
				var bv any
				if i < len(ba) {
					bv = ba[i]
				}
				walk(bv, v, maskChild(u, "", i), maskChild(bs, "", i), maskChild(as, "", i), fmt.Sprintf("%s[%d]", path, i))
			}
			return
		}
		changed := !reflect.DeepEqual(a, b)
		if changed && containsAny(path, "cidr") {
			bv, bok := b.(string)
			av, aok := a.(string)
			if bok && aok {
				before, be := netip.ParsePrefix(bv)
				after, ae := netip.ParsePrefix(av)
				if be == nil && ae == nil && before.Addr().BitLen() == after.Addr().BitLen() {
					label := "Network CIDR changed"
					severity := finding.SeverityWarning
					if after.Bits() < before.Bits() && after.Contains(before.Addr()) {
						label = "Network CIDR broadened"
						severity = finding.SeverityHigh
					}
					if before.Bits() < after.Bits() && before.Contains(after.Addr()) {
						label = "Network CIDR narrowed"
					}
					addIAC(r, "TOFU-CIDR", severity, label, res.Address, actions, env, "Attribute: "+path+"; IPv4/IPv6 prefix comparison; direction and other rules still require review")
				}
			}
		}

		public := a == "0.0.0.0/0" || a == "::/0" || (oneOf(path, ".public", ".publicly_accessible") && a == true)
		if public {
			severity := finding.SeverityWarning
			title := "Existing public access remains"
			if changed {
				severity = finding.SeverityCritical
				title = "Public access introduced"
			}
			addIAC(r, "TOFU-PUBLIC", severity, title, res.Address, actions, env, "Attribute: "+path+"; public-access indicator; value withheld")
		}
		if !changed {
			return
		}
		if a == false && containsAny(path, "encrypted", "encryption_enabled", "deletion_protection") {
			addIAC(r, "TOFU-PROTECTION", finding.SeverityCritical, "Protection disabled", res.Address, actions, env, "Attribute: "+path)
		}
		if containsAny(path, ".from_port", ".to_port", ".port_range", ".ip_protocol", ".protocol", ".cidr_ip", ".cidr_blocks", ".ipv6_cidr_blocks") {
			addIAC(r, "TOFU-NETWORK", finding.SeverityWarning, "Network rule changed", res.Address, actions, env, "Attribute: "+path+"; review protocol, range and direction together")
		}
		if containsAny(path, "policy", "assume_role", "trust") {
			addIAC(r, "TOFU-IAM", finding.SeverityHigh, "Permission or trust policy changed", res.Address, actions, env, "Attribute: "+path)
			if isMarked(bs) || isMarked(as) || isSensitivePath(path) {
				return
			}
			if err := explainPermissionDelta(r, permissionJSON(b), permissionJSON(a), res.Address, env); err != nil {
				r.IncompleteChecks = append(r.IncompleteChecks, res.Address+": policy semantics unavailable at "+path)
			}
		}
	}
	walk(c.Before, c.After, c.AfterUnknown, c.BeforeSensitive, c.AfterSensitive, "")
	// Unknown fields can be absent from after entirely.
	for _, p := range markerPaths(c.AfterUnknown, "") {
		if containsAny(strings.ToLower(p), "policy", "public", "cidr", "encrypt", "deletion_protection", "port", "trust") {
			r.IncompleteChecks = append(r.IncompleteChecks, res.Address+": security value unknown at "+p)
		}
	}
}

// Statement-level comparison preserves Effect, Resource, Principal and Condition
// together; it never mistakes a newly added Deny for an added Allow permission.
func policyAtoms(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	if s, ok := v.(string); ok {
		if json.Unmarshal([]byte(s), &v) != nil {
			return nil
		}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	statements := m["Statement"]
	if statements == nil {
		statements = m["statement"]
	}
	if statements == nil {
		return nil
	}
	list, ok := statements.([]any)
	if !ok {
		list = []any{statements}
	}
	out := map[string]any{}
	for _, s := range list {
		b, _ := json.Marshal(s)
		out[string(b)] = true
	}
	return out
}
