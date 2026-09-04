package toolkit

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"git-tools/finding"
)

type configSnapshot struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
	Line  int    `json:"line,omitempty"`
}

type configChange struct {
	Path   string          `json:"path"`
	Change string          `json:"change"`
	Before *configSnapshot `json:"before,omitempty"`
	After  *configSnapshot `json:"after,omitempty"`
}

type configDiff struct {
	SchemaVersion string         `json:"schema_version"`
	BeforeFormat  string         `json:"before_format"`
	AfterFormat   string         `json:"after_format"`
	Changed       bool           `json:"changed"`
	Summary       map[string]int `json:"summary"`
	Changes       []configChange `json:"changes"`
}

func runConfigDiff(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var beforePath, afterPath, syntax, beforeSyntax, afterSyntax string
	var outputFormat, pathFilter string
	var width int
	var showSecrets, showValues, check bool
	fs := flag.NewFlagSet("config-diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&beforePath, "before", "", "configuration before the change; use - for standard input")
	fs.StringVar(&afterPath, "after", "", "configuration after the change; use - for standard input")
	fs.StringVar(&syntax, "syntax", "auto", "input syntax for both files: auto, json, yaml, toml, or hcl")
	fs.StringVar(&beforeSyntax, "before-syntax", "", "override syntax for the before file")
	fs.StringVar(&afterSyntax, "after-syntax", "", "override syntax for the after file")
	fs.StringVar(&outputFormat, "format", "text", "output format: text or json")
	fs.StringVar(&pathFilter, "path", "", "show changes only at this path and its descendants")
	fs.IntVar(&width, "width", 100, "maximum text line width; minimum 40")
	fs.BoolVar(&showValues, "values", true, "include values before and after the change")
	fs.BoolVar(&showSecrets, "show-secrets", false, "show values whose paths look sensitive")
	fs.BoolVar(&check, "check", false, "exit 10 when semantic changes are present")
	setAccessibleUsage(fs, "config-diff", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if beforePath == "" || afterPath == "" {
		return fmt.Errorf("--before and --after are required")
	}
	if beforePath == "-" && afterPath == "-" {
		return fmt.Errorf("only one of --before and --after may use standard input")
	}
	if outputFormat != "text" && outputFormat != "json" {
		return fmt.Errorf("unknown format %q; expected text or json", outputFormat)
	}
	if width < 40 {
		return fmt.Errorf("--width must be at least 40")
	}
	if beforeSyntax == "" {
		beforeSyntax = syntax
	}
	if afterSyntax == "" {
		afterSyntax = syntax
	}

	beforeData, err := readInput(beforePath, stdin)
	if err != nil {
		return fmt.Errorf("before configuration: %w", err)
	}
	afterData, err := readInput(afterPath, stdin)
	if err != nil {
		return fmt.Errorf("after configuration: %w", err)
	}
	resolvedBefore, err := detectConfigSyntax(beforeSyntax, beforePath, beforeData)
	if err != nil {
		return fmt.Errorf("before configuration: %w", err)
	}
	resolvedAfter, err := detectConfigSyntax(afterSyntax, afterPath, afterData)
	if err != nil {
		return fmt.Errorf("after configuration: %w", err)
	}

	// Parse unredacted values so a sensitive change is still detected. Values
	// are redacted only after comparison and before rendering.
	beforeEntries, err := explainConfig(resolvedBefore, beforePath, beforeData, true, true)
	if err != nil {
		return fmt.Errorf("before configuration: %w", err)
	}
	afterEntries, err := explainConfig(resolvedAfter, afterPath, afterData, true, true)
	if err != nil {
		return fmt.Errorf("after configuration: %w", err)
	}
	if pathFilter != "" && !configPathExists(beforeEntries, pathFilter) && !configPathExists(afterEntries, pathFilter) {
		return fmt.Errorf("path %q was not found in either configuration", pathFilter)
	}

	changes := compareConfigEntries(beforeEntries, afterEntries)
	if pathFilter != "" {
		filter := normalizeConfigPath(pathFilter)
		selected := changes[:0]
		for _, change := range changes {
			if configPathWithin(change.Path, filter) {
				selected = append(selected, change)
			}
		}
		changes = selected
	}
	for index := range changes {
		prepareConfigChange(&changes[index], showValues, showSecrets)
	}
	summary := map[string]int{"added": 0, "removed": 0, "changed": 0}
	for _, change := range changes {
		summary[change.Change]++
	}
	diff := configDiff{
		SchemaVersion: "1", BeforeFormat: resolvedBefore, AfterFormat: resolvedAfter,
		Changed: len(changes) > 0, Summary: summary, Changes: changes,
	}
	if outputFormat == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(diff); err != nil {
			return err
		}
	} else {
		renderConfigDiffText(stdout, diff, width)
	}
	if check && diff.Changed {
		return reportError{status: finding.StatusReview}
	}
	return nil
}

func compareConfigEntries(beforeEntries, afterEntries []configEntry) []configChange {
	before := make(map[string]configEntry, len(beforeEntries))
	after := make(map[string]configEntry, len(afterEntries))
	paths := make(map[string]struct{}, len(beforeEntries)+len(afterEntries))
	for _, entry := range beforeEntries {
		before[entry.Path] = entry
		paths[entry.Path] = struct{}{}
	}
	for _, entry := range afterEntries {
		after[entry.Path] = entry
		paths[entry.Path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Slice(ordered, func(i, j int) bool { return configPathLess(ordered[i], ordered[j]) })

	changes := make([]configChange, 0)
	for _, path := range ordered {
		beforeEntry, hadBefore := before[path]
		afterEntry, hasAfter := after[path]
		switch {
		case !hadBefore:
			if isConfigContainer(afterEntry.Kind) && hasDescendantOnlyIn(path, after, before) {
				continue
			}
			changes = append(changes, configChange{Path: path, Change: "added", After: snapshotConfigEntry(afterEntry)})
		case !hasAfter:
			if isConfigContainer(beforeEntry.Kind) && hasDescendantOnlyIn(path, before, after) {
				continue
			}
			changes = append(changes, configChange{Path: path, Change: "removed", Before: snapshotConfigEntry(beforeEntry)})
		case beforeEntry.Kind != afterEntry.Kind || (!isConfigContainer(beforeEntry.Kind) && beforeEntry.Value != afterEntry.Value):
			changes = append(changes, configChange{
				Path: path, Change: "changed", Before: snapshotConfigEntry(beforeEntry), After: snapshotConfigEntry(afterEntry),
			})
		}
	}
	return changes
}

func snapshotConfigEntry(entry configEntry) *configSnapshot {
	return &configSnapshot{Kind: entry.Kind, Value: entry.Value, Line: entry.Line}
}

func isConfigContainer(kind string) bool {
	return kind == "object" || kind == "array" || kind == "body"
}

func hasDescendantOnlyIn(path string, primary, secondary map[string]configEntry) bool {
	for candidate := range primary {
		if candidate != path && configPathWithin(candidate, path) {
			if _, exists := secondary[candidate]; !exists {
				return true
			}
		}
	}
	return false
}

func prepareConfigChange(change *configChange, showValues, showSecrets bool) {
	for _, snapshot := range []*configSnapshot{change.Before, change.After} {
		if snapshot == nil {
			continue
		}
		if !showValues {
			snapshot.Value = ""
		} else if isSensitivePath(change.Path) && !showSecrets {
			snapshot.Value = "[REDACTED]"
		}
	}
}

func renderConfigDiffText(w io.Writer, diff configDiff, width int) {
	fmt.Fprintln(w, "CONFIGURATION DIFF")
	fmt.Fprintf(w, "Before format: %s\n", diff.BeforeFormat)
	fmt.Fprintf(w, "After format: %s\n", diff.AfterFormat)
	if diff.Changed {
		fmt.Fprintln(w, "Result: CHANGED")
	} else {
		fmt.Fprintln(w, "Result: UNCHANGED")
	}
	fmt.Fprintf(w, "Changes: %d\n", len(diff.Changes))
	fmt.Fprintf(w, "Added: %d\n", diff.Summary["added"])
	fmt.Fprintf(w, "Removed: %d\n", diff.Summary["removed"])
	fmt.Fprintf(w, "Changed: %d\n", diff.Summary["changed"])
	for index, change := range diff.Changes {
		fmt.Fprintf(w, "\nChange %d\n", index+1)
		writeWrapped(w, "Path: "+change.Path, width)
		fmt.Fprintf(w, "Action: %s\n", strings.ToUpper(change.Change))
		renderConfigSnapshot(w, "Before", change.Before, width)
		renderConfigSnapshot(w, "After", change.After, width)
	}
}

func renderConfigSnapshot(w io.Writer, label string, snapshot *configSnapshot, width int) {
	if snapshot == nil {
		return
	}
	fmt.Fprintf(w, "%s kind: %s\n", label, snapshot.Kind)
	if snapshot.Line > 0 {
		fmt.Fprintf(w, "%s line: %d\n", label, snapshot.Line)
	}
	if snapshot.Value != "" {
		writeWrapped(w, label+" value: "+snapshot.Value, width)
	}
}

func configPathExists(entries []configEntry, filter string) bool {
	filter = normalizeConfigPath(filter)
	for _, entry := range entries {
		if configPathWithin(entry.Path, filter) {
			return true
		}
	}
	return false
}

func normalizeConfigPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "$" || strings.HasPrefix(path, "$") {
		return path
	}
	if strings.HasPrefix(path, "[") {
		return "$" + path
	}
	return "$." + path
}

func configPathWithin(path, parent string) bool {
	return path == parent || strings.HasPrefix(path, parent+".") || strings.HasPrefix(path, parent+"[")
}

// configPathLess keeps array indexes in numeric reading order, so item 2 is
// presented before item 10. Other path components retain deterministic lexical
// ordering.
func configPathLess(left, right string) bool {
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		leftNumber, leftNext, leftOK := configArrayIndex(left, leftIndex)
		rightNumber, rightNext, rightOK := configArrayIndex(right, rightIndex)
		if leftOK && rightOK {
			leftNumber = strings.TrimLeft(leftNumber, "0")
			rightNumber = strings.TrimLeft(rightNumber, "0")
			if leftNumber == "" {
				leftNumber = "0"
			}
			if rightNumber == "" {
				rightNumber = "0"
			}
			if len(leftNumber) != len(rightNumber) {
				return len(leftNumber) < len(rightNumber)
			}
			if leftNumber != rightNumber {
				return leftNumber < rightNumber
			}
			leftIndex, rightIndex = leftNext, rightNext
			continue
		}
		if left[leftIndex] != right[rightIndex] {
			return left[leftIndex] < right[rightIndex]
		}
		leftIndex++
		rightIndex++
		if leftIndex == len(left) || rightIndex == len(right) {
			return len(left) < len(right)
		}
	}
	return len(left) < len(right)
}

func configArrayIndex(path string, start int) (string, int, bool) {
	if start >= len(path) || path[start] != '[' {
		return "", start, false
	}
	end := start + 1
	for end < len(path) && path[end] >= '0' && path[end] <= '9' {
		end++
	}
	if end == start+1 || end >= len(path) || path[end] != ']' {
		return "", start, false
	}
	return path[start+1 : end], end + 1, true
}
