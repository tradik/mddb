package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// Reading a forge's tar.gz archive of a commit (#421), streamed: one entry
// at a time, only the regular files the rule includes kept, each at most
// maxBytes, at most maxDocuments of them, and the whole archive at most
// maxGitArchiveBytes uncompressed. Symlinks, hard links and devices are
// skipped; a path that is absolute or climbs out with ".." fails the run, as
// an archive no forge would build. The forge's top directory
// (<repo>-<commit>/) is stripped, so a path is repository-relative — the key
// `mddb-cli autoindex` gives the same file.

// Bounds of an archive.
const (
	maxGitArchiveBytes = 1 << 30 // uncompressed, every entry counted
	// gitSniffBytes is how much of a file decides that it is binary, as
	// autoindex decides it.
	gitSniffBytes = 8 << 10
)

// errGitArchiveUnsafe is an entry whose path leaves the repository.
var errGitArchiveUnsafe = errors.New("the archive holds a path outside the repository")

// gitFile is one file of the archive the rule includes.
type gitFile struct {
	path     string
	body     []byte // nil when tooLarge
	size     int64
	modified time.Time
	tooLarge bool
}

// gitArchiveLimits are what readGitArchive keeps.
type gitArchiveLimits struct {
	maxFiles         int
	maxBytes         int64
	include, exclude []string
}

// keeps reports whether the rule includes a repository-relative path, with
// the globs `mddb-cli autoindex` matches.
func (l gitArchiveLimits) keeps(rel string) bool {
	return (len(l.include) == 0 || matchSyncGlobs(l.include, rel)) && !matchSyncGlobs(l.exclude, rel)
}

// countingReader fails once more than limit bytes have been read through it.
type countingReader struct {
	r     io.Reader
	n     int64
	limit int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.limit {
		return n, fmt.Errorf("the archive is larger than %d bytes uncompressed", c.limit)
	}
	return n, err
}

// readGitArchive reads the files of a tar.gz the rule includes. truncated is
// set when it holds more than maxFiles of them; the rest are not read.
func readGitArchive(r io.Reader, lim gitArchiveLimits) (files []gitFile, truncated bool, err error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, false, fmt.Errorf("the archive is not gzip: %w", err)
	}
	a := &gitArchiveReader{tr: tar.NewReader(&countingReader{r: zr, limit: maxGitArchiveBytes}), lim: lim}
	for {
		done, truncated, err := a.next()
		switch {
		case err != nil:
			return nil, false, err
		case done:
			return a.files, truncated, nil
		}
	}
}

// gitArchiveReader is an archive being read and the files kept so far.
type gitArchiveReader struct {
	tr    *tar.Reader
	lim   gitArchiveLimits
	files []gitFile
}

// next reads one entry. done is set at the end of the archive, and with
// truncated when an included file is one more than maxFiles.
func (a *gitArchiveReader) next() (done, truncated bool, err error) {
	hdr, err := a.tr.Next()
	if errors.Is(err, io.EOF) {
		return true, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading the archive: %w", err)
	}
	rel, err := gitArchivePath(hdr)
	if err != nil || rel == "" || !a.lim.keeps(rel) {
		return false, false, err
	}
	if len(a.files) == a.lim.maxFiles {
		return true, true, nil
	}
	f, keep, err := readGitEntry(a.tr, hdr, rel, a.lim.maxBytes)
	if keep {
		a.files = append(a.files, f)
	}
	return false, false, err
}

// gitArchivePath is an entry's repository-relative path: "" for what is
// not a regular file (a directory, a link, a device, the pax header GitHub
// records the commit in) and for the top directory itself.
func gitArchivePath(hdr *tar.Header) (string, error) {
	name := hdr.Name
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q", errGitArchiveUnsafe, name)
		}
	}
	if path.IsAbs(name) || strings.HasPrefix(name, `\`) || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("%w: %q", errGitArchiveUnsafe, name)
	}
	if hdr.Typeflag != tar.TypeReg {
		return "", nil
	}
	_, rel, found := strings.Cut(path.Clean(name), "/")
	if !found {
		return "", nil
	}
	return rel, nil
}

// readGitEntry reads one included file: a file over maxBytes is kept
// without its content (the run reports it), a binary one is left out.
func readGitEntry(tr io.Reader, hdr *tar.Header, rel string, maxBytes int64) (gitFile, bool, error) {
	f := gitFile{path: rel, size: hdr.Size, modified: hdr.ModTime.UTC()}
	if hdr.Size > maxBytes {
		f.tooLarge = true
		return f, true, nil
	}
	body, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
	if err != nil {
		return f, false, fmt.Errorf("reading %s from the archive: %w", rel, err)
	}
	if gitConvertFormat(rel) == "" && bytes.IndexByte(body[:min(len(body), gitSniffBytes)], 0) >= 0 {
		return f, false, nil
	}
	f.body = body
	return f, true, nil
}
