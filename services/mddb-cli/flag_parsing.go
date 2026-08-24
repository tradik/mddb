package main

import "strings"

// Parsing the CLI's key=value flag syntax.
//
// TEST-001: this loop was written out five times — in add, search,
// vector-search, export and import-url — so a fix to one left the others
// behind. It is the format documented in the manpage for --meta and --filter:
//
//	key=val1|val2,key2=val
//
// and for --env, where a key carries a single value:
//
//	key=val,key2=val2

// parseMetaFlag parses the multi-value form used by --meta and --filter.
//
// Pairs without an "=" are skipped rather than rejected: a trailing comma is a
// typo the shell makes easy, and refusing the whole command over it would be
// worse than ignoring the empty piece. A key repeated in one flag keeps its
// last value, matching how the server treats a repeated field.
func parseMetaFlag(s string) map[string][]string {
	meta := make(map[string][]string)
	if s == "" {
		return meta
	}
	for _, pair := range strings.Split(s, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 && kv[0] != "" {
			meta[kv[0]] = strings.Split(kv[1], "|")
		}
	}
	return meta
}

// parseEnvFlag parses the single-value form used by --env for templating.
//
// The value is taken whole, so a value containing "|" or "=" survives: an
// environment value is text, not a list.
func parseEnvFlag(s string) map[string]string {
	env := make(map[string]string)
	if s == "" {
		return env
	}
	for _, pair := range strings.Split(s, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 && kv[0] != "" {
			env[kv[0]] = kv[1]
		}
	}
	return env
}
