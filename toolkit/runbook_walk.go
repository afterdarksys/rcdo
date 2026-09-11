package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"reflect"
	"time"
)

type runbookStep struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Instruction    string   `json:"instruction"`
	Expected       string   `json:"expected"`
	StopConditions []string `json:"stop_conditions"`
	Checks         []string `json:"checks"`
}
type executableRunbook struct {
	SchemaVersion string            `json:"schema_version"`
	Title         string            `json:"title"`
	Targets       map[string]string `json:"targets"`
	Steps         []runbookStep     `json:"steps"`
}
type runbookProgress struct {
	Status       string         `json:"status"`
	Verification *boundArtifact `json:"verification,omitempty"`
}
type runbookState struct {
	SchemaVersion string            `json:"schema_version"`
	Source        boundArtifact     `json:"source"`
	Cursor        int               `json:"cursor"`
	Progress      []runbookProgress `json:"progress"`
}
type runbookVerification struct {
	SchemaVersion string            `json:"schema_version"`
	RunbookSHA256 string            `json:"runbook_sha256"`
	StepID        string            `json:"step_id"`
	Targets       map[string]string `json:"targets"`
	Source        string            `json:"source"`
	CollectedAt   string            `json:"collected_at"`
	Complete      *bool             `json:"complete"`
	Checks        []struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Evidence string `json:"evidence"`
	} `json:"checks"`
}

