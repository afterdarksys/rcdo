package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	if options.provenance != nil {
		path, e := filepath.Abs(configPath)
		if e != nil {
			return e
		}
		options.provenance.Artifacts = append(options.provenance.Artifacts, finding.ProvenanceArtifact{Path: path, SHA256: digestBytes(config)})
	}
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
			stableFindingID(&report, "IMPORTANT", file.Path, string(file.Change)), severity, "Important path changed", file.Path,
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
	return atomicReplaceChecked(path, data, stdout, nil)
}
func atomicReplaceChecked(path string, data []byte, stdout io.Writer, check func() error) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat input %q: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".rcdo-*")
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
	if check != nil {
		if err := check(); err != nil {
			return err
		}
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
	var reports, required stringList
	_, options, err := parseFlags("deploy-review", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.Var(&reports, "report", "finding report as COMPONENT=FILE or FILE; may be repeated")
		fs.Var(&required, "require", "required component name; may be repeated")
		return &options
	})
	if err != nil {
		return err
	}
	combined := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{}, IncompleteChecks: []string{}}
	present := map[string]bool{}
	if len(reports) == 0 {
		data, readErr := readInput(options.input, stdin)
		if readErr != nil {
			return readErr
		}
		if err := appendVersionedReport(&combined, data, "input"); err != nil {
			combined.IncompleteChecks = append(combined.IncompleteChecks, err.Error())
		} else {
			present["input"] = true
		}
	} else {
		declared := map[string]bool{}
		for _, spec := range reports {
			component, path, specErr := parseReportSpec(spec)
			if specErr != nil {
				return specErr
			}
			if declared[component] {
				return fmt.Errorf("duplicate report component %s", component)
			}
			declared[component] = true
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				combined.IncompleteChecks = append(combined.IncompleteChecks, fmt.Sprintf("%s: could not read report %s: %v", component, path, readErr))
				continue
			}
			if err := appendVersionedReport(&combined, data, component); err != nil {
				combined.IncompleteChecks = append(combined.IncompleteChecks, err.Error())
				continue
			}
			present[component] = true
		}
	}
	for _, component := range required {
		component = normalizeComponent(component)
		if component == "" {
			return fmt.Errorf("--require component cannot be empty")
		}
		if !present[component] {
			combined.IncompleteChecks = append(combined.IncompleteChecks, "required component "+component+" has no valid report")
		}
	}
	return emitReportOptions(stdout, options, combined)
}

