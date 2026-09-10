"""Opt-in, payload-free RCDO JSONL callback. Never executes tasks itself."""
import datetime
import json
import os
import uuid
from ansible import context
from ansible.plugins.callback import CallbackBase

DOCUMENTATION = r'''
name: rcdo
type: aggregate
short_description: Write payload-free RCDO task receipts
version_added: "1.0"
description:
  - Set RCDO_ANSIBLE_EVENTS to a new file in an existing directory.
  - Result bodies, variables, module arguments and loop items are never recorded.
requirements:
  - Explicitly enable rcdo in callbacks_enabled.
'''

class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'aggregate'
    CALLBACK_NAME = 'rcdo'
    CALLBACK_NEEDS_ENABLED = True

    def __init__(self):
        super().__init__()
        self._fd = None
        self._sequence = 0
        self._run = str(uuid.uuid4())
        self._check = False
        self._broken = False

    def _record(self, event, **fields):
        if self._fd is None or self._broken:
            return
        record = dict(schema_version='1', event=event, run_id=self._run,
                      sequence=self._sequence,
                      at=datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00', 'Z'))
        record.update(fields)
        data = (json.dumps(record, separators=(',', ':')) + '\n').encode()
        # Any recording failure suppresses all later records, including finish.
        try:
            if len(data) > 65535 or os.write(self._fd, data) != len(data):
                raise OSError('RCDO callback record could not be persisted')
            os.fsync(self._fd)
            self._sequence += 1
        except Exception:
            self._broken = True
            raise

    def v2_playbook_on_start(self, playbook):
        path = os.environ.get('RCDO_ANSIBLE_EVENTS')
        if not path:
            return
        self._fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        self._check = bool(context.CLIARGS.get('check', False))
        self._record('start', check_mode=self._check)

    def _result(self, result, outcome, ignored=False):
        try:
            task = result._task
            no_log = bool(getattr(task, 'no_log', False) or result._result.get('_ansible_no_log', False))
            check = getattr(task, 'check_mode', None)
            self._record('result', host=result._host.get_name(), task_id=str(task._uuid),
                         task='[withheld]' if no_log else task.get_name(),
                         outcome=outcome, check_mode=self._check if check is None else bool(check),
                         no_log=no_log, ignored=bool(ignored))
        except Exception:
            self._broken = True
            raise

    def v2_runner_on_ok(self, result):
        self._result(result, 'changed' if result._result.get('changed', False) else 'ok')

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._result(result, 'failed', ignore_errors)

    def v2_runner_on_unreachable(self, result):
        self._result(result, 'unreachable')

    def v2_runner_on_skipped(self, result):
        self._result(result, 'skipped')

    def v2_playbook_on_stats(self, stats):
        self._record('finish')
        if self._fd is not None:
            os.close(self._fd)
            self._fd = None
