package rcdo
import rego.v1

# Load this module plus one or more rule modules. Inputs are normalized snapshots,
# not raw provider responses. Missing facts are never interpreted as safe defaults.
decision := {"allow": count(findings) == 0, "findings": sort(findings), "incomplete": incomplete}
incomplete := [] if { valid_input } else := ["Expected normalized environment, approved, and resources with unique address, tags, region, public, and actions fields."]
valid_input if {
 is_object(input)
 is_string(input.environment)
 input.environment != ""
 is_boolean(input.approved)
 is_array(input.resources)
 every r in input.resources {
  is_object(r)
  is_string(r.address)
  r.address != ""
  is_object(r.tags)
  every k, v in r.tags { is_string(k); is_string(v) }
  is_string(r.region)
  r.region != ""
  is_boolean(r.public)
  is_array(r.actions)
  count(r.actions) > 0
  every a in r.actions { a in {"no-op", "create", "read", "update", "delete", "forget"} }
 }
 count({r.address | some r in input.resources}) == count(input.resources)
}
issue(rule, r, reason, remediation) := {"id": concat(":", [rule, r.address]), "severity": "high", "title": rule, "resource": r.address, "reason": reason, "remediation": remediation}
