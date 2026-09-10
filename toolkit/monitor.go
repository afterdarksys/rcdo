package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

type monitorState struct {
	SchemaVersion string        `json:"schema_version"`
	Source        boundArtifact `json:"source"`
	Syntax        string        `json:"syntax"`
	Offset        int           `json:"offset"`
	Line          int           `json:"line"`
	Paused        bool          `json:"paused"`
	Queue         []string      `json:"queue"`
	Dropped       int           `json:"dropped"`
	Gap           bool          `json:"gap"`
}

func monitorLine(data []byte, syntax string) (string, error) {
	if syntax == "ansible" {
		var event ansibleEvent
		if strictJSON(data, &event) != nil || event.SchemaVersion != "1" || !oneOf(event.Event, "start", "result", "finish") {
			return "", fmt.Errorf("invalid Ansible callback record")
		}
		if _, err := time.Parse(time.RFC3339Nano, event.At); err != nil || event.RunID == "" || event.Sequence < 0 {
			return "", fmt.Errorf("invalid callback identity/time")
		}
		task := event.Task
		if event.NoLog {
			task = "[withheld: no_log]"
		}
		checkMode := "unavailable"
		if event.CheckMode != nil {
			checkMode = fmt.Sprint(*event.CheckMode)
		}
		return auditText(fmt.Sprintf("Run %s. Sequence %d. Event %s. Host %s. Task %s. Outcome %s. Check mode %s. Ignored failure %t.", event.RunID, event.Sequence, event.Event, event.Host, task, event.Outcome, checkMode, event.Ignored)), nil
	}
	events, err := parseLog(data, syntax)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	return auditText(events[0].Message), nil
}

func monitorPoll(state *monitorState, maxQueue, batch int, accept bool) ([]string, error) {
	raw, err := readConfigSource(state.Source.Path)
	if err != nil {
		state.Gap = true
		return []string{"Gap: source unavailable. Reconnect requires --accept-gap."}, nil
	}
	if len(raw) >= 2 && bytes.Equal(raw[:2], []byte{31, 139}) || bytes.HasPrefix(raw, []byte("BZh")) {
		return nil, fmt.Errorf("live monitoring requires an uncompressed append-only file")
	}
	changed := state.Offset > len(raw) || state.Offset < 0
	if !changed {
		changed = digestBytes(raw[:state.Offset]) != state.Source.SHA256
	}
	messages := []string{}
	if changed || state.Gap {
		state.Gap = true
		if !accept {
			return []string{"Gap: source changed or reconnected. Use --accept-gap to restart from the current file; duplicates or missing events are possible."}, nil
		}
		state.Offset, state.Line = 0, 0
		state.Source.SHA256 = digestBytes(nil)
		state.Gap = false
		messages = append(messages, "Gap accepted: restarting current source; continuity is not established.")
	}
	rest := raw[state.Offset:]
	end := bytes.LastIndexByte(rest, '\n')
	if end >= 0 {
		for _, line := range bytes.Split(rest[:end], []byte("\n")) {
			state.Line++
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			value, e := monitorLine(line, state.Syntax)
			if e != nil {
				return nil, fmt.Errorf("invalid event at source line %d", state.Line)
			}
			for len(state.Queue) >= maxQueue {
				state.Queue = state.Queue[1:]
				state.Dropped++
			}
			state.Queue = append(state.Queue, fmt.Sprintf("Line %d. %s", state.Line, value))
		}
		state.Offset += end + 1
		state.Source.SHA256 = digestBytes(raw[:state.Offset])
	}
	if len(raw)-state.Offset > 64<<10 {
		return nil, fmt.Errorf("unfinished event exceeds 64 KiB")
	}
	if !state.Paused {
		count := min(batch, len(state.Queue))
		messages = append(messages, state.Queue[:count]...)
		state.Queue = state.Queue[count:]
	}
	return messages, nil
}

