package main

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

// The chat conversation host API (#405). The caller is the host application
// (or mddb-chat on its behalf), never a browser:
//
//	POST   /v1/chat/conversations                 {sub, scenario, displayName?, retentionDays?}
//	GET    /v1/chat/conversations?sub=&scenario=&limit=&cursor=
//	DELETE /v1/chat/conversations?sub=            everything of a user
//	GET    /v1/chat/conversations/export?sub=     everything of a user
//	GET    /v1/chat/conversations/{id}?sub=&limit=
//	POST   /v1/chat/conversations/{id}/messages   {sub, messages: [...]}
//	POST   /v1/chat/conversations/{id}/end        {sub}
//	DELETE /v1/chat/conversations/{id}?sub=
//
// sub is in the query string of a GET or DELETE and in the body of a POST.
// A conversation of another sub answers 404, as a missing one does.
//
// Permission is on the chat collection of the caller's tenant
// (chatCollection): read for GET, write for the rest. A key granted only
// that collection is a chat host and nothing else.
const chatConversationsPath = "/v1/chat/conversations"

// chatRule is what a chat operation needs on the chat collection.
func chatRule(write bool) accessRule {
	if write {
		return ruleWrite
	}
	return ruleRead
}

// handleChatConversations routes the host API.
func (s *Server) handleChatConversations(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, chatConversationsPath), "/")
	id, action, _ := strings.Cut(rest, "/")
	var h http.HandlerFunc
	switch {
	case rest == "" && r.Method == http.MethodGet:
		h = s.chatListHandler
	case rest == "" && r.Method == http.MethodPost:
		h = s.chatCreateHandler
	case rest == "" && r.Method == http.MethodDelete:
		h = s.chatDeleteForUserHandler
	case rest == "export" && r.Method == http.MethodGet:
		h = s.chatExportHandler
	case action == "" && r.Method == http.MethodGet:
		h = func(w http.ResponseWriter, r *http.Request) { s.chatGetHandler(w, r, id) }
	case action == "" && r.Method == http.MethodDelete:
		h = func(w http.ResponseWriter, r *http.Request) { s.chatDeleteHandler(w, r, id) }
	case action == "messages" && r.Method == http.MethodPost:
		h = func(w http.ResponseWriter, r *http.Request) { s.chatAppendHandler(w, r, id) }
	case action == "end" && r.Method == http.MethodPost:
		h = func(w http.ResponseWriter, r *http.Request) { s.chatEndHandler(w, r, id) }
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	write := r.Method != http.MethodGet
	if !s.allowed(w, r, chatRule(write), chatCollection(r.Context())) {
		return
	}
	if write {
		h = s.guardWrite(h)
	}
	h(w, r)
}

// chatFail answers a chat error: 409 for an ended conversation, else as bad
// does (404 for not found, 400 for the rest).
func chatFail(w http.ResponseWriter, err error) {
	if errors.Is(err, errChatEnded) {
		http.Error(w, `{"error":"conversation has ended"}`, http.StatusConflict)
		return
	}
	bad(w, err)
}

// chatReply answers with v, or with the error.
func chatReply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		chatFail(w, err)
		return
	}
	ok(w, v)
}

// chatBody decodes a POST body; an empty body is an empty request.
func chatBody(r *http.Request, into any) error {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil && !errors.Is(err, io.EOF) {
		return chatInvalid("unreadable body: %v", err)
	}
	return nil
}

// queryInt reads an integer query parameter; absent or unreadable is 0.
func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func (s *Server) chatCreateHandler(w http.ResponseWriter, r *http.Request) {
	var req ChatCreateRequest
	if err := chatBody(r, &req); err != nil {
		chatFail(w, err)
		return
	}
	conv, err := s.chatCreate(r.Context(), req)
	chatReply(w, conv, err)
}

func (s *Server) chatListHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.chatList(r.Context(), q.Get("sub"), q.Get("scenario"), queryInt(r, "limit"), q.Get("cursor"))
	chatReply(w, resp, err)
}

func (s *Server) chatGetHandler(w http.ResponseWriter, r *http.Request, id string) {
	conv, err := s.chatGet(r.Context(), id, r.URL.Query().Get("sub"), queryInt(r, "limit"))
	chatReply(w, conv, err)
}

func (s *Server) chatAppendHandler(w http.ResponseWriter, r *http.Request, id string) {
	var req ChatAppendRequest
	if err := chatBody(r, &req); err != nil {
		chatFail(w, err)
		return
	}
	conv, err := s.chatAppend(r.Context(), id, req)
	chatReply(w, conv, err)
}

func (s *Server) chatEndHandler(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Sub string `json:"sub"`
	}
	if err := chatBody(r, &req); err != nil {
		chatFail(w, err)
		return
	}
	conv, err := s.chatEnd(r.Context(), id, req.Sub)
	chatReply(w, conv, err)
}

func (s *Server) chatDeleteHandler(w http.ResponseWriter, r *http.Request, id string) {
	err := s.chatDelete(r.Context(), id, r.URL.Query().Get("sub"))
	chatReply(w, map[string]any{"deleted": true, "id": id}, err)
}

func (s *Server) chatDeleteForUserHandler(w http.ResponseWriter, r *http.Request) {
	sub := r.URL.Query().Get("sub")
	n, err := s.chatDeleteForUser(r.Context(), sub)
	chatReply(w, map[string]any{"deleted": n, "sub": sub}, err)
}

func (s *Server) chatExportHandler(w http.ResponseWriter, r *http.Request) {
	export, err := s.chatExport(r.Context(), r.URL.Query().Get("sub"))
	chatReply(w, export, err)
}
