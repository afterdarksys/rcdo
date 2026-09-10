package toolkit

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func readAuditRecords(path string) ([]byte, []auditRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("audit source must be a regular file")
	}
	var reader io.Reader = f
	head := make([]byte, 2)
	n, _ := f.ReadAt(head, 0)
	if n == 2 && bytes.Equal(head, []byte{31, 139}) {
		gz, e := gzip.NewReader(f)
		if e != nil {
			return nil, nil, e
		}
		defer gz.Close()
		reader = gz
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (64<<20)+1))
	if err != nil || len(raw) > 64<<20 {
		return nil, nil, fmt.Errorf("audit input invalid or exceeds 64 MiB")
	}
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		return nil, nil, fmt.Errorf("incomplete final audit record")
	}
	records := []auditRecord{}
	starts := map[string]auditRecord{}
	finishes := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var r auditRecord
		if len(line) > 4<<20 || strictJSON(line, &r) != nil || r.SchemaVersion != "1" || r.RunID == "" || r.At.IsZero() || !oneOf(r.Event, "start", "finish") {
			return nil, nil, fmt.Errorf("invalid audit record")
		}
		if r.Event == "start" {
			if _, ok := starts[r.RunID]; ok {
				return nil, nil, fmt.Errorf("duplicate audit start")
			}
			starts[r.RunID] = r
		} else {
			s, ok := starts[r.RunID]
			if !ok || finishes[r.RunID] || r.ExitCode == nil || r.At.Before(s.At) || s.Command != r.Command || s.User != r.User {
				return nil, nil, fmt.Errorf("unpaired or inconsistent audit finish")
			}
			finishes[r.RunID] = true
		}
		records = append(records, r)
	}
	return raw, records, nil
}

