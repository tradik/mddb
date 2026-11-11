package main

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Docs gaps (#406): what visitors ask that the documentation does not
// answer. A logged turn is a gap when its searches found nothing, or when
// the best score any of them reached is below a threshold. Gaps are grouped
// by the question normalised — lowercase, punctuation stripped, whitespace
// collapsed — so "How do I back up?" and "how do i back up" count together,
// most frequent first: what to write next.

// Defaults of the gaps view.
const (
	chatGapScoreEnv         = "MDDB_CHAT_GAP_SCORE"
	chatDefaultGapThreshold = 0.3
	chatDefaultGapLimit     = 100
	chatMaxGapLimit         = 1000
)

// ChatGapsFilter selects the turns the gaps are counted from.
type ChatGapsFilter struct {
	Scenario  string
	From      int64
	To        int64
	MinCount  int
	Threshold float64 // 0 is the server default
	Limit     int
}

// ChatGap is one question visitors asked that the docs did not answer.
type ChatGap struct {
	Question    string   `json:"question"`
	Example     string   `json:"example"`
	Count       int      `json:"count"`
	NoResults   int      `json:"noResults"`
	LastAskedAt int64    `json:"lastAskedAt"`
	Scenarios   []string `json:"scenarios"`
}

// ChatGapsResponse is the gaps, most frequent first, and the threshold used.
type ChatGapsResponse struct {
	Gaps      []ChatGap `json:"gaps"`
	Threshold float64   `json:"threshold"`
}

// chatGapThreshold resolves the threshold: the request's, else
// MDDB_CHAT_GAP_SCORE, else 0.3.
func chatGapThreshold(requested float64) float64 {
	if requested > 0 {
		return requested
	}
	if v, err := strconv.ParseFloat(env(chatGapScoreEnv, ""), 64); err == nil && v > 0 {
		return v
	}
	return chatDefaultGapThreshold
}

// normalizeChatQuestion is a question as gaps group it.
func normalizeChatQuestion(q string) string {
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, q)
	return strings.Join(strings.Fields(stripped), " ")
}

// chatGapOf reports whether a turn is a gap, and whether it found nothing.
func chatGapOf(t *ChatLogTurn, threshold float64) (gap, nothing bool) {
	switch t.Outcome {
	case ChatOutcomeNoResults:
		return true, true
	case ChatOutcomeAnswered:
	default:
		return false, false // an error or a refusal says nothing about the docs
	}
	searched, found, best := false, false, 0.0
	for _, tc := range t.ToolCalls {
		if tc.Query == "" {
			continue
		}
		searched = true
		if len(tc.ResultKeys) > 0 {
			found, best = true, max(best, tc.TopScore)
		}
	}
	switch {
	case !searched:
		return false, false
	case !found:
		return true, true
	}
	return best < threshold, false
}

// chatGaps groups the gap turns among turns (newest first).
func chatGaps(turns []ChatLogTurn, f ChatGapsFilter) ChatGapsResponse {
	threshold := chatGapThreshold(f.Threshold)
	byQuestion := map[string]*ChatGap{}
	for i := range turns {
		t := &turns[i]
		gap, nothing := chatGapOf(t, threshold)
		key := normalizeChatQuestion(t.Question)
		if !gap || key == "" {
			continue
		}
		g := byQuestion[key]
		if g == nil {
			// Turns arrive newest first: the first one is the latest asked.
			g = &ChatGap{Question: key, Example: t.Question, LastAskedAt: t.At}
			byQuestion[key] = g
		}
		g.Count++
		if nothing {
			g.NoResults++
		}
		if !containsString(g.Scenarios, t.Scenario) {
			g.Scenarios = append(g.Scenarios, t.Scenario)
		}
	}
	gaps := make([]ChatGap, 0, len(byQuestion))
	for _, g := range byQuestion {
		if g.Count >= max(f.MinCount, 1) {
			sort.Strings(g.Scenarios)
			gaps = append(gaps, *g)
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		a, b := gaps[i], gaps[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.LastAskedAt != b.LastAskedAt {
			return a.LastAskedAt > b.LastAskedAt
		}
		return a.Question < b.Question
	})
	limit := f.Limit
	if limit <= 0 {
		limit = chatDefaultGapLimit
	}
	return ChatGapsResponse{Gaps: gaps[:min(len(gaps), limit, chatMaxGapLimit)], Threshold: threshold}
}

// chatGapsOf reads the turns of the window and groups their gaps.
func (s *Server) chatGapsOf(ctx context.Context, f ChatGapsFilter) (ChatGapsResponse, error) {
	if f.MinCount < 0 || f.Threshold < 0 || f.Limit < 0 {
		return ChatGapsResponse{}, chatInvalid("minCount, threshold and limit may not be negative")
	}
	turns, err := s.chatLogMatching(ctx, ChatLogFilter{Scenario: f.Scenario, From: f.From, To: f.To})
	if err != nil {
		return ChatGapsResponse{}, err
	}
	return chatGaps(turns, f), nil
}
