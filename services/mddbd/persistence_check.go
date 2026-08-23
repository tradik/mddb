package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"mddb/internal/envconf"
)

// Persistence guarantees and the footguns that break them (GO-032).
//
// bbolt fsyncs on every commit, so an acknowledged write survives a crash —
// but only if the bytes went somewhere that survives too. The failure this
// guards is a container started without a volume: writes are acknowledged,
// fsynced, and then vanish with the container. The engine cannot prevent that
// deployment, but it must not stay quiet about it.

// PersistenceWarning is one problem found with the data directory.
type PersistenceWarning struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// PersistenceStatus summarises what the server can promise about durability.
type PersistenceStatus struct {
	Path       string               `json:"path"`
	Filesystem string               `json:"filesystem,omitempty"`
	Ephemeral  bool                 `json:"ephemeral"`
	Writable   bool                 `json:"writable"`
	FreeBytes  uint64               `json:"freeBytes,omitempty"`
	Warnings   []PersistenceWarning `json:"warnings,omitempty"`
}

// Durable reports whether an acknowledged write can be expected to survive a
// restart of the host process and its container.
func (p PersistenceStatus) Durable() bool {
	return p.Writable && !p.Ephemeral
}

// ephemeralFilesystems are the mount types whose contents do not outlive the
// container or the machine. overlay is the giveaway for a Docker data
// directory that was never given a volume.
var ephemeralFilesystems = map[string]string{
	"tmpfs":    "an in-memory filesystem — its contents are lost on restart",
	"ramfs":    "an in-memory filesystem — its contents are lost on restart",
	"overlay":  "the container's own writable layer — its contents are lost when the container is removed; mount a volume at this path",
	"overlay2": "the container's own writable layer — its contents are lost when the container is removed; mount a volume at this path",
	"aufs":     "the container's own writable layer — its contents are lost when the container is removed; mount a volume at this path",
}

// CheckPersistence inspects the directory holding the database and reports
// what durability it can actually offer.
func CheckPersistence(dbPath string) PersistenceStatus {
	dir := filepath.Dir(dbPath)
	status := PersistenceStatus{Path: dir}

	status.Filesystem = filesystemType(dir, "/proc/self/mountinfo")
	if reason, ephemeral := ephemeralFilesystems[status.Filesystem]; ephemeral {
		status.Ephemeral = true
		status.Warnings = append(status.Warnings, PersistenceWarning{
			Code:   "ephemeral_storage",
			Detail: fmt.Sprintf("the data directory is on %s: %s", status.Filesystem, reason),
		})
	}

	status.Writable = isWritable(dir)
	if !status.Writable {
		status.Warnings = append(status.Warnings, PersistenceWarning{
			Code:   "not_writable",
			Detail: "the data directory cannot be written to; writes will fail",
		})
	}

	if free, err := freeBytes(dir); err == nil {
		status.FreeBytes = free
		if minFree := int64(envconf.Int64("MDDB_DISK_MIN_FREE", 100*1024*1024)); minFree > 0 && free < uint64(minFree) {
			status.Warnings = append(status.Warnings, PersistenceWarning{
				Code: "low_disk_space",
				Detail: fmt.Sprintf("only %d MB free (below MDDB_DISK_MIN_FREE of %d MB); writes will start failing",
					free/(1024*1024), minFree/(1024*1024)),
			})
		}
	}

	return status
}

// filesystemType resolves the filesystem backing dir by finding its mount
// point in mountinfo. The longest matching mount point wins, since /data is
// more specific than /.
//
// mountinfoPath is a parameter so tests can supply a fixture; a missing or
// unreadable file yields "" rather than an error, because not knowing the
// filesystem is a reason to stay quiet, not to refuse to start.
func filesystemType(dir, mountinfoPath string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Clean(mountinfoPath)) // #nosec G304 -- caller supplies a fixed path or a test fixture
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	type mount struct {
		point string
		fs    string
	}
	var mounts []mount

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// mountinfo: id parent major:minor root MOUNT_POINT opts... - FSTYPE source ...
		fields := strings.Fields(scanner.Text())
		sep := -1
		for i, fld := range fields {
			if fld == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+1 >= len(fields) || sep < 5 {
			continue
		}
		mounts = append(mounts, mount{point: fields[4], fs: fields[sep+1]})
	}

	// Longest mount point first, so the most specific match wins.
	sort.Slice(mounts, func(i, j int) bool { return len(mounts[i].point) > len(mounts[j].point) })
	for _, m := range mounts {
		if abs == m.point || strings.HasPrefix(abs, strings.TrimSuffix(m.point, "/")+"/") {
			return m.fs
		}
	}
	return ""
}

// isWritable reports whether a file can actually be created in dir. Checking
// the mode bits is not enough: a read-only mount, a full filesystem or a
// container user mismatch all present as writable-looking permissions.
func isWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".mddb-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// freeBytes returns the space available to an unprivileged process.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	// Both fields are converted rather than one: syscall.Statfs_t is
	// platform-specific, and Bavail is uint64 on Linux but int64 on the BSDs.
	// Multiplying them directly compiles on Linux and fails the FreeBSD build
	// with a type mismatch, which is how this reached a release branch — the
	// only builder that could see it is the one nobody runs locally.
	// #nosec G115 -- a block count and a block size are both non-negative
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// logPersistenceStatus reports the findings at startup. Warnings are logged
// individually so a log collector can alert on the code rather than parsing
// prose.
func logPersistenceStatus(status PersistenceStatus, log func(msg string, args ...any)) {
	for _, w := range status.Warnings {
		log("persistence warning", "code", w.Code, "detail", w.Detail, "path", status.Path)
	}
}
