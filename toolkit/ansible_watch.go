package toolkit

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"git-tools/finding"
)

type ansibleEvent struct {
	SchemaVersion string `json:"schema_version"`
	Event         string `json:"event"`
	RunID         string `json:"run_id"`
	Sequence      int    `json:"sequence"`
	At            string `json:"at"`
	CheckMode     *bool  `json:"check_mode,omitempty"`
	Host          string `json:"host,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	Task          string `json:"task,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	NoLog         bool   `json:"no_log,omitempty"`
	Ignored       bool   `json:"ignored,omitempty"`
}

func reviewAnsibleEvents(data []byte, required []string, expectedRun string, age time.Duration) (finding.Report, error) {
	r := finding.Report{CompletedChecks: []string{"Finite Ansible callback artifact; no playbook is launched", "Changed/completed task results do not independently verify service health; skipped tasks do not establish check-mode support"}}
	if len(data) > 16<<20 {
		return r, fmt.Errorf("Ansible artifact exceeds 16 MiB")
	}
	wanted := map[string]bool{}
	for _, h := range required {
		if !operationLabel(h) || markdownSafe(h) != h || wanted[h] {
			return r, fmt.Errorf("invalid or duplicate required host")
		}
		wanted[h] = true
	}
	if len(wanted) == 0 {
		return r, fmt.Errorf("at least one --require-host is required")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	seenHosts := map[string]bool{}
	counts := map[string]int{}
	runID := ""
	finished := false
	lines := 0
	var last time.Time
	for scanner.Scan() {
		var e ansibleEvent
		if strictJSON(scanner.Bytes(), &e) != nil || e.SchemaVersion != "1" || e.Sequence != lines {
			return r, fmt.Errorf("invalid callback record or noncontiguous sequence")
		}
		lines++
		if lines > 100000 {
			return r, fmt.Errorf("callback record limit exceeded")
		}
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil || (!last.IsZero() && at.Before(last)) {
			return r, fmt.Errorf("invalid or backward event time")
		}
		last = at
		if finished {
			return r, fmt.Errorf("callback contains records after finish")
		}
		if lines == 1 {
			if e.Event != "start" || !operationLabel(e.RunID) || e.CheckMode == nil {
				return r, fmt.Errorf("callback must begin with run identity and mode")
			}
			runID = e.RunID
			if expectedRun != "" && runID != expectedRun {
				addIAC(&r, "ANS-RUN", finding.SeverityCritical, "Ansible run identity differs", "run", "review", "unknown", "Expected run ID does not match callback header")
			}
			mode := "execution"
			if *e.CheckMode {
				mode = "check mode: predictions, not applied changes"
			}
			r.CompletedChecks = append(r.CompletedChecks, "Run: "+safeReportText(runID)+"; "+mode)
			continue
		}
		if e.RunID != runID {
			return r, fmt.Errorf("callback mixes run identities")
		}
		if e.Event == "finish" {
			finished = true
			continue
		}
		if e.Event != "result" || e.CheckMode == nil || !operationLabel(e.Host) || !operationLabel(e.TaskID) || markdownSafe(e.Host) != e.Host || markdownSafe(e.TaskID) != e.TaskID || !oneOf(e.Outcome, "ok", "changed", "failed", "unreachable", "skipped") {
			return r, fmt.Errorf("invalid task result")
		}
		seenHosts[e.Host] = true
		counts[e.Outcome]++
		if !wanted[e.Host] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Observed host outside required scope: "+e.Host)
		}
		severity := finding.SeverityInfo
		switch e.Outcome {
		case "failed":
			severity = finding.SeverityHigh
		case "unreachable":
			severity = finding.SeverityHigh
			r.IncompleteChecks = append(r.IncompleteChecks, "Host unreachable during task: "+e.Host+"/"+e.TaskID)
		case "changed":
			severity = finding.SeverityWarning
		}
		task := safeReportText(e.Task)
		if e.NoLog {
			task = "task name withheld (no_log)"
		}
		if task == "" {
			task = "unnamed task"
		}
		mode := "execution"
		if *e.CheckMode {
			mode = "check mode; no applied-change claim"
		}
		title := fmt.Sprintf("%s: %s", e.Outcome, task)
		evidence := fmt.Sprintf("Record %d; host %s; task ID %s; mode %s; ignored failure %t; no_log %t", e.Sequence, e.Host, e.TaskID, mode, e.Ignored, e.NoLog)
		f := makeFinding(stableFindingID(&r, "ANS-TASK", runID, e.Host, e.TaskID, fmt.Sprint(e.Sequence)), severity, title, e.Host+"/"+e.TaskID, "review", "unknown", evidence, evidence, "Inspect this host/task record; use independent outcome checks before declaring recovery")
		r.Findings = append(r.Findings, f)
	}
	if scanner.Err() != nil {
		return r, fmt.Errorf("callback line exceeds limit or could not be read")
	}
	if lines == 0 {
		return r, fmt.Errorf("empty callback")
	}
	if !finished {
		r.IncompleteChecks = append(r.IncompleteChecks, "Final callback receipt missing; run may still be active or interrupted")
	}
	for _, h := range required {
		if !seenHosts[h] {
			r.IncompleteChecks = append(r.IncompleteChecks, "Required host has no task results: "+h)
		}
	}
	checkFresh(&r, "Last callback observation", last.Format(time.RFC3339Nano), age, time.Now().UTC())
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("Task result counts: %d ok; %d changed; %d failed; %d unreachable; %d skipped. Counts include repeated tasks, not unique hosts.", counts["ok"], counts["changed"], counts["failed"], counts["unreachable"], counts["skipped"]), "Source SHA-256: "+digestBytes(data))
	return r, nil
}
func runAnsibleWatch(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var hosts sessionPaths
	var runID string
	var age time.Duration
	_, o, err := parseFlags("ansible-watch", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.Var(&hosts, "require-host", "required inventory hostname; repeatable")
		fs.StringVar(&runID, "expect-run", "", "expected callback run UUID")
		fs.DurationVar(&age, "max-age", 24*time.Hour, "maximum age of last callback observation")
		return &o
	})
	if err != nil {
		return err
	}
	if age <= 0 || o.policy != "" {
		return fmt.Errorf("positive max-age and unsuppressed coverage required")
	}
	var data []byte
	if o.input == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 16<<20+1))
	} else {
		data, err = readConfigSource(o.input)
	}
	if err != nil {
		return err
	}
	r, err := reviewAnsibleEvents(data, hosts, strings.TrimSpace(runID), age)
	if err != nil {
		return err
	}
	return emitReportOptions(stdout, o, r)
}
