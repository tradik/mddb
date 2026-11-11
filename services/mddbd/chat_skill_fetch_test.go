package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// #404: importing a skill from a URL and updating it from its source —
// previews first, a pinned commit for GitHub, https only.

// The default client holds redirects to https, as the corpus pack client does.
func TestChatSkillFetchClientRefusesPlainRedirects(t *testing.T) {
	c := chatSkillFetchClient()
	plain, _ := http.NewRequest(http.MethodGet, "http://example.com/SKILL.md", nil)
	if err := c.CheckRedirect(plain, nil); err == nil || c.Timeout != chatSkillFetchTimeout {
		t.Errorf("a redirect to http was followed (timeout %v)", c.Timeout)
	}
}

func TestParseGitHubFileURL(t *testing.T) {
	for raw, want := range map[string]gitHubFile{
		"https://github.com/acme/skills/blob/main/tone/SKILL.md":                     {"acme", "skills", "main", "tone/SKILL.md", "main/tone/SKILL.md"},
		"https://github.com/acme/skills/raw/v1.2/SKILL.md":                           {"acme", "skills", "v1.2", "SKILL.md", "v1.2/SKILL.md"},
		"https://raw.githubusercontent.com/acme/skills/main/SKILL.md":                {"acme", "skills", "main", "SKILL.md", "main/SKILL.md"},
		"https://raw.githubusercontent.com/acme/skills/refs/heads/main/a/SKILL.md":   {"acme", "skills", "main", "a/SKILL.md", "refs/heads/main/a/SKILL.md"},
		"https://raw.githubusercontent.com/acme/skills/refs/tags/v1/SKILL.md":        {"acme", "skills", "v1", "SKILL.md", "refs/tags/v1/SKILL.md"},
		"https://raw.githubusercontent.com/acme/skills/refs/pull/SKILL.md":           {"acme", "skills", "refs", "pull/SKILL.md", "refs/pull/SKILL.md"},
		"https://github.com/acme/skills/blob/" + skillShaOne + "/SKILL.md":           {"acme", "skills", skillShaOne, "SKILL.md", skillShaOne + "/SKILL.md"},
		"https://github.com/acme/skills/tree/main/tone":                              {},
		"https://github.com/acme/skills":                                             {},
		"https://raw.githubusercontent.com/acme/skills/main":                         {},
		"https://gist.githubusercontent.com/acme/0123/raw/SKILL.md":                  {},
		"https://example.com/acme/skills/blob/main/SKILL.md":                         {},
		"https://github.com/acme/skills/blob/main":                                   {},
		"https://raw.githubusercontent.com/acme/skills/refs/heads/main/b/c/SKILL.md": {"acme", "skills", "main", "b/c/SKILL.md", "refs/heads/main/b/c/SKILL.md"},
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, isGitHub := parseGitHubFileURL(u)
		if got != want || isGitHub != (want.owner != "") {
			t.Errorf("%s: %+v %v, want %+v", raw, got, isGitHub, want)
		}
	}
	if isCommitSHA("main") || isCommitSHA(strings.Repeat("z", 40)) || isCommitSHA("abcd") || !isCommitSHA(skillShaOne) {
		t.Error("isCommitSHA")
	}
}

func TestChatSkillImportPreviews(t *testing.T) {
	src := newSkillSource(t)
	s := chatServer(t)
	post := func(body map[string]any) (int, chatSkillPreview, string) {
		raw, _ := json.Marshal(body)
		rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/skills/import", string(raw))
		var p chatSkillPreview
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return rec.Code, p, rec.Body.String()
	}

	// A GitHub blob URL: the branch is resolved to a commit and the file is
	// read at that commit. Nothing is stored.
	blob := "https://github.com/acme/skills/blob/main/tone/SKILL.md"
	code, p, body := post(map[string]any{"url": blob})
	if code != http.StatusOK || p.Name != "tone" || p.Description != "Friendly answers" || p.Content != "Be kind." || p.Raw != toneSkillMD ||
		p.Source == nil || p.Source.URL != blob || p.Source.Commit != skillShaOne || p.Source.FetchedAt.IsZero() ||
		p.Bytes != 8 || p.Sha256 != chatSkillHash("Be kind.") || p.Exists {
		t.Fatalf("blob import: %d %s", code, body)
	}
	if len(s.ListChatSkills()) != 0 || s.ChatScenariosVersion() != 0 {
		t.Fatal("an import preview stored something")
	}
	// A commit in the URL needs no lookup; a ref GitHub does not resolve is
	// fetched as written, without a commit.
	calls := src.apiCalls.Load()
	if code, p, body = post(map[string]any{"url": "https://raw.githubusercontent.com/acme/skills/" + skillShaTwo + "/tone/SKILL.md"}); code != http.StatusOK ||
		p.Source.Commit != skillShaTwo || p.Content != "Be kind and brief." || src.apiCalls.Load() != calls {
		t.Errorf("a pinned URL: %d %s", code, body)
	}
	if code, p, body = post(map[string]any{"url": "https://github.com/acme/skills/blob/feature/x/tone/SKILL.md"}); code != http.StatusOK ||
		p.Source.Commit != "" || p.Content != "Branch text." || p.Name != "" {
		t.Errorf("an unresolved ref: %d %s", code, body)
	}
	src.files["/raw/acme/skills/odd/SKILL.md"] = "Odd."
	if code, p, body = post(map[string]any{"url": "https://github.com/acme/skills/blob/odd/SKILL.md"}); code != http.StatusOK || p.Source.Commit != "" || p.Content != "Odd." {
		t.Errorf("an API answer that is no commit: %d %s", code, body)
	}
	// Any other https URL is fetched as it is.
	if code, p, body = post(map[string]any{"url": src.URL + "/plain/SKILL.md"}); code != http.StatusOK || p.Content != "Plain instructions." ||
		p.Source.Commit != "" || p.Source.URL != src.URL+"/plain/SKILL.md" {
		t.Errorf("a plain URL: %d %s", code, body)
	}
	// Pasted text is parsed the same way, without a source; exists says the
	// name is taken.
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/tone", `{"content":"x"}`); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	if code, p, body = post(map[string]any{"content": toneSkillMD}); code != http.StatusOK || p.Source != nil || !p.Exists || p.Name != "tone" {
		t.Errorf("pasted: %d %s", code, body)
	}

	for why, req := range map[string]map[string]any{
		"nothing":          {},
		"both":             {"url": blob, "content": "x"},
		"http":             {"url": "http://example.com/SKILL.md"},
		"no host":          {"url": "https:///SKILL.md"},
		"not a URL":        {"url": "https://exa mple.com/%zz"},
		"missing":          {"url": src.URL + "/ghost"},
		"binary":           {"url": src.URL + "/binary"},
		"too large":        {"url": src.URL + "/big"},
		"long content":     {"url": src.URL + "/long"},
		"leaves TLS":       {"url": src.URL + "/redirect"},
		"pasted too large": {"content": strings.Repeat("x", chatSkillMaxBytesDefault+1)},
	} {
		if code, _, body := post(req); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", why, code, body)
		}
	}
	if rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/skills/import", `{`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
}

