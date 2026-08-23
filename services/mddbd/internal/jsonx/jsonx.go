// Package jsonx picks a JSON implementation per direction (GO-037).
//
// Encoding uses goccy/go-json, decoding uses the standard library, and the
// split is the whole point of the package.
//
// goccy is measurably faster — GO-026 put it at 1.7× to 2.4× on this
// repository's own shapes, which is why it was adopted. GO-026 compared one
// axis, speed, and on that axis goccy wins every scenario. Fuzzing added a
// second axis nobody knew was there: goccy panics on malformed input where the
// standard library returns an error.
//
//	input: {"\0,\
//	  goccy  → panic: index out of range [9] with length 7
//	  stdlib → invalid escape sequence `\0` in string
//
// The panic is state-dependent rather than input-dependent, which is what made
// it so unpleasant to find. A single bad document reproduces nothing; in a
// mixed sequence of six inputs it fires on roughly 5% of calls, because the
// fault depends on state the previous decode left behind. FuzzLoadDoc found it
// by killing the machine it was running on.
//
// So the rule:
//
//   - **Encoding** takes structs this process built. They cannot be malformed,
//     goccy cannot panic on them, and it is twice as fast — so encoding stays
//     on goccy.
//   - **Decoding** takes bytes this process did not write: an HTTP request
//     body, a replication entry, a stored document read back after a restore,
//     a response from an embedding provider. Any of those can be corrupt, and
//     none of them is worth a crashed process. Decoding uses stdlib.
//
// Enforced by construction rather than by review: this package does not export
// a goccy decoder, so no caller can reach for one by accident.
//
// Go 1.27's encoding/json is backed by the v2 engine, and this is deliberately
// the v1-compatible surface rather than encoding/json/v2 directly: v2 does not
// sort map keys, so the same input would stop producing the same bytes — which
// the replication binlog depends on.
//
// internal/jsonbench pins that both implementations produce identical bytes for
// this repository's structures, which is what makes the split safe to make.
package jsonx

import (
	stdjson "encoding/json"
	"io"

	goccy "github.com/goccy/go-json"
)

// RawMessage is the standard library's, so a value decoded here and re-encoded
// on the way out travels through one type.
type RawMessage = stdjson.RawMessage

// Marshal encodes a value. goccy: this process built the value, so it cannot
// be malformed, and goccy is roughly twice as fast.
func Marshal(v any) ([]byte, error) {
	return goccy.Marshal(v)
}

// MarshalIndent encodes a value with indentation.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return goccy.MarshalIndent(v, prefix, indent)
}

// NewEncoder returns an encoder writing to w.
func NewEncoder(w io.Writer) *goccy.Encoder {
	return goccy.NewEncoder(w)
}

// Unmarshal decodes bytes into v.
//
// Standard library, unconditionally. These bytes came from somewhere else, and
// an error is an outcome the caller can handle while a panic is not.
func Unmarshal(data []byte, v any) error {
	return stdjson.Unmarshal(data, v)
}

// NewDecoder returns a decoder reading from r, for the same reason as
// Unmarshal.
func NewDecoder(r io.Reader) *stdjson.Decoder {
	return stdjson.NewDecoder(r)
}

// Valid reports whether data is valid JSON.
func Valid(data []byte) bool {
	return stdjson.Valid(data)
}
