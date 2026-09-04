package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git-tools/diffwalk"
	"git-tools/finding"
)

func runImportantCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var configPath string
	_, options, err := parseFlags("git-isimportant-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&configPath, "config", "giimp_check.conf", "important-path configuration file")
		return &options
	})
	if err != nil {
		return err
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config %q: %w", configPath, err)
	}
	patterns := parsePatterns(string(config))
	if len(patterns) == 0 {
		return fmt.Errorf("config %q contains no path patterns", configPath)
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	diff, err := diffwalk.Parse(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse diff: %w", err)
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"important path diff review"}, IncompleteChecks: []string{}}
	for _, file := range diff.Files {
		pattern := matchingPattern(file.Path, patterns)
		if pattern == "" {
			continue
		}
		fileDiff := diffwalk.Diff{Files: []diffwalk.File{file}}
		summary := fileDiff.Summary()
		severity := finding.SeverityWarning
		if summary.Deletions > 0 || file.Change == diffwalk.Deleted {
			severity = finding.SeverityHigh
		}
		report.Findings = append(report.Findings, makeFinding(
			normalizedID("IMPORTANT", len(report.Findings)), severity, "Important path changed", file.Path,
			string(file.Change), options.environment, fmt.Sprintf("Path matches protected pattern %q.", pattern),
			fmt.Sprintf("hunks %d; additions %d; deletions %d", summary.Hunks, summary.Additions, summary.Deletions),
			"Review this file explicitly and obtain the owner required by repository policy.",
		))
	}
	return emitReportOptions(stdout, options, report)
}

func parsePatterns(config string) []string {
	var patterns []string
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line != "" {
			patterns = append(patterns, line)
		}
	}
	return patterns
}

func matchingPattern(path string, patterns []string) string {
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, path)
		if err == nil && matched {
			return pattern
		}
		if strings.HasSuffix(pattern, "/**") && strings.HasPrefix(path, strings.TrimSuffix(pattern, "**")) {
			return pattern
		}
	}
	return ""
}

func runJSONUpdate(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var patchPath string
	var write bool
	_, options, err := parseFlags("git-update-json", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&patchPath, "patch", "", "JSON object containing updates")
		fs.BoolVar(&write, "write", false, "atomically replace the input file; default is preview to stdout")
		return &options
	})
	if err != nil {
		return err
	}
	if patchPath == "" {
		return fmt.Errorf("--patch is required")
	}
	if write && options.input == "-" {
		return fmt.Errorf("--write requires --input with a file path")
	}
	baseData, err := readInput(options.input, stdin)
	if err != nil {
		return err
	}
	patchData, err := os.ReadFile(patchPath)
	if err != nil {
		return fmt.Errorf("read patch %q: %w", patchPath, err)
	}
	var base, patch map[string]any
	if err := json.Unmarshal(baseData, &base); err != nil {
		return fmt.Errorf("parse base JSON: %w", err)
	}
	if err := json.Unmarshal(patchData, &patch); err != nil {
		return fmt.Errorf("parse patch JSON: %w", err)
	}
	merged := mergeObjects(base, patch)
	result, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("encode merged JSON: %w", err)
	}
	result = append(result, '\n')
	if !write {
		_, err = stdout.Write(result)
		return err
	}
	info, err := os.Stat(options.input)
	if err != nil {
		return fmt.Errorf("stat input %q: %w", options.input, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(options.input), ".git-update-json-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }
	defer cleanup()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(result); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, options.input); err != nil {
		return fmt.Errorf("replace input %q: %w", options.input, err)
	}
	fmt.Fprintf(stdout, "UPDATED: %s\n", options.input)
	return nil
}

func mergeObjects(base, patch map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(patch))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range patch {
		patchObject, patchOK := value.(map[string]any)
		baseObject, baseOK := result[key].(map[string]any)
		if patchOK && baseOK {
			result[key] = mergeObjects(baseObject, patchObject)
		} else {
			result[key] = value
		}
	}
	return result
}

