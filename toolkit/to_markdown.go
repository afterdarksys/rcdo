package toolkit

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"git-tools/finding"
)

const documentLimit = 32 << 20
const markdownCellLimit = 200000

type markdownOptions struct {
	input, output, from, mode, sheet, delimiter string
	header                                      bool
}

func runToMarkdown(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var o markdownOptions
	fs := flag.NewFlagSet("to-markdown", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.input, "input", "-", "source file or - for stdin")
	fs.StringVar(&o.output, "output", "-", "Markdown destination; existing files are never overwritten")
	fs.StringVar(&o.from, "from", "auto", "auto, docx, pdf, csv or xlsx; required for stdin")
	fs.StringVar(&o.mode, "table-mode", "table", "table or records (linear screen-reader layout)")
	fs.StringVar(&o.sheet, "sheet", "", "exact XLSX worksheet name; default includes all sheets")
	fs.StringVar(&o.delimiter, "delimiter", ",", "CSV field delimiter, one character")
	fs.BoolVar(&o.header, "header", true, "use first CSV/XLSX row as column headings")
	setAccessibleUsage(fs, "to-markdown", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments; use --input")
	}
	if o.from == "auto" {
		o.from = strings.TrimPrefix(strings.ToLower(filepath.Ext(o.input)), ".")
	}
	if !oneOf(o.from, "docx", "pdf", "csv", "xlsx") {
		return fmt.Errorf("--from must be docx, pdf, csv or xlsx; stdin requires --from")
	}
	if !oneOf(o.mode, "table", "records") {
		return fmt.Errorf("--table-mode must be table or records")
	}
	if o.sheet != "" && o.from != "xlsx" {
		return fmt.Errorf("--sheet applies only to XLSX")
	}
	if o.delimiter != "," && o.from != "csv" {
		return fmt.Errorf("--delimiter applies only to CSV")
	}
	if o.output != "-" {
		if _, err := os.Lstat(o.output); err == nil {
			return fmt.Errorf("output already exists; choose a new path")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	var reader io.Reader = stdin
	if o.input != "-" {
		f, err := os.Open(o.input)
		if err != nil {
			return fmt.Errorf("cannot open input")
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("input must be a regular file")
		}
		reader = f
	}
	data, err := io.ReadAll(io.LimitReader(reader, documentLimit+1))
	if err != nil || len(data) > documentLimit {
		return fmt.Errorf("input exceeds 32 MiB or cannot be read")
	}
	if len(data) == 0 {
		return fmt.Errorf("input is empty")
	}
	var converted string
	var notes []string
	partial := false
	switch o.from {
	case "csv":
		if !utf8.Valid(data) {
			return fmt.Errorf("CSV must be UTF-8 encoded")
		}
		sep, n := utf8.DecodeRuneInString(o.delimiter)
		if n != len(o.delimiter) || sep == utf8.RuneError || sep == 0 || sep == '\r' || sep == '\n' || sep == '"' {
			return fmt.Errorf("--delimiter must be one valid non-quote character")
		}
		r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
		r.Comma = sep
		r.FieldsPerRecord = -1
		var rows [][]string
		cells := 0
		for {
			row, e := r.Read()
			if e == io.EOF {
				break
			}
			if e != nil {
				return fmt.Errorf("invalid CSV structure")
			}
			cells += len(row)
			if cells > markdownCellLimit {
				return fmt.Errorf("CSV exceeds 200000 cells")
			}
			rows = append(rows, row)
		}
		converted, err = markdownRows(rows, o)
	case "xlsx":
		converted, notes, partial, err = workbookMarkdown(data, o)
	case "docx", "pdf":
		// Snapshot the bytes so external converters never reopen a changing source,
		// and option-looking filenames cannot become CLI flags.
		tmp, e := os.CreateTemp("", "rcdo-document-*."+o.from)
		if e != nil {
			return e
		}
		name := tmp.Name()
		defer os.Remove(name)
		if _, e = tmp.Write(data); e != nil {
			tmp.Close()
			return e
		}
		if e = tmp.Close(); e != nil {
			return e
		}
		var result commandResult
		if o.from == "docx" {
			if _, e := openOfficeParts(data); e != nil {
				return e
			}
			result = executeReadOnly("pandoc", "--sandbox", "--from=docx", "--to=gfm", "--wrap=none", name)
			notes = append(notes, "DOCX text conversion does not extract embedded images; complex layouts and tracked changes require source review.")
		} else {
			if !bytes.HasPrefix(data, []byte("%PDF-")) {
				return fmt.Errorf("input is not a PDF document")
			}
			result = executeReadOnly("pdftotext", "-enc", "UTF-8", "-eol", "unix", name, "-")
			notes = append(notes, "PDF text extraction does not perform OCR or reconstruct semantic tables/headings; verify reading order and image-only content against the source.")
		}
		if result.err != nil {
			fmt.Fprintf(stderr, "Conversion incomplete: %s requires a working local %s installation and a readable, unencrypted document.\n", o.from, map[string]string{"docx": "pandoc", "pdf": "pdftotext (Poppler)"}[o.from])
			return reportError{status: finding.StatusIncomplete}
		}
		if !utf8.Valid(result.stdout) {
			return fmt.Errorf("converter returned invalid UTF-8")
		}
		converted = string(result.stdout)
		if o.from == "pdf" {
			pages := strings.Split(converted, "\f")
			var text strings.Builder
			for i, p := range pages {
				p = strings.TrimSpace(p)
				if p == "" {
					if i < len(pages)-1 {
						notes = append(notes, fmt.Sprintf("PDF page %d has no extracted text; it may be blank or require OCR.", i+1))
						partial = true
					}
					continue
				}
				fmt.Fprintf(&text, "## Page %d\n\n%s\n\n", i+1, markdownEscape(p))
			}
			converted = text.String()
		}
		if strings.TrimSpace(converted) == "" {
			fmt.Fprintln(stderr, "Conversion incomplete: no text was extracted; image-only documents may require OCR.")
			return reportError{status: finding.StatusIncomplete}
		}
		if strings.TrimSpace(result.stderr) != "" {
			notes = append(notes, "The converter emitted diagnostics; inspect the source for omitted content.")
			partial = true
		}
	}
	if err != nil {
		return err
	}
	if len(converted) > documentLimit {
		return fmt.Errorf("Markdown output exceeds 32 MiB")
	}
	if !strings.HasSuffix(converted, "\n") {
		converted += "\n"
	}
	// Publish only fully generated output. An exclusive hard link atomically
	// publishes the finished file without overwriting any concurrent destination.
	if o.output == "-" {
		_, err = io.WriteString(stdout, converted)
	} else {
		err = publishMarkdown(o.output, []byte(converted))
	}
	if err != nil {
		return err
	}
	for _, note := range uniqueStrings(notes) {
		fmt.Fprintln(stderr, "Conversion note: "+note)
	}
	if partial {
		return reportError{status: finding.StatusIncomplete}
	}
	return nil
}
func publishMarkdown(destination string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".rcdo-markdown-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Link(name, destination); err != nil {
		return fmt.Errorf("cannot publish Markdown without overwriting destination: %w", err)
	}
	return nil
}
func markdownEscape(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "#", "\\#", "|", "\\|", "!", "\\!", "~", "\\~").Replace(s)
}
func markdownRows(rows [][]string, o markdownOptions) (string, error) {
	if len(rows) == 0 {
		return "", fmt.Errorf("no tabular data found")
	}
	columns := 0
	for _, r := range rows {
		if len(r) > columns {
			columns = len(r)
		}
	}
	if columns == 0 || columns > 16384 || len(rows) > markdownCellLimit/columns {
		return "", fmt.Errorf("expanded table exceeds 200000 cells or 16384 columns")
	}
	headers := make([]string, columns)
	for i := range headers {
		headers[i] = fmt.Sprintf("Column %d", i+1)
	}
	start := 0
	if o.header {
		start = 1
		for i, v := range rows[0] {
			if strings.TrimSpace(v) != "" {
				headers[i] = v
			}
		}
	}
	cell := func(v string) string { return strings.ReplaceAll(markdownEscape(v), "\n", "<br>") }
	var b strings.Builder
	if o.mode == "records" {
		if start == len(rows) {
			b.WriteString("Columns: ")
			for i, h := range headers {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(cell(h))
			}
			b.WriteString("\n")
		}
		for i := start; i < len(rows); i++ {
			fmt.Fprintf(&b, "### Record %d\n\n", i-start+1)
			for j, h := range headers {
				v := ""
				if j < len(rows[i]) {
					v = rows[i][j]
				}
				fmt.Fprintf(&b, "- %s (column %d): %s\n", cell(h), j+1, cell(v))
			}
			b.WriteString("\n")
		}
	} else {
		writeRow := func(row []string) {
			b.WriteString("|")
			for j := 0; j < columns; j++ {
				v := ""
				if j < len(row) {
					v = row[j]
				}
				b.WriteString(" " + cell(v) + " |")
			}
			b.WriteString("\n")
		}
		writeRow(headers)
		b.WriteString("|")
		for range headers {
			b.WriteString(" --- |")
		}
		b.WriteString("\n")
		for _, row := range rows[start:] {
			writeRow(row)
		}
	}
	return b.String(), nil
}

