package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"
)

// The global settings on every group (GO-052). The meta group's leader is
// their source of truth: a write to one is routed there. The leader of
// every other group copies them every few seconds — the whole set, which is
// small — and applies what differs through its own log, so its followers
// have them too. A copy rather than a log tail, because not every manager
// logs its writes; a key the meta group no longer has is removed.

// globalSyncTimeout bounds one copy.
const globalSyncTimeout = 30 * time.Second

// syncGlobal brings this group's global settings to the meta group's.
func (p *placementRuntime) syncGlobal() error {
	ctx, cancel := context.WithTimeout(context.Background(), globalSyncTimeout)
	defer cancel()
	remote, err := p.fetchGlobal(ctx)
	if err != nil {
		return err
	}
	local := map[string][]byte{}
	_ = scanBuckets(p.s.DB, globalBuckets, keepAll, func(b string, k, v []byte) error {
		local[b+"\x00"+string(k)] = append([]byte(nil), v...)
		return nil
	})
	var diff []*binlog.BinlogEntry
	for _, e := range remote {
		id := e.BucketName + "\x00" + string(e.Key)
		if v, ok := local[id]; !ok || !bytes.Equal(v, e.Value) {
			diff = append(diff, e)
		}
		delete(local, id)
	}
	for id := range local {
		b, k, _ := strings.Cut(id, "\x00")
		diff = append(diff, &binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: b, Key: []byte(k)})
	}
	if len(diff) == 0 {
		return nil
	}
	if err := logEntries(p.s.DBUpdate, p.s.Binlog, diff); err != nil {
		return err
	}
	p.reloadGlobal(diff)
	slog.Info("placement: global settings copied from the meta group", "changed", len(diff))
	return nil
}

// fetchGlobal reads the meta leader's whole copy of the global settings.
func (p *placementRuntime) fetchGlobal(ctx context.Context) ([]*binlog.BinlogEntry, error) {
	meta, err := p.leaderClient(p.cfg.meta)
	if err != nil {
		return nil, err
	}
	stream, err := meta.ExportKeys(withReplicationSecret(ctx), &proto.ExportKeysRequest{Global: true})
	if err != nil {
		return nil, err
	}
	var out []*binlog.BinlogEntry
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the meta leader's copy of the global settings ended early")
		}
		if err != nil {
			return nil, err
		}
		for _, e := range chunk.Entries {
			out = append(out, protoToEntry(e))
		}
		if chunk.SnapshotDone {
			return out, nil
		}
	}
}

// reloadGlobal has the managers that cache global settings read them again.
func (p *placementRuntime) reloadGlobal(changed []*binlog.BinlogEntry) {
	s := p.s
	ra := NewReplicationApplier(s)
	done := map[string]bool{}
	for _, e := range changed {
		if done[e.BucketName] {
			continue
		}
		done[e.BucketName] = true
		ra.updateInMemoryState(&binlog.BinlogEntry{BucketName: e.BucketName})
		switch {
		case strings.HasPrefix(e.BucketName, "auth_") && s.AuthManager != nil:
			_ = s.AuthManager.LoadAll()
		case e.BucketName == "tenants" && s.Tenants != nil:
			_ = s.Tenants.LoadAll()
		}
	}
}
