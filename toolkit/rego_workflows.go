package toolkit

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"git-tools/finding"
)

type regoCase struct {
	Name   string           `json:"name"`
	Input  json.RawMessage  `json:"input"`
	Expect *regoExpectation `json:"expect,omitempty"`
}
type regoExpectation struct {
	Status     finding.Status `json:"status"`
	FindingIDs *[]string      `json:"finding_ids,omitempty"`
}
type regoSuite struct {
	SchemaVersion string     `json:"schema_version"`
	Cases         []regoCase `json:"cases"`
}

func loadRegoSuite(path string, expectations bool) (regoSuite, []byte, error) {
	var suite regoSuite
	data, err := readConfigSource(path)
	if err != nil {
		return suite, nil, err
	}
	if err = strictJSON(data, &suite); err != nil {
		return suite, nil, err
	}
	if suite.SchemaVersion != "1" || len(suite.Cases) == 0 || len(suite.Cases) > 100 {
		return suite, nil, fmt.Errorf("suite requires schema_version 1 and 1 to 100 cases")
	}
	seen := map[string]bool{}
	for _, c := range suite.Cases {
		if strings.TrimSpace(c.Name) == "" || seen[c.Name] || len(c.Input) == 0 {
			return suite, nil, fmt.Errorf("cases require unique nonempty names and input")
		}
		seen[c.Name] = true
		if expectations && c.Expect == nil {
			return suite, nil, fmt.Errorf("each test requires expect")
		}
		if c.Expect != nil {
			if c.Expect.Status != finding.StatusClean && c.Expect.Status != finding.StatusReview && c.Expect.Status != finding.StatusBlocked {
				return suite, nil, fmt.Errorf("expected status must be clean, review, or blocked; incomplete evaluations cannot pass a test")
			}
			if c.Expect.FindingIDs != nil {
				ids := map[string]bool{}
				for _, id := range *c.Expect.FindingIDs {
					if strings.TrimSpace(id) == "" || ids[id] {
						return suite, nil, fmt.Errorf("expected finding IDs must be nonempty and unique")
					}
					ids[id] = true
				}
			}
		}
	}
	return suite, data, nil
}

