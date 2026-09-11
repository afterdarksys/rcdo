package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"git-tools/finding"
)

func hasAction(actions []string, target string) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func runPRManager(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "create" || args[0] == "update") {
		return runPRChange(args[0], args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "inspect" {
		args = args[1:]
	}
	var expectedCommit, collectPR, repository string
	var requiredChecks stringList
	_, options, err := parseFlags("pr-manager inspect", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		addProvenanceFlags(fs, &options)
		fs.StringVar(&expectedCommit, "expect-commit", "", "expected pull-request head SHA")
		fs.StringVar(&collectPR, "collect", "", "pull-request number, URL, or branch to collect using gh")
		fs.StringVar(&repository, "repo", "", "GitHub repository in OWNER/REPO form")
		fs.Var(&requiredChecks, "require-check", "required successful check name or status context; repeatable")
		return &options
	})
	if err != nil {
		return fmt.Errorf("pr-manager currently supports read-only inspect: %w", err)
	}
	var data []byte
	if collectPR != "" {
		ghArgs := []string{"pr", "view", collectPR, "--json", "number,title,headRefOid,mergeable,reviewDecision,statusCheckRollup,isDraft,url"}
		if repository != "" {
			ghArgs = append(ghArgs, "--repo", repository)
		}
		data, err = collectJSON("gh", ghArgs...)
	} else {
		data, err = readInput(options.input, stdin)
	}
	if err != nil {
		return emitReportOptions(stdout, options, finding.Report{IncompleteChecks: []string{err.Error()}})
	}
	object, err := decodeObject(data)
	if err != nil {
		return err
	}
	number := stringValue(object, "number")
	title := stringValue(object, "title")
	commit := stringValue(object, "headRefOid", "head_sha")
	if options.commit != "" && expectedCommit == "" {
		expectedCommit = options.commit
	}
	mergeable := strings.ToUpper(stringValue(object, "mergeable"))
	review := strings.ToUpper(stringValue(object, "reviewDecision", "review_decision"))
	resource := "pull-request-" + number
	if number == "" {
		resource = "pull-request"
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"GitHub pull-request state review"}, IncompleteChecks: []string{}}
	if title == "" || commit == "" {
		report.IncompleteChecks = append(report.IncompleteChecks, "pull-request title or head commit is missing")
	}
	if !oneOf(mergeable, "MERGEABLE", "CONFLICTING") {
		report.IncompleteChecks = append(report.IncompleteChecks, "pull-request mergeability is missing or unknown")
	}
	if _, present := object["statusCheckRollup"]; !present {
		report.IncompleteChecks = append(report.IncompleteChecks, "pull-request status checks are missing")
	}
	if expectedCommit != "" && commit != "" && commit != expectedCommit {
		report.Findings = append(report.Findings, makeFinding(
			"PR-COMMIT-001", finding.SeverityCritical, "Pull request uses an unexpected commit", resource,
			"review", options.environment, "The pull-request head does not match the expected commit.",
			fmt.Sprintf("expected %s; got %s", expectedCommit, commit), "Refresh the review against the current intended commit.",
		))
	}
	if mergeable == "CONFLICTING" {
		report.Findings = append(report.Findings, makeFinding(
			"PR-CONFLICT-001", finding.SeverityHigh, "Pull request has merge conflicts", resource,
			"merge", options.environment, "GitHub reports the pull request as conflicting.", "mergeable: "+mergeable,
			"Resolve conflicts and repeat all change reviews on the resulting commit.",
		))
	}
	if review != "APPROVED" {
		report.Findings = append(report.Findings, makeFinding(
			"PR-REVIEW-001", finding.SeverityWarning, "Pull request is not approved", resource,
			"merge", options.environment, "The review decision is not APPROVED.", "review decision: "+emptyValue(review),
			"Obtain the required independent review before merge or deployment.",
		))
	}
	checks, _ := json.Marshal(object["statusCheckRollup"])
	if regexpContains(checks, `(?i)"(conclusion|state|status)"\s*:\s*"(failure|failed|error|cancelled|canceled|timed_out)"`) {
		report.Findings = append(report.Findings, makeFinding(
			"PR-CHECK-001", finding.SeverityHigh, "Pull request has a failing check", resource,
			"merge", options.environment, "At least one status check did not succeed.", "matched failing status check",
			"Open the failing check, resolve the cause, and rerun it on the same commit.",
		))
	}
	checkItems, valid := object["statusCheckRollup"].([]any)
	if !valid {
		report.IncompleteChecks = append(report.IncompleteChecks, "Pull-request check results have no valid array")
	}
	success := map[string]bool{}
	seen := map[string]bool{}
	for i, raw := range checkItems {
		item, ok := raw.(map[string]any)
		name := stringValue(item, "name", "context")
		if !ok || name == "" || seen[name] {
			report.IncompleteChecks = append(report.IncompleteChecks, fmt.Sprintf("Check %d has missing, invalid or duplicate identity", i+1))
			continue
		}
		seen[name] = true
		state := strings.ToUpper(stringValue(item, "state"))
		status := strings.ToUpper(stringValue(item, "status"))
		conclusion := strings.ToUpper(stringValue(item, "conclusion"))
		if state != "" {
			success[name] = state == "SUCCESS"
			if !oneOf(state, "SUCCESS", "FAILURE", "ERROR") {
				report.IncompleteChecks = append(report.IncompleteChecks, "Status check is pending or unknown: "+safeReportText(name))
			}
		} else {
			success[name] = status == "COMPLETED" && conclusion == "SUCCESS"
			if status != "COMPLETED" || !oneOf(conclusion, "SUCCESS", "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE") {
				report.IncompleteChecks = append(report.IncompleteChecks, "Check completion or success is unverified: "+safeReportText(name))
			} else if !success[name] && !oneOf(conclusion, "FAILURE", "CANCELLED", "TIMED_OUT") {
				addIAC(&report, "PR-CHECK", finding.SeverityHigh, "Pull request check did not succeed", safeReportText(name), "review", options.environment, "Conclusion: "+conclusion)
			}
		}
	}
	for _, name := range requiredChecks {
		if !success[name] {
			report.IncompleteChecks = append(report.IncompleteChecks, "Required successful check is missing: "+safeReportText(name))
		}
	}
	bindReportSource(&report, options, "pr-manager", data)
	return emitReportOptions(stdout, options, report)
}

func recursiveString(object map[string]any, keys ...string) string {
	if value := stringValue(object, keys...); value != "" {
		return value
	}
	for _, key := range sortedKeys(object) {
		switch child := object[key].(type) {
		case map[string]any:
			if value := recursiveString(child, keys...); value != "" {
				return value
			}
		case []any:
			for _, item := range child {
				if nested, ok := item.(map[string]any); ok {
					if value := recursiveString(nested, keys...); value != "" {
						return value
					}
				}
			}
		}
	}
	return ""
}

func regexpContains(data []byte, pattern string) bool {
	matched, _ := regexp.Match(pattern, data)
	return matched
}

func emptyValue(value string) string {
	if value == "" {
		return "missing"
	}
	return value
}
