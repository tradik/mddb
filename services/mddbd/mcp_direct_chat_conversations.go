package main

import "context"

// Chat conversations through the direct client (#405): the functions the
// REST handlers call, after the same read permission on the chat collection
// of the caller's tenant. A call on an MCP API key carries no claims and is
// scoped by the key's toolsets instead (authorizeMCP).

// chatReadAccess admits a read of the caller's chat collection.
func (c *DirectClient) chatReadAccess(ctx context.Context) error {
	return c.server.authorizeMCP(ctx, chatRule(false), []string{chatCollection(ctx)})
}

// ChatConversationList is one page of a user's conversations, newest first.
func (c *DirectClient) ChatConversationList(ctx context.Context, sub, scenario string, limit int, cursor string) (*ChatListResponse, error) {
	if err := c.chatReadAccess(ctx); err != nil {
		return nil, err
	}
	page, err := c.server.chatList(ctx, sub, scenario, limit, cursor)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// ChatConversationGet is one conversation of a user with its last limit
// messages.
func (c *DirectClient) ChatConversationGet(ctx context.Context, id, sub string, limit int) (*ChatConversation, error) {
	if err := c.chatReadAccess(ctx); err != nil {
		return nil, err
	}
	return c.server.chatGet(ctx, id, sub, limit)
}
