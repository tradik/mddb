#!/usr/bin/env bash
#
# test-load-md-folder.sh — tests for scripts/load-md-folder.sh (#372).
#
# The loader runs the real mddb-cli (built from this tree) against a stub
# server that records each /v1/add body, so the test sees what reaches MDDB.
#
# Covers:
#   1. tags: a, b, c               -> tags=[a, b, c] (the #372 case)
#   2. tags: [a, b, c]             -> tags=[a, b, c]
#   3. tags: then "- a" lines      -> tags=[a, b, c]
#   4. the body is stored without the frontmatter block
#   5. -m meta wins over a frontmatter key; source and filename are added
#   6. -s reaches the CLI (the server URL is not only printed)
#   7. a CLI that cannot read frontmatter is refused  -> 1
#   8. a dry run sends nothing
#
# Needs go and python3.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
LOADER="${REPO_ROOT}/scripts/load-md-folder.sh"

RED='\033[0;31m'; GREEN='\033[0;32m'; NC='\033[0m'
PASS=0; FAIL=0

pass() { echo -e "${GREEN}✓${NC} $1"; PASS=$((PASS + 1)); }
fail() { echo -e "${RED}✗${NC} $1"; FAIL=$((FAIL + 1)); }

TMP="$(mktemp -d)"
SERVER_PID=""
cleanup() {
	[[ -n "${SERVER_PID}" ]] && kill "${SERVER_PID}" 2>/dev/null || true
	rm -rf "${TMP}"
}
trap cleanup EXIT

# The CLI under test, built from this tree.
(cd "${REPO_ROOT}/services/mddb-cli" && GOWORK=off go build -o "${TMP}/mddb-cli" .)

# A stub MDDB: every POST /v1/add body is appended to adds.jsonl; every
# request gets a small JSON answer.
cat > "${TMP}/stub.py" <<'PY'
import http.server, json, sys
out = sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def _answer(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else b""
        if self.path == "/v1/add":
            with open(out, "ab") as f:
                f.write(body + b"\n")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"id":"x","addedAt":0,"updatedAt":0}')
    do_GET = do_POST = _answer
    def log_message(self, *a):
        pass
srv = http.server.HTTPServer(("127.0.0.1", 0), H)
print(srv.server_port, flush=True)
srv.serve_forever()
PY
: > "${TMP}/adds.jsonl"
python3 -I "${TMP}/stub.py" 0 "${TMP}/adds.jsonl" > "${TMP}/port" &
SERVER_PID=$!
for _ in $(seq 50); do [[ -s "${TMP}/port" ]] && break; sleep 0.1; done
PORT="$(head -n1 "${TMP}/port")"

mkdir -p "${TMP}/docs"
printf -- '---\ntitle: Comma\ntags: database, markdown, golang\nstatus: draft\n---\n# Comma body\n' > "${TMP}/docs/comma.md"
printf -- '---\ntags: [database, markdown, golang]\n---\n# Flow body\n' > "${TMP}/docs/flow.md"
printf -- '---\ntags:\n  - database\n  - markdown\n  - golang\n---\n# Block body\n' > "${TMP}/docs/block.md"

# meta_of <key> <field> prints that add's field as JSON.
meta_of() {
	python3 -I -c '
import json, sys
for line in open(sys.argv[1]):
    d = json.loads(line)
    if d["key"] == sys.argv[2]:
        v = d["meta"].get(sys.argv[3]) if sys.argv[3] != "contentMd" else d["contentMd"]
        print(json.dumps(v))
' "${TMP}/adds.jsonl" "$1" "$2"
}

# 1-6: a real run, the server given with -s only.
if env -u MDDB_SERVER MDDB_CLI="${TMP}/mddb-cli" bash "${LOADER}" "${TMP}/docs" blog \
	-s "http://127.0.0.1:${PORT}" -m status=published >/dev/null 2>&1; then
	pass "loader run against the stub with -s (exit 0)"
else
	fail "loader run against the stub with -s failed"
fi

want='["database", "markdown", "golang"]'
for key in comma flow block; do
	got="$(meta_of "${key}" tags)"
	if [[ "${got}" == "${want}" ]]; then
		pass "${key}: tags=${got}"
	else
		fail "${key}: tags=${got}, want ${want}"
	fi
done

got="$(meta_of comma contentMd)"
if [[ "${got}" == '"# Comma body"' ]]; then
	pass "the body is stored without the frontmatter block"
else
	fail "contentMd=${got}"
fi

if [[ "$(meta_of comma status)" == '["published"]' && "$(meta_of comma title)" == '["Comma"]' \
	&& "$(meta_of comma source)" == '["folder-import"]' && "$(meta_of comma filename)" == '["comma.md"]' ]]; then
	pass "-m wins over frontmatter; source and filename are added"
else
	fail "meta merge: status=$(meta_of comma status) title=$(meta_of comma title) source=$(meta_of comma source)"
fi

# 7: an mddb-cli without --no-frontmatter would store the block as text.
cat > "${TMP}/old-cli" <<'SH'
#!/usr/bin/env bash
[[ "$1" == "add" ]] && echo "Flags: -m, --meta"
exit 0
SH
chmod +x "${TMP}/old-cli"
rc=0
MDDB_CLI="${TMP}/old-cli" bash "${LOADER}" "${TMP}/docs" blog >/dev/null 2>&1 || rc=$?
if [[ "${rc}" -eq 1 ]]; then
	pass "a CLI that cannot read frontmatter is refused (exit 1)"
else
	fail "an old CLI was accepted (exit ${rc})"
fi

# 8: a dry run never calls add.
: > "${TMP}/adds.jsonl"
MDDB_CLI="${TMP}/mddb-cli" MDDB_SERVER="http://127.0.0.1:${PORT}" bash "${LOADER}" "${TMP}/docs" blog -d >/dev/null 2>&1
if [[ ! -s "${TMP}/adds.jsonl" ]]; then
	pass "a dry run sends nothing"
else
	fail "a dry run sent $(wc -l < "${TMP}/adds.jsonl") documents"
fi

echo
echo "Passed: ${PASS}, failed: ${FAIL}"
[[ "${FAIL}" -eq 0 ]]
