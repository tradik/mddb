package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// planStore reads and writes a backlog through the server's ordinary
// document API: search with meta filters, aggregate, and conditional adds.
// Nothing here lists the whole collection: rows are found by kind and
// status, and the next row by a facet over the order values of the rows still to do.
type planStore struct {
	client     *mddb.Client
	collection string
	lang       string
}

const (
	// planPageSize is how many rows one search returns while listing.
	planPageSize = 200
	// planFacetLimit asks for every distinct order at once: a facet walks
	// the meta index, so its size is order values, not documents.
	planFacetLimit = 100_000
	// planTries bounds the retries after lost conditional writes: each one
	// means another session took the row, so the next try takes another.
	planTries = 20
)

// errPlanBusy is a write that lost planTries times in a row.
var errPlanBusy = errors.New("the backlog kept changing under this command; try again")

// isConflict reports whether err is a lost conditional write.
func isConflict(err error) bool {
	var apiErr *mddb.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	_, ok := apiErr.RevisionConflict()
	return ok
}

// backlogFilter selects backlog rows, with one of statuses when any are given.
func backlogFilter(statuses []string) map[string][]string {
	f := map[string][]string{"kind": {kindBacklog}}
	if len(statuses) > 0 {
		f["status"] = statuses
	}
	return f
}

// rows lists the backlog rows with the given statuses (all when none), in
// the order a session takes them.
func (s planStore) rows(ctx context.Context, statuses []string) ([]mddb.Document, error) {
	var out []mddb.Document
	for {
		page, err := s.client.Search(ctx, mddb.SearchRequest{Collection: s.collection, FilterMeta: backlogFilter(statuses),
			Sort: "key", Asc: true, Limit: planPageSize, Offset: len(out)})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		out = append(out, page...)
	}
	sortRows(out)
	return out, nil
}

// orders is the distinct order values of the rows with the given statuses.
func (s planStore) orders(ctx context.Context, statuses []string) ([]string, error) {
	agg, err := s.client.Aggregate(ctx, mddb.AggregateRequest{Collection: s.collection, FilterMeta: backlogFilter(statuses),
		Facets: []mddb.FacetRequest{{Field: "order", OrderBy: "value"}}, MaxFacetSize: planFacetLimit})
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(agg.Facets["order"]))
	for _, b := range agg.Facets["order"] {
		values = append(values, b.Value)
	}
	return values, nil
}

// nextOrder is one past the highest order any row has, so a new row goes
// last.
func (s planStore) nextOrder(ctx context.Context) (int, error) {
	values, err := s.orders(ctx, nil)
	if err != nil {
		return 0, err
	}
	high := 0
	for _, v := range values {
		if n, err := strconv.Atoi(v); err == nil && n > high {
			high = n
		}
	}
	return high + 1, nil
}

// first is the row still to do that a session would take now, or nil when there is none.
// Only the rows at the lowest order are fetched. When those rows were all
// taken between the facet and the search, it looks again.
func (s planStore) first(ctx context.Context) (*mddb.Document, error) {
	for range planTries {
		values, err := s.orders(ctx, []string{statusTodo})
		if err != nil || len(values) == 0 {
			return nil, err
		}
		filter := backlogFilter([]string{statusTodo})
		filter["order"] = []string{lowestOrder(values)}
		docs, err := s.client.Search(ctx, mddb.SearchRequest{Collection: s.collection, FilterMeta: filter, Sort: "key", Asc: true, Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(docs) > 0 {
			return &docs[0], nil
		}
	}
	return nil, errPlanBusy
}

// lowestOrder is the order value a session takes first: the lowest number,
// and among values that are not numbers the lowest string.
func lowestOrder(values []string) string {
	low := values[0]
	for _, v := range values[1:] {
		if orderOf(v) < orderOf(low) || (orderOf(v) == orderOf(low) && v < low) {
			low = v
		}
	}
	return low
}

// claim takes the first row still to do: it marks it doing with a write that only
// succeeds over the revision just read. A session that loses the race
// looks again and takes the next row instead.
func (s planStore) claim(ctx context.Context) (*mddb.Document, error) {
	for range planTries {
		doc, err := s.first(ctx)
		if err != nil || doc == nil {
			return nil, err
		}
		doc.Meta["status"] = []string{statusDoing}
		stored, err := s.write(ctx, *doc, doc.Revision)
		if isConflict(err) {
			continue
		}
		return stored, err
	}
	return nil, errPlanBusy
}

// update reads a row, lets change edit it, and writes it back over the
// revision it read, reading again when another write came first.
func (s planStore) update(ctx context.Context, key string, change func(*mddb.Document)) (*mddb.Document, error) {
	for range planTries {
		doc, err := s.client.Get(ctx, mddb.GetRequest{Collection: s.collection, Key: key, Lang: s.lang})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		change(doc)
		stored, err := s.write(ctx, *doc, doc.Revision)
		if isConflict(err) {
			continue
		}
		return stored, err
	}
	return nil, errPlanBusy
}

// add writes a new row. A slug that was derived, not given, moves aside
// for a different row with the same slug ("-2", "-3"); the same prompt
// under the same slug is the row already there, returned with added false.
func (s planStore) add(ctx context.Context, r planRow, slugGiven bool) (doc *mddb.Document, added bool, err error) {
	base := r.slug
	for n := 1; n <= planTries; n++ {
		if n > 1 {
			r.slug = base + "-" + strconv.Itoa(n)
		}
		req := mddb.AddRequest{Collection: s.collection, Key: planKey + r.slug, Lang: s.lang, Meta: r.meta(), ContentMD: r.prompt, IfAbsent: true}
		doc, err = s.client.Add(ctx, req)
		if !isConflict(err) {
			return doc, err == nil, err
		}
		have, err := s.client.Get(ctx, mddb.GetRequest{Collection: s.collection, Key: req.Key, Lang: s.lang})
		if err != nil {
			return nil, false, err
		}
		if have.ContentMD == r.prompt {
			return have, false, nil
		}
		if slugGiven {
			return nil, false, fmt.Errorf("%s already holds another row; pick another --slug", req.Key)
		}
	}
	return nil, false, errPlanBusy
}

// write stores doc as it is, over revision when one is given.
func (s planStore) write(ctx context.Context, doc mddb.Document, revision string) (*mddb.Document, error) {
	return s.client.Add(ctx, mddb.AddRequest{Collection: s.collection, Key: doc.Key, Lang: doc.Lang,
		Meta: doc.Meta, ContentMD: doc.ContentMD, ExpectedRevision: revision})
}
