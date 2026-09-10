# Markdown viewer

`rcdo markdown-view` reads Markdown directly in the terminal without a browser,
external pager or ANSI styling. It uses the Go Goldmark parser with GitHub-flavored
Markdown extensions. No external runtime or network access is required.

```sh
rcdo markdown-view --input README --width 72
rcdo markdown-view --input runbook.md --toc
rcdo markdown-view --input runbook.md --section 3
rcdo markdown-view --input runbook.md --find rollback
rcdo markdown-view --input runbook.md --format json
rcdo to-markdown --input inventory.csv | rcdo markdown-view
```

Headings become numbered sections. Text before the first top-level heading is an
Introduction section. ATX (`#`) and Setext headings are supported. Each section ends
at the next top-level heading, including a heading of a different level. Heading
level is announced; section numbers distinguish repeated titles. Nested headings
inside lists or quotes are read in their enclosing section.

Tables become rows with explicit column labels. Task lists announce completed or
not completed; ordinary lists and quotes have beginning/end markers. Link labels,
URLs, image alt text and image paths are readable, but nothing is fetched or
opened. Inline formatting is simplified for plain-text reading. Raw HTML is shown
as labelled source, never rendered; inline `<br>` becomes a line break. Code blocks
keep their indentation and original line lengths, so code is not altered by prose
wrapping. Terminal escape/control characters and invisible format controls are
removed or replaced throughout displayed content, including code.

`--width` wraps prose (minimum 40, default 72). `--toc` shows titles without body
content. `--find` selects sections containing a case-insensitive literal match in
titles, text or table fields; combine it with `--toc` for a compact result list.
JSON output contains schema version, exact-source SHA-256, total section count,
selected sections and typed blocks/fields. This is reading, not secret redaction:
document text is preserved apart from presentation and terminal controls.

## Persistent reading

```sh
rcdo markdown-view start --input runbook.md --state reading.json
rcdo markdown-view next --state reading.json
rcdo markdown-view previous --state reading.json
rcdo markdown-view show --state reading.json
rcdo markdown-view bookmark --state reading.json --name rollback
rcdo markdown-view goto --state reading.json --name rollback
rcdo markdown-view goto --state reading.json --section 4
```

Start accepts an optional `--section`. Navigation shows one section per invocation,
with explicit beginning/end announcements. It does not take over the terminal or
capture keys; shell history and ordinary screen-reader commands remain available.
Reading and bookmarks never approve operations or execute code found in documents.

State stores the absolute source path, SHA-256 of exact file bytes, current section
and named bookmarks. It contains no copy of the document. Start refuses to overwrite
existing state, and duplicate bookmark names require a new name. Source/state
aliases are rejected. Updates use the existing guarded atomic writer and recheck
source/state before replacing the navigation file.

A changed source returns exit 30 without using stale positions or rewriting state.
Read the current file with `--input`, then start a new state file to establish new
positions. No automatic heading remapping is attempted. JSON numbers and field
structure are stable within a document version; numbers are not permanent IDs
across edits.

Input must be UTF-8 and at most 4 MiB. The viewer accepts files or stdin in read
mode; persistent reading requires a regular file. Parsed navigation is limited to
100000 nodes and nesting depth 128. Empty documents are labelled explicitly.
Exit 0 means reading succeeded (including no search matches); 2 means invalid input,
arguments or state; 30 means the saved document version is stale.

This is a terminal text viewer. HTML/web presentation, full-screen keyboard UI,
image descriptions/OCR and automatic speech are not included. Actual screen-reader
and braille-device usability still needs operator testing.
