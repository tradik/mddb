package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// mddb-cli eval (SRCH-019): measure retrieval on an evaluation set and, with
// --fail-below, fail a CI job when quality drops under a threshold.

// evalResult mirrors the parts of the server's answer the CLI prints.
type evalResult struct {
	K       int `json:"k"`
	Queries int `json:"queries"`
	Results []struct {
		Config string  `json:"config"`
		Recall float64 `json:"recallAtK"`
		NDCG   float64 `json:"ndcgAtK"`
		MRR    float64 `json:"mrr"`
		P50Ms  float64 `json:"p50Ms"`
		P99Ms  float64 `json:"p99Ms"`
		Tokens int     `json:"tokens"`
		Error  string  `json:"error"`
	} `json:"results"`
}

func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval <collection>",
		Short: "Measure retrieval quality on an evaluation set",
		Long: `Runs the collection's evaluation set — or the queries in --queries — under
one or more search configurations and prints recall@k, nDCG@k, MRR, latency
and returned tokens for each.

A configuration is type[/algorithm][:k1=N][:b=N]: fts/bm25, fts/bm25f, hybrid,
vector/hnsw, fts/bm25:k1=0.9:b=0.4 (BM25 parameters).
Without --config, fts/bm25, hybrid and vector are compared.

--fail-below recall@10=0.8,ndcg=0.6 exits non-zero when any configuration
scores under a threshold (metrics: recall, ndcg, mrr; "@N" sets k).`,
		Args: cobra.ExactArgs(1),
		RunE: runEval,
	}
	cmd.Flags().String("queries", "", "JSON file with queries ([{query, expectedKeys, grades}] or {queries:[...]})")
	cmd.Flags().StringSlice("config", nil, "Search configuration, repeatable: fts/bm25, hybrid, vector/hnsw")
	cmd.Flags().Int("k", 0, "Cut-off (default 10)")
	cmd.Flags().String("fail-below", "", "Thresholds, e.g. recall@10=0.8,ndcg=0.6")
	return cmd
}

func runEval(cmd *cobra.Command, args []string) error {
	k, _ := cmd.Flags().GetInt("k")
	failBelow, _ := cmd.Flags().GetString("fail-below")
	thresholds, thresholdK, err := parseThresholds(failBelow)
	if err != nil {
		return err
	}
	if thresholdK > 0 {
		if k > 0 && k != thresholdK {
			return fmt.Errorf("--k %d and --fail-below @%d disagree", k, thresholdK)
		}
		k = thresholdK
	}
	body := map[string]any{"collection": args[0], "k": k}
	specs, _ := cmd.Flags().GetStringSlice("config")
	if len(specs) > 0 {
		configs, err := parseEvalConfigs(specs)
		if err != nil {
			return err
		}
		body["configs"] = configs
	}
	if path, _ := cmd.Flags().GetString("queries"); path != "" {
		queries, err := readEvalQueries(path)
		if err != nil {
			return err
		}
		body["queries"] = queries
	}
	raw, err := newClient().Eval(context.Background(), body)
	if err != nil {
		return err
	}
	var res evalResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if outputJSON {
		fmt.Println(string(raw))
	} else {
		printEval(res)
	}
	return checkThresholds(res, thresholds)
}

// parseEvalConfigs turns "fts/bm25" into {searchType, algorithm}; the
// algorithm of a vector config names the vector index. ":k1=0.9:b=0.4"
// after it tries BM25 parameters (SRCH-018).
func parseEvalConfigs(specs []string) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		head, opts, _ := strings.Cut(spec, ":")
		typ, alg, _ := strings.Cut(head, "/")
		c := map[string]any{"searchType": typ}
		switch {
		case alg == "":
		case typ == "vector":
			c["vectorAlgorithm"] = alg
		default:
			c["algorithm"] = alg
		}
		for _, opt := range strings.Split(opts, ":") {
			if opt == "" {
				continue
			}
			name, val, _ := strings.Cut(opt, "=")
			v, err := strconv.ParseFloat(val, 64)
			field := map[string]string{"k1": "bm25K1", "b": "bm25B"}[name]
			if err != nil || field == "" {
				return nil, fmt.Errorf("--config %q: options are k1=<number> and b=<number>", spec)
			}
			c[field] = v
		}
		out = append(out, c)
	}
	return out, nil
}

// readEvalQueries accepts a bare array or an object with "queries".
func readEvalQueries(path string) ([]any, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the user names the file
	if err != nil {
		return nil, err
	}
	var list []any
	if json.Unmarshal(data, &list) == nil {
		return list, nil
	}
	var wrapped struct {
		Queries []any `json:"queries"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil || len(wrapped.Queries) == 0 {
		return nil, fmt.Errorf("%s: expected a JSON array of queries or {\"queries\": [...]}", path)
	}
	return wrapped.Queries, nil
}

// parseThresholds reads "recall@10=0.8,ndcg=0.6".
func parseThresholds(s string) (map[string]float64, int, error) {
	out := map[string]float64{}
	k := 0
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		name, val, found := strings.Cut(part, "=")
		v, err := strconv.ParseFloat(val, 64)
		if !found || err != nil {
			return nil, 0, fmt.Errorf("--fail-below %q: want metric=value", part)
		}
		metric, at, hasK := strings.Cut(strings.ToLower(name), "@")
		if metric != "recall" && metric != "ndcg" && metric != "mrr" {
			return nil, 0, fmt.Errorf("--fail-below: unknown metric %q (recall, ndcg, mrr)", metric)
		}
		if hasK {
			n, err := strconv.Atoi(at)
			if err != nil || n <= 0 || (k > 0 && n != k) {
				return nil, 0, fmt.Errorf("--fail-below %q: one positive k for all metrics", part)
			}
			k = n
		}
		out[metric] = v
	}
	return out, k, nil
}

func printEval(res evalResult) {
	fmt.Printf("%d queries, k=%d\n\n", res.Queries, res.K)
	fmt.Printf("%-22s %8s %8s %8s %9s %9s %8s\n", "config", "recall", "ndcg", "mrr", "p50 ms", "p99 ms", "tokens")
	for _, r := range res.Results {
		if r.Error != "" {
			fmt.Printf("%-22s error: %s\n", r.Config, r.Error)
			continue
		}
		fmt.Printf("%-22s %8.3f %8.3f %8.3f %9.1f %9.1f %8d\n", r.Config, r.Recall, r.NDCG, r.MRR, r.P50Ms, r.P99Ms, r.Tokens)
	}
}

// checkThresholds fails when any configuration errs or scores under one.
func checkThresholds(res evalResult, thresholds map[string]float64) error {
	if len(thresholds) == 0 {
		return nil
	}
	var failed []string
	for _, r := range res.Results {
		if r.Error != "" {
			failed = append(failed, r.Config+": "+r.Error)
			continue
		}
		got := map[string]float64{"recall": r.Recall, "ndcg": r.NDCG, "mrr": r.MRR}
		for metric, floor := range thresholds {
			if got[metric] < floor {
				failed = append(failed, fmt.Sprintf("%s: %s %.3f < %.3f", r.Config, metric, got[metric], floor))
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("below threshold:\n  %s", strings.Join(failed, "\n  "))
	}
	return nil
}
