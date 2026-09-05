package toolkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"git-tools/finding"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type configWalkState struct {
	SchemaVersion string            `json:"schema_version"`
	Source        string            `json:"source"`
	Syntax        string            `json:"syntax"`
	Cursor        string            `json:"cursor"`
	SourceSHA256  string            `json:"source_sha256"`
	Bookmarks     map[string]string `json:"bookmarks"`
}

func saveConfigWalk(path string, s configWalkState, create bool) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if create {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("cannot create navigation state; use a new path in an existing directory")
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
func runConfigWalk(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || oneOf(args[0], "help", "--help") {
		fmt.Fprintln(stdout, "config-walk: persistent structural configuration navigation\nUsage: config-walk COMMAND [options]\nCommands: start, show, parent, child, next, previous, find, goto, bookmark\nPaths are exact; bookmarks never evaluate cloud configuration.")
		return nil
	}
	mode, args := args[0], args[1:]
	if !oneOf(mode, "start", "show", "parent", "child", "next", "previous", "find", "goto", "bookmark") {
		return fmt.Errorf("unknown config-walk command")
	}
	fs := flag.NewFlagSet("config-walk "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state", ".rcdo-config-walk.json", "navigation state file")
	width := fs.Int("width", 72, "text width; minimum 40")
	format := fs.String("format", "text", "text or json; JSON retains exact paths")
	var input, syntax, path, name, query string
	if mode == "start" {
		fs.StringVar(&input, "input", "", "configuration file")
		fs.StringVar(&syntax, "syntax", "auto", "auto, json, yaml, toml, hcl")
	}
	if mode == "goto" {
		fs.StringVar(&path, "path", "", "exact path")
		fs.StringVar(&name, "name", "", "bookmark name")
	}
	if mode == "bookmark" {
		fs.StringVar(&name, "name", "", "new bookmark name")
	}
	if mode == "find" {
		fs.StringVar(&query, "query", "", "case-insensitive path or kind search; values excluded")
	}
	setAccessibleUsage(fs, "config-walk "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *width < 40 || (*format != "text" && *format != "json") {
		return fmt.Errorf("invalid arguments, format or width")
	}
	var state configWalkState
	if mode == "start" {
		if input == "" {
			return fmt.Errorf("start requires --input")
		}
		absolute, err := filepath.Abs(input)
		if err != nil {
			return err
		}
		state = configWalkState{SchemaVersion: "1", Source: absolute, Syntax: syntax, Cursor: "$", Bookmarks: map[string]string{}}
		target, _ := filepath.Abs(*statePath)
		if target == absolute {
			return fmt.Errorf("navigation state must be separate from the source")
		}
	} else {
		data, err := readConfigSource(*statePath)
		if err != nil {
			return err
		}
		if json.Unmarshal(data, &state) != nil || state.SchemaVersion != "1" || !filepath.IsAbs(state.Source) {
			return fmt.Errorf("invalid navigation state")
		}
		if state.Bookmarks == nil {
			state.Bookmarks = map[string]string{}
		}
	}
	target, err := filepath.Abs(*statePath)
	if err != nil {
		return err
	}
	if target == state.Source {
		return fmt.Errorf("navigation state must be separate from source")
	}
	data, err := readConfigSource(state.Source)
	if err != nil {
		return err
	}
	resolved, err := detectConfigSyntax(state.Syntax, state.Source, data)
	if err != nil {
		return err
	}
	state.Syntax = resolved
	if err := validateConfigDocument(resolved, data); err != nil {
		return err
	}
	entries, err := explainConfig(resolved, state.Source, data, true, false)
	if err != nil {
		return fmt.Errorf("cannot navigate invalid configuration")
	}
	byPath := map[string]configEntry{}
	parents := map[string]string{}
	children := map[string][]string{}
	stack := []string{}
	for _, entry := range entries {
		if _, exists := byPath[entry.Path]; exists {
			return fmt.Errorf("ambiguous duplicate configuration paths; select an unambiguous document")
		}
		for len(stack) > 0 && !configPathWithin(entry.Path, stack[len(stack)-1]) {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			parents[entry.Path] = parent
			children[parent] = append(children[parent], entry.Path)
		}
		byPath[entry.Path] = entry
		stack = append(stack, entry.Path)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	changed := state.SourceSHA256 != "" && state.SourceSHA256 != digest
	missing := false
	if _, ok := byPath[state.Cursor]; !ok {
		missing = true
	}
	matches := []string{}
	if mode == "goto" {
		if (path == "") == (name == "") {
			return fmt.Errorf("goto requires exactly one of --path or --name")
		}
		if name != "" {
			var ok bool
			path, ok = state.Bookmarks[name]
			if !ok {
				return fmt.Errorf("bookmark does not exist")
			}
		}
		if _, ok := byPath[path]; !ok {
			return fmt.Errorf("target path no longer exists; choose a current exact path")
		}
		state.Cursor = path
		missing = false
	} else if !missing {
		switch mode {
		case "start", "show":
		case "parent":
			if parent, ok := parents[state.Cursor]; ok {
				state.Cursor = parent
			}
		case "child":
			if list := children[state.Cursor]; len(list) > 0 {
				state.Cursor = list[0]
			}
		case "next", "previous":
			siblings := children[parents[state.Cursor]]
			for i, p := range siblings {
				if p == state.Cursor {
					if mode == "next" && i+1 < len(siblings) {
						state.Cursor = siblings[i+1]
					}
					if mode == "previous" && i > 0 {
						state.Cursor = siblings[i-1]
					}
					break
				}
			}
		case "bookmark":
			if !operationLabel(name) {
				return fmt.Errorf("bookmark requires a single-line --name")
			}
			if _, exists := state.Bookmarks[name]; exists {
				return fmt.Errorf("bookmark already exists")
			}
			state.Bookmarks[name] = state.Cursor
		case "find":
			if strings.TrimSpace(query) == "" {
				return fmt.Errorf("find requires --query")
			}
			for _, entry := range entries {
				if strings.Contains(strings.ToLower(entry.Path+" "+entry.Kind), strings.ToLower(query)) {
					matches = append(matches, entry.Path)
				}
			}
		default:
			return fmt.Errorf("unknown config-walk command")
		}
	}
	status := "ready"
	if missing {
		status = "path_missing"
	}
	record := struct {
		Schema        string      `json:"schema"`
		Status        string      `json:"status"`
		Source        string      `json:"source"`
		SourceSHA256  string      `json:"source_sha256"`
		SourceChanged bool        `json:"source_changed"`
		Cursor        string      `json:"cursor"`
		Entry         configEntry `json:"entry"`
		Children      []string    `json:"children"`
		Matches       []string    `json:"matches"`
	}{"rcdo/config-walk/v1", status, state.Source, digest, changed, state.Cursor, byPath[state.Cursor], children[state.Cursor], matches}
	if *format == "json" {
		if err := json.NewEncoder(stdout).Encode(record); err != nil {
			return err
		}
	} else {
		var out bytes.Buffer
		fmt.Fprintf(&out, "CONFIGURATION POSITION\nSource: %s\nStatus: %s\nPath: %s\n", state.Source, status, state.Cursor)
		if changed {
			fmt.Fprintln(&out, "Source version changed. Position identifies a path, not an unchanged value.")
		}
		if missing {
			fmt.Fprintln(&out, "Saved path is missing. Use goto with an exact current path; no position was discarded.")
		} else {
			entry := byPath[state.Cursor]
			fmt.Fprintf(&out, "Kind: %s\nValue: %s\nDirect children: %d\n", entry.Kind, entry.Value, len(children[state.Cursor]))
			if entry.Line > 0 {
				fmt.Fprintf(&out, "Source line: %d\n", entry.Line)
			}
		}
		for _, p := range matches {
			fmt.Fprintf(&out, "Match: %s\n", p)
		}
		keys := make([]string, 0, len(state.Bookmarks))
		for k := range state.Bookmarks {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&out, "Bookmark: %s; path: %s\n", k, state.Bookmarks[k])
		}
		sessionText(stdout, out.String(), *width)
	}
	if missing {
		return reportError{status: finding.StatusIncomplete}
	}
	state.SourceSHA256 = digest
	return saveConfigWalk(*statePath, state, mode == "start")
}
