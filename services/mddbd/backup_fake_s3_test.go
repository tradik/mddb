package main

import (
	"bufio"
	"bytes"
	"crypto/md5" // #nosec G501 -- an S3 ETag is an MD5 by definition; nothing here is security
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 is an in-process S3-compatible server — enough of the protocol for
// minio-go's bucket check and creation, PutObject (aws-chunked streaming
// uploads included), GetObject, ListObjectsV2 and RemoveObject. MinIO is not
// available where these tests run, so the scheduled backup's S3 target is
// tested against this.
type fakeS3 struct {
	*httptest.Server
	mu      sync.Mutex
	buckets map[string]map[string][]byte
	// failures: method → status the next matching requests get.
	fail map[string]int
	// corrupt makes every GET of an object return other bytes.
	corrupt bool
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	f := &fakeS3{buckets: map[string]map[string][]byte{}, fail: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// endpoint is the host:port minio-go wants.
func (f *fakeS3) endpoint() string { return strings.TrimPrefix(f.URL, "http://") }

// failNext makes requests with method (or "LIST" for a listing) fail with code.
func (f *fakeS3) failNext(method string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[method] = code
}

// keys lists a bucket's keys, sorted.
func (f *fakeS3) keys(bucket string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.buckets[bucket] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// object returns one object's bytes.
func (f *fakeS3) object(bucket, key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buckets[bucket][key]
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	op := r.Method
	if op == http.MethodGet && key == "" && r.URL.Query().Has("list-type") {
		op = "LIST"
	}
	if code := f.fail[op]; code != 0 {
		w.WriteHeader(code)
		return
	}
	objs, exists := f.buckets[bucket]
	switch {
	case r.URL.Query().Has("location"):
		_, _ = io.WriteString(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`)
	case key == "" && r.Method == http.MethodHead:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
		}
	case key == "" && r.Method == http.MethodPut:
		f.buckets[bucket] = map[string][]byte{}
	case op == "LIST":
		f.list(w, objs, r.URL.Query().Get("prefix"))
	case r.Method == http.MethodPut:
		body, err := readS3Body(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		objs[key] = body
		w.Header().Set("ETag", etag(body))
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		body, ok := objs[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>no such key</Message></Error>`)
			return
		}
		if f.corrupt {
			body = append([]byte("corrupt"), body...)
		}
		w.Header().Set("ETag", etag(body))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	case r.Method == http.MethodDelete:
		delete(objs, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeS3) list(w http.ResponseWriter, objs map[string][]byte, prefix string) {
	type content struct {
		Key          string
		Size         int
		ETag         string
		LastModified string
	}
	var res struct {
		XMLName     xml.Name `xml:"ListBucketResult"`
		IsTruncated bool
		Contents    []content
	}
	for k, v := range objs {
		if strings.HasPrefix(k, prefix) {
			res.Contents = append(res.Contents, content{Key: k, Size: len(v), ETag: etag(v),
				LastModified: time.Now().UTC().Format(time.RFC3339)})
		}
	}
	sort.Slice(res.Contents, func(i, j int) bool { return res.Contents[i].Key < res.Contents[j].Key })
	out, _ := xml.Marshal(res)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(out)
}

func etag(b []byte) string {
	sum := md5.Sum(b) // #nosec G401 -- the S3 ETag format
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// readS3Body returns an upload's bytes, decoding aws-chunked encoding: minio-go
// streams over plain HTTP as "<hex size>[;chunk-signature=…]\r\n<data>\r\n"
// chunks ending in a zero-size one.
func readS3Body(r *http.Request) ([]byte, error) {
	if !strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		return io.ReadAll(r.Body)
	}
	br := bufio.NewReader(r.Body)
	var out bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeHex, _, _ := strings.Cut(strings.TrimSpace(line), ";")
		n, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out.Bytes(), nil
		}
		if _, err := io.CopyN(&out, br, n); err != nil {
			return nil, err
		}
		if _, err := br.Discard(2); err != nil { // the chunk's CRLF
			return nil, err
		}
	}
}

// fakeS3Action is a backup action for the bucket "backups" on f.
func fakeS3Action(f *fakeS3, prefix string, keep int) *BackupAction {
	return &BackupAction{
		Target: BackupTargetS3, Bucket: "backups", Prefix: prefix, Keep: keep,
		StorageConfig: &StorageConfigDef{Endpoint: f.endpoint(), Region: "us-east-1",
			AccessKey: "test", SecretKey: "test-secret"},
	}
}