func parseReportSpec(spec string) (string, string, error) {
	component, path, found := strings.Cut(spec, "=")
	if !found {
		path = spec
		component = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	component = normalizeComponent(component)
	path = strings.TrimSpace(path)
	if component == "" || path == "" {
		return "", "", fmt.Errorf("invalid --report %q; expected COMPONENT=FILE", spec)
	}
	return component, path, nil
}

func normalizeComponent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

type reviewChangeManifest struct {
	SchemaVersion      string            `json:"schema_version"`
	ChangeID           string            `json:"change_id"`
	Commit             string            `json:"commit"`
	Environment        string            `json:"environment"`
	RequiredComponents []string          `json:"required_components"`
	Reports            map[string]string `json:"reports"`
	Sources            map[string]string `json:"sources,omitempty"`
	Tools              map[string]string `json:"tools,omitempty"`
}

func runReviewChange(args []string, stdout, stderr io.Writer) error {
	var manifestPath, format, policy string
	var width int
	var maxAge time.Duration
	fs := flag.NewFlagSet("review-change", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&manifestPath, "manifest", "review-change.json", "change manifest containing required components and report paths")
	fs.StringVar(&format, "format", "text", "output format: text, json, github, or sarif")
	fs.StringVar(&policy, "policy", "", "JSON policy containing owned, expiring suppressions")
	fs.IntVar(&width, "width", finding.DefaultTextWidth, "maximum text line width; minimum 40")
	fs.DurationVar(&maxAge, "max-age", 24*time.Hour, "maximum component review age")
	setAccessibleUsage(fs, "review-change", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if maxAge <= 0 {
		return fmt.Errorf("max-age must be positive")
	}
	if format != "text" && format != "json" && format != "github" && format != "sarif" {
		return fmt.Errorf("unknown format %q; expected text, json, github, or sarif", format)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %q: %w", manifestPath, err)
	}
	if validateConfigDocument("json", data) != nil {
		return fmt.Errorf("invalid change manifest JSON")
	}
	var manifest reviewChangeManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("parse manifest %q: %w", manifestPath, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("parse manifest %q: trailing content after JSON object", manifestPath)
	}
	if manifest.SchemaVersion != "1" {
		return fmt.Errorf("manifest schema_version must be 1")
	}
	if strings.TrimSpace(manifest.ChangeID) == "" || strings.TrimSpace(manifest.Commit) == "" || strings.TrimSpace(manifest.Environment) == "" {
		return fmt.Errorf("manifest change_id, commit, and environment are required")
	}
	if len(manifest.RequiredComponents) == 0 {
		return fmt.Errorf("manifest required_components must not be empty")
	}

	combined := finding.Report{
		Findings:         []finding.Finding{},
		CompletedChecks:  []string{fmt.Sprintf("change manifest %s; commit %s; environment %s", manifest.ChangeID, manifest.Commit, manifest.Environment)},
		IncompleteChecks: []string{},
	}
	bindingOptions := commonOptions{input: manifestPath, changeID: manifest.ChangeID, commit: manifest.Commit, environment: manifest.Environment}
	bindReportSource(&combined, bindingOptions, "review-change", data)
	if err := combined.Provenance.Validate(); err != nil {
		return fmt.Errorf("invalid change manifest provenance: %w", err)
	}
	baseDir := filepath.Dir(manifestPath)
	present := map[string]bool{}
	declared := map[string]bool{}
	componentNames := make([]string, 0, len(manifest.Reports))
	for rawComponent := range manifest.Reports {
		componentNames = append(componentNames, rawComponent)
	}
	sort.Strings(componentNames)
	for _, rawComponent := range componentNames {
		reportPath := manifest.Reports[rawComponent]
		component := normalizeComponent(rawComponent)
		if component == "" || strings.TrimSpace(reportPath) == "" {
			return fmt.Errorf("manifest contains an empty component or report path")
		}
		if declared[component] {
			return fmt.Errorf("manifest component %q is duplicated after normalization", component)
		}
		declared[component] = true
		if !filepath.IsAbs(reportPath) {
			reportPath = filepath.Join(baseDir, reportPath)
		}
		reportData, readErr := os.ReadFile(reportPath)
		if readErr != nil {
			combined.IncompleteChecks = append(combined.IncompleteChecks, fmt.Sprintf("%s: could not read report %s: %v", component, reportPath, readErr))
			continue
		}
		absoluteReport, _ := filepath.Abs(reportPath)
		combined.Provenance.Artifacts = append(combined.Provenance.Artifacts, finding.ProvenanceArtifact{Path: absoluteReport, SHA256: digestBytes(reportData)})
		before := len(combined.Findings)
		if appendErr := appendVersionedReport(&combined, reportData, component); appendErr != nil {
			combined.IncompleteChecks = append(combined.IncompleteChecks, appendErr.Error())
			continue
		}
		present[component] = true
		componentReport, decodeErr := decodeSessionReport(reportData)
		if decodeErr != nil {
			return decodeErr
		}
		p := componentReport.Provenance
		if p == nil {
			combined.IncompleteChecks = append(combined.IncompleteChecks, component+": report has no change/source provenance; regenerate bound evidence with the producing tool or adapter")
		} else {
			combined.Provenance.Artifacts = append(combined.Provenance.Artifacts, p.Artifacts...)
			if err := checkProvenanceArtifacts(p); err != nil {
				combined.IncompleteChecks = append(combined.IncompleteChecks, component+": "+err.Error())
			}
			expectedTool := manifest.Tools[rawComponent]
			if expectedTool == "" {
				expectedTool = map[string]string{"ansible": "ansible-check", "opentofu": "tofu-check", "terraform": "tofu-check", "cloud-context": "cloud-context-check", "pull-request": "pr-manager", "workflow": "workflow-check"}[component]
				if expectedTool == "" {
					expectedTool = component
				}
			}
			if p.Tool != expectedTool {
				combined.IncompleteChecks = append(combined.IncompleteChecks, component+": report tool does not match required component tool")
			}
			if p.ChangeID != manifest.ChangeID || p.Commit != manifest.Commit || p.Environment != manifest.Environment {
				combined.IncompleteChecks = append(combined.IncompleteChecks, component+": report provenance does not match change, commit and environment")
			}
			checkFresh(&combined, component+" review", p.CollectedAt, maxAge, time.Now().UTC())
			sourcePath := manifest.Sources[rawComponent]
			if sourcePath == "" {
				combined.IncompleteChecks = append(combined.IncompleteChecks, component+": source artifact is required to verify report binding")
			} else {
				if !filepath.IsAbs(sourcePath) {
					sourcePath = filepath.Join(baseDir, sourcePath)
				}
				sourceData, sourceErr := readConfigSource(sourcePath)
				absoluteSource, _ := filepath.Abs(sourcePath)
				combined.Provenance.Artifacts = append(combined.Provenance.Artifacts, finding.ProvenanceArtifact{Path: absoluteSource, SHA256: p.SourceSHA256})
				if sourceErr != nil || digestBytes(sourceData) != p.SourceSHA256 {
					combined.IncompleteChecks = append(combined.IncompleteChecks, component+": reviewed source artifact is unavailable or changed")
				}
			}
		}
		for _, item := range combined.Findings[before:] {
			if !strings.EqualFold(item.Environment, manifest.Environment) {
				combined.IncompleteChecks = append(combined.IncompleteChecks, fmt.Sprintf("%s: finding %s environment %q does not match manifest environment %q", component, item.ID, item.Environment, manifest.Environment))
			}
		}
	}
	seenRequired := map[string]bool{}
	for _, rawComponent := range manifest.RequiredComponents {
		component := normalizeComponent(rawComponent)
		if component == "" {
			return fmt.Errorf("manifest required_components contains an empty name")
		}
		if seenRequired[component] {
			return fmt.Errorf("manifest required component %q is duplicated", component)
		}
		seenRequired[component] = true
		if !present[component] {
			combined.IncompleteChecks = append(combined.IncompleteChecks, "required component "+component+" has no valid report")
		}
	}
	options := commonOptions{format: format, environment: manifest.Environment, policy: policy, width: width}
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

func appendVersionedReport(combined *finding.Report, data []byte, source string) error {
	report, err := decodeSessionReport(data)
	if err != nil {
		return fmt.Errorf("report %s: %w", source, err)
	}
	if err := appendReport(combined, data, source); err != nil {
		return err
	}
	if err := checkProvenanceArtifacts(report.Provenance); err != nil {
		combined.IncompleteChecks = append(combined.IncompleteChecks, source+": "+err.Error())
	}
	return nil
}

func runCloudContextCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var expectedCloud, expectedAccount, expectedRegion, collectedRegion string
	var expectedProject, expectedZone, configuration string
	var collect bool
	_, options, err := parseFlags("cloud-context-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		addProvenanceFlags(fs, &options)
		fs.StringVar(&expectedCloud, "expect-cloud", "", "expected cloud: aws, alicloud or gcp")
		fs.StringVar(&expectedProject, "expect-project", "", "expected GCP project ID")
		fs.StringVar(&expectedZone, "expect-zone", "", "expected configured GCP zone")
		fs.StringVar(&configuration, "configuration", "", "gcloud named configuration")
		fs.StringVar(&expectedAccount, "expect-account", "", "expected account ID")
		fs.StringVar(&expectedRegion, "expect-region", "", "expected region")
		fs.BoolVar(&collect, "collect", false, "collect identity using the provider CLI")
		fs.StringVar(&collectedRegion, "actual-region", "", "active region when using --collect")
		return &options
	})
	if err != nil {
		return err
	}
	if expectedCloud == "" || expectedAccount == "" {
		return fmt.Errorf("--expect-cloud and --expect-account are required")
	}
	if expectedCloud == "gcp" {
		if collectedRegion != "" {
			return fmt.Errorf("GCP does not accept caller-supplied --actual-region")
		}
		return runGCPContextCheck(options, expectedProject, expectedAccount, expectedRegion, expectedZone, configuration, collect, stdin, stdout)
	}
	if expectedProject != "" || expectedZone != "" || configuration != "" {
		return fmt.Errorf("GCP context options require --expect-cloud gcp")
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
				stableFindingID(&report, "CONTEXT", check.label, check.expected, check.actual), finding.SeverityCritical,
				"Cloud context does not match", "deployment-context", "deploy", options.environment,
				"The active "+check.label+" differs from the explicitly expected value.",
				fmt.Sprintf("%s expected %s; got %s", check.label, check.expected, check.actual),
				"Stop before deployment and authenticate to the intended cloud context.",
			))
		}
	}
	bindReportSource(&report, options, "cloud-context-check", data)
	return emitReportOptions(stdout, options, report)
}