func TestChatSkillUpdateFromSource(t *testing.T) {
	src := newSkillSource(t)
	s := chatServer(t)
	blob := "https://github.com/acme/skills/blob/main/tone/SKILL.md"
	raw, _ := json.Marshal(map[string]any{"description": "Friendly answers", "content": "Be kind.",
		"source": map[string]any{"url": blob, "commit": skillShaOne, "fetchedAt": "2026-11-11T10:00:00Z"}})
	rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/tone", string(raw))
	if stored := skillJSON[chatSkillView](t, rec); rec.Code != http.StatusCreated || stored.Source == nil || stored.Source.Commit != skillShaOne {
		t.Fatalf("put with a source: %d %s", rec.Code, rec.Body.String())
	}
	update := func(body string) (int, chatSkillUpdate, string) {
		rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/skills/tone/update", body)
		var out chatSkillUpdate
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec.Body.String()
	}

	// The source has not moved: nothing to save, even with confirm.
	for _, body := range []string{"", `{"confirm":true}`} {
		code, out, text := update(body)
		if code != http.StatusOK || out.Changed || out.Saved || out.Skill.Version != 1 || out.Preview.Content != "Be kind." || out.Version != 1 {
			t.Fatalf("unchanged source with %q: %d %s", body, code, text)
		}
	}
	// The branch moves: a preview shows the new text next to the stored one.
	src.head.Store(skillShaTwo)
	code, out, text := update(`{}`)
	if code != http.StatusOK || !out.Changed || out.Saved || out.Skill.Content != "Be kind." || out.Preview.Content != "Be kind and brief." ||
		out.Preview.Source.Commit != skillShaTwo || out.Preview.Name != "tone" || out.Preview.Description != "Friendly answers" || !out.Preview.Exists {
		t.Fatalf("preview: %d %s", code, text)
	}
	if cur, _ := s.GetChatSkill("tone", 0); cur.Version != 1 {
		t.Fatal("a preview stored a version")
	}
	// Confirming a text other than the one read is refused.
	if code, _, text := update(`{"confirm":true,"sha256":"` + chatSkillHash("something else") + `"}`); code != http.StatusConflict {
		t.Errorf("a stale confirm: %d %s", code, text)
	}
	code, out, text = update(`{"confirm":true,"sha256":"` + out.Preview.Sha256 + `"}`)
	if code != http.StatusOK || !out.Saved || !out.Changed || out.Skill.Version != 2 || out.Skill.Content != "Be kind and brief." ||
		out.Skill.Source.Commit != skillShaTwo || out.Skill.Description != "Friendly answers" || len(out.Skill.History) != 1 || out.Version != 2 {
		t.Fatalf("confirmed: %d %s", code, text)
	}

	// What cannot be updated.
	if code, _, text := update(`{`); code != http.StatusBadRequest {
		t.Errorf("bad json: %d %s", code, text)
	}
	if rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/skills/ghost/update", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a missing skill: %d", rec.Code)
	}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/pasted", `{"content":"x"}`); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	if rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/skills/pasted/update", ""); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "no source to update from") {
		t.Errorf("a pasted skill: %d %s", rec.Code, rec.Body.String())
	}
	// The source is gone, or holds nothing to store.
	for page, why := range map[string]string{"/ghost": "a source that is gone", "/empty": "an empty source"} {
		raw, _ := json.Marshal(map[string]any{"content": "x", "source": map[string]any{"url": src.URL + page, "fetchedAt": "2026-11-11T10:00:00Z"}})
		if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/lost", string(raw)); rec.Code/100 != 2 {
			t.Fatal(rec.Body.String())
		}
		if rec := chatREST(t, s, root, http.MethodPost, "/v1/chat/skills/lost/update", `{"confirm":true}`); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", why, rec.Code, rec.Body.String())
		}
	}
}
