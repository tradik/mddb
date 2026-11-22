package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	json "mddb/internal/jsonx"

	"github.com/klauspost/compress/zstd"
)

// The pack archive (AGT-005): tar inside zstd, manifest.json first, then
// collection.db — or, in a pack of several collections, collection-1.db …
// collection-N.db (FE-022) — nothing else. zstd is the codec the server already links
// for document compression, so the format costs no new dependency.

const (
	// corpusManifestMax bounds manifest.json.
	corpusManifestMax = 1 << 20
	// corpusMaxWindow bounds the zstd window a pack may ask a decoder to
	// hold; the encoder below uses 8 MB.
	corpusMaxWindow = 64 << 20
	// corpusMaxBytesDefault is MDDB_CORPUS_MAX_BYTES unset: the largest
	// collection.db an install unpacks.
	corpusMaxBytesDefault = 64 << 30
)

// writeCorpusArchive streams the pack: m, then each collection's file from dir.
func writeCorpusArchive(w io.Writer, m *corpusManifest, dir *os.Root) error {
	// The options are constants NewWriter accepts; it fails on none else.
	zw, _ := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
	tw := tar.NewWriter(zw)
	err := writeCorpusMembers(tw, m, dir)
	// Arguments are evaluated in order: the tar is closed before the zstd
	// stream it writes into.
	return errors.Join(err, tw.Close(), zw.Close())
}

// writeCorpusMembers writes manifest.json, then each collection's file
// from dir.
func writeCorpusMembers(tw *tar.Writer, m *corpusManifest, dir *os.Root) error {
	manifest, _ := json.MarshalIndent(m, "", "  ") // strings, numbers and a time: always encodes
	if err := writeCorpusMember(tw, corpusManifestName, int64(len(manifest)), m.CreatedAt, bytes.NewReader(manifest)); err != nil {
		return err
	}
	for i, p := range m.parts() {
		if err := writeCorpusFile(tw, dir, corpusMemberName(m.Format, i), p.Bytes, m.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

// writeCorpusFile writes the file name of dir as the member of that name.
func writeCorpusFile(tw *tar.Writer, dir *os.Root, name string, size int64, modTime time.Time) error {
	f, err := dir.Open(name) // NOSONAR gosecurity:S2083 -- os.Root: a name corpusMemberName made, inside the build directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return writeCorpusMember(tw, name, size, modTime, f)
}

// writeCorpusMember writes one regular file of size bytes read from r.
func writeCorpusMember(tw *tar.Writer, name string, size int64, modTime time.Time, r io.Reader) error {
	hdr := &tar.Header{Name: name, Mode: 0o644, Size: size, ModTime: modTime, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := io.Copy(tw, r)
	return err
}

// readCorpusArchive reads a pack from r: its manifest, checked before the
// data is read, then each collection's file, written into dir as it streams
// and checked against the manifest's sha256. A member out of order, of
// another name or type, or files larger than maxBytes together are refused;
// member names are the ones corpusMemberName makes, never the manifest's, so
// nothing can be written outside dir.
func readCorpusArchive(r io.Reader, dir *os.Root, maxBytes int64) (*corpusManifest, error) {
	zr, tr := openCorpusArchive(r)
	defer zr.Close()
	m, err := readCorpusManifest(tr)
	if err != nil {
		return nil, err
	}
	left := maxBytes
	for i, p := range m.parts() {
		name := corpusMemberName(m.Format, i)
		hdr, err := nextCorpusMember(tr, name, left)
		if err != nil {
			return nil, err
		}
		left -= hdr.Size
		sum, err := copyCorpusData(tr, dir, name, hdr.Size)
		if err != nil {
			return nil, err
		}
		if sum != p.SHA256 {
			return nil, fmt.Errorf("%w: %s has sha256 %s, the manifest says %s: the pack is damaged or was altered", errCorpusInvalid, name, sum, p.SHA256)
		}
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the pack holds more than its manifest and the files it lists", errCorpusInvalid)
	}
	return m, nil
}

// openCorpusArchive is the tar inside the zstd stream r, with a decoder
// whose window is bounded. The caller closes the decoder.
func openCorpusArchive(r io.Reader) (*zstd.Decoder, *tar.Reader) {
	// Constant options, accepted. A bad stream fails at the reads.
	zr, _ := zstd.NewReader(r, zstd.WithDecoderMaxWindow(corpusMaxWindow), zstd.WithDecoderConcurrency(1))
	return zr, tar.NewReader(zr)
}

// readCorpusManifest reads and checks manifest.json, the first member.
func readCorpusManifest(tr *tar.Reader) (*corpusManifest, error) {
	hdr, err := nextCorpusMember(tr, corpusManifestName, corpusManifestMax)
	if err != nil {
		return nil, err
	}
	var m corpusManifest
	if err := json.NewDecoder(io.LimitReader(tr, hdr.Size)).Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: manifest.json: %w", errCorpusInvalid, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", errCorpusInvalid, err)
	}
	return &m, nil
}

// nextCorpusMember reads the next tar header and refuses anything but a
// regular file called name of at most limit bytes.
func nextCorpusMember(tr *tar.Reader, name string, limit int64) (*tar.Header, error) {
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("%w: reading the archive (is it a .mddbpack?): %w", errCorpusInvalid, err)
	}
	switch {
	case hdr.Name != name || hdr.Typeflag != tar.TypeReg:
		return nil, fmt.Errorf("%w: expected %s, found %q", errCorpusInvalid, name, hdr.Name)
	case hdr.Size > limit:
		return nil, fmt.Errorf("%w: %s is %d bytes, over the limit of %d (MDDB_CORPUS_MAX_BYTES)", errCorpusInvalid, name, hdr.Size, limit)
	}
	return hdr, nil
}

// copyCorpusData writes size bytes of r to the file name in dir and returns
// their sha256.
func copyCorpusData(r io.Reader, dir *os.Root, name string, size int64) (string, error) {
	f, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // NOSONAR gosecurity:S2083 -- os.Root: a name corpusMemberName made, inside a directory this server created
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.CopyN(io.MultiWriter(f, h), r, size)
	if err != nil {
		err = fmt.Errorf("%w: %s: %w", errCorpusInvalid, name, err)
	}
	return hex.EncodeToString(h.Sum(nil)), errors.Join(err, f.Close())
}
