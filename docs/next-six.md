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
