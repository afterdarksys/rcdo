package toolkit

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"git-tools/finding"
)

// The adapter consumes the aggregate JSON document, never a stream whose end
// could be confused with successful collection. Raw diagnostics stay in the
// source artifact because upstream errors can contain URLs and credentials.
func runJSONProbeCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var required sessionPaths
	var maxAge time.Duration
	_, options, err := parseFlags("jsonprobe-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.Var(&required, "require", "required check name; repeatable; at least one is required")
		fs.DurationVar(&maxAge, "max-age", 15*time.Minute, "maximum evidence age; positive duration")
		return &options
	})
	if err != nil {
		return err
	}
	if len(required) == 0 || maxAge <= 0 {
		return fmt.Errorf("provide at least one --require and a positive --max-age")
	}
	if strings.TrimSpace(options.environment) == "" || options.environment == "unknown" {
		return fmt.Errorf("--environment is required; it labels this review, not a verified cloud identity")
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{}, IncompleteChecks: []string{}}
	incomplete := func(reason string) error {
		report.IncompleteChecks = append(report.IncompleteChecks, reason)
		return emitReportOptions(stdout, options, report)
	}
	data, err := readInput(options.input, stdin)
	if err != nil {
		return incomplete("jsonprobe evidence is unavailable; collect aggregate JSON again")
	}
	var source struct {
		Schema           string `json:"schema"`
		Outcome          string `json:"outcome"`
		CollectedAt      string `json:"collected_at"`
		CollectorVersion string `json:"collector_version"`
		Checks           []struct {
			Schema string `json:"schema"`
			Name   string `json:"name"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return incomplete("jsonprobe evidence is invalid JSON; provide one aggregate document")
	}
	if source.Schema != "missing-utils/jsonprobe/v1" || source.Checks == nil || len(source.Checks) == 0 {
		return incomplete("jsonprobe evidence has an unsupported schema or no checks")
	}
	collected, err := time.Parse(time.RFC3339Nano, source.CollectedAt)
	if err != nil || strings.TrimSpace(source.CollectorVersion) == "" {
		report.IncompleteChecks = append(report.IncompleteChecks, "collection time or collector version is missing; recollect with a current jsonprobe")
	} else {
		age := time.Since(collected)
		if age < -time.Minute || age > maxAge {
			report.IncompleteChecks = append(report.IncompleteChecks, "jsonprobe evidence is stale or its collection time is in the future")
		}
	}
	names := map[string]bool{}
	outcome := "pass"
	for _, check := range source.Checks {
		if check.Schema != source.Schema || strings.TrimSpace(check.Name) == "" || names[check.Name] {
			return incomplete("jsonprobe checks have missing or duplicate names or inconsistent schemas")
		}
		names[check.Name] = true
		switch check.Type {
		case "tcp", "http", "file", "process":
		default:
			return incomplete("jsonprobe evidence contains an unsupported check type")
		}
		switch check.Status {
		case "pass":
			report.CompletedChecks = append(report.CompletedChecks, "jsonprobe "+check.Name+": "+check.Type+" check passed")
		case "fail":
			outcome = "fail"
			report.CompletedChecks = append(report.CompletedChecks, "jsonprobe "+check.Name+": "+check.Type+" check ran and failed")
			digest := sha256.Sum256([]byte(check.Name))
			report.Findings = append(report.Findings, makeFinding(fmt.Sprintf("JSONPROBE-%x", digest), finding.SeverityHigh, "Readiness check failed", check.Name, "validate readiness", options.environment, "The configured probe failed; this does not establish a root cause.", "check type: "+check.Type+"; status: fail", "Inspect the source evidence and service state; rerun the required checks after correction."))
		case "error":
			if outcome == "pass" {
				outcome = "partial"
			}
			report.IncompleteChecks = append(report.IncompleteChecks, "jsonprobe "+check.Name+": collection could not complete")
		default:
			return incomplete("jsonprobe evidence contains an unknown check status")
		}
	}
	if source.Outcome != outcome {
		report.IncompleteChecks = append(report.IncompleteChecks, "jsonprobe outcome disagrees with its check results")
	}
	seenRequired := map[string]bool{}
	for _, name := range required {
		if seenRequired[name] {
			return fmt.Errorf("duplicate --require %q", name)
		}
		seenRequired[name] = true
		if !names[name] {
			report.IncompleteChecks = append(report.IncompleteChecks, "required jsonprobe check is missing: "+name)
		}
	}
	report.CompletedChecks = append(report.CompletedChecks, fmt.Sprintf("source artifact SHA-256: %x", sha256.Sum256(data)))
	if err == nil {
		report.CompletedChecks = append(report.CompletedChecks, "source collection time: "+collected.UTC().Format(time.RFC3339Nano))
	}
	return emitReportOptions(stdout, options, report)
}
