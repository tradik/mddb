package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// #403: the scenario store — validation, defaults, the persisted version
// counter and the composed prompt.

func anyCollection(string) bool { return true }

// noChatSkills is a skill store without skills.
func noChatSkills(string) (*ChatSkill, bool) { return nil, false }

func TestChatScenarioNormalizeDefaultsAndRefusals(t *testing.T) {
	sc := &ChatScenario{Name: "support", AllowedCollections: []string{"docs"}}
	if err := sc.Normalize(anyCollection); err != nil {
		t.Fatal(err)
	}
	if sc.OffTopic.Mode != OffTopicRefuse || sc.Style.Emoji != EmojiOff || sc.Style.Verbosity != VerbosityNormal {
		t.Errorf("defaults: %+v %+v", sc.OffTopic, sc.Style)
	}
	if err := (&ChatScenario{Name: "s"}).Normalize(nil); err != nil {
		t.Errorf("no collections, no checker: %v", err)
	}

	hot, neg := 2.5, -0.1
	big := strings.Repeat("x", maxChatScenarioTextBytes+1)
	for why, bad := range map[string]*ChatScenario{
		"no name":          {},
		"long name":        {Name: strings.Repeat("n", maxChatScenarioNameLen+1)},
		"slash":            {Name: "a/b"},
		"reserved":         {Name: "version"},
		"big prompt":       {Name: "s", OperatorPrompt: big},
		"bad utf8":         {Name: "s", Guardrails: "\xff"},
		"off-topic mode":   {Name: "s", OffTopic: ChatOffTopic{Mode: "ignore"}},
		"emoji":            {Name: "s", Style: ChatStyle{Emoji: "lots"}},
		"verbosity":        {Name: "s", Style: ChatStyle{Verbosity: "epic"}},
		"hot temperature":  {Name: "s", LLM: ChatLLM{Temperature: &hot}},
		"cold temperature": {Name: "s", LLM: ChatLLM{Temperature: &neg}},
		"negative turns":   {Name: "s", MaxTurns: -1},
		"too many skills":  {Name: "s", Skills: make([]string, maxChatScenarioSkills+1)},
		"empty skill":      {Name: "s", Skills: []string{""}},
		"empty collection": {Name: "s", AllowedCollections: []string{""}},
		"twice":            {Name: "s", AllowedCollections: []string{"a", "a"}},
	} {
		if bad.Normalize(anyCollection) == nil {
			t.Errorf("%s was accepted", why)
		}
	}
	missing := &ChatScenario{Name: "s", AllowedCollections: []string{"ghost"}}
	if err := missing.Normalize(func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unknown collection: %v", err)
	}
}

func TestComposeChatPromptOrderAndClauses(t *testing.T) {
	sc := &ChatScenario{Name: "s", OperatorPrompt: " You are Ada. ", Guardrails: "No legal advice.",
		OffTopic:           ChatOffTopic{Mode: OffTopicRedirect, RedirectTemplate: "Ask me about MDDB."},
		Style:              ChatStyle{Emoji: EmojiSparingly, Verbosity: VerbosityShort, Markdown: true, CitationTemplate: "[{{key}}]"},
		AllowedCollections: []string{"docs", "plain", "runbooks"}}
	got, _ := ComposeChatPrompt(sc, func(c string) string {
		return map[string]string{"docs": "Use code blocks.", "runbooks": "Numbered steps."}[c]
	}, noChatSkills)
	want := []string{"You are Ada.", "Guardrails — never do the following:\nNo legal advice.",
		"reply with: Ask me about MDDB.", "Use emoji sparingly. Keep answers short. Format answers in Markdown. Cite every source as: [{{key}}]",
		"For answers drawn from the collection docs:\nUse code blocks.", "For answers drawn from the collection runbooks:\nNumbered steps."}
	at := 0
	for _, part := range want {
		i := strings.Index(got[at:], part)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", part, got)
		}
		at += i + len(part)
	}
	if strings.Contains(got, "collection plain") {
		t.Error("a collection without a responsePrompt got a paragraph")
	}

	none := func(string) string { return "" }
	for mode, phrase := range map[string]string{OffTopicRefuse: "decline politely", OffTopicBrief: "answer it briefly",
		OffTopicRedirect: defaultRedirectTemplate} {
		if p, _ := ComposeChatPrompt(&ChatScenario{OffTopic: ChatOffTopic{Mode: mode}}, none, noChatSkills); !strings.Contains(p, phrase) {
			t.Errorf("%s: %q", mode, p)
		}
	}
	for style, phrase := range map[ChatStyle]string{
		{Emoji: EmojiFree, Verbosity: VerbosityLong}: "Emoji are welcome. Answer thoroughly, with detail and examples. Answer in plain text, without Markdown.",
		{Emoji: EmojiOff}: "Do not use emoji. Answer in plain text",
	} {
		if p, _ := ComposeChatPrompt(&ChatScenario{Style: style}, none, noChatSkills); !strings.Contains(p, phrase) {
			t.Errorf("%+v: %q", style, p)
		}
	}
}

