package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/geo"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Every path of the geo surfaces — REST, gRPC and the MCP direct client —
// that the feature tests in geo_*_test.go leave out: refusals, filters that
// match, documents the index knows and the store does not, and failures of
// the store itself.

// geoCovServer is a handler test server with both geo indexes ready and
// three shops in Berlin, written the way a client writes them, plus a point
// whose document is gone and one whose stored document is unreadable.
func geoCovServer(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	s.GeoStore = geo.NewGeoStore(s.DB)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	s.GeoIndex, s.GeoHashIndex = geo.NewGeoIndex(), geo.NewGeoHashIndex()
	s.GeoIndex.SetReady()
	s.GeoHashIndex.SetReady()
	for i, pt := range [][2]string{{"52.52", "13.405"}, {"52.521", "13.406"}, {"52.522", "13.407"}} {
		addTestDoc(t, s, "v", "shop"+string(rune('a'+i)), "en", "shop "+pt[0], map[string][]string{
			"geo_lat": {pt[0]}, "geo_lng": {pt[1]}, "kind": {"shop"}})
	}
	if _, err := s.GeoStore.RebuildHash(s.GeoHashIndex, "v"); err != nil {
		t.Fatal(err)
	}
	// Known to the index, not to the store; and stored as garbage.
	s.GeoIndex.Add("v", "ghost", 52.5201, 13.4051)
	s.GeoIndex.Add("v", "garbage", 52.5202, 13.4052)
	if err := s.DBUpdateC("v", func(tx *bolt.Tx) error {
		return tx.Bucket(s.BucketNames.Docs).Put(storage.DocKey("v", "garbage"), []byte("not a document"))
	}); err != nil {
		t.Fatal(err)
	}
	return s, cleanup
}

// geoPost runs a REST geo handler with a raw body.
func geoPost(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return rec
}

