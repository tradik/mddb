package main

import "context"

// Delivering MCP progress notifications (GO-021).
//
// mcp_progress.go has always held a working sender: it formats a
// notifications/progress message and hands it to a writer. What was missing was
// everything around it — nothing extracted the client's progress token from a
// request, nothing gave the sender a way to reach the transport, and no tool
// ever reported anything. A long reindex looked identical to a hung one.
//
// The gap was structural rather than forgotten: MCPHandler.Handle takes a
// request and returns a response, while only the transport knows which session
// a notification belongs to. So the transport supplies the delivery function
// and the handler supplies the token.

// progressContextKey carries a sender through a tool call.
//
// A context value rather than a parameter on all 80 tool signatures: only the
// few operations long enough to be worth reporting need to look for it, and the
// rest are unchanged.
type progressContextKey struct{}

// withProgressSender attaches a sender for the duration of one tool call.
func withProgressSender(ctx context.Context, s *MCPProgressSender) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, progressContextKey{}, s)
}

// ReportProgress reports progress if the caller asked for it.
//
// Silent when the client sent no progress token or the transport cannot deliver
// notifications — a plain POST answers once and has nowhere to put them. A tool
// calls this without caring which is the case.
func ReportProgress(ctx context.Context, progress, total int, message string) {
	sender, ok := ctx.Value(progressContextKey{}).(*MCPProgressSender)
	if !ok || sender == nil {
		return
	}
	sender.Send(progress, total, message)
}

// HandleWithNotifier processes a request, delivering any progress
// notifications the tool emits through notify.
//
// notify may be nil, which is what Handle passes: a transport that cannot
// deliver notifications mid-request gets the same behaviour it always had.
func (h *MCPHandler) HandleWithNotifier(req map[string]interface{}, notify func(map[string]interface{})) map[string]interface{} {
	if notify == nil {
		return h.Handle(req)
	}

	// The token may be nil: the client is then not listening for progress but
	// may still have raised its log level, and the two are separate
	// subscriptions. Send installs nothing without a token, so progress stays
	// silent on its own.
	token := ExtractProgressToken(req)

	h.mu.Lock()
	h.notify = notify
	h.progressToken = token
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		h.notify = nil
		h.progressToken = nil
		h.mu.Unlock()
	}()

	return h.Handle(req)
}

// progressSender builds a sender for the request in flight, or nil.
func (h *MCPHandler) progressSender() *MCPProgressSender {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.notify == nil || h.progressToken == nil {
		return nil
	}
	return NewCallbackProgressSender(h.progressToken, h.notify)
}

// Delivering MCP log messages (GO-021).
//
// logging/setLevel was accepted, validated and stored, and then nothing ever
// consulted it: MCPLogMessage and mcpShouldLog sat unused beside it. A client
// could set the level to debug and receive exactly as much as one that set it
// to emergency — nothing.
//
// Log notifications travel the same way progress does, so they share the
// transport's delivery function.

// logToClient sends a log notification if the client asked for that level.
//
// Silent when the level is below the client's threshold or the transport cannot
// deliver notifications. The server's own slog output is unaffected: this is
// the copy the client sees, not a replacement for the operator's log.
func (h *MCPHandler) logToClient(level MCPLogLevel, logger, message string) {
	h.mu.Lock()
	notify, min := h.notify, h.logLevel
	h.mu.Unlock()

	if notify == nil || !mcpShouldLog(level, min) {
		return
	}
	notify(MCPLogMessage(level, logger, message))
}
