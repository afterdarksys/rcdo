package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"path/filepath"
	"strings"
	"time"
)

func runIACValidate(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var engine, dir string
	var native bool
	_, o, err := parseFlags("iac-validate", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&engine, "engine", "tofu", "tofu or terraform")
		fs.StringVar(&dir, "directory", ".", "initialized module directory")
		fs.BoolVar(&native, "native", false, "run engine validate -json; never initializes or applies")
		return &o
	})
	if err != nil {
		return err
	}
	if !oneOf(engine, "tofu", "terraform") {
		return fmt.Errorf("--engine must be tofu or terraform")
	}
	var data []byte
	var processErr error
	if native {
		if o.input != "-" {
			return fmt.Errorf("--native cannot accompany --input")
		}
		dir, err = filepath.Abs(dir)
		if err != nil {
			return err
		}
		result := executeReadOnly(engine, "-chdir="+dir, "validate", "-json")
		data, processErr = result.stdout, result.err
	} else {
		data, err = readInput(o.input, stdin)
		if err != nil {
			return err
		}
	}
	r := finding.Report{CompletedChecks: []string{"Native validation result review; remote state and services are not validated"}, Findings: []finding.Finding{}}
	var v struct {
		FormatVersion string `json:"format_version"`
		Valid         *bool  `json:"valid"`
		Errors        *int   `json:"error_count"`
		Warnings      *int   `json:"warning_count"`
		Diagnostics   []struct {
			Severity string `json:"severity"`
			Summary  string `json:"summary"`
			Range    *struct {
				Filename string `json:"filename"`
				Start    struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"diagnostics"`
	}
	if validateConfigDocument("json", data) != nil || json.Unmarshal(data, &v) != nil || v.Valid == nil || v.Errors == nil || v.Warnings == nil || strings.SplitN(v.FormatVersion, ".", 2)[0] != "1" || v.Diagnostics == nil {
		r.IncompleteChecks = append(r.IncompleteChecks, "Validation output missing or invalid; verify engine installation and module initialization")
		return emitReportOptions(stdout, o, r)
	}
	errors, warnings := 0, 0
	for _, d := range v.Diagnostics {
		severity := finding.SeverityWarning
		switch d.Severity {
		case "error":
			severity = finding.SeverityHigh
			errors++
		case "warning":
			warnings++
		default:
			r.IncompleteChecks = append(r.IncompleteChecks, "Unknown diagnostic severity")
			continue
		}
		resource := "configuration"
		if d.Range != nil {
			resource = fmt.Sprintf("%s:%d", d.Range.Filename, d.Range.Start.Line)
		}
		// Provider diagnostics may embed unmarked secrets. Do not copy summaries/snippets.
		addIAC(&r, "IAC-VALIDATE", severity, "Native validation "+d.Severity, resource, "validate", o.environment, "Diagnostic text withheld because provider messages may contain sensitive values; inspect native output locally")
	}
	if errors != *v.Errors || warnings != *v.Warnings || *v.Valid != (errors == 0) {
		r.IncompleteChecks = append(r.IncompleteChecks, "Validation counts or valid flag disagree with diagnostics")
	}
	if processErr != nil && *v.Valid {
		r.IncompleteChecks = append(r.IncompleteChecks, "Validation process failed despite valid JSON result")
	}
	if !*v.Valid && errors == 0 {
		r.IncompleteChecks = append(r.IncompleteChecks, "Invalid configuration without error diagnostics")
	}
	return emitReportOptions(stdout, o, r)
}
func runIACContext(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var expect string
	var age time.Duration
	_, o, err := parseFlags("iac-context", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&expect, "expect", "", "JSON workspace/backend/engine expectations")
		fs.DurationVar(&age, "max-age", 15*time.Minute, "maximum observation age")
		return &o
	})
	if err != nil {
		return err
	}
	if expect == "" || age <= 0 {
		return fmt.Errorf("--expect and a positive --max-age are required")
	}
	var expected map[string]string
	if err := readStrictJSONFile(expect, &expected); err != nil {
		return err
	}
	data, err := readInput(o.input, stdin)
	if err != nil {
		return err
	}
	var snapshot struct {
		SchemaVersion string            `json:"schema_version"`
		CollectedAt   string            `json:"collected_at"`
		Source        string            `json:"source"`
		Values        map[string]string `json:"values"`
	}
	if err := strictJSON(data, &snapshot); err != nil {
		return err
	}
	r := finding.Report{CompletedChecks: []string{"IaC context expectations compared to supplied observation"}}
	if snapshot.SchemaVersion != "1" || snapshot.Source == "" {
		r.IncompleteChecks = append(r.IncompleteChecks, "Context schema or source unavailable")
	}
	checkFresh(&r, "IaC context", snapshot.CollectedAt, age, time.Now().UTC())
	if len(expected) == 0 {
		return fmt.Errorf("at least one context expectation is required")
	}
	for _, k := range sortedStringValues(expected) {
		if !oneOf(k, "engine", "engine_version", "workspace", "backend", "backend_key", "account", "region") {
			return fmt.Errorf("unsupported context expectation %q", k)
		}
		actual, ok := snapshot.Values[k]
		if !ok {
			r.IncompleteChecks = append(r.IncompleteChecks, "Missing context field: "+k)
		} else if actual != expected[k] {
			addIAC(&r, "IAC-CONTEXT", finding.SeverityCritical, "IaC context mismatch", "context", "verify", o.environment, "Field: "+k+"; values withheld")
		} else {
			r.CompletedChecks = append(r.CompletedChecks, "Matched context field: "+k)
		}
	}
	return emitReportOptions(stdout, o, r)
}
func sortedStringValues(m map[string]string) []string {
	x := map[string]any{}
	for k := range m {
		x[k] = true
	}
	return sortedKeys(x)
}
