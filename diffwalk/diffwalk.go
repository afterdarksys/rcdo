// Package diffwalk parses and renders unified Git diffs for nonvisual review.
package diffwalk

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const SchemaVersion = "1"

type ChangeType string

const (
	Modified ChangeType = "modified"
	Added    ChangeType = "added"
	Deleted  ChangeType = "deleted"
	Renamed  ChangeType = "renamed"
)

type LineKind string

const (
	ContextLine LineKind = "context"
	AddedLine   LineKind = "added"
	RemovedLine LineKind = "removed"
)

type Line struct {
	Kind      LineKind `json:"kind"`
	OldNumber int      `json:"old_number,omitempty"`
	NewNumber int      `json:"new_number,omitempty"`
	Content   string   `json:"content"`
}

type Hunk struct {
	Header   string `json:"header"`
	Section  string `json:"section,omitempty"`
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
	Lines    []Line `json:"lines"`
}

type File struct {
	Path    string     `json:"path"`
	OldPath string     `json:"old_path"`
	NewPath string     `json:"new_path"`
	Change  ChangeType `json:"change"`
	Binary  bool       `json:"binary"`
	Hunks   []Hunk     `json:"hunks"`
}

type Diff struct {
	Files []File `json:"files"`
}

type Summary struct {
	Files       int `json:"files"`
	Hunks       int `json:"hunks"`
	Additions   int `json:"additions"`
	Deletions   int `json:"deletions"`
	BinaryFiles int `json:"binary_files"`
}

func (d Diff) Summary() Summary {
	summary := Summary{Files: len(d.Files)}
	for _, file := range d.Files {
		if file.Binary {
			summary.BinaryFiles++
		}
		for _, hunk := range file.Hunks {
			summary.Hunks++
			for _, line := range hunk.Lines {
				switch line.Kind {
				case AddedLine:
					summary.Additions++
				case RemovedLine:
					summary.Deletions++
				}
			}
		}
	}
	return summary
}

var hunkPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: ?(.*))?$`)

type parser struct {
	diff       Diff
	file       *File
	hunk       *Hunk
	oldCurrent int
	newCurrent int
	oldSeen    int
	newSeen    int
}

func Parse(r io.Reader) (Diff, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	p := parser{}
	lineNumber := 0
	readAny := false
	for scanner.Scan() {
		readAny = true
		lineNumber++
		if err := p.consume(scanner.Text(), lineNumber); err != nil {
			return Diff{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return Diff{}, fmt.Errorf("read diff: %w", err)
	}
	if !readAny {
		return Diff{}, fmt.Errorf("diff is empty")
	}
	if err := p.finishHunk(lineNumber); err != nil {
		return Diff{}, err
	}
	if len(p.diff.Files) == 0 {
		return Diff{}, fmt.Errorf("no file changes found in input")
	}
	return p.diff, nil
}

func (p *parser) consume(line string, inputLine int) error {
	if p.hunk != nil {
		if line == `\ No newline at end of file` {
			return nil
		}
		if p.oldSeen == p.hunk.OldLines && p.newSeen == p.hunk.NewLines {
			if err := p.finishHunk(inputLine - 1); err != nil {
				return err
			}
		} else {
			return p.consumeHunkLine(line, inputLine)
		}
	}

	switch {
	case strings.HasPrefix(line, "diff --git "):
		oldPath, newPath, err := parseGitPaths(strings.TrimPrefix(line, "diff --git "))
		if err != nil {
			return fmt.Errorf("line %d: %w", inputLine, err)
		}
		p.diff.Files = append(p.diff.Files, File{
			Path:    displayPath(oldPath, newPath),
			OldPath: oldPath,
			NewPath: newPath,
			Change:  Modified,
			Hunks:   []Hunk{},
		})
		p.file = &p.diff.Files[len(p.diff.Files)-1]
	case strings.HasPrefix(line, "rename from "):
		if p.file == nil {
			return fmt.Errorf("line %d: rename metadata appears before a file header", inputLine)
		}
		p.file.OldPath = decodePath(strings.TrimPrefix(line, "rename from "))
		p.file.Change = Renamed
	case strings.HasPrefix(line, "rename to "):
		if p.file == nil {
			return fmt.Errorf("line %d: rename metadata appears before a file header", inputLine)
		}
		p.file.NewPath = decodePath(strings.TrimPrefix(line, "rename to "))
		p.file.Path = p.file.NewPath
		p.file.Change = Renamed
	case strings.HasPrefix(line, "new file mode "):
		if p.file == nil {
			return fmt.Errorf("line %d: new-file metadata appears before a file header", inputLine)
		}
		p.file.Change = Added
	case strings.HasPrefix(line, "deleted file mode "):
		if p.file == nil {
			return fmt.Errorf("line %d: deleted-file metadata appears before a file header", inputLine)
		}
		p.file.Change = Deleted
	case strings.HasPrefix(line, "--- "):
		path := parseMarkerPath(strings.TrimPrefix(line, "--- "))
		if p.file == nil {
			p.diff.Files = append(p.diff.Files, File{Change: Modified, Hunks: []Hunk{}})
			p.file = &p.diff.Files[len(p.diff.Files)-1]
		}
		p.file.OldPath = path
		if path == "/dev/null" {
			p.file.Change = Added
		}
	case strings.HasPrefix(line, "+++ "):
		if p.file == nil {
			return fmt.Errorf("line %d: new path appears before an old path", inputLine)
		}
		path := parseMarkerPath(strings.TrimPrefix(line, "+++ "))
		p.file.NewPath = path
		if path == "/dev/null" {
			p.file.Change = Deleted
		}
		p.file.Path = displayPath(p.file.OldPath, p.file.NewPath)
	case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
		if p.file == nil {
			return fmt.Errorf("line %d: binary marker appears before a file header", inputLine)
		}
		p.file.Binary = true
	case strings.HasPrefix(line, "@@"):
		if p.file == nil {
			return fmt.Errorf("line %d: hunk appears before a file header", inputLine)
		}
		matches := hunkPattern.FindStringSubmatch(line)
		if matches == nil {
			return fmt.Errorf("line %d: malformed hunk header %q", inputLine, line)
		}
		oldStart, _ := strconv.Atoi(matches[1])
		oldLines := rangeCount(matches[2])
		newStart, _ := strconv.Atoi(matches[3])
		newLines := rangeCount(matches[4])
		p.file.Hunks = append(p.file.Hunks, Hunk{
			Header: line, OldStart: oldStart, OldLines: oldLines,
			NewStart: newStart, NewLines: newLines, Section: matches[5], Lines: []Line{},
		})
		p.hunk = &p.file.Hunks[len(p.file.Hunks)-1]
		p.oldCurrent, p.newCurrent = oldStart, newStart
		p.oldSeen, p.newSeen = 0, 0
	}
	return nil
}

func (p *parser) consumeHunkLine(line string, inputLine int) error {
	if line == "" {
		return fmt.Errorf("line %d: unexpected empty hunk line; context lines must start with a space", inputLine)
	}
	content := line[1:]
	switch line[0] {
	case ' ':
		p.hunk.Lines = append(p.hunk.Lines, Line{Kind: ContextLine, OldNumber: p.oldCurrent, NewNumber: p.newCurrent, Content: content})
		p.oldCurrent++
		p.newCurrent++
		p.oldSeen++
		p.newSeen++
	case '+':
		p.hunk.Lines = append(p.hunk.Lines, Line{Kind: AddedLine, NewNumber: p.newCurrent, Content: content})
		p.newCurrent++
		p.newSeen++
	case '-':
		p.hunk.Lines = append(p.hunk.Lines, Line{Kind: RemovedLine, OldNumber: p.oldCurrent, Content: content})
		p.oldCurrent++
		p.oldSeen++
	default:
		return fmt.Errorf("line %d: unexpected hunk line prefix %q", inputLine, line[0])
	}
	if p.oldSeen > p.hunk.OldLines || p.newSeen > p.hunk.NewLines {
		return fmt.Errorf("line %d: hunk body exceeds counts declared by header", inputLine)
	}
	return nil
}

func (p *parser) finishHunk(inputLine int) error {
	if p.hunk == nil {
		return nil
	}
	if p.oldSeen != p.hunk.OldLines || p.newSeen != p.hunk.NewLines {
		return fmt.Errorf("line %d: hunk body count mismatch: expected old %d/new %d lines, got old %d/new %d",
			inputLine, p.hunk.OldLines, p.hunk.NewLines, p.oldSeen, p.newSeen)
	}
	p.hunk = nil
	return nil
}

func rangeCount(value string) int {
	if value == "" {
		return 1
	}
	count, _ := strconv.Atoi(value)
	return count
}

func parseGitPaths(value string) (string, string, error) {
	parts := splitGitFields(value)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed git file header")
	}
	return trimGitPrefix(decodePath(parts[0])), trimGitPrefix(decodePath(parts[1])), nil
}

func splitGitFields(value string) []string {
	var fields []string
	for len(value) > 0 {
		value = strings.TrimLeft(value, " \t")
		if value == "" {
			break
		}
		if value[0] == '"' {
			for i, escaped := 1, false; i < len(value); i++ {
				if value[i] == '"' && !escaped {
					fields = append(fields, value[:i+1])
					value = value[i+1:]
					break
				}
				if value[i] == '\\' && !escaped {
					escaped = true
				} else {
					escaped = false
				}
				if i == len(value)-1 {
					return nil
				}
			}
			continue
		}
		end := strings.IndexAny(value, " \t")
		if end == -1 {
			fields = append(fields, value)
			break
		}
		fields = append(fields, value[:end])
		value = value[end:]
	}
	return fields
}

func parseMarkerPath(value string) string {
	if tab := strings.IndexByte(value, '\t'); tab >= 0 {
		value = value[:tab]
	}
	return trimGitPrefix(decodePath(value))
}

func decodePath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, `"`) {
		if decoded, err := strconv.Unquote(path); err == nil {
			return decoded
		}
	}
	return path
}

