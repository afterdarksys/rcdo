package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
)

func runReceiptReview(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("receipt-review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "runreceipt JSON file")
	width := fs.Int("width", 72, "text width")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" || fs.NArg() != 0 || *width < 40 {
		return fmt.Errorf("provide --input and width at least 40")
	}
	data, err := readConfigSource(*input)
	if err != nil {
		return err
	}
	var r struct {
		Schema, Label, Scope, State, Reason string
		OutcomeVerified                     bool `json:"outcome_verified"`
		Stages                              []struct {
			Index             int
			Executable, State string
			ExitCode          *int `json:"exit_code"`
		}
	}
	if json.Unmarshal(data, &r) != nil || r.Schema != "missing-utils/runreceipt/v1" || r.Scope != "local_process_only" || len(r.Stages) == 0 || len(r.Stages) > 16 || r.OutcomeVerified {
		return fmt.Errorf("invalid or unsupported execution receipt")
	}
	status := finding.StatusClean
	var out bytes.Buffer
	fmt.Fprintf(&out, "EXECUTION RECEIPT\nOperation: %s\nRecorder state: %s\n", r.Label, r.State)
	if r.State != "completed" {
		status = finding.StatusIncomplete
	}
	for i, s := range r.Stages {
		if s.Index != i+1 {
			return fmt.Errorf("invalid stage ordering")
		}
		result := "completion unknown"
		if s.State == "exited" && s.ExitCode != nil && *s.ExitCode >= 0 {
			result = fmt.Sprintf("local exit %d", *s.ExitCode)
			if *s.ExitCode != 0 && status == finding.StatusClean {
				status = finding.StatusReview
			}
		} else {
			status = finding.StatusIncomplete
		}
		fmt.Fprintf(&out, "Stage %d: %s; %s\n", s.Index, s.Executable, result)
	}
	fmt.Fprintln(&out, "Outcome verified: no. Check the target independently before retrying.\nThis receipt records local processes, not remote deployment success.")
	sessionText(stdout, out.String(), *width)
	if status != finding.StatusClean {
		return reportError{status: status}
	}
	return nil
}
