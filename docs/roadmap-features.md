# Accessible operations: the next feature batch

Each feature below has its own implementation commit and regression tests.
Read-only artifact workflows work offline; live collector completeness and actual
screen-reader, braille and magnification acceptance are separate milestones.
All schemas use explicit source identity, timestamps and coverage where applicable.

## LOG: investigate saved logs

```
rcdo log-read --input app.log --query error
rcdo log-read --input events.jsonl --syntax jsonl --request req-42
rcdo log-read --input events.jsonl --syntax jsonl --since 2026-09-10T12:00:00Z
rcdo log-read start --input app.log --query error --state log-reading.json
rcdo log-read next --state log-reading.json --context 2
rcdo log-read bookmark --state log-reading.json --name database
rcdo log-read goto --state log-reading.json --name database
```

Text mode treats each physical line as an event. JSONL accepts one object per line:
`{"timestamp":"2026-09-10T12:00:00Z","level":"error","resource":"api","request_id":"req-42","message":"Connection failed"}`.
Only message is required; escaped newlines retain multiline event content.
Unknown fields/duplicate keys are rejected; normalize provider logs to this schema.
Time bounds require timezone-bearing RFC3339, are inclusive, and report unfilterable
events as incomplete (30). No cross-host clock synchronization is inferred.

Summary groups identical original messages/level/resource/request combinations,
ignoring timestamps. First/last source lines and counts remain explicit; JSON also
lists all occurrence lines. Group display limits announce omissions. Raw artifacts
are never rewritten. Common secrets and terminal controls are filtered in output;
this is not guaranteed detection of arbitrary secrets. Search operates on displayed
message text. Reading persists filters, exact source bytes, cursor and bookmarks.
Changed/rotated evidence returns 30 without changing state. Goto accepts a matched
`--line` or `--name`; show/next/previous include up to 20 adjacent events. Input is
bounded to 8 MiB, 100000 lines and 64 KiB per line. No live tail or heuristic
stack-trace joining occurs. All commands support `--format json` and `--width`.
