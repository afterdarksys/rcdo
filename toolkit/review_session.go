package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"git-tools/finding"
)

type reviewAcknowledgement struct {
	Note string `json:"note"`
	At   string `json:"at"`
}
type reviewSession struct {
	SchemaVersion string                           `json:"schema_version"`
	ChangeID      string                           `json:"change_id"`
	Commit        string                           `json:"commit"`
	CreatedAt     string                           `json:"created_at"`
	Findings      []finding.Finding                `json:"findings"`
	Acknowledged  map[string]reviewAcknowledgement `json:"acknowledged"`
}

func runReviewSession(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(stdout, "review-session: resumable finding review")
		fmt.Fprintln(stdout, "Usage: review-session COMMAND [options]")
		fmt.Fprintln(stdout, "Commands:")
		fmt.Fprintln(stdout, "  start")
		fmt.Fprintln(stdout, "  status")
		fmt.Fprintln(stdout, "  next")
		fmt.Fprintln(stdout, "  acknowledge (alias: ack)")
		return nil
	}
	mode, args := args[0], args[1:]
	var sessionPath, reportPath, changeID, commit, findingID, note string
	fs := flag.NewFlagSet("review-session "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&sessionPath, "session", ".rcdo/review-session.json", "review session file")
	if mode == "start" {
		fs.StringVar(&reportPath, "report", "", "finding report JSON")
		fs.StringVar(&changeID, "change-id", "", "ticket or change identifier")
		fs.StringVar(&commit, "commit", "", "reviewed commit SHA")
	}
	if mode == "acknowledge" || mode == "ack" {
		fs.StringVar(&findingID, "id", "", "finding ID to acknowledge")
		fs.StringVar(&note, "note", "", "review note explaining the acknowledgement")
	}
	setAccessibleUsage(fs, "review-session "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	switch mode {
	case "start":
		if reportPath == "" || changeID == "" || commit == "" {
			return fmt.Errorf("start requires --report, --change-id, and --commit")
		}
		data, err := os.ReadFile(reportPath)
		if err != nil {
			return fmt.Errorf("read report: %w", err)
		}
		var envelope struct {
			Findings []finding.Finding `json:"findings"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("parse report: %w", err)
		}
		for i, item := range envelope.Findings {
			if err := item.Validate(); err != nil {
				return fmt.Errorf("finding %d: %w", i+1, err)
			}
		}
		session := reviewSession{SchemaVersion: "1", ChangeID: changeID, Commit: commit, CreatedAt: time.Now().UTC().Format(time.RFC3339), Findings: envelope.Findings, Acknowledged: map[string]reviewAcknowledgement{}}
		if err := writeReviewSession(sessionPath, session); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "REVIEW SESSION STARTED\nChange: %s\nCommit: %s\nFindings: %d\nSession: %s\n", changeID, commit, len(session.Findings), sessionPath)
		return nil
	case "status", "next", "acknowledge", "ack":
		session, err := readReviewSession(sessionPath)
		if err != nil {
			return err
		}
		if mode == "acknowledge" || mode == "ack" {
			if findingID == "" || note == "" {
				return fmt.Errorf("acknowledge requires --id and --note")
			}
			found := false
			for _, item := range session.Findings {
				if item.ID == findingID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("finding %q is not in this session", findingID)
			}
			session.Acknowledged[findingID] = reviewAcknowledgement{Note: note, At: time.Now().UTC().Format(time.RFC3339)}
			if err := writeReviewSession(sessionPath, session); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "ACKNOWLEDGED: %s\nNote: %s\n", findingID, note)
			return nil
		}
		if mode == "next" {
			for _, item := range orderedSessionFindings(session.Findings) {
				if _, ok := session.Acknowledged[item.ID]; !ok {
					renderSessionFinding(stdout, item)
					return nil
				}
			}
			fmt.Fprintln(stdout, "REVIEW SESSION COMPLETE\nUnacknowledged findings: 0")
			return nil
		}
		fmt.Fprintf(stdout, "REVIEW SESSION STATUS\nChange: %s\nCommit: %s\nTotal findings: %d\nAcknowledged: %d\nRemaining: %d\n", session.ChangeID, session.Commit, len(session.Findings), len(session.Acknowledged), len(session.Findings)-len(session.Acknowledged))
		return nil
	default:
		return fmt.Errorf("unknown review-session operation %q; expected start, status, next, or acknowledge", mode)
	}
}

func readReviewSession(path string) (reviewSession, error) {
	var session reviewSession
	data, err := os.ReadFile(path)
	if err != nil {
		return session, fmt.Errorf("read session %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return session, fmt.Errorf("parse session: %w", err)
	}
	if session.SchemaVersion != "1" {
		return session, fmt.Errorf("session schema_version must be 1")
	}
	if session.Acknowledged == nil {
		session.Acknowledged = map[string]reviewAcknowledgement{}
	}
	return session, nil
}

func writeReviewSession(path string, session reviewSession) error {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepathDir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
	return atomicReplace(path, data, io.Discard)
}

func filepathDir(path string) string {
	index := strings.LastIndexAny(path, `/\\`)
	if index < 0 {
		return "."
	}
	return path[:index]
}
func orderedSessionFindings(items []finding.Finding) []finding.Finding {
	result := append([]finding.Finding(nil), items...)
	sort.SliceStable(result, func(i, j int) bool { return sessionSeverity(result[i].Severity) > sessionSeverity(result[j].Severity) })
	return result
}
func sessionSeverity(value finding.Severity) int {
	switch value {
	case finding.SeverityCritical:
		return 4
	case finding.SeverityHigh:
		return 3
	case finding.SeverityWarning:
		return 2
	default:
		return 1
	}
}
func renderSessionFinding(w io.Writer, item finding.Finding) {
	fmt.Fprintf(w, "NEXT FINDING\nSeverity: %s\nID: %s\nTitle: %s\nResource: %s\nReason: %s\nNext action: %s\n", strings.ToUpper(string(item.Severity)), item.ID, item.Title, item.Resource, item.Reason, item.Remediation)
}
