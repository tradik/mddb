package main

import (
	"context"
	"errors"
)

// Chat skills through the direct client (#404): the functions the REST
// handlers and the gRPC methods call.

// ChatSkillList returns the skills, without contents, and the chat
// scenarios' version counter.
func (c *DirectClient) ChatSkillList(_ context.Context) ([]*chatSkillView, uint64, error) {
	return c.server.ListChatSkills(), c.server.ChatScenariosVersion(), nil
}

// ChatSkillGet returns one skill; version 0 is the current one.
func (c *DirectClient) ChatSkillGet(_ context.Context, name string, version int) (*chatSkillView, error) {
	return c.server.GetChatSkill(name, version)
}

// ChatSkillPut creates a skill or stores a new version; true when it is new.
func (c *DirectClient) ChatSkillPut(_ context.Context, sk *ChatSkill) (*chatSkillView, bool, error) {
	return c.server.PutChatSkill(sk)
}

// ChatSkillDelete removes a skill; while scenarios attach it, the error
// names them.
func (c *DirectClient) ChatSkillDelete(_ context.Context, name string) error {
	usedBy, err := c.server.DeleteChatSkill(name)
	if errors.Is(err, errChatSkillInUse) {
		return errors.Join(err, errors.New(chatSkillInUseMessage(name, usedBy)))
	}
	return err
}
