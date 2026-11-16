package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The Markdown a result file becomes in docs/BENCHMARK.md. Each result owns
// one block between a begin and an end marker named after its file; syncDocs
// rewrites the blocks and the doc guard test fails when a block and its file
// disagree, so a number in the document always has a file behind it.

// blockMarkers are the comments around the block of result name.
func blockMarkers(name string) (begin, end string) {
	return "<!-- bench-profile:" + name + ":begin -->", "<!-- bench-profile:" + name + ":end -->"
}

// render is the Markdown of one result.
func render(r *Result) string {
	var b strings.Builder
	e := r.Environment
	fmt.Fprintf(&b, "MDDB %s (`%s`), profile `%s`, seed %d, recorded %s on %s — %d cores, %.1f GB, %s/%s, kernel %s, %s. Calibration %.3f ms.\n",
		r.Version, r.Commit, r.Profile, r.Seed, strings.SplitN(r.RecordedAt, "T", 2)[0], orUnknown(e.CPU), e.Cores, e.MemoryGB,
		e.OS, e.Arch, orUnknown(e.Kernel), e.GoVersion, r.CalibrationMs)
	if len(r.Search) > 0 {
		fmt.Fprintf(&b, "\nSearch — %d vectors × %d dims in %d topics; %d timed queries × %d sequential rounds for latency, %d clients for QPS.\n\n",
			r.Corpus.Vectors, r.Corpus.Dims, r.Corpus.Topics, r.Corpus.Queries, r.Corpus.Rounds, r.Corpus.Clients)
		b.WriteString("| Scenario | Answered by | p50 ms | p95 ms | p99 ms | QPS | recall@10 |\n|---|---|---:|---:|---:|---:|---:|\n")
		for _, m := range r.Search {
			fmt.Fprintf(&b, "| `%s` | %s | %.3f | %.3f | %.3f | %.0f | %s |\n", m.Name, orDash(m.Ran), m.P50, m.P95, m.P99, m.QPS, recallText(m.Recall10))
		}
	}
	if len(r.Ingest) > 0 {
		b.WriteString("\n| Ingest | Documents | Batch | Embedding | docs/s | request p50 ms | p95 ms | p99 ms |\n|---|---:|---:|---|---:|---:|---:|---:|\n")
		for _, m := range r.Ingest {
			fmt.Fprintf(&b, "| `%s` | %d | %d | %s | %.0f | %.3f | %.3f | %.3f |\n", m.Name, m.Docs, m.BatchSize, m.Embedding, m.DocsPerSec, m.P50, m.P95, m.P99)
		}
	}
	rows := operationRows(r)
	if len(rows) > 0 {
		b.WriteString("\n| Mixed load, start-up and recovery | Value |\n|---|---:|\n")
		for _, row := range rows {
			fmt.Fprintf(&b, "| %s | %s |\n", row[0], row[1])
		}
	}
	return b.String()
}

// rows collects label and value pairs.
type rows [][2]string

func (rs *rows) add(label, format string, args ...any) {
	*rs = append(*rs, [2]string{label, fmt.Sprintf(format, args...)})
}

// operationRows are the single-value measurements, as label and value.
func operationRows(r *Result) [][2]string {
	var rs rows
	add := rs.add
	if m := r.Mixed; m != nil {
		add(fmt.Sprintf("Mixed (%d readers, %d writer, %.0f s): search p50 / p95 / p99", m.Readers, m.Writers, m.Seconds),
			"%.3f / %.3f / %.3f ms", m.Read.P50, m.Read.P95, m.Read.P99)
		add("Mixed: searches per second", "%.0f", m.ReadQPS)
		add("Mixed: write p50 / p95 / p99", "%.3f / %.3f / %.3f ms", m.Write.P50, m.Write.P95, m.Write.P99)
		add("Mixed: writes per second", "%.0f", m.WritesPerSec)
	}
	if s := r.Startup; s != nil {
		add("Clean restart of the search node: process start to healthy", "%.3f s", s.HealthySeconds)
		add("Clean restart of the search node: process start to every vector index answering", "%.3f s", s.IndexesReadySeconds)
		if len(s.IndexReadySeconds) > 0 {
			add("Clean restart: process start to each index's first answer", "%s", indexReady(s.IndexReadySeconds))
		}
		add("Peak resident memory of the search node", "%.1f MiB", s.PeakRSSMB)
	}
	if c := r.Recovery; c != nil {
		add("kill -9 under write: acknowledged writes / lost after restart", "%d / %d", c.AckedWrites, c.LostWrites)
		add("kill -9 under write: process start to ready", "%.3f s", c.ReadySeconds)
		add("Same node, clean restart: process start to ready", "%.3f s", c.CleanRestartSeconds)
	}
	if p := r.Replication; p != nil {
		rs.replication(p)
	}
	return rs
}

