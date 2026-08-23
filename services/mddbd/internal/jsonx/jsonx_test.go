package jsonx

import (
	"bytes"
	stdjson "encoding/json"
	"flag"
	"strings"
	"sync"
	"testing"

	goccy "github.com/goccy/go-json"
)

// stdlibMarshal is the standard library's encoder, named so the comparison
// below cannot accidentally compare goccy with itself.
var stdlibMarshal = stdjson.Marshal

// reproduceGO037 gates the test that deliberately triggers the dependency's
// panic. Behind a flag rather than a skip line, so it can actually be run when
// someone wants to check whether an upgrade fixed it.
var reproduceGO037 = flag.Bool("go037", false, "run the test that reproduces goccy's panic (GO-037)")

// GO-037. The package exists because goccy panics on malformed input where the
// standard library returns an error, and the panic is state-dependent: a single
// bad document reproduces nothing, a mixed sequence fires on about 5% of calls.
//
// These tests replay a sequence, not a case.

// malformed is the input family FuzzLoadDoc surfaced, plus the shapes around it.
var malformed = []string{
	`{"\0,\`,
	`{"key": "value", "nested": {"a": [1, 2, {"b": "\0`,
	`{"a":"\u00"`,
	`[{"x":1},{"y":`,
	`{"k":"v"}{"k":`,
	`{"\`,
}

type doc struct {
	ID        string              `json:"id"`
	Key       string              `json:"key"`
	Lang      string              `json:"lang"`
	Meta      map[string][]string `json:"meta"`
	ContentMD string              `json:"contentMd"`
	AddedAt   int64               `json:"addedAt"`
	UpdatedAt int64               `json:"updatedAt"`
}

func TestUnmarshalSurvivesASequenceOfMalformedInput(t *testing.T) {
	// 20 000 calls over a repeating mixed sequence. The state dependence means
	// one pass over six inputs proves nothing; this is the shape that fired.
	for i := 0; i < 20000; i++ {
		var d doc
		if err := Unmarshal([]byte(malformed[i%len(malformed)]), &d); err == nil {
			t.Fatalf("iteration %d: malformed input %q was accepted",
				i, malformed[i%len(malformed)])
		}
	}
}

func TestDecoderSurvivesASequenceOfMalformedInput(t *testing.T) {
	// A Decoder streams: it reads the first complete value and leaves the rest
	// for the next call, so `{"k":"v"}{"k":` legitimately succeeds where
	// Unmarshal rejects it as trailing data. The property under test is that
	// nothing panics, not that everything is refused.
	for i := 0; i < 20000; i++ {
		var d doc
		_ = NewDecoder(strings.NewReader(malformed[i%len(malformed)])).Decode(&d)
	}
}

// The difference between the two is worth pinning, because a handler that
// switches from one to the other changes what it accepts.
func TestDecoderStreamsWhereUnmarshalRejectsTrailingData(t *testing.T) {
	const twoValues = `{"key":"first"}{"key":"second"}`

	var viaUnmarshal doc
	if err := Unmarshal([]byte(twoValues), &viaUnmarshal); err == nil {
		t.Error("Unmarshal accepted trailing data after a complete value")
	}

	dec := NewDecoder(strings.NewReader(twoValues))
	var first, second doc
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("the decoder refused the first value: %v", err)
	}
	if first.Key != "first" {
		t.Errorf("first value = %q", first.Key)
	}
	if err := dec.Decode(&second); err != nil {
		t.Fatalf("the decoder refused the second value: %v", err)
	}
	if second.Key != "second" {
		t.Errorf("second value = %q", second.Key)
	}
}

// The same sequence through goccy, kept as evidence that the switch was
// necessary rather than precautionary. Skipped unless -run names it, because a
// test that documents a crash should not be able to cause one on every CI run.
func TestGoccyStillPanicsOnTheSameSequence(t *testing.T) {
	if testing.Short() {
		t.Skip("documents a crash in a dependency; run explicitly")
	}
	if !*reproduceGO037 {
		t.Skip("documents a crash in a dependency; run with -go037 to reproduce")
	}

	for i := 0; i < 20000; i++ {
		var d doc
		_ = goccy.Unmarshal([]byte(malformed[i%len(malformed)]), &d)
	}
}

// Concurrency matters here: the fault depends on decoder state, and the
// original crash was found by a fuzzer running many goroutines.
func TestUnmarshalIsSafeUnderConcurrency(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 24; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				var d doc
				_ = Unmarshal([]byte(malformed[(i+seed)%len(malformed)]), &d)
			}
		}(g)
	}
	wg.Wait()
}

// The split is the package's contract: encoding stays on goccy for speed, and
// both must produce identical bytes or the replication binlog changes shape.
func TestEncodingMatchesTheStandardLibraryByteForByte(t *testing.T) {
	values := []any{
		doc{
			ID: "c|k|en", Key: "k", Lang: "en",
			Meta:      map[string][]string{"zebra": {"z"}, "alpha": {"a", "b"}, "middle": {"m"}},
			ContentMD: "# Title\n\nBody with \"quotes\" and \\backslashes\\ and é accents.",
			AddedAt:   1700000000, UpdatedAt: 1700000001,
		},
		map[string]any{"b": 2, "a": 1, "c": []any{1, "two", nil, true}},
		[]doc{{Key: "one"}, {Key: "two"}},
		map[string][]string{},
		struct{}{},
	}

	for i, v := range values {
		ours, err := Marshal(v)
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		theirs, err := stdMarshal(v)
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		if !bytes.Equal(ours, theirs) {
			t.Errorf("value %d encodes differently:\n  jsonx:  %s\n  stdlib: %s", i, ours, theirs)
		}
	}
}

// Map key ordering is the specific property the binlog depends on, and the
// reason encoding/json/v2 is not used directly.
func TestMapKeysAreSorted(t *testing.T) {
	encoded, err := Marshal(map[string]int{"zebra": 1, "alpha": 2, "middle": 3})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"alpha":2,"middle":3,"zebra":1}`
	if string(encoded) != want {
		t.Errorf("map keys are not sorted:\n  got  %s\n  want %s", encoded, want)
	}
}

func TestRoundTrip(t *testing.T) {
	original := doc{
		ID: "c|k|en", Key: "k", Lang: "en",
		Meta:      map[string][]string{"tag": {"go", "rust"}},
		ContentMD: "body",
	}

	encoded, err := Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded doc
	if err := Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Key != original.Key || decoded.ContentMD != original.ContentMD {
		t.Errorf("round trip lost data: %+v", decoded)
	}
	if len(decoded.Meta["tag"]) != 2 {
		t.Errorf("metadata lost: %v", decoded.Meta)
	}
}

func TestEncoderAndValid(t *testing.T) {
	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if !Valid(buf.Bytes()) {
		t.Errorf("the encoder produced bytes Valid rejects: %s", buf.String())
	}
	for _, bad := range malformed {
		if Valid([]byte(bad)) {
			t.Errorf("Valid accepted %q", bad)
		}
	}
}

// stdMarshal is the comparison target, imported under its own name so the test
// cannot accidentally compare goccy with itself.
func stdMarshal(v any) ([]byte, error) {
	return stdlibMarshal(v)
}
