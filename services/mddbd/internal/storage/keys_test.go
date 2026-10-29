package storage

import "testing"

func TestNormalizeKeyLowersASCIIOnly(t *testing.T) {
	cases := map[string]string{
		"README.md":      "readme.md",
		"already-lower":  "already-lower",
		"":               "",
		"Zażółć.MD":      "zażółć.md", // non-ASCII is left as the identifier leaves it
		"ŻÓŁW":           "ŻÓŁw",
		"Path/To|Pipe.X": "path/to|pipe.x",
	}
	for in, want := range cases {
		if got := NormalizeKey(in); got != want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeKeyDoesNotAllocateWhenNothingChanges(t *testing.T) {
	s := "already-lower/path.md"
	if n := testing.AllocsPerRun(100, func() { _ = NormalizeKey(s) }); n != 0 {
		t.Errorf("NormalizeKey allocated %v times for a key already in normal form", n)
	}
}

func TestNormalizeByKeyEntry(t *testing.T) {
	cases := []struct {
		in, want string
		changed  bool
	}{
		{"bykey|docs|README.md|EN", "bykey|docs|readme.md|en", true},
		// The collection keeps its case; only what follows it is normalised.
		{"bykey|Notes|TODO.md|pl", "bykey|Notes|todo.md|pl", true},
		{"bykey|Notes|todo.md|pl", "bykey|Notes|todo.md|pl", false},
		// A key containing '|' is still all key-and-language.
		{"bykey|c|A|B|EN", "bykey|c|a|b|en", true},
		// Not an entry: unchanged.
		{"doc|c|README", "doc|c|README", false},
		{"bykey|NOPIPE", "bykey|NOPIPE", false},
	}
	for _, c := range cases {
		got, changed := NormalizeByKeyEntry([]byte(c.in))
		if string(got) != c.want || changed != c.changed {
			t.Errorf("NormalizeByKeyEntry(%q) = %q, %v; want %q, %v", c.in, got, changed, c.want, c.changed)
		}
	}
}

// Whatever ByKeyKey builds, NormalizeByKeyEntry leaves alone, and whatever
// NormalizeByKeyEntry makes of a raw entry is what ByKeyKey builds: the
// migration and the live code agree on one form.
func TestByKeyKeyAndTheMigrationAgree(t *testing.T) {
	for _, kl := range [][2]string{{"README.md", "en"}, {"Guide/Intro.MD", "EN"}, {"a|B", "Pl"}} {
		built := ByKeyKey("Coll", kl[0], kl[1])
		if _, changed := NormalizeByKeyEntry(built); changed {
			t.Errorf("ByKeyKey built %q, which the migration would rewrite", built)
		}
		raw := []byte("bykey|Coll|" + kl[0] + "|" + kl[1])
		if got, _ := NormalizeByKeyEntry(raw); string(got) != string(built) {
			t.Errorf("migrating %q gives %q, ByKeyKey builds %q", raw, got, built)
		}
	}
}
