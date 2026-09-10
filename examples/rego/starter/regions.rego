package rcdo
import rego.v1
# Customize this allowlist; it is an example organizational policy.
findings contains issue("approved-region", r, "The resource region is outside the policy allowlist.", "Use us-east-1 or us-west-2, or amend the reviewed policy.") if {
 valid_input
 some r in input.resources
 not r.region in {"us-east-1", "us-west-2"}
}
