package main

import "testing"

func TestRedactChatText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain text stays", "How do I configure replication?", "How do I configure replication?"},
		{"email", "mail me at jan.kowalski+test@example.co.uk please", "mail me at [redacted] please"},
		{"two emails", "a@b.io, c.d@e-f.org", "[redacted], [redacted]"},
		{"bearer", "Authorization: Bearer abc.DEF-123_xyz==", "Authorization: [redacted]"},
		{"basic", "basic dXNlcjpwYXNz", "[redacted]"},
		{"jwt", "here eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_part", "here [redacted]"},
		{"openai key", "my key sk-proj-abcdefghijklmnop1234", "my key [redacted]"},
		{"stripe key", "pk_live_abcdefghijklmnop", "[redacted]"},
		{"github token", "ghp_abcdefghijklmnopqrstuvwxyz0123", "[redacted]"},
		{"github pat", "github_pat_11ABCDEFGHIJKLMNOPQRST_xyz", "[redacted]"},
		{"slack", "xoxb-1234567890-abcdef", "[redacted]"},
		{"aws", "AKIAABCDEFGHIJKLMNOP is mine", "[redacted] is mine"},
		{"google", "AIza" + "SyA1234567890abcdefghijklmnopqrstuv", "[redacted]"},
		{"card spaced", "card 4111 1111 1111 1111 exp", "card [redacted] exp"},
		{"card dashed", "4111-1111-1111-1111", "[redacted]"},
		{"card plain", "5500000000000004", "[redacted]"},
		{"short numbers stay", "version 2190 and port 11023, phone 555-1234", "version 2190 and port 11023, phone 555-1234"},
		{"assignment", "api_key=hunter2 and password: 'p a s s'", "api_key=[redacted] and password: [redacted]"},
		{"assignment quoted", `token = "abc def"`, "token = [redacted]"},
		{"long mixed run", "id 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822c", "id [redacted]"},
		{"long word stays", "pneumonoultramicroscopicsilicovolcanoconiosis", "pneumonoultramicroscopicsilicovolcanoconiosis"},
		{"long digit run is not a card", "1234567890123456789012345678901234567", "1234567890123456789012345678901234567"},
		{"unicode around", "Zażółć a@b.pl gęślą", "Zażółć [redacted] gęślą"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactChatText(c.in); got != c.want {
				t.Fatalf("redactChatText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactChatMessages(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "I am a@b.io"},
		{Role: "assistant", Content: "ok", ToolCalls: []ChatToolCall{{Name: "search_docs", Query: "a@b.io", ResultKeys: []string{"a@b.io-key"}}}}}
	redactChatMessages(msgs)
	if msgs[0].Content != "I am [redacted]" || msgs[1].ToolCalls[0].Query != "[redacted]" {
		t.Fatalf("not redacted: %+v", msgs)
	}
	if msgs[1].ToolCalls[0].ResultKeys[0] != "a@b.io-key" {
		t.Fatal("result keys are document keys and stay")
	}
}
