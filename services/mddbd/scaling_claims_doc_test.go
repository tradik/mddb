package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Guard for what the docs say about sharding (DOC-020). COMPARISON.md stated
// "consistent-hash ring, manual" and two guides advised "sharding by
// collection", while no request is routed by a shard: sharding.go holds a
// ring that nothing outside it calls. The pattern is site_claims_doc_test.go's
// — read the fact from the code, then read every file that speaks of it.
// Paths are relative to services/mddbd.

// shardRouting matches code that would route by the shard cluster.
var shardRouting = regexp.MustCompile(`\.ShardCluster\.|\bGetShards?\(`)

// shardMention matches a line that speaks of shards at all.
var shardMention = regexp.MustCompile(`(?i)shard`)

// trueShardLines are the things a line may say about shards while the server
// routes nothing by them. Anything else is a claim to check by hand: either
// the line is wrong, or it is a new true statement and belongs here.
var trueShardLines = []*regexp.Regexp{
	// The server has none.
	regexp.MustCompile(`(?i)\bno sharding\b`),
	regexp.MustCompile(`^\| Horizontal sharding \| none\b`),
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

// shardRoutingUses lists the non-test source lines, outside sharding.go, that
// route by the shard cluster.
func shardRoutingUses(t *testing.T) []string {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil || len(sources) == 0 {
		t.Fatalf("glob *.go: %v, %d files", err, len(sources))
	}
	var uses []string
	for _, path := range sources {
		if path == "sharding.go" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		for _, line := range strings.Split(readRepoFile(t, path), "\n") {
			if shardRouting.MatchString(line) {
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
	for _, line := range strings.Split(text, "\n") {
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
	if uses := shardRoutingUses(t); len(uses) > 0 {
		t.Fatalf("requests are now routed by the shard cluster:\n  %s\n"+
			"the docs may say so — describe what it does in docs/ and update this guard", strings.Join(uses, "\n  "))
	}
	if _, err := os.Stat("sharding.go"); err != nil {
		t.Fatalf("sharding.go: %v — the guard reads its absence of callers; update it", err)
	}
	for _, path := range shardClaimFiles(t) {
		for _, claim := range unbackedShardClaims(readRepoFile(t, path)) {
			t.Errorf("%s: %q speaks of sharding, and the server routes nothing by shard — "+
				"say what the code does, or add the statement to trueShardLines if it is true", path, claim)
		}
	}
}

// The guard catches the claims DOC-020 removed and lets the true lines pass.
func TestShardingClaimGuardCatchesTheOldClaims(t *testing.T) {
	for _, line := range []string{
		"| Horizontal sharding | consistent-hash ring, manual | automatic | clustered |",
		"- Consider sharding by collection",
		"Distributed sharding across 4 shards",
	} {
		if got := unbackedShardClaims("intro\n" + line + "\n"); len(got) != 1 {
			t.Errorf("%q passed the guard", line)
		}
	}
	truths := "- No sharding: a node holds the whole database.\n" +
		"| Horizontal sharding | none — one node holds the whole database |\n" +
		"- **Lock-free Cache** - 16 shards for concurrent reads\n"
	if got := unbackedShardClaims(truths); len(got) != 0 {
		t.Errorf("true statements flagged: %q", got)
	}
	if !shardRouting.MatchString("shard := s.ShardCluster.GetShard(key)") || shardRouting.MatchString("ShardCluster *ShardCluster") {
		t.Error("shardRouting must match a call through the cluster and not its declaration")
	}
}