func trimGitPrefix(path string) string {
	if path == "/dev/null" {
		return path
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

func displayPath(oldPath, newPath string) string {
	if newPath != "" && newPath != "/dev/null" {
		return newPath
	}
	return oldPath
}

func (d Diff) Select(fileIndex, hunkIndex int) (Diff, error) {
	if fileIndex < 0 || hunkIndex < 0 {
		return Diff{}, fmt.Errorf("file and hunk indexes cannot be negative")
	}
	if fileIndex == 0 {
		if hunkIndex != 0 {
			return Diff{}, fmt.Errorf("--hunk requires --file")
		}
		return d, nil
	}
	if fileIndex > len(d.Files) {
		return Diff{}, fmt.Errorf("file index %d is out of range; diff has %d files", fileIndex, len(d.Files))
	}
	file := d.Files[fileIndex-1]
	if hunkIndex > 0 {
		if hunkIndex > len(file.Hunks) {
			return Diff{}, fmt.Errorf("hunk index %d is out of range; file %d has %d hunks", hunkIndex, fileIndex, len(file.Hunks))
		}
		file.Hunks = []Hunk{file.Hunks[hunkIndex-1]}
	}
	return Diff{Files: []File{file}}, nil
}

func (d Diff) Search(query string) (Diff, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Diff{}, fmt.Errorf("search query cannot be blank")
	}
	query = strings.ToLower(query)
	result := Diff{Files: []File{}}
	for _, file := range d.Files {
		matched := File{
			Path: file.Path, OldPath: file.OldPath, NewPath: file.NewPath,
			Change: file.Change, Binary: file.Binary, Hunks: []Hunk{},
		}
		for _, hunk := range file.Hunks {
			found := false
			for _, line := range hunk.Lines {
				if line.Kind != ContextLine && strings.Contains(strings.ToLower(line.Content), query) {
					found = true
					break
				}
			}
			if found {
				matched.Hunks = append(matched.Hunks, hunk)
			}
		}
		if len(matched.Hunks) > 0 {
			result.Files = append(result.Files, matched)
		}
	}
	return result, nil
}