func runAudit(args []string, configPath string, stdout, stderr io.Writer) error {
	mode := "read"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("audit "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "audit JSONL or gzip archive; default configured audit file")
	output := fs.String("output", "", "new gzip archive for rotate")
	older := fs.Duration("older-than", 30*24*time.Hour, "minimum age of every archived run before prune")
	apply := fs.Bool("apply", false, "perform rotation; otherwise preview")
	user := fs.String("user", "", "exact OS username")
	command := fs.String("command", "", "exact command")
	since := fs.String("since", "", "inclusive UTC/RFC3339 start time")
	until := fs.String("until", "", "inclusive UTC/RFC3339 start time")
	exit := fs.Int("exit", -1, "exit code filter; -1 means any")
	unfinished := fs.Bool("unfinished", false, "only invocations without a finish; may still be running")
	show := fs.Bool("show-output", false, "include captured redacted output")
	limit := fs.Int("limit", 50, "maximum invocations shown, 1..10000")
	format := fs.String("format", "text", "text or json")
	width := fs.Int("width", 72, "minimum 40")
	setAccessibleUsage(fs, "audit "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(mode, "read", "rotate", "prune") || !oneOf(*format, "text", "json") || *limit < 1 || *limit > 10000 || *width < 40 || *exit < -1 {
		return fmt.Errorf("invalid audit options")
	}
	if mode != "read" && (*user != "" || *command != "" || *since != "" || *until != "" || *exit >= 0 || *unfinished || *show) {
		return fmt.Errorf("read filters do not select rotation or retention scope")
	}
	if mode == "read" && *apply {
		return fmt.Errorf("--apply is only valid for rotate or prune")
	}
	var from, to time.Time
	var err error
	if *since != "" {
		from, err = time.Parse(time.RFC3339Nano, *since)
		if err != nil {
			return err
		}
	}
	if *until != "" {
		to, err = time.Parse(time.RFC3339Nano, *until)
		if err != nil {
			return err
		}
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return fmt.Errorf("since is later than until")
	}
	if *input == "" {
		if configPath == "" {
			configPath, err = defaultAppConfigPath()
			if err != nil {
				return err
			}
		}
		c, _, e := loadAppConfig(configPath)
		if e != nil {
			return e
		}
		*input = auditConfigPath(configPath, c.Audit)
	}
	if mode == "prune" {
		return pruneAudit(*input, *older, *apply, stdout)
	}
	if mode == "rotate" {
		return rotateAudit(*input, *output, *apply, stdout)
	}
	_, records, err := readAuditRecords(*input)
	if err != nil {
		return err
	}
	ends := map[string]auditRecord{}
	for _, r := range records {
		if r.Event == "finish" {
			ends[r.RunID] = r
		}
	}
	type row struct {
		Start  auditRecord  `json:"start"`
		Finish *auditRecord `json:"finish,omitempty"`
		Status string       `json:"status"`
	}
	result := struct {
		Runs    []row `json:"runs"`
		Matched int   `json:"matched"`
		Omitted int   `json:"omitted"`
	}{Runs: []row{}}
	pending := false
	for index, s := range records {
		if s.Event != "start" {
			continue
		}
		e, done := ends[s.RunID]
		if index == len(records)-1 && s.Command == "audit" && s.PID == os.Getpid() && !done {
			continue
		}
		if *user != "" && s.User != *user || *command != "" && s.Command != *command || !from.IsZero() && s.At.Before(from) || !to.IsZero() && s.At.After(to) || *unfinished && done || *exit >= 0 && (!done || *e.ExitCode != *exit) {
			continue
		}
		result.Matched++
		if len(result.Runs) >= *limit {
			result.Omitted++
			continue
		}
		s.Arguments = auditArguments(s.Arguments)
		e.Arguments = auditArguments(e.Arguments)
		e.EffectiveArguments = auditArguments(e.EffectiveArguments)
		s.Stdout, s.Stderr = "", ""
		if !*show {
			e.Stdout, e.Stderr = "", ""
		} else {
			e.Stdout, e.Stderr = auditText(e.Stdout), auditText(e.Stderr)
		}
		v := row{Start: s, Status: "unfinished (possibly running)"}
		if done {
			v.Finish = &e
			v.Status = "finished"
		} else {
			pending = true
		}
		result.Runs = append(result.Runs, v)
	}
	if *format == "json" {
		err = json.NewEncoder(stdout).Encode(result)
	} else {
		for i, v := range result.Runs {
			writeWrapped(stdout, auditText(fmt.Sprintf("%d. %s. User %s. Command %s. Started %s. Run %s.", i+1, v.Status, v.Start.User, v.Start.Command, v.Start.At.Format(time.RFC3339), v.Start.RunID)), *width)
			if v.Finish != nil {
				writeWrapped(stdout, fmt.Sprintf("Exit %d. Duration %d ms. Stdout %d bytes; truncated %t. Stderr %d bytes; truncated %t.", *v.Finish.ExitCode, v.Finish.DurationMS, v.Finish.StdoutBytes, v.Finish.StdoutTruncated, v.Finish.StderrBytes, v.Finish.StderrTruncated), *width)
				if *show {
					writeWrapped(stdout, "Stdout: "+v.Finish.Stdout+"\nStderr: "+v.Finish.Stderr, *width)
				}
			}
		}
		fmt.Fprintf(stdout, "Matched %d invocations; omitted %d.\n", result.Matched, result.Omitted)
	}
	if err != nil {
		return err
	}
	if pending || result.Omitted > 0 {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}

func rotateAudit(input, output string, apply bool, w io.Writer) error {
	if output == "" || !strings.HasSuffix(output, ".gz") {
		return fmt.Errorf("rotate requires a new --output archive.gz")
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return err
	}
	if err := separateArtifact(output, boundArtifact{Path: absolute}); err != nil {
		return err
	}
	unlock, err := lockAudit(input)
	if err != nil {
		return err
	}
	defer unlock()
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("rotation requires a regular active log, not a symlink")
	}
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	var head [2]byte
	n, _ := f.Read(head[:])
	f.Close()
	if n == 2 && bytes.Equal(head[:], []byte{31, 139}) {
		return fmt.Errorf("rotate requires uncompressed JSONL")
	}
	raw, records, err := readAuditRecords(input)
	if err != nil {
		return err
	}
	if bytes.HasPrefix(raw, []byte{31, 139}) || strings.HasSuffix(input, ".gz") {
		return fmt.Errorf("rotate requires an active uncompressed JSONL file")
	}
	completed := map[string]bool{}
	for _, r := range records {
		if r.Event == "finish" {
			completed[r.RunID] = true
		}
	}
	var archive, active bytes.Buffer
	for _, r := range records {
		dest := &active
		if completed[r.RunID] {
			dest = &archive
		}
		if err := json.NewEncoder(dest).Encode(r); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "Archive %d completed invocations; retain %d active records. Apply: %t.\n", len(completed), len(records)-2*len(completed), apply)
	if !apply || len(completed) == 0 {
		return nil
	}
	var zipped bytes.Buffer
	gz := gzip.NewWriter(&zipped)
	if _, err := gz.Write(archive.Bytes()); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := publishMarkdown(output, zipped.Bytes()); err != nil {
		return err
	}
	// Archive is durable before replacing the live file. A failed replacement leaves
	// both copies intact, never silently discards history or in-progress runs.
	return atomicReplaceChecked(input, active.Bytes(), io.Discard, func() error {
		now, err := os.ReadFile(input)
		if err != nil || !bytes.Equal(now, raw) {
			return fmt.Errorf("audit source changed; archive retained and live file untouched")
		}
		return nil
	})
}

func pruneAudit(input string, age time.Duration, apply bool, w io.Writer) error {
	if age <= 0 || !strings.HasSuffix(input, ".gz") {
		return fmt.Errorf("prune requires a gzip archive and positive --older-than")
	}
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("archive must be a regular file, not a symlink")
	}
	source, raw, err := captureArtifact(input)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(raw, []byte{31, 139}) {
		return fmt.Errorf("archive must be gzip")
	}
	_, records, err := readAuditRecords(input)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	cutoff := time.Now().Add(-age)
	for _, r := range records {
		if r.At.After(cutoff) {
			return fmt.Errorf("archive contains records newer than retention cutoff")
		}
		if r.Event == "start" {
			active[r.RunID] = true
		} else {
			delete(active, r.RunID)
		}
	}
	if len(active) > 0 {
		return fmt.Errorf("archive contains unfinished runs")
	}
	fmt.Fprintf(w, "Remove archive containing %d completed invocations older than %s. Apply: %t.\n", len(records)/2, age, apply)
	if !apply {
		return nil
	}
	if _, err := readBoundArtifact(source); err != nil {
		return err
	}
	return os.Remove(input)
}
