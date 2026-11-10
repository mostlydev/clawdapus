import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest


FAKE = r'''
import os, pathlib, sqlite3, sys
home=pathlib.Path(os.environ['HOME']); hh=pathlib.Path(os.environ['HERMES_HOME'])
task=sys.argv[sys.argv.index('-q')+1]
sid='session-1'
resumed=sys.argv[sys.argv.index('--resume')+1] if '--resume' in sys.argv else ''
sid=resumed or sid
db=sqlite3.connect(hh/'state.db')
db.execute('CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT)')
has_sessions=True
if has_sessions:
    db.execute('CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, parent_session_id TEXT, end_reason TEXT, started_at REAL, ended_at REAL)')
    db.execute('INSERT OR IGNORE INTO sessions VALUES(?,NULL,NULL,1,NULL)',(sid,))
def child(new, parent, started=3):
    if has_sessions: db.execute('INSERT OR IGNORE INTO sessions VALUES(?,?,NULL,?,NULL)',(new,parent,started))
def end(old, reason, at=2):
    if has_sessions: db.execute('UPDATE sessions SET end_reason=?, ended_at=? WHERE id=?',(reason,at,old))
def say(where, text):
    db.execute('INSERT INTO messages(session_id,role,content) VALUES(?,?,?)',(where,'assistant',text))
if 'continued' in task:
    # A compression moved the conversation to a continuation session.
    # The turn's earlier messages were flushed to the old session first.
    db.execute('INSERT INTO messages(session_id,role,content) VALUES(?,?,?)',(sid,'user',task))
    end(sid,'compression'); child('session-2',sid); sid='session-2'
if 'twice' in task:
    # A broken store: two continuations of one session.
    child('session-3','session-1')
if 'loop' in task:
    # A broken store: the continuation chain is a cycle.
    end('session-2','compression'); db.execute("UPDATE sessions SET parent_session_id='session-2', started_at=3 WHERE id='session-1'")
if 'foreign' in task:
    # A reply lands in a session unrelated to this home's conversation.
    sid='session-foreign'; child(sid,None)
    if has_sessions: db.execute("UPDATE sessions SET parent_session_id=NULL WHERE id=?",(sid,))
if 'subagent' in task:
    # A delegated child replies, before and after the main session's turn.
    child('session-child',sid); say('session-child','child-answer')
if 'branch' in task:
    # A branched child of an ended session is not a continuation.
    end(sid,'branched'); child('session-branch',sid); say('session-branch','branch-answer')
if 'second-root' in task:
    child('session-other',None); say('session-other','other-answer')
if 'liar' in task:
    # The runner reports a session other than the one the store proves.
    child('session-liar',None); say('session-liar','liar-answer')
db.execute('INSERT INTO messages(session_id,role,content) VALUES(?,?,?)',(sid,'user',task))
if task!='user-only':
    answer='é'*100 if task=='large' else 'stored-answer'
    db.execute('INSERT INTO messages(session_id,role,content) VALUES(?,?,?)',(sid,'assistant',answer))
if 'subagent' in task: say('session-child','child-late')
if 'missing-table' in task: db.execute('DROP TABLE sessions')
if 'missing-row' in task: db.execute('DELETE FROM sessions')
if 'missing-column' in task: db.execute('ALTER TABLE sessions RENAME COLUMN ended_at TO incompatible')
db.commit(); db.close()
question=home/'.claw-run/question.md'
if task=='question': question.write_text('Which branch?')
if task=='question-link': question.symlink_to(os.environ['OUTSIDE'])
if task=='question-fifo': os.mkfifo(question)
print('arbitrary stdout noise, not the answer')
if 'liar' in task:
    print('session_id: session-liar',file=sys.stderr)
elif 'silent' not in task:
    print('session_id: '+sid,file=sys.stderr)
if 'badhint' in task:
    print('session_id: ../../x',file=sys.stderr)
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

    def failure(self, result, code, reason, runner_exit=None):
        """A failed run carries one frame: a fixed reason and nothing else."""
        self.assertEqual(result.returncode, code, result.stderr)
        want = {'outcome': 'failed', 'reason': reason}
        if runner_exit is not None:
            want['runner_exit'] = runner_exit
        self.assertEqual(result.stdout, b'\x1eclaw-run-result\n' + json.dumps(want, separators=(',', ':')).encode() + b'\n')

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
                if mode == 'failed':
                    self.failure(result, 2, 'runner_exit', 2)
                else:
                    self.failure(result, 70, 'turn_not_stored')

    def test_old_assistant_row_cannot_prove_the_new_turn(self):
        with tempfile.TemporaryDirectory() as home:
            self.framed(self.invoke(home, 'answer'))
            self.failure(self.invoke(home, 'user-only'), 70, 'turn_not_stored')

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
            self.failure(self.invoke(home, 'answer'), 71, 'session_locator_malformed')
            self.assertFalse(Path(home, 'hermes', 'state.db').exists())

    def session(self, home):
        return Path(home, '.claw-run', 'session').read_text()

    def test_lost_session_line_is_recovered_from_the_store(self):
        # The runner can lose its stderr line to a background thread's
        # redirect. The store still proves the session.
        for first, second, want in (
            ('silent', 'silent', 'session-1'),
            ('sessions', 'silent sessions', 'session-1'),
            ('sessions', 'silent sessions continued', 'session-2'),
            ('silent sessions subagent', 'silent sessions subagent', 'session-1'),
            ('silent sessions subagent continued', 'silent sessions', 'session-2'),
            ('badhint silent', 'badhint silent', 'session-1'),
            ('sessions', 'silent sessions branch', 'session-1'),
        ):
            with self.subTest(first=first, second=second), tempfile.TemporaryDirectory() as home:
                header, body = self.framed(self.invoke(home, first))
                self.assertEqual(body, b'stored-answer')
                header, body = self.framed(self.invoke(home, second))
                self.assertEqual((header['session'], body), (want, b'stored-answer'))
                self.assertEqual(self.session(home), want)

    def test_store_must_prove_the_session(self):
        # (first run or None, run under test, reason)
        for first, task, reason in (
            # Resume: the reply went to a session that is not this conversation.
            ('sessions', 'silent sessions foreign', 'turn_not_stored'),
            ('sessions', 'sessions foreign', 'session_unproven'),
            # Resume: the runner names a session the store does not prove.
            ('sessions', 'sessions liar', 'session_unproven'),
            # Resume: a store that cannot name one continuation.
            ('sessions', 'silent sessions continued twice', 'session_unproven'),
            ('sessions', 'silent sessions continued loop', 'session_unproven'),
            # Resume with no session records: another session is not a continuation.
            ('answer', 'silent missing-table', 'session_unproven'),
            # First run: two new sessions without a parent.
            (None, 'silent sessions second-root', 'session_unproven'),
            (None, 'sessions second-root', 'session_unproven'),
            (None, 'sessions liar', 'session_unproven'),
        ):
            with self.subTest(first=first, task=task), tempfile.TemporaryDirectory() as home:
                if first:
                    self.framed(self.invoke(home, first))
                self.failure(self.invoke(home, task), 70, reason)
                if first:
                    self.assertEqual(self.session(home), 'session-1')
                else:
                    self.assertFalse(Path(home, '.claw-run', 'session').exists())

    def test_missing_ancestry_is_not_session_proof(self):
        for first in (None, 'answer'):
            for damage in ('missing-table', 'missing-row', 'missing-column'):
                with self.subTest(first=first, damage=damage), tempfile.TemporaryDirectory() as home:
                    if first:
                        self.framed(self.invoke(home, first))
                    self.failure(self.invoke(home, 'silent ' + damage), 70, 'session_unproven')
                    if first:
                        self.assertEqual(self.session(home), 'session-1')
                    else:
                        self.assertFalse(Path(home, '.claw-run', 'session').exists())

    def test_first_run_cannot_adopt_a_session_that_already_existed(self):
        with tempfile.TemporaryDirectory() as home:
            store = Path(home, 'hermes')
            store.mkdir()
            db = sqlite3.connect(store/'state.db')
            db.execute('CREATE TABLE messages(id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT)')
            db.execute("INSERT INTO messages(session_id,role,content) VALUES('session-1','assistant','older')")
            db.commit(); db.close()
            self.failure(self.invoke(home, 'silent'), 70, 'session_unproven')

    def test_failure_frame_carries_no_runner_output(self):
        with tempfile.TemporaryDirectory() as home:
            result = self.invoke(home, 'failed')
            self.assertNotIn(b'noise', result.stdout)
            self.assertNotIn(b'session-1', result.stdout)
            self.assertNotIn(home.encode(), result.stdout)

    def test_usage_failure_is_framed(self):
        with tempfile.TemporaryDirectory() as home:
            self.failure(self.invoke(home, ' '), 64, 'usage')


if __name__ == '__main__':
    unittest.main()