type officeParts map[string][]byte

func openOfficeParts(data []byte) (officeParts, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid or encrypted Office ZIP container")
	}
	if len(z.File) > 10000 {
		return nil, fmt.Errorf("Office archive contains too many entries")
	}
	parts := officeParts{}
	total := 0
	for _, file := range z.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := file.Name
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return nil, fmt.Errorf("unsafe Office archive path")
		}
		if _, exists := parts[name]; exists {
			return nil, fmt.Errorf("duplicate Office archive entry")
		}
		if file.UncompressedSize64 > documentLimit || uint64(total)+file.UncompressedSize64 > 64<<20 {
			return nil, fmt.Errorf("Office archive exceeds expanded size limit")
		}
		f, e := file.Open()
		if e != nil {
			return nil, fmt.Errorf("cannot read Office archive entry")
		}
		b, e := io.ReadAll(io.LimitReader(f, documentLimit+1))
		f.Close()
		if e != nil || len(b) > documentLimit {
			return nil, fmt.Errorf("invalid or oversized Office archive entry")
		}
		total += len(b)
		if total > 64<<20 {
			return nil, fmt.Errorf("Office archive exceeds expanded size limit")
		}
		parts[name] = b
	}
	return parts, nil
}

type sheetString struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

func (s sheetString) value() string {
	var b strings.Builder
	b.WriteString(s.Text)
	for _, r := range s.Runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

type sheetCell struct {
	Ref     string      `xml:"r,attr"`
	Type    string      `xml:"t,attr"`
	Style   int         `xml:"s,attr"`
	Value   *string     `xml:"v"`
	Inline  sheetString `xml:"is"`
	Formula *string     `xml:"f"`
}

func workbookMarkdown(data []byte, o markdownOptions) (string, []string, bool, error) {
	parts, err := openOfficeParts(data)
	if err != nil {
		return "", nil, false, err
	}
	var book struct {
		Sheets []struct {
			Name  string `xml:"name,attr"`
			ID    string `xml:"id,attr"`
			State string `xml:"state,attr"`
		} `xml:"sheets>sheet"`
	}
	var relationships struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(parts["xl/workbook.xml"], &book) != nil || xml.Unmarshal(parts["xl/_rels/workbook.xml.rels"], &relationships) != nil || len(book.Sheets) == 0 {
		return "", nil, false, fmt.Errorf("invalid XLSX workbook or relationships")
	}
	targets := map[string]string{}
	for _, r := range relationships.Items {
		if _, ok := targets[r.ID]; ok {
			return "", nil, false, fmt.Errorf("duplicate workbook relationship")
		}
		if r.Mode == "External" {
			continue
		}
		target := path.Clean(path.Join("xl", r.Target))
		if strings.HasPrefix(r.Target, "/") {
			target = path.Clean(strings.TrimPrefix(r.Target, "/"))
		}
		if !strings.HasPrefix(target, "xl/") {
			return "", nil, false, fmt.Errorf("worksheet relationship escapes workbook")
		}
		targets[r.ID] = target
	}
	var shared struct {
		Items []sheetString `xml:"si"`
	}
	if s, ok := parts["xl/sharedStrings.xml"]; ok {
		if xml.Unmarshal(s, &shared) != nil {
			return "", nil, false, fmt.Errorf("invalid shared strings")
		}
	}
	notes := []string{"XLSX exports stored values; numeric/date display formats, charts, images, comments and merged-cell layouts are not reproduced. Hidden rows/columns and sheets are included and formulas are never executed."}
	partial := false
	found := false
	var out strings.Builder
	totalCells := 0
	for _, s := range book.Sheets {
		if o.sheet != "" && s.Name != o.sheet {
			continue
		}
		found = true
		body, ok := parts[targets[s.ID]]
		if !ok {
			return "", nil, false, fmt.Errorf("worksheet part is unavailable")
		}
		var sheet struct {
			Rows []struct {
				Number int         `xml:"r,attr"`
				Cells  []sheetCell `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if xml.Unmarshal(body, &sheet) != nil {
			return "", nil, false, fmt.Errorf("invalid worksheet XML")
		}
		fmt.Fprintf(&out, "## %s\n\n", markdownEscape(s.Name))
		if s.State != "" && s.State != "visible" {
			out.WriteString("Sheet visibility: " + markdownEscape(s.State) + ".\n\n")
		}
		var rows [][]string
		lastRow := 0
		for _, r := range sheet.Rows {
			rowNum := r.Number
			if rowNum == 0 {
				rowNum = lastRow + 1
			}
			if rowNum <= lastRow {
				return "", nil, false, fmt.Errorf("duplicate or unordered worksheet row")
			}
			if rowNum > lastRow+1 {
				notes = append(notes, "Empty worksheet row gaps are omitted; record numbers count exported rows.")
			}
			lastRow = rowNum
			row := []string{}
			previousCol := -1
			for _, c := range r.Cells {
				col := previousCol + 1
				if c.Ref != "" {
					var number int
					col, number, err = cellCoordinates(c.Ref)
					if err != nil || number != rowNum {
						return "", nil, false, fmt.Errorf("invalid or inconsistent cell reference")
					}
				}
				if col <= previousCol || col >= 16384 {
					return "", nil, false, fmt.Errorf("duplicate, unordered or oversized cell reference")
				}
				previousCol = col
				if totalCells+col+1 > markdownCellLimit {
					return "", nil, false, fmt.Errorf("workbook exceeds 200000 cells")
				}
				for len(row) <= col {
					row = append(row, "")
				}
				v := ""
				if c.Value != nil {
					v = *c.Value
				}
				switch c.Type {
				case "s":
					i, e := strconv.Atoi(v)
					if e != nil || i < 0 || i >= len(shared.Items) {
						return "", nil, false, fmt.Errorf("invalid shared string index")
					}
					v = shared.Items[i].value()
				case "inlineStr":
					v = c.Inline.value()
				case "b":
					if v == "1" {
						v = "true"
					} else if v == "0" {
						v = "false"
					} else {
						return "", nil, false, fmt.Errorf("invalid Boolean cell")
					}
				case "", "n", "str", "d", "e":
				default:
					return "", nil, false, fmt.Errorf("unsupported XLSX cell type")
				}
				if c.Formula != nil {
					notes = append(notes, "Formula results are cached workbook values and may be stale; formulas were not recalculated.")
					if c.Value == nil {
						v = "[formula result unavailable]"
						partial = true
					}
				}
				row[col] = v
			}
			totalCells += len(row)
			if len(row) > 0 {
				rows = append(rows, row)
			}
		}
		if len(rows) == 0 {
			out.WriteString("No cell values.\n\n")
			continue
		}
		table, e := markdownRows(rows, o)
		if e != nil {
			return "", nil, false, e
		}
		out.WriteString(table)
		out.WriteString("\n")
		if out.Len() > documentLimit {
			return "", nil, false, fmt.Errorf("Markdown output exceeds 32 MiB")
		}
	}
	if !found {
		return "", nil, false, fmt.Errorf("worksheet not found")
	}
	if partial {
		notes = append(notes, "Some formula cells have no cached results; exported placeholders indicate incomplete conversion.")
	}
	return out.String(), notes, partial, nil
}
func cellCoordinates(ref string) (int, int, error) {
	col := 0
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A'+1)
		i++
		if col > 16384 {
			return 0, 0, fmt.Errorf("column out of range")
		}
	}
	row, err := strconv.Atoi(ref[i:])
	if i == 0 || err != nil || row < 1 || row > 1048576 {
		return 0, 0, fmt.Errorf("invalid cell coordinate")
	}
	return col - 1, row, nil
}
