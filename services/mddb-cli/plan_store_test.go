package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

func testStore(s *planServer) planStore {
	return planStore{client: mddb.New(s.URL), collection: "c", lang: "en"}
}

// seed adds rows with the given prompts at orders 1, 2, 3...
func seed(t *testing.T, st planStore, prompts ...string) {
	t.Helper()
	for i, p := range prompts {
		if _, _, err := st.add(context.Background(), planRow{prompt: p, slug: slugify(p), order: i + 1}, false); err != nil {
			t.Fatal(err)
		}
	}
}

// TestClaim_TwoCallersGetDifferentRows is the ticket's race: both callers
// read the backlog before either writes, so both pick row one; the
// conditional write lets one of them have it and sends the other to row two.
func TestClaim_TwoCallersGetDifferentRows(t *testing.T) {
	srv := newPlanServer(t)
	st := testStore(srv)
	seed(t, st, "first row", "second row", "third row")
	srv.hold(2)
	var wg sync.WaitGroup
	got := make([]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := st.claim(context.Background())
			errs[i] = err
			if d != nil {
				got[i] = d.Key
			}
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	if got[0] == got[1] || got[0] == "" || got[1] == "" {
		t.Fatalf("both callers took %v", got)
	}
	for _, k := range []string{"backlog/first-row", "backlog/second-row"} {
		if d, _ := srv.doc(k); metaFirst(d, "status") != statusDoing {
			t.Errorf("%s is %v", k, d.Meta["status"])
		}
	}
	if d, _ := srv.doc("backlog/third-row"); metaFirst(d, "status") != statusTodo {
		t.Errorf("third row was taken: %v", d.Meta)
	}
}

// TestClaim_ManyCallers: as many callers as rows, all at once; every row is
// taken exactly once.
func TestClaim_ManyCallers(t *testing.T) {
	srv := newPlanServer(t)
	st := testStore(srv)
	const n = 8
	var prompts []string
	for i := range n {
		prompts = append(prompts, fmt.Sprintf("row %d", i))
	}
	seed(t, st, prompts...)
	var mu sync.Mutex
	taken := map[string]int{}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := st.claim(context.Background())
			if err != nil || d == nil {
				t.Errorf("claim: %v %v", d, err)
				return
			}
			mu.Lock()
			taken[d.Key]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(taken) != n {
		t.Fatalf("taken: %v", taken)
	}
	if d, err := st.claim(context.Background()); d != nil || err != nil {
		t.Fatalf("an empty backlog gave %v %v", d, err)
	}
}

func TestClaim_GivesUpAndFails(t *testing.T) {
	srv := newPlanServer(t)
	st := testStore(srv)
	seed(t, st, "one")
	srv.conflictAlways = true
	if _, err := st.claim(context.Background()); !errors.Is(err, errPlanBusy) {
		t.Fatalf("claim: %v", err)
	}
	if _, err := st.update(context.Background(), "backlog/one", func(*mddb.Document) {}); !errors.Is(err, errPlanBusy) {
		t.Fatalf("update: %v", err)
	}
	srv.conflictAlways = false
	srv.setFail("/v1/aggregate", true)
	if _, err := st.claim(context.Background()); err == nil {
		t.Fatal("aggregate failure ignored")
	}
	if _, err := st.nextOrder(context.Background()); err == nil {
		t.Fatal("aggregate failure ignored")
	}
	srv.setFail("/v1/aggregate", false)
	srv.setFail("/v1/search", true)
	if _, err := st.first(context.Background()); err == nil {
		t.Fatal("search failure ignored")
	}
	if _, err := st.rows(context.Background(), nil); err == nil {
		t.Fatal("search failure ignored")
	}
	srv.setFail("/v1/search", false)
	srv.setFail("/v1/add", true)
	if _, err := st.claim(context.Background()); err == nil || isConflict(err) {
		t.Fatalf("add failure: %v", err)
	}
}

// Orders that are not numbers sort after numbers, and between themselves
// as strings; a facet value no search finds (an index behind a write) is
// looked for again, a bounded number of times.
func TestFirst_Orders(t *testing.T) {
	srv := newPlanServer(t)
	st := testStore(srv)
	seed(t, st, "a", "b")
	setOrder := func(key, order string) {
		srv.mu.Lock()
		d := srv.docs["c|"+key+"|en"]
		d.Meta["order"] = []string{order}
		srv.docs["c|"+key+"|en"] = d
		srv.mu.Unlock()
	}
	setOrder("backlog/a", "x")
	if got, err := st.first(context.Background()); err != nil || got == nil || got.Key != "backlog/b" {
		t.Fatalf("first: %v %v", got, err)
	}
	setOrder("backlog/b", "w")
	if got, _ := st.first(context.Background()); got == nil || got.Key != "backlog/b" {
		t.Fatalf("first: %v", got)
	}
	srv.hideSearch = true
	if _, err := st.first(context.Background()); !errors.Is(err, errPlanBusy) {
		t.Fatalf("hidden: %v", err)
	}
	empty := planStore{client: st.client, collection: "none", lang: "en"}
	if got, err := empty.first(context.Background()); got != nil || err != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
}

func TestRows_PagesPastTheServerCap(t *testing.T) {
	srv := newPlanServer(t)
	st := testStore(srv)
	seed(t, st, "a", "b", "c", "d", "e")
	srv.maxLimit = 2
	docs, err := st.rows(context.Background(), []string{statusTodo})
	if err != nil || len(docs) != 5 {
		t.Fatalf("%d %v", len(docs), err)
	}
	if srv.searches != 4 {
		t.Fatalf("searches: %d", srv.searches)
	}
}

func TestAdd_Slugs(t *testing.T) {
	srv := newPlanServer(t)
	st := testStore(srv)
	ctx := context.Background()
	r := planRow{prompt: "same words", slug: "same-words", order: 1}
	if _, added, err := st.add(ctx, r, false); err != nil || !added {
		t.Fatal(added, err)
	}
	// The same prompt again is the same row.
	if d, added, err := st.add(ctx, r, false); err != nil || added || d.Key != "backlog/same-words" {
		t.Fatal(d, added, err)
	}
	// A different prompt with the same derived slug moves aside.
	r.prompt = "same words, other row"
	if d, added, err := st.add(ctx, r, false); err != nil || !added || d.Key != "backlog/same-words-2" {
		t.Fatal(d, added, err)
	}
	// A slug the user gave does not.
	if _, _, err := st.add(ctx, planRow{prompt: "third", slug: "same-words"}, true); err == nil || !strings.Contains(err.Error(), "--slug") {
		t.Fatal(err)
	}
	srv.conflictAlways = true
	srv.mu.Lock()
	for i := 3; i <= planTries; i++ {
		srv.docs[fmt.Sprintf("c|backlog/same-words-%d|en", i)] = mddb.Document{Key: "x", ContentMD: "other"}
	}
	srv.mu.Unlock()
	if _, _, err := st.add(ctx, planRow{prompt: "fourth", slug: "same-words"}, false); !errors.Is(err, errPlanBusy) {
		t.Fatal(err)
	}
	srv.setFail("/v1/get", true)
	if _, _, err := st.add(ctx, planRow{prompt: "fifth", slug: "same-words"}, false); err == nil {
		t.Fatal("get failure ignored")
	}
}

func TestProgressAndImportErrors(t *testing.T) {
	srv := newPlanServer(t)
	st := testStore(srv)
	ctx := context.Background()
	row := mddb.Document{Key: "backlog/x", Meta: map[string][]string{"issues": {"3"}, "commit": {"abc"}}}
	d, err := st.progress(ctx, row, "note", time.Date(2026, 11, 22, 23, 0, 0, 0, time.UTC))
	if err != nil || d.Key != "progress/2026-11-22-x" || d.ContentMD != "note" {
		t.Fatal(d, err)
	}
	if got := d.Meta; metaFirst(*d, "kind") != kindProgress || got["commit"][0] != "abc" || got["release"] != nil {
		t.Fatalf("%v", got)
	}
	if _, _, err := st.importTable(ctx, "nothing"); !errors.Is(err, errNoTable) {
		t.Fatal(err)
	}
	if _, _, err := st.importTable(ctx, "| A |\n|---|\n| x |\n"); !errors.Is(err, errNoPromptColumn) {
		t.Fatal(err)
	}
	srv.setFail("/v1/aggregate", true)
	if _, _, err := st.importTable(ctx, backlogTable); err == nil {
		t.Fatal("aggregate failure ignored")
	}
	srv.setFail("/v1/aggregate", false)
	srv.setFail("/v1/add", true)
	if _, _, err := st.importTable(ctx, backlogTable); err == nil || !strings.Contains(err.Error(), "31-webhook") {
		t.Fatal(err)
	}
}

func TestIsConflict(t *testing.T) {
	if isConflict(errors.New("plain")) || isConflict(&mddb.APIError{StatusCode: 409, Body: `{"error":"other"}`}) {
		t.Fatal("not a conflict")
	}
}

func TestPrintPlanJSONFails(t *testing.T) {
	if err := printPlanJSON(make(chan int)); err == nil {
		t.Fatal("a channel marshalled")
	}
}
