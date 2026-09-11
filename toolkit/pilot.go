package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"strings"
	"time"
)

var pilotTasks = map[string][]string{
	"workflow":      {"confirm-nonproduction-identity", "trace-output-consumer", "identify-override", "detect-stale-output", "detect-wrong-host", "explain-stop-condition", "resume-interrupted-review", "verify-run-outcome"},
	"accessibility": {"identify-risk", "log-bookmark", "monitor-pause", "permission-scope", "context-mismatch", "identity-bookmark", "resume-task", "audit-output"},
	"integration":   {"docker-context", "iac-backend", "ansible-inventory", "spacelift-run", "network-endpoint"},
}

type pilotObservation struct {
	Task     string        `json:"task"`
	Outcome  string        `json:"outcome"`
	Notes    string        `json:"notes"`
	At       string        `json:"at"`
	Evidence boundArtifact `json:"evidence"`
}
type pilotState struct {
	SchemaVersion string             `json:"schema_version"`
	Suite         string             `json:"suite"`
	Operator      string             `json:"operator"`
	Setup         string             `json:"setup"`
	CreatedAt     string             `json:"created_at"`
	Observations  []pilotObservation `json:"observations"`
}

func runPilot(args []string, stdout, stderr io.Writer) error {
	mode := "show"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("pilot "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("state", ".rcdo-pilot.json", "operator acceptance session")
	suite := fs.String("suite", "accessibility", "accessibility, integration, or workflow")
	operator := fs.String("operator", "", "actual operator name for start")
	setup := fs.String("setup", "", "actual OS, terminal, assistive technology/CLI versions and nonproduction scope")
	task := fs.String("task", "", "task ID to record")
	outcome := fs.String("outcome", "", "pass, fail or blocked")
	notes := fs.String("notes", "", "actual observation, obstacles and assistance needed")
	evidence := fs.String("evidence", "", "local evidence artifact for recorded observation")
	format := fs.String("format", "text", "text or json")
	width := fs.Int("width", 72, "minimum 40")
	setAccessibleUsage(fs, "pilot "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(mode, "start", "record", "show") || !oneOf(*format, "text", "json") || *width < 40 {
		return fmt.Errorf("invalid pilot options")
	}
	var state pilotState
	var previous []byte
	if mode == "start" {
		if pilotTasks[*suite] == nil || !operationLabel(*operator) || !operationLabel(*setup) {
			return fmt.Errorf("start requires a supported suite, --operator and actual --setup")
		}
		state = pilotState{SchemaVersion: "1", Suite: *suite, Operator: auditText(*operator), Setup: auditText(*setup), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Observations: []pilotObservation{}}
		if err := saveWorkState(*path, state, nil, nil); err != nil {
			return err
		}
	} else {
		var err error
		previous, err = readConfigSource(*path)
		if err != nil {
			return err
		}
		if strictJSON(previous, &state) != nil || state.SchemaVersion != "1" || pilotTasks[state.Suite] == nil || !operationLabel(state.Operator) || !operationLabel(state.Setup) || len(state.Observations) > 1000 {
			return fmt.Errorf("invalid pilot session")
		}
	}
	if mode == "record" {
		if !oneOf(*task, pilotTasks[state.Suite]...) || !oneOf(*outcome, "pass", "fail", "blocked") || !operationLabel(*notes) || *evidence == "" || len(state.Observations) >= 1000 {
			return fmt.Errorf("record requires a suite task, outcome, observation notes and evidence file")
		}
		source, _, err := captureArtifact(*evidence)
		if err != nil {
			return err
		}
		if err := separateArtifact(*path, source); err != nil {
			return err
		}
		state.Observations = append(state.Observations, pilotObservation{Task: *task, Outcome: *outcome, Notes: auditText(*notes), At: time.Now().UTC().Format(time.RFC3339Nano), Evidence: source})
		if err := saveWorkState(*path, state, previous, func() error { _, err := readBoundArtifact(source); return err }); err != nil {
			return err
		}
	}
	latest := map[string]pilotObservation{}
	for _, v := range state.Observations {
		if !oneOf(v.Task, pilotTasks[state.Suite]...) || !oneOf(v.Outcome, "pass", "fail", "blocked") {
			return fmt.Errorf("invalid pilot observation")
		}
		latest[v.Task] = v
	}
	type row struct {
		Task        string            `json:"task"`
		Status      string            `json:"status"`
		Observation *pilotObservation `json:"observation,omitempty"`
	}
	result := struct {
		Suite    string `json:"suite"`
		Operator string `json:"operator"`
		Setup    string `json:"setup"`
		Basis    string `json:"basis"`
		Tasks    []row  `json:"tasks"`
	}{Suite: state.Suite, Operator: state.Operator, Setup: state.Setup, Basis: "Operator-reported observations with bound evidence; not automated accessibility certification", Tasks: []row{}}
	pending, failed := false, false
	for _, task := range pilotTasks[state.Suite] {
		r := row{Task: task, Status: "pending"}
		if v, ok := latest[task]; ok {
			r.Observation = &v
			r.Status = v.Outcome
			if _, err := readBoundArtifact(v.Evidence); err != nil {
				r.Status = "evidence_stale"
			}
			if r.Status == "fail" {
				failed = true
			}
			if r.Status != "pass" && r.Status != "fail" {
				pending = true
			}
		} else {
			pending = true
		}
		result.Tasks = append(result.Tasks, r)
	}
	if *format == "json" {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return err
		}
	} else {
		writeWrapped(stdout, result.Basis, *width)
		writeWrapped(stdout, "Operator: "+auditText(state.Operator)+". Setup: "+auditText(state.Setup), *width)
		for _, r := range result.Tasks {
			writeWrapped(stdout, r.Task+": "+r.Status, *width)
			if r.Observation != nil {
				writeWrapped(stdout, auditText(r.Observation.Notes), *width)
			}
		}
	}
	if pending {
		return reportError{status: finding.StatusIncomplete}
	}
	if failed {
		return reportError{status: finding.StatusBlocked}
	}
	return nil
}
