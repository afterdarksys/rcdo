# Operator accessibility pilot worksheet

Status: not run. Automated tests and synthetic practice do not establish device
compatibility. Use nonproduction data and record actual observations below.

## Setup

- Date and RCDO commit:
- Operating system, terminal and versions:
- Screen reader, magnifier or braille display/software and versions:
- Font size, zoom, terminal width, report layout:
- Optional CLIs and their versions:
- Practice artifact directory and results.json:

## Tasks

1. Run `rcdo doctor --sample`, identify the severity, target and next action.
2. Read the synthetic wrong-account report; state the expected and observed account.
3. Find the repeated error count, save a log bookmark and return to it.
4. Resume an incident; identify the next action and any changed evidence.
5. Follow the API dependency and identify missing region coverage.
6. Find the unreachable host and differing service field in the fleet report.
7. Read one finding in each report layout; spell its exact resource identifier.
8. Attempt runbook advancement after completion without verification; explain the block.
9. Read the failed health check, then the passing synthetic verification artifact.
10. Read the stale plan review and explain why the old review cannot establish current safety.

For each task record completion, elapsed time if useful, navigation obstacles,
missed or ambiguous information, assistance required and a concrete improvement.
Measure the workflow, not the operator's visual ability. Do not record credentials.

## Results

No operator results have been recorded. Add findings here only after observation.

## Structured continuation sessions

Use `rcdo pilot start --suite accessibility --operator NAME --setup ACTUAL_SETUP`
to open a structured session. `pilot show` lists the task IDs. Record observed
results with `pilot record --task ID --outcome pass|fail|blocked --notes OBSERVATION
--evidence FILE`. Use a separate `--suite integration` session for nonproduction
Docker, IaC, Ansible inventory, Spacelift and endpoint checks. Preserve the actual
CLI/AT versions and scope in setup notes. Artifact hashes detect changed evidence.
No operator results have been filled in automatically.
