package main

import (
	"regexp"
	"strings"
)

// Redaction of stored chat text (#406). A scenario that logs its turns
// redacts them by default: what a visitor types may hold an e-mail address,
// a pasted API key or a card number, and none of that belongs in a log an
// operator reads. Redaction happens here, in the store, so every writer —
// mddb-chat, a host over REST or gRPC — gets the same rules.
//
// It is a best effort over common shapes, not a guarantee: a secret that
// looks like an ordinary word passes.

// chatRedacted replaces whatever a rule matches.
const chatRedacted = "[redacted]"

// chatRedactRules are applied in order; each replaces its whole match.
var chatRedactRules = []*regexp.Regexp{
	// E-mail addresses.
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`),
	// "Bearer <token>" and "Basic <credentials>" as an Authorization header carries them.
	regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/\-]+=*`),
	// JSON Web Tokens.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]*`),
	// Keys with a well-known prefix: sk-/pk-/rk- (OpenAI, Stripe), GitHub,
	// Slack, AWS access key ids, Google API keys.
	regexp.MustCompile(`\b(?:(?:sk|pk|rk)[-_][A-Za-z0-9_\-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9\-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_\-]{35})`),
	// Card-like runs: 13 to 19 digits, optionally grouped by spaces or dashes.
	regexp.MustCompile(`\b(?:\d[ \-]?){12,18}\d\b`),
}

// chatSecretAssignRe is "api_key=…", "token: …", "password = …": the name
// stays, the value goes.
var chatSecretAssignRe = regexp.MustCompile(`(?i)\b((?:api[_\-]?key|access[_\-]?token|secret|token|password|passwd)\s*[:=]\s*)("[^"]*"|'[^']*'|\S+)`)

// chatLongTokenRe is a long unbroken run of key characters; such a run is
// redacted when it mixes letters and digits, which a word does not.
var chatLongTokenRe = regexp.MustCompile(`[A-Za-z0-9_\-]{32,}`)

// redactChatText replaces e-mail addresses, bearer and API-token-looking
// strings and card-like digit runs with [redacted].
func redactChatText(text string) string {
	if text == "" {
		return text
	}
	text = chatSecretAssignRe.ReplaceAllString(text, "${1}"+chatRedacted)
	for _, re := range chatRedactRules {
		text = re.ReplaceAllString(text, chatRedacted)
	}
	return chatLongTokenRe.ReplaceAllStringFunc(text, func(run string) string {
		if strings.ContainsAny(run, "0123456789") && strings.IndexFunc(run, isASCIILetter) >= 0 {
			return chatRedacted
		}
		return run
	})
}

// isASCIILetter reports whether r is an ASCII letter.
func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

// redactChatMessages redacts the text of messages in place: the content and
// each tool call's query. Result keys are document keys, not visitor text.
func redactChatMessages(msgs []ChatMessage) {
	for i := range msgs {
		msgs[i].Content = redactChatText(msgs[i].Content)
		for j := range msgs[i].ToolCalls {
			msgs[i].ToolCalls[j].Query = redactChatText(msgs[i].ToolCalls[j].Query)
		}
	}
}
