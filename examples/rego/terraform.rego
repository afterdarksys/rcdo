package rcdo
import rego.v1

# Evaluate terraform/tofu show -json output. Flag managed resource deletions,
# including replacements (whose actions include delete).
findings := [f |
 some change in object.get(input, "resource_changes", [])
 change.mode == "managed"
 "delete" in change.change.actions
 f := {
  "id": change.address,
  "severity": "high",
  "title": "Resource deletion requires review",
  "resource": change.address,
  "reason": "The plan deletes or replaces this managed resource.",
  "remediation": "Review dependencies, backups, and approval before applying.",
 }
]

decision := {
 "allow": count(findings) == 0,
 "findings": findings,
 "incomplete": incomplete,
}

incomplete := ["Expected Terraform/OpenTofu plan JSON with format_version and well-formed resource_changes."] if {
 not valid_input
} else := []

valid_input if {
 is_object(input)
 is_string(input.format_version)
 is_array(input.resource_changes)
 every change in input.resource_changes {
  valid_change(change)
 }
}

valid_change(change) if {
 is_object(change)
 is_string(change.address)
 change.address != ""
 change.mode in {"managed", "data"}
 is_object(change.change)
 is_array(change.change.actions)
 count(change.change.actions) > 0
 every action in change.change.actions {
  action in {"no-op", "create", "read", "update", "delete", "forget"}
 }
}
