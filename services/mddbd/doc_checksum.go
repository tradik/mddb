package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// The checksum of a collection's documents: GET /v1/checksum, and the
// checksum column of /v1/stats, gRPC and MCP stats.
//
// It used to XOR together, per document, the CRC32 of the key and of the
// first 64 bytes of the value. CRC32 is linear, so XORing many of them
// cancels out: a thousand documents whose keys differ only in a counter
// added nothing at all, and a follower missing its last 1,000 documents had
// the leader's checksum. It also never looked past byte 64 of a document.
//
// Now every document contributes a 64-bit value derived from its whole key
// (collection, key and language) and its whole stored value (content,
// metadata, revision and timestamps, as stored), passed through a
// non-linear mixer, and the contributions are added. Adding is independent
// of the order the documents are read in, so the stats walk and the
// per-collection read agree; the mixer makes the contributions behave as
// independent random numbers, so documents do not cancel each other out.
// Two nodes holding the same documents byte for byte have the same
// checksum; any document added, removed or changed changes it, except with
// a chance of about one in 2^64. It is a replica comparison, not a
// cryptographic digest.

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// docSetSum accumulates the checksum of a set of documents.
type docSetSum uint64

// add counts the document stored under key with value v.
func (d *docSetSum) add(key, v []byte) {
	var buf [16]byte
	binary.BigEndian.PutUint32(buf[0:], crc32.Checksum(key, castagnoli))
	binary.BigEndian.PutUint32(buf[4:], crc32.Checksum(v, castagnoli))
	binary.BigEndian.PutUint64(buf[8:], uint64(len(key))<<32|uint64(len(v)))
	*d += docSetSum(mix64(binary.BigEndian.Uint64(buf[:8])) ^ mix64(binary.BigEndian.Uint64(buf[8:])+0x9e3779b97f4a7c15))
}

// String is the checksum as the API returns it: 16 hex digits.
func (d docSetSum) String() string {
	return fmt.Sprintf("%016x", uint64(d))
}

// mix64 is the SplitMix64 finalizer: a bijection whose output bits each
// depend on every input bit.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
