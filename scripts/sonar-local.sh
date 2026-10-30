#!/usr/bin/env bash
#
# sonar-local.sh — the SonarCloud scan of the Tests workflow, run on this
# machine before a push, so a failing quality gate costs no CI minutes.
#
#   SONAR_TOKEN=… scripts/sonar-local.sh            # analyse this branch's PR
#   SONAR_TOKEN=… GO_COVERPROFILE=x.out scripts/sonar-local.sh
#   scripts/sonar-local.sh --dry-run                 # print the plan only
#
# Steps, as in the `sonarcloud` job of .github/workflows/test.yml:
#   1. coverage: the server (or GO_COVERPROFILE, e.g. a merged profile you
#      already have), the CLI and the Go client; lcov from the panel and the
#      Node.js client; coverage.xml from the Python client and the LangChain
#      adapter;
#   2. every report rewritten to this checkout (scripts/sonar-coverage-paths.py);
#   3. the scanner in Docker, with the repository mounted at its own path so
#      the reports' absolute paths hold inside the container. With an open pull
#      request for this branch it is analysed as that pull request, otherwise
#      as the branch. sonar.qualitygate.wait makes the exit status the gate's.
#
# SONAR_TOKEN comes from the environment and is never printed. Reports go to a
# temporary directory, never into the tree.
set -euo pipefail

SCANNER_IMAGE="sonarsource/sonar-scanner-cli:12.3.0.4285_8.1.0"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DRY=0
[[ "${1:-}" == "--dry-run" ]] && DRY=1

run() {
	if (( DRY )); then printf '+ %s\n' "$*"; else "$@"; fi
}

if (( ! DRY )) && [[ -z "${SONAR_TOKEN:-}" ]]; then
	echo "sonar-local: SONAR_TOKEN is not set (SonarCloud → My Account → Security)" >&2
	exit 2
fi
for tool in docker go npm python3; do
	command -v "${tool}" >/dev/null || { echo "sonar-local: ${tool} not found" >&2; exit 2; }
done

OUT="$(mktemp -d)"
trap 'rm -rf "${OUT}"' EXIT
mkdir -p "${OUT}/in" "${OUT}/reports"
UV="$(command -v uv || echo "${HOME}/.local/bin/uv")"

# 1. Coverage.
if [[ -n "${GO_COVERPROFILE:-}" ]]; then
	run cp "${GO_COVERPROFILE}" "${OUT}/in/go-mddbd.out"
else
	run bash -c "cd '${REPO}/services/mddbd' && go test -race -count=1 -timeout 40m -coverprofile='${OUT}/in/go-mddbd.out' ./... >/dev/null"
fi
run bash -c "cd '${REPO}/services/mddb-cli' && GOWORK=off go test -count=1 -coverprofile='${OUT}/in/go-cli.out' ./... >/dev/null"
run bash -c "cd '${REPO}/clients/go/mddb' && go test -count=1 -coverprofile='${OUT}/in/go-client.out' ./... >/dev/null"
run bash -c "cd '${REPO}/services/mddb-panel' && npx vitest run --coverage >/dev/null && cp coverage/lcov.info '${OUT}/in/panel.lcov'"
run bash -c "cd '${REPO}/clients/nodejs' && npm run -s test:coverage >/dev/null && mv lcov.info '${OUT}/in/nodejs.lcov'"
run bash -c "cd '${REPO}/clients/python' && '${UV}' sync -q --locked --extra test && '${UV}' run --no-sync pytest -q --cov=mddb_client --cov-report=xml:'${OUT}/in/python.xml' >/dev/null"
run bash -c "cd '${REPO}/integrations/langchain-mddb' && '${UV}' sync -q --locked --extra test && '${UV}' run --no-sync pytest -q --cov=langchain_mddb --cov-report=xml:'${OUT}/in/langchain.xml' >/dev/null"

# 2. Paths. The converter accepts paths inside the repository or its working
# directory (scripts/safepath.py), so it runs from OUT.
fix=(python3 "${REPO}/scripts/sonar-coverage-paths.py")
run bash -c "cd '${OUT}' && $(printf '%q ' "${fix[@]}") go '${REPO}' in/go-mddbd.out reports/go-mddbd.out \
	&& $(printf '%q ' "${fix[@]}") go '${REPO}' in/go-cli.out reports/go-cli.out \
	&& $(printf '%q ' "${fix[@]}") go '${REPO}' in/go-client.out reports/go-client.out \
	&& $(printf '%q ' "${fix[@]}") lcov '${REPO}' in/panel.lcov reports/panel.lcov services/mddb-panel \
	&& $(printf '%q ' "${fix[@]}") lcov '${REPO}' in/nodejs.lcov reports/nodejs.lcov clients/nodejs \
	&& $(printf '%q ' "${fix[@]}") cobertura '${REPO}' in/python.xml reports/python.xml clients/python \
	&& $(printf '%q ' "${fix[@]}") cobertura '${REPO}' in/langchain.xml reports/langchain.xml integrations/langchain-mddb"

# 3. Scan. A pull request for this branch, if there is one.
branch="$(git -C "${REPO}" rev-parse --abbrev-ref HEAD)"
pr="$(gh pr view "${branch}" --json number,baseRefName -q '"\(.number) \(.baseRefName)"' 2>/dev/null || true)"
target=(-Dsonar.branch.name="${branch}")
if [[ -n "${pr}" ]]; then
	target=(-Dsonar.pullrequest.key="${pr%% *}" -Dsonar.pullrequest.branch="${branch}" -Dsonar.pullrequest.base="${pr##* }")
fi
R="${OUT}/reports"
run docker run --rm -e SONAR_TOKEN -v "${REPO}:${REPO}" -v "${OUT}:${OUT}" -w "${REPO}" \
	--user "$(id -u):$(id -g)" "${SCANNER_IMAGE}" \
	-Dsonar.projectBaseDir="${REPO}" -Dsonar.qualitygate.wait=true -Dsonar.qualitygate.timeout=600 \
	"${target[@]}" \
	-Dsonar.go.coverage.reportPaths="${R}/go-mddbd.out,${R}/go-cli.out,${R}/go-client.out" \
	-Dsonar.javascript.lcov.reportPaths="${R}/panel.lcov,${R}/nodejs.lcov" \
	-Dsonar.python.coverage.reportPaths="${R}/python.xml,${R}/langchain.xml"
