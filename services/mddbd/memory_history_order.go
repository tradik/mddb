package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"mddb/internal/storage"
)

// Chronological order of a session's messages.
//
// addedAt has whole-second precision, so two messages written within one
// second tied on it and came back in the order of their random ids. The key
// carries the creation time instead — in nanoseconds since 2.19.0, in seconds
// before — and the id breaks what is left of a tie, so the order the history
// documents holds on every call.

// secondsKeyLimit separates a key timestamp in seconds (written before
// 2.19.0) from one in nanoseconds: 10^12 seconds is beyond year 33000, and
// 10^12 nanoseconds is 1970.
const secondsKeyLimit = 1_000_000_000_000

// memoryMessageKey is a message's key: the session, then the creation time in
// nanoseconds zero-padded to sort as text, then the message id.
func memoryMessageKey(sessionID string, created time.Time, msgID string) string {
	return fmt.Sprintf("%s/%020d-%s", sessionID, created.UnixNano(), msgID)
}

// messageCreatedNanos is when a message was created, from its key; a key
// without a timestamp falls back to addedAt.
func messageCreatedNanos(d storage.Doc) int64 {
	tail := d.Key[strings.LastIndexByte(d.Key, '/')+1:]
	stamp, _, _ := strings.Cut(tail, "-")
	n, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return d.AddedAt * int64(time.Second)
	}
	if n < secondsKeyLimit {
		return n * int64(time.Second)
	}
	return n
}

// sortMessagesChronologically orders messages by creation time, then id.
func sortMessagesChronologically(messages []storage.Doc) {
	created := make(map[string]int64, len(messages))
	for _, m := range messages {
		created[m.ID] = messageCreatedNanos(m)
	}
	sort.SliceStable(messages, func(i, j int) bool {
		a, b := created[messages[i].ID], created[messages[j].ID]
		if a != b {
			return a < b
		}
		return messages[i].ID < messages[j].ID
	})
}
