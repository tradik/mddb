package main

import (
	"errors"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// The chat scenario API (#403):
//
//	GET    /v1/chat/scenarios                 list, with the store's version
//	GET    /v1/chat/scenarios/version         the version alone (cheap poll)
//	GET    /v1/chat/scenarios/{name}          one scenario
//	PUT    /v1/chat/scenarios/{name}          create (201) or replace (200)
//	DELETE /v1/chat/scenarios/{name}          remove
//	GET    /v1/chat/scenarios/{name}/prompt   the composed system prompt, its size and missing skills
//
// Writes need a global administrator. Reads need read permission on every
// collection the scenario allows; the list leaves out what the caller may
// not read.

const chatScenariosPath = "/v1/chat/scenarios"

// chatScenarioList is the body of the list.
type chatScenarioList struct {
	Scenarios []*ChatScenario `json:"scenarios"`
	Version   uint64          `json:"version"`
}

func (s *Server) handleChatScenarios(w http.ResponseWriter, r *http.Request) {
	name, verb, _ := strings.Cut(strings.Trim(strings.TrimPrefix(r.URL.Path, chatScenariosPath), "/"), "/")
	get := r.Method == http.MethodGet
	switch {
	case get && name == "":
		ok(w, chatScenarioList{Scenarios: s.ListChatScenarios(r.Context()), Version: s.ChatScenariosVersion()})
	case get && name == "version" && verb == "":
		ok(w, map[string]uint64{"version": s.ChatScenariosVersion()})
	case get && (verb == "" || verb == "prompt"):
		s.getChatScenario(w, r, name, verb == "prompt")
	case r.Method == http.MethodPut && name != "" && verb == "":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.putChatScenario(w, r, name) })(w, r)
	case r.Method == http.MethodDelete && name != "" && verb == "":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.deleteChatScenario(w, r, name) })(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// chatScenarioError answers a failed read or delete: 404, 401/403 or 400.
func chatScenarioError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errChatScenarioNotFound):
		http.Error(w, `{"error":"chat scenario not found"}`, http.StatusNotFound)
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrUnauthorized):
		writeAccessDenied(w, err)
	default:
		bad(w, err)
	}
}

func (s *Server) getChatScenario(w http.ResponseWriter, r *http.Request, name string, prompt bool) {
	sc, err := s.GetChatScenario(r.Context(), name)
	if err != nil {
		chatScenarioError(w, err)
		return
	}
	if prompt {
		ok(w, s.ChatScenarioPrompt(sc))
		return
	}
	ok(w, sc)
}

func (s *Server) putChatScenario(w http.ResponseWriter, r *http.Request, name string) {
	if !s.requireAdmin(w, r) {
		return
	}
	var sc ChatScenario
	if err := json.NewDecoder(r.Body).Decode(&sc); err != nil {
		bad(w, err)
		return
	}
	sc.Name = name // the path names the scenario
	created, err := s.PutChatScenario(r.Context(), &sc)
	if err != nil {
		bad(w, err)
		return
	}
	if !created {
		ok(w, &sc)
		return
	}
	b, _ := json.Marshal(&sc)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(b) // #nosec G705 -- response write to http.ResponseWriter
}

func (s *Server) deleteChatScenario(w http.ResponseWriter, r *http.Request, name string) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.DeleteChatScenario(name); err != nil {
		chatScenarioError(w, err)
		return
	}
	ok(w, map[string]any{"deleted": true, "version": s.ChatScenariosVersion()})
}
