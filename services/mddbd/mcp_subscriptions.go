package main

// subscriptions/listen — the replacement MCP 2026-07-28 gives for the GET
// stream and resources/subscribe (MCP-001).
//
// A client opens one long-lived request and names the notification types it
// wants. The server acknowledges with the subset it will actually honor, then
// the response stream stays open and carries those notifications until either
// side closes it.
//
// MDDB honors nothing, and says so. The tool table, the prompts and the
// resource descriptors are all built once at startup: there is no moment in
// the life of the process at which a list changes, and no resource whose
// content MDDB is watching. An acknowledgement listing types we cannot deliver
// would be a promise, so the honored set is empty and a client is left relying
// on the ttlMs each list already carries. When something here does become
// observable — a custom-tool reload, say — this is the file that grows a
// producer, and the protocol contract does not change.

// mcpNotificationFilter is the set of notification types one subscription
// asked for.
type mcpNotificationFilter struct {
	ToolsListChanged      bool
	PromptsListChanged    bool
	ResourcesListChanged  bool
	ResourceSubscriptions []string
}

// mcpParseNotificationFilter reads the filter out of a listen request,
// tolerating every field's absence: all of them are optional and an omitted
// field means "not subscribed".
func mcpParseNotificationFilter(req map[string]interface{}) mcpNotificationFilter {
	notifications, _ := mcpParams(req)["notifications"].(map[string]interface{})
	f := mcpNotificationFilter{}
	f.ToolsListChanged, _ = notifications["toolsListChanged"].(bool)
	f.PromptsListChanged, _ = notifications["promptsListChanged"].(bool)
	f.ResourcesListChanged, _ = notifications["resourcesListChanged"].(bool)
	if uris, ok := notifications["resourceSubscriptions"].([]interface{}); ok {
		for _, u := range uris {
			if s, ok := u.(string); ok {
				f.ResourceSubscriptions = append(f.ResourceSubscriptions, s)
			}
		}
	}
	return f
}

// mcpHonoredNotifications returns the subset of a filter MDDB will deliver.
//
// It takes the filter it cannot honor so that the day a producer exists, the
// argument is already here and only this body changes.
func mcpHonoredNotifications(_ mcpNotificationFilter) map[string]interface{} {
	return map[string]interface{}{}
}

// mcpSubscriptionAck builds the notifications/subscriptions/acknowledged
// message that must be the first thing on a subscription's stream.
//
// The subscription's id is the JSON-RPC id of the listen request, and it is
// repeated on every message the stream carries — including this one — because
// on stdio all subscriptions share one channel and the client has nothing else
// to demultiplex them with.
func mcpSubscriptionAck(id interface{}, honored map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/subscriptions/acknowledged",
		"params": map[string]interface{}{
			"_meta": map[string]interface{}{
				metaSubscriptionID: id,
			},
			"notifications": honored,
		},
	}
}

// handleSubscriptionsListen builds the result that closes a subscription.
//
// A subscription ends either because the client closed the stream or because
// the server tore it down, and the second case is the one the spec asks to be
// signalled: a result on the original request says "this ended cleanly",
// distinguishing it from a dropped connection. Since MDDB honors no
// notification type, every subscription it accepts ends the moment it is
// acknowledged, and this is that result.
func (h *MCPHandler) handleSubscriptionsListen(req map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"_meta": map[string]interface{}{
			metaSubscriptionID: req["id"],
		},
	}
}
