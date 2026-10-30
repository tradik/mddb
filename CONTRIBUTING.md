# Contributing to MDDB

Thank you for your interest in contributing to MDDB! This guide will help you get started.

Be respectful and constructive in all project interactions. Report conduct or
security concerns to **security@tradik.com**.

## Prerequisites

- [Go 1.27+](https://golang.org/dl/)
- [Make](https://www.gnu.org/software/make/)
- [Protocol Buffers compiler](https://grpc.io/docs/protoc-installation/) (only if modifying `proto/mddb.proto`)
- [Node.js 24+](https://nodejs.org/) (only for `mddb-panel` changes)

## Project Structure

MDDB is a monorepo with the following services:

| Directory | Description |
|-----------|-------------|
| `services/mddbd` | Core database server (HTTP/JSON + gRPC/Protobuf + MCP) |
| `services/mddb-cli` | Command-line client |
| `services/mddb-panel` | React web UI |
| `proto/` | Protocol Buffer definitions |
| `docs/` | Documentation |

## Getting Started

```bash
# Clone the repository
git clone https://github.com/tradik/mddb.git
cd mddb

# Build the server
cd services/mddbd
make build

# Run tests
make test

# Run linter
make lint
```

### Re-running a failed CI shard

CI splits the server's root package, which holds over 3,000 tests, into three
shards per storage layout (`Core tests (single, 2/3)` and so on), plus three
on Windows. The shards are chosen by `scripts/go-test-shard.py` from test name
prefixes. To run the shard that failed:

```bash
make test-shard SHARD=2                                       # single-file layout
MDDB_STORAGE_LAYOUT=per-collection make test-shard SHARD=2    # per-collection layout
```

### SonarCloud

Pull requests and pushes to `main` are analysed by the SonarCloud scanner
(OPS-021). The scan is the last job of the Tests workflow, so it can hand
SonarCloud the coverage of the suites that run there: the Go server, CLI and
client, the panel, the Node.js client, the Python client and the LangChain
adapter. A change the Tests workflow does not run for (an integration with its
own workflow, docs) is scanned by `.github/workflows/sonarcloud.yml` instead,
never by both. Code tested only in its own workflow is listed in
`sonar.coverage.exclusions` in `sonar-project.properties`, next to the
workflow that enforces its coverage floor.

`sonar-project.properties` (the scanner) and `.sonarcloud.properties`
(Automatic Analysis, off; kept in case it is turned back on) must exclude the
same files. Add an exclusion to both, with its reason, and check:

```bash
make check-sonar-exclusions   # the two lists are in step
make test-sonar               # the scan helpers' own tests
```

## Development Workflow

1. **Fork** the repository
2. **Create a feature branch** from `main`:
   ```bash
   git checkout -b feat/my-feature
   ```
3. **Make your changes**
4. **Add tests** for new functionality
5. **Regenerate proto code** if you modified `proto/mddb.proto`:
   ```bash
   make generate-proto
   ```
6. **Run checks**:
   ```bash
   make fmt
   make lint
   make test
   ```
7. **Commit** using conventional commits (see below)
8. **Push** and open a Pull Request

### Documentation site

`docs/` and `blog/` are published to [mddb.tradik.com](https://mddb.tradik.com)
by the SSG build. Preview and verify locally:

```bash
make docs-dev        # live preview on http://localhost:8888
make docs-build      # build the static site into dist/
make docs-linkcheck  # build, then fail on any link that hits a redirect
```

The build validates its own output. SSG fails on dead links, images with no
`alt`, indexable pages missing a `<title>` or meta description, and pages
nothing links to (`check_*` in `.ssg.yaml`); `check-docs-links.py` adds the one
thing it cannot see — links that only resolve through a redirect.

Two rules follow from that:

- Repository files have no URL on the published site, so **never link to source
  with a repo-relative path** (`../services/mddbd/main.go`). Use the full GitHub
  URL (`https://github.com/tradik/mddb/blob/main/services/mddbd/main.go`) — or,
  when the page exists on the site, link the site instead.
- **Link the final URL, never a redirect.** Cloudflare Pages strips `.html` and
  appends a missing trailing slash, answering with a 308 either way — so write
  `/docs/config/`, not `/docs/config`.

Every page also needs a `description:` in its front matter: it becomes the meta
description, and the build fails without one.

## Commit Convention

We use [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | Usage |
|--------|-------|
| `feat:` | New feature |
| `fix:` | Bug fix |
| `docs:` | Documentation changes |
| `chore:` | Maintenance, dependencies, CI |
| `refactor:` | Code refactoring (no behavior change) |
| `test:` | Adding or updating tests |
| `perf:` | Performance improvements |

Examples:
```
feat: add TTL support for documents
fix: resolve race condition in vector index
docs: update API reference with new endpoints
```

## Pull Request Guidelines

- Keep PRs focused on a single change
- Include a clear description of what changed and why
- Add or update tests as needed
- Update documentation for user-facing changes
- Ensure CI passes (tests, lint, build)

## Reporting Issues

- Use [GitHub Issues](https://github.com/tradik/mddb/issues) for bugs and feature requests
- For security vulnerabilities, see [SECURITY.md](SECURITY.md)
- **Attach a diagnose bundle** to a bug report: `mddb-cli diagnose` (admin
  login or key) saves a tar.gz with the build, the configuration through an
  allowlist, stats, metrics, query-shape counters and recent error classes —
  no document content, queries, keys, actors, IPs or paths, and collection
  names hashed. Run `mddb-cli diagnose --inspect` first to see exactly what it
  holds. See [docs/TELEMETRY.md, "Diagnose bundle"](docs/TELEMETRY.md#diagnose-bundle).
- A question about code that MDDB's graph or search did not answer has its own
  template ("Code or graph question"); paste `query-shape.json` from the bundle
  there. Those reports, with the numbers, decide whether the graph goes deeper
  (CODE-006) or gains a module map (CODE-007).

## License

By contributing, you agree that your contributions will be licensed under the [BSD 3-Clause License](LICENSE).
