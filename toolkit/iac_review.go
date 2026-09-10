package toolkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"reflect"
	"strings"
)

func readStrictJSONFile(path string, v any) error {
	data, err := readConfigSource(path)
	if err != nil {
		return err
	}
	return strictJSON(data, v)
}
func strictJSON(data []byte, v any) error {
	if err := validateConfigDocument("json", data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return fmt.Errorf("invalid JSON schema or unknown fields")
	}
	return nil
}
func digestBytes(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func runPlanReview(mode string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var before, after, resource string
	_, o, err := parseFlags(mode, args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&resource, "resource", "", "exact resource address or module prefix")
		if mode == "plan-diff" {
			fs.StringVar(&before, "before", "", "previous saved-plan JSON")
			fs.StringVar(&after, "after", "", "new saved-plan JSON")
		}
		return &o
	})
	if err != nil {
		return err
	}
	path := o.input
	if mode == "plan-diff" {
		if before == "" || after == "" || before == "-" && after == "-" {
			return fmt.Errorf("supply --before and --after; at most one may be stdin")
		}
		path = after
	}
	data, err := readInput(path, stdin)
	if err != nil {
		return err
	}
	p, err := decodePlan(data)
	if err != nil {
		return err
	}
	r := reviewIAC(p, o.environment, iacLimits{})
	r.CompletedChecks = append(r.CompletedChecks, "Plan JSON SHA-256: "+digestBytes(data))
	matches := func(a string) bool { return resource == "" || a == resource || strings.HasPrefix(a, resource+".") }
	if mode == "plan-explain" {
		found := false
		for _, res := range p.ResourceChanges {
			if !matches(res.Address) {
				continue
			}
			found = true
			evidence := []string{"Actions: " + strings.Join(res.Change.Actions, ",")}
			evidence = append(evidence, changeLines(res.Change)...)
			addIAC(&r, "PLAN-RESOURCE", finding.SeverityInfo, "Resource plan detail", res.Address, strings.Join(res.Change.Actions, ","), o.environment, strings.Join(evidence, "\n"))
		}
		if resource != "" && !found {
			return fmt.Errorf("resource or module was not found")
		}
	} else {
		oldData, err := readInput(before, stdin)
		if err != nil {
			return err
		}
		old, err := decodePlan(oldData)
		if err != nil {
			return err
		}
		oldReport := reviewIAC(old, o.environment, iacLimits{})
		r.IncompleteChecks = append(r.IncompleteChecks, oldReport.IncompleteChecks...)
		r.CompletedChecks = append(r.CompletedChecks, "Previous plan JSON SHA-256: "+digestBytes(oldData))
		oldMap, newMap := map[string]iacResource{}, map[string]iacResource{}
		keys := map[string]any{}
		for _, res := range old.ResourceChanges {
			k := res.Address + "#" + res.Deposed
			oldMap[k] = res
			keys[k] = true
		}
		for _, res := range p.ResourceChanges {
			k := res.Address + "#" + res.Deposed
			newMap[k] = res
			keys[k] = true
		}
		for _, k := range sortedKeys(keys) {
			b, be := oldMap[k]
			a, ae := newMap[k]
			address := a.Address
			if !ae {
				address = b.Address
			}
			if !matches(address) {
				continue
			}
			if !reflect.DeepEqual(b, a) {
				label := "Resource plan changed"
				if !be {
					label = "Resource added to plan"
				}
				if !ae {
					label = "Resource removed from plan"
				}
				details := []string{"Actions: " + strings.Join(b.Change.Actions, ",") + " -> " + strings.Join(a.Change.Actions, ",")}
				details = append(details, changeLines(iacChange{Before: b.Change.After, After: a.Change.After, AfterUnknown: a.Change.AfterUnknown, BeforeSensitive: b.Change.AfterSensitive, AfterSensitive: a.Change.AfterSensitive})...)
				if !reflect.DeepEqual(b.Change.AfterUnknown, a.Change.AfterUnknown) {
					details = append(details, "Unknown-value markers changed")
				}
				if !reflect.DeepEqual(b.Change.Before, a.Change.Before) {
					details = append(details, "Prior-state values changed (values withheld)")
				}
				if !reflect.DeepEqual(b.Change.ReplacePaths, a.Change.ReplacePaths) || b.ActionReason != a.ActionReason {
					details = append(details, "Replacement paths or action reason changed")
				}
				addIAC(&r, "PLAN-DIFF", finding.SeverityWarning, label, address, "compare", o.environment, strings.Join(details, "\n"))
			}
		}
		if !reflect.DeepEqual(old.OutputChanges, p.OutputChanges) {
			addIAC(&r, "PLAN-OUTPUT-DIFF", finding.SeverityWarning, "Planned outputs changed between reviews", "outputs", "compare", o.environment, "Output values withheld; recheck consumers")
		}
		current := map[string]bool{}
		for _, f := range r.Findings {
			current[f.ID] = true
		}
		for _, f := range oldReport.Findings {
			if !current[f.ID] {
				addIAC(&r, "PLAN-RESOLVED", finding.SeverityInfo, "Previous finding absent from new plan", f.Resource, "compare", o.environment, "Previous finding: "+f.ID+"; absence does not verify deployment or remediation")
			}
		}
	}
	if resource != "" {
		filtered := []finding.Finding{}
		for _, f := range r.Findings {
			if matches(f.Resource) || f.Resource == "plan" || f.Severity == finding.SeverityHigh || f.Severity == finding.SeverityCritical {
				filtered = append(filtered, f)
			}
		}
		r.Findings = filtered
		r.CompletedChecks = append(r.CompletedChecks, "Display filtered to "+resource+"; completeness checks cover the whole plan")
	}
	return emitReportOptions(stdout, o, r)
}
