package toolkit

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"sort"
	"time"
)

// runWatch reviews a finite evidence window. Continuous monitoring is separate
// future work; no terminal redraw, daemon, or live-state inference occurs here.
func runWatch(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var resource string
	var maximum int
	_, options, err := parseFlags("watch", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var options commonOptions
		addCommonFlags(fs, &options)
		fs.StringVar(&resource, "resource", "", "filter by exact resource ID")
		fs.IntVar(&maximum, "max-groups", 20, "maximum displayed groups; remaining groups stay explicit")
		return &options
	})
	if err != nil {
		return err
	}
	if maximum < 1 || maximum > 1000 {
		return fmt.Errorf("--max-groups must be between 1 and 1000")
	}
	var source struct {
		Schema  string `json:"schema"`
		Source  string `json:"source"`
		Context string `json:"context"`
		Outcome string `json:"outcome"`
		Events  int    `json:"events"`
		Ignored int    `json:"ignored"`
		Groups  []struct {
			Resource string    `json:"resource"`
			Action   string    `json:"action"`
			ExitCode string    `json:"exit_code"`
			Count    int       `json:"count"`
			First    time.Time `json:"first"`
			Last     time.Time `json:"last"`
		} `json:"groups"`
		Diagnostics []string `json:"diagnostics"`
	}
	report := finding.Report{Findings: []finding.Finding{}, CompletedChecks: []string{}, IncompleteChecks: []string{}}
	incomplete := func(s string) error {
		report.IncompleteChecks = append(report.IncompleteChecks, s)
		return emitReportOptions(stdout, options, report)
	}
	raw, err := readInput(options.input, stdin)
	if err != nil {
		return incomplete("event window unavailable")
	}
	if json.Unmarshal(raw, &source) != nil || source.Schema != "missing-utils/eventwhy/v1" || source.Groups == nil {
		return incomplete("invalid event window")
	}
	if source.Source != "cli" && source.Source != "provided" {
		return incomplete("unknown event source")
	}
	if source.Outcome != "pass" && source.Outcome != "partial" {
		return incomplete("unknown collection outcome")
	}
	if source.Outcome == "partial" || len(source.Diagnostics) > 0 {
		report.IncompleteChecks = append(report.IncompleteChecks, "event collection has gaps or bounded history; inspect the source diagnostics")
	}
	total := 0
	seen := map[string]bool{}
	for _, g := range source.Groups {
		key := g.Resource + "\x00" + g.Action + "\x00" + g.ExitCode
		if !operationLabel(g.Resource) || !operationLabel(g.Action) || g.Count <= 0 || g.Count > 10000 || g.First.IsZero() || g.Last.Before(g.First) || seen[key] {
			return incomplete("invalid or duplicated event group")
		}
		seen[key] = true
		total += g.Count
		if resource != "" && resource != g.Resource {
			continue
		}
		severity := finding.SeverityInfo
		switch g.Action {
		case "oom":
			severity = finding.SeverityHigh
		case "die", "restart", "kill", "health_status: unhealthy":
			severity = finding.SeverityWarning
		}
		id := sha256.Sum256([]byte(key))
		item := makeFinding(fmt.Sprintf("EVENT-%x", id), severity, "Container event: "+g.Action, g.Resource, "inspect event evidence", options.environment, "This event was observed in a bounded historical window; it does not establish current health or a root cause.", fmt.Sprintf("count: %d; first: %s; last: %s", g.Count, g.First.Format(time.RFC3339), g.Last.Format(time.RFC3339)), "Inspect current health and related evidence before choosing a remediation.")
		if g.ExitCode != "" {
			if !operationLabel(g.ExitCode) || len(g.ExitCode) > 8 {
				return incomplete("invalid exit evidence")
			}
			item.Evidence = append(item.Evidence, "reported exit code: "+g.ExitCode+"; exit code alone does not prove OOM")
		}
		report.Findings = append(report.Findings, item)
	}
	if total != source.Events || source.Ignored < 0 {
		return incomplete("event count disagrees with groups")
	}
	sort.SliceStable(report.Findings, func(i, j int) bool {
		return sessionSeverity(report.Findings[i].Severity) > sessionSeverity(report.Findings[j].Severity)
	})
	matched := len(report.Findings)
	if matched > maximum {
		report.Findings = report.Findings[:maximum]
		report.IncompleteChecks = append(report.IncompleteChecks, fmt.Sprintf("display limit: %d additional matching groups omitted; increase --max-groups or filter by resource", matched-maximum))
	}
	report.CompletedChecks = append(report.CompletedChecks, fmt.Sprintf("bounded event evidence: %d container events; %d matching groups; %d other events ignored", total, matched, source.Ignored), "source: "+source.Source+"; historical review, not a live health check", fmt.Sprintf("event artifact SHA-256: %x", sha256.Sum256(raw)))
	if source.Context != "" {
		if !operationLabel(source.Context) {
			return incomplete("invalid Docker context label")
		}
		report.CompletedChecks = append(report.CompletedChecks, "Docker context label: "+source.Context)
	}
	if resource != "" && matched == 0 {
		report.IncompleteChecks = append(report.IncompleteChecks, "requested resource has no events in this artifact; current state is unknown")
	}
	return emitReportOptions(stdout, options, report)
}
