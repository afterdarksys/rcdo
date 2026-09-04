package toolkit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type errorRule struct {
	name, tool, pattern, cause, next string
	re                               *regexp.Regexp
}

var errorRules = []errorRule{
	{name: "Authentication failed", pattern: `(?i)(unauthorized|unauthenticated|invalid.*token|expired.*token|no valid credential)`, cause: "The tool could not prove the caller identity or the credential has expired.", next: "Refresh the intended credential, confirm the account identity, and retry the read-only operation first."},
	{name: "Permission denied", pattern: `(?i)(accessdenied|permission denied|forbidden|not authorized to perform)`, cause: "The active identity lacks permission for the requested operation or a policy explicitly denies it.", next: "Confirm the active identity and inspect the narrow IAM, RBAC, or filesystem permission involved."},
	{name: "Name or endpoint lookup failed", pattern: `(?i)(no such host|name or service not known|temporary failure in name resolution|could not resolve host)`, cause: "DNS or endpoint resolution failed before the service could be reached.", next: "Verify the endpoint, DNS context, proxy, VPN, and resolver state."},
	{name: "Connection timed out", pattern: `(?i)(timed? out|deadline exceeded|context deadline exceeded)`, cause: "The operation exceeded its deadline; reachability, service health, or workload duration may be responsible.", next: "Check reachability and service health, then retry with bounded diagnostics before increasing a timeout."},
	{name: "Configuration syntax error", pattern: `(?i)(invalid (json|yaml|toml|hcl)|parse error|syntax error|unexpected token|did not find expected)`, cause: "The input does not match the syntax expected by the tool.", next: "Use config-explain with an explicit --syntax and inspect the reported line and path."},
	{name: "Terraform or OpenTofu state lock", tool: "tofu", pattern: `(?i)(error acquiring the state lock|state.*lock.*held|lock info)`, cause: "Another operation or a stale lock prevents concurrent state modification.", next: "Identify the lock owner and active run. Force-unlock only after proving no operation is still using the state."},
	{name: "Kubernetes object conflict", tool: "kubectl", pattern: `(?i)(object has been modified|the object provided is unrecognized|conflict)`, cause: "The object changed after the local copy or request was prepared.", next: "Fetch the current object, reapply the intended minimal change, and review the new diff."},
	{name: "Ansible host unreachable", tool: "ansible", pattern: `(?i)(unreachable!|failed to connect to the host|no route to host)`, cause: "Ansible could not establish its managed-host connection.", next: "Confirm inventory, target scope, network path, SSH identity, and privilege escalation without broadening the play."},
}

type errorExplanation struct {
	SchemaVersion string   `json:"schema_version"`
	Tool          string   `json:"tool"`
	Summary       string   `json:"summary"`
	ProbableCause string   `json:"probable_cause"`
	NextAction    string   `json:"next_action"`
	Evidence      []string `json:"evidence"`
	Confidence    string   `json:"confidence"`
}

func runErrorExplain(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var input, tool, format string
	var maxEvidence int
	fs := flag.NewFlagSet("error-explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&input, "input", "-", "error output file; use - for standard input")
	fs.StringVar(&tool, "tool", "auto", "source tool name or auto")
	fs.StringVar(&format, "format", "text", "output format: text or json")
	fs.IntVar(&maxEvidence, "evidence-lines", 3, "maximum redacted evidence lines")
	setAccessibleUsage(fs, "error-explain", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("unknown format %q", format)
	}
	if maxEvidence < 1 || maxEvidence > 20 {
		return fmt.Errorf("--evidence-lines must be between 1 and 20")
	}
	data, err := readInput(input, stdin)
	if err != nil {
		return err
	}
	if tool == "auto" {
		tool = detectErrorTool(string(data))
	}
	explanation := explainError(tool, data, maxEvidence)
	if format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(explanation)
	}
	fmt.Fprintf(stdout, "ERROR EXPLANATION\nTool: %s\nSummary: %s\nProbable cause: %s\nNext action: %s\nConfidence: %s\n", explanation.Tool, explanation.Summary, explanation.ProbableCause, explanation.NextAction, strings.ToUpper(explanation.Confidence))
	for i, line := range explanation.Evidence {
		writeWrapped(stdout, fmt.Sprintf("Evidence %d: %s", i+1, line), 100)
	}
	return nil
}

func detectErrorTool(value string) string {
	lower := strings.ToLower(value)
	for _, tool := range []string{"terraform", "tofu", "kubectl", "ansible", "github", "aws", "alicloud"} {
		if strings.Contains(lower, tool) {
			if tool == "terraform" {
				return "tofu"
			}
			return tool
		}
	}
	return "unknown"
}
func explainError(tool string, data []byte, max int) errorExplanation {
	value := string(data)
	result := errorExplanation{SchemaVersion: "1", Tool: tool, Summary: "Unclassified command failure", ProbableCause: "The available text does not match a built-in failure pattern.", NextAction: "Read the first causal error and the final context line; rerun with the originating tool named by --tool if known.", Confidence: "low"}
	for _, rule := range errorRules {
		if rule.tool != "" && rule.tool != tool {
			continue
		}
		re := rule.re
		if re == nil {
			re = regexp.MustCompile(rule.pattern)
		}
		if re.MatchString(value) {
			result.Summary, result.ProbableCause, result.NextAction, result.Confidence = rule.name, rule.cause, rule.next, "medium"
			break
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() && len(result.Evidence) < max {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			if len(line) > 300 {
				line = line[:300] + "..."
			}
			result.Evidence = append(result.Evidence, redactLine(line))
		}
	}
	return result
}
