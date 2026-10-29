package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const evalAnswer = `{"collection":"kb","k":10,"queries":2,"results":[
 {"config":"fts/bm25","recallAtK":0.9,"ndcgAtK":0.7,"mrr":0.8,"p50Ms":1.2,"p99Ms":3.4,"tokens":120},
 {"config":"vector","error":"no embedding provider"}]}`

func TestEval_PrintsAndSendsConfigs(t *testing.T) {
	srv := newFakeServer(t, map[string]interface{}{"/v1/eval": evalAnswer})
	file := filepath.Join(t.TempDir(), "q.json")
	if err := os.WriteFile(file, []byte(`{"queries":[{"query":"backup","expectedKeys":["backup.md"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, srv.URL, "eval", "kb", "--config", "fts/bm25", "--config", "vector/hnsw", "--config", "hybrid",
		"--queries", file, "--k", "10")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fts/bm25") || !strings.Contains(out, "0.900") || !strings.Contains(out, "error: no embedding") {
		t.Fatalf("output: %s", out)
	}
	body := srv.lastCall(t).Body
	configs := body["configs"].([]interface{})
	if configs[0].(map[string]interface{})["algorithm"] != "bm25" || configs[1].(map[string]interface{})["vectorAlgorithm"] != "hnsw" ||
		len(configs[2].(map[string]interface{})) != 1 || len(body["queries"].([]interface{})) != 1 || body["k"] != float64(10) {
		t.Fatalf("request: %+v", body)
	}
	if out, _ = runCLI(t, srv.URL, "--json", "eval", "kb"); !strings.Contains(out, `"recallAtK":0.9`) {
		t.Fatalf("json: %s", out)
	}
}

func TestEval_FailBelow(t *testing.T) {
	srv := newFakeServer(t, map[string]interface{}{"/v1/eval": evalAnswer})
	// The erroring config fails the gate as well as a low score does.
	_, err := runCLI(t, srv.URL, "eval", "kb", "--fail-below", "recall@10=0.95")
	if err == nil || !strings.Contains(err.Error(), "recall 0.900 < 0.950") || !strings.Contains(err.Error(), "vector:") {
		t.Fatalf("gate: %v", err)
	}
	if srv.lastCall(t).Body["k"] != float64(10) {
		t.Fatal("@10 sets k")
	}
	passing := newFakeServer(t, map[string]interface{}{"/v1/eval": `{"k":5,"queries":1,"results":[{"config":"fts","recallAtK":1,"ndcgAtK":1,"mrr":1}]}`})
	if _, err := runCLI(t, passing.URL, "eval", "kb", "--fail-below", "recall=0.8, ndcg=0.5,mrr=0.5"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"recall", "recall=x", "bleu=0.5", "recall@0=0.5", "recall@5=0.5,ndcg@10=0.5"} {
		if _, err := runCLI(t, srv.URL, "eval", "kb", "--fail-below", bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if _, err := runCLI(t, srv.URL, "eval", "kb", "--k", "5", "--fail-below", "recall@10=0.5"); err == nil {
		t.Fatal("k disagreement")
	}
}

func TestEval_Failures(t *testing.T) {
	srv := newFakeServer(t, map[string]interface{}{"/v1/eval": failure{400, `{"error":"no evaluation queries"}`}})
	if _, err := runCLI(t, srv.URL, "eval", "kb"); err == nil {
		t.Fatal("server error")
	}
	garbled := newFakeServer(t, map[string]interface{}{"/v1/eval": `[1]`})
	if _, err := runCLI(t, garbled.URL, "eval", "kb"); err == nil {
		t.Fatal("bad response")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"x":1}`), 0o600)
	for _, f := range []string{bad, filepath.Join(dir, "missing.json")} {
		if _, err := runCLI(t, srv.URL, "eval", "kb", "--queries", f); err == nil {
			t.Fatalf("%s accepted", f)
		}
	}
	arr := filepath.Join(dir, "arr.json")
	_ = os.WriteFile(arr, []byte(`[{"query":"q","expectedKeys":["a"]}]`), 0o600)
	if q, err := readEvalQueries(arr); err != nil || len(q) != 1 {
		t.Fatalf("array: %v", err)
	}
}
