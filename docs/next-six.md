# Operations continuation

## 1. Audit investigation and retention

`rcdo audit read` uses the configured audit path. `--input` selects another JSONL
file or gzip archive. Filters: `--user`, `--command`, `--since`, `--until`, `--exit`,
and `--unfinished`. Times filter invocation start time. `--show-output` includes
redacted captured output; JSON retains record fields. Missing finishes mean
unfinished or still running, never successful. Omitted results and unfinished
displayed invocations return 30. The reader excludes its own latest start record.

```
rcdo audit read --command log-read --exit 2 --show-output
rcdo audit rotate --input audit.jsonl --output archive-2026-09.gz
rcdo audit rotate --input audit.jsonl --output archive-2026-09.gz --apply
rcdo audit prune --input archive-2026-09.gz --older-than 720h --apply
```

Rotation and pruning preview unless `--apply` is explicit. Rotation uses the
writer's append lock, archives complete start/finish pairs and retains active
runs. Existing archives are never overwritten. If replacing the active log fails,
both copies remain for recovery. Pruning only accepts an explicitly named gzip
archive whose runs are complete and all records exceed the age threshold.
No automatic deletion or wildcard directory cleanup occurs. Reading/rotation is
bounded to 64 MiB decoded input; individual records to 4 MiB. Retention source
fingerprinting uses the existing 16 MiB file limit. Concurrent noncooperating
writers and external filesystem changes are outside transactional guarantees.

## 2. Live announcements

```
rcdo monitor start --input app.log --state monitor.json
rcdo monitor follow --state monitor.json --duration 10m
rcdo monitor pause --state monitor.json
rcdo monitor resume --state monitor.json
rcdo monitor poll --state monitor.json --accept-gap
```

The monitor polls an explicitly selected append-only local file. Use `--syntax
jsonl` for normalized log/event objects or `--syntax ansible` for the supplied
callback plugin. An external producer writes the file; rcdo launches no playbook,
daemon or event collector. Pause suppresses announcements while a running follower
continues bounded collection. Resume drains pending announcements in batches.
`--max-queue` bounds pending events and explicitly counts dropped announcements.
Incomplete lines wait for a newline. Content-prefix changes, truncation and an
observed missing source produce a gap requiring explicit acknowledgement. A gap
restart may duplicate events; a file replaced with an identical prefix cannot be
distinguished. This is an announcement feed, not host-coverage or rollout success
verification: use `ansible-watch` for the latter.

Input is limited to 16 MiB and individual lines to 64 KiB. Compressed files remain
supported by the finite `log-read` reader, not live monitoring. `--format jsonl`
emits per-poll timestamp, messages, pause state, queue count, drops and gaps. Text
uses ordinary new lines, without terminal redraws. Ctrl-C ends a follower; polling
is otherwise bounded by `--duration` (maximum 24 hours). Concurrent state changes
are guarded; if a conflicting writer wins, restart the follower from saved state.
