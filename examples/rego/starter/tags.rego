package rcdo
import rego.v1
# Customize required tags for your organization.
findings contains issue("required-tags", r, "Owner and Environment tags must be nonempty.", "Set the required tags on the resource.") if {
 valid_input
 some r in input.resources
 some key in ["Owner", "Environment"]
 trim_space(object.get(r.tags, key, "")) == ""
}
