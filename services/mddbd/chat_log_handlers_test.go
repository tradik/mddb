package main

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

// The admin review API (#406) through the registered routes.

// chatLogSeed stores three logged turns: a signed user's thumbs-down that
// found nothing, an anonymous answered turn, and one in another scenario.
func chatLogSeed(t *testing.T, s *Server) (signed, anon *ChatConversation) {
	t.Helper()
	signed = chatLogged(t, s, "user-1", "support", false, chatTurnMsgs("=SUM(A1) where?", "Nothing found.", ChatOutcomeNoResults, 1000,
		ChatToolCall{Name: "search_docs", Collection: "docs", Query: "where", DurationMs: 5})...)
	if _, err := s.chatFeedback(t.Context(), signed.ID, ChatFeedbackRequest{Sub: "user-1", MessageID: signed.Messages[1].ID,
		Rating: ChatRatingDown, Comment: "Useless, \"really\""}); err != nil {
		t.Fatal(err)
	}
	anon = chatLogged(t, s, "anon:sess-9", "support", true, chatTurnMsgs("How to back up?", "Use /v1/backup.", ChatOutcomeAnswered, 2000,
		ChatToolCall{Name: "search_docs", Query: "backup", ResultKeys: []string{"backup", "restore"}, TopScore: 0.82})...)
	chatLogged(t, s, "user-2", "sales", false, chatTurnMsgs("price?", "Free.", ChatOutcomeAnswered, 3000,
		ChatToolCall{Name: "lookup", Server: "crm", Query: "price", ResultKeys: []string{"p1"}, TopScore: 0.95})...)
	return signed, anon
}

func TestChatLogREST(t *testing.T) {
	s := chatServer(t)
	signed, anon := chatLogSeed(t, s)

	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/log", "")
	var page ChatLogPage
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Turns) != 3 || page.Turns[0].Scenario != "sales" {
		t.Fatalf("log: %d %s", rec.Code, rec.Body)
	}
	rec = chatREST(t, s, root, http.MethodGet, "/v1/chat/log?scenario=support&rating=down&outcome=noResults&from=1970-01-01&to=1970-01-01", "")
	page = ChatLogPage{}
	if json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Turns) != 1 {
		t.Fatalf("filtered: %d %s", rec.Code, rec.Body)
	}
	got := page.Turns[0]
	if got.ConversationID != signed.ID || got.Sub != "user-1" || got.DisplayName != "Name of user-1" || got.Comment != `Useless, "really"` ||
		got.ToolCalls[0].Query != "where" {
		t.Errorf("signed turn: %+v", got)
	}
	rec = chatREST(t, s, root, http.MethodGet, "/v1/chat/log?scenario=support&outcome=answered&from=1500&to="+time.Unix(2500, 0).UTC().Format(time.RFC3339), "")
	page = ChatLogPage{}
	if json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Turns) != 1 || page.Turns[0].ConversationID != anon.ID ||
		!page.Turns[0].Anonymous || page.Turns[0].Sub != "" || strings.Contains(rec.Body.String(), "sess-9") {
		t.Fatalf("anonymous: %s", rec.Body)
	}
	for _, q := range []string{"from=yesterday", "to=nope", "rating=meh", "limit=1&cursor=!!"} {
		if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/log?"+q, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/log", "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestChatLogExportREST(t *testing.T) {
	s := chatServer(t)
	chatLogSeed(t, s)
	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/log/export?format=csv&scenario=support", "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), ".csv") {
		t.Fatalf("csv: %d %v", rec.Code, rec.Header())
	}
	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil || len(rows) != 3 || strings.Join(rows[0], ",") != strings.Join(chatLogCSVHeader, ",") {
		t.Fatalf("rows: %v %v", rows, err)
	}
	signed := rows[2]
	if signed[7] != "'=SUM(A1) where?" || signed[10] != "down" || signed[11] != `Useless, "really"` || signed[4] != "user-1" ||
		signed[17] != "search_docs(where) → " || signed[15] != "10" {
		t.Errorf("signed row: %q", signed)
	}
	if anon := rows[1]; anon[4] != "" || anon[6] != "true" || anon[18] != "0.82" {
		t.Errorf("anonymous row: %q", anon)
	}
	rec = chatREST(t, s, root, http.MethodGet, "/v1/chat/log/export?format=csv&scenario=sales", "")
	rows, _ = csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if len(rows) != 2 || rows[1][17] != "crm/lookup(price) → p1" {
		t.Errorf("an external MCP tool names its server: %q", rows)
	}

	rec = chatREST(t, s, root, http.MethodGet, "/v1/chat/log/export?rating=down", "")
	var turns []ChatLogTurn
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
		json.Unmarshal(rec.Body.Bytes(), &turns) != nil || len(turns) != 1 {
		t.Fatalf("json: %d %s", rec.Code, rec.Body)
	}
	rec = chatREST(t, s, root, http.MethodGet, "/v1/chat/log/export?format=json&scenario=none", "")
	if rec.Body.String() != "[]\n" {
		t.Errorf("empty json: %q", rec.Body)
	}
	for _, q := range []string{"format=xml", "rating=x"} {
		if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/log/export?"+q, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

func TestChatGapsREST(t *testing.T) {
	s := chatServer(t)
	chatLogSeed(t, s)
	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/gaps?threshold=0.9&minCount=1", "")
	var res ChatGapsResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil || len(res.Gaps) != 2 || res.Threshold != 0.9 {
		t.Fatalf("gaps: %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"threshold=abc", "from=x", "minCount=-2"} {
		if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/gaps?"+q, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/gaps", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d", rec.Code)
	}
}

// The log holds every user's turns: global admins only, and nobody
// anonymous.
func TestChatLogPermissions(t *testing.T) {
	s := chatServer(t)
	for _, target := range []string{"/v1/chat/log", "/v1/chat/log/export?format=csv", "/v1/chat/gaps"} {
		for _, who := range []*JWTClaims{alice, tina} {
			if rec := chatREST(t, s, who, http.MethodGet, target, ""); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s: %d", who.Username, target, rec.Code)
			}
		}
		mux := http.NewServeMux()
		s.registerRoutes(mux, true)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s: %d", target, rec.Code)
		}
	}
}

func TestCSVSafeCell(t *testing.T) {
	for in, want := range map[string]string{"": "", "plain": "plain", "=1+1": "'=1+1", "+x": "'+x", "-x": "'-x", "@x": "'@x", "\tx": "'\tx"} {
		if got := csvSafeCell(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// A store that cannot be read fails the log, the export and the gaps.
func TestChatLogClosedStore(t *testing.T) {
	s := chatServer(t)
	chatLogSeed(t, s)
	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/log/export", "")
	var turns []ChatLogTurn
	if json.Unmarshal(rec.Body.Bytes(), &turns) != nil || len(turns) != 3 {
		t.Fatalf("every turn: %s", rec.Body)
	}
	// Both storage layouts: the conversations may live in their own file.
	closeStorage(t, s, chatConversationsCollection)
	for _, target := range []string{"/v1/chat/log", "/v1/chat/log/export", "/v1/chat/gaps"} {
		if rec := chatREST(t, s, root, http.MethodGet, target, ""); rec.Code == http.StatusOK {
			t.Errorf("%s on a closed store: %d", target, rec.Code)
		}
	}
}
