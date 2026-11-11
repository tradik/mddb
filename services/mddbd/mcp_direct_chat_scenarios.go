package main

import "context"

// Chat scenarios through the direct client (#403): the functions the REST
// handlers and the gRPC methods call, so every surface reads and writes alike.

// ChatScenarioList returns the scenarios the caller may read and the store's
// version counter.
func (c *DirectClient) ChatScenarioList(ctx context.Context) ([]*ChatScenario, uint64, error) {
	return c.server.ListChatScenarios(ctx), c.server.ChatScenariosVersion(), nil
}

// ChatScenarioGet returns one scenario and its composed system prompt, with
// the prompt's size and the skills it names that do not exist (#404).
func (c *DirectClient) ChatScenarioGet(ctx context.Context, name string) (*ChatScenario, *chatScenarioPrompt, error) {
	sc, err := c.server.GetChatScenario(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	p := c.server.ChatScenarioPrompt(sc)
	return sc, &p, nil
}

// ChatScenarioPut creates or replaces a scenario; true when it is new.
func (c *DirectClient) ChatScenarioPut(ctx context.Context, sc *ChatScenario) (bool, error) {
	return c.server.PutChatScenario(ctx, sc)
}

// ChatScenarioDelete removes a scenario.
func (c *DirectClient) ChatScenarioDelete(_ context.Context, name string) error {
	return c.server.DeleteChatScenario(name)
}
