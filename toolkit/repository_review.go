package toolkit

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"git-tools/finding"
	"gopkg.in/yaml.v3"
)

type repositoryPolicy struct {
	Version       string `json:"version" yaml:"version"`
	CriticalPaths []struct {
		Pattern  string `json:"pattern" yaml:"pattern"`
		Owner    string `json:"owner" yaml:"owner"`
		Severity string `json:"severity" yaml:"severity"`
		Reason   string `json:"reason" yaml:"reason"`
	} `json:"critical_paths" yaml:"critical_paths"`
	RequiredCompanions []struct {
		When    string   `json:"when" yaml:"when"`
		Require []string `json:"require" yaml:"require"`
		Reason  string   `json:"reason" yaml:"reason"`
	} `json:"required_companions" yaml:"required_companions"`
	ForbiddenPatterns []struct {
		Files       string `json:"files" yaml:"files"`
		Pattern     string `json:"pattern" yaml:"pattern"`
		Severity    string `json:"severity" yaml:"severity"`
		Title       string `json:"title" yaml:"title"`
		Remediation string `json:"remediation" yaml:"remediation"`
	} `json:"forbidden_patterns" yaml:"forbidden_patterns"`
}

type changedFile struct{ Status, OldPath, Path string }

func loadRepositoryPolicy(path string) (repositoryPolicy, error) {
	var policy repositoryPolicy
	if path == "" {
		return policy, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return policy, fmt.Errorf("read repository policy %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return policy, fmt.Errorf("parse repository policy %q: %w", path, err)
	}
	if policy.Version != "1" {
		return policy, fmt.Errorf("repository policy version must be 1")
	}
	for i, rule := range policy.CriticalPaths {
		if rule.Pattern == "" || rule.Owner == "" || rule.Reason == "" {
			return policy, fmt.Errorf("critical_paths item %d requires pattern, owner, and reason", i+1)
		}
		if _, err := policySeverity(rule.Severity); err != nil {
			return policy, fmt.Errorf("critical_paths item %d: %w", i+1, err)
		}
	}
	for i, rule := range policy.RequiredCompanions {
		if rule.When == "" || len(rule.Require) == 0 || rule.Reason == "" {
			return policy, fmt.Errorf("required_companions item %d requires when, require, and reason", i+1)
		}
	}
	for i, rule := range policy.ForbiddenPatterns {
		if rule.Files == "" || rule.Pattern == "" || rule.Title == "" || rule.Remediation == "" {
			return policy, fmt.Errorf("forbidden_patterns item %d is incomplete", i+1)
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return policy, fmt.Errorf("forbidden_patterns item %d pattern: %w", i+1, err)
		}
		if _, err := policySeverity(rule.Severity); err != nil {
			return policy, fmt.Errorf("forbidden_patterns item %d: %w", i+1, err)
		}
	}
	return policy, nil
}

func policySeverity(value string) (finding.Severity, error) {
	switch strings.ToLower(value) {
	case "", "warning":
		return finding.SeverityWarning, nil
	case "info":
		return finding.SeverityInfo, nil
	case "high":
		return finding.SeverityHigh, nil
	case "critical":
		return finding.SeverityCritical, nil
	default:
		return "", fmt.Errorf("unknown severity %q", value)
	}
}

func runRepositoryReview(args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var base, head, repoPolicyPath string
	var options commonOptions
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&options.format, "format", "text", "output format: text, json, github, or sarif")
	fs.StringVar(&options.environment, "environment", "unknown", "deployment environment label")
	fs.StringVar(&options.policy, "policy", "", "JSON policy containing owned, expiring suppressions")
	fs.StringVar(&base, "base", "HEAD", "base Git revision")
	fs.StringVar(&head, "head", "", "head Git revision; default is the working tree")
	fs.StringVar(&repoPolicyPath, "repo-policy", ".rcdo/policy.yaml", "repository policy file; missing default is allowed")
	setAccessibleUsage(fs, "review", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if options.format != "text" && options.format != "json" && options.format != "github" && options.format != "sarif" {
		return fmt.Errorf("unknown format %q", options.format)
	}
	if repoPolicyPath == ".rcdo/policy.yaml" {
		if _, statErr := os.Stat(repoPolicyPath); os.IsNotExist(statErr) {
			if _, legacyErr := os.Stat(".git-tools/policy.yaml"); legacyErr == nil {
				repoPolicyPath = ".git-tools/policy.yaml"
			} else {
				repoPolicyPath = ""
			}
		}
	}
	policy, err := loadRepositoryPolicy(repoPolicyPath)
	if err != nil {
		return err
	}
	gitArgs := []string{"diff", "--name-status", "--find-renames", base}
	if head != "" {
		gitArgs = append(gitArgs, head)
	}
	result := executeReadOnly("git", gitArgs...)
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{}, IncompleteChecks: []string{}}
	if result.err != nil {
		report.IncompleteChecks = append(report.IncompleteChecks, "git change collection failed: "+strings.TrimSpace(result.stderr))
		return emitReportOptions(stdout, options, report)
	}
	files, parseErr := parseChangedFiles(result.stdout)
	if parseErr != nil {
		return parseErr
	}
	if head == "" {
		untracked := executeReadOnly("git", "ls-files", "--others", "--exclude-standard")
		if untracked.err == nil {
			for _, path := range strings.Split(strings.TrimSpace(string(untracked.stdout)), "\n") {
				if path != "" {
					files = append(files, changedFile{Status: "A", OldPath: path, Path: path})
				}
			}
		} else {
			report.IncompleteChecks = append(report.IncompleteChecks, "could not inventory untracked working-tree files")
		}
	}
	headLabel := head
	if headLabel == "" {
		headLabel = "working tree"
	}
	report.CompletedChecks = append(report.CompletedChecks, fmt.Sprintf("Git change inventory from %s to %s", base, headLabel))
	if repoPolicyPath != "" {
		report.CompletedChecks = append(report.CompletedChecks, "repository policy "+repoPolicyPath)
	}
	changedPaths := make([]string, 0, len(files))
	for _, file := range files {
		changedPaths = append(changedPaths, file.Path)
	}
	if len(files) > 0 {
		evidence := make([]string, 0, len(files))
		for _, file := range files {
			evidence = append(evidence, file.Status+" "+file.Path)
		}
		report.Findings = append(report.Findings, finding.Finding{
			ID:       stableFindingID(&report, "REPO-CHANGES", base, headLabel, strings.Join(changedPaths, "\x00")),
			Severity: finding.SeverityInfo, Title: "Repository files changed", Resource: "repository",
			Action: "review change inventory", Environment: options.environment,
			Reason: fmt.Sprintf("Git reports %d changed file(s) in the selected range.", len(files)), Evidence: evidence,
			Confidence: finding.ConfidenceHigh, Remediation: "Review the listed files and the risk-ranked findings that follow.",
		})
	}
	applyRepositoryPathPolicy(&report, policy, files, changedPaths, options.environment)

	for _, file := range files {
		beforeData, beforeOK := revisionFile(base, file.OldPath)
		afterData, afterOK := headFile(head, file.Path)
		if file.Status == "A" {
			beforeOK = false
		}
		if file.Status == "D" {
			afterOK = false
		}
		if file.Status != "D" && !afterOK {
			report.IncompleteChecks = append(report.IncompleteChecks, "could not read changed file "+file.Path)
			continue
		}
		if isStructuredConfig(file.Path) && beforeOK && afterOK {
			beforeSyntax, e1 := detectConfigSyntax("auto", file.OldPath, beforeData)
			afterSyntax, e2 := detectConfigSyntax("auto", file.Path, afterData)
			if e1 != nil || e2 != nil {
				report.IncompleteChecks = append(report.IncompleteChecks, "could not detect configuration syntax for "+file.Path)
			} else {
				beforeEntries, e1 := explainConfig(beforeSyntax, file.OldPath, beforeData, true, true)
				afterEntries, e2 := explainConfig(afterSyntax, file.Path, afterData, true, true)
				if e1 != nil || e2 != nil {
					report.IncompleteChecks = append(report.IncompleteChecks, "could not parse structured change "+file.Path)
				} else if changes := compareConfigEntries(beforeEntries, afterEntries); len(changes) > 0 {
					report.Findings = append(report.Findings, makeFinding(stableFindingID(&report, "CONFIG", file.Path), finding.SeverityInfo, "Structured configuration changed", file.Path, "review semantic diff", options.environment, "The document has path-level semantic changes.", summarizeConfigChanges(changes), "Run config-diff for labeled before-and-after values."))
				}
			}
		}
		if afterOK {
			if isOperationalText(file.Path) {
				added := afterData
				if file.Status != "A" {
					args := []string{"diff", "--no-color", base}
					if head != "" {
						args = append(args, head)
					}
					args = append(args, "--", file.Path)
					fileDiff := executeReadOnly("git", args...)
					if fileDiff.err != nil {
						report.IncompleteChecks = append(report.IncompleteChecks, "dangerous-command scan failed for "+file.Path)
					} else {
						added = addedDiffLines(fileDiff.stdout)
					}
				}
				if len(bytes.TrimSpace(added)) > 0 {
					appendFindings(&report, scanRules(added, file.Path, options.environment, dangerRules).Findings)
				}
			}
			if strings.HasPrefix(file.Path, ".github/workflows/") {
				appendFindings(&report, scanRules(afterData, file.Path, options.environment, ghaRules).Findings)
			}
			if strings.Contains(strings.ToLower(file.Path), "ansible") || strings.HasSuffix(file.Path, ".yml") || strings.HasSuffix(file.Path, ".yaml") {
				appendFindings(&report, scanRules(afterData, file.Path, options.environment, ansibleRules).Findings)
			}
			applyForbiddenPatterns(&report, policy, file.Path, afterData, options.environment)
			if isStructuredConfig(file.Path) {
				appendFindings(&report, scanOperationalPolicy(file.Path, afterData, options.environment).Findings)
			}
		}
	}
	report.CompletedChecks = append(report.CompletedChecks, "structured configuration, workflow, Ansible, command, and operational policy review")
	return emitReportOptions(stdout, options, report)
}

