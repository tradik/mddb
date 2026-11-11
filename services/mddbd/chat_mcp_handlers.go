package main

import (
	"errors"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// The external MCP server API of the chat (MCP-005):
//
//	GET    /v1/chat/mcp-secrets          the secrets: name, header, set, usedBy — never a value — and encryptionReady (admin)
//	PUT    /v1/chat/mcp-secrets/{name}   set or replace {value, header?}; 409 without MDDB_ENCRYPTION_KEY (admin)
//	DELETE /v1/chat/mcp-secrets/{name}   remove; 409 with usedBy while a scenario uses it (admin)
//	POST   /v1/chat/mcp/inspect          {url, secretRef?}: the server's tools and their annotations (admin)
//	POST   /v1/chat/mcp/call             {scenario, server, tool, arguments}: call an allowed tool (scenario readers)

const (
	chatMcpSecretsPath = "/v1/chat/mcp-secrets" // #nosec G101 -- a route, not a credential
	chatMcpInspectPath = "/v1/chat/mcp/inspect"
	chatMcpCallPath    = "/v1/chat/mcp/call"
)

// chatMcpSecretRequest is the body of a secret's PUT.
type chatMcpSecretRequest struct {
	Header string `json:"header"`
	Value  string `json:"value"`
}

// chatMcpInspectRequest is the body of an inspect.
type chatMcpInspectRequest struct {
	URL       string `json:"url"`
	SecretRef string `json:"secretRef"`
}

// chatMcpAnswer writes v, or the status err maps to.
func chatMcpAnswer(w http.ResponseWriter, v any, err error) {
	var off featureDisabledError
	switch {
	case err == nil:
		ok(w, v)
	case errors.As(err, &off):
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": "feature disabled", "feature": off.feature})
	case errors.Is(err, errChatMcpSecretNotFound), errors.Is(err, errChatScenarioNotFound), errors.Is(err, errChatMcpServerNotFound):
		writeJSONError(w, http.StatusNotFound, map[string]any{"error": err.Error()})
	case errors.Is(err, errChatMcpNotAllowed):
		writeJSONError(w, http.StatusForbidden, map[string]any{"error": err.Error()})
	case errors.Is(err, errChatMcpSecretsNeedKey), errors.Is(err, errChatMcpSecretUnsealed):
		// The server's configuration is in the way, as a feature locked by
		// the environment is (409 with lockedBy): requires names the fix.
		writeJSONError(w, http.StatusConflict, map[string]any{"error": err.Error(), "requires": "MDDB_ENCRYPTION_KEY"})
	case errors.Is(err, errChatMcpUnreachable):
		writeJSONError(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrUnauthorized):
		writeAccessDenied(w, err)
	default:
		bad(w, err)
	}
}

func (s *Server) handleChatMcpSecrets(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, chatMcpSecretsPath), "/")
	switch {
	case r.Method == http.MethodGet && name == "":
		ok(w, s.ListChatMcpSecrets())
	case r.Method == http.MethodPut && name != "":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.putChatMcpSecret(w, r, name) })(w, r)
	case r.Method == http.MethodDelete && name != "":
		s.guardWrite(func(w http.ResponseWriter, _ *http.Request) { s.deleteChatMcpSecret(w, name) })(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) putChatMcpSecret(w http.ResponseWriter, r *http.Request, name string) {
	var req chatMcpSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, errors.New("the body must be {\"value\": ..., \"header\": ...}")) // the decoder's error may quote the value
		return
	}
	created, err := s.PutChatMcpSecret(name, req.Header, req.Value)
	if err != nil {
		chatMcpAnswer(w, nil, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSONError(w, status, map[string]any{"name": name, "header": req.Header, "set": true, "version": s.ChatScenariosVersion()})
}

func (s *Server) deleteChatMcpSecret(w http.ResponseWriter, name string) {
	usedBy, err := s.DeleteChatMcpSecret(name)
	if errors.Is(err, errChatMcpSecretInUse) {
		writeJSONError(w, http.StatusConflict, map[string]any{"error": chatMcpSecretInUseMessage(name, usedBy), "usedBy": usedBy})
		return
	}
	chatMcpAnswer(w, map[string]any{"deleted": true, "version": s.ChatScenariosVersion()}, err)
}

// chatMcpSecretInUseMessage names the scenarios that keep a secret from going.
func chatMcpSecretInUseMessage(name string, usedBy []string) string {
	return "mcp secret " + name + " is in use by: " + strings.Join(usedBy, ", ")
}

func (s *Server) handleChatMcpInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req chatMcpInspectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	out, err := s.InspectChatMcpServer(r.Context(), req.URL, req.SecretRef)
	chatMcpAnswer(w, out, err)
}

func (s *Server) handleChatMcpCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req chatMcpCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	out, err := s.CallChatMcpTool(r.Context(), &req)
	chatMcpAnswer(w, out, err)
}
