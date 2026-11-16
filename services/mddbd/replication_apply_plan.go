package main

import "mddb/internal/binlog"

// How a follower splits a batch into transactions (GO-050 follow-up).
//
// Every commit is a sync to disk, so a batch is applied in one transaction
// per database file it touches — one in the single-file layout. In the
// per-collection layout a document's entries go to its collection's file and
// its full-text entries to the main file, interleaved; applying them run by
// run cost a transaction per run, which is most of the cost the batch was
// meant to remove. Entries for different files touch different keys, so
// each file's entries are applied together, in their own order.
//
// The position written with each file's transaction is the highest LSN
// through which every entry is in a committed file: one less than the first
// entry of the next file still to commit, and the batch's last LSN for the
// last file. A crash between two files' commits resumes from there and
// applies again some entries the first file already holds — in order, onto a
// file that holds them, which leaves it as it was. A collection bucket
// deleted outright goes to every file and is applied on its own, between
// the entries before it and those after it.

// applyGroup is what one transaction (or, for a bucket deleted everywhere,
// one pass over the files) applies.
type applyGroup struct {
	target     string // collection whose file it is; "" for the main file
	everywhere bool
	entries    []*binlog.BinlogEntry
	through    uint64 // the position its transaction records
}

// plan splits entries into groups in the order they are applied.
func (ra *ReplicationApplier) plan(entries []*binlog.BinlogEntry) []applyGroup {
	var out []applyGroup
	for start := 0; start < len(entries); {
		if _, all := ra.target(entries[start]); all {
			out = append(out, applyGroup{everywhere: true, entries: entries[start : start+1], through: entries[start].LSN})
			start++
			continue
		}
		end := start + 1
		for end < len(entries) && !ra.everywhere(entries[end]) {
			end++
		}
		out = append(out, ra.byFile(entries[start:end])...)
		start = end
	}
	return out
}

func (ra *ReplicationApplier) everywhere(e *binlog.BinlogEntry) bool {
	_, all := ra.target(e)
	return all
}

// byFile groups a run without everywhere-entries by file, files in the order
// of their first entry, and sets each group's position.
func (ra *ReplicationApplier) byFile(run []*binlog.BinlogEntry) []applyGroup {
	var groups []applyGroup
	index := map[string]int{}
	for _, e := range run {
		t, _ := ra.target(e)
		i, seen := index[t]
		if !seen {
			i = len(groups)
			index[t] = i
			groups = append(groups, applyGroup{target: t})
		}
		groups[i].entries = append(groups[i].entries, e)
	}
	for i := range groups {
		groups[i].through = run[len(run)-1].LSN
		if i+1 < len(groups) {
			groups[i].through = groups[i+1].entries[0].LSN - 1
		}
	}
	return groups
}
