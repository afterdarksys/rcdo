"""Replay synthetic audit cases without running cloud or Ansible commands.

Usage: python3 docs/reviews/2026-09-11-reproduce.py /path/to/rcdo
Exit 1 means at least one unsafe clean/ready result was reproduced.
"""

import json
import pathlib
import subprocess
import sys
import tempfile


binary = str(pathlib.Path(sys.argv[1]).resolve())
failures = []


def check(name, args, source="", unsafe=True):
    result = subprocess.run(
        ["rcdo", *args, "--format", "json"], executable=binary,
        input=source, text=True, capture_output=True, timeout=30,
    )
    report = json.loads(result.stdout) if result.stdout.strip() else {}
    reproduced = unsafe and result.returncode == 0 and report.get("status") in ("clean", "ready")
    if reproduced:
        failures.append(name)
    print(f"{name}: exit {result.returncode}; status {report.get('status', 'no report')}; "
          f"unsafe success reproduced: {reproduced}")
    if result.stderr:
        print(result.stderr.strip())
    return result.stdout


with tempfile.TemporaryDirectory(prefix="rcdo-review-") as temporary:
    root = pathlib.Path(temporary)
    empty = root / "empty.json"
    empty.write_text("{}")
    check("R1 empty required report", ["deploy-review", "--report", f"terraform={empty}", "--require", "terraform"])
    check("R2 truncated scan", ["ansible-check"], "#" + "x" * (4 * 1024 * 1024) + "\n- hosts: all\n")
    check("R3 control: unquoted hazards", ["ansible-check"],
          "- hosts: all\n  tasks:\n    - shell: echo hello\n      ignore_errors: true\n", unsafe=False)
    check("R3 quoted/commented hazards", ["ansible-check"],
          '- hosts: "all"\n  tasks:\n    - shell: echo hello\n      ignore_errors: true # continue\n')
    clean = check("R4 source staging review", ["ansible-check", "--environment", "staging"],
                  "- hosts: web\n  tasks: []\n", unsafe=False)
    (root / "staging.json").write_text(clean)
    manifest = root / "manifest.json"
    manifest.write_text(json.dumps({
        "schema_version": "1", "change_id": "production-change", "commit": "b" * 40,
        "environment": "production", "required_components": ["ansible"],
        "reports": {"ansible": "staging.json"},
    }))
    check("R4 staging evidence reused for production", ["review-change", "--manifest", str(manifest)])
    check("R5 pending PR checks", ["pr-manager", "inspect"], json.dumps({
        "number": 1, "title": "Synthetic review", "headRefOid": "a" * 40,
        "mergeable": "UNKNOWN", "reviewDecision": "APPROVED",
        "statusCheckRollup": [{"status": "IN_PROGRESS", "conclusion": None}],
    }))
    check("R6 omitted Ansible sections", ["ansible2aws"], """- hosts: localhost
  pre_tasks:
    - ansible.builtin.command: /bin/false
  roles:
    - required_role
  tasks:
    - name: Create VPC
      amazon.aws.ec2_vpc_net:
        name: beta-vpc
        cidr_block: 10.20.0.0/16
  post_tasks:
    - ansible.builtin.command: /bin/false
""")

print(f"Unsafe success cases reproduced: {len(failures)}")
sys.exit(1 if failures else 0)
