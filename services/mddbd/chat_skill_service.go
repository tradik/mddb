package main

import (
	"context"
	"errors"
	"fmt"
)

// The one implementation of chat skills (#404) that REST, gRPC and MCP call.
// Every call needs a global administrator, reads too — a skill's text is
// part of system prompts — which each surface's gate checks before it gets
// here (access_rules_*.go).

// chatSkillView is a skill as the API shows it: with its size and the
// scenarios using it. A listing leaves the content and the history out; a
// read by name lists the history without its contents.
type chatSkillView struct {
	ChatSkill
	Bytes  int      `json:"bytes"`
	UsedBy []string `json:"usedBy"`
}

// chatSkillPreview is a SKILL.md parsed but not stored: what an import or
// an update would save. Raw is the whole text as fetched or pasted, front
// matter included, so an operator reads all of it before saving.
type chatSkillPreview struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Content     string           `json:"content"`
	Raw         string           `json:"raw"`
	Source      *ChatSkillSource `json:"source,omitempty"`
	Bytes       int              `json:"bytes"`
	Sha256      string           `json:"sha256"` // of content: what a confirmed update names
	Exists      bool             `json:"exists"` // a skill of this name is stored
}

// chatSkillUpdate answers an update from the source: the stored skill, what
// the source holds now, and whether it was saved.
type chatSkillUpdate struct {
	Skill   *chatSkillView    `json:"skill"`
	Preview *chatSkillPreview `json:"preview"`
	Changed bool              `json:"changed"`
	Saved   bool              `json:"saved"`
	Version uint64            `json:"version"`
}

// chatSkillView builds the view of sk. full keeps the content.
func (s *Server) chatSkillView(sk *ChatSkill, full bool) *chatSkillView {
	v := &chatSkillView{ChatSkill: *sk, Bytes: len(sk.Content), UsedBy: s.ChatScenarios.SkillUsedBy(sk.Name)}
	v.History = nil
	if !full {
		v.Content = ""
		return v
	}
	for _, r := range sk.History {
		r.Content = ""
		v.History = append(v.History, r)
	}
	return v
}

// ListChatSkills returns every skill, by name, without contents.
func (s *Server) ListChatSkills() []*chatSkillView {
	out := []*chatSkillView{}
	if s.ChatScenarios == nil {
		return out
	}
	for _, sk := range s.ChatScenarios.ListSkills() {
		out = append(out, s.chatSkillView(sk, false))
	}
	return out
}

// storedChatSkill looks a skill up.
func (s *Server) storedChatSkill(name string) (*ChatSkill, error) {
	if s.ChatScenarios == nil {
		return nil, errChatScenariosUnavailable
	}
	sk, found := s.ChatScenarios.GetSkill(name)
	if !found {
		return nil, errChatSkillNotFound
	}
	return sk, nil
}

// GetChatSkill returns one skill: the current version when version is 0,
// else that version, which must be the current one or one of the kept ones.
func (s *Server) GetChatSkill(name string, version int) (*chatSkillView, error) {
	sk, err := s.storedChatSkill(name)
	if err != nil {
		return nil, err
	}
	if version == 0 {
		version = sk.Version
	}
	at, kept := sk.at(version)
	if !kept {
		return nil, fmt.Errorf("%w: version %d of %q is not kept", errChatSkillNotFound, version, name)
	}
	return s.chatSkillView(at, true), nil
}

// PutChatSkill validates and stores a skill. Content that is a whole
// SKILL.md is split: its body is the content, and its description is used
// when none is given. created reports whether the name was new.
func (s *Server) PutChatSkill(sk *ChatSkill) (view *chatSkillView, created bool, err error) {
	if s.ChatScenarios == nil {
		return nil, false, errChatScenariosUnavailable
	}
	_, description, body := parseChatSkill(sk.Content)
	if sk.Content = body; sk.Description == "" {
		sk.Description = description
	}
	if err := sk.Normalize(); err != nil {
		return nil, false, err
	}
	if created, err = s.ChatScenarios.PutSkill(sk); err != nil {
		return nil, false, err
	}
	return s.chatSkillView(sk, true), created, nil
}

// DeleteChatSkill removes a skill; errChatSkillInUse, with the scenarios
// that attach it, while one does.
func (s *Server) DeleteChatSkill(name string) (usedBy []string, err error) {
	if s.ChatScenarios == nil {
		return nil, errChatScenariosUnavailable
	}
	return s.ChatScenarios.DeleteSkill(name)
}

// ChatSkillUsedBy names the scenarios that attach a skill.
func (s *Server) ChatSkillUsedBy(name string) ([]string, error) {
	if _, err := s.storedChatSkill(name); err != nil {
		return nil, err
	}
	return s.ChatScenarios.SkillUsedBy(name), nil
}

// chatSkillPreviewOf parses a SKILL.md into a preview.
func (s *Server) chatSkillPreviewOf(raw string, src *ChatSkillSource) *chatSkillPreview {
	name, description, content := parseChatSkill(raw)
	p := &chatSkillPreview{Name: name, Description: description, Content: content, Raw: raw, Source: src,
		Bytes: len(content), Sha256: chatSkillHash(content)}
	if s.ChatScenarios != nil {
		_, p.Exists = s.ChatScenarios.GetSkill(name)
	}
	return p
}

// ImportChatSkill previews a skill without storing it: the SKILL.md at
// rawURL, fetched through the SSRF guard, or the pasted text.
func (s *Server) ImportChatSkill(ctx context.Context, rawURL, text string) (*chatSkillPreview, error) {
	if (rawURL == "") == (text == "") {
		return nil, errors.New("give either url or content")
	}
	var src *ChatSkillSource
	if rawURL != "" {
		var err error
		if text, src, err = fetchChatSkill(ctx, rawURL); err != nil {
			return nil, err
		}
	}
	p := s.chatSkillPreviewOf(text, src)
	if limit := chatSkillMaxBytes(); p.Bytes > limit {
		return nil, fmt.Errorf("content is %d bytes, over the %d-byte limit (MDDB_CHAT_SKILL_MAX_BYTES)", p.Bytes, limit)
	}
	return p, nil
}

// UpdateChatSkill fetches a skill's source again. Without confirm it only
// previews; with it, the fetched text is stored as a new version — unless
// sha256, the hash of the preview the operator read, no longer matches.
func (s *Server) UpdateChatSkill(ctx context.Context, name string, confirm bool, sha256 string) (*chatSkillUpdate, error) {
	sk, err := s.storedChatSkill(name)
	if err != nil {
		return nil, err
	}
	if sk.Source == nil {
		return nil, fmt.Errorf("skill %q was not imported from a URL, so it has no source to update from", name)
	}
	p, err := s.ImportChatSkill(ctx, sk.Source.URL, "")
	if err != nil {
		return nil, err
	}
	next := &ChatSkill{Name: name, Description: p.Description, Content: p.Content, Source: p.Source}
	if next.Description == "" {
		next.Description = sk.Description
	}
	p.Name, p.Description, p.Exists = name, next.Description, true
	out := &chatSkillUpdate{Preview: p, Changed: !sk.sameAs(next)}
	if confirm && out.Changed {
		if sha256 != "" && sha256 != p.Sha256 {
			return nil, errChatSkillStale
		}
		if out.Skill, _, err = s.PutChatSkill(next); err != nil {
			return nil, err
		}
		out.Saved = true
	} else {
		out.Skill = s.chatSkillView(sk, true)
	}
	out.Version = s.ChatScenariosVersion()
	return out, nil
}