func openChatDB(t *testing.T, path string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Closed before t.TempDir() is removed (cleanups run last-registered
	// first): Windows cannot delete a file that is still open. bbolt's Close
	// is a no-op on a closed database, so a test may still close it itself.
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestChatScenarioManagerPersistsScenariosAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	db := openChatDB(t, path)
	m := NewChatScenarioManager(db)
	if (*ChatScenarioManager)(nil).Version() != 0 {
		t.Error("nil manager version")
	}
	if _, err := m.Put(&ChatScenario{Name: "early"}); err == nil {
		t.Error("a write before LoadAll was accepted")
	}
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "x.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	m.SetBinlog(bl)

	if created, err := m.Put(&ChatScenario{Name: "b", Description: "one"}); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if created, err := m.Put(&ChatScenario{Name: "a"}); err != nil || !created {
		t.Fatalf("create a: %v %v", created, err)
	}
	first, _ := m.Get("b")
	if created, err := m.Put(&ChatScenario{Name: "b", Description: "two"}); err != nil || created {
		t.Fatalf("replace: %v %v", created, err)
	}
	second, _ := m.Get("b")
	if !second.CreatedAt.Equal(first.CreatedAt) || !second.UpdatedAt.After(first.UpdatedAt) || second.Description != "two" {
		t.Errorf("replace kept %v/%v, was %v/%v", second.CreatedAt, second.UpdatedAt, first.CreatedAt, first.UpdatedAt)
	}
	second.Description = "mutated copy"
	if again, _ := m.Get("b"); again.Description != "two" {
		t.Error("Get handed out the stored value")
	}
	if list := m.List(); len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Errorf("list: %+v", list)
	}
	if err := m.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("a"); !errors.Is(err, errChatScenarioNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	if _, found := m.Get("a"); found || m.Version() != 4 {
		t.Errorf("after 3 puts and a delete: found=%v version=%d", found, m.Version())
	}
	if bl.CurrentLSN() != 8 {
		t.Errorf("binlog holds %d entries, want a change and a version for each of 4 writes", bl.CurrentLSN())
	}

	// A corrupt entry is skipped; the rest and the counter survive a restart.
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketChatScenarios).Put([]byte("broken"), []byte("{"))
	}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := m.Put(&ChatScenario{Name: "late"}); err == nil {
		t.Error("a write to a closed database was accepted")
	}
	if err := m.Delete("b"); err == nil {
		t.Error("a delete on a closed database was accepted")
	}
	if err := m.LoadAll(); err == nil {
		t.Error("LoadAll on a closed database")
	}
	db2 := openChatDB(t, path)
	defer func() { _ = db2.Close() }()
	m2 := NewChatScenarioManager(db)
	m2.FollowDB(func() *bolt.DB { return db2 })
	if err := m2.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if list := m2.List(); len(list) != 1 || list[0].Name != "b" || m2.Version() != 4 {
		t.Errorf("after restart: %+v version=%d", list, m2.Version())
	}
}

func TestChatScenarioPutRefusesWhatJSONCannotCarry(t *testing.T) {
	db := openChatDB(t, filepath.Join(t.TempDir(), "chat.db"))
	defer func() { _ = db.Close() }()
	m := NewChatScenarioManager(db)
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Put(&ChatScenario{Name: "s", LLM: ChatLLM{ExtraBody: map[string]any{"f": func() {}}}}); err == nil {
		t.Error("an extraBody that is not JSON was stored")
	}
	if _, err := m.Put(&ChatScenario{}); err == nil {
		t.Error("a scenario without a name was stored")
	}
	if m.Version() != 0 {
		t.Error("a refused write bumped the version")
	}
}

// A follower applies the leader's two binlog entries — the scenario and the
// version counter — and serves both without a restart.
func TestChatScenariosReplicate(t *testing.T) {
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	ra := NewReplicationApplier(s)
	bucket := string(bucketChatScenarios)
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogPut, BucketName: bucket, Key: []byte("support"),
		Value: []byte(`{"name":"support","operatorPrompt":"You are Ada."}`)})
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 2, Type: binlog.BinlogPut, BucketName: bucket, Key: chatScenarioVersionKey,
		Value: []byte{0, 0, 0, 0, 0, 0, 0, 7}})
	if sc, found := s.ChatScenarios.Get("support"); !found || sc.OperatorPrompt != "You are Ada." || s.ChatScenariosVersion() != 7 {
		t.Errorf("after replication: %+v found=%v version=%d", sc, found, s.ChatScenariosVersion())
	}
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 3, Type: binlog.BinlogDelete, BucketName: bucket, Key: []byte("support")})
	if _, found := s.ChatScenarios.Get("support"); found {
		t.Error("a replicated delete left the scenario")
	}
	// Without the manager the entry still applies.
	s.ChatScenarios = nil
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 4, Type: binlog.BinlogPut, BucketName: bucket, Key: []byte("k"), Value: []byte("{}")})
	if ra.LastAppliedLSN() != 4 {
		t.Errorf("LSN = %d, want 4", ra.LastAppliedLSN())
	}
}
