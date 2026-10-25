# bbolt v1.5.0 with mddb patches

This is go.etcd.io/bbolt v1.5.0 (MIT, see LICENSE), used by mddbd through a
`replace` directive in `services/mddbd/go.mod`. The `cmd/`, `.github/`,
`scripts/` and `tests/` directories of the release are left out (`tests/`
holds integration suites that need gofail or root), and `go.mod` names the
monorepo's toolchain. Two changes:

1. **Deferred freelist loading** (mddb #333) — `Options.DeferFreelistLoad`,
   `DB.LoadFreelist()` and `DB.FreelistLoaded()`, in `deferred_freelist.go`
   and five marked hunks in `db.go`. A writable open normally walks every
   page to rebuild the freelist when none is stored; with the option it
   returns after reading the meta pages, read transactions run at once, and
   writers wait until the freelist is loaded. Readers that begin before it
   exists are registered with it when it is installed, so pages they can see
   are not reused. A load that runs after the database was closed returns
   `ErrDatabaseNotOpen` instead of reading the unmapped file.
2. **A damaged page during the freelist walk is an error, not a crash.**
   `freepages` walked the tree in a goroutine of its own, where a panic on a
   damaged page could not be recovered by any caller and ended the process.
   It is now reported back to the caller's goroutine, and `Open` returns it
   as an error.

Tests: `deferred_freelist_test.go`, run with
`GOWORK=off go test -race -run Deferred .` in this directory. The release's
own unit suite passes (`go test -short ./...`).

**Upgrading bbolt:** copy the new release over this directory, keep this file
and `deferred_freelist*.go`, re-apply the hunks marked `mddb patch` in
`db.go`, and run the tests above. Both changes are candidates for upstream.
