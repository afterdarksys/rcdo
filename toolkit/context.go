package toolkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"gopkg.in/yaml.v3"
	"io"
	"strings"
	"time"
	"unicode"
)

func operationLabel(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 2048 && !strings.ContainsFunc(s, unicode.IsControl)
}

type workContext struct {
	SchemaVersion string `yaml:"schema_version"`
	Name          string `yaml:"name"`
	Environment   string `yaml:"environment"`
	Cloud         string `yaml:"cloud"`
	Profile       string `yaml:"profile"`
	Region        string `yaml:"region"`
	Account       string `yaml:"account"`
	Principal     string `yaml:"principal,omitempty"`
}

func runContext(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var expected string
	var maxAge time.Duration
	_, options, err := parseFlags("context", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&expected, "expect", "", "named JSON/YAML context expectation file")
		fs.DurationVar(&maxAge, "max-age", 5*time.Minute, "maximum observation age")
		return &options
	})
	if err != nil {
		return err
	}
	if expected == "" || maxAge <= 0 {
		return fmt.Errorf("--expect and positive --max-age are required")
	}
	if options.policy != "" {
		return fmt.Errorf("context identity checks do not support suppressions")
	}
	data, err := readInput(expected, stdin)
	if err != nil {
		return fmt.Errorf("cannot read context expectations")
	}
	var want workContext
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if decoder.Decode(&want) != nil || decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("expectations must contain one valid context document")
	}
	if want.SchemaVersion != "1" || (want.Cloud != "aws" && want.Cloud != "alicloud") {
		return fmt.Errorf("context requires schema_version 1 and cloud aws or alicloud")
	}
	for _, s := range []string{want.Name, want.Environment, want.Profile, want.Region, want.Account} {
		if !operationLabel(s) {
			return fmt.Errorf("context name, environment, profile, region and account are required single-line values")
		}
	}
	if want.Principal != "" && !operationLabel(want.Principal) {
		return fmt.Errorf("invalid expected principal")
	}
	options.environment = want.Environment
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{}, IncompleteChecks: []string{}}
	incomplete := func(message string) error {
		report.IncompleteChecks = append(report.IncompleteChecks, message)
		return emitReportOptions(stdout, options, report)
	}
	raw, err := readInput(options.input, stdin)
	if err != nil {
		return incomplete("context snapshot unavailable")
	}
	var got struct {
		Schema           string    `json:"schema"`
		Source           string    `json:"source"`
		Cloud            string    `json:"cloud"`
		Profile          string    `json:"profile"`
		Region           string    `json:"region"`
		Account          string    `json:"account"`
		Principal        string    `json:"principal"`
		CollectedAt      time.Time `json:"collected_at"`
		CollectorVersion string    `json:"collector_version"`
		Outcome          string    `json:"outcome"`
		Diagnostics      []string  `json:"diagnostics"`
	}
	if json.Unmarshal(raw, &got) != nil || got.Schema != "missing-utils/contextsnap/v1" {
		return incomplete("invalid or unsupported context snapshot")
	}
	if got.Source != "cli" && got.Source != "provided" {
		return incomplete("context evidence source is unknown")
	}
	if got.Outcome != "pass" || len(got.Diagnostics) > 0 {
		return incomplete("identity collector did not complete; recollect before using this context")
	}
	for _, s := range []string{got.Cloud, got.Profile, got.Region, got.Account, got.Principal, got.CollectorVersion} {
		if !operationLabel(s) {
			return incomplete("snapshot contains missing or unsafe identity labels")
		}
	}
	if got.CollectedAt.IsZero() || time.Since(got.CollectedAt) > maxAge || time.Until(got.CollectedAt) > time.Minute {
		report.IncompleteChecks = append(report.IncompleteChecks, "identity evidence is stale or its observation time is invalid")
	}
	fields := [][3]string{{"cloud", want.Cloud, got.Cloud}, {"profile", want.Profile, got.Profile}, {"region", want.Region, got.Region}, {"account", want.Account, got.Account}}
	if want.Principal != "" {
		fields = append(fields, [3]string{"principal", want.Principal, got.Principal})
	}
	for _, field := range fields {
		if field[1] != field[2] {
			report.Findings = append(report.Findings, makeFinding("CONTEXT-"+strings.ToUpper(field[0]), finding.SeverityCritical, "Work context does not match intended "+field[0], want.Name, "verify target", want.Environment, "The collected value differs from the named context.", "expected: "+field[1]+"; observed: "+field[2], "Select the intended context and recollect identity before continuing."))
		}
	}
	report.CompletedChecks = append(report.CompletedChecks, "context: "+want.Name+"; environment: "+want.Environment, "cloud: "+got.Cloud+"; account: "+got.Account, "principal: "+got.Principal, "profile: "+got.Profile, "region selected for collector: "+got.Region+"; STS does not establish resource location", "observation time: "+got.CollectedAt.UTC().Format(time.RFC3339), fmt.Sprintf("snapshot SHA-256: %x", sha256.Sum256(raw)), fmt.Sprintf("expectations SHA-256: %x", sha256.Sum256(data)))
	if got.Source == "provided" {
		report.CompletedChecks = append(report.CompletedChecks, "source: supplied snapshot and timestamp; live identity was not verified by this invocation")
	} else {
		report.CompletedChecks = append(report.CompletedChecks, "source: CLI identity observation; local artifact is unsigned")
	}
	return emitReportOptions(stdout, options, report)
}
