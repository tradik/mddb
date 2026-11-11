package main

import (
	"context"
	"errors"
)

// Chat MCP secrets through the direct client (MCP-005): the functions the
// REST handlers and the gRPC methods call.

// ChatMcpSecretList returns the secrets without values, and whether a
// secret can be put (an at-rest key is loaded).
func (c *DirectClient) ChatMcpSecretList(_ context.Context) (*chatMcpSecretListing, error) {
	return c.server.ListChatMcpSecrets(), nil
}

// ChatMcpSecretPut sets or replaces a secret; true when it is new.
func (c *DirectClient) ChatMcpSecretPut(_ context.Context, name, header, value string) (bool, error) {
	return c.server.PutChatMcpSecret(name, header, value)
}

// ChatMcpSecretDelete removes a secret; while scenarios name it, the error
// names them.
func (c *DirectClient) ChatMcpSecretDelete(_ context.Context, name string) error {
	usedBy, err := c.server.DeleteChatMcpSecret(name)
	if errors.Is(err, errChatMcpSecretInUse) {
		return errors.Join(err, errors.New(chatMcpSecretInUseMessage(name, usedBy)))
	}
	return err
}
