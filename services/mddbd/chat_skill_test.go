package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"
)

// #404: chat skills — the SKILL.md reader, validation, the store and the
// composition into a scenario's prompt.

const humanizerSkill = `---
name: humanizer
version: 2.1.1
description: |
  Remove signs of AI-generated writing from text. Use when editing
  text, so it sounds natural.
allowed-tools:
  - Read
---

# Humanizer

Rewrite the text plainly.
`

func TestParseChatSkill(t *testing.T) {
	name, description, content := parseChatSkill(humanizerSkill)
	if name != "humanizer" || content != "# Humanizer\n\nRewrite the text plainly." ||
		description != "Remove signs of AI-generated writing from text. Use when editing\ntext, so it sounds natural." {
		t.Errorf("block scalar: %q, %q, %q", name, description, content)
	}
	name, description, content = parseChatSkill("---\nname: tone\ndescription: >-\n  Friendly,\n  short.\n\nother: x\n---\nBe kind.")
	if name != "tone" || description != "Friendly, short." || content != "Be kind." {
		t.Errorf("folded scalar: %q, %q, %q", name, description, content)
	}
	name, description, content = parseChatSkill("---\nname: \"triage\"\ndescription: Sort tickets, then answer\n---\nSort first.")
	if name != "triage" || description != "Sort tickets, then answer" || content != "Sort first." {
		t.Errorf("one line: %q, %q, %q", name, description, content)
	}
	if name, description, content = parseChatSkill("  Just instructions.\n"); name != "" || description != "" || content != "Just instructions." {
		t.Errorf("no front matter: %q, %q, %q", name, description, content)
	}
}

func TestChatSkillNormalize(t *testing.T) {
	sk := &ChatSkill{Name: "tone", Description: " Friendly. ", Content: "\nBe kind.\n",
		Source: &ChatSkillSource{URL: "https://example.com/SKILL.md", FetchedAt: time.Unix(1, 0)}}
	if err := sk.Normalize(); err != nil || sk.Description != "Friendly." || sk.Content != "Be kind." {
		t.Fatalf("normalize: %+v, %v", sk, err)
	}
	t.Setenv("MDDB_CHAT_SKILL_MAX_BYTES", "16")
	for why, bad := range map[string]*ChatSkill{
		"no name":         {Content: "x"},
		"reserved import": {Name: "import", Content: "x"},
		"reserved":        {Name: "version", Content: "x"},
		"a slash":         {Name: "a/b", Content: "x"},
		"no content":      {Name: "a", Content: "  "},
		"too long":        {Name: "a", Content: strings.Repeat("x", 17)},
		"not utf-8":       {Name: "a", Content: "\xff"},
		"description":     {Name: "a", Content: "x", Description: "\xff"},
		"http source":     {Name: "a", Content: "x", Source: &ChatSkillSource{URL: "http://example.com/s", FetchedAt: time.Unix(1, 0)}},
		"no fetch time":   {Name: "a", Content: "x", Source: &ChatSkillSource{URL: "https://example.com/s"}},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("%s was accepted", why)
		}
	}
	if err := validChatKeyName("skill", "import", "version", "import"); err == nil || !strings.Contains(err.Error(), `"version" or "import"`) {
		t.Errorf("reserved names in the message: %v", err)
	}
}

func TestChatByteLimits(t *testing.T) {
	if chatSkillMaxBytes() != chatSkillMaxBytesDefault || chatPromptMaxBytes() != chatPromptMaxBytesDefault {
		t.Errorf("defaults: %d, %d", chatSkillMaxBytes(), chatPromptMaxBytes())
	}
	t.Setenv("MDDB_CHAT_SKILL_MAX_BYTES", "100")
	t.Setenv("MDDB_CHAT_PROMPT_MAX_BYTES", "0")
	if chatSkillMaxBytes() != 100 || chatPromptMaxBytes() != chatPromptMaxBytesDefault {
		t.Errorf("set: %d, %d", chatSkillMaxBytes(), chatPromptMaxBytes())
	}
	t.Setenv("MDDB_CHAT_PROMPT_MAX_BYTES", "9999999999")
	if chatPromptMaxBytes() != chatPromptMaxBytesDefault {
		t.Error("a limit over 1 GiB was taken")
	}
	if chatPromptWarning(75, 100) != "" || !strings.Contains(chatPromptWarning(76, 100), "76% of the 100-byte limit") ||
		!strings.Contains(chatPromptWarning(101, 100), "over the 100-byte limit") {
		t.Errorf("warnings: %q, %q, %q", chatPromptWarning(75, 100), chatPromptWarning(76, 100), chatPromptWarning(101, 100))
	}
}

func TestChatSkillManagerVersionsAndHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	db := openChatDB(t, path)
	m := NewChatScenarioManager(db)
	if _, found := (*ChatScenarioManager)(nil).GetSkill("x"); found {
		t.Error("a nil manager has a skill")
	}
	if _, err := m.PutSkill(&ChatSkill{Name: "early", Content: "x"}); err == nil {
		t.Error("a write before LoadAll was accepted")
	}
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	m.SetBinlog(bl)

	sk := &ChatSkill{Name: "tone", Description: "Friendly", Content: "v1"}
	if created, err := m.PutSkill(sk); err != nil || !created || sk.Version != 1 || m.Version() != 1 {
		t.Fatalf("create: %+v created=%v err=%v version=%d", sk, created, err, m.Version())
	}
	// The same text again stores nothing.
	same := &ChatSkill{Name: "tone", Description: "Friendly", Content: "v1"}
	if created, err := m.PutSkill(same); err != nil || created || same.Version != 1 || same.CreatedAt.IsZero() || m.Version() != 1 {
		t.Fatalf("unchanged put: %+v created=%v err=%v version=%d", same, created, err, m.Version())
	}
	src := &ChatSkillSource{URL: "https://example.com/s", Commit: "abc", FetchedAt: clock}
	for i := 2; i <= 13; i++ {
		next := &ChatSkill{Name: "tone", Description: "Friendly", Content: fmt.Sprintf("v%d", i), Source: src}
		if created, err := m.PutSkill(next); err != nil || created || next.Version != i {
			t.Fatalf("version %d: %+v, %v", i, next, err)
		}
	}
	// The same text from another commit is a change; so is losing the source.
	moved := &ChatSkill{Name: "tone", Description: "Friendly", Content: "v13", Source: &ChatSkillSource{URL: src.URL, Commit: "def"}}
	if _, err := m.PutSkill(moved); err != nil || moved.Version != 14 {
		t.Fatalf("a new commit: %+v, %v", moved, err)
	}
	pasted := &ChatSkill{Name: "tone", Description: "Friendly", Content: "v13"}
	if _, err := m.PutSkill(pasted); err != nil || pasted.Version != 15 {
		t.Fatalf("without a source: %+v, %v", pasted, err)
	}
	got, _ := m.GetSkill("tone")
	if len(got.History) != chatSkillHistoryLen || got.History[0].Version != 14 || got.History[9].Version != 5 ||
		got.History[1].Content != "v13" || got.History[1].Bytes != 3 || !got.CreatedAt.Before(got.UpdatedAt) {
		t.Errorf("history: %+v", got.History)
	}
	if old, kept := got.at(5); !kept || old.Content != "v5" || old.Version != 5 || old.History != nil || old.Source.Commit != "abc" {
		t.Errorf("version 5: %+v kept=%v", old, kept)
	}
	if _, kept := got.at(4); kept {
		t.Error("version 4 is still kept")
	}
	if cur, kept := got.at(15); !kept || cur != got {
		t.Error("the current version is not itself")
	}

	// Used by, and a delete refused while a scenario attaches the skill.
	if _, err := m.PutSkill(&ChatSkill{Name: "alpha", Content: "a"}); err != nil {
		t.Fatal(err)
	}
	for _, sc := range []*ChatScenario{{Name: "zeta", Skills: []string{"tone", "tone"}}, {Name: "beta", Skills: []string{"alpha", "tone"}}, {Name: "plain"}} {
		if _, err := m.Put(sc); err != nil {
			t.Fatal(err)
		}
	}
	if used := m.SkillUsedBy("tone"); len(used) != 2 || used[0] != "beta" || used[1] != "zeta" {
		t.Errorf("used by: %v", used)
	}
	before := m.Version()
	if used, err := m.DeleteSkill("tone"); err != errChatSkillInUse || len(used) != 2 || m.Version() != before {
		t.Errorf("delete in use: %v, %v", used, err)
	}
	if _, err := m.DeleteSkill("ghost"); err != errChatSkillNotFound {
		t.Errorf("delete of a missing skill: %v", err)
	}
	if list := m.ListSkills(); len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "tone" {
		t.Errorf("list: %+v", list)
	}

	// A restart keeps the skills, their history and the one counter.
	_ = db.Close()
	db = openChatDB(t, path)
	m2 := NewChatScenarioManager(db)
	if err := m2.LoadAll(); err != nil {
		t.Fatal(err)
	}
	again, found := m2.GetSkill("tone")
	if !found || again.Version != 15 || len(again.History) != chatSkillHistoryLen || m2.Version() != before {
		t.Errorf("after a restart: %+v found=%v version=%d, want %d", again, found, m2.Version(), before)
	}
	for _, name := range []string{"zeta", "beta"} {
		if err := m2.Delete(name); err != nil {
			t.Fatal(err)
		}
	}
	if used, err := m2.DeleteSkill("tone"); err != nil || used != nil || m2.Version() != before+3 {
		t.Errorf("delete: %v, %v, version %d", used, err, m2.Version())
	}
	if _, found := m2.GetSkill("tone"); found {
		t.Error("a deleted skill is still there")
	}
	// A closed database refuses the write and keeps the counter.
	_ = db.Close()
	if _, err := m2.DeleteSkill("alpha"); err == nil || m2.Version() != before+3 {
		t.Errorf("delete on a closed database: %v", err)
	}
}