func validateRunbook(b executableRunbook) error {
	if b.SchemaVersion != "1" || !operationLabel(b.Title) || len(b.Targets) == 0 || len(b.Steps) == 0 || len(b.Steps) > 1000 {
		return fmt.Errorf("runbook requires version 1, title, targets and 1..1000 steps")
	}
	for k, v := range b.Targets {
		if !operationLabel(k) || !operationLabel(v) || isSensitivePath(k) {
			return fmt.Errorf("invalid or sensitive target field")
		}
	}
	seen := map[string]bool{}
	for _, s := range b.Steps {
		if !operationLabel(s.ID) || seen[s.ID] || !operationLabel(s.Title) || !operationLabel(s.Instruction) || !operationLabel(s.Expected) || len(s.Checks) == 0 {
			return fmt.Errorf("invalid runbook step or missing verification requirements")
		}
		seen[s.ID] = true
		checks := map[string]bool{}
		for _, id := range s.Checks {
			if !operationLabel(id) || checks[id] {
				return fmt.Errorf("invalid or duplicate required check")
			}
			checks[id] = true
		}
		for _, stop := range s.StopConditions {
			if !operationLabel(stop) {
				return fmt.Errorf("invalid stop condition")
			}
		}
	}
	return nil
}
func verifyRunbookStep(raw []byte, b executableRunbook, s runbookState, index int, age time.Duration) (finding.Report, error) {
	var v runbookVerification
	if strictJSON(raw, &v) != nil || v.SchemaVersion != "1" || v.Checks == nil {
		return finding.Report{}, fmt.Errorf("invalid runbook verification schema")
	}
	r := finding.Report{CompletedChecks: []string{"Runbook verification uses supplied observations; artifact authenticity is not independently attested"}}
	if v.RunbookSHA256 != s.Source.SHA256 || v.StepID != b.Steps[index].ID || !reflect.DeepEqual(v.Targets, b.Targets) {
		addIAC(&r, "RUN-BINDING", finding.SeverityCritical, "Verification targets a different runbook, step or environment", b.Steps[index].ID, "verify", "unknown", "Runbook hash, step ID and exact target map must all match")
	}
	checkFresh(&r, "Step verification", v.CollectedAt, age, time.Now().UTC())
	if !operationLabel(v.Source) || v.Complete == nil || !*v.Complete {
		r.IncompleteChecks = append(r.IncompleteChecks, "Verification collector evidence is incomplete")
	}
	checks := map[string]bool{}
	for _, c := range v.Checks {
		if !operationLabel(c.ID) || checks[c.ID] {
			return r, fmt.Errorf("invalid or duplicate verification check")
		}
		checks[c.ID] = true
		switch c.Status {
		case "pass":
			if !operationLabel(c.Evidence) {
				r.IncompleteChecks = append(r.IncompleteChecks, "Passing check has no evidence reference: "+c.ID)
			} else {
				r.CompletedChecks = append(r.CompletedChecks, "Check passed: "+c.ID)
			}
		case "fail":
			addIAC(&r, "RUN-FAILED", finding.SeverityHigh, "Runbook stop condition: check failed", c.ID, "verify", "unknown", "Verification status: fail; do not advance")
		default:
			r.IncompleteChecks = append(r.IncompleteChecks, "Check not verified: "+c.ID)
		}
	}
	for _, id := range b.Steps[index].Checks {
		if !checks[id] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required verification missing: "+id)
		}
	}
	return r, nil
}
func runRunbookWalk(args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && oneOf(args[0], "--help", "-help", "-h") {
		fmt.Fprintln(stderr, "Runbook commands: start, show, read, attempt, complete, verify, next, back, handoff.")
		args = append([]string{"show"}, args...)
	}
	if len(args) == 0 {
		return fmt.Errorf("runbook requires start, show, read, attempt, complete, verify, next, back or handoff")
	}
	mode, args := args[0], args[1:]
	if !oneOf(mode, "start", "show", "read", "attempt", "complete", "verify", "next", "back", "handoff") {
		return fmt.Errorf("unknown runbook command")
	}
	fs := flag.NewFlagSet("runbook "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state", ".rcdo-runbook.json", "progress file")
	input := fs.String("input", "", "runbook for start; verification artifact for verify")
	width := fs.Int("width", 72, "text width, minimum 40")
	format := fs.String("format", "text", "text or json")
	age := fs.Duration("max-age", 15*time.Minute, "maximum verification age")
	setAccessibleUsage(fs, "runbook "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *width < 40 || !oneOf(*format, "text", "json") || *age <= 0 {
		return fmt.Errorf("invalid runbook options")
	}
	var s runbookState
	var b executableRunbook
	var previous, raw []byte
	var err error
	if mode == "start" {
		if *input == "" {
			return fmt.Errorf("start requires --input")
		}
		s.Source, raw, err = captureArtifact(*input)
		s.SchemaVersion = "1"
	} else {
		previous, err = readConfigSource(*statePath)
		if err == nil && strictJSON(previous, &s) != nil {
			err = fmt.Errorf("invalid progress state")
		}
		if err == nil {
			raw, err = readBoundArtifact(s.Source)
		}
		if err != nil {
			fmt.Fprintln(stderr, "Runbook source/state is stale or unavailable; establish a new progress file.")
			return reportError{status: finding.StatusIncomplete}
		}
	}
	if err != nil {
		return err
	}
	if strictJSON(raw, &b) != nil {
		return fmt.Errorf("invalid runbook JSON")
	}
	if err = validateRunbook(b); err != nil {
		return err
	}
	if err = separateArtifact(*statePath, s.Source); err != nil {
		return err
	}
	if mode == "start" {
		s.Progress = make([]runbookProgress, len(b.Steps))
		for i := range s.Progress {
			s.Progress[i].Status = "unread"
		}
	}
	if s.SchemaVersion != "1" || len(s.Progress) != len(b.Steps) || s.Cursor < 0 || s.Cursor >= len(b.Steps) {
		return fmt.Errorf("invalid runbook progress")
	}
	for _, p := range s.Progress {
		if !oneOf(p.Status, "unread", "read", "attempted", "completed", "verified") || (p.Status == "verified" && p.Verification == nil) {
			return fmt.Errorf("invalid step progress")
		}
	}
	var report finding.Report
	mutating := !oneOf(mode, "show", "handoff")
	for i, p := range s.Progress {
		if p.Status != "verified" || (mode == "verify" && i == s.Cursor) {
			continue
		}
		if err := separateArtifact(*statePath, *p.Verification); err != nil {
			return err
		}
		evidence, e := readBoundArtifact(*p.Verification)
		if e != nil {
			report.IncompleteChecks = append(report.IncompleteChecks, "Verified evidence changed or missing for "+b.Steps[i].ID)
			continue
		}
		r, e := verifyRunbookStep(evidence, b, s, i, *age)
		if e != nil {
			report.IncompleteChecks = append(report.IncompleteChecks, "Invalid stored verification for "+b.Steps[i].ID)
		} else {
			report.Findings = append(report.Findings, r.Findings...)
			report.IncompleteChecks = append(report.IncompleteChecks, r.IncompleteChecks...)
		}
	}
	if mode == "next" && report.Status() != finding.StatusClean {
		return emitReport(stdout, *format, *width, report)
	}
	current := &s.Progress[s.Cursor]
	switch mode {
	case "read":
		if current.Status == "unread" {
			current.Status = "read"
		}
	case "attempt":
		if current.Status != "read" {
			return fmt.Errorf("read the step before recording an attempt")
		}
		current.Status = "attempted"
	case "complete":
		if current.Status != "attempted" {
			return fmt.Errorf("record an attempt before completion")
		}
		current.Status = "completed"
	case "verify":
		if *input == "" || !oneOf(current.Status, "completed", "verified") {
			return fmt.Errorf("verification requires completed operator action and --input evidence")
		}
		a, data, e := captureArtifact(*input)
		if e != nil {
			return e
		}
		if e = separateArtifact(*statePath, a); e != nil {
			return e
		}
		r, e := verifyRunbookStep(data, b, s, s.Cursor, *age)
		if e != nil {
			return e
		}
		report.Findings = append(report.Findings, r.Findings...)
		report.IncompleteChecks = append(report.IncompleteChecks, r.IncompleteChecks...)
		report.CompletedChecks = append(report.CompletedChecks, r.CompletedChecks...)
		current.Verification = &a
		current.Status = "completed"
		if r.Status() == finding.StatusClean {
			current.Status = "verified"
		}
	case "next":
		for i := 0; i <= s.Cursor; i++ {
			if s.Progress[i].Status != "verified" {
				return fmt.Errorf("all preceding steps and the current step must be verified before advancing")
			}
		}
		if s.Cursor+1 < len(b.Steps) {
			s.Cursor++
		}
	case "back":
		if s.Cursor > 0 {
			s.Cursor--
		}
	}
	if mutating {
		err = saveWorkState(*statePath, s, previous, func() error { _, e := readBoundArtifact(s.Source); return e })
		if err != nil {
			return err
		}
	}
	step := b.Steps[s.Cursor]
	if *format == "json" {
		err = json.NewEncoder(stdout).Encode(struct {
			SchemaVersion string            `json:"schema_version"`
			Runbook       executableRunbook `json:"runbook"`
			Progress      runbookState      `json:"progress"`
			Verification  finding.Report    `json:"verification"`
		}{"1", b, s, report})
	} else {
		for _, line := range []string{"Runbook: " + b.Title, fmt.Sprintf("Step %d of %d. %s. Status: %s", s.Cursor+1, len(b.Steps), step.Title, s.Progress[s.Cursor].Status), "Instruction (not executed): " + step.Instruction, "Expected result: " + step.Expected} {
			writeWrapped(stdout, markdownSafe(redactAIText(line)), *width)
		}
		for _, stop := range step.StopConditions {
			writeWrapped(stdout, "Stop condition: "+markdownSafe(stop), *width)
		}
		writeWrapped(stdout, "Reading, attempted/completed operator actions and evidence-backed verification are distinct. No commands are executed.", *width)
		if mode == "handoff" {
			for i, p := range s.Progress {
				writeWrapped(stdout, fmt.Sprintf("Step %s: %s", b.Steps[i].ID, p.Status), *width)
			}
		}
		if report.Status() != finding.StatusClean {
			return emitReport(stdout, *format, *width, report)
		}
	}
	if err != nil {
		return err
	}
	if report.Status() != finding.StatusClean {
		return reportError{status: report.Status()}
	}
	return nil
}
