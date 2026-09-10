// Package toolkit implements the RCDO command suite.
package toolkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"git-tools/finding"
)

const Version = "rcdo 1.3.0-beta.1"

var commandNames = []string{
	"context-summary",
	"log-read",
	"markdown-view", "to-markdown",
	"spacelift-watch", "iac-config-check", "command-gen", "plan-explain", "plan-diff", "iac-validate", "iac-context", "spacelift-runs", "spacelift-diff",
	"a11y-output-check", "ai-assist", "ansible-check", "ansible2ali", "ansible2aws", "cloud-context-check", "config", "config-diff", "config-explain", "config-remove", "config-set", "config-walk", "receipt-review", "decompose", "deploy-review", "diff-walk", "error-explain", "evidence-pack", "gha-tool",
	"git-danger-check", "git-isimportant-check", "git-update-json", "hcl2ali", "hcl2aws",
	"context", "handoff", "watch", "jsonprobe-check", "ops-policy-check", "pr-manager", "repo-policy-check", "review", "review-brief", "review-change", "review-session", "runbook-check", "spacelift-check", "tofu-check",
}

func Run(command string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if command == "rcdo" || command == "git-tools" || command == "" {
		if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
			printHelp(stdout)
			return 0
		}
		if args[0] == "--version" || args[0] == "version" {
			fmt.Fprintln(stdout, Version)
			return 0
		}
		command, args = args[0], args[1:]
	}

	var err error
	var configPath string
	args, configPath, err = extractRuntimeConfigFlag(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if command != "config" {
		args, err = applyConfiguredDefaults(command, args, configPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
	}
	switch command {
	case "context-summary":
		err = runContextSummary(args, stdin, stdout, stderr)
	case "log-read":
		err = runLogRead(args, stdin, stdout, stderr)
	case "markdown-view":
		err = runMarkdownView(args, stdin, stdout, stderr)
	case "to-markdown":
		err = runToMarkdown(args, stdin, stdout, stderr)
	case "spacelift-watch":
		err = runSpaceWatch(args, stdin, stdout, stderr)
	case "iac-config-check":
		err = runIACConfig(args, stdin, stdout, stderr)
	case "command-gen":
		err = runCommandGen(args, stdin, stdout, stderr)
	case "plan-explain", "plan-diff":
		err = runPlanReview(command, args, stdin, stdout, stderr)
	case "iac-validate":
		err = runIACValidate(args, stdin, stdout, stderr)
	case "iac-context":
		err = runIACContext(args, stdin, stdout, stderr)
	case "spacelift-runs", "spacelift-diff":
		err = runSpaceUtility(command, args, stdin, stdout, stderr)
	case "context":
		err = runContext(args, stdin, stdout, stderr)
	case "watch":
		err = runWatch(args, stdin, stdout, stderr)
	case "handoff":
		err = runSessionNavigation("handoff", args, stdout, stderr)
	case "config":
		err = runAppConfig(args, stdin, stdout, stderr)
	case "ai-assist":
		err = runAIAssist(args, configPath, stdin, stdout, stderr)
	case "decompose", "hcl2aws", "hcl2ali", "ansible2aws", "ansible2ali":
		err = runDecompose(command, args, stdin, stdout, stderr)
	case "diff-walk", "git-diff-walker":
		err = runDiffWalk(args, stdin, stdout, stderr)
	case "git-danger-check":
		err = runRuleCheck(command, args, stdin, stdout, stderr, dangerRules)
	case "gha-tool":
		err = runGHA(args, stdin, stdout, stderr)
	case "ansible-check":
		err = runAnsible(args, stdin, stdout, stderr)
	case "runbook-check":
		err = runRunbookCheck(args, stdin, stdout, stderr)
	case "jsonprobe-check":
		err = runJSONProbeCheck(args, stdin, stdout, stderr)
	case "tofu-check":
		err = runTofuCheck(args, stdin, stdout, stderr)
	case "spacelift-check":
		err = runSpaceliftCheck(args, stdin, stdout, stderr)
	case "pr-manager":
		err = runPRManager(args, stdin, stdout, stderr)
	case "git-isimportant-check":
		err = runImportantCheck(args, stdin, stdout, stderr)
	case "git-update-json":
		err = runJSONUpdate(args, stdin, stdout, stderr)
	case "deploy-review":
		err = runDeployReview(args, stdin, stdout, stderr)
	case "review-change":
		err = runReviewChange(args, stdout, stderr)
	case "cloud-context-check":
		err = runCloudContextCheck(args, stdin, stdout, stderr)
	case "config-explain":
		err = runConfigExplain(args, stdin, stdout, stderr)
	case "config-diff":
		err = runConfigDiff(args, stdin, stdout, stderr)
	case "receipt-review":
		err = runReceiptReview(args, stdout, stderr)
	case "config-walk":
		err = runConfigWalk(args, stdout, stderr)
	case "config-set":
		err = runConfigMutate("set", args, stdin, stdout, stderr)
	case "config-remove":
		err = runConfigMutate("remove", args, stdin, stdout, stderr)
	case "review", "git-review":
		err = runRepositoryReview(args, stdin, stdout, stderr)
	case "repo-policy-check":
		err = runRepositoryPolicyCheck(args, stdin, stdout, stderr)
	case "ops-policy-check":
		err = runOperationalPolicyCheck(args, stdin, stdout, stderr)
	case "review-session":
		err = runReviewSession(args, stdout, stderr)
	case "error-explain":
		err = runErrorExplain(args, stdin, stdout, stderr)
	case "review-brief":
		err = runReviewBrief(args, stdin, stdout, stderr)
	case "a11y-output-check":
		err = runA11yOutputCheck(args, stdin, stdout, stderr)
	case "evidence-pack":
		err = runEvidencePack(args, stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n", command)
		printHelp(stderr)
		return 2
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var reported reportError
	if errors.As(err, &reported) {
		return statusExitCode(reported.status)
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
	return 2
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, "rcdo: Ryan Coleman's accessible DevOps toolkit")
	fmt.Fprintln(w, "Usage: rcdo COMMAND [options]")
	fmt.Fprintln(w, "Commands:")
	for _, name := range commandNames {
		fmt.Fprintf(w, "  %s\n", name)
	}
	fmt.Fprintln(w, "All review commands accept --format text or --format json.")
}

type reportError struct{ status finding.Status }

func (e reportError) Error() string { return string(e.status) }

func statusExitCode(status finding.Status) int {
	switch status {
	case finding.StatusReview:
		return 10
	case finding.StatusBlocked:
		return 20
	case finding.StatusIncomplete:
		return 30
	default:
		return 0
	}
}

type commonOptions struct {
	input       string
	format      string
	environment string
	policy      string
	width       int
}

func addCommonFlags(fs *flag.FlagSet, options *commonOptions) {
	fs.StringVar(&options.input, "input", "-", "input file; use - for standard input")
	fs.StringVar(&options.format, "format", "text", "output format: text or json")
	fs.StringVar(&options.environment, "environment", "unknown", "deployment environment label")
	fs.StringVar(&options.policy, "policy", "", "JSON policy containing owned, expiring suppressions")
	fs.IntVar(&options.width, "width", finding.DefaultTextWidth, "maximum text line width; minimum 40")
}

// overrideIntDefault lets one command take a different default from the common
// flag set without re-registering the flag, which panics. It updates DefValue
// too so `--help` does not advertise a default the command will not use.
func overrideIntDefault(fs *flag.FlagSet, target *int, name string, value int) {
	*target = value
	if f := fs.Lookup(name); f != nil {
		f.DefValue = strconv.Itoa(value)
	}
}

func parseFlags(name string, args []string, stderr io.Writer, configure func(*flag.FlagSet) *commonOptions) (*flag.FlagSet, commonOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	options := configure(fs)
	setAccessibleUsage(fs, name, stderr)
	if err := fs.Parse(args); err != nil {
		return fs, *options, err
	}
	if fs.NArg() > 0 {
		return fs, *options, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if options.format != "text" && options.format != "json" && options.format != "sarif" && options.format != "github" {
		return fs, *options, fmt.Errorf("unknown format %q; expected text, json, sarif, or github", options.format)
	}
	if options.width < finding.MinTextWidth {
		return fs, *options, fmt.Errorf("--width must be at least %d", finding.MinTextWidth)
	}
	return fs, *options, nil
}

func setAccessibleUsage(fs *flag.FlagSet, name string, w io.Writer) {
	fs.Usage = func() {
		fmt.Fprintf(w, "Usage: %s [options]\n", name)
		fs.VisitAll(func(option *flag.Flag) {
			fmt.Fprintf(w, "\nOption: --%s\n", option.Name)
			writeWrapped(w, "Description: "+option.Usage, 88)
			if option.DefValue != "" && option.DefValue != "false" && option.DefValue != "0" {
				fmt.Fprintf(w, "Default: %s\n", option.DefValue)
			}
		})
	}
}

func readInput(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read standard input: %w", err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, fmt.Errorf("input is empty")
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input %q: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("input %q is empty", path)
	}
	return data, nil
}

func emitReport(w io.Writer, format string, width int, report finding.Report) error {
	// Commands that build commonOptions directly rather than through
	// addCommonFlags never register --width, so width arrives as 0. Fall back
	// rather than refusing to render a report the user asked for.
	if width <= 0 {
		width = finding.DefaultTextWidth
	}
	var err error
	if format == "json" {
		err = finding.RenderJSON(w, report)
	} else if format == "sarif" {
		err = finding.RenderSARIF(w, report)
	} else if format == "github" {
		err = finding.RenderGitHub(w, report)
	} else {
		err = finding.RenderTextWidth(w, report, width)
	}
	if err != nil {
		return err
	}
	if report.Status() != finding.StatusClean {
		return reportError{status: report.Status()}
	}
	return nil
}

func emitReportOptions(w io.Writer, options commonOptions, report finding.Report) error {
	filtered, err := applyPolicy(options.policy, report, time.Now().UTC())
	if err != nil {
		return err
	}
	return emitReport(w, options.format, options.width, filtered)
}

func makeFinding(id string, severity finding.Severity, title, resource, action, environment, reason, evidence, remediation string) finding.Finding {
	return finding.Finding{
		ID: id, Severity: severity, Title: title, Resource: resource, Action: action,
		Environment: environment, Reason: reason, Evidence: []string{evidence},
		Confidence: finding.ConfidenceMedium, Remediation: remediation,
	}
}

// stableFindingID keeps acknowledgements useful when unrelated findings are
// inserted earlier in a report. Only genuinely identical findings receive a
// numeric disambiguator.
func stableFindingID(report *finding.Report, prefix string, parts ...string) string {
	for index := range parts {
		parts[index] = strings.ToLower(strings.TrimSpace(parts[index]))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	base := fmt.Sprintf("%s-%X", strings.ToUpper(prefix), digest[:5])
	candidate := base
	for duplicate := 2; ; duplicate++ {
		used := false
		for _, item := range report.Findings {
			if item.ID == candidate {
				used = true
				break
			}
		}
		if !used {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, duplicate)
	}
}

func decodeObject(data []byte) (map[string]any, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	return object, nil
}

func stringValue(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			switch typed := value.(type) {
			case string:
				return typed
			case json.Number:
				return typed.String()
			case float64:
				return fmt.Sprintf("%.0f", typed)
			}
		}
	}
	return ""
}

func boolValue(object map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			if typed, valid := value.(bool); valid {
				return typed, true
			}
		}
	}
	return false, false
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func basename(path string) string { return filepath.Base(strings.TrimSpace(path)) }

func normalizeCheckSubcommand(args []string) []string {
	if len(args) > 0 && (args[0] == "check" || args[0] == "dangerous") {
		return args[1:]
	}
	return args
}
