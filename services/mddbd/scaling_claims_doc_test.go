package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Guard for what the docs say about sharding (DOC-020, GO-052). Before
// 2.19.8 COMPARISON.md stated "consistent-hash ring, manual" and two guides
// advised "sharding by collection" while nothing was routed by a shard. Since
// GO-052 the server places whole collections across replicated node groups:
// the consistent-hash ring (consistent_hash.go, once sharding.go) gives a new
// collection its default group, and a collection is never split. The docs may
// say that and nothing more — no splitting of one collection, no automatic
// rebalancing, no shard cluster routing documents. The pattern is
// site_claims_doc_test.go's: read the fact from the code, then read every file
// that speaks of it. Paths are relative to services/mddbd.

// shardCode matches code of the shard cluster sharding.go held until GO-052
// replaced it with the placement: a type or call that would route documents
// by shard.
var shardCode = regexp.MustCompile(`\bShardCluster\b|\bGetShards?\(`)

// placementRing matches the placement building its default-group ring.
var placementRing = regexp.MustCompile(`\bnewConsistentHash\(`)

// shardMention matches a line that speaks of shards at all.
var shardMention = regexp.MustCompile(`(?i)shard`)

// trueShardLines are the things a line may say about shards. Anything else
// is a claim to check by hand: either the line is wrong, or it is a new true
// statement and belongs here.
var trueShardLines = []*regexp.Regexp{
	// What the server does: whole collections on replicated node groups, a
	// consistent-hash default, explicit moves; never one collection split,
	// never an automatic rebalance.
	regexp.MustCompile(`^\| Horizontal sharding \| by whole collection across replicated node groups \(placement map, consistent-hash default, explicit moves under load\); one collection is never split; no automatic rebalancing \|`),
	regexp.MustCompile(`(?i)\bnode groups each hold whole collections, never part of one\b`),
	// The in-process cache is split into shards; that is not data sharding.
	regexp.MustCompile(`(?i)cache`),
	// The CI test suite runs in shards.
	regexp.MustCompile(`parallel shards`),
	// What other systems do, where a page compares against them.
	regexp.MustCompile(`^shard across a cluster; MDDB is built to be one file`),
	regexp.MustCompile(`you need automatic sharding at a scale one machine`),
	regexp.MustCompile(`is a cluster: an index is split into shards`),
	regexp.MustCompile(`\| A cluster; shards and replicas spread one index`),
	regexp.MustCompile(`Every vector of a collection lives on one machine \|`),
}

// shardClaimFiles are the pages a reader takes the product's scope from.
func shardClaimFiles(t *testing.T) []string {
	t.Helper()
	files := []string{"../../README.md", "../../.ssg.yaml"}
	for _, pattern := range []string{"../../docs/*.md", "../../blog/*.md", "../../services/ssg-template/*.html"} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			t.Fatalf("glob %s: %v, %d files — the guard's paths may be stale", pattern, err, len(matches))
		}
		files = append(files, matches...)
	}
	return files
}

// shardCodeUses lists the non-test source lines that use a shard cluster.
func shardCodeUses(t *testing.T) []string {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil || len(sources) == 0 {
		t.Fatalf("glob *.go: %v, %d files", err, len(sources))
	}
	var uses []string
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		for line := range strings.SplitSeq(readRepoFile(t, path), "\n") {
			if shardCode.MatchString(line) {
				uses = append(uses, path+": "+strings.TrimSpace(line))
			}
		}
	}
	return uses
}

// unbackedShardClaims returns the lines of text that speak of shards without
// matching a statement known to be true.
func unbackedShardClaims(text string) []string {
	var claims []string
	for line := range strings.SplitSeq(text, "\n") {
		if !shardMention.MatchString(line) {
			continue
		}
		known := false
		for _, re := range trueShardLines {
			known = known || re.MatchString(line)
		}
		if !known {
			claims = append(claims, strings.TrimSpace(line))
		}
	}
	return claims
}

func TestShardingClaimsDocsInSync(t *testing.T) {
	if uses := shardCodeUses(t); len(uses) > 0 {
		t.Fatalf("a shard cluster is back in the code:\n  %s\n"+
			"the placement (GO-052) routes whole collections; describe what this does in docs/ and update this guard",
			strings.Join(uses, "\n  "))
	}
	if _, err := os.Stat("sharding.go"); err == nil {
		t.Fatal("sharding.go is back: the ring lives in consistent_hash.go and the placement uses it — update this guard")
	}
	if !placementRing.MatchString(readRepoFile(t, "placement_runtime.go")) {
		t.Fatal("placement_runtime.go no longer builds the consistent-hash ring: the docs say a new collection " +
			"goes to the group its name hashes to — fix the docs or the code, and this guard")
	}
	for _, path := range shardClaimFiles(t) {
		for _, claim := range unbackedShardClaims(readRepoFile(t, path)) {
			t.Errorf("%s: %q speaks of sharding; the server places whole collections on node groups and splits none — "+
				"say what the code does, or add the statement to trueShardLines if it is true", path, claim)
		}
	}
}

// The guard catches the claims DOC-020 removed, the ones GO-052 made false,
// and claims of what does not exist; and lets the true lines pass.
func TestShardingClaimGuardCatchesTheOldClaims(t *testing.T) {
	for _, line := range []string{
		"| Horizontal sharding | consistent-hash ring, manual | automatic | clustered |",
		"- Consider sharding by collection",
		"Distributed sharding across 4 shards",
		// True before GO-052, false since.
		"- No sharding: a node holds the whole database.",
		"| Horizontal sharding | none — one node holds the whole database |",
		// Never true.
		"A large collection is sharded across the nodes of a group",
		"| Horizontal sharding | by whole collection across replicated node groups, with automatic rebalancing |",
		"Documents are routed to shards by a consistent-hash ring",
	} {
		if got := unbackedShardClaims("intro\n" + line + "\n"); len(got) != 1 {
			t.Errorf("%q passed the guard", line)
		}
	}
	truths := "| Horizontal sharding | by whole collection across replicated node groups (placement map, " +
		"consistent-hash default, explicit moves under load); one collection is never split; no automatic rebalancing | automatic, mature | clustered |\n" +
		"| Deployment | One node. From 2.19.8, node groups each hold whole collections, never part of one | A cluster; shards and replicas spread one index over many nodes |\n" +
		"- **Lock-free Cache** - 16 shards for concurrent reads\n"
	if got := unbackedShardClaims(truths); len(got) != 0 {
		t.Errorf("true statements flagged: %q", got)
	}
	if !shardCode.MatchString("shard := s.ShardCluster.GetShard(key)") || !shardCode.MatchString("type ShardCluster struct") ||
		shardCode.MatchString("ring := newConsistentHash(groups)") {
		t.Error("shardCode must match any use of a shard cluster and not the placement's ring")
	}
	if !placementRing.MatchString("ring: newConsistentHash(pc.names),") {
		t.Error("placementRing must match the placement building its ring")
	}
}
