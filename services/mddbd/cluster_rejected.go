package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"mddb/internal/binlog"
)

// The rejected-writes file (GO-051).
//
// A node that led and took writes its followers never received has, after
// a new leader was elected, a log that parts from the cluster's. Its data
// is replaced by a snapshot of the new leader's; the writes past the shared
// point are not dropped silently but saved here, one JSON object per line,
// for an operator to review and, if wanted, write again:
//
//	{"lsn":1042,"term":4,"type":"Put","bucket":"docs","key":"posts|…","keyBase64":"…","valueBase64":"…","time":"2026-11-16T10:15:00Z"}
//
// The values are as stored: compressed, or encrypted when the collection
// is. The file is named rejected-writes-<node>-term<term>-<UTC time>.jsonl,
// in MDDB_CLUSTER_REJECTED_DIR or next to the binlog.

// rejectedEntry is one line of the file.
type rejectedEntry struct {
	LSN         uint64 `json:"lsn"`
	Term        uint64 `json:"term"`
	Type        string `json:"type"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key,omitempty"` // when it is text
	KeyBase64   string `json:"keyBase64"`
	ValueBase64 string `json:"valueBase64,omitempty"`
	Time        string `json:"time"`
}

// saveRejected writes the log's entries after match to a new rejected-writes
// file and returns its path and how many there were. Nothing is written
// when there are none.
func (c *clusterRuntime) saveRejected(match uint64) (string, int, error) {
	bl := c.s.Binlog
	if bl.CurrentLSN() <= match {
		return "", 0, nil
	}
	dir := c.settings.rejectedDir
	if dir == "" {
		dir = filepath.Dir(bl.Stats().Path)
	}
	name := fmt.Sprintf("rejected-writes-%s-term%d-%s.jsonl", fileSafe(c.s.NodeID), bl.TermAt(bl.CurrentLSN()),
		time.Now().UTC().Format("20060102T150405.000Z"))
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	n := 0
	err = bl.ScanFrom(match, func(e *binlog.BinlogEntry) error {
		n++
		return enc.Encode(rejectedLine(e, bl.TermAt(e.LSN)))
	})
	if err == nil {
		err = w.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return path, n, err
	}
	c.rejected.Store(&path)
	return path, n, nil
}

// rejectedLine is entry e, written in term, as a line of the file.
func rejectedLine(e *binlog.BinlogEntry, term uint64) rejectedEntry {
	out := rejectedEntry{
		LSN: e.LSN, Term: term, Type: e.Type.String(), Bucket: e.BucketName,
		KeyBase64: base64.StdEncoding.EncodeToString(e.Key),
		Time:      time.Unix(0, e.Timestamp).UTC().Format(time.RFC3339Nano),
	}
	if utf8.Valid(e.Key) {
		out.Key = string(e.Key)
	}
	if e.Value != nil {
		out.ValueBase64 = base64.StdEncoding.EncodeToString(e.Value)
	}
	return out
}

// fileSafe keeps letters, digits, dot, dash and underscore of s.
func fileSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}