func runMonitor(args []string, stdout, stderr io.Writer) error {
	mode := "poll"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("monitor "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "append-only log, event JSONL, or Ansible callback file for start")
	statePath := fs.String("state", ".rcdo-monitor.json", "saved monitor state")
	syntax := fs.String("syntax", "text", "text, jsonl (normalized events), or ansible")
	queue := fs.Int("max-queue", 100, "bounded pending announcements, 1..1000")
	batch := fs.Int("batch", 10, "maximum announcements per poll, 1..100")
	accept := fs.Bool("accept-gap", false, "acknowledge missing/rotated evidence and restart current source")
	interval := fs.Duration("interval", time.Second, "follow polling interval, 100ms..1m")
	duration := fs.Duration("duration", 30*time.Second, "follow duration, at most 24h")
	format := fs.String("format", "text", "text or jsonl")
	width := fs.Int("width", 72, "minimum 40")
	setAccessibleUsage(fs, "monitor "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !oneOf(mode, "start", "poll", "pause", "resume", "follow") || !oneOf(*syntax, "text", "jsonl", "ansible") || !oneOf(*format, "text", "jsonl") || *queue < 1 || *queue > 1000 || *batch < 1 || *batch > 100 || *width < 40 || *interval < 100*time.Millisecond || *interval > time.Minute || *duration <= 0 || *duration > 24*time.Hour {
		return fmt.Errorf("invalid monitor options")
	}
	if mode == "start" {
		if *input == "" {
			return fmt.Errorf("start requires --input")
		}
		source, _, err := captureArtifact(*input)
		if err != nil {
			return err
		}
		if err := separateArtifact(*statePath, source); err != nil {
			return err
		}
		source.SHA256 = digestBytes(nil)
		state := monitorState{SchemaVersion: "1", Source: source, Syntax: *syntax, Queue: []string{}}
		if err := saveWorkState(*statePath, state, nil, nil); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	deadline := time.NewTimer(*duration)
	defer deadline.Stop()
	hadGap := false
	for {
		previous, err := readConfigSource(*statePath)
		if err != nil {
			return err
		}
		var state monitorState
		if strictJSON(previous, &state) != nil || state.SchemaVersion != "1" || !oneOf(state.Syntax, "text", "jsonl", "ansible") || state.Offset < 0 || len(state.Queue) > 1000 || state.Dropped < 0 || !filepath.IsAbs(state.Source.Path) || len(state.Source.SHA256) != 64 || state.Line < 0 {
			return fmt.Errorf("invalid monitor state")
		}
		if err := separateArtifact(*statePath, state.Source); err != nil {
			return err
		}
		messages := []string{}
		if mode == "pause" {
			state.Paused = true
		} else if mode == "resume" {
			state.Paused = false
		}
		if mode != "pause" {
			messages, err = monitorPoll(&state, *queue, *batch, *accept)
			if err != nil {
				return err
			}
			*accept = false
		}
		if err := saveWorkState(*statePath, state, previous, nil); err != nil {
			return err
		}
		result := struct {
			At       time.Time `json:"at"`
			Messages []string  `json:"messages"`
			Paused   bool      `json:"paused"`
			Queued   int       `json:"queued"`
			Dropped  int       `json:"dropped"`
			Gap      bool      `json:"gap"`
		}{time.Now().UTC(), messages, state.Paused, len(state.Queue), state.Dropped, state.Gap}
		if *format == "jsonl" {
			err = json.NewEncoder(stdout).Encode(result)
		} else {
			for _, m := range messages {
				writeWrapped(stdout, auditText(m), *width)
			}
			fmt.Fprintf(stdout, "Paused: %t. Queued: %d. Dropped: %d. Gap: %t.\n", state.Paused, len(state.Queue), state.Dropped, state.Gap)
		}
		if err != nil {
			return err
		}
		hadGap = hadGap || state.Gap || state.Dropped > 0
		if mode != "follow" {
			break
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if hadGap {
				return reportError{status: finding.StatusIncomplete}
			}
			return nil
		case <-deadline.C:
			timer.Stop()
			if hadGap {
				return reportError{status: finding.StatusIncomplete}
			}
			return nil
		case <-timer.C:
		}
	}
	if hadGap {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
