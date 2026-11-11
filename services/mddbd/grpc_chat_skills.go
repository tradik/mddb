package main

import (
	"context"
	"errors"
	"time"

	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Chat skills over gRPC (#404). Each method calls what the REST handler
// calls, so the surfaces answer alike. Every method needs a global
// administrator — the access gate says so (access_rules_grpc.go) and each
// method checks again — and a method that stores needs a writable server.

// ListChatSkills lists the skills, as GET /v1/chat/skills.
func (g *GRPCServer) ListChatSkills(ctx context.Context, _ *proto.ListChatSkillsRequest) (*proto.ListChatSkillsResponse, error) {
	if err := g.requireAdminRPC(ctx); err != nil {
		return nil, err
	}
	list := g.server.ListChatSkills()
	out := &proto.ListChatSkillsResponse{Skills: make([]*proto.ChatSkillProto, 0, len(list)), Version: g.server.ChatScenariosVersion()}
	for _, sk := range list {
		out.Skills = append(out.Skills, chatSkillToProto(sk))
	}
	return out, nil
}

// GetChatSkill returns one skill, as GET /v1/chat/skills/{name}?version=.
func (g *GRPCServer) GetChatSkill(ctx context.Context, req *proto.GetChatSkillRequest) (*proto.ChatSkillProto, error) {
	if err := g.requireAdminRPC(ctx); err != nil {
		return nil, err
	}
	sk, err := g.server.GetChatSkill(req.GetName(), int(req.GetVersion()))
	if err != nil {
		return nil, chatSkillGRPCError(err)
	}
	return chatSkillToProto(sk), nil
}

// PutChatSkill creates a skill or stores a new version, as
// PUT /v1/chat/skills/{name}.
func (g *GRPCServer) PutChatSkill(ctx context.Context, req *proto.PutChatSkillRequest) (*proto.PutChatSkillResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil { // a writable server and an administrator
		return nil, err
	}
	p := req.GetSkill()
	sk := &ChatSkill{Name: p.GetName(), Description: p.GetDescription(), Content: p.GetContent(), Source: chatSkillSourceFromProto(p.GetSource())}
	view, created, err := g.server.PutChatSkill(sk)
	if err != nil {
		return nil, chatSkillGRPCError(err)
	}
	return &proto.PutChatSkillResponse{Skill: chatSkillToProto(view), Created: created, Version: g.server.ChatScenariosVersion()}, nil
}

// DeleteChatSkill removes a skill, as DELETE /v1/chat/skills/{name}.
func (g *GRPCServer) DeleteChatSkill(ctx context.Context, req *proto.DeleteChatSkillRequest) (*proto.DeleteChatSkillResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil {
		return nil, err
	}
	usedBy, err := g.server.DeleteChatSkill(req.GetName())
	if errors.Is(err, errChatSkillInUse) {
		return nil, status.Error(codes.FailedPrecondition, chatSkillInUseMessage(req.GetName(), usedBy))
	}
	if err != nil {
		return nil, chatSkillGRPCError(err)
	}
	return &proto.DeleteChatSkillResponse{Deleted: true, Version: g.server.ChatScenariosVersion()}, nil
}

// ImportChatSkill previews a SKILL.md, as POST /v1/chat/skills/import.
func (g *GRPCServer) ImportChatSkill(ctx context.Context, req *proto.ImportChatSkillRequest) (*proto.ChatSkillPreviewProto, error) {
	if err := g.requireAdminRPC(ctx); err != nil {
		return nil, err
	}
	p, err := g.server.ImportChatSkill(ctx, req.GetUrl(), req.GetContent())
	if err != nil {
		return nil, chatSkillGRPCError(err)
	}
	return chatSkillPreviewToProto(p), nil
}

// UpdateChatSkill fetches a skill's source again, as
// POST /v1/chat/skills/{name}/update.
func (g *GRPCServer) UpdateChatSkill(ctx context.Context, req *proto.UpdateChatSkillRequest) (*proto.UpdateChatSkillResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil {
		return nil, err
	}
	out, err := g.server.UpdateChatSkill(ctx, req.GetName(), req.GetConfirm(), req.GetSha256())
	if err != nil {
		return nil, chatSkillGRPCError(err)
	}
	return &proto.UpdateChatSkillResponse{Skill: chatSkillToProto(out.Skill), Preview: chatSkillPreviewToProto(out.Preview),
		Changed: out.Changed, Saved: out.Saved, Version: out.Version}, nil
}

// GetChatSkillUsedBy names the scenarios attaching a skill, as
// GET /v1/chat/skills/{name}/used-by.
func (g *GRPCServer) GetChatSkillUsedBy(ctx context.Context, req *proto.GetChatSkillUsedByRequest) (*proto.GetChatSkillUsedByResponse, error) {
	if err := g.requireAdminRPC(ctx); err != nil {
		return nil, err
	}
	scenarios, err := g.server.ChatSkillUsedBy(req.GetName())
	if err != nil {
		return nil, chatSkillGRPCError(err)
	}
	return &proto.GetChatSkillUsedByResponse{Name: req.GetName(), Scenarios: scenarios}, nil
}

// chatSkillGRPCError maps a failure to its status, as chatSkillAnswer maps
// it to an HTTP one.
func chatSkillGRPCError(err error) error {
	code := codes.InvalidArgument
	switch {
	case errors.Is(err, errChatSkillNotFound):
		code = codes.NotFound
	case errors.Is(err, errChatSkillStale):
		code = codes.Aborted
	}
	return status.Error(code, err.Error())
}

// chatSkillSourceToProto is a source on the wire, nil for a pasted skill.
func chatSkillSourceToProto(src *ChatSkillSource) *proto.ChatSkillSourceProto {
	if src == nil {
		return nil
	}
	return &proto.ChatSkillSourceProto{Url: src.URL, Commit: src.Commit, FetchedAt: chatUnix(src.FetchedAt)}
}

// chatSkillSourceFromProto reads a source sent by a client.
func chatSkillSourceFromProto(p *proto.ChatSkillSourceProto) *ChatSkillSource {
	if p == nil {
		return nil
	}
	src := &ChatSkillSource{URL: p.GetUrl(), Commit: p.GetCommit()}
	if p.GetFetchedAt() != 0 {
		src.FetchedAt = time.Unix(p.GetFetchedAt(), 0).UTC()
	}
	return src
}

// chatSkillToProto is the skill on the wire.
func chatSkillToProto(v *chatSkillView) *proto.ChatSkillProto {
	out := &proto.ChatSkillProto{Name: v.Name, Description: v.Description, Content: v.Content,
		Source: chatSkillSourceToProto(v.Source), Version: safeInt32(v.Version), Bytes: int64(v.Bytes), UsedBy: v.UsedBy,
		CreatedAt: chatUnix(v.CreatedAt), UpdatedAt: chatUnix(v.UpdatedAt)}
	for _, r := range v.History {
		out.History = append(out.History, &proto.ChatSkillRevisionProto{Version: safeInt32(r.Version), Description: r.Description,
			Bytes: int64(r.Bytes), Source: chatSkillSourceToProto(r.Source), UpdatedAt: chatUnix(r.UpdatedAt)})
	}
	return out
}

// chatSkillPreviewToProto is a preview on the wire.
func chatSkillPreviewToProto(p *chatSkillPreview) *proto.ChatSkillPreviewProto {
	return &proto.ChatSkillPreviewProto{Name: p.Name, Description: p.Description, Content: p.Content, Raw: p.Raw,
		Source: chatSkillSourceToProto(p.Source), Bytes: int64(p.Bytes), Sha256: p.Sha256, Exists: p.Exists}
}
