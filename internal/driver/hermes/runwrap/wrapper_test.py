import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


FAKE = r'''
import os, pathlib, sqlite3, sys
home=pathlib.Path(os.environ['HOME']); hh=pathlib.Path(os.environ['HERMES_HOME'])
task=sys.argv[sys.argv.index('-q')+1]
sid='session-1'
db=sqlite3.connect(hh/'state.db')
db.execute('CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT)')
db.execute('INSERT INTO messages(session_id,role,content) VALUES(?,?,?)',(sid,'user',task))
if task!='user-only':
    answer='é'*100 if task=='large' else 'stored-answer'
    db.execute('INSERT INTO messages(session_id,role,content) VALUES(?,?,?)',(sid,'assistant',answer))
db.commit(); db.close()
question=home/'.claw-run/question.md'
if task=='question': question.write_text('Which branch?')
if task=='question-link': question.symlink_to(os.environ['OUTSIDE'])
if task=='question-fifo': os.mkfifo(question)
print('arbitrary stdout noise, not the answer')
print('session_id: '+sid,file=sys.stderr)
sys.exit(2 if task=='failed' else 0)
'''


class WrapperTests(unittest.TestCase):
    def invoke(self, home, task, limit=4096):
        root = Path(home)
        bindir = root / 'bin'
        bindir.mkdir(exist_ok=True)
        fake = bindir / 'hermes'
        fake.write_text('#!' + sys.executable + '\n' + FAKE)
        fake.chmod(0o755)
        outside = root / 'outside'
        outside.write_text('PRIVATE-OUTSIDE-MARKER')
        env = dict(os.environ, PATH=str(bindir)+os.pathsep+os.environ['PATH'],
                   HOME=home, HERMES_HOME=str(root/'hermes'), OUTSIDE=str(outside),
                   CLAW_RUN_HOME=home, CLAW_RUN_MAX_RESULT_BYTES=str(limit),
                   CLAW_RUN_CONFIG='model: {}', CLAW_RUN_SOUL='identity',
                   CLAW_RUN_AGENTS='contract', CLAW_RUN_TOKEN='fake-run-secret',
                   CLAW_RUN_TOOLSETS='terminal,file', PYTHONDONTWRITEBYTECODE='1')
        return subprocess.run([sys.executable, str(Path(__file__).with_name('wrapper.py')), task],
                              env=env, capture_output=True, timeout=5)

    def framed(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(result.stdout.startswith(b'\x1eclaw-run-result\n'))
        header, body = result.stdout.split(b'\n', 2)[1:]
        return json.loads(header), body.rstrip(b'\n')

    def test_stored_answer_and_result_bound(self):
        with tempfile.TemporaryDirectory() as home:
            header, body = self.framed(self.invoke(home, 'large', limit=31))
            self.assertEqual(header['outcome'], 'answer')
            self.assertTrue(header['truncated'])
            self.assertLessEqual(len(body), 31)
            body.decode('utf-8')
            self.assertNotIn(b'noise', body)

    def test_failed_or_user_only_turn_has_no_result(self):
        for mode in ('failed', 'user-only'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as home:
                result = self.invoke(home, mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn(b'\x1eclaw-run-result', result.stdout)

    def test_old_assistant_row_cannot_prove_the_new_turn(self):
        with tempfile.TemporaryDirectory() as home:
            self.framed(self.invoke(home, 'answer'))
            result = self.invoke(home, 'user-only')
            self.assertEqual(result.returncode, 70)
            self.assertNotIn(b'\x1eclaw-run-result', result.stdout)

    def test_question_is_consumed_and_stale_question_cleared(self):
        with tempfile.TemporaryDirectory() as home:
            header, body = self.framed(self.invoke(home, 'question'))
            self.assertEqual(header['outcome'], 'question')
            self.assertEqual(body, b'Which branch?')
            question = Path(home, '.claw-run', 'question.md')
            self.assertFalse(question.exists())
            question.write_text('stale from a killed turn')
            header, body = self.framed(self.invoke(home, 'answer'))
            self.assertEqual(header['outcome'], 'answer')
            self.assertEqual(body, b'stored-answer')

    def test_question_symlink_and_fifo_never_read(self):
        for mode in ('question-link', 'question-fifo'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as home:
                header, body = self.framed(self.invoke(home, mode))
                self.assertEqual(header['outcome'], 'answer')
                self.assertEqual(body, b'stored-answer')

    def test_malformed_session_locator_fails(self):
        with tempfile.TemporaryDirectory() as home:
            control = Path(home, '.claw-run')
            control.mkdir()
            (control/'session').write_text('../../foreign')
            result = self.invoke(home, 'answer')
            self.assertEqual(result.returncode, 71)
            self.assertFalse(Path(home, 'hermes', 'state.db').exists())


if __name__ == '__main__':
    unittest.main()
