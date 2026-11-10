# Task-home run wrapper for Hermes (ADR-027). Pinned against hermes-base
# v2026.6.19. It runs one turn, resuming the session recorded in the home when
# there is one, and is the only writer of the result frame on stdout.
import json, os, re, sqlite3, stat, subprocess, sys

MARK = "\x1eclaw-run-result\n"
SID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")
home = os.environ["CLAW_RUN_HOME"]
limit = int(os.environ["CLAW_RUN_MAX_RESULT_BYTES"])
hh, ws, ctl = home + "/hermes", home + "/workspace", home + "/.claw-run"
db_path = hh + "/state.db"
session_file, question_file = ctl + "/session", ctl + "/question.md"


def die(code, msg):
    sys.stderr.write("claw-run: " + msg + "\n")
    sys.exit(code)


def real_dir(path):
    try:
        os.mkdir(path, 0o700)
    except FileExistsError:
        pass
    if not stat.S_ISDIR(os.lstat(path).st_mode):
        die(71, path + " is not a directory")


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
        die(71, path + " cannot be cleared")


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


os.umask(0o077)
if len(sys.argv) != 2 or not sys.argv[1].strip():
    die(64, "expected the task as the only argument")
task = sys.argv[1]
for d in (hh, ws, ctl):
    real_dir(d)

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
        die(71, "session locator is malformed")
    cmd += ["--resume", prior]

before = last_message_id()
os.makedirs("/tmp/claw-run", exist_ok=True)
with open("/tmp/claw-run/out", "wb") as out, open("/tmp/claw-run/err", "wb") as err:
    code = subprocess.run(cmd, cwd=ws, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=err).returncode

with open("/tmp/claw-run/err", "rb") as f:
    f.seek(0, 2)
    f.seek(max(0, f.tell() - 65536))
    tail = f.read().decode("utf-8", "replace")
ids = re.findall(r"^session_id:\s*(\S+)\s*$", tail, re.M)
if code != 0:
    sys.stderr.write(tail[-4000:])
    die(code if 0 < code < 64 else 1, "hermes exited %d" % code)
if not ids or not SID.match(ids[-1]):
    die(70, "hermes reported no session id")
sid = ids[-1]
answer = stored_answer(sid, before)
if answer is None:
    die(70, "the turn was not stored under session " + sid)
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
