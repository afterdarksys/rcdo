"""Contract tests without an Ansible installation or remote hosts."""
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

ansible = types.ModuleType('ansible')
ansible.context = types.SimpleNamespace(CLIARGS={'check': True})
callback = types.ModuleType('ansible.plugins.callback')
callback.CallbackBase = type('CallbackBase', (), {})
sys.modules.update({'ansible': ansible, 'ansible.plugins': types.ModuleType('ansible.plugins'),
                    'ansible.plugins.callback': callback})
spec = importlib.util.spec_from_file_location('rcdo_callback', Path(__file__).parent / 'callback_plugins' / 'rcdo.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class CallbackTests(unittest.TestCase):
    def test_metadata_only_and_sticky_record_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'events.jsonl'
            with patch.dict(os.environ, {'RCDO_ANSIBLE_EVENTS': str(path)}):
                c = module.CallbackModule()
                c.v2_playbook_on_start(None)
                task = types.SimpleNamespace(no_log=True, check_mode=False, _uuid='task-id', get_name=lambda:'secret-name')
                result = types.SimpleNamespace(_task=task, _host=types.SimpleNamespace(get_name=lambda:'host'),
                    _result={'changed':True, 'stdout':'secret-body', 'password':'secret-password'})
                c.v2_runner_on_ok(result)
                with patch.object(module.os, 'write', side_effect=OSError('disk full')):
                    with self.assertRaises(OSError):
                        c.v2_runner_on_failed(result)
                c.v2_playbook_on_stats(None)
            text = path.read_text()
            self.assertNotIn('secret-', text)
            records = [json.loads(line) for line in text.splitlines()]
            self.assertEqual([r['event'] for r in records], ['start', 'result'])
            self.assertFalse(records[1]['check_mode'])
            self.assertTrue(records[1]['no_log'])
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with patch.dict(os.environ, {'RCDO_ANSIBLE_EVENTS': str(path)}):
                with self.assertRaises(FileExistsError):
                    module.CallbackModule().v2_playbook_on_start(None)

if __name__ == '__main__':
    unittest.main()
