# Convert documents to Markdown

`rcdo to-markdown` converts DOCX, PDF, CSV and XLSX locally. It writes Markdown
to stdout by default, so it works with pipes, or publishes a new file with
`--output`. Existing files (including symlinks) are never overwritten.

```sh
rcdo to-markdown --input report.docx --output report.md
rcdo to-markdown --input report.pdf --output report.md
rcdo to-markdown --input inventory.csv
rcdo to-markdown --input budget.xlsx --sheet Budget --output budget.md
rcdo to-markdown --input budget.xlsx --table-mode records
cat inventory.csv | rcdo to-markdown --from csv --header=false
rcdo to-markdown --input inventory.csv --delimiter ';'
```

Use `--table-mode records` for a linear, screen-reader-friendly list of labelled
fields. Each label includes its column number, keeping duplicate headings
unambiguous. `--table-mode table` (default) generates GitHub-flavored Markdown
tables. The first populated row is treated as headers unless `--header=false`.
Empty headings receive generated column names. Ragged CSV rows are padded, and
multiline cells use `<br>` in the generated Markdown.

## Supported conversions

| Source | Local converter | Preserved content and limits |
| --- | --- | --- |
| DOCX | Pandoc | Headings, paragraphs, lists, basic tables and inline formatting. Embedded images are not extracted; complex layouts and tracked changes need source review. |
| PDF | Poppler `pdftotext` | Extractable text grouped by page. Does not perform OCR or reconstruct semantic tables/headings. Reading order depends on the PDF text layer. |
| CSV | Built-in Go reader | UTF-8/BOM, quoted fields, multiline fields, custom delimiter, headers and row values. |
| XLSX | Built-in ZIP/XML reader | Worksheet order/names, shared/inline rich text, stored cell values, Boolean cells and sparse column positions. |

DOCX requires `pandoc` on PATH; PDF requires `pdftotext` from Poppler. Missing
converters, unreadable/encrypted input or failed extraction return exit 30 with
an explanation on stderr. CSV/XLSX need no additional packages or AI service.
No conversion invokes macros, evaluates spreadsheet formulas or fetches external
workbook links. DOCX uses Pandoc's sandbox and no filters.

XLSX exports **stored values**, not formatted display strings: date serials,
currency/percentage formats, charts, images, comments and merged-cell layouts
are not reproduced. ISO date cells remain ISO strings. Formula results come from
the workbook cache and may be stale. Missing cached results produce an explicit
placeholder and exit 30. Hidden sheets, rows and columns are included; hidden
sheet names are labelled. Completely empty row gaps are omitted. Record numbers
count exported rows rather than original worksheet row numbers.

PDFs without extractable text return exit 30 and suggest OCR. A page without
extracted text may be blank or scanned; it is reported with its source page number
and makes the conversion incomplete. Text available from other pages is retained.
Some mixed image/text documents can still contain undetected image-only content;
verify the source when fidelity matters. Extracted text and cell content are
escaped as Markdown/HTML text. DOCX formatting is produced by Pandoc.

This is document conversion, not secret redaction: source document values are
preserved. Conversion notes go to stderr and do not contaminate Markdown stdout.

## Output, limits and exit codes

- `0`: conversion finished within the documented support limits.
- `2`: invalid input/options, unsupported structure, limits exceeded or output failure.
- `30`: conversion unavailable or partial (such as missing cached formula values).

Partial Markdown, when available, is written even with exit 30; always check the
exit code. A converter failure or parse failure publishes no Markdown file. Each
`--output` file is written privately, then published atomically without overwriting
an existing path; the destination filesystem must support hard links. It retains
private file permissions. Shell redirection follows ordinary shell overwrite rules,
so use `--output` when you want RCDO's overwrite protection.

Input is capped at 32 MiB; Office archives at 64 MiB expanded and 10000 entries;
individual archive entries at 32 MiB; tables at 200000 cells and 16384 columns.
Markdown output is capped at 32 MiB. Unsafe/duplicate ZIP paths, missing worksheet
parts, invalid shared-string indexes and inconsistent cell coordinates are rejected.
External converters have a one-minute process deadline and bounded output.
Temporary copies are removed after conversion. Filename-based detection supports
`.docx`, `.pdf`, `.csv` and `.xlsx`; stdin requires an explicit `--from`.

## Format references

- [WordprocessingML structure](https://learn.microsoft.com/en-us/office/open-xml/word/structure-of-a-wordprocessingml-document)
- [Spreadsheet cell values](https://learn.microsoft.com/en-us/office/open-xml/spreadsheet/working-with-sheets)
- [Cached spreadsheet formulas](https://learn.microsoft.com/en-us/office/open-xml/spreadsheet/working-with-formulas)
