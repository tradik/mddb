package main

import (
	"encoding/csv"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// The log export (#406): every turn the filter selects, as CSV or JSON,
// written row by row and flushed as it goes. Identities are in it because
// the route is global admin only; an anonymous session has none.

// chatLogCSVHeader is the first row of a CSV export.
var chatLogCSVHeader = []string{"time", "conversationId", "messageId", "scenario", "sub", "displayName", "anonymous",
	"question", "answer", "outcome", "rating", "comment", "model", "provider", "latencyMs", "promptTokens",
	"completionTokens", "tools", "topScore"}

// exportChatLog writes the export in the format the query asks for.
func (s *Server) exportChatLog(w http.ResponseWriter, r *http.Request, f ChatLogFilter) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		chatFail(w, chatInvalid("format must be csv or json"))
		return
	}
	turns, err := s.chatLogExport(r.Context(), f)
	if err != nil {
		chatFail(w, err)
		return
	}
	name := "chat-log-" + time.Unix(chatNow(), 0).UTC().Format("20060102-150405") + "." + format
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		writeChatLogCSV(w, turns)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeChatLogJSON(w, turns)
}

// flushChat sends what is written so far, when the writer can.
func flushChat(w io.Writer) {
	if fl, isFlusher := w.(http.Flusher); isFlusher {
		fl.Flush()
	}
}

// writeChatLogJSON writes turns as one JSON array, a turn at a time.
func writeChatLogJSON(w io.Writer, turns []ChatLogTurn) {
	_, _ = io.WriteString(w, "[")
	for i, t := range turns {
		if i > 0 {
			_, _ = io.WriteString(w, ",")
		}
		b, _ := json.Marshal(t) // strings and numbers: it cannot fail
		_, _ = w.Write(b)
		flushChat(w)
	}
	_, _ = io.WriteString(w, "]\n")
}

// writeChatLogCSV writes turns as RFC 4180 CSV under chatLogCSVHeader.
func writeChatLogCSV(w io.Writer, turns []ChatLogTurn) {
	cw := csv.NewWriter(w)
	_ = cw.Write(chatLogCSVHeader)
	for _, t := range turns {
		_ = cw.Write(chatLogCSVRow(t))
		cw.Flush()
		flushChat(w)
	}
	cw.Flush()
}

// chatLogCSVRow is one turn as CSV cells.
func chatLogCSVRow(t ChatLogTurn) []string {
	var prompt, completion int64
	if t.Tokens != nil {
		prompt, completion = t.Tokens.Prompt, t.Tokens.Completion
	}
	tools := make([]string, 0, len(t.ToolCalls))
	best := 0.0
	for _, tc := range t.ToolCalls {
		tools = append(tools, tc.Name+"("+tc.Query+") → "+strings.Join(tc.ResultKeys, " "))
		best = max(best, tc.TopScore)
	}
	row := []string{time.Unix(t.At, 0).UTC().Format(time.RFC3339), t.ConversationID, t.MessageID, t.Scenario, t.Sub,
		t.DisplayName, strconv.FormatBool(t.Anonymous), t.Question, t.Answer, t.Outcome, t.Rating, t.Comment, t.Model,
		t.Provider, strconv.FormatInt(t.LatencyMs, 10), strconv.FormatInt(prompt, 10), strconv.FormatInt(completion, 10),
		strings.Join(tools, "; "), strconv.FormatFloat(best, 'f', -1, 64)}
	for i, cell := range row {
		row[i] = csvSafeCell(cell)
	}
	return row
}

// csvSafeCell keeps a spreadsheet from running a cell as a formula (CSV
// injection): text from a visitor that starts with = + - @ or a tab is
// prefixed with an apostrophe.
func csvSafeCell(cell string) string {
	if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
		return "'" + cell
	}
	return cell
}
