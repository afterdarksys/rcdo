package toolkit

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func officeFixture(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range sortedStringValues(parts) {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(parts[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func xlsxFixture(t *testing.T, sheet string) []byte {
	return officeFixture(t, map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Budget" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>Name</t></si><si><r><t>R&amp;</t></r><r><t>D</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml":   sheet,
	})
}
func TestMarkdownCSV(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
		want        string
	}{
		{"escaping", "name,value\nservice,\"a|b <script>\"\n", nil, `| service | a\|b &lt;script&gt; |`},
		{"records", "name,value\nservice,4\n", []string{"--table-mode", "records"}, "- value (column 2): 4"},
		{"no header", "alice,4\n", []string{"--header=false"}, "| Column 1 | Column 2 |"},
		{"multiline", "name,value\nservice,\"line one\nline two\"\n", nil, "line one<br>line two"},
		{"delimiter", "name;value\nservice;4\n", []string{"--delimiter", ";"}, "| service | 4 |"},
		{"BOM", "\ufeffname,value\nservice,4\n", nil, "| name | value |"},
		{"ragged", "name\nservice,4\n", nil, "| name | Column 2 |"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--from", "csv"}, tc.args...)
			code, out, err := execute("to-markdown", args, tc.input)
			if code != 0 || !strings.Contains(out, tc.want) || err != "" {
				t.Fatalf("%d %q %s", code, out, err)
			}
		})
	}
}
func TestMarkdownOutputNoOverwrite(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "result.md")
	code, out, err := execute("to-markdown", []string{"--from", "csv", "--output", p}, "a,b\n1,2\n")
	if code != 0 || out != "" || err != "" {
		t.Fatalf("%d %s %s", code, out, err)
	}
	b, e := os.ReadFile(p)
	if e != nil || !strings.Contains(string(b), "| 1 | 2 |") {
		t.Fatal(string(b), e)
	}
	code, _, _ = execute("to-markdown", []string{"--from", "csv", "--output", p}, "changed\n")
	if code != 2 {
		t.Fatal(code)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(b, after) {
		t.Fatal("overwrote output")
	}
	missing := filepath.Join(d, "missing.md")
	code, _, _ = execute("to-markdown", []string{"--from", "csv", "--output", missing}, "a,\"broken")
	if code != 2 {
		t.Fatal(code)
	}
	if _, e = os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("published failed conversion")
	}
	link := filepath.Join(d, "link.md")
	if e = os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	code, _, _ = execute("to-markdown", []string{"--from", "csv", "--output", link}, "changed\n")
	if code != 2 {
		t.Fatal("followed symlink")
	}
}
func TestMarkdownWorkbook(t *testing.T) {
	data := xlsxFixture(t, `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="inlineStr"><is><t>Total</t></is></c></row><row r="2"><c r="A2" t="s"><v>1</v></c><c r="C2"><f>1+1</f><v>2</v></c></row></sheetData></worksheet>`)
	code, out, err := execute("to-markdown", []string{"--from", "xlsx"}, string(data))
	if code != 0 || !strings.Contains(out, "## Budget") || !strings.Contains(out, "| R&amp;D |  | 2 |") || !strings.Contains(err, "cached") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("to-markdown", []string{"--from", "xlsx", "--sheet", "Budget", "--table-mode", "records"}, string(data))
	if code != 0 || !strings.Contains(out, "Total (column 3): 2") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, _, _ = execute("to-markdown", []string{"--from", "xlsx", "--sheet", "missing"}, string(data))
	if code != 2 {
		t.Fatal(code)
	}
}
func TestMarkdownWorkbookMissingFormulaAndInvalidCells(t *testing.T) {
	data := xlsxFixture(t, `<worksheet><sheetData><row r="1"><c r="A1"><f>1+1</f></c></row></sheetData></worksheet>`)
	code, out, err := execute("to-markdown", []string{"--from", "xlsx", "--header=false"}, string(data))
	if code != 30 || !strings.Contains(out, "formula result unavailable") || !strings.Contains(err, "incomplete") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	for _, cell := range []string{`<c r="A1" t="s"><v>9999</v></c>`, `<c r="XFE1"><v>1</v></c>`, `<c r="A2"><v>1</v></c>`, `<c r="A1"><v>1</v></c><c r="A1"><v>2</v></c>`} {
		data = xlsxFixture(t, `<worksheet><sheetData><row r="1">`+cell+`</row></sheetData></worksheet>`)
		code, out, err = execute("to-markdown", []string{"--from", "xlsx"}, string(data))
		if code != 2 || out != "" {
			t.Fatalf("%d %s %s", code, out, err)
		}
	}
}
func TestMarkdownOfficeArchivePaths(t *testing.T) {
	for _, name := range []string{"../outside.xml", "/absolute.xml", "xl/../../escape.xml"} {
		data := officeFixture(t, map[string]string{name: "<x/>"})
		if _, err := openOfficeParts(data); err == nil {
			t.Fatal(name)
		}
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 2; i++ {
		w, _ := z.Create("xl/workbook.xml")
		fmt.Fprint(w, "<x/>")
	}
	z.Close()
	if _, err := openOfficeParts(b.Bytes()); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestMarkdownExternalConverters(t *testing.T) {
	original := executeReadOnly
	defer func() { executeReadOnly = original }()
	executeReadOnly = func(name string, args ...string) commandResult {
		if name == "pandoc" {
			if args[0] != "--sandbox" || !strings.Contains(strings.Join(args, " "), "--to=gfm") {
				t.Fatal(args)
			}
			return commandResult{stdout: []byte("# Heading\n\nText\n")}
		}
		if name != "pdftotext" || args[0] != "-enc" {
			t.Fatal(name, args)
		}
		return commandResult{stdout: []byte("First page <text>\fSecond page\f")}
	}
	docx := officeFixture(t, map[string]string{"word/document.xml": "<document/>"})
	code, out, err := execute("to-markdown", []string{"--from", "docx"}, string(docx))
	if code != 0 || !strings.Contains(out, "# Heading") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	code, out, err = execute("to-markdown", []string{"--from", "pdf"}, "%PDF-fixture")
	if code != 0 || !strings.Contains(out, "## Page 2") || !strings.Contains(out, "&lt;text&gt;") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	executeReadOnly = func(string, ...string) commandResult { return commandResult{stdout: []byte("\f")} }
	code, out, err = execute("to-markdown", []string{"--from", "pdf"}, "%PDF-fixture")
	if code != 30 || out != "" || !strings.Contains(err, "OCR") {
		t.Fatalf("%d %s %s", code, out, err)
	}
	executeReadOnly = func(string, ...string) commandResult {
		return commandResult{err: os.ErrNotExist, stderr: "private document text"}
	}
	code, out, err = execute("to-markdown", []string{"--from", "docx"}, string(docx))
	if code != 30 || strings.Contains(out+err, "private document text") {
		t.Fatalf("%d %s %s", code, out, err)
	}
}
