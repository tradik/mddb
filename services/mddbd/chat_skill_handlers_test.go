package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	json "mddb/internal/jsonx"
)

// #404: the REST surface of chat skills, through the registered routes and
// the access gate.

const (
	skillShaOne = "1111111111111111111111111111111111111111"
	skillShaTwo = "2222222222222222222222222222222222222222"
	toneSkillMD = "---\nname: tone\ndescription: Friendly answers\n---\nBe kind."
)

// skillSource is the TLS server skills are fetched from in a test: GitHub's
// API under /api, its raw host under /raw, and a few plain pages.
type skillSource struct {
	*httptest.Server
	head     atomic.Value // the commit "main" is at
	apiCalls atomic.Int32
	files    map[string]string // by request path
}

func newSkillSource(t *testing.T) *skillSource {
	t.Helper()
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	src := &skillSource{files: map[string]string{
		"/raw/acme/skills/" + skillShaOne + "/tone/SKILL.md": toneSkillMD,
		"/raw/acme/skills/" + skillShaTwo + "/tone/SKILL.md": "---\nname: tone\n---\nBe kind and brief.",
		"/raw/acme/skills/feature/x/tone/SKILL.md":           "Branch text.",
		"/plain/SKILL.md": "Plain instructions.",
		"/binary":         "\xff\xfe",
		"/empty":          "  \n",
		"/big":            strings.Repeat("x", chatSkillMaxBytesDefault+chatSkillFrontMatterAllowance+1),
		"/long":           strings.Repeat("y", chatSkillMaxBytesDefault+1),
	}}
	src.head.Store(skillShaOne)
	src.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/repos/acme/skills/commits/main":
			src.apiCalls.Add(1)
			if r.Header.Get("Accept") != "application/vnd.github.sha" || !strings.HasPrefix(r.Header.Get("User-Agent"), "mddbd/") {
				http.Error(w, "bad headers", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(src.head.Load().(string) + "\n"))
		case "/api/repos/acme/skills/commits/odd":
			_, _ = w.Write([]byte("not a commit"))
		case "/redirect":
			http.Redirect(w, r, "http://example.com/SKILL.md", http.StatusFound)
		default:
			body, found := src.files[r.URL.Path]
			if !found {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(src.Close)
	prevClient, prevAPI, prevRaw := chatSkillFetchClient, chatSkillGitHubAPI, chatSkillGitHubRaw
	chatSkillFetchClient = func() *http.Client {
		c := src.Client()
		c.CheckRedirect = httpsOnlyRedirect(func(*http.Request, []*http.Request) error { return nil })
		return c
	}
	chatSkillGitHubAPI, chatSkillGitHubRaw = src.URL+"/api", src.URL+"/raw"
	t.Cleanup(func() { chatSkillFetchClient, chatSkillGitHubAPI, chatSkillGitHubRaw = prevClient, prevAPI, prevRaw })
	return src
}

// skillJSON decodes a response body into T.
func skillJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v in %s", err, rec.Body.String())
	}
	return out
}