func atomicReplace(path string, data []byte, stdout io.Writer) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat input %q: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".git-tools-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace input %q: %w", path, err)
	}
	fmt.Fprintf(stdout, "UPDATED: %s\n", path)
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runDeployReview(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var reports stringList
	_, options, err := parseFlags("deploy-review", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.Var(&reports, "report", "finding JSON report to combine; may be repeated")
		return &options
	})
	if err != nil {
		return err
	}
	combined := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{}, IncompleteChecks: []string{}}
	if len(reports) == 0 {
		data, readErr := readInput(options.input, stdin)
		if readErr != nil {
			return readErr
		}
		if err := appendReport(&combined, data, "input"); err != nil {
			return err
		}
	} else {
		for _, path := range reports {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				combined.IncompleteChecks = append(combined.IncompleteChecks, fmt.Sprintf("could not read report %s: %v", path, readErr))
				continue
			}
			if err := appendReport(&combined, data, path); err != nil {
				combined.IncompleteChecks = append(combined.IncompleteChecks, err.Error())
			}
		}
	}
	return emitReportOptions(stdout, options, combined)
}

func appendReport(combined *finding.Report, data []byte, source string) error {
	var envelope struct {
		Findings         []finding.Finding `json:"findings"`
		CompletedChecks  []string          `json:"completed_checks"`
		IncompleteChecks []string          `json:"incomplete_checks"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("parse report %s: %w", source, err)
	}
	for i := range envelope.Findings {
		envelope.Findings[i].ID = strings.ToUpper(strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))) + ":" + envelope.Findings[i].ID
	}
	combined.Findings = append(combined.Findings, envelope.Findings...)
	for _, completed := range envelope.CompletedChecks {
		combined.CompletedChecks = append(combined.CompletedChecks, source+": "+completed)
	}
	for _, incomplete := range envelope.IncompleteChecks {
		combined.IncompleteChecks = append(combined.IncompleteChecks, source+": "+incomplete)
	}
	return nil
}

func runCloudContextCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var expectedCloud, expectedAccount, expectedRegion, collectedRegion string
	var collect bool
	_, options, err := parseFlags("cloud-context-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&expectedCloud, "expect-cloud", "", "expected cloud: aws or alicloud")
		fs.StringVar(&expectedAccount, "expect-account", "", "expected account ID")
		fs.StringVar(&expectedRegion, "expect-region", "", "expected region")
		fs.BoolVar(&collect, "collect", false, "collect caller identity using aws or aliyun CLI")
		fs.StringVar(&collectedRegion, "actual-region", "", "active region when using --collect")
		return &options
	})
	if err != nil {
		return err
	}
	if expectedCloud == "" || expectedAccount == "" {
		return fmt.Errorf("--expect-cloud and --expect-account are required")
	}
	var data []byte
	if collect {
		var command string
		var commandArgs []string
		switch strings.ToLower(expectedCloud) {
		case "aws":
			command, commandArgs = "aws", []string{"sts", "get-caller-identity", "--output", "json"}
		case "alicloud", "aliyun":
			command, commandArgs = "aliyun", []string{"sts", "get-caller-identity"}
		default:
			return fmt.Errorf("--collect supports --expect-cloud aws or alicloud")
		}
		data, err = collectJSON(command, commandArgs...)
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
	if collect {
		object["cloud"] = expectedCloud
		if collectedRegion != "" {
			object["region"] = collectedRegion
		}
	}
	actualCloud := recursiveString(object, "cloud", "provider")
	actualAccount := recursiveString(object, "account_id", "accountId", "AccountId", "account", "Account")
	actualRegion := recursiveString(object, "region", "Region")
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{"cloud identity comparison"}, IncompleteChecks: []string{}}
	checks := []struct{ label, expected, actual string }{
		{"cloud", expectedCloud, actualCloud}, {"account", expectedAccount, actualAccount}, {"region", expectedRegion, actualRegion},
	}
	for _, check := range checks {
		if check.expected == "" {
			continue
		}
		if check.actual == "" {
			report.IncompleteChecks = append(report.IncompleteChecks, check.label+" was not found in the identity snapshot")
			continue
		}
		if !strings.EqualFold(check.expected, check.actual) {
			report.Findings = append(report.Findings, makeFinding(
				normalizedID("CONTEXT", len(report.Findings)), finding.SeverityCritical,
				"Cloud context does not match", "deployment-context", "deploy", options.environment,
				"The active "+check.label+" differs from the explicitly expected value.",
				fmt.Sprintf("%s expected %s; got %s", check.label, check.expected, check.actual),
				"Stop before deployment and authenticate to the intended cloud context.",
			))
		}
	}
	return emitReportOptions(stdout, options, report)
}
