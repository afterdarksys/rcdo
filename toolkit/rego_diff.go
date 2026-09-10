package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"reflect"
	"sort"
	"time"

	"git-tools/finding"
)

func regoAllowed(r finding.Report) bool {
	for _, f := range r.Findings {
		if f.ID == "rego-denied" {
			return false
		}
	}
	return true
}
func sortedPolicyFindings(r finding.Report) []finding.Finding {
	fs := append([]finding.Finding{}, r.Findings...)
	sort.Slice(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
	return fs
}
func policyDelta(before, after finding.Report) []string {
	old := map[string]finding.Finding{}
	next := map[string]finding.Finding{}
	for _, f := range before.Findings {
		old[f.ID] = f
	}
	for _, f := range after.Findings {
		next[f.ID] = f
	}
	ids := map[string]bool{}
	for id := range old {
		ids[id] = true
	}
	for id := range next {
		ids[id] = true
	}
	keys := []string{}
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	evidence := []string{}
	for _, id := range keys {
		a, aok := old[id]
		b, bok := next[id]
		if aok && bok && reflect.DeepEqual(a, b) {
			continue
		}
		switch {
		case !aok:
			raw, _ := json.Marshal(b)
			evidence = append(evidence, "Added finding: "+string(raw))
		case !bok:
			raw, _ := json.Marshal(a)
			evidence = append(evidence, "Removed finding: "+string(raw))
		default:
			araw, _ := json.Marshal(a)
			braw, _ := json.Marshal(b)
			evidence = append(evidence, "Before finding: "+string(araw), "After finding: "+string(braw))
		}
	}
	return evidence
}

func runRegoDiff(args []string, stdout, stderr io.Writer) error {
	var o commonOptions
	var before, after []string
	var suitePath, query string
	var budget time.Duration
	_, o, err := parseFlags("rego-diff", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		policyWorkflowFlags(fs, &o, &query, &budget)
		fs.StringVar(&suitePath, "suite", "", "JSON input cases; expectations are ignored")
		fs.Func("before", "previous module; repeat to compose", func(v string) error { before = append(before, v); return nil })
		fs.Func("after", "new module; repeat to compose", func(v string) error { after = append(after, v); return nil })
		return &o
	})
	if err != nil {
		return err
	}
	if err = validatePolicyBudget(query, budget); err != nil {
		return err
	}
	suite, data, err := loadRegoSuite(suitePath, false)
	if err != nil {
		return err
	}
	oldPaths, cleanOld, err := snapshotRegoModules(before)
	if err != nil {
		return err
	}
	defer cleanOld()
	newPaths, cleanNew, err := snapshotRegoModules(after)
	if err != nil {
		return err
	}
	defer cleanNew()
	report := finding.Report{CompletedChecks: []string{"Comparison suite SHA-256: " + digestBytes(data)}}
	deadline := time.Now().Add(budget)
	for i, c := range suite.Cases {
		label := fmt.Sprintf("Case %d (%s)", i+1, c.Name)
		remaining := time.Until(deadline)
		if remaining < 100*time.Millisecond {
			report.IncompleteChecks = append(report.IncompleteChecks, fmt.Sprintf("Comparison timeout: %d cases not compared", len(suite.Cases)-i))
			break
		}
		a, e := evaluateRegoReport(oldPaths, query, o.environment, c.Input, remaining)
		if e != nil {
			report.IncompleteChecks = append(report.IncompleteChecks, label+": before evaluation failed: "+e.Error())
			continue
		}
		appendPolicyEvidence(&report, label+" before", a)
		remaining = time.Until(deadline)
		if remaining < 100*time.Millisecond {
			report.IncompleteChecks = append(report.IncompleteChecks, fmt.Sprintf("Comparison timeout: %d cases not compared", len(suite.Cases)-i))
			break
		}
		b, e := evaluateRegoReport(newPaths, query, o.environment, c.Input, remaining)
		if e != nil {
			report.IncompleteChecks = append(report.IncompleteChecks, label+": after evaluation failed: "+e.Error())
			continue
		}
		appendPolicyEvidence(&report, label+" after", b)
		if a.Status() == finding.StatusIncomplete || b.Status() == finding.StatusIncomplete {
			continue
		}
		summary := fmt.Sprintf("Before: allow=%t, status=%s. After: allow=%t, status=%s.", regoAllowed(a), a.Status(), regoAllowed(b), b.Status())
		if reflect.DeepEqual(sortedPolicyFindings(a), sortedPolicyFindings(b)) {
			report.CompletedChecks = append(report.CompletedChecks, label+": unchanged. "+summary)
			continue
		}
		severity := finding.SeverityWarning
		if (regoAllowed(a) && !regoAllowed(b)) || (a.Status() != finding.StatusBlocked && b.Status() == finding.StatusBlocked) {
			severity = finding.SeverityHigh
		}
		f := makeFinding(fmt.Sprintf("rego-diff.%d", i+1), severity, "Policy behavior changed", c.Name, "compare", o.environment, summary, "Identical input SHA-256: "+digestBytes(c.Input), "Review decision and finding changes before adopting the new policy")
		f.Evidence = append(f.Evidence, policyDelta(a, b)...)
		report.Findings = append(report.Findings, f)
	}
	return emitReport(stdout, o.format, o.width, report)
}
