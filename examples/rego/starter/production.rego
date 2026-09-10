package rcdo
import rego.v1
# Approval is a supplied observation, not verified authorization.
findings contains issue("production-safeguard", r, "Production mutation lacks a supplied approval observation.", "Obtain approval and provide evidence through your review process.") if {
 valid_input
 input.environment == "production"
 not input.approved
 some r in input.resources
 some a in r.actions
 a in {"create", "update", "delete", "forget"}
}
