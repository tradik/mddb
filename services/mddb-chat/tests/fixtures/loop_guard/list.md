Every service in the monorepo has its own test command; run each from the repository root:

1. Run `make test` in `services/mddbd` and check that every test passes in the default storage layout.
2. Run `make test` in `services/mddbd` and check that every test passes in the per-collection storage layout.
3. Run `make test` in `services/mddb-chat` and check that every test passes, the fake mddbd included.
4. Run `make test` in `services/mddb-panel` and check that every test passes, coverage included.
5. Run `make test` in `services/mddb-cli` and check that every test passes against a local server.
6. Run `make test` in `clients/python` and check that every test passes with the generated stubs.
7. Run `make test` in `clients/nodejs` and check that every test passes with the synced proto file.

The keys you will see in the reports:

- `coverage.total` is the share of statements the tests ran.
- `coverage.files` is the same share for each file on its own.
- `coverage.threshold` is the share below which the run fails.
- `coverage.changed` is the share for the files the branch changed.

If one of the steps fails, fix it before running the next: the later steps assume the earlier ones passed, and a failure further down is often the same failure seen from another side.
