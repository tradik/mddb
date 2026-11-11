package main

import "context"

// The conversation log through the direct client (#406): the functions the
// REST handlers call. Both are global admin; the MCP gate checks that for a
// signed-in caller (access_rules_mcp.go), and an MCP API key reaches them
// only through the admin toolset.

// ChatLogList is one page of the conversation log.
func (c *DirectClient) ChatLogList(ctx context.Context, f ChatLogFilter) (*ChatLogPage, error) {
	page, err := c.server.chatLog(ctx, f)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// ChatGaps is the docs gaps.
func (c *DirectClient) ChatGaps(ctx context.Context, f ChatGapsFilter) (*ChatGapsResponse, error) {
	res, err := c.server.chatGapsOf(ctx, f)
	if err != nil {
		return nil, err
	}
	return &res, nil
}
