package binlog

import (
	"bytes"
	"testing"
)

// TEST-003. A binlog entry is the one structure a follower decodes from bytes
// a *different machine* wrote. A corrupt or truncated entry — a half-written
// file after a crash, a partial network read, a leader on a different version —
// must be rejected, never panic the follower.

func FuzzUnmarshalBinlogEntry(f *testing.F) {
	f.Add(MarshalBinlogEntry(&BinlogEntry{
		LSN: 1, Type: BinlogPut, BucketName: "docs",
		Key: []byte("doc|c|1"), Value: []byte("payload"),
	}))
	f.Add(MarshalBinlogEntry(&BinlogEntry{
		LSN: 1 << 60, Type: BinlogDelete, BucketName: "",
		Key: nil, Value: nil,
	}))
	f.Add(MarshalBinlogEntry(&BinlogEntry{}))
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 1})
	// Header present, then a length prefix claiming far more than follows.
	f.Add(append(make([]byte, 16), 0xff, 0xff, 0xff, 0xff))

	f.Fuzz(func(t *testing.T, data []byte) {
		entry, n, err := UnmarshalBinlogEntry(data)
		if err != nil {
			return
		}
		if entry == nil {
			t.Fatal("decoder returned neither an entry nor an error")
		}
		// The byte count drives the caller's advance through the file. A
		// value outside the input would make a reader either skip entries or
		// loop forever on the same one.
		if n <= 0 || n > len(data) {
			t.Fatalf("decoder consumed %d bytes of a %d-byte input", n, len(data))
		}
	})
}

// A follower must reconstruct exactly what the leader wrote. Fuzzing the round
// trip catches a field whose written size and read size disagree — which
// silently shifts every following entry in the stream.
func FuzzBinlogEntryRoundTrip(f *testing.F) {
	f.Add(uint64(1), uint8(0), "docs", []byte("k"), []byte("v"))
	f.Add(uint64(0), uint8(1), "", []byte(nil), []byte(nil))
	f.Add(^uint64(0), uint8(2), "a-very-long-bucket-name", []byte{0, 1, 2}, []byte{255})

	f.Fuzz(func(t *testing.T, lsn uint64, typ uint8, bucket string, key, value []byte) {
		original := &BinlogEntry{
			LSN: lsn, Type: BinlogEntryType(typ),
			BucketName: bucket, Key: key, Value: value,
		}
		encoded := MarshalBinlogEntry(original)

		got, n, err := UnmarshalBinlogEntry(encoded)
		if err != nil {
			t.Fatalf("an entry this encoder produced does not decode: %v", err)
		}
		if n != len(encoded) {
			t.Fatalf("decoder consumed %d of %d bytes — a stream reader would desynchronise", n, len(encoded))
		}
		if got.LSN != lsn || got.Type != BinlogEntryType(typ) || got.BucketName != bucket {
			t.Fatalf("header changed:\n wrote %d/%d/%q\n read  %d/%d/%q",
				lsn, typ, bucket, got.LSN, got.Type, got.BucketName)
		}
		if !bytes.Equal(got.Key, key) {
			t.Fatalf("key changed: wrote %q, read %q", key, got.Key)
		}
		if !bytes.Equal(got.Value, value) {
			t.Fatalf("value changed: wrote %q, read %q", value, got.Value)
		}
	})
}

// A stream of entries must decode as that same stream. This is the property a
// follower actually depends on: one bad length turns entry N+1 into garbage.
func FuzzBinlogStream(f *testing.F) {
	f.Add([]byte("abc"), []byte("def"), 3)

	f.Fuzz(func(t *testing.T, key, value []byte, count int) {
		if count < 1 || count > 32 {
			return
		}

		var stream []byte
		for i := range count {
			stream = append(stream, MarshalBinlogEntry(&BinlogEntry{
				LSN: uint64(i), Type: BinlogPut, BucketName: "docs",
				Key: key, Value: value,
			})...)
		}

		pos, decoded := 0, 0
		for pos < len(stream) {
			entry, n, err := UnmarshalBinlogEntry(stream[pos:])
			if err != nil {
				t.Fatalf("entry %d of %d failed to decode at offset %d: %v", decoded, count, pos, err)
			}
			if entry.LSN != uint64(decoded) {
				t.Fatalf("entry %d carries LSN %d — the stream desynchronised", decoded, entry.LSN)
			}
			pos += n
			decoded++
		}
		if decoded != count {
			t.Fatalf("wrote %d entries, read back %d", count, decoded)
		}
	})
}
