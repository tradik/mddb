package storage

import "bytes"

// DocKey is the BoltDB key for a document by collection+id.
func DocKey(coll, id string) []byte { return []byte("doc|" + coll + "|" + id) }

// byKeyTag starts every key of the "bykey" bucket.
const byKeyTag = "bykey|"

// NormalizeKey returns the form a document key or language is identified by:
// ASCII letters in lower case, every other byte as it is. It is the rule a
// document's identifier is built with, so "README.md" and "readme.md" name
// one document and, since DOC-017, one key-index entry. A string with nothing
// to change is returned as it is, without allocating.
func NormalizeKey(s string) string {
	for i := 0; i < len(s); i++ {
		if isUpperASCII(s[i]) {
			return string(AppendNormalized(make([]byte, 0, len(s)), s))
		}
	}
	return s
}

// AppendNormalized appends NormalizeKey(s) to buf, for callers building a
// key in a reused buffer.
func AppendNormalized(buf []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUpperASCII(c) {
			c += 'a' - 'A'
		}
		buf = append(buf, c)
	}
	return buf
}

func isUpperASCII(c byte) bool { return c >= 'A' && c <= 'Z' }

// ByKeyKey is the secondary-index key mapping collection+key+lang to a doc id.
//
// The key and language are normalised (NormalizeKey) and the collection is
// not: collections are case-sensitive, and "Blog" and "blog" are two. Every
// read and write of the index goes through this function or the prefixes
// below, so no spelling of a key reaches the index raw (DOC-017).
func ByKeyKey(coll, key, lang string) []byte {
	return AppendByKeyKey(nil, coll, key, lang)
}

// AppendByKeyKey appends ByKeyKey(coll, key, lang) to buf, for callers that
// build many keys in one reused buffer.
func AppendByKeyKey(buf []byte, coll, key, lang string) []byte {
	buf = append(buf, byKeyTag...)
	buf = append(buf, coll...)
	buf = append(buf, '|')
	buf = AppendNormalized(buf, key)
	buf = append(buf, '|')
	return AppendNormalized(buf, lang)
}

// ByKeyCollectionPrefix is the prefix of every key-index entry of a
// collection.
func ByKeyCollectionPrefix(coll string) []byte { return []byte(byKeyTag + coll + "|") }

// ByKeyPathPrefix is the prefix of the key-index entries of a collection
// whose key starts with keyPrefix, in every language; keyPrefix is
// normalised. For one key in every language, pass the key followed by "|".
func ByKeyPathPrefix(coll, keyPrefix string) []byte {
	return AppendNormalized(ByKeyCollectionPrefix(coll), keyPrefix)
}

// NormalizeByKeyEntry returns the normalised form of a stored key-index
// entry — what ByKeyKey builds for it — and whether it differs from k. The
// collection is the part between the tag and the next '|', as everywhere
// else in the bucket; everything after it is the key and the language.
// Anything not shaped like an entry is returned unchanged.
func NormalizeByKeyEntry(k []byte) ([]byte, bool) {
	if !bytes.HasPrefix(k, []byte(byKeyTag)) {
		return k, false
	}
	i := bytes.IndexByte(k[len(byKeyTag):], '|')
	if i < 0 {
		return k, false
	}
	head := len(byKeyTag) + i + 1
	rest := string(k[head:])
	if NormalizeKey(rest) == rest {
		return k, false
	}
	out := make([]byte, 0, len(k))
	out = append(out, k[:head]...)
	return AppendNormalized(out, rest), true
}

// RevPrefix is the key prefix for a document's revision history.
func RevPrefix(coll, id string) []byte { return []byte("rev|" + coll + "|" + id + "|") }

// MetaKeyPrefix is the key prefix for the metadata-value index.
func MetaKeyPrefix(coll, mk, mv string) []byte {
	return []byte("meta|" + coll + "|" + mk + "|" + mv + "|")
}