func parseChangedFiles(data []byte) ([]changedFile, error) {
	var files []changedFile
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			return nil, fmt.Errorf("parse git name-status line %q", line)
		}
		status := string(parts[0][0])
		file := changedFile{Status: status, OldPath: parts[1], Path: parts[1]}
		if (status == "R" || status == "C") && len(parts) >= 3 {
			file.OldPath, file.Path = parts[1], parts[2]
		}
		files = append(files, file)
	}
	return files, nil
}

func revisionFile(revision, path string) ([]byte, bool) {
	if path == "" {
		return nil, false
	}
	result := executeReadOnly("git", "show", revision+":"+path)
	return result.stdout, result.err == nil
}

func headFile(head, path string) ([]byte, bool) {
	if head == "" {
		data, err := os.ReadFile(path)
		return data, err == nil
	}
	return revisionFile(head, path)
}

func isStructuredConfig(path string) bool {
	lower := strings.ToLower(path)
	for _, suffix := range []string{".json", ".yaml", ".yml", ".toml", ".hcl", ".tf", ".tfvars", ".tofu"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func isOperationalText(path string) bool {
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	if base == "makefile" || base == "dockerfile" || strings.HasPrefix(path, ".github/workflows/") {
		return true
	}
	for _, suffix := range []string{".sh", ".bash", ".zsh", ".ps1", ".mk"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func addedDiffLines(data []byte) []byte {
	var output bytes.Buffer
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			output.WriteString(strings.TrimPrefix(line, "+"))
			output.WriteByte('\n')
		}
	}
	return output.Bytes()
}

func appendFindings(report *finding.Report, values []finding.Finding) {
	for _, item := range values {
		item.ID = stableFindingID(report, item.ID, item.Resource, item.Title)
		report.Findings = append(report.Findings, item)
	}
}

func summarizeConfigChanges(changes []configChange) string {
	counts := map[string]int{}
	for _, change := range changes {
		counts[change.Change]++
	}
	return fmt.Sprintf("%d changes: %d added, %d removed, %d changed", len(changes), counts["added"], counts["removed"], counts["changed"])
}

func applyRepositoryPathPolicy(report *finding.Report, policy repositoryPolicy, files []changedFile, paths []string, environment string) {
	for _, file := range files {
		for _, rule := range policy.CriticalPaths {
			if matchingPattern(file.Path, []string{rule.Pattern}) == "" {
				continue
			}
			severity, _ := policySeverity(rule.Severity)
			report.Findings = append(report.Findings, makeFinding(stableFindingID(report, "POLICY-PATH", file.Path, rule.Pattern), severity, "Repository critical path changed", file.Path, strings.ToLower(file.Status), environment, rule.Reason, "owner: "+rule.Owner+"; pattern: "+rule.Pattern, "Obtain review from "+rule.Owner+" and verify the affected operational boundary."))
		}
	}
	for _, rule := range policy.RequiredCompanions {
		triggered := false
		for _, path := range paths {
			if matchingPattern(path, []string{rule.When}) != "" {
				triggered = true
				break
			}
		}
		if !triggered {
			continue
		}
		for _, required := range rule.Require {
			present := false
			for _, path := range paths {
				if matchingPattern(path, []string{required}) != "" {
					present = true
					break
				}
			}
			if !present {
				report.Findings = append(report.Findings, makeFinding(stableFindingID(report, "POLICY-COMPANION", rule.When, required), finding.SeverityHigh, "Required companion change is missing", required, "complete change", environment, rule.Reason, "changes matching "+rule.When+" require "+required, "Add or update the required companion file, or document an owned policy exception."))
			}
		}
	}
}

func applyForbiddenPatterns(report *finding.Report, policy repositoryPolicy, path string, data []byte, environment string) {
	for _, rule := range policy.ForbiddenPatterns {
		if matchingPattern(path, []string{rule.Files}) == "" {
			continue
		}
		re := regexp.MustCompile(rule.Pattern)
		matches := re.FindAll(data, -1)
		if len(matches) == 0 {
			continue
		}
		severity, _ := policySeverity(rule.Severity)
		report.Findings = append(report.Findings, makeFinding(stableFindingID(report, "POLICY-CONTENT", path, rule.Pattern), severity, rule.Title, path, "review content", environment, "Repository policy forbids matching content.", fmt.Sprintf("pattern matched %d time(s)", len(matches)), rule.Remediation))
	}
}

func runRepositoryPolicyCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var policyPath string
	_, options, err := parseFlags("repo-policy-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&policyPath, "repo-policy", ".rcdo/policy.yaml", "repository policy file")
		return &o
	})
	if err != nil {
		return err
	}
	if policyPath == ".rcdo/policy.yaml" {
		if _, err := os.Stat(policyPath); os.IsNotExist(err) {
			if _, legacyErr := os.Stat(".git-tools/policy.yaml"); legacyErr == nil {
				policyPath = ".git-tools/policy.yaml"
			}
		}
	}
	policy, err := loadRepositoryPolicy(policyPath)
	if err != nil {
		return err
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	files, err := parseChangedFiles(data)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"repository path and companion policy"}, IncompleteChecks: []string{}}
	applyRepositoryPathPolicy(&report, policy, files, paths, options.environment)
	return emitReportOptions(stdout, options, report)
}

