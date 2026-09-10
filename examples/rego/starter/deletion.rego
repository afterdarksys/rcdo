package rcdo
import rego.v1
findings contains issue("resource-deletion", r, "The action list includes deletion or forgetting a resource.", "Review dependencies and recovery before proceeding.") if {
 valid_input
 some r in input.resources
 some a in r.actions
 a in {"delete", "forget"}
}
