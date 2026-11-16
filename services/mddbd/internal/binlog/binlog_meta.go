package binlog

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
)

// The sidecar: "<binlog>.meta", a small JSON file next to the log.
//
// The log file is entries and nothing else, in the layout every release
// since the first has written, so a log from an older release opens as it is
// and an older release can still read one this release wrote. What a file of
// entries cannot say is kept here:
//
//   - logId: which log this is. A leader that cannot vouch for its log starts
//     a new one under a new ID, and a follower holding the old ID takes a
//     snapshot instead of continuing a history that is not the leader's.
//   - baseLsn: the LSN before the first entry, which is all that is left of
//     the position when retention or a new log has emptied the file.
//   - terms: where each election term begins (GO-051), in a cluster.
//   - state / cleanLsn: "clean" with the last LSN when the log was closed
//     with everything on disk, "open" while it is in use. A log found "open"
//     at start was not closed: the process died, and entries the leader had
//     committed to its database may never have reached this file.
//
// version guards the sidecar's own layout and entryFormat the layout of the
// entries in the log; a file written with a higher number than this build
// knows is refused rather than misread. Both exist so that a later release
// can add to either (an election term, for one) without guessing.

const (
	metaVersion        = 1
	entryFormatVersion = 1
	stateOpen          = "open"
	stateClean         = "clean"
)

// logMeta is the sidecar's content.
type logMeta struct {
	Version     int    `json:"version"`
	EntryFormat int    `json:"entryFormat"`
	LogID       string `json:"logId"`
	BaseLSN     uint64 `json:"baseLsn"`
	State       string `json:"state"`
	CleanLSN    uint64 `json:"cleanLsn"`
	// Terms is where each election term begins (GO-051, binlog_terms.go);
	// absent outside a cluster. A release that does not know it ignores it.
	Terms []TermStart `json:"terms,omitempty"`
}

// metaStatus is what loadMeta found.
type metaStatus int

const (
	metaMissing metaStatus = iota // no sidecar: a new log, or one from before 2.19.8
	metaLoaded
	metaUnreadable // damaged: nothing in it can be relied on
)

// fileOps are the file operations a test can make fail. They are swapped
// as a whole, atomically: the flusher and retention goroutines call them
// while a test replaces them.
type fileOps struct {
	rename   func(string, string) error
	truncate func(string, int64) error
	openFile func(string, int, os.FileMode) (*os.File, error)
	sync     func(*os.File) error
}

var ops atomic.Pointer[fileOps]

func init() {
	ops.Store(&fileOps{rename: os.Rename, truncate: os.Truncate, openFile: os.OpenFile, sync: (*os.File).Sync})
}

func osRename(from, to string) error           { return ops.Load().rename(from, to) }
func osTruncate(path string, size int64) error { return ops.Load().truncate(path, size) }
func syncFile(f *os.File) error                { return ops.Load().sync(f) }
func osOpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	return ops.Load().openFile(path, flag, perm)
}

func (b *Binlog) metaPath() string { return b.path + ".meta" }

// loadMeta reads the sidecar. A sidecar from a newer release is an error:
// this build does not know what it would be ignoring.
func loadMeta(path string) (logMeta, metaStatus, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if os.IsNotExist(err) {
		return logMeta{}, metaMissing, nil
	}
	var m logMeta
	if err != nil || json.Unmarshal(raw, &m) != nil {
		return logMeta{}, metaUnreadable, nil
	}
	if m.Version > metaVersion || m.EntryFormat > entryFormatVersion {
		return logMeta{}, metaUnreadable, fmt.Errorf(
			"%s was written by a newer release (version %d, entry format %d; this build reads %d and %d)",
			filepath.Base(path), m.Version, m.EntryFormat, metaVersion, entryFormatVersion)
	}
	return m, metaLoaded, nil
}

// saveMeta writes the sidecar for the log's current position, replacing the
// old one in a single rename. The caller holds b.mu.
func (b *Binlog) saveMeta(state string) error {
	raw, _ := json.Marshal(logMeta{
		Version:     metaVersion,
		EntryFormat: entryFormatVersion,
		LogID:       FormatLogID(b.logID),
		BaseLSN:     b.baseLSN,
		State:       state,
		CleanLSN:    b.lsn.Load(),
		Terms:       b.terms,
	})
	tmp := b.metaPath() + ".tmp"
	// #nosec G304 -- next to the binlog, whose path the operator configured
	f, err := osOpenFile(filepath.Clean(tmp), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("writing the binlog sidecar: %w", err)
	}
	_, err = f.Write(append(raw, '\n'))
	if err == nil {
		err = syncFile(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = osRename(tmp, b.metaPath())
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing the binlog sidecar: %w", err)
	}
	return nil
}

// newLogID draws a log ID; 0 is kept for "unknown".
func newLogID() uint64 {
	var raw [8]byte
	for {
		_, _ = rand.Read(raw[:]) // crypto/rand.Read does not fail (Go 1.24+)
		if id := binary.BigEndian.Uint64(raw[:]); id != 0 {
			return id
		}
	}
}

// FormatLogID is a log ID as the sidecar and the status endpoints show it.
func FormatLogID(id uint64) string {
	return fmt.Sprintf("%016x", id)
}

// parseLogID reads FormatLogID's output; anything else is 0, "unknown".
func parseLogID(s string) uint64 {
	id, _ := strconv.ParseUint(s, 16, 64)
	return id
}
