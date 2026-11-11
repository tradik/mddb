package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The conversation log review API (#406), global admin only (the gate,
// access_rules_http.go):
//
//	GET /v1/chat/log?scenario=&from=&to=&rating=&outcome=&cursor=&limit=
//	GET /v1/chat/log/export?format=csv|json&scenario=&from=&to=&rating=&outcome=
//	GET /v1/chat/gaps?scenario=&from=&to=&minCount=&threshold=&limit=
//
// from and to are unix seconds, an RFC 3339 time or a date (YYYY-MM-DD; a
// date as "to" means the end of that day, UTC).
const (
	chatLogPath       = "/v1/chat/log"
	chatLogExportPath = "/v1/chat/log/export"
	chatGapsPath      = "/v1/chat/gaps"
)

// chatTimeParam reads a time bound from the query string.
func chatTimeParam(r *http.Request, name string, end bool) (int64, error) {
	return parseChatTime(name, r.URL.Query().Get(name), end)
}

// parseChatTime reads a time bound: unix seconds, RFC 3339 or a date; end
// makes a bare date its last second. "" is no bound.
func parseChatTime(name, v string, end bool) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.Unix(), nil
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return 0, chatInvalid("%s must be unix seconds, RFC 3339 or YYYY-MM-DD", name)
	}
	if end {
		return t.Unix() + secondsPerDay - 1, nil
	}
	return t.Unix(), nil
}

// chatLogFilterOf reads the log filter from the query string.
func chatLogFilterOf(r *http.Request) (ChatLogFilter, error) {
	from, err := chatTimeParam(r, "from", false)
	if err != nil {
		return ChatLogFilter{}, err
	}
	to, err := chatTimeParam(r, "to", true)
	if err != nil {
		return ChatLogFilter{}, err
	}
	q := r.URL.Query()
	return ChatLogFilter{Scenario: q.Get("scenario"), From: from, To: to, Rating: q.Get("rating"),
		Outcome: q.Get("outcome"), Cursor: q.Get("cursor"), Limit: queryInt(r, "limit")}, nil
}

// handleChatLog answers GET /v1/chat/log and /v1/chat/log/export.
func (s *Server) handleChatLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	f, err := chatLogFilterOf(r)
	if err != nil {
		chatFail(w, err)
		return
	}
	if r.URL.Path == chatLogExportPath {
		s.exportChatLog(w, r, f)
		return
	}
	page, err := s.chatLog(r.Context(), f)
	chatReply(w, page, err)
}

// handleChatGaps answers GET /v1/chat/gaps.
func (s *Server) handleChatGaps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	f, err := chatLogFilterOf(r)
	if err != nil {
		chatFail(w, err)
		return
	}
	threshold, err := strconv.ParseFloat(r.URL.Query().Get("threshold"), 64)
	if err != nil && r.URL.Query().Get("threshold") != "" {
		chatFail(w, chatInvalid("threshold must be a number"))
		return
	}
	res, err := s.chatGapsOf(r.Context(), ChatGapsFilter{Scenario: f.Scenario, From: f.From, To: f.To,
		MinCount: queryInt(r, "minCount"), Threshold: threshold, Limit: f.Limit})
	chatReply(w, res, err)
}
