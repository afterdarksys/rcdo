package toolkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
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

type reviewAcknowledgement struct {
	Note string `json:"note"`
	At   string `json:"at"`
}
type reviewInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type reviewSession struct {
	SchemaVersion string                           `json:"schema_version"`
	Cursor        string                           `json:"cursor,omitempty"`
	Bookmarks     map[string]string                `json:"bookmarks,omitempty"`
	Notes         []sessionNote                    `json:"notes,omitempty"`
	Owner         string                           `json:"owner,omitempty"`
	Impact        string                           `json:"impact,omitempty"`
	NextAction    string                           `json:"next_action,omitempty"`
	ChangeID      string                           `json:"change_id"`
	Commit        string                           `json:"commit"`
	Repository    string                           `json:"repository,omitempty"`
	CreatedAt     string                           `json:"created_at"`
	ExpiresAt     string                           `json:"expires_at"`
	Report        finding.Report                   `json:"report"`
	ReportInput   reviewInput                      `json:"report_input"`
	Artifacts     []reviewInput                    `json:"artifacts"`
	Acknowledged  map[string]reviewAcknowledgement `json:"acknowledged"`
}

type sessionPaths []string

func (v *sessionPaths) String() string { return strings.Join(*v, ", ") }
func (v *sessionPaths) Set(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("artifact path must not be empty")
	}
	*v = append(*v, s)
	return nil
}

func runReviewSession(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(stdout, "review-session: resumable finding review\nUsage: review-session COMMAND [options]\nCommands:\n  start\n  status\n  next\n  acknowledge (alias: ack)\n  resume, repeat, back, forward\n  bookmark, goto, note, action\nAcknowledgement records reading; it does not resolve findings or authorize deployment.")
		return nil
	}
	mode, args := args[0], args[1:]
	if oneOf(mode, "resume", "repeat", "back", "forward", "bookmark", "goto", "note", "action") {
		return runSessionNavigation(mode, args, stdout, stderr)
	}
	var sessionPath, reportPath, changeID, commit, repo, findingID, note string
	var artifacts sessionPaths
	var owner, impact, nextAction string
	var width int
	var maxAge time.Duration
	fs := flag.NewFlagSet("review-session "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&sessionPath, "session", ".rcdo/review-session.json", "review session file")
	fs.IntVar(&width, "width", finding.DefaultTextWidth, "maximum text line width; minimum 40")
	if mode == "start" {
		fs.StringVar(&owner, "owner", "", "owner label for handoffs")
		fs.StringVar(&impact, "impact", "", "operator-supplied impact statement")
		fs.StringVar(&nextAction, "next-action", "", "operator-supplied next action")
		fs.DurationVar(&maxAge, "max-age", 24*time.Hour, "session lifetime; positive duration")
		fs.StringVar(&reportPath, "report", "", "versioned finding report JSON")
		fs.StringVar(&changeID, "change-id", "", "ticket or change identifier")
		fs.StringVar(&commit, "commit", "", "reviewed commit; resolved to HEAD when --repo is used")
		fs.StringVar(&repo, "repo", "", "optional clean Git repository; rechecked on resume")
		fs.Var(&artifacts, "artifact", "additional input to fingerprint, such as a plan or policy; repeatable")
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
	if width < finding.MinTextWidth {
		return fmt.Errorf("--width must be at least %d", finding.MinTextWidth)
	}
	// Buffer every session message through the existing accessible line wrapper.
	var output bytes.Buffer
	defer func() {
		for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
			if line != "" {
				writeWrapped(stdout, line, width)
			}
		}
	}()
	switch mode {
	case "start":
		if maxAge <= 0 {
			return fmt.Errorf("--max-age must be positive")
		}
		if strings.TrimSpace(reportPath) == "" || strings.TrimSpace(changeID) == "" || strings.TrimSpace(commit) == "" {
			return fmt.Errorf("start requires --report, --change-id, and --commit")
		}
		if _, err := os.Lstat(sessionPath); err == nil {
			return fmt.Errorf("session already exists; choose a new --session path to preserve review history")
		} else if !os.IsNotExist(err) {
			return err
		}
		input, data, err := captureReviewReport(reportPath)
		if err != nil {
			return err
		}
		report, err := decodeSessionReport(data)
		if err != nil {
			return err
		}
		session := reviewSession{SchemaVersion: "2", ChangeID: changeID, Commit: commit, CreatedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().UTC().Add(maxAge).Format(time.RFC3339Nano), Owner: owner, Impact: impact, NextAction: nextAction, Report: report, ReportInput: input, Artifacts: []reviewInput{}, Acknowledged: map[string]reviewAcknowledgement{}}
		if repo != "" {
			repo, err = filepath.Abs(repo)
			if err != nil {
				return err
			}
			head, err := cleanReviewHead(repo)
			if err != nil {
				return err
			}
			selected := executeReadOnly("git", "-C", repo, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
			if selected.err != nil || strings.TrimSpace(string(selected.stdout)) != head {
				return fmt.Errorf("--commit must resolve to the current repository HEAD")
			}
			session.Repository = repo
			session.Commit = head
			// Keep session output outside the bound repository so it cannot dirty that repository.
			absoluteSession, err := filepath.Abs(sessionPath)
			if err != nil {
				return err
			}
			root := executeReadOnly("git", "-C", repo, "rev-parse", "--show-toplevel")
			if root.err != nil {
				return fmt.Errorf("cannot determine repository root")
			}
			relative, err := filepath.Rel(strings.TrimSpace(string(root.stdout)), absoluteSession)
			if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
				return fmt.Errorf("with --repo, store --session outside the repository")
			}
		}
		for _, path := range artifacts {
			item, err := captureReviewInput(path)
			if err != nil {
				return err
			}
			session.Artifacts = append(session.Artifacts, item)
		}
		if err := writeReviewSession(sessionPath, session); err != nil {
			return err
		}
		fmt.Fprintln(&output, "REVIEW SESSION STARTED")
		renderSessionStatus(&output, session)
		return nil
	case "status", "next", "acknowledge", "ack":
		session, err := readReviewSession(sessionPath)
		if err != nil {
			return err
		}
		if err := sessionFresh(session); err != nil {
			fmt.Fprintf(&output, "REVIEW RESULT: INCOMPLETE\nSESSION STALE: %s\nNext action: regenerate the report and start a new session.\n", err)
			return reportError{status: finding.StatusIncomplete}
		}
		renderSessionStatus(&output, session)
		if mode == "acknowledge" || mode == "ack" {
			if strings.TrimSpace(findingID) == "" || strings.TrimSpace(note) == "" {
				return fmt.Errorf("acknowledge requires --id and a nonblank --note")
			}
			found := false
			for _, item := range session.Report.Findings {
				if item.ID == findingID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("finding %q is not in this session", findingID)
			}
			if _, exists := session.Acknowledged[findingID]; exists {
				return fmt.Errorf("finding is already acknowledged; existing review history is preserved")
			}
			session.Acknowledged[findingID] = reviewAcknowledgement{Note: note, At: time.Now().UTC().Format(time.RFC3339)}
			if err := writeReviewSession(sessionPath, session); err != nil {
				return err
			}
			fmt.Fprintf(&output, "ACKNOWLEDGED: %s\nNote: %s\n", findingID, note)
			return nil
		}
		if mode == "next" {
			for _, item := range orderedSessionFindings(session.Report.Findings) {
				if _, ok := session.Acknowledged[item.ID]; !ok {
					session.Cursor = item.ID
					if err := writeReviewSession(sessionPath, session); err != nil {
						return err
					}
					renderSessionFinding(&output, item)
					return reportError{status: session.Report.Status()}
				}
			}
			fmt.Fprintln(&output, "READING COMPLETE\nUnacknowledged findings: 0")
		}
		return reportError{status: session.Report.Status()}
	default:
		return fmt.Errorf("unknown review-session operation %q; expected start, status, next, or acknowledge", mode)
	}
}

