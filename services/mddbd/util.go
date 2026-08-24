package main

import (
	"io"
	"mddb/internal/storage"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func genID(parts ...string) string {
	// Optimized ID generation without string allocations
	totalLen := 0
	for i, part := range parts {
		totalLen += len(part)
		if i < len(parts)-1 {
			totalLen++ // for '|'
		}
	}

	buf := make([]byte, 0, totalLen)
	for i, part := range parts {
		for j := 0; j < len(part); j++ {
			c := part[j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			buf = append(buf, c)
		}
		if i < len(parts)-1 {
			buf = append(buf, '|')
		}
	}

	return string(buf)
}
func applyEnv(s string, env map[string]string) string {
	for k, v := range env {
		s = strings.ReplaceAll(s, "%%"+k+"%%", v)
	}
	return s
}
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
}
func intersect(sets ...[]string) []string {
	if len(sets) == 0 {
		return nil
	}
	m := map[string]int{}
	for _, s := range sets {
		for _, id := range s {
			m[id]++
		}
	}
	out := []string{}
	for id, c := range m {
		if c == len(sets) {
			out = append(out, id)
		}
	}
	return out
}

// copyBufferSize is how much of a file is read at a time when copying it.
// 256 KiB keeps the syscall count low on database-sized files without pinning
// meaningful memory: one buffer per concurrent copy, and backups do not run
// concurrently.
const copyBufferSize = 256 << 10

func copyFile(src, dst string) error {
	// #nosec G304 -- Function intentionally copies provided path
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	// The temp file is created by os.CreateTemp in the destination's own
	// directory rather than at a fixed dst+".tmp". Two properties come from
	// that: the name is unique, so two copies to the same destination no longer
	// write through each other's temp file; and the path this function later
	// deletes is one os.CreateTemp produced, not one assembled from the
	// caller's string.
	out, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	// Clean up on every failure path. Both calls are no-ops once the copy has
	// succeeded — Close on a closed file, Remove on a path the rename has
	// emptied. Without this a copy that fails midway leaves an orphan the size
	// of whatever it managed to write, which on a restore is a partial copy of
	// the database, sitting on the filesystem that most likely just ran out of
	// room.
	defer func() {
		_ = out.Close()
		// #nosec G703 -- tmp is the name os.CreateTemp just produced, so this
		// can only unlink the file this function itself created two statements
		// ago. Its reachable set is a subset of that create's.
		_ = os.Remove(tmp)
	}()

	// GO-020: this copies whole database files during backup and restore.
	// io.Copy alone reads in 32 KiB steps with a buffer it allocates per call;
	// the pooled reader raises that to copyBufferSize and reuses the buffer
	// across every copy, so a multi-gigabyte database costs a fraction of the
	// read syscalls and no repeated allocation.
	buffered := NewZeroCopyReader(in, copyBufferSize)
	defer func() { _ = buffered.Close() }()

	if _, err = io.Copy(out, buffered); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst) // #nosec G703 -- paths are internally constructed
}

// httpFlusher resolves the http.Flusher behind w, walking Unwrap()-style
// middleware wrappers the way http.ResponseController does. GO-040: a bare
// `w.(http.Flusher)` assertion fails on any wrapper that hides Flush, which
// turned every SSE endpoint into a 500 whenever the metrics middleware was on.
func httpFlusher(w http.ResponseWriter) (http.Flusher, bool) {
	for {
		if f, ok := w.(http.Flusher); ok {
			return f, true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, false
		}
		w = u.Unwrap()
	}
}

func sortDocs(docs []storage.Doc, field string, asc bool) {
	sort.Slice(docs, func(i, j int) bool {
		var less bool
		switch field {
		case "addedAt":
			less = docs[i].AddedAt < docs[j].AddedAt
		case "updatedAt":
			less = docs[i].UpdatedAt < docs[j].UpdatedAt
		case "key":
			less = docs[i].Key < docs[j].Key
		default:
			less = docs[i].UpdatedAt < docs[j].UpdatedAt
		}
		if asc {
			return less
		}
		return !less
	})
}
