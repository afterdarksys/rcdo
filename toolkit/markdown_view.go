package toolkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"git-tools/finding"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type markdownField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type markdownBlock struct {
	Kind   string          `json:"kind"`
	Text   string          `json:"text"`
	Fields []markdownField `json:"fields,omitempty"`
}
type markdownSection struct {
	Number int             `json:"number"`
	Level  int             `json:"level"`
	Title  string          `json:"title"`
	Blocks []markdownBlock `json:"blocks"`
}
type markdownDocument struct {
	SchemaVersion string            `json:"schema_version"`
	SourceSHA256  string            `json:"source_sha256"`
	Sections      []markdownSection `json:"sections"`
}
type markdownState struct {
	SchemaVersion string         `json:"schema_version"`
	Source        string         `json:"source"`
	SourceSHA256  string         `json:"source_sha256"`
	Section       int            `json:"section"`
	Bookmarks     map[string]int `json:"bookmarks"`
}

func markdownSafe(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, s)
}
func markdownInline(n ast.Node, source []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.Text:
			value := v.Value(source)
			if !v.IsRaw() {
				value = util.UnescapePunctuations(value)
				b.WriteString(html.UnescapeString(string(value)))
			} else {
				b.Write(value)
			}
			if v.HardLineBreak() {
				b.WriteString("\n")
			} else if v.SoftLineBreak() {
				b.WriteByte(' ')
			}
			return
		case *ast.String:
			b.Write(v.Value)
			return
		case *ast.AutoLink:
			b.Write(v.Label(source))
			if !bytes.Equal(v.Label(source), v.URL(source)) {
				fmt.Fprintf(&b, " (link: %s)", v.URL(source))
			}
			return
		case *ast.CodeSpan:
			b.WriteByte('`')
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				switch t := c.(type) {
				case *ast.Text:
					b.Write(t.Value(source))
				default:
					b.Write(c.Text(source))
				}
			}
			b.WriteByte('`')
			return
		case *ast.RawHTML:
			var raw strings.Builder
			for i := 0; i < v.Segments.Len(); i++ {
				segment := v.Segments.At(i)
				raw.Write(segment.Value(source))
			}
			if oneOf(strings.ToLower(strings.TrimSpace(raw.String())), "<br>", "<br/>", "<br />") {
				b.WriteString("\n")
			} else {
				b.WriteString("[HTML: " + raw.String() + "]")
			}
			return
		case *extast.TaskCheckBox:
			if v.IsChecked {
				b.WriteString("Task completed: ")
			} else {
				b.WriteString("Task not completed: ")
			}
			return
		case *extast.Strikethrough:
			b.WriteString("Deleted text: ")
		case *ast.Image:
			b.WriteString("Image: ")
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
		switch v := n.(type) {
		case *ast.Link:
			fmt.Fprintf(&b, " (link: %s)", v.Destination)
		case *ast.Image:
			fmt.Fprintf(&b, " (source: %s)", v.Destination)
		}
	}
	walk(n)
	return markdownSafe(b.String())
}
func parseMarkdown(source []byte) (markdownDocument, error) {
	doc := markdownDocument{SchemaVersion: "1", SourceSHA256: digestBytes(source), Sections: []markdownSection{}}
	if !utf8.Valid(source) {
		return doc, fmt.Errorf("Markdown input must be UTF-8")
	}
	source = bytes.ReplaceAll(bytes.TrimPrefix(source, []byte{0xef, 0xbb, 0xbf}), []byte("\r\n"), []byte("\n"))
	root := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(source))
	nodes, depth := 0, 0
	err := ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			nodes++
			depth++
		} else {
			depth--
		}
		if depth > 128 || nodes > 100000 {
			return ast.WalkStop, fmt.Errorf("Markdown exceeds navigation complexity limit")
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return doc, err
	}
	addSection := func(title string, level int) {
		doc.Sections = append(doc.Sections, markdownSection{Number: len(doc.Sections) + 1, Level: level, Title: title, Blocks: []markdownBlock{}})
	}
	add := func(kind, value string, fields []markdownField) {
		doc.Sections[len(doc.Sections)-1].Blocks = append(doc.Sections[len(doc.Sections)-1].Blocks, markdownBlock{Kind: kind, Text: markdownSafe(value), Fields: fields})
	}
	codeText := func(n ast.Node) string {
		var b strings.Builder
		for i := 0; i < n.Lines().Len(); i++ {
			segment := n.Lines().At(i)
			b.Write(segment.Value(source))
		}
		return strings.TrimSuffix(markdownSafe(b.String()), "\n")
	}
	var blocks func(ast.Node)
	blocks = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.Paragraph, *ast.TextBlock:
			add("paragraph", markdownInline(n, source), nil)
			return
		case *ast.Heading:
			add("heading", fmt.Sprintf("Heading level %d: %s", v.Level, markdownInline(v, source)), nil)
			return
		case *ast.FencedCodeBlock:
			add("code-start", "Code block: "+markdownSafe(string(v.Language(source))), nil)
			add("code", codeText(v), nil)
			add("code-end", "End code block.", nil)
			return
		case *ast.CodeBlock:
			add("code-start", "Code block:", nil)
			add("code", codeText(v), nil)
			add("code-end", "End code block.", nil)
			return
		case *ast.HTMLBlock:
			add("html", "HTML source (not rendered):\n"+codeText(v), nil)
			if v.HasClosure() {
				add("html", string(v.ClosureLine.Value(source)), nil)
			}
			return
		case *ast.ThematicBreak:
			add("separator", "Section break.", nil)
			return
		case *ast.Blockquote:
			add("quote-start", "Quote:", nil)
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				blocks(c)
			}
			add("quote-end", "End quote.", nil)
			return
		case *ast.List:
			index := v.Start
			add("list-start", "List:", nil)
			for item := n.FirstChild(); item != nil; item = item.NextSibling() {
				label := "Item:"
				if v.IsOrdered() {
					label = fmt.Sprintf("Item %d:", index)
					index++
				}
				add("list-item", label, nil)
				for c := item.FirstChild(); c != nil; c = c.NextSibling() {
					blocks(c)
				}
			}
			add("list-end", "End list.", nil)
			return
		case *extast.Table:
			headers := []string{}
			rowNumber := 0
			for row := n.FirstChild(); row != nil; row = row.NextSibling() {
				values := []string{}
				for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
					values = append(values, markdownInline(cell, source))
				}
				if _, ok := row.(*extast.TableHeader); ok {
					headers = values
					labels := []markdownField{}
					for i, label := range headers {
						labels = append(labels, markdownField{Label: fmt.Sprintf("Column %d", i+1), Value: label})
					}
					add("table-header", "Table columns:", labels)
					continue
				}
				rowNumber++
				fields := []markdownField{}
				for i, value := range values {
					label := fmt.Sprintf("Column %d", i+1)
					if i < len(headers) && headers[i] != "" {
						label = fmt.Sprintf("%s (column %d)", headers[i], i+1)
					}
					fields = append(fields, markdownField{Label: label, Value: value})
				}
				add("table-row", fmt.Sprintf("Row %d:", rowNumber), fields)
			}
			add("table-end", "End table.", nil)
			return
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			blocks(c)
		}
	}
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok {
			addSection(markdownInline(h, source), h.Level)
			continue
		}
		if len(doc.Sections) == 0 {
			addSection("Introduction", 0)
		}
		blocks(n)
	}
	if len(doc.Sections) == 0 {
		addSection("Empty document", 0)
	}
	return doc, nil
}
func readMarkdownInput(input string, stdin io.Reader) ([]byte, error) {
	var r io.Reader = stdin
	if input != "-" {
		f, err := os.Open(input)
		if err != nil {
			return nil, fmt.Errorf("cannot open Markdown source")
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Markdown source must be a regular file")
		}
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, 4<<20+1))
	if err != nil || len(b) > 4<<20 {
		return nil, fmt.Errorf("Markdown exceeds 4 MiB or cannot be read")
	}
	return b, nil
}
func runMarkdownView(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	mode := "read"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	if !oneOf(mode, "read", "start", "show", "next", "previous", "goto", "bookmark") {
		return fmt.Errorf("unknown viewer command; use read, start, show, next, previous, goto or bookmark")
	}
	fs := flag.NewFlagSet("markdown-view "+mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "-", "Markdown file or stdin (read mode)")
	statePath := fs.String("state", ".rcdo-markdown-view.json", "persistent navigation state")
	format := fs.String("format", "text", "text or json")
	width := fs.Int("width", 72, "prose width, minimum 40; code retains original line lengths")
	toc := fs.Bool("toc", false, "show section titles only")
	query := fs.String("find", "", "case-insensitive literal search across sections")
	section := fs.Int("section", 0, "one-based section number; required by goto unless using a bookmark")
	name := fs.String("name", "", "bookmark name for bookmark or goto")
	setAccessibleUsage(fs, "markdown-view "+mode, stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *width < 40 || !oneOf(*format, "text", "json") || *section < 0 {
		return fmt.Errorf("invalid arguments, width, section or format")
	}
	if *name != "" && !oneOf(mode, "bookmark", "goto") {
		return fmt.Errorf("--name applies only to bookmark or goto")
	}
	if mode != "read" && (*toc || *query != "") {
		return fmt.Errorf("--toc and --find apply to read mode")
	}
	if !oneOf(mode, "read", "start", "goto") && *section != 0 {
		return fmt.Errorf("--section applies to read, start or goto")
	}
	var s markdownState
	var stored []byte
	var err error
	notice := ""
	if mode != "read" {
		if mode == "start" {
			if *input == "-" {
				return fmt.Errorf("start requires --input with a file")
			}
			absolute, err := filepath.Abs(*input)
			if err != nil {
				return err
			}
			s = markdownState{SchemaVersion: "1", Source: absolute, Section: 1, Bookmarks: map[string]int{}}
		} else {
			if *input != "-" {
				return fmt.Errorf("saved navigation reads its bound source; do not pass --input")
			}
			stored, err = readConfigSource(*statePath)
			if err != nil {
				return err
			}
			if strictJSON(stored, &s) != nil || s.SchemaVersion != "1" || !filepath.IsAbs(s.Source) || s.SourceSHA256 == "" || s.Section < 1 {
				return fmt.Errorf("invalid Markdown navigation state")
			}
			if s.Bookmarks == nil {
				s.Bookmarks = map[string]int{}
			}
		}
		absolute, err := filepath.Abs(*statePath)
		if err != nil {
			return err
		}
		sourceInfo, sourceErr := os.Stat(s.Source)
		stateInfo, stateErr := os.Stat(absolute)
		if absolute == s.Source || sourceErr == nil && stateErr == nil && os.SameFile(sourceInfo, stateInfo) {
			return fmt.Errorf("navigation state must be separate from Markdown source")
		}
		*input = s.Source
	}
	data, err := readMarkdownInput(*input, stdin)
	if err != nil {
		return err
	}
	doc, err := parseMarkdown(data)
	if err != nil {
		return err
	}
	if mode != "read" {
		if mode != "start" && s.SourceSHA256 != doc.SourceSHA256 {
			fmt.Fprintln(stderr, "Markdown source changed; saved section positions were not used. Read the current file or start a new navigation state.")
			return reportError{status: finding.StatusIncomplete}
		}
		if s.Section > len(doc.Sections) {
			return fmt.Errorf("invalid saved section")
		}
		for key, v := range s.Bookmarks {
			if !operationLabel(key) || v < 1 || v > len(doc.Sections) {
				return fmt.Errorf("invalid saved bookmark")
			}
		}
		s.SourceSHA256 = doc.SourceSHA256
		switch mode {
		case "start":
			if *section > 0 {
				s.Section = *section
			}
		case "next":
			if s.Section < len(doc.Sections) {
				s.Section++
			} else {
				notice = "End of document."
			}
		case "previous":
			if s.Section > 1 {
				s.Section--
			} else {
				notice = "Beginning of document."
			}
		case "goto":
			if (*section == 0) == (*name == "") {
				return fmt.Errorf("goto requires exactly one of --section or --name")
			}
			if *name != "" {
				n, ok := s.Bookmarks[*name]
				if !ok {
					return fmt.Errorf("bookmark not found")
				}
				s.Section = n
			} else {
				s.Section = *section
			}
		case "bookmark":
			if !operationLabel(*name) {
				return fmt.Errorf("bookmark requires a nonempty single-line --name")
			}
			if _, exists := s.Bookmarks[*name]; exists {
				return fmt.Errorf("bookmark already exists; choose another name")
			}
			s.Bookmarks[*name] = s.Section
			notice = "Bookmark saved: " + *name
		}
		if s.Section < 1 || s.Section > len(doc.Sections) {
			return fmt.Errorf("section not found")
		}
		if mode != "show" {
			encoded, err := json.MarshalIndent(s, "", "  ")
			if err != nil {
				return err
			}
			encoded = append(encoded, '\n')
			check := func() error {
				current, err := readMarkdownInput(s.Source, nil)
				if err != nil || digestBytes(current) != s.SourceSHA256 {
					return fmt.Errorf("source changed while navigating")
				}
				if mode != "start" {
					current, err := readConfigSource(*statePath)
					if err != nil || !bytes.Equal(current, stored) {
						return fmt.Errorf("navigation state changed concurrently")
					}
				}
				return nil
			}
			if err = check(); err != nil {
				return err
			}
			if mode == "start" {
				err = publishMarkdown(*statePath, encoded)
			} else {
				err = atomicReplaceChecked(*statePath, encoded, io.Discard, check)
			}
			if err != nil {
				return err
			}
		}
		*section = s.Section
	}
	total := len(doc.Sections)
	selected := []markdownSection{}
	for _, s := range doc.Sections {
		if *section > 0 && s.Number != *section {
			continue
		}
		if *query != "" {
			var searchable strings.Builder
			searchable.WriteString(s.Title)
			for _, block := range s.Blocks {
				searchable.WriteString("\n" + block.Text)
				for _, field := range block.Fields {
					searchable.WriteString("\n" + field.Label + " " + field.Value)
				}
			}
			if !strings.Contains(strings.ToLower(searchable.String()), strings.ToLower(*query)) {
				continue
			}
		}
		if *toc {
			s.Blocks = nil
		}
		selected = append(selected, s)
	}
	if *section > total {
		return fmt.Errorf("section not found")
	}
	doc.Sections = selected
	if *format == "json" {
		return json.NewEncoder(stdout).Encode(struct {
			markdownDocument
			Total  int    `json:"total_sections"`
			Notice string `json:"notice,omitempty"`
		}{doc, total, markdownSafe(notice)})
	}
	if notice != "" {
		writeWrapped(stdout, markdownSafe(notice), *width)
	}
	if len(selected) == 0 {
		fmt.Fprintln(stdout, "No matching sections.")
		return nil
	}
	for _, s := range selected {
		writeWrapped(stdout, fmt.Sprintf("Section %d of %d. Heading level %d: %s", s.Number, total, s.Level, s.Title), *width)
		if *toc {
			continue
		}
		fmt.Fprintln(stdout)
		for _, b := range s.Blocks {
			if b.Kind == "code" {
				fmt.Fprintln(stdout, b.Text)
			} else {
				for _, line := range strings.Split(b.Text, "\n") {
					writeWrapped(stdout, line, *width)
				}
			}
			for _, f := range b.Fields {
				writeWrapped(stdout, f.Label+": "+strings.ReplaceAll(f.Value, "\n", "; "), *width)
			}
			if oneOf(b.Kind, "paragraph", "code-end", "table-row", "table-end", "quote-end", "list-end", "html") {
				fmt.Fprintln(stdout)
			}
		}
	}
	return nil
}
