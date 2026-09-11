package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"path/filepath"
	"time"
)

type incidentEvent struct {
	Number   int            `json:"number"`
	At       string         `json:"at"`
	Kind     string         `json:"kind"`
	Text     string         `json:"text"`
	Evidence *boundArtifact `json:"evidence,omitempty"`
}
type incidentHypothesis struct {
	ID     int    `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"`
}
type incidentState struct {
	SchemaVersion string                   `json:"schema_version"`
	Title         string                   `json:"title"`
	NextAction    string                   `json:"next_action"`
	Cursor        int                      `json:"cursor"`
	Hypotheses    []incidentHypothesis     `json:"hypotheses"`
	Events        []incidentEvent          `json:"events"`
	Evidence      map[string]boundArtifact `json:"evidence"`
}

func runIncident(args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && oneOf(args[0], "--help", "-help", "-h") {
		fmt.Fprintln(stderr, "Incident commands: start, show, resume, note, hypothesis, resolve, evidence.\nMore commands: action, next-action, next, previous, handoff.")
		args = append([]string{"show"}, args...)
	}
	if len(args) == 0 {
		return fmt.Errorf("incident requires start, show, resume, note, hypothesis, resolve, evidence, action, next-action, next, previous or handoff")
	}
	mode, args := args[0], args[1:]
	if !oneOf(mode, "start", "show", "resume", "note", "hypothesis", "resolve", "evidence", "action", "next-action", "next", "previous", "handoff") {
		return fmt.Errorf("unknown incident command")
	}
	fs := flag.NewFlagSet("incident "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state", ".rcdo-incident.json", "incident workspace")
	title := fs.String("title", "", "incident title for start")
	message := fs.String("text", "", "operator statement or next action")
	status := fs.String("status", "", "hypothesis: supported/rejected/open; action: attempted/completed")
	id := fs.Int("id", 0, "hypothesis ID")
	input := fs.String("input", "", "evidence file")
	name := fs.String("name", "", "evidence label")
	limit := fs.Int("limit", 10, "maximum recent timeline entries, 1 to 1000")
	width := fs.Int("width", 72, "text width, minimum 40")
	format := fs.String("format", "text", "text or json")
	setAccessibleUsage(fs, "incident "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *width < 40 || !oneOf(*format, "text", "json") || *limit < 1 || *limit > 1000 {
		return fmt.Errorf("invalid incident options")
	}
	var s incidentState
	var previous []byte
	var err error
	if mode == "start" {
		if !operationLabel(*title) {
			return fmt.Errorf("start requires a single-line title")
		}
		s = incidentState{SchemaVersion: "1", Title: redactAIText(*title), Hypotheses: []incidentHypothesis{}, Events: []incidentEvent{}, Evidence: map[string]boundArtifact{}}
	} else {
		previous, err = readConfigSource(*statePath)
		if err != nil {
			return err
		}
		if strictJSON(previous, &s) != nil || s.SchemaVersion != "1" || !operationLabel(s.Title) || len(s.Events) > 10000 || s.Cursor < 1 || len(s.Events) == 0 || s.Cursor > len(s.Events) {
			return fmt.Errorf("invalid incident workspace")
		}
		if s.Evidence == nil {
			s.Evidence = map[string]boundArtifact{}
		}
		for i, e := range s.Events {
			if e.Number != i+1 || !operationLabel(e.Text) || !operationLabel(e.Kind) {
				return fmt.Errorf("invalid incident timeline")
			}
			if _, err := time.Parse(time.RFC3339Nano, e.At); err != nil {
				return fmt.Errorf("invalid timeline time")
			}
		}
		for i, h := range s.Hypotheses {
			if h.ID != i+1 || !operationLabel(h.Text) || !oneOf(h.Status, "open", "supported", "rejected") {
				return fmt.Errorf("invalid stored hypothesis")
			}
		}
	}
	mutating := !oneOf(mode, "show", "resume", "handoff")
	add := func(kind, value string, a *boundArtifact) {
		s.Events = append(s.Events, incidentEvent{Number: len(s.Events) + 1, At: time.Now().UTC().Format(time.RFC3339Nano), Kind: kind, Text: markdownSafe(redactAIText(value)), Evidence: a})
		s.Cursor = len(s.Events)
	}
	switch mode {
	case "start":
		add("started", "Incident workspace started: "+s.Title, nil)
	case "note", "hypothesis", "next-action", "action":
		if !operationLabel(*message) {
			return fmt.Errorf("--text must be a nonempty single-line operator statement")
		}
		if mode == "action" && !oneOf(*status, "attempted", "completed") {
			return fmt.Errorf("action requires --status attempted or completed; this is an operator record, not execution proof")
		}
		value := redactAIText(*message)
		if mode == "hypothesis" {
			s.Hypotheses = append(s.Hypotheses, incidentHypothesis{len(s.Hypotheses) + 1, value, "open"})
		}
		if mode == "next-action" {
			s.NextAction = value
		}
		kind := mode
		if mode == "action" {
			kind = "operator-action-" + *status
		}
		add(kind, value, nil)
	case "resolve":
		if *id < 1 || *id > len(s.Hypotheses) || !oneOf(*status, "open", "supported", "rejected") {
			return fmt.Errorf("resolve requires an existing --id and --status open, supported or rejected")
		}
		s.Hypotheses[*id-1].Status = *status
		add("operator-hypothesis-status", fmt.Sprintf("Hypothesis %d: %s (operator assessment)", *id, *status), nil)
	case "evidence":
		if *input == "" || !operationLabel(*name) {
			return fmt.Errorf("evidence requires --input and single-line --name")
		}
		a, _, err := captureArtifact(*input)
		if err != nil {
			return err
		}
		if err = separateArtifact(*statePath, a); err != nil {
			return err
		}
		s.Evidence[*name] = a
		add("evidence", "Evidence version attached: "+*name, &a)
	case "next":
		if s.Cursor < len(s.Events) {
			s.Cursor++
		}
	case "previous":
		if s.Cursor > 1 {
			s.Cursor--
		}
	}
	if len(s.Events) > 10000 {
		return fmt.Errorf("incident timeline limit reached")
	}
	stale := []string{}
	for _, label := range sortedArtifactLabels(s.Evidence) {
		a := s.Evidence[label]
		if err := separateArtifact(*statePath, a); err != nil {
			return err
		}
		if _, err := readBoundArtifact(a); err != nil {
			stale = append(stale, label)
		}
	}
	if mutating {
		if err = saveWorkState(*statePath, s, previous, nil); err != nil {
			return err
		}
	}
	if *format == "json" {
		err = json.NewEncoder(stdout).Encode(struct {
			incidentState
			Stale           []string `json:"stale_evidence"`
			OperatorRecords bool     `json:"operator_records"`
		}{s, stale, true})
	} else {
		writeWrapped(stdout, "Incident: "+markdownSafe(s.Title), *width)
		writeWrapped(stdout, "Next action: "+markdownSafe(emptyValue(s.NextAction)), *width)
		writeWrapped(stdout, "Timeline entries and hypothesis assessments are operator records, not independently verified outcomes.", *width)
		for _, h := range s.Hypotheses {
			writeWrapped(stdout, fmt.Sprintf("Hypothesis %d. %s. %s", h.ID, h.Status, markdownSafe(h.Text)), *width)
		}
		for _, label := range sortedArtifactLabels(s.Evidence) {
			a := s.Evidence[label]
			writeWrapped(stdout, "Evidence: "+markdownSafe(label)+"; file: "+markdownSafe(filepath.Base(a.Path))+"; SHA-256: "+a.SHA256, *width)
		}
		for _, label := range stale {
			writeWrapped(stdout, "Stale or unavailable evidence: "+markdownSafe(label), *width)
		}
		events := s.Events
		if oneOf(mode, "next", "previous") {
			events = s.Events[s.Cursor-1 : s.Cursor]
		} else if len(events) > *limit {
			writeWrapped(stdout, fmt.Sprintf("Earlier timeline entries omitted: %d; use --limit or --format json.", len(events)-*limit), *width)
			events = events[len(events)-*limit:]
		}
		for _, e := range events {
			writeWrapped(stdout, fmt.Sprintf("Entry %d. %s. %s. %s", e.Number, e.At, e.Kind, markdownSafe(e.Text)), *width)
		}
	}
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
func sortedArtifactLabels(m map[string]boundArtifact) []string {
	x := map[string]any{}
	for k := range m {
		x[k] = true
	}
	return sortedKeys(x)
}
