# Git Diff Walker MVP

The first usable command turns a unified Git diff into a linear review that does not depend on color or visual alignment.

## Acceptance criteria

1. Read a unified diff from standard input or an explicitly named file.
2. Summarize file count, hunk count, additions, and deletions before presenting details.
3. Label each file, hunk, added line, removed line, and context line in plain text.
4. Preserve old and new line numbers so a reviewer can locate every change.
5. Identify added, deleted, modified, renamed, and binary files.
6. Select one file and optionally one hunk using one-based indexes.
7. Search changed lines case-insensitively and retain only matching hunks.
8. Emit a stable, structured JSON representation when requested.
9. Reject malformed hunk headers, malformed hunk bodies, invalid selections, unknown formats, and empty input with actionable errors.
10. Never invoke Git or modify the repository; input is read-only and explicit.
