package main

import (
	"io"
	"mddb/internal/storage"
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
	tmp := dst + ".tmp"
	// #nosec G304 -- Subpath created securely
	out, err := os.Create(filepath.Clean(tmp))
	if err != nil {
		return err
	}

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
