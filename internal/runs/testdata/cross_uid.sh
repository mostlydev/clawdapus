#!/bin/sh
# Cross-user proof for task-home deliverables, run entirely inside one Linux
# container so no bind mount hides file ownership:
#   supervisor user (uid 1000) lays out homes,
#   worker user (uid 65534, umask 077) writes deliverables, once through the
#   real wrapper and twice without it,
#   supervisor user reads them with the supervisor's own code.
# Usage: internal/runs/testdata/cross_uid.sh   (from the repository root)
set -eu
arch=$(docker version --format '{{.Server.Arch}}')
work=$(mktemp -d)
name="claw-cross-uid-$$"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -o "$work/runs.test" ./internal/runs
cp internal/driver/hermes/runwrap/wrapper.py "$work/wrapper.py"
cat > "$work/hermes" <<'PY'
#!/usr/bin/env python3
# A stand-in runner: stores a reply, reports its session, leaves a deliverable.
import os, pathlib, sqlite3, sys
home = pathlib.Path(os.environ["HOME"])
db = sqlite3.connect(home / "hermes" / "state.db")
db.execute("CREATE TABLE sessions(id TEXT PRIMARY KEY, parent_session_id TEXT, end_reason TEXT, started_at REAL, ended_at REAL)")
db.execute("CREATE TABLE messages(id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT)")
db.execute("INSERT INTO sessions VALUES('session-1',NULL,NULL,1,NULL)")
db.execute("INSERT INTO messages(session_id,role,content) VALUES('session-1','assistant','done')")
db.commit()
d = home / "deliverables"
d.mkdir(exist_ok=True)
(d / "report.md").write_text("# Report\n")
print("session_id: session-1", file=sys.stderr)
PY
chmod 755 "$work/hermes"
docker run -d --name "$name" python:3.12-alpine sleep 600 >/dev/null
docker exec "$name" mkdir -p /pod /tool
docker cp "$work/runs.test" "$name:/tool/runs.test"
docker cp "$work/wrapper.py" "$name:/tool/wrapper.py"
docker cp "$work/hermes" "$name:/tool/hermes"
docker exec "$name" sh -c 'chown 1000:1000 /pod && chmod 755 /tool/runs.test /tool/hermes'
homes=$(docker exec -u 1000:1000 -e CLAW_DELIVERABLES_PREPARE=/pod "$name" /tool/runs.test -test.run '^TestDeliverablesCrossUserPrepare$' | grep '^HOME ')
path_of() { echo "$homes" | awk -v h="$1" '$2 == h {print $3}'; }
# The worker's user must be able to reach the homes: the supervisor's 0700
# parents keep other host users out, and Docker mounts only the data
# directory into a worker. Model that mount by letting the worker's user
# traverse to it here, and only here.
docker exec "$name" sh -c 'chmod 711 /pod/.claw-homes /pod/.claw-homes/*'
# 1. Through the real wrapper: private umask, then publication.
docker exec -u 65534:65534 -e HOME="$(path_of published)" -e HERMES_HOME="$(path_of published)/hermes" \
  -e CLAW_RUN_HOME="$(path_of published)" -e CLAW_RUN_MAX_RESULT_BYTES=4096 -e CLAW_RUN_CONFIG='model: {}' \
  -e CLAW_RUN_SOUL=identity -e CLAW_RUN_AGENTS=contract -e CLAW_RUN_TOKEN=fake -e CLAW_RUN_TOOLSETS=terminal \
  -e PATH=/tool:/usr/local/bin:/usr/bin:/bin "$name" python3 /tool/wrapper.py task >/dev/null
# 2. Without the wrapper's publication, as a turn killed before it would.
docker exec -u 65534:65534 "$name" sh -c "umask 077; printf '# Report\n' > '$(path_of denied-file)/deliverables/report.md'"
docker exec -u 65534:65534 "$name" sh -c "umask 077; mkdir '$(path_of denied-dir)/deliverables'; printf '# Report\n' > '$(path_of denied-dir)/deliverables/report.md'"
docker exec "$name" sh -c 'chmod 700 /pod/.claw-homes /pod/.claw-homes/*'
echo "--- modes as the worker left them"
docker exec "$name" sh -c 'cd /pod/.claw-homes && for d in */home/deliverables; do stat -c "%u %a %n" "$d" "$d"/*; done'
echo "--- supervisor user reads them"
docker exec -u 1000:1000 -e CLAW_DELIVERABLES_FIXTURE=/pod "$name" /tool/runs.test -test.v -test.run '^TestDeliverablesCrossUserFixture$' | grep -E '^(=== RUN|--- |PASS|FAIL|ok|    deliverables_test)'
