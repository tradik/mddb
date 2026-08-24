package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Text encoding at the write boundary (GO-036).
//
// Documents are stored through protobuf, whose `string` fields must be valid
// UTF-8. A file in Latin-1 or Windows-1250 — which is a large share of any
// archive older than about 2010, and most Polish ones — fails deep inside
// marshalling with a message about protobuf, saying nothing about encoding and
// nothing about what to do.
//
// Someone hit this before and fixed it in the one place it hurt them
// (wiki_import.go), leaving every other write path unchanged. That is the same
// shape as the collection config being rebuilt from a partial view in three
// separate handlers: a fix on one path while the others keep the bug.
//
// The two kinds of write want opposite treatment, and this file provides both
// rather than picking one:
//
//   - **One document, chosen by a person** — upload, add, import from a URL.
//     Refuse, and say what is wrong. The user picked this file; silently
//     dropping the bytes that would not decode turns `café` into `caf` and
//     tells nobody.
//   - **A bulk import** — a wiki dump, a bulk ingest. Sanitise and count. A
//     20 GB dump must not fail because one page is in the wrong encoding, but
//     the response has to report how many documents were changed. Today it
//     reports nothing at all.
//
// "Sanitise everywhere" was rejected: quietly altering a document nobody asked
// to alter is worse than refusing it.

// ErrInvalidUTF8 describes text that cannot be stored as-is.
//
// Carries the field name and a command that fixes it, because "invalid UTF-8"
// tells a user what happened and not what to do about it.
type ErrInvalidUTF8 struct {
	Field string
}

func (e *ErrInvalidUTF8) Error() string {
	return fmt.Sprintf(
		"%s is not valid UTF-8 — the file is probably in Latin-1 or Windows-1250. "+
			"Convert it first, for example: iconv -f windows-1250 -t utf-8 file.md",
		e.Field)
}

// ValidateDocumentText refuses a document whose text cannot be stored.
//
// Checked here rather than inside each handler, so every transport inherits it
// — the lesson of the config-overwrite bug, which existed three times because
// three handlers each did their own validation.
func ValidateDocumentText(contentMD string, meta map[string][]string) error {
	if !utf8.ValidString(contentMD) {
		return &ErrInvalidUTF8{Field: "contentMd"}
	}
	for key, values := range meta {
		if !utf8.ValidString(key) {
			return &ErrInvalidUTF8{Field: fmt.Sprintf("meta key %q", truncateForError(key))}
		}
		for _, v := range values {
			if !utf8.ValidString(v) {
				return &ErrInvalidUTF8{Field: fmt.Sprintf("meta[%s]", key)}
			}
		}
	}
	return nil
}

// SanitizeDocumentText drops undecodable bytes and reports whether it changed
// anything.
//
// For bulk paths. The replacement is empty rather than U+FFFD: a run of
// replacement characters in a search index is noise that matches queries it
// should not, and the point of a bulk import is that the corpus stays usable.
func SanitizeDocumentText(contentMD string, meta map[string][]string) (string, map[string][]string, bool) {
	changed := false

	if !utf8.ValidString(contentMD) {
		contentMD = strings.ToValidUTF8(contentMD, "")
		changed = true
	}

	if len(meta) == 0 {
		return contentMD, meta, changed
	}

	// Copied only when something needs changing: the common case is a clean
	// document, and rebuilding its metadata map for nothing costs an
	// allocation per document across an entire import.
	var clean map[string][]string
	for key, values := range meta {
		cleanKey := key
		if !utf8.ValidString(key) {
			cleanKey = strings.ToValidUTF8(key, "")
			changed = true
		}

		var cleanValues []string
		for i, v := range values {
			if utf8.ValidString(v) {
				continue
			}
			if cleanValues == nil {
				cleanValues = append([]string(nil), values...)
			}
			cleanValues[i] = strings.ToValidUTF8(v, "")
			changed = true
		}

		if cleanKey != key || cleanValues != nil {
			if clean == nil {
				clean = make(map[string][]string, len(meta))
				for k, v := range meta {
					clean[k] = v
				}
			}
			if cleanKey != key {
				delete(clean, key)
			}
			if cleanValues != nil {
				clean[cleanKey] = cleanValues
			} else {
				clean[cleanKey] = values
			}
		}
	}

	if clean != nil {
		meta = clean
	}
	return contentMD, meta, changed
}

// truncateForError keeps an error message readable when the offending value is
// a whole document's worth of bytes.
func truncateForError(s string) string {
	const max = 40
	s = strings.ToValidUTF8(s, "")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
