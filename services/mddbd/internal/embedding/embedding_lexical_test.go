package embedding

import (
	"context"
	"math"
	"testing"
)

// cosine of two L2-normalised vectors is their dot product.
func lexDot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func lexEmbed(t *testing.T, p *LexicalProvider, text string) []float32 {
	t.Helper()
	v, err := p.Embed(context.Background(), text, RoleDocument)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLexicalIsDeterministicAcrossInstances(t *testing.T) {
	text := "Set up a read replica that follows the leader — zażółć gęślą jaźń 42"
	a := lexEmbed(t, NewLexicalProvider(512), text)
	b := lexEmbed(t, NewLexicalProvider(512), text)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("component %d differs: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestLexicalRoleDoesNotChangeTheVector(t *testing.T) {
	p := NewLexicalProvider(0)
	d, _ := p.Embed(context.Background(), "backup and restore", RoleDocument)
	q, _ := p.Embed(context.Background(), "backup and restore", RoleQuery)
	if lexDot(d, q) < 0.999999 {
		t.Errorf("document and query vectors differ: cosine %v", lexDot(d, q))
	}
}

func TestLexicalDimensionsAndUnitNorm(t *testing.T) {
	for _, dims := range []int{256, 512, 1024, 2048} {
		p := NewLexicalProvider(dims)
		if p.Dimensions() != dims {
			t.Fatalf("Dimensions() = %d, want %d", p.Dimensions(), dims)
		}
		v := lexEmbed(t, p, "Serve the API over HTTPS with my own certificate")
		if len(v) != dims {
			t.Fatalf("len = %d, want %d", len(v), dims)
		}
		if n := math.Sqrt(lexDot(v, v)); math.Abs(n-1) > 1e-5 {
			t.Errorf("dims %d: norm %v, want 1", dims, n)
		}
	}
}

func TestLexicalTextWithoutWordsIsAZeroVector(t *testing.T) {
	v := lexEmbed(t, NewLexicalProvider(256), " --- !!! ")
	if len(v) != 256 || lexDot(v, v) != 0 {
		t.Errorf("want a 256-long zero vector, got norm² %v", lexDot(v, v))
	}
}

func TestLexicalSharedWordsScoreHigher(t *testing.T) {
	p := NewLexicalProvider(512)
	q := lexEmbed(t, p, "replication leader follower")
	near := lexEmbed(t, p, "Replication: a follower copies every write from the leader.")
	far := lexEmbed(t, p, "Backups are written to a tarball and restored with one command.")
	if lexDot(q, near) <= lexDot(q, far) {
		t.Errorf("overlapping text %v not above unrelated %v", lexDot(q, near), lexDot(q, far))
	}
}

func TestLexicalToleratesATypo(t *testing.T) {
	p := NewLexicalProvider(512)
	right := lexEmbed(t, p, "replication")
	typo := lexEmbed(t, p, "replicaton")
	other := lexEmbed(t, p, "certificate")
	if lexDot(right, typo) < 0.5 {
		t.Errorf("typo cosine %v, want at least 0.5 from shared n-grams", lexDot(right, typo))
	}
	if lexDot(right, typo) <= lexDot(right, other) {
		t.Errorf("typo %v not closer than an unrelated word %v", lexDot(right, typo), lexDot(right, other))
	}
}

func TestLexicalIsCaseInsensitive(t *testing.T) {
	p := NewLexicalProvider(512)
	if c := lexDot(lexEmbed(t, p, "Deploy NOW"), lexEmbed(t, p, "deploy now")); c < 0.999999 {
		t.Errorf("case changed the vector: cosine %v", c)
	}
}

func TestLexicalRepetitionIsSublinear(t *testing.T) {
	p := NewLexicalProvider(512)
	once := lexEmbed(t, p, "cache eviction")
	many := lexEmbed(t, p, "cache cache cache cache cache cache cache cache eviction")
	// With raw counts "eviction" would vanish; with 1+ln(tf) it stays visible.
	if c := lexDot(once, many); c < 0.7 {
		t.Errorf("repetition swamped the text: cosine %v", c)
	}
}

func TestLexicalEmbedBatchMatchesEmbed(t *testing.T) {
	p := NewLexicalProvider(256)
	texts := []string{"one fish", "two fish", ""}
	out, err := p.EmbedBatch(context.Background(), texts, RoleQuery)
	if err != nil || len(out) != len(texts) {
		t.Fatalf("EmbedBatch = %d vectors, %v", len(out), err)
	}
	for i, txt := range texts {
		single := lexEmbed(t, p, txt)
		for j := range single {
			if single[j] != out[i][j] {
				t.Fatalf("text %d component %d differs", i, j)
			}
		}
	}
}

func TestLexicalHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := NewLexicalProvider(256)
	if _, err := p.Embed(ctx, "x", RoleDocument); err == nil {
		t.Error("Embed ignored a cancelled context")
	}
	if _, err := p.EmbedBatch(ctx, []string{"x"}, RoleDocument); err == nil {
		t.Error("EmbedBatch ignored a cancelled context")
	}
}

func TestLexicalDimensionsAreClamped(t *testing.T) {
	cases := map[int]int{0: 512, -3: 512, 100: 256, 256: 256, 777: 777, 2048: 2048, 9000: 2048}
	for in, want := range cases {
		if got := NewLexicalProvider(in).Dimensions(); got != want {
			t.Errorf("NewLexicalProvider(%d).Dimensions() = %d, want %d", in, got, want)
		}
	}
}

func TestLexicalDimensionsFromEnv(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "")
	if got := LexicalDimensionsFromEnv(); got != LexicalDefaultDimensions {
		t.Errorf("default = %d", got)
	}
	t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "1024")
	if got := LexicalDimensionsFromEnv(); got != 1024 {
		t.Errorf("1024 → %d", got)
	}
	t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "64")
	if got := LexicalDimensionsFromEnv(); got != LexicalMinDimensions {
		t.Errorf("64 → %d, want clamped to %d", got, LexicalMinDimensions)
	}
}

func TestLexicalModelAndSemantic(t *testing.T) {
	p := NewLexicalProvider(512)
	if p.Model() != LexicalModel {
		t.Errorf("Model() = %q", p.Model())
	}
	if p.Semantic() || IsSemantic(p) {
		t.Error("the lexical provider must not report itself as semantic")
	}
}
