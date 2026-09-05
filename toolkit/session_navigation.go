package toolkit

import (
	"bytes"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
)

type sessionNote struct {
	FindingID string `json:"finding_id"`
	Text      string `json:"text"`
	At        string `json:"at"`
}

// Session output is linear, wrapped, and strips control characters from labels
// originating in files. Notes are operator statements, not collected findings.
func sessionText(w io.Writer, text string, width int) {
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		safe := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, line)
		writeWrapped(w, redactLine(safe), width)
	}
}
func runSessionNavigation(mode string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("session", ".rcdo/review-session.json", "review session file")
	width := fs.Int("width", 72, "maximum text width; minimum 40")
	var name, note, id string
	if mode == "bookmark" || mode == "goto" {
		fs.StringVar(&name, "name", "", "bookmark name")
	}
	if mode == "note" || mode == "action" {
		fs.StringVar(&note, "note", "", "operator statement; avoid secrets")
	}
	if mode == "note" {
		fs.StringVar(&id, "id", "", "finding ID; default current finding")
	}
	setAccessibleUsage(fs, mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || *width < 40 {
		return fmt.Errorf("invalid arguments or --width below 40")
	}
	session, err := readReviewSession(*path)
	if err != nil {
		return err
	}
	ordered := orderedSessionFindings(session.Report.Findings)
	ids := map[string]bool{}
	for _, f := range ordered {
		ids[f.ID] = true
	}
	if session.Cursor != "" && !ids[session.Cursor] {
		return fmt.Errorf("session cursor refers to a missing finding")
	}
	for key, value := range session.Bookmarks {
		if !operationLabel(key) || !ids[value] {
			return fmt.Errorf("invalid stored bookmark")
		}
	}
	for _, n := range session.Notes {
		if !ids[n.FindingID] || !operationLabel(n.Text) {
			return fmt.Errorf("invalid stored note")
		}
		if _, err := time.Parse(time.RFC3339, n.At); err != nil {
			return fmt.Errorf("invalid note time")
		}
	}
	stale := sessionFresh(session)
	var out bytes.Buffer
	if stale != nil {
		fmt.Fprintf(&out, "REVIEW RESULT: INCOMPLETE\nHISTORICAL EVIDENCE: %s\n", stale)
	}
	var statusText bytes.Buffer
	renderSessionStatus(&statusText, session)
	if stale != nil {
		out.WriteString(strings.Replace(statusText.String(), "REVIEW RESULT:", "STORED REVIEW RESULT:", 1))
	} else {
		out.Write(statusText.Bytes())
	}
	if session.Owner != "" {
		fmt.Fprintf(&out, "Operator-supplied owner: %s\n", session.Owner)
	}
	if session.Impact != "" {
		fmt.Fprintf(&out, "Operator-supplied impact: %s\n", session.Impact)
	}
	if session.NextAction != "" {
		fmt.Fprintf(&out, "Operator-supplied next action: %s\n", session.NextAction)
	}
	if mode == "handoff" {
		fmt.Fprintln(&out, "HANDOFF\nNo deployment or remediation execution is established by this record.")
		for _, check := range session.Report.CompletedChecks {
			fmt.Fprintf(&out, "Recorded check: %s\n", check)
		}
		for _, item := range ordered {
			fmt.Fprintf(&out, "Unresolved finding: %s; %s; %s\n", item.ID, item.Severity, item.Title)
			if _, ok := session.Acknowledged[item.ID]; ok {
				fmt.Fprintln(&out, "Reading status: acknowledged; not resolved")
			}
			fmt.Fprintf(&out, "Target: %s\nReason: %s\nRecommended check: %s\n", item.Resource, item.Reason, item.Remediation)
			for _, evidence := range item.Evidence {
				fmt.Fprintf(&out, "Recorded evidence: %s\n", evidence)
			}
		}
		for _, n := range session.Notes {
			fmt.Fprintf(&out, "Operator note for %s at %s: %s\n", n.FindingID, n.At, n.Text)
		}
		fmt.Fprintf(&out, "Source report: %s\nSource SHA-256: %s\n", session.ReportInput.Path, session.ReportInput.SHA256)
		for _, artifact := range session.Artifacts {
			fmt.Fprintf(&out, "Bound artifact: %s\nSHA-256: %s\n", artifact.Path, artifact.SHA256)
		}
		sessionText(stdout, out.String(), *width)
		if stale != nil {
			return reportError{status: finding.StatusIncomplete}
		}
		return reportError{status: session.Report.Status()}
	}
	mutating := oneOf(mode, "bookmark", "note", "action")
	if stale != nil && mutating {
		sessionText(stdout, out.String(), *width)
		return reportError{status: finding.StatusIncomplete}
	}
	index := 0
	for i, item := range ordered {
		if item.ID == session.Cursor {
			index = i
			break
		}
	}
	if mode == "action" {
		if !operationLabel(note) {
			return fmt.Errorf("--note requires a nonblank single-line statement of at most 2048 bytes")
		}
		session.NextAction = redactLine(note)
	} else if len(ordered) == 0 {
		fmt.Fprintln(&out, "No findings to navigate. Check coverage remains authoritative.")
		sessionText(stdout, out.String(), *width)
		if stale != nil {
			return reportError{status: finding.StatusIncomplete}
		}
		return reportError{status: session.Report.Status()}
	} else {
		switch mode {
		case "back":
			if index > 0 {
				index--
			}
		case "forward":
			if index < len(ordered)-1 {
				index++
			}
		case "bookmark":
			if !operationLabel(name) {
				return fmt.Errorf("--name is required and must be a single-line label")
			}
			if session.Bookmarks == nil {
				session.Bookmarks = map[string]string{}
			}
			if _, exists := session.Bookmarks[name]; exists {
				return fmt.Errorf("bookmark already exists; choose a new name")
			}
			session.Bookmarks[name] = ordered[index].ID
		case "goto":
			target, ok := session.Bookmarks[name]
			if !ok {
				return fmt.Errorf("bookmark does not exist")
			}
			for i, item := range ordered {
				if item.ID == target {
					index = i
					break
				}
			}
		case "note":
			if id == "" {
				id = ordered[index].ID
			}
			if !ids[id] || !operationLabel(note) {
				return fmt.Errorf("note needs a valid finding and a nonblank single-line --note")
			}
			if len(session.Notes) >= 1000 {
				return fmt.Errorf("session note limit reached")
			}
			session.Notes = append(session.Notes, sessionNote{FindingID: id, Text: redactLine(note), At: time.Now().UTC().Format(time.RFC3339)})
		case "resume", "repeat":
		default:
			return fmt.Errorf("unsupported navigation operation")
		}
		session.Cursor = ordered[index].ID
		fmt.Fprintf(&out, "Reading position: %d of %d\n", index+1, len(ordered))
		renderSessionFinding(&out, ordered[index])
		for _, n := range session.Notes {
			if n.FindingID == session.Cursor {
				fmt.Fprintf(&out, "Operator note at %s: %s\n", n.At, n.Text)
			}
		}
		keys := make([]string, 0, len(session.Bookmarks))
		for key := range session.Bookmarks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if session.Bookmarks[key] == session.Cursor {
				fmt.Fprintf(&out, "Bookmark: %s\n", key)
			}
		}
	}
	if stale == nil {
		if err := writeReviewSession(*path, session); err != nil {
			return err
		}
	}
	if mode == "action" {
		fmt.Fprintf(&out, "Next action recorded: %s\n", session.NextAction)
	}
	sessionText(stdout, out.String(), *width)
	if stale != nil {
		return reportError{status: finding.StatusIncomplete}
	}
	if mutating {
		return nil
	}
	return reportError{status: session.Report.Status()}
}
