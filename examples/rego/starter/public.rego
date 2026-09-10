package rcdo
import rego.v1
findings contains issue("public-exposure", r, "The normalized snapshot marks this resource public.", "Remove public exposure or review a documented exception.") if {
 valid_input
 some r in input.resources
 r.public
}