func scanOperationalPolicy(path string, data []byte, environment string) finding.Report {
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"built-in cloud and operations policy"}, IncompleteChecks: []string{}}
	syntax, err := detectConfigSyntax("auto", path, data)
	if err != nil {
		report.IncompleteChecks = append(report.IncompleteChecks, err.Error())
		return report
	}
	entries, err := explainConfig(syntax, path, data, true, true)
	if err != nil {
		report.IncompleteChecks = append(report.IncompleteChecks, err.Error())
		return report
	}
	for _, entry := range entries {
		lowerPath, lowerValue := strings.ToLower(entry.Path), strings.ToLower(entry.Value)
		var severity finding.Severity
		var title, reason, remediation string
		switch {
		case strings.Contains(lowerValue, "0.0.0.0/0") || strings.Contains(lowerValue, "::/0"):
			severity, title, reason, remediation = finding.SeverityCritical, "Public network access configured", "A configuration value allows traffic from every IPv4 or IPv6 address.", "Restrict the CIDR and obtain security review."
		case strings.Contains(lowerPath, "privileged") && lowerValue == "true":
			severity, title, reason, remediation = finding.SeverityHigh, "Privileged workload configured", "Privileged execution weakens host isolation.", "Remove privileged mode or document the narrow capability requirement."
		case strings.Contains(lowerPath, "deletion_protection") && lowerValue == "false":
			severity, title, reason, remediation = finding.SeverityHigh, "Deletion protection disabled", "A stateful or critical resource may be easier to delete accidentally.", "Enable deletion protection or document recovery controls."
		case (strings.Contains(lowerPath, "encrypt") || strings.Contains(lowerPath, "kms")) && lowerValue == "false":
			severity, title, reason, remediation = finding.SeverityHigh, "Encryption disabled", "The configuration explicitly disables an encryption control.", "Enable encryption and select the approved key policy."
		case (strings.Contains(lowerPath, "action") || strings.Contains(lowerPath, "permission")) && (lowerValue == `"*"` || strings.Contains(lowerValue, `["*"]`)):
			severity, title, reason, remediation = finding.SeverityCritical, "Wildcard permission configured", "The policy appears to grant every action or permission.", "Replace the wildcard with the minimum required actions and resources."
		default:
			continue
		}
		report.Findings = append(report.Findings, makeFinding(stableFindingID(&report, "OPS-POLICY", path, entry.Path, title), severity, title, path+":"+entry.Path, "review policy", environment, reason, "matched at "+entry.Path, remediation))
	}
	return report
}

func runOperationalPolicyCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_, options, err := parseFlags("ops-policy-check", args, stderr, func(fs *flag.FlagSet) *commonOptions { var o commonOptions; addCommonFlags(fs, &o); return &o })
	if err != nil {
		return err
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	report := scanOperationalPolicy(basename(options.input), data, options.environment)
	return emitReportOptions(stdout, options, report)
}
