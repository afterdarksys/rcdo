package toolkit

import (
	"flag"
	"fmt"
	"io"
	"time"

	"git-tools/finding"
)

func runPolicyReview(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var o commonOptions
	var modules []string
	var query, limitsPath string
	var budget time.Duration
	_, o, err := parseFlags("policy-review", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		policyWorkflowFlags(fs, &o, &query, &budget)
		fs.StringVar(&o.input, "input", "-", "Terraform/OpenTofu saved-plan JSON, or stdin")
		fs.StringVar(&limitsPath, "limits", "", "JSON deletion/replacement limits and critical resource addresses")
		fs.Func("rego", "module file; repeat to compose", func(v string) error { modules = append(modules, v); return nil })
		return &o
	})
	if err != nil {
		return err
	}
	if err = validatePolicyBudget(query, budget); err != nil {
		return err
	}
	var input []byte
	if o.input == "-" {
		input, err = io.ReadAll(io.LimitReader(stdin, (16<<20)+1))
	} else {
		input, err = readConfigSource(o.input)
	}
	if err != nil {
		return err
	}
	if len(input) > 16<<20 {
		return fmt.Errorf("input exceeds 16 MiB")
	}
	if err = validateConfigDocument("json", input); err != nil {
		return err
	}
	var limits iacLimits
	var limitsHash string
	if limitsPath != "" {
		data, e := readConfigSource(limitsPath)
		if e != nil {
			return e
		}
		if e = strictJSON(data, &limits); e != nil {
			return e
		}
		limitsHash = digestBytes(data)
	}
	if (limits.MaxDeletes != nil && *limits.MaxDeletes < 0) || (limits.MaxReplacements != nil && *limits.MaxReplacements < 0) {
		return fmt.Errorf("impact limits must be nonnegative")
	}
	paths, cleanup, err := snapshotRegoModules(modules)
	if err != nil {
		return err
	}
	defer cleanup()
	report := finding.Report{CompletedChecks: []string{"Shared plan input SHA-256: " + digestBytes(input)}}
	if limitsHash != "" {
		report.CompletedChecks = append(report.CompletedChecks, "IaC limits SHA-256: "+limitsHash)
	}
	// Each check contributes its own status and namespaced findings. Neither can
	// suppress the other, and an incomplete check has aggregate precedence.
	merge := func(name string, r finding.Report) {
		report.CompletedChecks = append(report.CompletedChecks, fmt.Sprintf("Check %s: status=%s; findings=%d; incomplete=%d", name, r.Status(), len(r.Findings), len(r.IncompleteChecks)))
		appendPolicyEvidence(&report, name, r)
		for _, f := range r.Findings {
			f.ID = name + "/" + f.ID
			report.Findings = append(report.Findings, f)
		}
	}
	deadline := time.Now().Add(budget)
	plan, e := decodePlan(input)
	if e != nil {
		merge("iac", finding.Report{IncompleteChecks: []string{"Plan cannot be decoded: " + e.Error()}})
	} else {
		merge("iac", reviewIAC(plan, o.environment, limits))
	}
	remaining := time.Until(deadline)
	if remaining < 100*time.Millisecond {
		merge("rego", finding.Report{IncompleteChecks: []string{"Review evaluation budget exhausted"}})
	} else {
		r, e := evaluateRegoReport(paths, query, o.environment, input, remaining)
		if e != nil {
			r = finding.Report{IncompleteChecks: []string{"Evaluation failed: " + e.Error()}}
		}
		merge("rego", r)
	}
	return emitReportOptions(stdout, o, report)
}
