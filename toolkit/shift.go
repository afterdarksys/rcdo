package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"git-tools/finding"
)

// shiftBrief is the spoken front door for one change window. It reads reports
// the operator already produced. It does not collect cloud data and it never
// treats a clean report as proof that a deployment is safe.

type shiftReport struct {
	path       string
	hash       string
	computed   finding.Status
	declared   finding.Status
	findings   []finding.Finding
	completed  []string
	incomplete []string
	lied       bool
}

type shiftBrief struct {
	status      finding.Status
	changeID    string
	identity    string
	blocker     string
	uncertainty string
	next        string
	reports     []shiftReport
}

var identityLabelPattern = regexp.MustCompile(`^identity label: cloud ([a-z0-9]+); (account|subscription|project) ([^;]+); principal (.+)$`)

func runShift(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("shift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var reports stringList
	layout := fs.String("layout", "plain", "plain, speech, or braille")
	format := fs.String("format", "text", "text, json, or ticket")
	width := fs.Int("width", 0, "line width; defaults 72 plain, 80 speech, 40 braille; minimum 40")
	changeID := fs.String("change-id", "", "ticket or change identifier, optional")
	fs.Var(&reports, "report", "finding report JSON file; repeat for each report")
	setAccessibleUsage(fs, "shift", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(*layout, "plain", "speech", "braille") || !oneOf(*format, "text", "json", "ticket") {
		return fmt.Errorf("invalid shift options")
	}
	if *changeID != "" && !operationLabel(*changeID) {
		return fmt.Errorf("--change-id must be a single-line label")
	}
	if len(reports) == 0 || len(reports) > 8 {
		return fmt.Errorf("shift requires 1 to 8 --report files")
	}
	if *width == 0 {
		*width = 72
		if *layout == "speech" {
			*width = 80
		}
		if *layout == "braille" {
			*width = 40
		}
	}
	if *width < 40 {
		return fmt.Errorf("width must be at least 40")
	}
	loaded := make([]shiftReport, 0, len(reports))
	for _, path := range reports {
		if path == "-" || strings.HasPrefix(path, "-") {
			return fmt.Errorf("shift reads report files so the next command can name them")
		}
		data, err := readConfigSource(path)
		if err != nil {
			return err
		}
		if len(data) > 16<<20 {
			return fmt.Errorf("report exceeds 16 MiB")
		}
		item, err := decodeShiftReport(path, data)
		if err != nil {
			return err
		}
		loaded = append(loaded, item)
	}
	brief := buildShiftBrief(*changeID, loaded)
	var err error
	if *format == "json" {
		err = writeShiftJSON(stdout, brief)
	} else {
		layoutName := *layout
		if *format == "ticket" {
			layoutName = "ticket"
		}
		err = writeShiftText(stdout, layoutName, *width, brief)
	}
	if err != nil {
		return err
	}
	if brief.status != finding.StatusClean {
		return reportError{status: brief.status}
	}
	return nil
}

func decodeShiftReport(path string, data []byte) (shiftReport, error) {
	var envelope struct {
		Provenance       *finding.Provenance `json:"provenance,omitempty"`
		SchemaVersion    string              `json:"schema_version"`
		Status           finding.Status      `json:"status"`
		Summary          finding.Summary     `json:"summary,omitempty"`
		Findings         []finding.Finding   `json:"findings"`
		CompletedChecks  []string            `json:"completed_checks"`
		IncompleteChecks []string            `json:"incomplete_checks"`
	}
	if strictJSON(data, &envelope) != nil || envelope.SchemaVersion != finding.SchemaVersion {
		return shiftReport{}, fmt.Errorf("invalid finding report %s", path)
	}
	report := finding.Report{Findings: envelope.Findings, CompletedChecks: envelope.CompletedChecks, IncompleteChecks: envelope.IncompleteChecks}
	if err := report.Validate(); err != nil {
		return shiftReport{}, fmt.Errorf("invalid finding report %s: %w", path, err)
	}
	item := shiftReport{
		path: path, hash: digestBytes(data), computed: report.Status(), declared: envelope.Status,
		findings: envelope.Findings, completed: envelope.CompletedChecks, incomplete: envelope.IncompleteChecks,
	}
	if envelope.Status != report.Status() {
		item.lied = true
		item.incomplete = append(item.incomplete, "report status does not match its findings")
		item.computed = finding.StatusIncomplete
	}
	return item, nil
}

func buildShiftBrief(changeID string, reports []shiftReport) shiftBrief {
	brief := shiftBrief{status: finding.StatusClean, changeID: changeID, reports: reports}
	identities := []string{}
	var top *finding.Finding
	var topPath string
	topRank := -1
	rank := map[finding.Severity]int{finding.SeverityInfo: 1, finding.SeverityWarning: 2, finding.SeverityHigh: 3, finding.SeverityCritical: 4}
	for _, report := range reports {
		brief.status = worseShiftStatus(brief.status, report.computed)
		for _, check := range report.completed {
			if identityLabelPattern.MatchString(check) {
				identities = append(identities, check)
			}
		}
		for i := range report.findings {
			item := report.findings[i]
			if rank[item.Severity] > topRank {
				topRank = rank[item.Severity]
				copy := item
				top = &copy
				topPath = report.path
			}
		}
	}
	switch {
	case len(identities) == 0:
		brief.identity = "Identity: not in the supplied reports."
		brief.status = finding.StatusIncomplete
		brief.next = "rcdo context --input snapshot.json --expect work.yaml --format text"
	case len(uniqueStrings(identities)) > 1:
		brief.identity = "Identity: the supplied reports disagree."
		brief.status = worseShiftStatus(brief.status, finding.StatusBlocked)
		brief.next = "rcdo context --input snapshot.json --expect work.yaml --format text"
	default:
		brief.identity = "Identity: " + strings.TrimPrefix(identities[0], "identity label: ")
		if top != nil {
			brief.next = "rcdo report-read --layout speech --id " + shiftQuote(top.ID) + " --input " + shiftQuote(topPath)
		}
	}
	if top == nil {
		brief.blocker = "Blocker: none in the supplied reports."
	} else {
		brief.blocker = fmt.Sprintf("Blocker: %s. %s. Resource: %s. Next action: %s.", top.Severity, strings.TrimRight(top.Title, "."), top.Resource, strings.TrimRight(top.Remediation, "."))
		if brief.next == "" {
			brief.next = "rcdo report-read --layout speech --id " + shiftQuote(top.ID) + " --input " + shiftQuote(topPath)
		}
	}
	uncertainty := ""
	uncertainPath := ""
	for _, report := range reports {
		if uncertainty == "" && len(report.incomplete) > 0 {
			uncertainty = report.incomplete[0]
			uncertainPath = report.path
		}
	}
	if uncertainty == "" {
		brief.uncertainty = "Uncertainty: none recorded in the supplied reports."
	} else {
		brief.uncertainty = "Uncertainty: " + uncertainty
		if top == nil && !strings.HasPrefix(brief.identity, "Identity: the supplied") && !strings.HasPrefix(brief.identity, "Identity: not") {
			brief.next = "rcdo report-read --layout speech --input " + shiftQuote(uncertainPath)
		}
	}
	if brief.next == "" {
		brief.next = "No further rcdo command is required for these reports. Check the target independently before approval."
	}
	return brief
}

func worseShiftStatus(current, next finding.Status) finding.Status {
	rank := map[finding.Status]int{finding.StatusClean: 0, finding.StatusReview: 1, finding.StatusBlocked: 2, finding.StatusIncomplete: 3}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func shiftQuote(value string) string {
	if operationLabel(value) && !strings.ContainsAny(value, " '\\$`\"") {
		return value
	}
	return "<see the report; the identifier is not safe to paste>"
}

func writeShiftText(w io.Writer, layout string, width int, brief shiftBrief) error {
	var output bytes.Buffer
	line := func(label, value string) {
		text := redactLine(label + value)
		if layout == "braille" && value != "" {
			writeWrapped(&output, redactLine(strings.TrimSuffix(label, " ")), width)
			writeWrapped(&output, redactLine(value), width)
			return
		}
		writeWrapped(&output, text, width)
	}
	change := brief.changeID
	if change == "" {
		change = "not supplied"
	}
	switch layout {
	case "ticket":
		line("Change ticket paragraph", "")
		line("Change: ", change)
		line(brief.identity, "")
		line("Result: ", strings.ToUpper(string(brief.status)))
		line(brief.blocker, "")
		line(strings.Replace(brief.uncertainty, "Uncertainty: ", "Not proven: ", 1), "")
		for _, report := range brief.reports {
			line("Report: ", report.path+" SHA-256 "+report.hash)
		}
		line("This paragraph reads local reports. It is not approval to deploy.", "")
	default:
		line("Shift brief.", "")
		line("Change: ", change)
		line(brief.identity, "")
		line("Result: ", string(brief.status))
		line(brief.blocker, "")
		line(brief.uncertainty, "")
		line("Next command: ", brief.next)
		line("Deployment proven: no.", "")
	}
	_, err := io.Copy(w, &output)
	return err
}

func writeShiftJSON(w io.Writer, brief shiftBrief) error {
	type row struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Status string `json:"status"`
	}
	rows := make([]row, 0, len(brief.reports))
	for _, report := range brief.reports {
		rows = append(rows, row{Path: report.path, SHA256: report.hash, Status: string(report.computed)})
	}
	payload := struct {
		Schema           string `json:"schema"`
		Status           string `json:"status"`
		ChangeID         string `json:"change_id"`
		Identity         string `json:"identity"`
		Blocker          string `json:"blocker"`
		Uncertainty      string `json:"uncertainty"`
		NextCommand      string `json:"next_command"`
		DeploymentProven bool   `json:"deployment_proven"`
		Reports          []row  `json:"reports"`
	}{
		Schema: "rcdo/shift/v1", Status: string(brief.status), ChangeID: brief.changeID,
		Identity: redactLine(brief.identity), Blocker: redactLine(brief.blocker), Uncertainty: redactLine(brief.uncertainty),
		NextCommand: brief.next, DeploymentProven: false, Reports: rows,
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(payload)
}