func TestGeoCov_RESTRefusals(t *testing.T) {
	s, cleanup := geoCovServer(t)
	defer cleanup()
	square := `{"type":"Polygon","coordinates":[[[13.3,52.4],[13.5,52.4],[13.5,52.6],[13.3,52.6],[13.3,52.4]]]}`
	for name, tc := range map[string]struct {
		h    http.HandlerFunc
		body string
		code int
	}{
		"search bad json":      {s.handleGeoSearch, `{`, http.StatusBadRequest},
		"encode bad json":      {s.handleGeoEncode, `{`, http.StatusBadRequest},
		"decode bad json":      {s.handleGeoDecode, `{`, http.StatusBadRequest},
		"decode bad hash":      {s.handleGeoDecode, `{"geohash":"ail"}`, http.StatusBadRequest},
		"within bad json":      {s.handleGeoWithin, `{`, http.StatusBadRequest},
		"within no coll":       {s.handleGeoWithin, `{"minLat":1,"maxLat":2,"minLng":1,"maxLng":2}`, http.StatusBadRequest},
		"polygon bad json":     {s.handleGeoPolygon, `{`, http.StatusBadRequest},
		"polygon no coll":      {s.handleGeoPolygon, `{"polygon":` + square + `}`, http.StatusBadRequest},
		"polygon both":         {s.handleGeoPolygon, `{"collection":"v"}`, http.StatusBadRequest},
		"polygon invalid":      {s.handleGeoPolygon, `{"collection":"v","polygon":{"coordinates":[]}}`, http.StatusBadRequest},
		"multipolygon invalid": {s.handleGeoPolygon, `{"collection":"v","multiPolygon":{"coordinates":[]}}`, http.StatusBadRequest},
		"reindex bad json":     {s.handleGeoReindex, `{`, http.StatusBadRequest},
	} {
		if rec := geoPost(tc.h, tc.body); rec.Code != tc.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// Not ready: radius by geohash, bounding box and polygon.
	s.GeoHashIndex = geo.NewGeoHashIndex()
	if rec := geoPost(s.handleGeoSearch, `{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":100,"algorithm":"geohash"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("geohash not ready: %d", rec.Code)
	}
	s.GeoIndex = geo.NewGeoIndex()
	if rec := geoPost(s.handleGeoWithin, `{"collection":"v","minLat":1,"maxLat":2,"minLng":1,"maxLng":2}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("within not ready: %d", rec.Code)
	}
	if rec := geoPost(s.handleGeoPolygon, `{"collection":"v","polygon":`+square+`}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("polygon not ready: %d", rec.Code)
	}

	// Reindex: postcode loading is for administrators, and needs the subsystem.
	covEDenyAll(t, s)
	if rec := geoPost(s.handleGeoReindex, `{"loadPostcodes":[{"country":"PL","csvPath":"pl.csv"}]}`); rec.Code != http.StatusForbidden {
		t.Errorf("reindex as anonymous: %d", rec.Code)
	}
	s.AuthManager = nil
	s.GeoStore = nil
	if rec := geoPost(s.handleGeoReindex, `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("reindex without the subsystem: %d", rec.Code)
	}
}

func TestGeoCov_RESTSearches(t *testing.T) {
	s, cleanup := geoCovServer(t)
	defer cleanup()
	square := `{"type":"Polygon","coordinates":[[[13.3,52.4],[13.5,52.4],[13.5,52.6],[13.3,52.6],[13.3,52.4]]]}`
	multi := `{"type":"MultiPolygon","coordinates":[[[[13.3,52.4],[13.5,52.4],[13.5,52.6],[13.3,52.6],[13.3,52.4]]]]}`
	total := func(rec *httptest.ResponseRecorder) int {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Total
	}
	shops, none := `"filterMeta":{"kind":["shop"]}`, `"filterMeta":{"kind":["none"]}`
	for name, tc := range map[string]struct {
		h    http.HandlerFunc
		body string
		want int
	}{
		// The ghost and the garbage are found by the index and skipped.
		"radius":             {s.handleGeoSearch, `{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":5000,"includeContent":true}`, 3},
		"radius filtered":    {s.handleGeoSearch, `{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":5000,` + shops + `}`, 3},
		"within":             {s.handleGeoWithin, `{"collection":"v","minLat":52,"maxLat":53,"minLng":13,"maxLng":14}`, 3},
		"within filtered":    {s.handleGeoWithin, `{"collection":"v","minLat":52,"maxLat":53,"minLng":13,"maxLng":14,` + shops + `}`, 3},
		"within no match":    {s.handleGeoWithin, `{"collection":"v","minLat":52,"maxLat":53,"minLng":13,"maxLng":14,` + none + `}`, 0},
		"polygon filtered":   {s.handleGeoPolygon, `{"collection":"v","polygon":` + square + `,` + shops + `}`, 3},
		"multipolygon":       {s.handleGeoPolygon, `{"collection":"v","multiPolygon":` + multi + `}`, 3},
		"multipolygon empty": {s.handleGeoPolygon, `{"collection":"v","multiPolygon":` + multi + `,` + none + `}`, 0},
	} {
		if got := total(geoPost(tc.h, tc.body)); got != tc.want {
			t.Errorf("%s: total %d, want %d", name, got, tc.want)
		}
	}
	if rec := geoPost(s.handleGeoPolygon, `{"collection":"v","multiPolygon":`+multi+`,`+none+`}`); !strings.Contains(rec.Body.String(), `"multiPolygon"`) {
		t.Errorf("an empty multipolygon answer names its shape: %s", rec.Body.String())
	}

	// Encode at the default precision.
	if rec := geoPost(s.handleGeoEncode, `{"lat":52.52,"lng":13.405}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"precision":12`) {
		t.Errorf("encode: %d %s", rec.Code, rec.Body.String())
	}

	// Stats: per collection, with a postcode dataset; and with no index.
	s.GeoIndex.SetPostcodes(geo.NewPostcodeLookup())
	rec := httptest.NewRecorder()
	s.handleGeoStats(rec, httptest.NewRequest(http.MethodGet, "/v1/geo-stats", nil))
	if !strings.Contains(rec.Body.String(), `"v"`) {
		t.Errorf("stats: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	(&Server{}).handleGeoStats(rec, httptest.NewRequest(http.MethodGet, "/v1/geo-stats", nil))
	if !strings.Contains(rec.Body.String(), `"ready":false`) {
		t.Errorf("stats without an index: %s", rec.Body.String())
	}

	// Hydration when the documents bucket is missing returns nothing.
	if err := s.DBUpdateC("v", func(tx *bolt.Tx) error { return tx.DeleteBucket(s.BucketNames.Docs) }); err != nil {
		t.Fatal(err)
	}
	if got := s.hydrateGeoResults("v", []geo.GeoResult{{DocID: "x"}}, false, false); len(got) != 0 {
		t.Errorf("hydrate without a bucket: %v", got)
	}
}

func TestGeoCov_RESTReindexFailures(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MDDB_GEO_DATA_DIR", dir)
	s, cleanup := geoCovServer(t)
	defer cleanup()
	if err := os.WriteFile(filepath.Join(dir, "pl.csv"), []byte("00-001,52.231,21.006\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := geoPost(s.handleGeoReindex, `{"loadPostcodes":[{"country":"PL","csvPath":"missing.csv"}]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a CSV that is not there: %d %s", rec.Code, rec.Body.String())
	}
	if rec := geoPost(s.handleGeoReindex, `{"loadPostcodes":[{"country":"","csvPath":"pl.csv"}]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a CSV without a country: %d %s", rec.Code, rec.Body.String())
	}
	if rec := geoPost(s.handleGeoReindex, `{"collection":"v"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"points":3`) {
		t.Errorf("reindex: %d %s", rec.Code, rec.Body.String())
	}
	_ = s.collectionDB("v").Close() // the file holding the collection, in either layout
	if rec := geoPost(s.handleGeoReindex, `{"collection":"v"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("reindex over a closed store: %d", rec.Code)
	}
}

func TestGeoCov_GRPC(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MDDB_GEO_DATA_DIR", dir)
	s, cleanup := geoCovServer(t)
	defer cleanup()
	ctx := context.Background()
	g := &GRPCServer{server: s}

	geoGRPCSearches(ctx, t, g)
	if _, err := g.GeoEncode(ctx, &proto.GeoEncodeRequest{Lat: 52.52, Lng: 13.405, Precision: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GeoDecode(ctx, &proto.GeoDecodeRequest{Geohash: "u33db"}); err != nil {
		t.Fatal(err)
	}
	geoGRPCReindexAndStats(ctx, t, g, dir)
	geoGRPCReindexRefusals(ctx, t, g, s)
	geoGRPCWithoutDocuments(ctx, t, g, s)

	_ = s.collectionDB("v").Close() // the file holding the collection, in either layout
	if _, err := g.GeoReindex(ctx, &proto.GeoReindexRequest{Collection: "v"}); status.Code(err) != codes.Internal {
		t.Fatalf("reindex over a closed store: %v", err)
	}
}

// geoGRPCSearches checks radius and bounding-box search over gRPC, filtered
// and unfiltered, with and without content.
func geoGRPCSearches(ctx context.Context, t *testing.T, g *GRPCServer) {
	t.Helper()
	shops := map[string]*proto.MetaValues{"kind": {Values: []string{"shop"}}}
	none := map[string]*proto.MetaValues{"kind": {Values: []string{"none"}}}

	r, err := g.GeoSearch(ctx, &proto.GeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000, FilterMeta: shops, IncludeContent: true})
	if err != nil || r.Total != 3 || r.Results[0].Document.ContentMd == "" {
		t.Fatalf("radius filtered: %v %+v", err, r)
	}
	// Unfiltered, the ghost and the garbage are found and skipped.
	if r, err := g.GeoSearch(ctx, &proto.GeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000}); err != nil || r.Total != 3 {
		t.Fatalf("radius: %v %+v", err, r)
	}
	w, err := g.GeoWithin(ctx, &proto.GeoWithinRequest{Collection: "v", MinLat: 52, MaxLat: 53, MinLng: 13, MaxLng: 14, FilterMeta: shops})
	if err != nil || w.Total != 3 || w.Results[0].Document.ContentMd != "" {
		t.Fatalf("within filtered: %v %+v", err, w)
	}
	w, err = g.GeoWithin(ctx, &proto.GeoWithinRequest{Collection: "v", MinLat: 52, MaxLat: 53, MinLng: 13, MaxLng: 14, IncludeContent: true})
	if err != nil || w.Total != 3 || w.Results[0].Document.ContentMd == "" {
		t.Fatalf("within: %v %+v", err, w)
	}
	if w, err := g.GeoWithin(ctx, &proto.GeoWithinRequest{Collection: "v", MinLat: 52, MaxLat: 53, MinLng: 13, MaxLng: 14, FilterMeta: none}); err != nil || w.Total != 0 {
		t.Fatalf("within, no match: %v %+v", err, w)
	}
}

// geoGRPCReindexAndStats rebuilds the collection while loading a postcode
// dataset written to dir, and checks the stats report both.
func geoGRPCReindexAndStats(ctx context.Context, t *testing.T, g *GRPCServer, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "pl.csv"), []byte("00-001,52.231,21.006\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rx, err := g.GeoReindex(ctx, &proto.GeoReindexRequest{Collection: "v", LoadPostcodes: []*proto.GeoPostcodeLoadProto{{Country: "PL", CsvPath: "pl.csv"}}})
	if err != nil || rx.Points != 3 || rx.PostcodesLoaded["PL"] != 1 {
		t.Fatalf("reindex: %v %+v", err, rx)
	}
	st, err := g.GeoStats(ctx, &proto.GeoStatsRequest{})
	if err != nil || st.Collections["v"].GetLastRebuildUnix() == 0 || st.PostcodeDatasets["PL"] != 1 || !st.Ready {
		t.Fatalf("stats: %v %+v", err, st)
	}
	if st, err := (&GRPCServer{server: &Server{}}).GeoStats(ctx, &proto.GeoStatsRequest{}); err != nil || len(st.Collections) != 0 {
		t.Fatalf("stats without an index: %v %+v", err, st)
	}
}

// geoGRPCReindexRefusals checks reindex refuses bad postcode loads, and
// creates the postcode lookup when the index has none.
func geoGRPCReindexRefusals(ctx context.Context, t *testing.T, g *GRPCServer, s *Server) {
	t.Helper()
	for name, req := range map[string]*proto.GeoReindexRequest{
		"outside the data dir": {LoadPostcodes: []*proto.GeoPostcodeLoadProto{{Country: "PL", CsvPath: "../x.csv"}}},
		"missing CSV":          {LoadPostcodes: []*proto.GeoPostcodeLoadProto{{Country: "PL", CsvPath: "missing.csv"}}},
		"no country":           {LoadPostcodes: []*proto.GeoPostcodeLoadProto{{Country: "", CsvPath: "pl.csv"}}},
	} {
		if _, err := g.GeoReindex(ctx, req); err == nil {
			t.Errorf("reindex, %s: accepted", name)
		}
	}
	s.GeoIndex.SetPostcodes(nil)
	if _, err := g.GeoReindex(ctx, &proto.GeoReindexRequest{LoadPostcodes: []*proto.GeoPostcodeLoadProto{{Country: "PL", CsvPath: "pl.csv"}}}); err != nil {
		t.Fatalf("reindex creating the postcode lookup: %v", err)
	}
}

// geoGRPCWithoutDocuments checks documents the index knows and the store
// cannot give back are skipped: a store without a documents bucket gives
// back nothing.
func geoGRPCWithoutDocuments(ctx context.Context, t *testing.T, g *GRPCServer, s *Server) {
	t.Helper()
	if err := s.DBUpdateC("v", func(tx *bolt.Tx) error { return tx.DeleteBucket(s.BucketNames.Docs) }); err != nil {
		t.Fatal(err)
	}
	if r, err := g.GeoSearch(ctx, &proto.GeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000}); err != nil || r.Total != 0 {
		t.Fatalf("radius without documents: %v %+v", err, r)
	}
	if w, err := g.GeoWithin(ctx, &proto.GeoWithinRequest{Collection: "v", MinLat: 52, MaxLat: 53, MinLng: 13, MaxLng: 14}); err != nil || w.Total != 0 {
		t.Fatalf("within without documents: %v %+v", err, w)
	}
}

func TestGeoCov_GRPCRefusals(t *testing.T) {
	s, cleanup := geoCovServer(t)
	defer cleanup()
	ctx := context.Background()
	g := &GRPCServer{server: s}
	want := func(name string, err error, code codes.Code) {
		t.Helper()
		if status.Code(err) != code {
			t.Errorf("%s: %v, want %v", name, err, code)
		}
	}
	_, err := g.GeoWithin(ctx, &proto.GeoWithinRequest{Collection: "v", MinLat: 2, MaxLat: 1})
	want("within bbox", err, codes.InvalidArgument)
	_, err = g.GeoDecode(ctx, &proto.GeoDecodeRequest{Geohash: "ail"})
	want("decode", err, codes.InvalidArgument)

	s.GeoIndex = geo.NewGeoIndex()
	_, err = g.GeoWithin(ctx, &proto.GeoWithinRequest{Collection: "v", MinLat: 1, MaxLat: 2})
	want("within not ready", err, codes.Unavailable)

	gs := s.GeoStore
	s.GeoStore = nil
	_, err = g.GeoReindex(ctx, &proto.GeoReindexRequest{})
	want("reindex without the subsystem", err, codes.FailedPrecondition)
	s.GeoStore = gs

	covEDenyAll(t, s)
	_, err = g.GeoSearch(ctx, &proto.GeoSearchRequest{Collection: "v", Lat: 1, Lng: 1, RadiusMeters: 1})
	want("search as anonymous", err, codes.PermissionDenied)
	_, err = g.GeoWithin(ctx, &proto.GeoWithinRequest{Collection: "v"})
	want("within as anonymous", err, codes.PermissionDenied)
	_, err = g.GeoReindex(ctx, &proto.GeoReindexRequest{Collection: "v"})
	want("reindex as anonymous", err, codes.PermissionDenied)

	s.Mode = ModeRead
	_, err = g.GeoReindex(ctx, &proto.GeoReindexRequest{Collection: "v"})
	want("reindex read-only", err, codes.PermissionDenied)
}

func TestGeoCov_MCPDirect(t *testing.T) {
	s, cleanup := geoCovServer(t)
	defer cleanup()
	ctx := context.Background()
	c := NewDirectClient(s)

	geoMCPSearches(ctx, t, c)
	if h, err := c.GeoEncode(ctx, 52.52, 13.405, 0); err != nil || len(h) != geo.GeohashMaxPrecision {
		t.Fatalf("encode at the default precision: %v %q", err, h)
	}
	s.GeoIndex.SetPostcodes(geo.NewPostcodeLookup())
	if st, err := c.GeoStats(ctx); err != nil || st.Collections["v"] == 0 || st.PostcodeDatasets == nil {
		t.Fatalf("stats: %v %+v", err, st)
	}
	if st, err := NewDirectClient(&Server{}).GeoStats(ctx); err != nil || len(st.Collections) != 0 {
		t.Fatalf("stats without an index: %v %+v", err, st)
	}
	geoMCPNotReady(ctx, t, c, s)

	// Hydration without a documents bucket gives back nothing.
	if err := s.DBUpdateC("v", func(tx *bolt.Tx) error { return tx.DeleteBucket(s.BucketNames.Docs) }); err != nil {
		t.Fatal(err)
	}
	if got := c.loadGeoResults("v", []geo.GeoResult{{DocID: "x"}}, false, false); len(got) != 0 {
		t.Fatalf("load without a bucket: %v", got)
	}
}

// geoMCPSearches checks radius search by both algorithms and bounding-box
// search through the direct MCP client, and the bounding box's refusals.
func geoMCPSearches(ctx context.Context, t *testing.T, c *DirectClient) {
	t.Helper()
	shops := map[string][]string{"kind": {"shop"}}
	for _, algo := range []string{"rtree", "geohash"} {
		r, err := c.GeoSearch(ctx, &MCPGeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000, Algorithm: algo, FilterMeta: shops, IncludeContent: true})
		if err != nil || r.Total != 3 {
			t.Fatalf("radius %s: %v %+v", algo, err, r)
		}
	}
	if r, err := c.GeoSearch(ctx, &MCPGeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000}); err != nil || r.Total != 3 {
		t.Fatalf("radius, skipping the ghost and the garbage: %v %+v", err, r)
	}
	w, err := c.GeoWithin(ctx, &MCPGeoWithinRequest{Collection: "v", MinLat: 52, MaxLat: 53, MinLng: 13, MaxLng: 14, FilterMeta: shops})
	if err != nil || w.Total != 3 || w.Results[0].Document.ContentMD != "" {
		t.Fatalf("within filtered: %v %+v", err, w)
	}
	if w, err := c.GeoWithin(ctx, &MCPGeoWithinRequest{Collection: "v", MinLat: 52, MaxLat: 53, MinLng: 13, MaxLng: 14, FilterMeta: map[string][]string{"kind": {"none"}}}); err != nil || w.Total != 0 {
		t.Fatalf("within, no match: %v %+v", err, w)
	}
	for name, req := range map[string]*MCPGeoWithinRequest{
		"no collection": {MinLat: 1, MaxLat: 2},
		"bbox":          {Collection: "v", MinLat: 2, MaxLat: 1},
	} {
		if _, err := c.GeoWithin(ctx, req); err == nil {
			t.Errorf("within %s: accepted", name)
		}
	}
}

// geoMCPNotReady swaps in empty indexes and checks radius by either index,
// and the bounding box, refuse while not ready.
func geoMCPNotReady(ctx context.Context, t *testing.T, c *DirectClient, s *Server) {
	t.Helper()
	s.GeoHashIndex = geo.NewGeoHashIndex()
	if _, err := c.GeoSearch(ctx, &MCPGeoSearchRequest{Collection: "v", Lat: 1, Lng: 1, RadiusMeters: 1, Algorithm: "geohash"}); err == nil {
		t.Error("geohash not ready: accepted")
	}
	s.GeoIndex = geo.NewGeoIndex()
	if _, err := c.GeoSearch(ctx, &MCPGeoSearchRequest{Collection: "v", Lat: 1, Lng: 1, RadiusMeters: 1}); err == nil {
		t.Error("rtree not ready: accepted")
	}
	if _, err := c.GeoWithin(ctx, &MCPGeoWithinRequest{Collection: "v", MinLat: 1, MaxLat: 2}); err == nil {
		t.Error("within not ready: accepted")
	}
}
