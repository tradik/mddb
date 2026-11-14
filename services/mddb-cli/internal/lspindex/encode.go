package lspindex

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The meta keys of semantic code facts, format v1. The format is defined by
// testdata/semantic-facts/README.md and its reference encoder; mddbd
// validates the same keys with its own implementation.
const (
	KeyIndex = "symIndex"
	KeyDefs  = "symDefs"
	KeyRefs  = "symRefs"
	KeyImpls = "symImpls"

	formatVersion = "v1"
	// maxRefLines is how many lines one symRefs value carries; more continue
	// in another value with the same target.
	maxRefLines = 64
	// maxValueBytes is the longest value the server keeps.
	maxValueBytes = 1024
)

// factKinds are the kinds the format accepts.
var factKinds = map[string]bool{
	"function": true, "method": true, "class": true, "interface": true,
	"struct": true, "type": true, "enum": true, "constructor": true,
}

// Encode turns one file's facts into the four meta keys. content is the
// document's content exactly as it will be stored (its hash binds the facts
// to it) and source is Result.Server. Empty keys are left out; symIndex is
// always present, so a file with nothing to say is still marked as indexed.
// Index only returns facts that encode under the format's rules.
func Encode(content, source string, facts *FileFacts) map[string][]string {
	out := map[string][]string{}
	if facts != nil {
		var defs, refs, impls []string
		for _, d := range facts.Defs {
			defs = append(defs, defValue(d))
		}
		for _, r := range facts.Refs {
			refs = append(refs, refValues(r)...)
		}
		for _, i := range facts.Impls {
			impls = append(impls, implValue(i))
		}
		for key, vals := range map[string][]string{KeyDefs: defs, KeyRefs: refs, KeyImpls: impls} {
			if len(vals) > 0 {
				sort.Strings(vals)
				out[key] = vals
			}
		}
	}
	out[KeyIndex] = []string{strings.Join([]string{formatVersion, hash12(content), factsDigest(out), source}, " ")}
	return out
}

// factsDigest lets the caller see on the next run whether the facts changed
// while the content did not: the hash over every value, keys in a fixed
// order, values sorted.
func factsDigest(meta map[string][]string) string {
	var b strings.Builder
	for _, key := range []string{KeyDefs, KeyRefs, KeyImpls} {
		for _, v := range meta[key] {
			b.WriteString(key + "\t" + v + "\n")
		}
	}
	return hash12(b.String())
}

// hash12 is the first 12 hex digits of SHA-256.
func hash12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// defValue is `<name> <kind> <start>-<end>[ <container>]`.
func defValue(d Def) string {
	v := d.Name + " " + d.Kind + " " + strconv.Itoa(d.Start) + "-" + strconv.Itoa(d.End)
	if d.Container != "" {
		v += " " + d.Container
	}
	return v
}

// refValues is `<target> <line>,<line>,...`, at most maxRefLines lines per
// value. Lines are made ascending and unique here, so a caller building Refs
// by hand cannot produce a value the server drops.
func refValues(r Ref) []string {
	seen := map[int]bool{}
	var lines []int
	for _, n := range r.Lines {
		if !seen[n] {
			seen[n] = true
			lines = append(lines, n)
		}
	}
	sort.Ints(lines)
	var out []string
	for i := 0; i < len(lines); i += maxRefLines {
		chunk := lines[i:min(i+maxRefLines, len(lines))]
		nums := make([]string, len(chunk))
		for j, n := range chunk {
			nums[j] = strconv.Itoa(n)
		}
		out = append(out, r.Target+" "+strings.Join(nums, ","))
	}
	return out
}

// implValue is `<target> <local>`.
func implValue(i Impl) string {
	return i.Target + " " + i.Local
}

// validName reports whether s may be a name, kind, container or source: not
// empty, no whitespace, '|' or '#'.
func validName(s string) bool {
	return s != "" && !strings.ContainsAny(s, "|#") && !hasSpace(s)
}

// validPath reports whether s may be a document path: not empty, no
// whitespace or '|'. A path may contain '#'; the name follows the last one.
func validPath(s string) bool {
	return s != "" && !strings.Contains(s, "|") && !hasSpace(s)
}

// hasSpace matches Python's \s on str, as the reference encoder does: Unicode
// spaces and the separators U+001C..U+001F, which str.isspace also counts.
func hasSpace(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
	}) >= 0
}

// defEncodable reports whether d encodes to a value the server keeps.
func defEncodable(d Def) bool {
	return validName(d.Name) && factKinds[d.Kind] && d.Start >= 1 && d.Start <= d.End &&
		(d.Container == "" || validName(d.Container)) && len(defValue(d)) <= maxValueBytes
}

// refEncodable reports whether every value of r is one the server keeps.
func refEncodable(r Ref) bool {
	if !targetValid(r.Target) || len(r.Lines) == 0 {
		return false
	}
	for _, v := range refValues(r) {
		if len(v) > maxValueBytes {
			return false
		}
	}
	return true
}

// implEncodable reports whether i encodes to a value the server keeps.
func implEncodable(i Impl) bool {
	return targetValid(i.Target) && validName(i.Local) && len(implValue(i)) <= maxValueBytes
}

// targetValid checks `<path>#<name>`; the name follows the last '#'.
func targetValid(target string) bool {
	cut := strings.LastIndex(target, "#")
	return cut > 0 && validPath(target[:cut]) && validName(target[cut+1:])
}
