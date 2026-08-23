package main

import (
	"reflect"
	"testing"
)

// TEST-001. This loop existed five times, so a fix to one left the others
// behind. These are the cases a user actually types.

func TestParseMetaFlag(t *testing.T) {
	cases := map[string]struct {
		in   string
		want map[string][]string
	}{
		"one key one value": {
			"lang=en",
			map[string][]string{"lang": {"en"}},
		},
		"one key many values": {
			"tag=go|rust|zig",
			map[string][]string{"tag": {"go", "rust", "zig"}},
		},
		"many keys": {
			"tag=go|rust,status=draft",
			map[string][]string{"tag": {"go", "rust"}, "status": {"draft"}},
		},
		"empty flag": {"", map[string][]string{}},
		// A trailing comma is a typo the shell makes easy. Dropping the empty
		// piece beats refusing the whole command.
		"trailing comma": {
			"tag=go,",
			map[string][]string{"tag": {"go"}},
		},
		"pair without a value separator is skipped": {
			"justakey,tag=go",
			map[string][]string{"tag": {"go"}},
		},
		"empty key is skipped": {
			"=orphan,tag=go",
			map[string][]string{"tag": {"go"}},
		},
		// The value keeps everything after the first "=", so a URL survives.
		"value containing an equals sign": {
			"url=https://x/?a=b",
			map[string][]string{"url": {"https://x/?a=b"}},
		},
		"empty value is a single empty string": {
			"tag=",
			map[string][]string{"tag": {""}},
		},
		"repeated key keeps the last": {
			"tag=go,tag=rust",
			map[string][]string{"tag": {"rust"}},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := parseMetaFlag(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseMetaFlag(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestParseEnvFlag(t *testing.T) {
	cases := map[string]struct {
		in   string
		want map[string]string
	}{
		"one pair":  {"user=ada", map[string]string{"user": "ada"}},
		"two pairs": {"user=ada,role=admin", map[string]string{"user": "ada", "role": "admin"}},
		"empty":     {"", map[string]string{}},
		// Unlike --meta, a pipe is part of the value: an environment value is
		// text, not a list.
		"pipe is not a separator": {
			"cmd=a|b",
			map[string]string{"cmd": "a|b"},
		},
		"value containing an equals sign": {
			"expr=x=y",
			map[string]string{"expr": "x=y"},
		},
		"pair without a value separator is skipped": {
			"orphan,user=ada",
			map[string]string{"user": "ada"},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := parseEnvFlag(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseEnvFlag(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// The two flags share a syntax and differ in one rule; a change to one must not
// quietly redefine the other.
func TestMetaAndEnvDisagreeOnlyAboutThePipe(t *testing.T) {
	const in = "k=a|b"

	meta := parseMetaFlag(in)
	env := parseEnvFlag(in)

	if len(meta["k"]) != 2 {
		t.Errorf("--meta split %q into %v, want two values", in, meta["k"])
	}
	if env["k"] != "a|b" {
		t.Errorf("--env split %q into %q, want it kept whole", in, env["k"])
	}
}
