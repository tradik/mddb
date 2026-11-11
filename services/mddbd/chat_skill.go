package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"mddb/internal/envconf"
)

// Chat skills (#404): reusable Markdown instruction blocks in the SKILL.md
// format — front matter with a name and a description, then the
// instructions — that scenarios attach by name, in a chosen order.
//
// A skill is operator-trusted text: it becomes part of a system prompt. So
// every surface is admin only, an import shows the whole text before
// anything is saved, and a skill imported from a URL is pinned to the
// content fetched; it is fetched again only when an operator asks.

// Limits, and how many earlier versions of a skill are kept for a diff.
const (
	chatSkillMaxBytesDefault  = 32768
	chatPromptMaxBytesDefault = 131072
	chatSkillHistoryLen       = 10
)

// ChatSkillSource is where an imported skill came from.
type ChatSkillSource struct {
	URL       string    `json:"url"`
	Commit    string    `json:"commit,omitempty"` // the Git commit, for a GitHub URL
	FetchedAt time.Time `json:"fetchedAt"`
}

// ChatSkillRevision is an earlier version of a skill. Content is left out
// of the listings, where Bytes says how long it was.
type ChatSkillRevision struct {
	Version     int              `json:"version"`
	Description string           `json:"description,omitempty"`
	Content     string           `json:"content,omitempty"`
	Bytes       int              `json:"bytes"`
	Source      *ChatSkillSource `json:"source,omitempty"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

// ChatSkill is one skill as stored. Version grows by one on every change;
// History holds the versions before it, newest first.
type ChatSkill struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Content     string              `json:"content,omitempty"`
	Source      *ChatSkillSource    `json:"source,omitempty"`
	Version     int                 `json:"version"`
	History     []ChatSkillRevision `json:"history,omitempty"`
	CreatedAt   time.Time           `json:"createdAt"`
	UpdatedAt   time.Time           `json:"updatedAt"`
}

// Errors of the skill store.
var (
	errChatSkillNotFound = errors.New("chat skill not found")
	errChatSkillInUse    = errors.New("chat skill is in use")
	errChatSkillStale    = errors.New("the source changed since the preview")
)

// chatSkillMaxBytes is MDDB_CHAT_SKILL_MAX_BYTES: the longest skill content.
func chatSkillMaxBytes() int {
	return chatByteLimit("MDDB_CHAT_SKILL_MAX_BYTES", chatSkillMaxBytesDefault)
}

// chatPromptMaxBytes is MDDB_CHAT_PROMPT_MAX_BYTES: the longest composed
// system prompt a scenario may have.
func chatPromptMaxBytes() int {
	return chatByteLimit("MDDB_CHAT_PROMPT_MAX_BYTES", chatPromptMaxBytesDefault)
}

// chatByteLimit reads a byte limit, keeping the default for a value below 1
// or above 1 GiB.
func chatByteLimit(key string, def int64) int {
	n := envconf.Int64(key, def)
	if n < 1 || n > 1<<30 {
		n = def
	}
	return int(n)
}

// Normalize trims a skill and refuses one that cannot be stored.
func (k *ChatSkill) Normalize() error {
	if err := validChatKeyName("skill", k.Name, "version", "import"); err != nil {
		return err
	}
	k.Description, k.Content = strings.TrimSpace(k.Description), strings.TrimSpace(k.Content)
	if err := validChatText("description", k.Description); err != nil {
		return err
	}
	switch n := len(k.Content); {
	case n == 0:
		return errors.New("content is required")
	case n > chatSkillMaxBytes():
		return fmt.Errorf("content is %d bytes, over the %d-byte limit (MDDB_CHAT_SKILL_MAX_BYTES)", n, chatSkillMaxBytes())
	case !utf8.ValidString(k.Content):
		return errors.New("content is not valid UTF-8")
	}
	if k.Source != nil {
		if !strings.HasPrefix(k.Source.URL, "https://") {
			return errors.New("source.url must be an https URL")
		}
		if k.Source.FetchedAt.IsZero() {
			return errors.New("source.fetchedAt is required")
		}
	}
	return nil
}

// sameAs reports whether two versions hold the same text and source, so a
// put that changes nothing does not make a version.
func (k *ChatSkill) sameAs(o *ChatSkill) bool {
	if k.Description != o.Description || k.Content != o.Content || (k.Source == nil) != (o.Source == nil) {
		return false
	}
	return k.Source == nil || (k.Source.URL == o.Source.URL && k.Source.Commit == o.Source.Commit)
}

// revision is this version as an entry of the history.
func (k *ChatSkill) revision() ChatSkillRevision {
	return ChatSkillRevision{Version: k.Version, Description: k.Description, Content: k.Content, Bytes: len(k.Content),
		Source: k.Source, UpdatedAt: k.UpdatedAt}
}

// at returns the skill as it was at version v: the current one, or one
// rebuilt from the history, without a history of its own.
func (k *ChatSkill) at(v int) (*ChatSkill, bool) {
	if v == k.Version {
		return k, true
	}
	for _, r := range k.History {
		if r.Version == v {
			return &ChatSkill{Name: k.Name, Description: r.Description, Content: r.Content, Source: r.Source,
				Version: r.Version, CreatedAt: k.CreatedAt, UpdatedAt: r.UpdatedAt}, true
		}
	}
	return nil, false
}

// chatSkillHash is the sha256 of a skill's content, which a confirmed
// update names so that what is saved is what was previewed.
func chatSkillHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// parseChatSkill reads a SKILL.md: name and description from its front
// matter, read by the server's parser (frontmatter.go), and the
// instructions from the body. Text without front matter is all instructions.
func parseChatSkill(text string) (name, description, content string) {
	meta, body := parseFrontmatter(text)
	name = strings.Join(meta["name"], ", ")
	description = strings.Join(meta["description"], ", ")
	if strings.Trim(description, "|>+-") == "" && description != "" {
		// `description: |` — a YAML block scalar, which the flat reader
		// leaves as its marker: the indented lines under it are the text.
		block, _, _ := splitFrontmatter(strings.TrimSpace(text))
		description = frontmatterBlockText(block, "description", strings.HasPrefix(description, ">"))
	}
	return name, description, body
}

// frontmatterBlockText returns the indented lines under `key:` in a front
// matter block, joined by spaces when folded and by newlines otherwise.
func frontmatterBlockText(block, key string, folded bool) string {
	var lines []string
	open := false
	for _, raw := range strings.Split(block, "\n") {
		indented := strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")
		switch {
		case open && (indented || strings.TrimSpace(raw) == ""):
			lines = append(lines, strings.TrimSpace(raw))
		case open:
			open = false
		case strings.HasPrefix(raw, key+":"):
			open = true
		}
	}
	sep := "\n"
	if folded {
		sep = " "
	}
	return strings.TrimSpace(strings.Join(lines, sep))
}
