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

type tofuPlan struct {
	FormatVersion   string `json:"format_version"`
	Errored         bool   `json:"errored"`
	ResourceChanges []struct {
		Address string `json:"address"`
		Type    string `json:"type"`
		Change  struct {
			Actions []string       `json:"actions"`
			Before  map[string]any `json:"before"`
			After   map[string]any `json:"after"`
		} `json:"change"`
	} `json:"resource_changes"`
	ResourceDrift []struct {
		Address string `json:"address"`
		Type    string `json:"type"`
		Change  struct {
			Actions []string `json:"actions"`
		} `json:"change"`
	} `json:"resource_drift"`
	Checks []struct {
		Status  string `json:"status"`
		Address struct {
			ToDisplay string `json:"to_display"`
		} `json:"address"`
	} `json:"checks"`
}

func runTofuCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var planFile string
	_, options, err := parseFlags("tofu-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&planFile, "plan", "", "collect JSON from this saved plan using tofu show -json")
		return &options
	})
	if err != nil {
		return err
	}
	var data []byte
	if planFile != "" {
		data, err = collectJSON("tofu", "show", "-json", planFile)
	} else {
		data, err = readInput(options.input, stdin)
	}
	if err != nil {
		return emitReportOptions(stdout, options, finding.Report{IncompleteChecks: []string{err.Error()}})
	}
	var plan tofuPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return fmt.Errorf("parse OpenTofu plan JSON: %w", err)
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"OpenTofu resource change review"}, IncompleteChecks: []string{}}
	if plan.FormatVersion == "" {
		report.IncompleteChecks = append(report.IncompleteChecks, "format_version is missing from OpenTofu JSON")
	} else if strings.SplitN(plan.FormatVersion, ".", 2)[0] != "1" {
		report.IncompleteChecks = append(report.IncompleteChecks, "unsupported OpenTofu JSON major format version "+plan.FormatVersion)
	}
	if plan.Errored {
		report.Findings = append(report.Findings, makeFinding(
			"TOFU-ERRORED-001", finding.SeverityCritical, "OpenTofu plan is errored", "plan",
			"apply", options.environment, "The plan reports errored=true and cannot be treated as a complete deployment plan.",
			"errored: true", "Resolve the planning error and generate a new saved plan before review.",
		))
	}
	if plan.ResourceChanges == nil {
		report.IncompleteChecks = append(report.IncompleteChecks, "resource_changes is missing; provide output from tofu show -json PLANFILE")
		return emitReportOptions(stdout, options, report)
	}
	for _, change := range plan.ResourceChanges {
		actions := strings.Join(change.Change.Actions, ",")
		resource := change.Address
		if resource == "" {
			resource = change.Type
		}
		stateful := containsAny(strings.ToLower(change.Type+" "+change.Address), "database", "db_", "rds", "bucket", "storage", "disk", "volume", "redis", "cache")
		switch {
		case hasAction(change.Change.Actions, "delete") && hasAction(change.Change.Actions, "create"):
			severity := finding.SeverityHigh
			if stateful {
				severity = finding.SeverityCritical
			}
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "TOFU-REPLACE", resource, actions), severity, "Resource will be replaced",
				resource, actions, options.environment, "The plan contains both delete and create actions.",
				"actions: "+actions, "Confirm downtime, data migration, dependencies, and tested rollback before applying.",
			))
		case hasAction(change.Change.Actions, "delete"):
			severity := finding.SeverityHigh
			if stateful {
				severity = finding.SeverityCritical
			}
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "TOFU-DELETE", resource, actions), severity, "Resource will be deleted",
				resource, actions, options.environment, "The plan contains a delete action.",
				"actions: "+actions, "Verify the target identity, dependents, backups, retention behavior, and rollback plan.",
			))
		}

		serializedAfter, _ := json.Marshal(change.Change.After)
		after := strings.ToLower(string(serializedAfter))
		if containsAny(after, `"0.0.0.0/0"`, `"::/0"`, `"public":true`, `"publicly_accessible":true`) {
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "TOFU-PUBLIC", resource, actions), finding.SeverityCritical,
				"Resource may become publicly accessible", resource, actions, options.environment,
				"The planned after-state contains a public-access indicator.", "matched public access value in after-state",
				"Restrict ingress or public access and obtain the required security review.",
			))
		}
		identityResource := containsAny(strings.ToLower(change.Type), "iam", "ram_", "role", "policy")
		if identityResource && hasAction(change.Change.Actions, "update") {
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "TOFU-IAM", resource, actions), finding.SeverityHigh,
				"Identity or policy resource changes", resource, actions, options.environment,
				"The update affects an identity, role, or policy resource.", "resource type: "+change.Type,
				"Review added permissions, trust relationships, conditions, and privilege-escalation paths.",
			))
		}
	}
	for _, drift := range plan.ResourceDrift {
		if len(drift.Change.Actions) == 0 || (len(drift.Change.Actions) == 1 && drift.Change.Actions[0] == "no-op") {
			continue
		}
		report.Findings = append(report.Findings, makeFinding(
			stableFindingID(&report, "TOFU-DRIFT", drift.Address, strings.Join(drift.Change.Actions, ",")), finding.SeverityWarning,
			"Resource drift detected", drift.Address, strings.Join(drift.Change.Actions, ","), options.environment,
			"The real resource changed outside the reviewed configuration.", "resource type: "+drift.Type,
			"Determine who or what changed the resource before applying a plan that may overwrite it.",
		))
	}
	for _, check := range plan.Checks {
		if containsAny(strings.ToLower(check.Status), "fail", "error") {
			resource := check.Address.ToDisplay
			if resource == "" {
				resource = "OpenTofu check"
			}
			report.Findings = append(report.Findings, makeFinding(
				stableFindingID(&report, "TOFU-CHECK", resource, check.Status), finding.SeverityHigh,
				"OpenTofu check failed", resource, "apply", options.environment,
				"A precondition, postcondition, or check block did not pass.", "status: "+check.Status,
				"Resolve the failed check and create a fresh plan.",
			))
		}
	}
	return emitReportOptions(stdout, options, report)
}

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

func runSpaceliftCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var expectedCommit, expectedStack, collectStack, collectRun string
	_, options, err := parseFlags("spacelift-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&expectedCommit, "expect-commit", "", "expected commit SHA")
		fs.StringVar(&expectedStack, "expect-stack", "", "expected stack ID")
		fs.StringVar(&collectStack, "stack", "", "collect a run snapshot from this stack using spacectl")
		fs.StringVar(&collectRun, "run", "", "run ID used with --stack")
		return &options
	})
	if err != nil {
		return err
	}
	var data []byte
	if collectStack != "" || collectRun != "" {
		if collectStack == "" || collectRun == "" {
			return fmt.Errorf("--stack and --run must be supplied together")
		}
		data, err = collectJSON("spacectl", "stack", "show", "--id", collectStack, "--run", collectRun, "--output", "json", "--no-color")
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
	stack := recursiveString(object, "stack_id", "stackId", "stack")
	commit := recursiveString(object, "commit_sha", "commitSha", "commit", "head_sha")
	state := recursiveString(object, "state", "status")
	runID := recursiveString(object, "run_id", "runId", "id")
	if runID == "" {
		runID = "spacelift-run"
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"Spacelift run identity and policy review"}, IncompleteChecks: []string{}}
	for _, required := range []struct{ label, value string }{
		{"stack ID", stack}, {"commit SHA", commit}, {"run state", state},
	} {
		if required.value == "" {
			report.IncompleteChecks = append(report.IncompleteChecks, required.label+" was not found in the run JSON")
		}
	}
	if expectedCommit != "" && commit != "" && !strings.HasPrefix(commit, expectedCommit) && !strings.HasPrefix(expectedCommit, commit) {
		report.Findings = append(report.Findings, makeFinding(
			"SPACE-COMMIT-001", finding.SeverityCritical, "Spacelift run uses an unexpected commit", runID,
			"verify run", options.environment, "The planned commit does not match the expected commit.",
			fmt.Sprintf("expected %s; got %s", expectedCommit, commit), "Discard the stale run and plan the intended commit.",
		))
	}
	if expectedStack != "" && stack != "" && stack != expectedStack {
		report.Findings = append(report.Findings, makeFinding(
			"SPACE-STACK-001", finding.SeverityCritical, "Spacelift run uses an unexpected stack", runID,
			"verify run", options.environment, "The run stack does not match the expected stack.",
			fmt.Sprintf("expected %s; got %s", expectedStack, stack), "Stop and select the intended stack before planning again.",
		))
	}
	stateLower := strings.ToLower(state)
	if containsAny(stateLower, "failed", "rejected", "discarded", "canceled", "cancelled") {
		report.Findings = append(report.Findings, makeFinding(
			"SPACE-STATE-001", finding.SeverityHigh, "Spacelift run is not successful", runID,
			"review run", options.environment, "The run state indicates failure, rejection, discard, or cancellation.",
			"state: "+state, "Resolve the failed phase or policy result and create a fresh run.",
		))
	}
	serialized, _ := json.Marshal(object)
	if regexpContains(serialized, `(?i)"(decision|outcome|status)"\s*:\s*"(deny|denied|reject|rejected|fail|failed)"`) {
		report.Findings = append(report.Findings, makeFinding(
			"SPACE-POLICY-001", finding.SeverityHigh, "Spacelift policy did not pass", runID,
			"review policy", options.environment, "A policy result contains a deny, reject, or failure outcome.",
			"matched a failing policy outcome", "Read the named policy result and resolve it instead of bypassing it.",
		))
	}
	return emitReportOptions(stdout, options, report)
}

func runPRManager(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "create" || args[0] == "update") {
		return runPRChange(args[0], args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "inspect" {
		args = args[1:]
	}
	var expectedCommit, collectPR, repository string
	_, options, err := parseFlags("pr-manager inspect", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&expectedCommit, "expect-commit", "", "expected pull-request head SHA")
		fs.StringVar(&collectPR, "collect", "", "pull-request number, URL, or branch to collect using gh")
		fs.StringVar(&repository, "repo", "", "GitHub repository in OWNER/REPO form")
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
	if mergeable == "" {
		report.IncompleteChecks = append(report.IncompleteChecks, "pull-request mergeability is missing")
	}
	if _, present := object["statusCheckRollup"]; !present {
		report.IncompleteChecks = append(report.IncompleteChecks, "pull-request status checks are missing")
	}
	if expectedCommit != "" && commit != "" && !strings.HasPrefix(commit, expectedCommit) && !strings.HasPrefix(expectedCommit, commit) {
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
