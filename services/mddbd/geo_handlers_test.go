package main

import (
	"bytes"
	"encoding/json"
	"mddb/internal/cache"
	"mddb/internal/geo"
	"mddb/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// newTestServerForGeo creates a minimal Server wired up with a GeoStore
// and both geo indexes, marked ready. Returned Server has no FTS, no
// vector — just geo — so tests stay focused and fast.
func newTestServerForGeo(t *testing.T) (*Server, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		DB:   db,
		Path: dbPath,
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache: cache.NewDocumentCache(100, 60),
	}
	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	s.GeoStore = geo.NewGeoStore(db)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	s.GeoIndex = geo.NewGeoIndex()
	s.GeoIndex.SetReady()
	s.GeoHashIndex = geo.NewGeoHashIndex()
	s.GeoHashIndex.SetReady()
	return s, func() { _ = db.Close() }
}

func TestHandleGeoSearch_MissingCollection(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()

	body := []byte(`{"lat":52.52,"lng":13.405,"radiusMeters":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleGeoSearch_InvalidRadius(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"collection":"v","lat":52,"lng":13,"radiusMeters":0}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleGeoSearch_InvalidLatLng(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"collection":"v","lat":200,"lng":0,"radiusMeters":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleGeoSearch_UnknownAlgorithm(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"collection":"v","lat":52,"lng":13,"radiusMeters":1000,"algorithm":"bogus"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleGeoSearch_IndexNotReady(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	// Reset ready flag on a fresh index.
	s.GeoIndex = geo.NewGeoIndex()
	body := []byte(`{"collection":"v","lat":52,"lng":13,"radiusMeters":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got status %d, want 503", w.Code)
	}
}

func TestHandleGeoSearch_EmptyResults(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"collection":"v","lat":52,"lng":13,"radiusMeters":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}
	var resp GeoSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Algorithm != "rtree" {
		t.Errorf("algorithm=%q, want rtree", resp.Algorithm)
	}
	if resp.Total != 0 {
		t.Errorf("total=%d, want 0", resp.Total)
	}
}

func TestHandleGeoWithin_InvalidBBox(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"collection":"v","minLat":10,"maxLat":5,"minLng":0,"maxLng":10}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-within", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoWithin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleGeoEncode_Valid(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"lat":52.52,"lng":13.405,"precision":8}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-encode", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoEncode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var resp GeoEncodeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Geohash) != 8 {
		t.Errorf("geohash length=%d, want 8", len(resp.Geohash))
	}
}

func TestHandleGeoEncode_InvalidLatLng(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"lat":999,"lng":0}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-encode", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoEncode(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleGeoDecode_Valid(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"geohash":"u33d8"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-decode", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoDecode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var resp GeoDecodeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.MinLat == resp.MaxLat {
		t.Error("bbox should have non-zero extent")
	}
}

func TestHandleGeoDecode_MissingHash(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{"geohash":""}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-decode", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoDecode(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestHandleGeoStats_Empty(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/v1/geo-stats", nil)
	w := httptest.NewRecorder()
	s.handleGeoStats(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var resp GeoStatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Ready {
		t.Error("expected ready=true on fresh index")
	}
}

func TestHandleGeoReindex_Empty(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-reindex", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoReindex(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp GeoReindexResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Points != 0 {
		t.Errorf("points=%d, want 0", resp.Points)
	}
}

// seedGeoDocs writes N documents with explicit geo_lat/geo_lng into the
// "docs" bucket and the in-memory GeoIndex so handlers that hydrate
// results from BoltDB actually have something to return.
func seedGeoDocs(t *testing.T, s *Server, collection string, points [][2]float64) {
	t.Helper()
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(s.BucketNames.Docs)
		for i, p := range points {
			docID := "d" + string(rune('0'+i))
			d := storage.Doc{
				ID:        docID,
				Key:       docID,
				Lang:      "en",
				Meta:      map[string][]string{"geo_lat": {fmtFloat(p[0])}, "geo_lng": {fmtFloat(p[1])}},
				ContentMD: "point " + docID,
			}
			data, err := marshalDoc(&d)
			if err != nil {
				return err
			}
			if err := b.Put(storage.DocKey(collection, docID), data); err != nil {
				return err
			}
			s.GeoIndex.Add(collection, docID, p[0], p[1])
			s.GeoHashIndex.Add(collection, docID, p[0], p[1])
		}
		return nil
	})
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'f', 6, 64) }

