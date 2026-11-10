# Task-home run wrapper for Hermes (ADR-027). Pinned against hermes-base
# v2026.6.19. It runs one turn, resuming the session recorded in the home when
# there is one, and is the only writer of the result frame on stdout.
import json, os, re, sqlite3, stat, subprocess, sys, tempfile

MARK = "\x1eclaw-run-result\n"
SID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")
home = os.environ["CLAW_RUN_HOME"]
limit = int(os.environ["CLAW_RUN_MAX_RESULT_BYTES"])
hh, ws, ctl = home + "/hermes", home + "/workspace", home + "/.claw-run"
db_path = hh + "/state.db"
session_file, question_file = ctl + "/session", ctl + "/question.md"


def die(code, msg, reason, runner_exit=None):
    # The message is for a person reading the container log. The supervisor
    # reads only the frame: a fixed reason code and, when the runner itself
    # failed, its exit code. No output, session id or path goes in the frame.
    sys.stderr.write("claw-run: " + msg + "\n")
    header = {"outcome": "failed", "reason": reason}
    if runner_exit is not None:
        header["runner_exit"] = runner_exit
    sys.stdout.buffer.write(MARK.encode() + json.dumps(header, separators=(",", ":")).encode() + b"\n")
    sys.stdout.buffer.flush()
    sys.exit(code)


def real_dir(path):
    try:
        os.mkdir(path, 0o700)
    except FileExistsError:
        pass
    if not stat.S_ISDIR(os.lstat(path).st_mode):
        die(71, path + " is not a directory", "home_invalid")


def read_small(path, cap):
    # Never follow a link or block on a FIFO a previous turn left behind.
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError:
        return None
    try:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            return None
        return os.read(fd, cap + 1)
    finally:
        os.close(fd)


def replace_file(path, data):
    tmp = path + ".claw-tmp"
    try:
        os.unlink(tmp)
    except FileNotFoundError:
        pass
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        os.write(fd, data)
        os.fsync(fd)
    finally:
        os.close(fd)
    os.replace(tmp, path)


def remove(path):
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass
    except OSError:
        die(71, path + " cannot be cleared", "home_invalid")


def last_message_id():
    if not os.path.exists(db_path):
        return 0
    db = sqlite3.connect("file:" + db_path + "?mode=ro", uri=True)
    try:
        return db.execute("SELECT COALESCE(MAX(id), 0) FROM messages").fetchone()[0]
    except sqlite3.Error:
        return 0
    finally:
        db.close()


def stored_answer(sid, after):
    # The turn is proven stored only by an assistant reply appended by this
    # run under the reported session, which after a compression is the
    # continuation session.
    db = sqlite3.connect("file:" + db_path + "?mode=ro", uri=True)
    try:
        row = db.execute(
            "SELECT content FROM messages WHERE session_id = ? AND role = 'assistant' AND id > ?"
            " AND content IS NOT NULL AND content != '' ORDER BY id DESC LIMIT 1",
            (sid, after),
        ).fetchone()
    finally:
        db.close()
    return row[0] if row else None


MAX_HOPS, MAX_CANDIDATES = 32, 64


def continuation_tip(db, start):
    # Follow compression continuations from a session to the live end of its
    # conversation. The edge is the runner's own definition: the parent ended
    # with reason "compression" and the child started at or after that. A
    # delegated or branched child also names a parent, and is not followed.
    # Two continuations of one session, or a chain past the bound (which is
    # also how a cycle ends), prove nothing.
    cur = start
    for _ in range(MAX_HOPS):
        try:
            if db.execute("SELECT 1 FROM sessions WHERE id = ?", (cur,)).fetchone() is None:
                return None
            rows = db.execute(
                "SELECT c.id FROM sessions c JOIN sessions p ON p.id = c.parent_session_id"
                " WHERE p.id = ? AND p.end_reason = 'compression' AND c.started_at >= p.ended_at LIMIT 2",
                (cur,),
            ).fetchall()
        except sqlite3.Error:
            return None  # missing ancestry cannot prove a continuation or its absence
        if not rows:
            return cur
        if len(rows) > 1 or not isinstance(rows[0][0], str) or not SID.match(rows[0][0]):
            return None
        cur = rows[0][0]
    return None


def proven_session(prior, after):
    # Where this run's turn must be, read from the store and not from the
    # runner's output. On a resume it is the prior session or its
    # continuation. On a first run it is the one session this run created
    # that has no parent, or its continuation. Anything else is unproven:
    # there is no guessing by newest row.
    db = sqlite3.connect("file:" + db_path + "?mode=ro", uri=True)
    try:
        if prior:
            return continuation_tip(db, prior)
        rows = db.execute(
            "SELECT DISTINCT session_id FROM messages WHERE id > ? LIMIT ?", (after, MAX_CANDIDATES + 1)
        ).fetchall()
        if len(rows) > MAX_CANDIDATES:
            return None
        roots = []
        for (sid,) in rows:
            if not isinstance(sid, str) or not SID.match(sid):
                return None
            if db.execute("SELECT 1 FROM messages WHERE session_id = ? AND id <= ? LIMIT 1", (sid, after)).fetchone():
                continue  # existed before this run
            parent = db.execute("SELECT parent_session_id FROM sessions WHERE id = ?", (sid,)).fetchone()
            if parent is None:
                return None
            if parent[0] is None:
                roots.append(sid)
        if len(roots) != 1:
            return None
        return continuation_tip(db, roots[0])
    except sqlite3.Error:
        return None
    finally:
        db.close()


