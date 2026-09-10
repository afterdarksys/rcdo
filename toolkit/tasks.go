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
	"sort"
	"strings"
	"time"

	"git-tools/finding"
)

type taskEntry struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Identity string `json:"identity"`
	AddedAt  string `json:"added_at"`
}
type taskRegistry struct {
	SchemaVersion string               `json:"schema_version"`
	Entries       map[string]taskEntry `json:"entries"`
}
type taskView struct {
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Path         string         `json:"path"`
	Title        string         `json:"title"`
	Cursor       string         `json:"cursor"`
	NextAction   string         `json:"next_action"`
	Status       finding.Status `json:"status"`
	ReadingState string         `json:"reading_state"`
	Gaps         []string       `json:"incomplete_checks"`
	Details      string         `json:"details,omitempty"`
}

func taskMetadata(kind string, raw []byte) (taskView, string, error) {
	v := taskView{Kind: kind, Status: finding.StatusClean, Gaps: []string{}}
	identity := ""
	switch kind {
	case "incident":
		var s incidentState
		if strictJSON(raw, &s) != nil || s.SchemaVersion != "1" || !operationLabel(s.Title) || len(s.Events) == 0 || s.Cursor < 1 || s.Cursor > len(s.Events) {
			return v, "", fmt.Errorf("invalid incident workspace")
		}
		v.Title = s.Title
		v.Cursor = fmt.Sprintf("timeline entry %d of %d", s.Cursor, len(s.Events))
		v.NextAction = s.NextAction
		v.ReadingState = s.Events[s.Cursor-1].Text
		identity = taskIdentityJSON([]string{s.Title, s.Events[0].At, s.Events[0].Kind})
	case "review":
		var s reviewSession
		if strictJSON(raw, &s) != nil || s.SchemaVersion != "2" || !operationLabel(s.ChangeID) || !operationLabel(s.Commit) || s.Report.Validate() != nil || len(s.ReportInput.SHA256) != 64 || !filepath.IsAbs(s.ReportInput.Path) {
			return v, "", fmt.Errorf("invalid review session")
		}
		if _, err := time.Parse(time.RFC3339, s.CreatedAt); err != nil {
			return v, "", fmt.Errorf("invalid review creation time")
		}
		v.Title = s.ChangeID
		v.Cursor = s.Cursor
		v.NextAction = s.NextAction
		v.Status = s.Report.Status()
		v.ReadingState = fmt.Sprintf("%d findings; %d reading acknowledgements (not resolutions)", len(s.Report.Findings), len(s.Acknowledged))
		identity = taskIdentityJSON([]string{s.ChangeID, s.Commit, s.CreatedAt, s.ReportInput.Path, s.ReportInput.SHA256})
	case "runbook":
		var s runbookState
		if strictJSON(raw, &s) != nil || s.SchemaVersion != "1" || len(s.Source.SHA256) != 64 || !filepath.IsAbs(s.Source.Path) || s.Cursor < 0 || s.Cursor >= len(s.Progress) {
			return v, "", fmt.Errorf("invalid runbook progress")
		}
		v.Title = filepath.Base(s.Source.Path)
		v.Cursor = fmt.Sprintf("step %d of %d", s.Cursor+1, len(s.Progress))
		v.ReadingState = s.Progress[s.Cursor].Status
		identity = taskIdentityJSON(s.Source)
	case "state":
		var s stateWalkSession
		if strictJSON(raw, &s) != nil || s.SchemaVersion != "1" || len(s.Source.SHA256) != 64 || !filepath.IsAbs(s.Source.Path) || s.Cursor == "" || s.Bookmarks == nil {
			return v, "", fmt.Errorf("invalid state navigation")
		}
		v.Title = filepath.Base(s.Source.Path)
		v.Cursor = s.Cursor
		v.ReadingState = fmt.Sprintf("%d bookmarks", len(s.Bookmarks))
		v.NextAction = "Continue state navigation at the saved node"
		identity = taskIdentityJSON(s.Source)
	default:
		return v, "", fmt.Errorf("kind must be incident, review, runbook or state")
	}
	return v, digestBytes([]byte(kind + "\n" + identity)), nil
}
func taskIdentityJSON(value any) string { b, _ := json.Marshal(value); return string(b) }
func inspectTask(name string, entry taskEntry, width int, details bool) taskView {
	v := taskView{Name: name, Kind: entry.Kind, Path: entry.Path, Status: finding.StatusIncomplete, Gaps: []string{}}
	raw, err := readConfigSource(entry.Path)
	if err != nil {
		v.Gaps = append(v.Gaps, "Workflow file unavailable")
		return v
	}
	parsed, identity, err := taskMetadata(entry.Kind, raw)
	if err != nil {
		v.Gaps = append(v.Gaps, "Workflow file is invalid for its registered kind")
		return v
	}
	if identity != entry.Identity {
		v.Gaps = append(v.Gaps, "Workflow identity changed; register the replacement explicitly")
		return v
	}
	v = parsed
	v.Name = name
	v.Path = entry.Path
	output := &limitedCommandBuffer{limit: 128 << 10}
	var writer io.Writer = io.Discard
	if details {
		writer = output
	}
	switch entry.Kind {
	case "incident":
		err = runIncident([]string{"resume", "--state", entry.Path, "--width", fmt.Sprint(width)}, writer, writer)
	case "runbook":
		err = runRunbookWalk([]string{"show", "--state", entry.Path, "--width", fmt.Sprint(width)}, writer, writer)
		var s runbookState
		_ = strictJSON(raw, &s)
		if b, e := readBoundArtifact(s.Source); e == nil {
			var book executableRunbook
			if strictJSON(b, &book) == nil && s.Cursor < len(book.Steps) {
				v.Title = book.Title
				v.NextAction = book.Steps[s.Cursor].Instruction
			}
		}
	case "state":
		err = runStateWalk([]string{"show", "--state", entry.Path, "--width", fmt.Sprint(width)}, writer, writer)
	case "review":
		var s reviewSession
		_ = strictJSON(raw, &s)
		if e := sessionFresh(s); e != nil {
			v.Status = finding.StatusIncomplete
			v.Gaps = append(v.Gaps, "Review evidence is stale or unavailable")
		}
		ordered := orderedSessionFindings(s.Report.Findings)
		found := s.Cursor == ""
		for i, f := range ordered {
			if f.ID == s.Cursor || (s.Cursor == "" && i == 0) {
				found = true
				if v.Cursor == "" {
					v.Cursor = "First finding (no saved cursor): " + f.ID
				}
				if v.NextAction == "" {
					v.NextAction = f.Remediation
				}
				if details {
					renderSessionFinding(writer, f)
				}
			}
		}
		if !found {
			v.Status = finding.StatusIncomplete
			v.Gaps = append(v.Gaps, "Saved finding cursor is unavailable")
		}
		for _, gap := range s.Report.IncompleteChecks {
			v.Gaps = append(v.Gaps, gap)
		}
	}
	if err != nil {
		var reported reportError
		if errors.As(err, &reported) {
			v.Status = reported.status
			if reported.status == finding.StatusIncomplete {
				v.Gaps = append(v.Gaps, "Workflow reader reports incomplete evidence; resume details identify the gap")
			}
		} else {
			v.Status = finding.StatusIncomplete
			v.Gaps = append(v.Gaps, "Workflow reader rejected the saved state")
		}
	}
	after, e := readConfigSource(entry.Path)
	if e != nil || !bytes.Equal(after, raw) {
		v.Status = finding.StatusIncomplete
		v.Gaps = append(v.Gaps, "Workflow changed while being inspected; repeat the read")
	}
	if details {
		v.Details = safeReportText(output.String())
		if output.exceeded {
			v.Gaps = append(v.Gaps, "Detail output truncated at 128 KiB")
			v.Status = finding.StatusIncomplete
		}
	}
	return v
}
func runTasks(args []string, stdout, stderr io.Writer) error {
	mode := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	if !oneOf(mode, "list", "add", "remove", "resume") {
		return fmt.Errorf("tasks supports list, add, remove or resume")
	}
	fs := flag.NewFlagSet("tasks "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("registry", ".rcdo-tasks.json", "explicit task registry file")
	name := fs.String("name", "", "unique task name")
	kind := fs.String("kind", "", "incident, review, runbook or state")
	input := fs.String("input", "", "saved workflow file for add")
	width := fs.Int("width", 72, "minimum 40")
	format := fs.String("format", "text", "text or json")
	setAccessibleUsage(fs, "tasks "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *width < 40 || !oneOf(*format, "text", "json") || (mode != "list" && (!operationLabel(*name) || markdownSafe(*name) != *name)) {
		return fmt.Errorf("invalid task options")
	}
	registry := taskRegistry{SchemaVersion: "1", Entries: map[string]taskEntry{}}
	var previous []byte
	var err error
	if _, statErr := os.Lstat(*path); statErr == nil {
		data, readErr := readConfigSource(*path)
		if readErr != nil {
			return readErr
		}
		previous = data
		if strictJSON(data, &registry) != nil || registry.SchemaVersion != "1" || registry.Entries == nil || len(registry.Entries) > 100 {
			return fmt.Errorf("invalid task registry")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("task registry unavailable")
	}

	for n, e := range registry.Entries {
		if !operationLabel(n) || !filepath.IsAbs(e.Path) || len(e.Identity) != 64 || !oneOf(e.Kind, "incident", "review", "runbook", "state") {
			return fmt.Errorf("invalid registered entry")
		}
		if err := separateArtifact(*path, boundArtifact{Path: e.Path}); err != nil {
			return err
		}
	}
	var guard func() error
	switch mode {
	case "add":
		if *input == "" {
			return fmt.Errorf("add requires --input and --kind")
		}
		if _, exists := registry.Entries[*name]; exists || len(registry.Entries) >= 100 {
			return fmt.Errorf("task name exists or registry has 100 entries")
		}
		artifact, raw, e := captureArtifact(*input)
		if e != nil {
			return e
		}
		if e = separateArtifact(*path, artifact); e != nil {
			return e
		}
		_, identity, e := taskMetadata(*kind, raw)
		if e != nil {
			return e
		}
		registry.Entries[*name] = taskEntry{Kind: *kind, Path: artifact.Path, Identity: identity, AddedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		guard = func() error { _, e := readBoundArtifact(artifact); return e }
	case "remove":
		if _, ok := registry.Entries[*name]; !ok {
			return fmt.Errorf("task name not found")
		}
		delete(registry.Entries, *name)
	case "resume":
		if _, ok := registry.Entries[*name]; !ok {
			return fmt.Errorf("task name not found")
		}
	}
	if mode == "add" || mode == "remove" {
		if err = saveWorkState(*path, registry, previous, guard); err != nil {
			return err
		}
	}
	names := []string{}
	for n := range registry.Entries {
		if mode == "resume" && n != *name {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	views := []taskView{}
	status := finding.StatusClean
	for _, n := range names {
		v := inspectTask(n, registry.Entries[n], *width, mode == "resume")
		views = append(views, v)
		if v.Status == finding.StatusIncomplete {
			status = finding.StatusIncomplete
		} else if status != finding.StatusIncomplete && v.Status == finding.StatusBlocked {
			status = finding.StatusBlocked
		} else if status == finding.StatusClean && v.Status == finding.StatusReview {
			status = finding.StatusReview
		}
	}
	if *format == "json" {
		err = json.NewEncoder(stdout).Encode(struct {
			SchemaVersion string         `json:"schema_version"`
			Status        finding.Status `json:"status"`
			Tasks         []taskView     `json:"tasks"`
		}{"1", status, views})
	} else {
		if len(views) == 0 {
			fmt.Fprintln(stdout, "No registered tasks.")
		}
		for i, v := range views {
			for _, line := range []string{fmt.Sprintf("%d. %s (%s). Status: %s", i+1, v.Name, v.Kind, v.Status), "Title: " + v.Title, "Saved position: " + v.Cursor, "Reading state: " + v.ReadingState, "Next action: " + emptyValue(v.NextAction)} {
				writeWrapped(stdout, safeReportText(line), *width)
			}
			for _, gap := range v.Gaps {
				writeWrapped(stdout, "Incomplete: "+safeReportText(gap), *width)
			}
			if v.Details != "" {
				fmt.Fprintln(stdout, v.Details)
			}
		}
		writeWrapped(stdout, "Task switching does not advance, acknowledge or execute a workflow. Remove deletes only its registry entry.", *width)
	}
	if err != nil {
		return err
	}
	if status != finding.StatusClean {
		return reportError{status: status}
	}
	return nil
}