func decodeSessionReport(data []byte) (finding.Report, error) {
	var envelope struct {
		SchemaVersion string            `json:"schema_version"`
		Status        finding.Status    `json:"status"`
		Findings      []finding.Finding `json:"findings"`
		Completed     []string          `json:"completed_checks"`
		Incomplete    []string          `json:"incomplete_checks"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return finding.Report{}, fmt.Errorf("parse report: %w", err)
	}
	report := finding.Report{Findings: envelope.Findings, CompletedChecks: envelope.Completed, IncompleteChecks: envelope.Incomplete}
	if envelope.SchemaVersion != finding.SchemaVersion || envelope.Findings == nil || envelope.Completed == nil || envelope.Incomplete == nil {
		return report, fmt.Errorf("session requires a versioned report with findings, completed_checks, and incomplete_checks arrays")
	}
	if err := report.Validate(); err != nil {
		return report, err
	}
	if envelope.Status != report.Status() {
		return report, fmt.Errorf("report status does not match findings and coverage")
	}
	if len(report.CompletedChecks)+len(report.IncompleteChecks) == 0 {
		return report, fmt.Errorf("report must declare completed or incomplete check coverage")
	}
	return report, nil
}

func captureReviewReport(path string) (reviewInput, []byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return reviewInput{}, nil, err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return reviewInput{}, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return reviewInput{}, nil, err
	}
	if !info.Mode().IsRegular() {
		return reviewInput{}, nil, fmt.Errorf("report must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 32<<20+1))
	if err != nil {
		return reviewInput{}, nil, err
	}
	if len(data) > 32<<20 {
		return reviewInput{}, nil, fmt.Errorf("report exceeds 32 MiB")
	}
	return reviewInput{Path: absolute, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}, data, nil
}
func captureReviewInput(path string) (reviewInput, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return reviewInput{}, err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return reviewInput{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return reviewInput{}, err
	}
	if !info.Mode().IsRegular() {
		return reviewInput{}, fmt.Errorf("artifact must be a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return reviewInput{}, err
	}
	return reviewInput{Path: absolute, SHA256: fmt.Sprintf("%x", h.Sum(nil))}, nil
}
func cleanReviewHead(repo string) (string, error) {
	head := executeReadOnly("git", "-C", repo, "rev-parse", "--verify", "HEAD")
	if head.err != nil {
		return "", fmt.Errorf("cannot read repository HEAD")
	}
	state := executeReadOnly("git", "-C", repo, "status", "--porcelain=v1", "--untracked-files=all")
	if state.err != nil || len(bytes.TrimSpace(state.stdout)) != 0 {
		return "", fmt.Errorf("repository is dirty or cannot be inspected")
	}
	return strings.TrimSpace(string(head.stdout)), nil
}
func sessionFresh(session reviewSession) error {
	expires, err := time.Parse(time.RFC3339Nano, session.ExpiresAt)
	if err != nil || !time.Now().Before(expires) {
		return fmt.Errorf("session expired; collect fresh evidence")
	}
	input, data, err := captureReviewReport(session.ReportInput.Path)
	if err != nil || input.SHA256 != session.ReportInput.SHA256 {
		return fmt.Errorf("source report is missing or changed")
	}
	report, err := decodeSessionReport(data)
	if err != nil {
		return fmt.Errorf("source report is invalid")
	}
	expected, _ := json.Marshal(report)
	actual, _ := json.Marshal(session.Report)
	if !bytes.Equal(expected, actual) {
		return fmt.Errorf("stored report differs from its source")
	}
	for _, old := range session.Artifacts {
		current, err := captureReviewInput(old.Path)
		if err != nil || current.SHA256 != old.SHA256 {
			return fmt.Errorf("bound artifact is missing or changed: %s", old.Path)
		}
	}
	if session.Repository != "" {
		head, err := cleanReviewHead(session.Repository)
		if err != nil {
			return err
		}
		if head != session.Commit {
			return fmt.Errorf("repository HEAD changed")
		}
	}
	return nil
}
func readReviewSession(path string) (reviewSession, error) {
	var session reviewSession
	data, err := os.ReadFile(path)
	if err != nil {
		return session, fmt.Errorf("read session: %w", err)
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return session, fmt.Errorf("parse session: %w", err)
	}
	if session.SchemaVersion != "2" {
		return session, fmt.Errorf("session schema_version must be 2; recreate legacy sessions from a fresh versioned report")
	}
	if strings.TrimSpace(session.ChangeID) == "" || strings.TrimSpace(session.Commit) == "" || !filepath.IsAbs(session.ReportInput.Path) || len(session.ReportInput.SHA256) != 64 {
		return session, fmt.Errorf("invalid session identity or source report")
	}
	if _, err := time.Parse(time.RFC3339, session.CreatedAt); err != nil {
		return session, fmt.Errorf("invalid session creation time")
	}
	if err := session.Report.Validate(); err != nil {
		return session, err
	}
	ids := map[string]bool{}
	for _, item := range session.Report.Findings {
		ids[item.ID] = true
	}
	for id, ack := range session.Acknowledged {
		if !ids[id] || strings.TrimSpace(ack.Note) == "" {
			return session, fmt.Errorf("invalid acknowledgement %q", id)
		}
		if _, err := time.Parse(time.RFC3339, ack.At); err != nil {
			return session, fmt.Errorf("invalid acknowledgement time")
		}
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
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	return atomicReplace(path, data, io.Discard)
}
func renderSessionStatus(w io.Writer, s reviewSession) {
	fmt.Fprintf(w, "REVIEW RESULT: %s\nChange: %s\nCommit: %s\nTotal findings: %d\nAcknowledged: %d\nRemaining: %d\n", strings.ToUpper(string(s.Report.Status())), s.ChangeID, s.Commit, len(s.Report.Findings), len(s.Acknowledged), len(s.Report.Findings)-len(s.Acknowledged))
	if s.Repository == "" {
		fmt.Fprintln(w, "Repository binding: unavailable; commit is a supplied label.")
	} else {
		fmt.Fprintf(w, "Repository binding: %s\n", s.Repository)
	}
	summary := s.Report.Summary()
	fmt.Fprintf(w, "Blocking findings: %d\nCompleted checks: %d\n", summary.High+summary.Critical, len(s.Report.CompletedChecks))
	for _, check := range s.Report.IncompleteChecks {
		fmt.Fprintf(w, "Incomplete check: %s\n", check)
	}
	fmt.Fprintln(w, "Acknowledgement records reading only; this session does not authorize deployment.")
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
	fmt.Fprintf(w, "NEXT FINDING\nSeverity: %s\nID: %s\nTitle: %s\nResource: %s\nEnvironment: %s\nReason: %s\nNext action: %s\n", strings.ToUpper(string(item.Severity)), item.ID, item.Title, item.Resource, item.Environment, item.Reason, item.Remediation)
}