// replication adds the leader and follower rows.
func (rs *rows) replication(p *Replication) {
	label := fmt.Sprintf("Replication lag, %d probes under %d writes/s: p50 / p95 / p99", p.LagProbes, p.LagLoad)
	if p.LateProbes < p.LagProbes {
		rs.add(label, "%.3f / %.3f / %.3f ms", p.Lag.P50, p.Lag.P95, p.Lag.P99)
	} else {
		rs.add(label, "— (no probe arrived)")
	}
	rs.add("Replication: probes not on the follower within 30 s", "%d of %d", p.LateProbes, p.LagProbes)
	catchup := fmt.Sprintf("Follower catch-up after missing %d documents: process start to in sync", p.CatchupDocs)
	if p.InSync {
		rs.add(catchup, "%.3f s", p.CatchupSeconds)
	} else {
		rs.add(catchup, "not in sync after %.0f s", p.CatchupSeconds)
	}
	rs.add("Follower checksum and document count equal the leader's", "%t", p.InSync)
	if p.OutOfSync != "" {
		rs.add("Follower out of sync", "%s", strings.ReplaceAll(p.OutOfSync, "|", "/"))
	}
}

// indexReady is each algorithm's seconds, in report order.
func indexReady(ready map[string]float64) string {
	parts := make([]string, 0, len(ready))
	for _, a := range vectorAlgorithms {
		if s, ok := ready[a]; ok {
			parts = append(parts, fmt.Sprintf("%s %.1f s", a, s))
		}
	}
	return strings.Join(parts, ", ")
}

// orDash is s, or "—" where the server did not say (hybrid search reports no
// algorithm).
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// resultFiles are the result files of dir by block name (the file name
// without .json), in name order.
func resultFiles(dir string) ([]string, map[string]*Result, error) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(paths)
	names, out := make([]string, 0, len(paths)), map[string]*Result{}
	for _, path := range paths {
		r, err := readResult(path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		names, out[name] = append(names, name), r
	}
	return names, out, nil
}

// block is the text between the markers of name in doc, and where it sits.
func block(doc, name string) (body string, start, end int, err error) {
	begin, finish := blockMarkers(name)
	start = strings.Index(doc, begin)
	end = strings.Index(doc, finish)
	if start < 0 || end < start {
		return "", 0, 0, fmt.Errorf("no block for %s: add the lines %q and %q where its table belongs", name, begin, finish)
	}
	start += len(begin)
	return doc[start:end], start, end, nil
}

// docDrift lists, for every result file of dir, how its block in doc differs
// from what the file renders to; empty when the document is in sync.
func docDrift(doc, dir string) ([]string, error) {
	names, results, err := resultFiles(dir)
	if err != nil {
		return nil, err
	}
	var drift []string
	for _, name := range names {
		body, _, _, err := block(doc, name)
		switch {
		case err != nil:
			drift = append(drift, err.Error())
		case strings.TrimSpace(body) != strings.TrimSpace(render(results[name])):
			drift = append(drift, fmt.Sprintf("the block of %s differs from %s.json", name, name))
		}
	}
	return drift, nil
}

// syncDocs rewrites the block of every result file of dir in the document
// at docPath.
func syncDocs(docPath, dir string) error {
	raw, err := os.ReadFile(docPath) // #nosec G304 -- the repository's own document, named by the operator
	if err != nil {
		return err
	}
	doc := string(raw)
	names, results, err := resultFiles(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		_, start, end, err := block(doc, name)
		if err != nil {
			return err
		}
		doc = doc[:start] + "\n" + render(results[name]) + doc[end:]
	}
	return os.WriteFile(docPath, []byte(doc), 0o644) // #nosec G306 G703 -- a tracked document, world-readable like the rest of the repository
}