// A follower applies the leader's binlog entries for a skill and serves it.
func TestChatSkillsReplicate(t *testing.T) {
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	ra := NewReplicationApplier(s)
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogPut, BucketName: string(bucketChatSkills), Key: []byte("tone"),
		Value: []byte(`{"name":"tone","content":"Be kind.","version":3}`)})
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 2, Type: binlog.BinlogPut, BucketName: string(bucketChatSkills), Key: []byte("broken"), Value: []byte(`{`)})
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 3, Type: binlog.BinlogPut, BucketName: string(bucketChatScenarios), Key: chatScenarioVersionKey,
		Value: []byte{0, 0, 0, 0, 0, 0, 0, 9}})
	if sk, found := s.ChatScenarios.GetSkill("tone"); !found || sk.Content != "Be kind." || sk.Version != 3 || s.ChatScenariosVersion() != 9 {
		t.Errorf("after replication: %+v found=%v version=%d", sk, found, s.ChatScenariosVersion())
	}
	if _, found := s.ChatScenarios.GetSkill("broken"); found {
		t.Error("a corrupt entry became a skill")
	}
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 4, Type: binlog.BinlogDelete, BucketName: string(bucketChatSkills), Key: []byte("tone")})
	if _, found := s.ChatScenarios.GetSkill("tone"); found {
		t.Error("a replicated delete left the skill")
	}
}

func TestComposeChatPromptAppendsSkillsAfterGuardrails(t *testing.T) {
	skills := map[string]*ChatSkill{
		"tone":   {Name: "tone", Description: "Friendly", Content: "Be kind."},
		"triage": {Name: "triage", Content: "Sort first."},
	}
	lookup := func(name string) (*ChatSkill, bool) { sk, found := skills[name]; return sk, found }
	sc := &ChatScenario{OperatorPrompt: "You are Ada.", Guardrails: "No legal advice.",
		Skills: []string{"triage", "ghost", "tone", "triage", "phantom"}, AllowedCollections: []string{"docs"}}
	got, missing := ComposeChatPrompt(sc, func(string) string { return "Use code blocks." }, lookup)
	want := []string{"You are Ada.", "Guardrails — never do the following:\nNo legal advice.", "decline politely",
		chatSkillsHeader, "Skill triage:\nSort first.", "Skill tone — Friendly:\nBe kind.", "Do not use emoji.", "collection docs"}
	at := 0
	for _, part := range want {
		i := strings.Index(got[at:], part)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", part, got)
		}
		at += i + len(part)
	}
	if strings.Count(got, "Sort first.") != 1 || !strings.Contains(chatSkillsHeader, "take precedence") {
		t.Errorf("a skill named twice was composed twice:\n%s", got)
	}
	if len(missing) != 2 || missing[0] != "ghost" || missing[1] != "phantom" {
		t.Errorf("missing: %v", missing)
	}
	// Without skills the prompt is what it was before skills existed.
	plain, none := ComposeChatPrompt(&ChatScenario{OperatorPrompt: "You are Ada."}, func(string) string { return "" }, lookup)
	if none != nil || strings.Contains(plain, "Skill") || !strings.HasPrefix(plain, "You are Ada.\n\nIf a question is outside your scope") {
		t.Errorf("no skills: %q, %v", plain, none)
	}
	// Only missing skills: no header.
	if p, m := ComposeChatPrompt(&ChatScenario{Skills: []string{"ghost"}}, func(string) string { return "" }, lookup); strings.Contains(p, "Skills —") || len(m) != 1 {
		t.Errorf("only a missing skill: %q, %v", p, m)
	}
}