// Freeze modules once so all cases and pipeline checks use reviewed bytes.
func snapshotRegoModules(paths []string) ([]string, func(), error) {
	if len(paths) == 0 || len(paths) > 32 {
		return nil, nil, fmt.Errorf("provide 1 to 32 module files")
	}
	dir, err := os.MkdirTemp("", "rcdo-policy-suite-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	targets := []string{}
	total := 0
	for i, path := range paths {
		data, e := readConfigSource(path)
		if e != nil {
			cleanup()
			return nil, nil, e
		}
		total += len(data)
		if total > 16<<20 {
			cleanup()
			return nil, nil, fmt.Errorf("combined modules exceed 16 MiB")
		}
		target := filepath.Join(dir, fmt.Sprintf("module%d.rego", i))
		if e = os.WriteFile(target, data, 0600); e != nil {
			cleanup()
			return nil, nil, e
		}
		targets = append(targets, target)
	}
	return targets, cleanup, nil
}

func evaluateRegoReport(modules []string, query, environment string, input []byte, timeout time.Duration) (finding.Report, error) {
	var output, diagnostics bytes.Buffer
	args := []string{"--format", "json", "--query", query, "--environment", environment, "--timeout", timeout.String()}
	for _, p := range modules {
		args = append(args, "--rego", p)
	}
	err := runRego(args, bytes.NewReader(input), &output, &diagnostics)
	var statusErr reportError
	if err != nil && !errors.As(err, &statusErr) {
		return finding.Report{}, err
	}
	var report finding.Report
	if json.Unmarshal(output.Bytes(), &report) != nil {
		return report, fmt.Errorf("cannot decode Rego report")
	}
	if e := report.Validate(); e != nil {
		return finding.Report{}, fmt.Errorf("invalid Rego report")
	}
	return report, nil
}

func regoIDs(r finding.Report) []string {
	ids := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	return ids
}
func policyWorkflowFlags(fs *flag.FlagSet, o *commonOptions, query *string, timeout *time.Duration) {
	fs.StringVar(&o.format, "format", "text", "text, json, sarif, or github")
	fs.IntVar(&o.width, "width", finding.DefaultTextWidth, "text width")
	fs.StringVar(&o.environment, "environment", "unknown", "environment label")
	fs.StringVar(query, "query", "data.rcdo.decision", "decision reference")
	fs.DurationVar(timeout, "timeout", 30*time.Second, "total evaluation budget, 100ms to 1m")
}
func validatePolicyBudget(query string, timeout time.Duration) error {
	// Keep validation independent of case evaluation, including empty time budgets.
	if !regoQueryPattern.MatchString(query) {
		return fmt.Errorf("query must be a data.package.rule reference")
	}
	if timeout < 100*time.Millisecond || timeout > time.Minute {
		return fmt.Errorf("timeout must be between 100ms and 1m")
	}
	return nil
}
func appendPolicyEvidence(dst *finding.Report, label string, src finding.Report) {
	for _, s := range src.CompletedChecks {
		dst.CompletedChecks = append(dst.CompletedChecks, label+": "+s)
	}
	for _, s := range src.IncompleteChecks {
		dst.IncompleteChecks = append(dst.IncompleteChecks, label+": "+s)
	}
}

func runRegoTest(args []string, stdout, stderr io.Writer) error {
	var o commonOptions
	var modules []string
	var suitePath, query string
	var budget time.Duration
	_, o, err := parseFlags("rego-test", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		policyWorkflowFlags(fs, &o, &query, &budget)
		fs.StringVar(&suitePath, "suite", "", "JSON fixture suite")
		fs.Func("rego", "module file; repeat to compose", func(v string) error { modules = append(modules, v); return nil })
		return &o
	})
	if err != nil {
		return err
	}
	if err = validatePolicyBudget(query, budget); err != nil {
		return err
	}
	suite, data, err := loadRegoSuite(suitePath, true)
	if err != nil {
		return err
	}
	paths, cleanup, err := snapshotRegoModules(modules)
	if err != nil {
		return err
	}
	defer cleanup()
	report := finding.Report{CompletedChecks: []string{"Fixture suite SHA-256: " + digestBytes(data)}}
	deadline := time.Now().Add(budget)
	for i, c := range suite.Cases {
		label := fmt.Sprintf("Case %d (%s)", i+1, c.Name)
		remaining := time.Until(deadline)
		if remaining < 100*time.Millisecond {
			report.IncompleteChecks = append(report.IncompleteChecks, fmt.Sprintf("Suite timeout: %d cases not evaluated", len(suite.Cases)-i))
			break
		}
		result, e := evaluateRegoReport(paths, query, o.environment, c.Input, remaining)
		if e != nil {
			report.IncompleteChecks = append(report.IncompleteChecks, label+": evaluation failed: "+e.Error())
			continue
		}
		appendPolicyEvidence(&report, label, result)
		if result.Status() == finding.StatusIncomplete {
			continue
		}
		matches := result.Status() == c.Expect.Status
		reason := fmt.Sprintf("Expected status %s; observed %s.", c.Expect.Status, result.Status())
		if c.Expect.FindingIDs != nil {
			expected := append([]string{}, (*c.Expect.FindingIDs)...)
			sort.Strings(expected)
			ids := regoIDs(result)
			matches = matches && reflect.DeepEqual(expected, ids)
			reason += fmt.Sprintf(" Expected finding IDs %v; observed %v.", expected, ids)
		}
		if matches {
			report.CompletedChecks = append(report.CompletedChecks, label+": PASS. "+reason)
		} else {
			report.Findings = append(report.Findings, makeFinding(fmt.Sprintf("rego-test.%d", i+1), finding.SeverityHigh, "Policy fixture failed", c.Name, "test", o.environment, reason, "Fixture input SHA-256: "+digestBytes(c.Input), "Review the policy change or correct the fixture expectation"))
		}
	}
	return emitReport(stdout, o.format, o.width, report)
}