func TestHandleGeoSearch_WithResults(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	seedGeoDocs(t, s, "v", [][2]float64{
		{52.52, 13.405},  // Berlin center
		{52.521, 13.406}, // ~100 m away
		{48.857, 2.352},  // Paris (far)
	})

	body := []byte(`{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":5000,"includeContent":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp GeoSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 {
		t.Errorf("total=%d, want 2", resp.Total)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results=%d, want 2", len(resp.Results))
	}
	// Sorted ascending by distance.
	if resp.Results[0].DistanceMeters > resp.Results[1].DistanceMeters {
		t.Error("results not sorted by distance")
	}
	// includeContent=true means ContentMD must be present.
	if resp.Results[0].Document.ContentMD == "" {
		t.Error("expected ContentMD to be populated with includeContent=true")
	}
}

func TestHandleGeoSearch_IncludeContentFalse(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	seedGeoDocs(t, s, "v", [][2]float64{{52.52, 13.405}})
	body := []byte(`{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var resp GeoSearchResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Results) != 1 {
		t.Fatalf("results=%d, want 1", len(resp.Results))
	}
	if resp.Results[0].Document.ContentMD != "" {
		t.Error("expected ContentMD to be stripped with includeContent=false")
	}
}

func TestHandleGeoSearch_GeohashAlgorithm(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	seedGeoDocs(t, s, "v", [][2]float64{{52.52, 13.405}, {52.53, 13.41}})
	body := []byte(`{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":5000,"algorithm":"geohash"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoSearch(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp GeoSearchResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Algorithm != "geohash" {
		t.Errorf("algorithm=%q, want geohash", resp.Algorithm)
	}
	if resp.Total < 1 {
		t.Errorf("expected >= 1 result, got %d", resp.Total)
	}
}

func TestHandleGeoWithin_WithResults(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	seedGeoDocs(t, s, "v", [][2]float64{
		{52.52, 13.405}, // inside
		{52.53, 13.41},  // inside
		{48.857, 2.352}, // outside
	})
	body := []byte(`{"collection":"v","minLat":52.5,"maxLat":52.6,"minLng":13.3,"maxLng":13.5}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-within", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoWithin(w, req)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var resp GeoWithinResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Errorf("total=%d, want 2", resp.Total)
	}
}

func TestHandleGeoReindex_LoadPostcodes(t *testing.T) {
	// Write a tiny CSV inside the geo data directory and pass its name to the
	// reindex handler. The directory is where CSVs are confined to: the
	// endpoint used to accept any path on the filesystem.
	dir := t.TempDir()
	t.Setenv("MDDB_GEO_DATA_DIR", dir)
	csvPath := filepath.Join(dir, "pl.csv")
	if err := os.WriteFile(csvPath, []byte("00-001,52.231,21.006\n"), 0600); err != nil {
		t.Fatal(err)
	}

	s, cleanup := newTestServerForGeo(t)
	defer cleanup()

	body, _ := json.Marshal(GeoReindexRequest{
		LoadPostcodes: []GeoPostcodeLoad{{Country: "PL", CSVPath: csvPath}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-reindex", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoReindex(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp GeoReindexResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.PostcodesLoaded["PL"] != 1 {
		t.Errorf("postcodesLoaded[PL]=%d, want 1", resp.PostcodesLoaded["PL"])
	}
	// The lookup must now be attached to GeoIndex.
	if pc := s.GeoIndex.Postcodes(); pc == nil {
		t.Error("expected postcode lookup to be attached to GeoIndex")
	}
}

// CodeQL go/path-injection: csvPath came from the request body and went
// straight to os.Open, so this endpoint would read any file the process could.
func TestHandleGeoReindexRefusesAPathOutsideTheDataDir(t *testing.T) {
	t.Setenv("MDDB_GEO_DATA_DIR", t.TempDir())

	s, cleanup := newTestServerForGeo(t)
	defer cleanup()

	for _, hostile := range []string{
		"/etc/passwd",
		"../../../etc/passwd",
		"../outside.csv",
	} {
		body, _ := json.Marshal(GeoReindexRequest{
			LoadPostcodes: []GeoPostcodeLoad{{Country: "PL", CSVPath: hostile}},
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/geo-reindex", bytes.NewReader(body))
		w := httptest.NewRecorder()
		s.handleGeoReindex(w, req)

		if w.Code == http.StatusOK {
			t.Errorf("%s was accepted", hostile)
		}
	}
}

// A symlink inside the directory pointing out of it is the escape a naive
// prefix check misses; confineToDir resolves both ends before comparing.
func TestHandleGeoReindexRefusesASymlinkOutOfTheDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MDDB_GEO_DATA_DIR", dir)

	outside := filepath.Join(t.TempDir(), "secret.csv")
	if err := os.WriteFile(outside, []byte("00-001,1,1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "pl.csv")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	s, cleanup := newTestServerForGeo(t)
	defer cleanup()

	body, _ := json.Marshal(GeoReindexRequest{
		LoadPostcodes: []GeoPostcodeLoad{{Country: "PL", CSVPath: "pl.csv"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/geo-reindex", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleGeoReindex(w, req)

	if w.Code == http.StatusOK {
		t.Error("a symlink out of the data directory was followed")
	}
}
