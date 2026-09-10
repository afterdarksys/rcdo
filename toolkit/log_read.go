package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"git-tools/finding"
)

type logEvent struct {
	Line      int    `json:"line"`
	Timestamp string `json:"timestamp,omitempty"`
	Level     string `json:"level,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Request   string `json:"request_id,omitempty"`
	Message   string `json:"message"`
	key       string
}
type logFilter struct {
	Query   string `json:"query"`
	Request string `json:"request_id"`
	Since   string `json:"since"`
	Until   string `json:"until"`
}
type logState struct {
	SchemaVersion string         `json:"schema_version"`
	Source        boundArtifact  `json:"source"`
	Syntax        string         `json:"syntax"`
	Filter        logFilter      `json:"filter"`
	Cursor        int            `json:"cursor"`
	Bookmarks     map[string]int `json:"bookmarks"`
}

func parseLog(data []byte, syntax string) ([]logEvent, error) {
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("log exceeds 8 MiB")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) > 100000 {
		return nil, fmt.Errorf("log exceeds 100000 lines")
	}
	events := []logEvent{}
	for i, line := range lines {
		if len(line) > 64<<10 {
			return nil, fmt.Errorf("log line exceeds 64 KiB")
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		e := logEvent{Line: i + 1, Message: string(line)}
		if syntax == "jsonl" {
			var v struct {
				Timestamp string  `json:"timestamp"`
				Level     string  `json:"level"`
				Resource  string  `json:"resource"`
				Request   string  `json:"request_id"`
				Message   *string `json:"message"`
			}
			if strictJSON(line, &v) != nil || v.Message == nil {
				return nil, fmt.Errorf("invalid JSON event at line %d", i+1)
			}
			e.Timestamp, e.Level, e.Resource, e.Request, e.Message = v.Timestamp, v.Level, v.Resource, v.Request, *v.Message
		}
		e.key = digestBytes([]byte(e.Level + "\x00" + e.Resource + "\x00" + e.Request + "\x00" + e.Message))
		e.Message = markdownSafe(redactAIText(e.Message))
		e.Level = markdownSafe(e.Level)
		e.Resource = markdownSafe(redactAIText(e.Resource))
		e.Request = markdownSafe(redactAIText(e.Request))
		e.Timestamp = markdownSafe(e.Timestamp)
		events = append(events, e)
	}
	return events, nil
}
func logMatches(events []logEvent, f logFilter) ([]int, int, error) {
	var since, until time.Time
	var err error
	if f.Since != "" {
		since, err = time.Parse(time.RFC3339Nano, f.Since)
		if err != nil {
			return nil, 0, fmt.Errorf("--since requires RFC3339 with timezone")
		}
	}
	if f.Until != "" {
		until, err = time.Parse(time.RFC3339Nano, f.Until)
		if err != nil {
			return nil, 0, fmt.Errorf("--until requires RFC3339 with timezone")
		}
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return nil, 0, fmt.Errorf("--since is later than --until")
	}
	indexes := []int{}
	unknown := 0
	for i, e := range events {
		if f.Query != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(f.Query)) {
			continue
		}
		if f.Request != "" && e.Request != f.Request {
			continue
		}
		if !since.IsZero() || !until.IsZero() {
			t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err != nil {
				unknown++
				continue
			}
			if !since.IsZero() && t.Before(since) || !until.IsZero() && t.After(until) {
				continue
			}
		}
		indexes = append(indexes, i)
	}
	return indexes, unknown, nil
}
func runLogRead(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	mode := "summary"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	if !oneOf(mode, "summary", "start", "show", "next", "previous", "goto", "bookmark") {
		return fmt.Errorf("unknown log-read command")
	}
	fs := flag.NewFlagSet("log-read "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "-", "bounded log artifact; stdin in summary mode")
	syntax := fs.String("syntax", "text", "text or jsonl")
	statePath := fs.String("state", ".rcdo-log.json", "saved reading state")
	format := fs.String("format", "text", "text or json")
	width := fs.Int("width", 72, "text width, minimum 40")
	context := fs.Int("context", 2, "adjacent events in reading mode, 0 to 20")
	limit := fs.Int("max-groups", 20, "summary group display limit, 1 to 1000")
	line := fs.Int("line", 0, "source line for goto")
	name := fs.String("name", "", "bookmark name")
	var filter logFilter
	fs.StringVar(&filter.Query, "query", "", "literal message search, case insensitive")
	fs.StringVar(&filter.Request, "request", "", "exact request/correlation ID")
	fs.StringVar(&filter.Since, "since", "", "inclusive RFC3339 timestamp")
	fs.StringVar(&filter.Until, "until", "", "inclusive RFC3339 timestamp")
	setAccessibleUsage(fs, "log-read "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(*syntax, "text", "jsonl") || !oneOf(*format, "text", "json") || *width < 40 || *context < 0 || *context > 20 || *limit < 1 || *limit > 1000 {
		return fmt.Errorf("invalid log options")
	}
	var state logState
	var data, previous []byte
	var err error
	if oneOf(mode, "summary", "start") {
		if mode == "start" && *input == "-" {
			return fmt.Errorf("start requires a file")
		}
		if *input == "-" {
			data, err = io.ReadAll(io.LimitReader(stdin, 8<<20+1))
		} else {
			state.Source, data, err = captureArtifact(*input)
		}
		if err != nil {
			return err
		}
		state.SchemaVersion, state.Syntax, state.Filter, state.Bookmarks = "1", *syntax, filter, map[string]int{}
	} else {
		if *input != "-" || filter != (logFilter{}) {
			return fmt.Errorf("saved reading uses bound source and filters; start a new state to change them")
		}
		previous, err = readConfigSource(*statePath)
		if err != nil {
			return err
		}
		if strictJSON(previous, &state) != nil || state.SchemaVersion != "1" || !oneOf(state.Syntax, "text", "jsonl") {
			return fmt.Errorf("invalid log state")
		}
		if state.Bookmarks == nil {
			state.Bookmarks = map[string]int{}
		}
		data, err = readBoundArtifact(state.Source)
		if err != nil {
			fmt.Fprintln(stderr, "Log evidence is stale or unavailable; start a new reading state.")
			return reportError{status: finding.StatusIncomplete}
		}
	}
	events, err := parseLog(data, state.Syntax)
	if err != nil {
		return err
	}
	indexes, unknown, err := logMatches(events, state.Filter)
	if err != nil {
		return err
	}
	result := struct {
		SchemaVersion string `json:"schema_version"`
		SHA256        string `json:"sha256"`
		Total         int    `json:"total_events"`
		Matched       int    `json:"matched_events"`
		Unknown       int    `json:"unknown_time_events"`
		Omitted       int    `json:"omitted_groups"`
		Notice        string `json:"notice,omitempty"`
		Groups        []struct {
			Count   int      `json:"count"`
			Lines   []int    `json:"lines"`
			Example logEvent `json:"example"`
		} `json:"groups,omitempty"`
		Events []logEvent `json:"events,omitempty"`
		Cursor int        `json:"cursor,omitempty"`
	}{SchemaVersion: "1", SHA256: digestBytes(data), Total: len(events), Matched: len(indexes), Unknown: unknown}
	if mode == "summary" {
		positions := map[string]int{}
		for _, i := range indexes {
			e := events[i]
			g, ok := positions[e.key]
			if !ok {
				g = len(result.Groups)
				positions[e.key] = g
				result.Groups = append(result.Groups, struct {
					Count   int      `json:"count"`
					Lines   []int    `json:"lines"`
					Example logEvent `json:"example"`
				}{Example: e})
			}
			result.Groups[g].Count++
			result.Groups[g].Lines = append(result.Groups[g].Lines, e.Line)
		}
		if len(result.Groups) > *limit {
			result.Omitted = len(result.Groups) - *limit
			result.Groups = result.Groups[:*limit]
		}
	} else {
		if err := separateArtifact(*statePath, state.Source); err != nil {
			return err
		}
		if len(indexes) == 0 {
			return fmt.Errorf("no matching events; navigation state not changed")
		}
		if mode == "start" {
			state.Cursor = events[indexes[0]].Line
		}
		position := -1
		for p, i := range indexes {
			if events[i].Line == state.Cursor {
				position = p
			}
		}
		if position < 0 {
			return fmt.Errorf("saved cursor is not a matched event")
		}
		switch mode {
		case "next":
			if position+1 < len(indexes) {
				position++
			} else {
				result.Notice = "End of matching events."
			}
		case "previous":
			if position > 0 {
				position--
			} else {
				result.Notice = "Beginning of matching events."
			}
		case "goto":
			target := *line
			if (*line == 0) == (*name == "") {
				return fmt.Errorf("goto requires --line or --name")
			}
			if *name != "" {
				var ok bool
				target, ok = state.Bookmarks[*name]
				if !ok {
					return fmt.Errorf("bookmark not found")
				}
			}
			position = -1
			for p, i := range indexes {
				if events[i].Line == target {
					position = p
				}
			}
			if position < 0 {
				return fmt.Errorf("target line is not a matched event")
			}
		case "bookmark":
			if !operationLabel(*name) {
				return fmt.Errorf("bookmark requires a single-line name")
			}
			if _, ok := state.Bookmarks[*name]; ok {
				return fmt.Errorf("bookmark already exists")
			}
			state.Bookmarks[*name] = state.Cursor
			result.Notice = "Bookmark saved: " + *name
		}
		state.Cursor = events[indexes[position]].Line
		result.Cursor = state.Cursor
		lo, hi := indexes[position]-*context, indexes[position]+*context+1
		if lo < 0 {
			lo = 0
		}
		if hi > len(events) {
			hi = len(events)
		}
		result.Events = events[lo:hi]
		if mode != "show" {
			err = saveWorkState(*statePath, state, previous, func() error { _, err := readBoundArtifact(state.Source); return err })
			if err != nil {
				return err
			}
		}
	}
	if *format == "json" {
		err = json.NewEncoder(stdout).Encode(result)
	} else {
		writeWrapped(stdout, fmt.Sprintf("Log: %d events; %d match; %d have unavailable timestamps; %d groups omitted.", result.Total, result.Matched, result.Unknown, result.Omitted), *width)
		writeWrapped(stdout, "Times are source observations; cross-host clock synchronization is not established.", *width)
		if result.Notice != "" {
			writeWrapped(stdout, markdownSafe(result.Notice), *width)
		}
		for _, g := range result.Groups {
			writeWrapped(stdout, fmt.Sprintf("Count %d. First line %d. Last line %d. %s", g.Count, g.Lines[0], g.Lines[len(g.Lines)-1], g.Example.Message), *width)
		}
		for _, e := range result.Events {
			writeWrapped(stdout, fmt.Sprintf("Line %d. Time %s. Request %s. %s", e.Line, emptyValue(e.Timestamp), emptyValue(e.Request), e.Message), *width)
		}
	}
	if err != nil {
		return err
	}
	if unknown > 0 {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
