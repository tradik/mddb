package main

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The gain report (AGT-004): GET /v1/gain?window=30d, MCP get_gain and
// `mddb-cli gain`. Read-only; server-wide, because the counters keep no
// collection names.

const (
	defaultGainDays = 30
	maxGainDays     = 365
)

// GainDay is one day's counts.
type GainDay struct {
	Date string `json:"date"`
	GainCounters
}

// GainReport sums a window of days.
type GainReport struct {
	WindowDays int          `json:"windowDays"`
	From       string       `json:"from"`
	To         string       `json:"to"`
	Totals     GainCounters `json:"totals"`
	// TotalSearches is the sum of Totals.Searches.
	TotalSearches int64 `json:"totalSearches"`
	// TokensWithoutRetrieval is what the same hits would have cost as whole
	// documents: tokensReturned + tokensAvoided.
	TokensWithoutRetrieval int64 `json:"tokensWithoutRetrieval"`
	// SavedPercent is tokensAvoided as a share of TokensWithoutRetrieval.
	SavedPercent float64   `json:"savedPercent"`
	Daily        []GainDay `json:"daily,omitempty"`
}

// parseGainWindow reads "30d", "30" or "" (the default) as a day count.
func parseGainWindow(s string) (int, error) {
	if s == "" {
		return defaultGainDays, nil
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil || n < 1 || n > maxGainDays {
		return 0, fmt.Errorf("window must be 1d..%dd, got %q", maxGainDays, s)
	}
	return n, nil
}

// Report sums the last days, with a per-day breakdown when daily is set.
func (g *GainRecorder) Report(days int, daily bool) (*GainReport, error) {
	if g == nil {
		return nil, errors.New("gain counters are not running")
	}
	order, byDay, err := g.Days(days)
	if err != nil {
		return nil, err
	}
	r := &GainReport{WindowDays: days, From: order[0], To: order[len(order)-1]}
	for _, d := range order {
		c := byDay[d]
		r.Totals.add(c)
		if daily {
			r.Daily = append(r.Daily, GainDay{Date: d, GainCounters: c})
		}
	}
	for _, n := range r.Totals.Searches {
		r.TotalSearches += n
	}
	r.TokensWithoutRetrieval = r.Totals.TokensReturned + r.Totals.TokensAvoided
	if r.TokensWithoutRetrieval > 0 {
		r.SavedPercent = math.Round(1000*float64(r.Totals.TokensAvoided)/float64(r.TokensWithoutRetrieval)) / 10
	}
	return r, nil
}

// handleGain serves GET /v1/gain?window=30d&daily=true.
func (s *Server) handleGain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.permitted(w, r, "*", PermRead) {
		return
	}
	days, err := parseGainWindow(r.URL.Query().Get("window"))
	if err != nil {
		bad(w, err)
		return
	}
	rep, err := s.Gain.Report(days, r.URL.Query().Get("daily") == "true")
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, rep)
}

// Days returns stored plus pending counts for the last n days, oldest first.
func (g *GainRecorder) Days(n int) ([]string, map[string]GainCounters, error) {
	if n <= 0 {
		return nil, nil, errors.New("window must be at least one day")
	}
	today := g.now().UTC()
	days := make([]string, n)
	for i := range days {
		days[i] = today.AddDate(0, 0, i-n+1).Format(time.DateOnly)
	}
	out := make(map[string]GainCounters, n)
	err := g.db().View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucketStats); b != nil {
			for _, d := range days {
				out[d] = readGainDay(b, d)
			}
		}
		return nil
	})
	g.mu.Lock()
	for _, d := range days {
		if p := g.pending[d]; p != nil {
			c := out[d]
			c.add(cloneGain(*p))
			out[d] = c
		}
	}
	g.mu.Unlock()
	return days, out, err
}

func cloneGain(c GainCounters) GainCounters {
	c.Searches, c.Algorithms, c.Fusion = maps.Clone(c.Searches), maps.Clone(c.Algorithms), maps.Clone(c.Fusion)
	return c
}
