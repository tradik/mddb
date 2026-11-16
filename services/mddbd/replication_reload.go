package main

import (
	"log/slog"

	"mddb/internal/binlog"
	"mddb/internal/geo"
)

// A follower writes every replicated bucket from the log, but a manager
// that keeps its bucket in memory answers from what it loaded: the tenants
// (GO-054), curation rules, synonyms and stop words were read once at start,
// so a quota or a rule set on the leader reached the follower's file and
// none of its answers until a restart; the geo indexes likewise. refresh
// reloads each manager whose bucket an applied batch touched, once per batch.

// refresh brings the in-memory state up to entries, just applied: the vector
// and geo indexes and the document caches entry by entry, then each manager
// whose bucket the entries touch, once.
func (ra *ReplicationApplier) refresh(entries []*binlog.BinlogEntry) {
	reloads := ra.server.replicatedManagerReloads()
	var due []string
	seen := map[string]bool{}
	for _, entry := range entries {
		switch name := entry.BucketName; name {
		case "vectors":
			ra.applyVector(entry)
		case "docs":
			ra.invalidateDocCache(entry)
		case "geo":
			ra.applyGeo(entry)
		default:
			if reloads[name] != nil && !seen[name] {
				seen[name] = true
				due = append(due, name)
			}
		}
	}
	for _, name := range due {
		if err := reloads[name](); err != nil {
			slog.Warn("Replication applier could not reload a manager", "bucket", name, "err", err)
		}
	}
}

// replicatedManagerReloads maps each replicated bucket a manager keeps in
// memory to what reloads that manager. A manager the server does not run
// reloads as nothing.
func (s *Server) replicatedManagerReloads() map[string]func() error {
	chat := func() error { return reloadIf(s.ChatScenarios != nil, s.ChatScenarios.LoadAll) } // #403, #404, MCP-005
	return map[string]func() error{
		"webhooks": func() error { return reloadIf(s.WebhookManager != nil, s.WebhookManager.LoadAll) },
		"schemas":  func() error { return reloadIf(s.SchemaManager != nil, s.SchemaManager.LoadAll) },
		"automation": func() error {
			if s.CronScheduler != nil {
				defer s.CronScheduler.Reload()
			}
			return reloadIf(s.AutomationManager != nil, s.AutomationManager.LoadAll)
		},
		"aliases":          func() error { return reloadIf(s.Aliases != nil, s.Aliases.LoadAll) },
		"features":         func() error { return reloadIf(s.Features != nil, s.Features.LoadAll) }, // GO-049
		"chat_scenarios":   chat,
		"chat_skills":      chat,
		"chat_mcp_secrets": chat,
		"colmeta":          func() error { return reloadIf(s.CollectionManager != nil, s.CollectionManager.LoadAll) },
		"tenants":          func() error { return reloadIf(s.Tenants != nil, s.Tenants.LoadAll) }, // GO-054
		"curation":         func() error { return reloadIf(s.CurationManager != nil, s.CurationManager.LoadAll) },
		"synonyms":         func() error { return reloadIf(s.SynonymManager != nil, s.SynonymManager.LoadAll) },
		"stopwords":        func() error { return reloadIf(s.StopWordManager != nil, s.StopWordManager.LoadAll) },
	}
}

// applyGeo updates the follower's geo indexes with one replicated point.
func (ra *ReplicationApplier) applyGeo(entry *binlog.BinlogEntry) {
	var indexes []geo.PointIndex
	if ra.server.GeoIndex != nil {
		indexes = append(indexes, ra.server.GeoIndex)
	}
	if ra.server.GeoHashIndex != nil {
		indexes = append(indexes, ra.server.GeoHashIndex)
	}
	geo.ApplyReplicated(entry.Type == binlog.BinlogPut, entry.Key, entry.Value, indexes...)
}