func TestChatSkillRESTLifecycle(t *testing.T) {
	s := chatServer(t)
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/skills", ""); rec.Body.String() != `{"skills":[],"version":0}` {
		t.Fatalf("empty list: %s", rec.Body.String())
	}
	whole, _ := json.Marshal(map[string]string{"content": toneSkillMD})
	rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/tone", string(whole))
	got := skillJSON[chatSkillView](t, rec)
	if rec.Code != http.StatusCreated || got.Name != "tone" || got.Description != "Friendly answers" || got.Content != "Be kind." ||
		got.Version != 1 || got.Bytes != 8 || got.UsedBy == nil || len(got.UsedBy) != 0 || got.CreatedAt.IsZero() || got.Source != nil {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"usedBy":[]`) || !strings.Contains(rec.Body.String(), `"updatedAt"`) {
		t.Errorf("the body lacks usedBy or updatedAt: %s", rec.Body.String())
	}
	// The same text again changes nothing; a new text is version 2.
	rec = chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/tone", `{"description":"Friendly answers","content":"Be kind."}`)
	if again := skillJSON[chatSkillView](t, rec); rec.Code != http.StatusOK || again.Version != 1 {
		t.Fatalf("unchanged put: %d %s", rec.Code, rec.Body.String())
	}
	rec = chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/tone", `{"description":"Warm","content":"Be kind and warm."}`)
	next := skillJSON[chatSkillView](t, rec)
	if rec.Code != http.StatusOK || next.Version != 2 || len(next.History) != 1 || next.History[0].Version != 1 ||
		next.History[0].Content != "" || next.History[0].Bytes != 8 || next.History[0].Description != "Friendly answers" {
		t.Fatalf("version 2: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/version", ""); rec.Body.String() != `{"version":2}` {
		t.Errorf("the scenarios' version after two skill changes: %s", rec.Body.String())
	}
	old := skillJSON[chatSkillView](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/skills/tone?version=1", ""))
	cur := skillJSON[chatSkillView](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/skills/tone", ""))
	if old.Content != "Be kind." || old.Version != 1 || old.History != nil || cur.Content != "Be kind and warm." || len(cur.History) != 1 {
		t.Errorf("versions: old %+v\ncurrent %+v", old, cur)
	}
	for target, want := range map[string]int{"/v1/chat/skills/tone?version=9": 404, "/v1/chat/skills/tone?version=x": 400,
		"/v1/chat/skills/tone?version=0": 400, "/v1/chat/skills/ghost": 404, "/v1/chat/skills/ghost/used-by": 404, "/v1/chat/skills/import": 404} {
		if rec := chatREST(t, s, root, http.MethodGet, target, ""); rec.Code != want {
			t.Errorf("GET %s: %d, want %d", target, rec.Code, want)
		}
	}
	list := skillJSON[chatSkillList](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/skills", ""))
	if len(list.Skills) != 1 || list.Skills[0].Content != "" || list.Skills[0].History != nil || list.Skills[0].Bytes != 17 || list.Version != 2 {
		t.Errorf("list: %+v", list)
	}
	for body, why := range map[string]string{`{`: "bad json", `{"content":""}`: "no content",
		`{"content":"x","source":{"url":"http://example.com/s"}}`: "an http source"} {
		if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/other", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", why, rec.Code)
		}
	}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/import", `{"content":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a skill named import: %d", rec.Code)
	}

	// A scenario attaches the skill: its prompt carries the text after the
	// guardrails, the skill says who uses it, and it cannot be deleted.
	body := `{"operatorPrompt":"You are Ada.","guardrails":"No legal advice.","skills":["tone","ghost"]}`
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", body); rec.Code != http.StatusCreated {
		t.Fatalf("scenario: %d %s", rec.Code, rec.Body.String())
	}
	prompt := skillJSON[chatScenarioPrompt](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/support/prompt", ""))
	guard, skill, style := strings.Index(prompt.Prompt, "No legal advice."), strings.Index(prompt.Prompt, "Skill tone — Warm:\nBe kind and warm."),
		strings.Index(prompt.Prompt, "Do not use emoji.")
	if guard < 0 || skill < guard || style < skill || !strings.Contains(prompt.Prompt, "take precedence") ||
		len(prompt.MissingSkills) != 1 || prompt.MissingSkills[0] != "ghost" || prompt.Bytes != len(prompt.Prompt) ||
		prompt.ApproxTokens != prompt.Bytes/4 || prompt.Warning != "" {
		t.Errorf("prompt: %+v", prompt)
	}
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/skills/tone/used-by", ""); rec.Body.String() != `{"name":"tone","scenarios":["support"]}` {
		t.Errorf("used by: %s", rec.Body.String())
	}
	rec = chatREST(t, s, root, http.MethodDelete, "/v1/chat/skills/tone", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"usedBy":["support"]`) ||
		!strings.Contains(rec.Body.String(), "in use by: support") {
		t.Errorf("delete in use: %d %s", rec.Code, rec.Body.String())
	}
	chatREST(t, s, root, http.MethodDelete, "/v1/chat/scenarios/support", "")
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/skills/tone", ""); rec.Body.String() != `{"deleted":true,"version":5}` {
		t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/skills/tone", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", rec.Code)
	}
	for method, target := range map[string]string{http.MethodPost: "/v1/chat/skills", http.MethodPut: "/v1/chat/skills/a/used-by",
		http.MethodDelete: "/v1/chat/skills", http.MethodPatch: "/v1/chat/skills/a", http.MethodGet: "/v1/chat/skills/a/update"} {
		if rec := chatREST(t, s, root, method, target, "{}"); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", method, target, rec.Code)
		}
	}
}

// Everything is for a global administrator, reads included.
func TestChatSkillPermissions(t *testing.T) {
	s := chatServer(t)
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/tone", `{"content":"Be kind."}`); rec.Code != http.StatusCreated {
		t.Fatalf("put: %d", rec.Code)
	}
	calls := map[string]string{"/v1/chat/skills": http.MethodGet, "/v1/chat/skills/tone": http.MethodGet,
		"/v1/chat/skills/tone/used-by": http.MethodGet, "/v1/chat/skills/import": http.MethodPost, "/v1/chat/skills/tone/update": http.MethodPost}
	for _, who := range []*JWTClaims{alice, tina} {
		for target, method := range calls {
			if rec := chatREST(t, s, who, method, target, `{"content":"x"}`); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d", who.Username, method, target, rec.Code)
			}
		}
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			if rec := chatREST(t, s, who, method, "/v1/chat/skills/tone", `{"content":"x"}`); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s: %d", who.Username, method, rec.Code)
			}
		}
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chat/skills", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list: %d", rec.Code)
	}
	// A read-only server stores nothing and still serves the reads.
	s.Mode = ModeRead
	for target, method := range map[string]string{"/v1/chat/skills/tone": http.MethodPut, "/v1/chat/skills/tone/update": http.MethodPost} {
		if rec := chatREST(t, s, root, method, target, `{"content":"x"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s on a read-only server: %d", method, rec.Code)
		}
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/skills/tone", ""); rec.Code != http.StatusForbidden {
		t.Errorf("DELETE on a read-only server: %d", rec.Code)
	}
	if rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/skills/tone", ""); rec.Code != http.StatusOK {
		t.Errorf("GET on a read-only server: %d", rec.Code)
	}
}

