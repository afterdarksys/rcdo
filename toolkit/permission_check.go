package toolkit

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"git-tools/finding"
)

// Threats: a blind operator can approve an Allow that does not mean what it
// sounds like. permission-check matches one action and one resource against
// one policy document. Conditions, NotAction, NotResource, NotPrincipal, and
// unsupported wildcards are not evaluated and fail closed. Permission
// boundaries, service-control policies, session policies, and other documents
// are out of scope. A document allow is not effective access.

func runPermissionCheck(args []string, stdout, stderr io.Writer) error {
	var document, action, resource string
	_, options, err := parseFlags("permission-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&document, "document", "", "one IAM or RAM policy JSON document")
		fs.StringVar(&action, "action", "", "one action, such as s3:GetObject")
		fs.StringVar(&resource, "resource", "", "one resource identifier")
		return &options
	})
	if err != nil {
		return err
	}
	if document == "" || !operationLabel(action) || !operationLabel(resource) || options.input != "-" || options.policy != "" {
		return fmt.Errorf("permission-check requires --document, --action, and --resource; suppressions are unsupported")
	}
	raw, err := readConfigSource(document)
	if err != nil {
		return err
	}
	statements, err := permissionStatements(raw)
	if err != nil {
		return err
	}
	report := finding.Report{CompletedChecks: []string{"One policy document. Permission boundaries, service-control policies, session policies, and resource policies were not evaluated. Effective access is not established."}}
	actionOK, resourceOK := true, true
	if _, ok := policyGlob(action, action); !ok {
		actionOK = false
	}
	if _, ok := policyGlob(resource, resource); !ok {
		resourceOK = false
	}
	if !actionOK || !resourceOK {
		report.IncompleteChecks = append(report.IncompleteChecks, "The requested action or resource uses a wildcard this check does not evaluate")
		return emitReportOptions(stdout, options, report)
	}
	decision := "no-match"
	for _, statement := range statements {
		if statement["Condition"] != nil || statement["NotAction"] != nil || statement["NotResource"] != nil || statement["NotPrincipal"] != nil {
			matched, ok := statementCouldMatch(statement, action, resource)
			if !ok {
				report.IncompleteChecks = append(report.IncompleteChecks, "A statement uses a wildcard this check does not evaluate")
				return emitReportOptions(stdout, options, report)
			}
			if matched {
				report.IncompleteChecks = append(report.IncompleteChecks, "A matching statement has a condition or a complement. It was not treated as allow or deny.")
			}
			continue
		}
		actions, _ := statement["Action"].([]string)
		resources, _ := statement["Resource"].([]string)
		actionMatch, actionKnown := anyPolicyGlob(actions, action)
		resourceMatch, resourceKnown := anyPolicyGlob(resources, resource)
		if !actionKnown || !resourceKnown {
			report.IncompleteChecks = append(report.IncompleteChecks, "A statement uses a wildcard this check does not evaluate")
			return emitReportOptions(stdout, options, report)
		}
		if actionMatch && resourceMatch {
			if statement["Effect"] == "Deny" {
				decision = "deny"
			} else if decision != "deny" {
				decision = "allow"
			}
		}
	}
	switch decision {
	case "deny":
		report.Findings = append(report.Findings, makeFinding("PERMISSION-DECISION", finding.SeverityHigh, "This document explicitly denies the action", resource, action, options.environment, "An explicit Deny matched the action and resource.", "Policy SHA-256: "+digestBytes(raw), "Do not treat any other Allow in this document as permission to proceed."))
	case "allow":
		report.Findings = append(report.Findings, makeFinding("PERMISSION-DECISION", finding.SeverityInfo, "This document contains a matching Allow", resource, action, options.environment, "A matching Allow was found and no evaluated statement denied it.", "Policy SHA-256: "+digestBytes(raw), "This is not effective access. Evaluate the identity's other policies before approval."))
	default:
		report.Findings = append(report.Findings, makeFinding("PERMISSION-DECISION", finding.SeverityWarning, "This document does not allow the action", resource, action, options.environment, "No evaluated Allow matched the action and resource.", "Policy SHA-256: "+digestBytes(raw), "Do not infer a grant from a policy that does not match."))
	}
	return emitReportOptions(stdout, options, report)
}

func statementCouldMatch(statement map[string]any, action, resource string) (bool, bool) {
	actions, _ := statement["Action"].([]string)
	if statement["NotAction"] != nil {
		actions, _ = statement["NotAction"].([]string)
	}
	resources, _ := statement["Resource"].([]string)
	if statement["NotResource"] != nil {
		resources, _ = statement["NotResource"].([]string)
	}
	actionMatch, actionKnown := anyPolicyGlob(actions, action)
	resourceMatch, resourceKnown := anyPolicyGlob(resources, resource)
	if !actionKnown || !resourceKnown {
		return false, false
	}
	return actionMatch && resourceMatch, true
}

func anyPolicyGlob(patterns []string, value string) (bool, bool) {
	matched := false
	for _, pattern := range patterns {
		okMatch, known := policyGlob(pattern, value)
		if !known {
			return false, false
		}
		matched = matched || okMatch
	}
	return matched, true
}

func policyGlob(pattern, value string) (bool, bool) {
	if strings.ContainsAny(pattern, "?{[\\") || strings.ContainsAny(value, "?{[\\") {
		return false, false
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value, true
	}
	if !strings.HasPrefix(value, parts[0]) {
		return false, true
	}
	rest := value[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		if part == "" {
			continue
		}
		index := strings.Index(rest, part)
		if index < 0 {
			return false, true
		}
		rest = rest[index+len(part):]
	}
	return strings.HasSuffix(rest, parts[len(parts)-1]), true
}
