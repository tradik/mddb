package main

import (
	"encoding/json"
	"strings"

	"mddb/internal/binlog"
)

// Which keys belong to a collection, and which are global (GO-052).
//
// A collection's data is its file. Its settings are in the main file, under
// keys that name it: they move with it, so the group holding a collection
// holds everything that shapes its answers. Global settings — users, keys,
// tenants and the rest of globalBuckets — are the meta group's, and every
// other group's leader keeps a copy. Everything else in the main file is the
// node's own (logs, jobs, statistics) and is never copied.

// settingsBuckets are the main-file buckets holding per-collection settings.
var settingsBuckets = map[string]bool{
	"colmeta": true, "schemas": true, "synonyms": true, "stopwords": true,
	"spelldicts": true, "curation": true,
}

// globalBuckets are the meta group's: copied to every other group.
var globalBuckets = []string{
	"auth_users", "auth_permissions", "auth_apikeys", "auth_groups", "auth_group_perms",
	mcpAPIKeyBucket, "tenants", "features", "aliases", "webhooks", "automation",
	"embedding_configs", "chat_scenarios", "chat_skills", "chat_mcp_secrets",
}

// settingsCollection is the collection a settings key belongs to.
func settingsCollection(bucket string, key, value []byte) (string, bool) {
	k := string(key)
	switch bucket {
	case "colmeta":
		return k, k != ""
	case "schemas":
		return cutField(strings.CutPrefix(k, "schema|"))
	case "synonyms":
		return cutField(strings.CutPrefix(k, "syn|"))
	case "stopwords":
		return cutField(strings.CutPrefix(k, "sw|"))
	case "spelldicts":
		return cutField(strings.CutPrefix(k, "col|"))
	case "curation":
		var rule struct {
			Collection string `json:"collection"`
		}
		_ = json.Unmarshal(value, &rule)
		return rule.Collection, rule.Collection != ""
	}
	return "", false
}

// cutField is the first "|"-separated field of rest, when ok.
func cutField(rest string, ok bool) (string, bool) {
	if !ok {
		return "", false
	}
	c, _, _ := strings.Cut(rest, "|")
	return c, c != ""
}

// entryCollection is the collection a log entry writes to, "" when it
// writes to none (a global or a node's own key).
func entryCollection(e *binlog.BinlogEntry) string {
	if isCollectionBucket([]byte(e.BucketName)) {
		c, _ := keyCollection(e.BucketName, e.Key)
		return c
	}
	if settingsBuckets[e.BucketName] {
		c, _ := settingsCollection(e.BucketName, e.Key, e.Value)
		return c
	}
	return ""
}

// entryGate is the log's entry gate on a node of a placement: a write to a
// collection is taken only by the group holding it, and not while a move
// switches it over. A copy being imported or dropped is the node's own
// work and passes.
func (p *placementRuntime) entryGate(entries []*binlog.BinlogEntry) error {
	seen := ""
	for _, e := range entries {
		c := entryCollection(e)
		if c == "" || c == seen {
			continue
		}
		seen = c
		if err := p.admitsWrite(c); err != nil {
			return err
		}
	}
	return nil
}

// admitsWrite is nil when this node's group may write collection c now.
func (p *placementRuntime) admitsWrite(c string) error {
	if p.local.passes(c) {
		return nil
	}
	if p.local.isGated(c) {
		return &collectionElsewhereError{Collection: c}
	}
	group, known := p.owner(c)
	if !known {
		return &collectionElsewhereError{Collection: c}
	}
	if group != p.cfg.self {
		return &collectionElsewhereError{Collection: c, Group: group, LeaderHTTP: p.knownLeaderHTTP(group)}
	}
	return nil
}
