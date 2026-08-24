package main

import (
	"context"
	"strings"
	"testing"

	proto "mddb/proto"
)

// SRCH-002. The mode exists because the other two miss a real case: a page
// forked and lightly edited. `exact` sees two different hashes; `similar`
// needs embeddings, which most installations do not have configured.

func seedForDuplicates(t *testing.T, srv *Server, collection string, docs map[string]string) {
	t.Helper()
	batch := make([]*proto.BatchDocument, 0, len(docs))
	for key, content := range docs {
		batch = append(batch, makeBatchDoc(key, "en", content, nil, false))
	}
	resp, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), collection, batch)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failed > 0 {
		t.Fatalf("seeding failed: %v", resp.Errors)
	}
}

const runbook = "restart the service by running systemctl restart the unit and then " +
	"check journalctl for errors before reporting the incident resolved to the team"

func TestMinHashFindsAForkedAndEditedDocument(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	seedForDuplicates(t, srv, "runbooks", map[string]string{
		// Same document, forked per environment and lightly edited. Different
		// content hashes, so `exact` finds nothing.
		"restart-staging.md":    runbook + " on staging",
		"restart-production.md": runbook + " on production",
		// Same subject, written independently: this must NOT be grouped, or
		// the mode is just measuring topic like the vector one.
		"certificates.md": "rotating a certificate means issuing new key material, " +
			"reloading every proxy and confirming the chain validates end to end",
	})

	groups, scanned, err := srv.findMinHashDuplicates("runbooks", 0.7, false)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 3 {
		t.Errorf("scanned %d documents, want 3", scanned)
	}
	if len(groups) != 1 {
		t.Fatalf("found %d groups, want 1: %+v", len(groups), groups)
	}
	if len(groups[0].Documents) != 2 {
		t.Errorf("the group holds %d documents, want the two forks", len(groups[0].Documents))
	}
	for _, d := range groups[0].Documents {
		if strings.Contains(d.DocID, "certificate") {
			t.Error("an independently written document on the same subject was grouped as a duplicate")
		}
		if d.Score <= 0 {
			t.Errorf("%s carries no overlap score", d.DocID)
		}
	}
	if groups[0].Type != "minhash" {
		t.Errorf("group type = %q", groups[0].Type)
	}
}

func TestMinHashGroupsTransitively(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	// A→B and B→C overlap above the threshold; A→C may not. All three belong
	// to one group, matching what the other modes do.
	seedForDuplicates(t, srv, "chain", map[string]string{
		"a.md": runbook,
		"b.md": runbook + " and escalate if it recurs",
		"c.md": runbook + " and escalate if it recurs within the hour to the on-call engineer",
	})

	groups, _, err := srv.findMinHashDuplicates("chain", 0.6, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("found %d groups, want 1", len(groups))
	}
	if len(groups[0].Documents) != 3 {
		t.Errorf("the chain grouped %d of 3 documents", len(groups[0].Documents))
	}
}

func TestMinHashNeedsNoEmbeddings(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	// No embedding provider is configured on this server — which is the point.
	if srv.Embedding != nil {
		t.Skip("this fixture unexpectedly has an embedding provider")
	}

	seedForDuplicates(t, srv, "plain", map[string]string{
		"one.md": runbook,
		"two.md": runbook + " today",
	})

	groups, _, err := srv.findMinHashDuplicates("plain", 0.7, false)
	if err != nil {
		t.Fatalf("minhash needs no embeddings but failed: %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("found %d groups without an embedding provider", len(groups))
	}
}

func TestMinHashResultsAreDeterministic(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	seedForDuplicates(t, srv, "det", map[string]string{
		"a.md": runbook, "b.md": runbook + " x", "c.md": runbook + " y",
		"d.md": "an entirely different subject about scaling replicas up and down",
	})

	first, _, err := srv.findMinHashDuplicates("det", 0.7, false)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := srv.findMinHashDuplicates("det", 0.7, false)
	if err != nil {
		t.Fatal(err)
	}

	// Map iteration order would renumber the same duplicates differently on
	// every call, which makes the output impossible to diff between runs.
	if len(first) != len(second) {
		t.Fatalf("two runs found %d and %d groups", len(first), len(second))
	}
	for i := range first {
		if first[i].GroupID != second[i].GroupID {
			t.Errorf("group %d renumbered between runs", i)
		}
		if len(first[i].Documents) != len(second[i].Documents) {
			t.Errorf("group %d changed size between runs", i)
		}
		for j := range first[i].Documents {
			if first[i].Documents[j].DocID != second[i].Documents[j].DocID {
				t.Errorf("group %d member %d differs between runs", i, j)
			}
		}
	}
}

func TestMinHashOnAnEmptyOrSingletonCollection(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	groups, scanned, err := srv.findMinHashDuplicates("nothing-here", 0.7, false)
	if err != nil {
		t.Fatalf("an empty collection returned an error: %v", err)
	}
	if len(groups) != 0 || scanned != 0 {
		t.Errorf("empty collection: %d groups, %d scanned", len(groups), scanned)
	}

	seedForDuplicates(t, srv, "one", map[string]string{"only.md": runbook})
	groups, scanned, err = srv.findMinHashDuplicates("one", 0.7, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("a single document was reported as a duplicate group")
	}
	if scanned != 1 {
		t.Errorf("scanned %d, want 1", scanned)
	}
}

func TestMinHashThresholdDefaultsAndApplies(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	seedForDuplicates(t, srv, "thr", map[string]string{
		"a.md": runbook,
		"b.md": runbook + " with a good deal of additional text appended to it that " +
			"changes roughly a third of the shingles in the document overall",
	})

	// A threshold of 1.0 admits only an exact text match.
	strict, _, err := srv.findMinHashDuplicates("thr", 1.0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(strict) != 0 {
		t.Errorf("a threshold of 1.0 grouped documents that are not identical")
	}

	// A permissive threshold groups them.
	loose, _, err := srv.findMinHashDuplicates("thr", 0.3, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(loose) != 1 {
		t.Errorf("a threshold of 0.3 found %d groups, want 1", len(loose))
	}

	// Zero means "use the default", not "group everything".
	def, _, err := srv.findMinHashDuplicates("thr", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = def
}

func TestMinHashCanReturnContent(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	seedForDuplicates(t, srv, "content", map[string]string{
		"a.md": runbook, "b.md": runbook + " now",
	})

	groups, _, err := srv.findMinHashDuplicates("content", 0.7, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) == 0 {
		t.Fatal("no groups found")
	}
	for _, d := range groups[0].Documents {
		if d.ContentMD == "" {
			t.Errorf("%s was returned without its content despite includeContent", d.DocID)
		}
		if d.Key == "" {
			t.Errorf("%s was returned without its key", d.DocID)
		}
	}
}
