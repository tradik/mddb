package spell

import "testing"

// TestSpellSuggestIgnoresPunctuation: prose punctuation around a word is not
// part of it. "document," used to come back as a misspelling of "document";
// a real typo keeps its punctuation in the corrected text.
func TestSpellSuggestIgnoresPunctuation(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	sm := NewSpellManager(db)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"document", "database"} {
		if err := sm.AddWord("col", "en", w, 10); err != nil {
			t.Fatal(err)
		}
	}

	if _, sug := sm.Suggest("col", "en", "the document, the database.", 5); len(sug) != 0 {
		t.Errorf("correct words with punctuation: got %+v, want no suggestions", sug)
	}
	corrected, sug := sm.Suggest("col", "en", "(documnt), databse!", 5)
	if len(sug) != 2 || sug[0].Original != "documnt" || sug[1].Original != "databse" {
		t.Fatalf("typos with punctuation: got %+v", sug)
	}
	if want := "(document), database!"; corrected != want {
		t.Errorf("corrected = %q, want %q", corrected, want)
	}
}

func TestSplitPunctuation(t *testing.T) {
	cases := []struct{ in, lead, word, trail string }{
		{"word", "", "word", ""},
		{"\"word,\"", "\"", "word", ",\""},
		{"don't.", "", "don't", "."},
		{"...", "...", "", ""},
		{"«słowo»", "«", "słowo", "»"},
	}
	for _, c := range cases {
		lead, word, trail := splitPunctuation(c.in)
		if lead != c.lead || word != c.word || trail != c.trail {
			t.Errorf("splitPunctuation(%q) = %q,%q,%q want %q,%q,%q", c.in, lead, word, trail, c.lead, c.word, c.trail)
		}
	}
}
