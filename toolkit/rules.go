package toolkit

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"git-tools/finding"
)

type textRule struct {
	id          string
	severity    finding.Severity
	title       string
	pattern     *regexp.Regexp
	remediation string
}

var dangerRules = []textRule{
	{"DELETE", finding.SeverityCritical, "Destructive infrastructure command", regexp.MustCompile(`(?i)\b(aws|aliyun|aliyuncli|gcloud|oci|kubectl)\b.*\b(delete|terminate|destroy|remove|purge)\b`), "Verify identity and target scope, take required backups, and document rollback before execution."},
	{"TFDESTROY", finding.SeverityCritical, "OpenTofu/Terraform destroy command", regexp.MustCompile(`(?i)\b(tofu|terraform)\s+destroy\b`), "Review the plan and require a second operator to confirm every destroyed resource."},
	{"AUTOAPPROVE", finding.SeverityHigh, "Automatic infrastructure approval", regexp.MustCompile(`(?i)\b(tofu|terraform)\s+(apply|destroy)\b.*-auto-approve\b`), "Remove automatic approval and review the saved plan before applying it."},
	{"KUBEALL", finding.SeverityHigh, "Broad Kubernetes operation", regexp.MustCompile(`(?i)\bkubectl\b.*\b(delete|rollout restart|scale)\b.*(--all\b|\ball\b)`), "Name the namespace and resources explicitly, then preview the selected objects."},
	{"PRUNE", finding.SeverityHigh, "Destructive container cleanup", regexp.MustCompile(`(?i)\bdocker\s+(system|image|volume|container)\s+prune\b`), "List affected objects first and avoid broad prune operations on shared hosts."},
	{"RMRF", finding.SeverityCritical, "Recursive forced deletion", regexp.MustCompile(`(?i)(^|[;&|]\s*)rm\s+-[^\n]*r[^\n]*f|(^|[;&|]\s*)rm\s+-[^\n]*f[^\n]*r`), "Replace the broad deletion with a validated, explicit target and a recoverable operation."},
	{"CURLPIPE", finding.SeverityHigh, "Downloaded content executes directly", regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh|python)\b`), "Download, pin, inspect, and verify the artifact before executing it."},
	{"WILDCARD", finding.SeverityWarning, "Wildcard appears in an operational command", regexp.MustCompile(`(?i)\b(aws|aliyun|gcloud|oci|kubectl|docker)\b[^\n]*\*`), "Replace the wildcard with explicit targets and verify the resulting selection."},
}

var ghaRules = []textRule{
	{"WRITEALL", finding.SeverityCritical, "Workflow grants write-all permissions", regexp.MustCompile(`(?i)^\s*permissions\s*:\s*write-all\s*$`), "Grant the minimum permissions needed at job scope."},
	{"PRTARGET", finding.SeverityHigh, "Workflow uses pull_request_target", regexp.MustCompile(`(?i)^\s*pull_request_target\s*:`), "Do not execute untrusted pull-request code with base-repository secrets or write permissions."},
	{"INJECT", finding.SeverityHigh, "GitHub expression appears directly in a shell command", regexp.MustCompile(`(?i)^\s*(run\s*:\s*)?.*\$\{\{\s*github\.event\.(issue|pull_request|comment|review|head_commit)`), "Pass untrusted values through a quoted environment variable and validate them before use."},
	{"SECRETLOG", finding.SeverityCritical, "Command may print a secret", regexp.MustCompile(`(?i)^\s*(run\s*:\s*)?.*\b(echo|printenv|env)\b.*\b(secrets\.|secret|token|password)`), "Remove secret output and confirm logs and artifacts cannot expose credentials."},
	{"MUTABLE", finding.SeverityWarning, "Action reference is not pinned to a commit SHA", regexp.MustCompile(`(?i)^\s*-?\s*uses\s*:\s*[^./\s][^\s@]+/[^\s@]+@(main|master|v?\d+(?:\.\d+){0,2})\s*(?:#.*)?$`), "Pin third-party actions to a reviewed full commit SHA and retain a version comment."},
	{"SELHOST", finding.SeverityWarning, "Workflow uses a self-hosted runner", regexp.MustCompile(`(?i)^\s*runs-on\s*:.*self-hosted`), "Verify runner isolation, trust boundary, cleanup, and which pull requests may reach it."},
}

var ansibleRules = []textRule{
	{"ALLHOSTS", finding.SeverityHigh, "Play targets all hosts", regexp.MustCompile(`(?i)^\s*-?\s*hosts\s*:\s*(all|\*)\s*$`), "Use an explicit inventory group and confirm the host limit before execution."},
	{"SHELL", finding.SeverityWarning, "Task uses shell or command", regexp.MustCompile(`(?i)^\s*(ansible\.builtin\.)?(shell|command)\s*:`), "Prefer a purpose-built idempotent module, or add guards and check-mode behavior."},
	{"IGNORE", finding.SeverityHigh, "Task ignores errors", regexp.MustCompile(`(?i)^\s*ignore_errors\s*:\s*(true|yes)\s*$`), "Handle the expected failure explicitly and stop on unexpected states."},
	{"LATEST", finding.SeverityWarning, "Package is upgraded to latest", regexp.MustCompile(`(?i)^\s*state\s*:\s*latest\s*$`), "Pin or constrain the package version and test the upgrade separately."},
	{"PLAINTEXT", finding.SeverityCritical, "Possible plaintext credential", regexp.MustCompile(`(?i)^\s*(password|passwd|api_key|secret|access_key)\s*:\s*[^!{][^\n]*$`), "Store the value in Ansible Vault or an approved secret manager and prevent it from reaching logs."},
	{"CHANGED", finding.SeverityWarning, "Command always reports a change", regexp.MustCompile(`(?i)^\s*changed_when\s*:\s*(true|yes)\s*$`), "Use an output-based condition so repeated runs accurately report whether state changed."},
}

func runRuleCheck(name string, args []string, stdin io.Reader, stdout, stderr io.Writer, rules []textRule) error {
	_, options, err := parseFlags(name, args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		return &options
	})
	if err != nil {
		return err
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	report := scanRules(data, basename(options.input), options.environment, rules)
	return emitReportOptions(stdout, options, report)
}

func runAnsible(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	args = normalizeCheckSubcommand(args)
	return runNativeRuleCheck("ansible-check", args, stdin, stdout, stderr, ansibleRules, "ansible-playbook", []string{"--syntax-check"})
}

func runNativeRuleCheck(name string, args []string, stdin io.Reader, stdout, stderr io.Writer, rules []textRule, nativeProgram string, nativeArgs []string) error {
	var native bool
	_, options, err := parseFlags(name, args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.BoolVar(&native, "native", false, "also run the installed native syntax checker; requires a named input file")
		return &options
	})
	if err != nil {
		return err
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	report := scanRules(data, basename(options.input), options.environment, rules)
	if native {
		if options.input == "-" {
			report.IncompleteChecks = append(report.IncompleteChecks, nativeProgram+" requires --input with a file path")
		} else if _, lookupErr := exec.LookPath(nativeProgram); lookupErr != nil {
			report.IncompleteChecks = append(report.IncompleteChecks, nativeProgram+" is not installed")
		} else {
			commandArgs := append(append([]string{}, nativeArgs...), options.input)
			result := executeReadOnly(nativeProgram, commandArgs...)
			if result.err != nil {
				evidence := strings.TrimSpace(result.stderr)
				if evidence == "" {
					evidence = result.err.Error()
				}
				if len(evidence) > 500 {
					evidence = evidence[:500] + "..."
				}
				report.Findings = append(report.Findings, makeFinding(
					normalizedID("NATIVE", len(report.Findings)), finding.SeverityHigh,
					nativeProgram+" reported an error", basename(options.input), "validate", options.environment,
					"The native syntax or semantic checker exited unsuccessfully.", redactLine(evidence),
					"Resolve the native checker output and rerun the review.",
				))
			} else {
				report.CompletedChecks = append(report.CompletedChecks, nativeProgram+" validation")
			}
		}
	}
	return emitReportOptions(stdout, options, report)
}

func scanRules(data []byte, resource, environment string, rules []textRule) finding.Report {
	if resource == "-" {
		resource = "standard-input"
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"built-in static safety rules"}, IncompleteChecks: []string{}}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		for _, rule := range rules {
			if rule.pattern.MatchString(line) {
				id := normalizedID(rule.id, len(report.Findings))
				report.Findings = append(report.Findings, makeFinding(
					id, rule.severity, rule.title, resource, "review line", environment,
					fmt.Sprintf("Rule %s matched line %d.", rule.id, lineNumber),
					fmt.Sprintf("line %d: %s", lineNumber, redactLine(line)), rule.remediation,
				))
			}
		}
	}
	return report
}

var secretAssignment = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|access[_-]?key)(\s*[:=]\s*)([^\s]+)`)

func redactLine(line string) string {
	return secretAssignment.ReplaceAllString(line, "$1$2[REDACTED]")
}

func runRunbookCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_, options, err := parseFlags("runbook-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		return &options
	})
	if err != nil {
		return err
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	lower := strings.ToLower(string(data))
	requirements := []struct {
		name, title, remediation string
		severity                 finding.Severity
		alternatives             []string
	}{
		{"ROLLBACK", "Runbook has no rollback procedure", "Add numbered rollback steps, decision criteria, and the person authorized to initiate them.", finding.SeverityHigh, []string{"rollback", "roll back", "backout"}},
		{"VALIDATE", "Runbook has no post-change validation", "Add observable health checks, expected results, and a validation timeout.", finding.SeverityHigh, []string{"validation", "validate", "health check", "verification"}},
		{"OWNER", "Runbook has no owner or escalation contact", "Name the responsible role and escalation path without relying only on a visual diagram.", finding.SeverityWarning, []string{"owner", "on-call", "oncall", "escalation"}},
		{"SCOPE", "Runbook has no explicit target scope", "State the environment, account, region, and resource or service names.", finding.SeverityWarning, []string{"environment", "account", "region", "scope"}},
		{"STOP", "Runbook has no stop conditions", "Document signals that require pausing or aborting the change.", finding.SeverityWarning, []string{"stop condition", "abort", "do not proceed", "pause"}},
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"runbook required-section review"}, IncompleteChecks: []string{}}
	for _, requirement := range requirements {
		found := false
		for _, alternative := range requirement.alternatives {
			if strings.Contains(lower, alternative) {
				found = true
				break
			}
		}
		if !found {
			report.Findings = append(report.Findings, makeFinding(
				normalizedID(requirement.name, len(report.Findings)), requirement.severity,
				requirement.title, basename(options.input), "complete runbook", options.environment,
				"A required operational concept was not found.", "searched case-insensitively", requirement.remediation,
			))
		}
	}
	return emitReportOptions(stdout, options, report)
}
