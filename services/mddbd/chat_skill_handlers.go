package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

// The chat skill API (#404), admin only, reads included:
//
//	GET    /v1/chat/skills                  list, without contents
//	POST   /v1/chat/skills/import           preview a SKILL.md from {url} or {content}; stores nothing
//	GET    /v1/chat/skills/{name}           one skill; ?version=N for a kept earlier version
//	PUT    /v1/chat/skills/{name}           create (201) or store a new version (200)
//	DELETE /v1/chat/skills/{name}           remove; 409 with usedBy while a scenario attaches it
//	GET    /v1/chat/skills/{name}/used-by   the scenarios attaching it
//	POST   /v1/chat/skills/{name}/update    fetch the source again: a preview, saved with confirm: true

const chatSkillsPath = "/v1/chat/skills"

// chatSkillList is the body of the list. Version is the scenario store's
// counter, which a skill change bumps too.
type chatSkillList struct {
	Skills  []*chatSkillView `json:"skills"`
	Version uint64           `json:"version"`
}

// chatSkillRequest is the body of the two POSTs.
type chatSkillRequest struct {
	URL     string `json:"url"`
	Content string `json:"content"`
	Confirm bool   `json:"confirm"`
	Sha256  string `json:"sha256"`
}

func (s *Server) handleChatSkills(w http.ResponseWriter, r *http.Request) {
	name, verb, _ := strings.Cut(strings.Trim(strings.TrimPrefix(r.URL.Path, chatSkillsPath), "/"), "/")
	switch {
	case r.Method == http.MethodGet && name == "":
		ok(w, chatSkillList{Skills: s.ListChatSkills(), Version: s.ChatScenariosVersion()})
	case r.Method == http.MethodPost && name == "import" && verb == "":
		s.importChatSkill(w, r)
	case r.Method == http.MethodGet && verb == "":
		s.getChatSkill(w, r, name)
	case r.Method == http.MethodGet && verb == "used-by":
		scenarios, err := s.ChatSkillUsedBy(name)
		chatSkillAnswer(w, map[string]any{"name": name, "scenarios": scenarios}, err)
	case r.Method == http.MethodPut && name != "" && verb == "":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.putChatSkill(w, r, name) })(w, r)
	case r.Method == http.MethodDelete && name != "" && verb == "":
		s.guardWrite(func(w http.ResponseWriter, _ *http.Request) { s.deleteChatSkill(w, name) })(w, r)
	case r.Method == http.MethodPost && verb == "update":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.updateChatSkill(w, r, name) })(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// chatSkillAnswer writes v, or the status err maps to: 404 for a missing
// skill or version, 409 for a source that changed since the preview, else 400.
func chatSkillAnswer(w http.ResponseWriter, v any, err error) {
	switch {
	case err == nil:
		ok(w, v)
	case errors.Is(err, errChatSkillNotFound):
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": err.Error()})
	case errors.Is(err, errChatSkillStale):
		writeJSONError(w, http.StatusConflict, map[string]any{"error": err.Error()})
	default:
		bad(w, err)
	}
}

// writeJSONError writes a JSON error body with its status.
func writeJSONError(w http.ResponseWriter, status int, body map[string]any) {
	b, _ := json.Marshal(body)
	w.WriteHeader(status)
	_, _ = w.Write(b) // #nosec G705 -- response write to http.ResponseWriter
}

func (s *Server) getChatSkill(w http.ResponseWriter, r *http.Request, name string) {
	version := 0
	if raw := r.URL.Query().Get("version"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			bad(w, errors.New("version must be a positive number"))
			return
		}
		version = n
	}
	sk, err := s.GetChatSkill(name, version)
	chatSkillAnswer(w, sk, err)
}

func (s *Server) importChatSkill(w http.ResponseWriter, r *http.Request) {
	var req chatSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	p, err := s.ImportChatSkill(r.Context(), req.URL, req.Content)
	chatSkillAnswer(w, p, err)
}

func (s *Server) putChatSkill(w http.ResponseWriter, r *http.Request, name string) {
	var sk ChatSkill
	if err := json.NewDecoder(r.Body).Decode(&sk); err != nil {
		bad(w, err)
		return
	}
	sk.Name = name // the path names the skill
	view, created, err := s.PutChatSkill(&sk)
	if err != nil || !created {
		chatSkillAnswer(w, view, err)
		return
	}
	b, _ := json.Marshal(view)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(b) // #nosec G705 -- response write to http.ResponseWriter
}

func (s *Server) deleteChatSkill(w http.ResponseWriter, name string) {
	usedBy, err := s.DeleteChatSkill(name)
	if errors.Is(err, errChatSkillInUse) {
		writeJSONError(w, http.StatusConflict, map[string]any{"error": chatSkillInUseMessage(name, usedBy), "usedBy": usedBy})
		return
	}
	chatSkillAnswer(w, map[string]any{"deleted": true, "version": s.ChatScenariosVersion()}, err)
}

// chatSkillInUseMessage names the scenarios that keep a skill from going.
func chatSkillInUseMessage(name string, usedBy []string) string {
	return "chat skill " + name + " is in use by: " + strings.Join(usedBy, ", ")
}

func (s *Server) updateChatSkill(w http.ResponseWriter, r *http.Request, name string) {
	var req chatSkillRequest
	// An empty body is a preview.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && r.ContentLength != 0 {
		bad(w, err)
		return
	}
	out, err := s.UpdateChatSkill(r.Context(), name, req.Confirm, req.Sha256)
	chatSkillAnswer(w, out, err)
}
