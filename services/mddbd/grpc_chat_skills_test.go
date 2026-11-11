package main

import (
	"strings"
	"testing"
	"time"

	"mddb/proto"

	"google.golang.org/grpc/codes"
)

// #404: chat skills over gRPC.

func TestChatSkillGRPCLifecycle(t *testing.T) {
	src := newSkillSource(t)
	s := chatServer(t)
	g, admin := NewGRPCServer(s), asClaims(root)
	blob := "https://github.com/acme/skills/blob/main/tone/SKILL.md"

	preview, err := g.ImportChatSkill(admin, &proto.ImportChatSkillRequest{Url: blob})
	if err != nil || preview.GetName() != "tone" || preview.GetContent() != "Be kind." || preview.GetRaw() != toneSkillMD ||
		preview.GetSource().GetCommit() != skillShaOne || preview.GetSource().GetFetchedAt() == 0 || preview.GetExists() ||
		preview.GetBytes() != 8 || preview.GetSha256() != chatSkillHash("Be kind.") || preview.GetDescription() != "Friendly answers" {
		t.Fatalf("import: %+v, %v", preview, err)
	}
	put, err := g.PutChatSkill(admin, &proto.PutChatSkillRequest{Skill: &proto.ChatSkillProto{Name: preview.GetName(),
		Description: preview.GetDescription(), Content: preview.GetContent(), Source: preview.GetSource()}})
	if err != nil || !put.GetCreated() || put.GetVersion() != 1 || put.GetSkill().GetVersion() != 1 ||
		put.GetSkill().GetSource().GetUrl() != blob || put.GetSkill().GetSource().GetFetchedAt() != preview.GetSource().GetFetchedAt() ||
		put.GetSkill().GetCreatedAt() == 0 || put.GetSkill().GetBytes() != 8 {
		t.Fatalf("put: %+v, %v", put, err)
	}
	// The source moves; a preview, then a confirmed update.
	src.head.Store(skillShaTwo)
	up, err := g.UpdateChatSkill(admin, &proto.UpdateChatSkillRequest{Name: "tone"})
	if err != nil || !up.GetChanged() || up.GetSaved() || up.GetSkill().GetVersion() != 1 || up.GetPreview().GetContent() != "Be kind and brief." {
		t.Fatalf("update preview: %+v, %v", up, err)
	}
	if _, err := g.UpdateChatSkill(admin, &proto.UpdateChatSkillRequest{Name: "tone", Confirm: true, Sha256: "stale"}); grpcCode(err) != codes.Aborted {
		t.Errorf("a stale confirm: %v", err)
	}
	up, err = g.UpdateChatSkill(admin, &proto.UpdateChatSkillRequest{Name: "tone", Confirm: true, Sha256: up.GetPreview().GetSha256()})
	if err != nil || !up.GetSaved() || up.GetSkill().GetVersion() != 2 || up.GetVersion() != 2 || len(up.GetSkill().GetHistory()) != 1 ||
		up.GetSkill().GetHistory()[0].GetVersion() != 1 || up.GetSkill().GetHistory()[0].GetBytes() != 8 ||
		up.GetSkill().GetHistory()[0].GetSource().GetCommit() != skillShaOne || up.GetSkill().GetHistory()[0].GetUpdatedAt() == 0 {
		t.Fatalf("update: %+v, %v", up, err)
	}
	old, err := g.GetChatSkill(admin, &proto.GetChatSkillRequest{Name: "tone", Version: 1})
	if err != nil || old.GetContent() != "Be kind." || old.GetVersion() != 1 {
		t.Fatalf("version 1: %+v, %v", old, err)
	}
	cur, err := g.GetChatSkill(admin, &proto.GetChatSkillRequest{Name: "tone"})
	if err != nil || cur.GetContent() != "Be kind and brief." || cur.GetVersion() != 2 {
		t.Fatalf("get: %+v, %v", cur, err)
	}
	// A pasted skill has no source on the wire.
	pasted, err := g.PutChatSkill(admin, &proto.PutChatSkillRequest{Skill: &proto.ChatSkillProto{Name: "plain", Content: "Plain."}})
	if err != nil || pasted.GetSkill().GetSource() != nil {
		t.Fatalf("pasted: %+v, %v", pasted, err)
	}
	list, err := g.ListChatSkills(admin, &proto.ListChatSkillsRequest{})
	if err != nil || len(list.GetSkills()) != 2 || list.GetSkills()[1].GetName() != "tone" || list.GetSkills()[1].GetContent() != "" ||
		list.GetSkills()[1].GetBytes() != 18 || list.GetVersion() != 3 {
		t.Fatalf("list: %+v, %v", list, err)
	}

	// Used by a scenario: listed, in its prompt, and not deletable.
	if _, err := g.PutChatScenario(admin, &proto.PutChatScenarioRequest{Scenario: &proto.ChatScenarioProto{Name: "support", Skills: []string{"tone", "ghost"}}}); err != nil {
		t.Fatal(err)
	}
	used, err := g.GetChatSkillUsedBy(admin, &proto.GetChatSkillUsedByRequest{Name: "tone"})
	if err != nil || used.GetName() != "tone" || len(used.GetScenarios()) != 1 || used.GetScenarios()[0] != "support" {
		t.Fatalf("used by: %+v, %v", used, err)
	}
	prompt, err := g.GetChatScenarioPrompt(admin, &proto.GetChatScenarioPromptRequest{Name: "support"})
	if err != nil || !strings.Contains(prompt.GetPrompt(), "Skill tone — Friendly answers:\nBe kind and brief.") ||
		prompt.GetBytes() != int64(len(prompt.GetPrompt())) || prompt.GetApproxTokens() != prompt.GetBytes()/4 ||
		len(prompt.GetMissingSkills()) != 1 || prompt.GetMissingSkills()[0] != "ghost" || prompt.GetWarning() != "" {
		t.Fatalf("prompt: %+v, %v", prompt, err)
	}
	if _, err := g.DeleteChatSkill(admin, &proto.DeleteChatSkillRequest{Name: "tone"}); grpcCode(err) != codes.FailedPrecondition ||
		!strings.Contains(err.Error(), "in use by: support") {
		t.Errorf("delete in use: %v", err)
	}

	// Refusals.
	if _, err := g.GetChatSkill(admin, &proto.GetChatSkillRequest{Name: "ghost"}); grpcCode(err) != codes.NotFound {
		t.Errorf("get of a missing skill: %v", err)
	}
	if _, err := g.GetChatSkill(admin, &proto.GetChatSkillRequest{Name: "tone", Version: 9}); grpcCode(err) != codes.NotFound {
		t.Errorf("a version not kept: %v", err)
	}
	if _, err := g.GetChatSkillUsedBy(admin, &proto.GetChatSkillUsedByRequest{Name: "ghost"}); grpcCode(err) != codes.NotFound {
		t.Errorf("used by of a missing skill: %v", err)
	}
	if _, err := g.PutChatSkill(admin, &proto.PutChatSkillRequest{}); grpcCode(err) != codes.InvalidArgument {
		t.Errorf("put without a skill: %v", err)
	}
	if _, err := g.ImportChatSkill(admin, &proto.ImportChatSkillRequest{}); grpcCode(err) != codes.InvalidArgument {
		t.Errorf("import of nothing: %v", err)
	}
	if _, err := g.UpdateChatSkill(admin, &proto.UpdateChatSkillRequest{Name: "plain"}); grpcCode(err) != codes.InvalidArgument {
		t.Errorf("update of a pasted skill: %v", err)
	}
	if _, err := g.DeleteChatSkill(admin, &proto.DeleteChatSkillRequest{Name: "ghost"}); grpcCode(err) != codes.NotFound {
		t.Errorf("delete of a missing skill: %v", err)
	}
	// Admin only, reads included.
	for _, who := range []*JWTClaims{alice, tina} {
		ctx := asClaims(who)
		_, e1 := g.ListChatSkills(ctx, &proto.ListChatSkillsRequest{})
		_, e2 := g.GetChatSkill(ctx, &proto.GetChatSkillRequest{Name: "tone"})
		_, e3 := g.PutChatSkill(ctx, &proto.PutChatSkillRequest{Skill: &proto.ChatSkillProto{Name: "x", Content: "x"}})
		_, e4 := g.DeleteChatSkill(ctx, &proto.DeleteChatSkillRequest{Name: "plain"})
		_, e5 := g.ImportChatSkill(ctx, &proto.ImportChatSkillRequest{Content: "x"})
		_, e6 := g.UpdateChatSkill(ctx, &proto.UpdateChatSkillRequest{Name: "tone"})
		_, e7 := g.GetChatSkillUsedBy(ctx, &proto.GetChatSkillUsedByRequest{Name: "tone"})
		for i, err := range []error{e1, e2, e3, e4, e5, e6, e7} {
			if grpcCode(err) != codes.PermissionDenied {
				t.Errorf("%s call %d: %v", who.Username, i+1, err)
			}
		}
	}
	del, err := g.DeleteChatSkill(admin, &proto.DeleteChatSkillRequest{Name: "plain"})
	if err != nil || !del.GetDeleted() || del.GetVersion() != 5 {
		t.Fatalf("delete: %+v, %v", del, err)
	}
	s.Mode = ModeRead
	if _, err := g.PutChatSkill(admin, &proto.PutChatSkillRequest{Skill: &proto.ChatSkillProto{Name: "x", Content: "x"}}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("put on a read-only server: %v", err)
	}
}

func TestChatSkillSourceProto(t *testing.T) {
	if chatSkillSourceToProto(nil) != nil || chatSkillSourceFromProto(nil) != nil {
		t.Error("a nil source is not nil on the other side")
	}
	if src := chatSkillSourceFromProto(&proto.ChatSkillSourceProto{Url: "https://example.com/s"}); !src.FetchedAt.IsZero() {
		t.Errorf("a missing fetch time became %v", src.FetchedAt)
	}
	at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	back := chatSkillSourceFromProto(chatSkillSourceToProto(&ChatSkillSource{URL: "https://example.com/s", Commit: "abc", FetchedAt: at}))
	if back.URL != "https://example.com/s" || back.Commit != "abc" || !back.FetchedAt.Equal(at) {
		t.Errorf("round trip: %+v", back)
	}
}
