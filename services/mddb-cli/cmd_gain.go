package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// mddb-cli gain (AGT-004): what retrieval saved its callers.

// gainReport mirrors the server's /v1/gain answer.
type gainReport struct {
	WindowDays int `json:"windowDays"`
	Totals     struct {
		Searches       map[string]int64 `json:"searches"`
		Algorithms     map[string]int64 `json:"algorithms"`
		Fusion         map[string]int64 `json:"fusion"`
		TokensReturned int64            `json:"tokensReturned"`
		TokensAvoided  int64            `json:"tokensAvoided"`
		FragmentHits   int64            `json:"fragmentHits"`
		DocumentHits   int64            `json:"documentHits"`
	} `json:"totals"`
	TotalSearches          int64   `json:"totalSearches"`
	TokensWithoutRetrieval int64   `json:"tokensWithoutRetrieval"`
	SavedPercent           float64 `json:"savedPercent"`
}

func newGainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gain",
		Short: "Show tokens returned and saved by retrieval",
		Long: `Reports, for the whole server over a window of days, how many searches ran
(by type and algorithm), how many tokens they returned, and how many tokens
passages and chunks saved compared with returning their whole documents.
Token counts are estimates (4 bytes per token). See docs/TELEMETRY.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			window, _ := cmd.Flags().GetString("window")
			raw, err := newClient().Gain(context.Background(), window, false)
			if err != nil {
				return err
			}
			if outputJSON {
				fmt.Println(string(raw))
				return nil
			}
			var r gainReport
			if err := json.Unmarshal(raw, &r); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			printGain(r)
			return nil
		},
	}
	cmd.Flags().String("window", "30d", "Days to report, e.g. 7d (max 365d)")
	return cmd
}

func printGain(r gainReport) {
	t := r.Totals
	fmt.Printf("Last %d days\n", r.WindowDays)
	fmt.Printf("  Searches            %d   (%s)\n", r.TotalSearches, shares(t.Searches, false))
	fmt.Printf("  Passages returned   %d\n", t.FragmentHits)
	fmt.Printf("  Documents returned  %d\n", t.DocumentHits)
	fmt.Println("Estimated context")
	fmt.Printf("  without retrieval   %s tokens\n", compact(r.TokensWithoutRetrieval))
	fmt.Printf("  with retrieval      %s tokens\n", compact(t.TokensReturned))
	fmt.Printf("  saved               %s  (%.1f%%)\n", compact(t.TokensAvoided), r.SavedPercent)
	if len(t.Algorithms) > 0 {
		line := "Algorithms   " + shares(t.Algorithms, true)
		if len(t.Fusion) > 0 {
			line += "   Fusion " + shares(t.Fusion, true)
		}
		fmt.Println(line)
	}
}

// shares renders counts largest first, as counts or as percentages.
func shares(counts map[string]int64, percent bool) string {
	type kv struct {
		k string
		v int64
	}
	var list []kv
	var total int64
	for k, v := range counts {
		list = append(list, kv{k, v})
		total += v
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	parts := make([]string, 0, len(list))
	for _, e := range list {
		if percent {
			parts = append(parts, fmt.Sprintf("%s %d%%", e.k, 100*e.v/total))
		} else {
			parts = append(parts, fmt.Sprintf("%s %d", e.k, e.v))
		}
	}
	return strings.Join(parts, " · ")
}

// compact renders 160700000 as 160.7M.
func compact(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fG", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
