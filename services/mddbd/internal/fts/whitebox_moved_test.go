package fts

import (
	"os"
	"sort"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestNormalizeAutocompletePrefix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Mark", "mark"},
		{"mark*", "mark"},
		{"mar d", "mar"}, // stop at first separator
		{"  ", ""},
		{"ABC123", "abc123"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := normalizeAutocompletePrefix(tc.in); got != tc.want {
			t.Errorf("normalize(%q)=%q; want %q", tc.in, got, tc.want)
		}
	}
}
func TestWildcardMatch(t *testing.T) {
	tests := []struct {
		pattern string
		text    string
		want    bool
	}{
		{"prog*", "programming", true},
		{"prog*", "program", true},
		{"prog*", "pro", false},
		{"te?t", "test", true},
		{"te?t", "text", true},
		{"te?t", "tet", false},
		{"*ing", "programming", true},
		{"*ing", "running", true},
		{"*ing", "run", false},
		{"*", "anything", true},
		{"?", "a", true},
		{"?", "ab", false},
		{"p*m*g", "programming", true},
		{"p*m*g", "prog", false},
	}

	for _, tt := range tests {
		got := wildcardMatch(tt.pattern, tt.text)
		if got != tt.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
		}
	}
}
func TestPositionalIndex_EncodeDecodePositions(t *testing.T) {
	positions := []uint32{0, 3, 7, 15}
	encoded := encodePositions(positions)
	decoded := decodePositions(encoded)

	if len(decoded) != len(positions) {
		t.Fatalf("expected %d positions, got %d", len(positions), len(decoded))
	}
	for i, p := range positions {
		if decoded[i] != p {
			t.Fatalf("position[%d]: expected %d, got %d", i, p, decoded[i])
		}
	}
}
func TestTokenize_Basics(t *testing.T) {
	cases := []struct {
		in  string
		out []tokenType
	}{
		{"rust", []tokenType{tokTerm, tokEOF}},
		{"rust AND performance", []tokenType{tokTerm, tokAnd, tokTerm, tokEOF}},
		{"a OR b", []tokenType{tokTerm, tokOr, tokTerm, tokEOF}},
		{"NOT spam", []tokenType{tokNot, tokTerm, tokEOF}},
		{"-spam", []tokenType{tokNot, tokTerm, tokEOF}},
		{"+must have", []tokenType{tokRequire, tokTerm, tokTerm, tokEOF}},
		{`"machine learning"`, []tokenType{tokPhrase, tokEOF}},
		{`"machine learning"~5`, []tokenType{tokProximity, tokEOF}},
		{"mark*", []tokenType{tokWildcard, tokEOF}},
		{"color~1", []tokenType{tokFuzzy, tokEOF}},
		{"(a OR b) AND c", []tokenType{tokLParen, tokTerm, tokOr, tokTerm, tokRParen, tokAnd, tokTerm, tokEOF}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := tokenize(tc.in)
			if err != nil {
				t.Fatalf("tokenize(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.out) {
				t.Fatalf("len(tokens)=%d want %d: %+v", len(got), len(tc.out), got)
			}
			for i, tok := range got {
				if tok.typ != tc.out[i] {
					t.Errorf("token %d: type=%d want %d (payload=%q)", i, tok.typ, tc.out[i], tok.s)
				}
			}
		})
	}
}
func TestTokenize_FuzzyAndProximityPayload(t *testing.T) {
	toks, err := tokenize("color~2")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) < 1 || toks[0].typ != tokFuzzy || toks[0].s != "color" || toks[0].n != 2 {
		t.Errorf("fuzzy payload mismatch: %+v", toks[0])
	}
	toks, err = tokenize(`"rust systems"~7`)
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) < 1 || toks[0].typ != tokProximity || toks[0].s != "rust systems" || toks[0].n != 7 {
		t.Errorf("proximity payload mismatch: %+v", toks[0])
	}
}
func TestIsConsonant(t *testing.T) {
	tests := []struct {
		name string
		word string
		idx  int
		want bool
	}{
		{"vowel_a", "apple", 0, false},
		{"vowel_e", "hello", 1, false},
		{"vowel_i", "bit", 1, false},
		{"vowel_o", "top", 1, false},
		{"vowel_u", "cup", 1, false},
		{"consonant_b", "bat", 0, true},
		{"consonant_t", "bat", 2, true},
		{"y_at_start", "yes", 0, true},
		{"y_after_consonant", "byte", 1, false},
		{"y_after_vowel", "day", 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConsonant([]byte(tt.word), tt.idx)
			if got != tt.want {
				t.Errorf("isConsonant(%q, %d) = %v, want %v", tt.word, tt.idx, got, tt.want)
			}
		})
	}
}
func TestMeasure(t *testing.T) {
	tests := []struct {
		word string
		want int
	}{
		{"", 0},
		{"a", 0},        // V
		{"b", 0},        // C
		{"ab", 1},       // VC = (VC){1}
		{"tr", 0},       // CC
		{"tree", 0},     // CCVV
		{"trouble", 1},  // CC V C V C V = (VC){1}
		{"oats", 1},     // V C V C
		{"trees", 1},    // CC V V C
		{"troubles", 2}, // CC V C V C V C
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := measure([]byte(tt.word))
			if got != tt.want {
				t.Errorf("measure(%q) = %d, want %d", tt.word, got, tt.want)
			}
		})
	}
}
func TestHasVowel(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"bcd", false},
		{"abc", true},
		{"xyz", true}, // y after x (consonant) is a vowel
		{"yell", true},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := hasVowel([]byte(tt.word))
			if got != tt.want {
				t.Errorf("hasVowel(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}
func TestEndsCVC(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"hop", true},  // h-o-p, CVC, p not w/x/y
		{"lov", true},  // l-o-v, CVC
		{"bow", false}, // ends with w
		{"box", false}, // ends with x
		{"boy", false}, // ends with y
		{"ab", false},  // too short
		{"a", false},   // too short
		{"", false},    // empty
		{"oat", false}, // o is vowel at position 0, a is vowel at 1 => not CVC
		{"bat", true},  // b-a-t CVC
		{"pet", true},  // p-e-t CVC
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := endsCVC([]byte(tt.word))
			if got != tt.want {
				t.Errorf("endsCVC(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}
func TestRemoveSuffix(t *testing.T) {
	tests := []struct {
		word string
		n    int
		want string
	}{
		{"running", 3, "runn"},
		{"tested", 2, "test"},
		{"abc", 0, "abc"},
		{"abc", 3, ""},
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := string(removeSuffix([]byte(tt.word), tt.n))
			if got != tt.want {
				t.Errorf("removeSuffix(%q, %d) = %q, want %q", tt.word, tt.n, got, tt.want)
			}
		})
	}
}
func TestStep1a(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"caresses", "caress"}, // SSES -> SS
		{"ponies", "poni"},     // IES -> I
		{"caress", "caress"},   // SS -> SS
		{"cats", "cat"},        // S -> (remove)
		{"cat", "cat"},         // no suffix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step1a([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step1a(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestStep1b(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"feed", "feed"},          // EED with m=0 -> unchanged
		{"agreed", "agree"},       // EED with m>0 -> EE
		{"plastered", "plaster"},  // ED with vowel in stem
		{"bled", "bled"},          // ED without vowel in stem
		{"motoring", "motor"},     // ING with vowel in stem
		{"sing", "sing"},          // ING without vowel in stem
		{"conflated", "conflate"}, // ED -> stem ends "at" -> add e
		{"troubled", "trouble"},   // ED -> stem ends with double (ll) but l is exempt
		{"hopping", "hop"},        // ING -> stem ends with double (pp) -> remove last
		{"filing", "file"},        // ING -> m=1, CVC -> add e
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step1b([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step1b(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestWildcardMatchCoverage(t *testing.T) {
	tests := []struct {
		pattern string
		text    string
		want    bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"?", "a", true},
		{"?", "", false},
		{"?", "ab", false},
		{"hello", "hello", true},
		{"hello", "world", false},
		{"hel*", "hello", true},
		{"hel*", "help", true},
		{"hel*", "he", false},
		{"*lo", "hello", true},
		{"*lo", "lo", true},
		{"*lo", "low", false},
		{"h?llo", "hello", true},
		{"h?llo", "hallo", true},
		{"h?llo", "hllo", false},
		{"h*o", "hello", true},
		{"h*o", "ho", true},
		{"h*o", "hey", false},
		{"*a*b*", "aXYZb", true},
		{"*a*b*", "xaxbx", true},
		{"*a*b*", "xyz", false},
		{"", "", true},
		{"", "a", false},
		{"**", "abc", true},
		{"a*b*c", "abc", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXbY", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.text, func(t *testing.T) {
			got := wildcardMatch(tt.pattern, tt.text)
			if got != tt.want {
				t.Errorf("wildcardMatch(%q, %q) = %v, want %v", tt.pattern, tt.text, got, tt.want)
			}
		})
	}
}
func TestDecodeCollectionStatsEmpty(t *testing.T) {
	cs := decodeCollectionStats(nil)
	if cs.TotalDocs != 0 || cs.TotalTerms != 0 {
		t.Errorf("nil should decode to zeros: %+v", cs)
	}
	cs2 := decodeCollectionStats([]byte(""))
	if cs2.TotalDocs != 0 {
		t.Errorf("empty should decode to zeros: %+v", cs2)
	}
}
func TestDecodeCollectionStatsValid(t *testing.T) {
	cs := collectionStats{TotalDocs: 42, TotalTerms: 1000}
	encoded := encodeCollectionStats(cs)
	decoded := decodeCollectionStats(encoded)
	if decoded.TotalDocs != 42 {
		t.Errorf("TotalDocs: got %d, want 42", decoded.TotalDocs)
	}
	if decoded.TotalTerms != 1000 {
		t.Errorf("TotalTerms: got %d, want 1000", decoded.TotalTerms)
	}
}
func TestEncodeDecodePositions(t *testing.T) {
	positions := []uint32{1, 5, 10, 20, 100}
	encoded := encodePositions(positions)
	decoded := decodePositions(encoded)
	if len(decoded) != len(positions) {
		t.Fatalf("length mismatch: %d vs %d", len(decoded), len(positions))
	}
	for i, p := range positions {
		if decoded[i] != p {
			t.Errorf("position[%d]: got %d, want %d", i, decoded[i], p)
		}
	}
}
func TestDecodePositionsEmpty(t *testing.T) {
	decoded := decodePositions(nil)
	if len(decoded) != 0 {
		t.Errorf("nil should decode to empty, got %d", len(decoded))
	}
	decoded2 := decodePositions([]byte{})
	if len(decoded2) != 0 {
		t.Errorf("empty should decode to empty, got %d", len(decoded2))
	}
}
func TestEndsWithDouble(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"fall", true},
		{"miss", true},
		{"buzz", true},
		{"cat", false},
		{"a", false},
		{"", false},
		{"bee", false}, // ee are vowels, not consonants
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got := endsWithDouble([]byte(tt.word))
			if got != tt.want {
				t.Errorf("endsWithDouble(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}
func TestStep1c(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"happy", "happi"}, // Y with vowel in stem -> I
		{"sky", "sky"},     // Y without vowel in stem -> unchanged
		{"cat", "cat"},     // no Y suffix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step1c([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step1c(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestStep2(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"relational", "relate"},     // ational -> ate
		{"conditional", "condition"}, // tional -> tion
		{"valenci", "valence"},       // enci -> ence
		{"hesitanci", "hesitance"},   // anci -> ance
		{"digitizer", "digitize"},    // izer -> ize
		{"formalli", "formal"},       // alli -> al
		{"cat", "cat"},               // no matching suffix
		// m=0 stem should not apply
		{"ational", "ational"}, // stem "at" has m=0
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step2([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step2(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestStep3(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"triplicate", "triplic"},   // icate -> ic
		{"formative", "form"},       // ative -> ""
		{"formalize", "formal"},     // alize -> al
		{"electriciti", "electric"}, // iciti -> ic
		{"electrical", "electric"},  // ical -> ic
		{"hopeful", "hope"},         // ful -> ""
		{"goodness", "good"},        // ness -> ""
		{"cat", "cat"},              // no matching suffix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step3([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step3(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestStep4(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"revival", "reviv"},      // al with m>1
		{"allowance", "allow"},    // ance with m>1
		{"inference", "infer"},    // ence with m>1
		{"adjustable", "adjust"},  // able with m>1
		{"adoption", "adopt"},     // ion with t preceding, m>1
		{"impression", "impress"}, // ion with s preceding, m>1
		{"activate", "activ"},     // ate with m>1
		{"cat", "cat"},            // no matching suffix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step4([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step4(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestStep5a(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"probate", "probat"}, // m>1, remove e
		{"rate", "rate"},      // m=1, CVC -> keep e
		{"cease", "ceas"},     // m>1
		{"cat", "cat"},        // no e suffix
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step5a([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step5a(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestStep5b(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"controll", "control"}, // m>1, double l -> single l
		{"roll", "roll"},        // m=1 -> unchanged
		{"cat", "cat"},          // no double ending
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := string(step5b([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("step5b(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestSnowballStemmer_SatisfiesInterface(t *testing.T) {
	stemmer := newSnowballStemmer("de")
	if stemmer == nil {
		t.Fatal("newSnowballStemmer('de') returned nil")
	}
	var s Stemmer = stemmer
	// German: "Häuser" (houses) should stem
	result := s.Stem("häuser")
	if result == "häuser" {
		t.Errorf("German stemmer did not modify 'häuser'")
	}
}
func TestSnowballStemmer_UnsupportedLanguage(t *testing.T) {
	stemmer := newSnowballStemmer("xx")
	if stemmer != nil {
		t.Error("newSnowballStemmer('xx') should return nil for unsupported language")
	}
}
func TestPolishStopWords(t *testing.T) {
	// Common Polish stop words that should be filtered (min 2 chars, since tokenizer requires len>=2)
	stopWords := []string{"na", "że", "jest", "nie", "do", "to", "jak", "ale", "czy", "lub"}
	for _, w := range stopWords {
		if !defaultStopWordsPL[w] {
			t.Errorf("expected %q to be a Polish stop word", w)
		}
	}

	// Content words that should NOT be stop words
	contentWords := []string{"programowanie", "komputer", "baza", "danych", "wyszukiwanie"}
	for _, w := range contentWords {
		if defaultStopWordsPL[w] {
			t.Errorf("expected %q to NOT be a Polish stop word", w)
		}
	}
}
func TestGermanStemming(t *testing.T) {
	stemmer := newSnowballStemmer("de")
	if stemmer == nil {
		t.Fatal("German stemmer not available")
	}

	// German words should be modified
	tests := []string{"häuser", "laufen", "kinder", "programmierung"}
	for _, word := range tests {
		result := stemmer.Stem(word)
		if result == word {
			t.Errorf("German stemmer did not modify %q", word)
		}
	}
}
func TestFrenchStemming(t *testing.T) {
	stemmer := newSnowballStemmer("fr")
	if stemmer == nil {
		t.Fatal("French stemmer not available")
	}

	result := stemmer.Stem("maisons")
	if result == "maisons" {
		t.Error("French stemmer did not modify 'maisons'")
	}
}
func TestSpanishStemming(t *testing.T) {
	stemmer := newSnowballStemmer("es")
	if stemmer == nil {
		t.Fatal("Spanish stemmer not available")
	}

	result := stemmer.Stem("casas")
	if result == "casas" {
		t.Error("Spanish stemmer did not modify 'casas'")
	}
}
func TestRussianStemming(t *testing.T) {
	stemmer := newSnowballStemmer("ru")
	if stemmer == nil {
		t.Fatal("Russian stemmer not available")
	}

	result := stemmer.Stem("домов")
	if result == "домов" {
		t.Error("Russian stemmer did not modify 'домов'")
	}
}
func TestResolveLang_NoRegistry(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	idx := NewFTSIndex(db)
	idx.SetStemmer(NewPorterStemmer())

	stemmer, stopWords := idx.resolveLang("pl")
	// Without registry, should fall back to defaults
	if stemmer == nil {
		t.Error("expected fallback stemmer")
	}
	if stopWords == nil {
		t.Error("expected fallback stop words")
	}
}
func TestResolveLang_WithRegistry(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	idx := NewFTSIndex(db)
	reg := NewLangRegistry("en")
	RegisterDefaultLanguages(reg)
	idx.SetLangRegistry(reg)

	stemmer, stopWords := idx.resolveLang("pl")
	if stemmer == nil {
		t.Error("expected Polish stemmer")
	}
	// Polish stop words should include "jest"
	if !stopWords["jest"] {
		t.Error("expected Polish stop words to include 'jest'")
	}
	// And should NOT include English "the"
	if stopWords["the"] {
		t.Error("Polish stop words should not include 'the'")
	}
}
func TestStopWords_PerLanguage(t *testing.T) {
	tests := []struct {
		lang      string
		stopWords map[string]bool
		expected  []string
	}{
		{"de", defaultStopWordsDE, []string{"der", "die", "das", "und", "ist"}},
		{"fr", defaultStopWordsFR, []string{"le", "la", "les", "de", "et"}},
		{"es", defaultStopWordsES, []string{"el", "la", "los", "de", "en"}},
		{"it", defaultStopWordsIT, []string{"il", "la", "le", "di", "che"}},
		{"ru", defaultStopWordsRU, []string{"и", "в", "на", "не", "он"}},
		{"sv", defaultStopWordsSV, []string{"och", "att", "den", "för", "med"}},
	}

	for _, tc := range tests {
		for _, word := range tc.expected {
			if !tc.stopWords[word] {
				t.Errorf("[%s] expected %q to be a stop word", tc.lang, word)
			}
		}
	}
}
func TestStopWordManager_ListLang_DefaultEnglish(t *testing.T) {
	db, langReg, cleanup := newLangTestEnv(t)
	defer cleanup()

	swm := NewStopWordManager(db)
	_ = swm.EnsureBucket()
	_ = swm.LoadAll()
	swm.SetLangRegistry(langReg)

	defaults, custom, lang := swm.ListLang("test", "")
	if lang != "en" {
		t.Errorf("expected resolved lang 'en', got %q", lang)
	}
	if len(defaults) != len(defaultStopWords) {
		t.Errorf("expected %d English defaults, got %d", len(defaultStopWords), len(defaults))
	}
	if len(custom) != 0 {
		t.Errorf("expected 0 custom, got %d", len(custom))
	}
}
func TestStopWordManager_ListLang_Polish(t *testing.T) {
	db, langReg, cleanup := newLangTestEnv(t)
	defer cleanup()

	swm := NewStopWordManager(db)
	_ = swm.EnsureBucket()
	_ = swm.LoadAll()
	swm.SetLangRegistry(langReg)

	defaults, _, lang := swm.ListLang("test", "pl")
	if lang != "pl" {
		t.Errorf("expected resolved lang 'pl', got %q", lang)
	}
	if len(defaults) != len(defaultStopWordsPL) {
		t.Errorf("expected %d Polish defaults, got %d", len(defaultStopWordsPL), len(defaults))
	}
	// Verify Polish stop words are present
	found := map[string]bool{}
	for _, w := range defaults {
		found[w] = true
	}
	for _, expected := range []string{"ale", "na", "że", "jest", "aby"} {
		if !found[expected] {
			t.Errorf("expected Polish stop word %q in defaults", expected)
		}
	}
}
func TestStopWordManager_ListLang_German(t *testing.T) {
	db, langReg, cleanup := newLangTestEnv(t)
	defer cleanup()

	swm := NewStopWordManager(db)
	_ = swm.EnsureBucket()
	_ = swm.LoadAll()
	swm.SetLangRegistry(langReg)

	defaults, _, lang := swm.ListLang("test", "de")
	if lang != "de" {
		t.Errorf("expected resolved lang 'de', got %q", lang)
	}
	if len(defaults) != len(defaultStopWordsDE) {
		t.Errorf("expected %d German defaults, got %d", len(defaultStopWordsDE), len(defaults))
	}
}
func TestStopWordManager_ListLang_WithCustomWords(t *testing.T) {
	db, langReg, cleanup := newLangTestEnv(t)
	defer cleanup()

	swm := NewStopWordManager(db)
	_ = swm.EnsureBucket()
	_ = swm.LoadAll()
	swm.SetLangRegistry(langReg)

	// Add custom stop words
	_ = swm.Add("test", []string{"customword1", "customword2"})

	defaults, custom, lang := swm.ListLang("test", "fr")
	if lang != "fr" {
		t.Errorf("expected resolved lang 'fr', got %q", lang)
	}
	if len(defaults) != len(defaultStopWordsFR) {
		t.Errorf("expected %d French defaults, got %d", len(defaultStopWordsFR), len(defaults))
	}
	if len(custom) != 2 {
		t.Errorf("expected 2 custom words, got %d", len(custom))
	}
}
func TestStopWordManager_ListLang_NoRegistry(t *testing.T) {
	db, _, cleanup := newLangTestEnv(t)
	defer cleanup()

	swm := NewStopWordManager(db)
	_ = swm.EnsureBucket()
	_ = swm.LoadAll()
	// Intentionally not setting langRegistry

	defaults, _, lang := swm.ListLang("test", "pl")
	if lang != "en" {
		t.Errorf("without registry, expected 'en', got %q", lang)
	}
	if len(defaults) != len(defaultStopWords) {
		t.Errorf("without registry, expected English defaults (%d), got %d", len(defaultStopWords), len(defaults))
	}
}

func newLangTestEnv(t *testing.T) (*bolt.DB, *LangRegistry, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "fts_lang_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	langReg := NewLangRegistry("en")
	RegisterDefaultLanguages(langReg)
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return db, langReg, cleanup
}
func openTestDB(t *testing.T) *bolt.DB {
	t.Helper()
	f, err := os.CreateTemp("", "fts_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	})
	_ = db.Update(func(tx *bolt.Tx) error {
		for _, b := range []string{"fts", "ftsrev", "ftsf", "ftsfmeta", "ftsfstat", "ftsfrev", "ftsp"} {
			_, _ = tx.CreateBucketIfNotExists([]byte(b))
		}
		return nil
	})
	return db
}
func TestHasSuffix(t *testing.T) {
	tests := []struct {
		word   string
		suffix string
		want   bool
	}{
		{"running", "ing", true},
		{"running", "run", false},
		{"ed", "ed", true},
		{"a", "ab", false},
		{"", "x", false},
		{"test", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.word+"_"+tt.suffix, func(t *testing.T) {
			got := hasSuffix([]byte(tt.word), tt.suffix)
			if got != tt.want {
				t.Errorf("hasSuffix(%q, %q) = %v, want %v", tt.word, tt.suffix, got, tt.want)
			}
		})
	}
}

func newLangFTS(t *testing.T) (*FTSIndex, func()) {
	t.Helper()
	idx := NewFTSIndex(openTestDB(t))
	_ = idx.EnsureBuckets()
	langReg := NewLangRegistry("en")
	RegisterDefaultLanguages(langReg)
	idx.SetStemmer(NewPorterStemmer())
	idx.SetLangRegistry(langReg)
	return idx, func() {}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