func TestChatSkillsWithoutAStore(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if _, _, err := s.PutChatSkill(&ChatSkill{Name: "s", Content: "x"}); err != errChatScenariosUnavailable {
		t.Errorf("put: %v", err)
	}
	if _, err := s.DeleteChatSkill("s"); err != errChatScenariosUnavailable {
		t.Errorf("delete: %v", err)
	}
	if _, err := s.GetChatSkill("s", 0); err != errChatScenariosUnavailable {
		t.Errorf("get: %v", err)
	}
	if _, err := s.ChatSkillUsedBy("s"); err != errChatScenariosUnavailable {
		t.Errorf("used by: %v", err)
	}
	if _, err := s.UpdateChatSkill(t.Context(), "s", false, ""); err != errChatScenariosUnavailable {
		t.Errorf("update: %v", err)
	}
	if len(s.ListChatSkills()) != 0 {
		t.Error("a server without a store lists skills")
	}
	// A preview needs no store.
	if p, err := s.ImportChatSkill(t.Context(), "", toneSkillMD); err != nil || p.Name != "tone" || p.Exists {
		t.Errorf("preview: %+v, %v", p, err)
	}
	rec := httptest.NewRecorder()
	s.handleChatSkills(rec, httptest.NewRequest(http.MethodGet, "/v1/chat/skills/s", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET without a store: %d", rec.Code)
	}
}

// A store that cannot write answers the put with its error.
func TestChatSkillPutStoreFailure(t *testing.T) {
	s := chatServer(t)
	m := NewChatScenarioManager(openChatDB(t, filepath.Join(t.TempDir(), "empty.db"))) // never loaded: no buckets
	s.ChatScenarios = m
	if _, _, err := s.PutChatSkill(&ChatSkill{Name: "tone", Content: "x"}); err == nil {
		t.Error("a put on a store without buckets succeeded")
	}
}

// A scenario whose composed prompt is over MDDB_CHAT_PROMPT_MAX_BYTES is
// refused, and one past three quarters of it is stored with a warning.
func TestChatScenarioPromptCap(t *testing.T) {
	s := chatServer(t)
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/long", `{"content":"`+strings.Repeat("x", 300)+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("skill: %d", rec.Code)
	}
	t.Setenv("MDDB_CHAT_PROMPT_MAX_BYTES", "400")
	rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/big", `{"operatorPrompt":"You are Ada.","skills":["long"]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "over the 400-byte limit (MDDB_CHAT_PROMPT_MAX_BYTES)") {
		t.Fatalf("over the cap: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/small", `{"operatorPrompt":"You are Ada."}`); rec.Code != http.StatusCreated {
		t.Fatalf("under the cap: %d %s", rec.Code, rec.Body.String())
	}
	t.Setenv("MDDB_CHAT_PROMPT_MAX_BYTES", "700")
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/big", `{"operatorPrompt":"You are Ada.","skills":["long"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("a larger cap: %d %s", rec.Code, rec.Body.String())
	}
	prompt := skillJSON[chatScenarioPrompt](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/big/prompt", ""))
	if !strings.Contains(prompt.Warning, "% of the 700-byte limit") || prompt.Bytes <= 525 {
		t.Errorf("warning: %+v", prompt)
	}
}