def publish_deliverables():
    # The supervisor reads deliverables as another user. Everything else in
    # the home stays private under the 077 umask; this one directory and the
    # regular files directly in it are made readable. Each is opened without
    # following a link and changed through its own descriptor, so a link or
    # a FIFO placed here is never touched.
    try:
        dfd = os.open(home + "/deliverables", os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    except OSError:
        return
    try:
        try:
            os.fchmod(dfd, 0o755)
        except OSError:
            pass  # not ours to change: the supervisor made it, already open
        for name in os.listdir(dfd)[:4096]:
            try:
                fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=dfd)
            except OSError:
                continue
            try:
                if stat.S_ISREG(os.fstat(fd).st_mode):
                    os.fchmod(fd, 0o644)
            except OSError:
                pass
            finally:
                os.close(fd)
    finally:
        os.close(dfd)


os.umask(0o077)
if len(sys.argv) != 2 or not sys.argv[1].strip():
    die(64, "expected the task as the only argument", "usage")
task = sys.argv[1]
for d in (hh, ws, ctl):
    real_dir(d)
# Files a killed or older turn left unreadable are published now.
publish_deliverables()

# Managed config and contract are regenerated every run. The run token never
# touches the home: it reaches Hermes only through that process's environment.
replace_file(hh + "/config.yaml", os.environ["CLAW_RUN_CONFIG"].encode())
replace_file(hh + "/SOUL.md", os.environ["CLAW_RUN_SOUL"].encode())
replace_file(ws + "/AGENTS.md", os.environ["CLAW_RUN_AGENTS"].encode())
remove(hh + "/.env")
# A question left by a killed run is not this run's question.
remove(question_file)

env = {k: v for k, v in os.environ.items() if not k.startswith("CLAW_RUN_")}
env["CLAW_MODEL_KEY"] = os.environ["CLAW_RUN_TOKEN"]

cmd = ["hermes", "chat", "-Q", "--toolsets", os.environ["CLAW_RUN_TOOLSETS"], "-q", task]
raw = read_small(session_file, 256)
prior = raw.decode("ascii", "replace").strip() if raw else ""
if prior:
    if not SID.match(prior):
        die(71, "session locator is malformed", "session_locator_malformed")
    cmd += ["--resume", prior]

before = last_message_id()
with tempfile.TemporaryDirectory(prefix="claw-run-", dir="/tmp") as capture:
    with open(capture + "/out", "wb") as out, open(capture + "/err", "wb") as err:
        code = subprocess.run(cmd, cwd=ws, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=err).returncode
    with open(capture + "/err", "rb") as f:
        f.seek(0, 2)
        f.seek(max(0, f.tell() - 65536))
        tail = f.read().decode("utf-8", "replace")
ids = re.findall(r"^session_id:\s*(\S+)\s*$", tail, re.M)
# Whatever the turn's outcome, what it left for the requester is published.
publish_deliverables()
if code != 0:
    sys.stderr.write(tail[-4000:])
    die(code if 0 < code < 64 else 1, "hermes exited %d" % code, "runner_exit", code)
if last_message_id() <= before:
    die(70, "hermes stored nothing for this turn", "turn_not_stored")
sid = proven_session(prior, before)
if sid is None:
    die(70, "the store does not prove which session this turn belongs to", "session_unproven")
# The runner's own report is a cross-check. It can be missing: a background
# thread in the runner may have redirected the process's stderr at the moment
# the line was printed. When present it must agree with the store.
if ids and SID.match(ids[-1]):
    if ids[-1] != sid:
        die(70, "hermes reported a session the store does not prove", "session_unproven")
else:
    sys.stderr.write("claw-run: hermes reported no session id; using the session the store proves\n")
answer = stored_answer(sid, before)
if answer is None:
    die(70, "the turn was not stored under its session", "turn_not_stored")
replace_file(session_file, sid.encode())

outcome, body = "answer", answer.encode("utf-8", "replace")
question = read_small(question_file, limit)
if question and question.strip():
    outcome, body = "question", question
    remove(question_file)
truncated = len(body) > limit
if truncated:
    body = body[:limit].decode("utf-8", "ignore").encode()
header = json.dumps({"outcome": outcome, "session": sid, "truncated": truncated}, separators=(",", ":"))
sys.stdout.buffer.write(MARK.encode() + header.encode() + b"\n" + body + b"\n")
sys.stdout.buffer.flush()
