package main

// KeyBuilder provides efficient key construction without string allocations
type KeyBuilder struct {
	buf [512]byte
}

// BuildDocKey builds a document key: doc|collection|id
func (kb *KeyBuilder) BuildDocKey(coll, id string) []byte {
	n := 0
	n += copy(kb.buf[n:], "doc|")
	n += copy(kb.buf[n:], coll)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], id)
	return kb.buf[:n]
}

// BuildByKey builds a bykey index key: bykey|collection|key|lang
func (kb *KeyBuilder) BuildByKey(coll, key, lang string) []byte {
	n := 0
	n += copy(kb.buf[n:], "bykey|")
	n += copy(kb.buf[n:], coll)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], key)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], lang)
	return kb.buf[:n]
}

// BuildRevPrefix builds a revision key prefix: rev|collection|docID|
func (kb *KeyBuilder) BuildRevPrefix(coll, id string) []byte {
	n := 0
	n += copy(kb.buf[n:], "rev|")
	n += copy(kb.buf[n:], coll)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], id)
	kb.buf[n] = '|'
	n++
	return kb.buf[:n]
}

// BuildRevKey builds a complete revision key: rev|collection|docID|timestamp
func (kb *KeyBuilder) BuildRevKey(coll, id string, timestamp int64) []byte {
	n := 0
	n += copy(kb.buf[n:], "rev|")
	n += copy(kb.buf[n:], coll)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], id)
	kb.buf[n] = '|'
	n++
	
	// Use optimized timestamp formatting
	tsBytes := FormatTimestamp(timestamp, kb.buf[n:n+20])
	n += len(tsBytes)
	
	return kb.buf[:n]
}

// BuildMetaKeyPrefix builds a metadata key prefix: meta|collection|key|value|
func (kb *KeyBuilder) BuildMetaKeyPrefix(coll, mk, mv string) []byte {
	n := 0
	n += copy(kb.buf[n:], "meta|")
	n += copy(kb.buf[n:], coll)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], mk)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], mv)
	kb.buf[n] = '|'
	n++
	return kb.buf[:n]
}

// BuildMetaKey builds a complete metadata key: meta|collection|key|value|docID
func (kb *KeyBuilder) BuildMetaKey(coll, mk, mv, docID string) []byte {
	n := 0
	n += copy(kb.buf[n:], "meta|")
	n += copy(kb.buf[n:], coll)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], mk)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], mv)
	kb.buf[n] = '|'
	n++
	n += copy(kb.buf[n:], docID)
	return kb.buf[:n]
}

// Reset clears the buffer (optional, for reuse)
func (kb *KeyBuilder) Reset() {
	// Buffer is reused automatically, no need to clear
}
