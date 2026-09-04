# Shared Finding Format and Accessible Renderer

The first release establishes one finding format that every future checker can emit.

## Acceptance criteria

1. A finding records a stable ID, severity, title, resource, action, environment, reason, evidence, confidence, and remediation.
2. Validation rejects missing required fields and unknown severity or confidence values.
3. Text output starts with an overall result and count summary.
4. Text output orders findings by severity while preserving input order within a severity.
5. Text output labels every field and communicates severity without depending on color or layout.
6. A report with unavailable checks is labeled `INCOMPLETE`, even if it has no blocking findings.
7. Critical or high-severity findings make a complete report `BLOCKED`; lower severities produce `REVIEW`, and no findings produce `CLEAN`.
8. JSON output uses the same model, includes a schema version and summary, and is deterministic.
9. Renderers reject invalid reports instead of presenting them as complete.
