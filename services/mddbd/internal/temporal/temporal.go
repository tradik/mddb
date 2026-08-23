package temporal

import (
	"encoding/binary"
	"fmt"
	"math"
	"mddb/internal/binlog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// TemporalEventType classifies document lifecycle events.
type TemporalEventType string

const (
	EventCreate TemporalEventType = "create"
	EventUpdate TemporalEventType = "update"
	EventAccess TemporalEventType = "access"
)

var (
	bucketTemporal    = []byte("temporal")
	bucketTemporalHot = []byte("temporal_hot")
)

// TemporalEvent is a single recorded lifecycle event.
type TemporalEvent struct {
	DocID     string            `json:"docId"`
	EventType TemporalEventType `json:"eventType"`
	Timestamp int64             `json:"timestamp"`
	Actor     string            `json:"actor,omitempty"`
}

// temporalJob is an async event write task.
type temporalJob struct {
	collection string
	docID      string
	eventType  TemporalEventType
	actor      string
	ts         int64  // UnixNano
	seq        uint64 // monotonic sequence for tie-breaking identical nanoseconds
}

// TemporalManager tracks document lifecycle events and hot-doc leaderboards.
type TemporalManager struct {
	db     *bolt.DB
	ch     chan temporalJob
	done   chan struct{}
	once   sync.Once
	seq    atomic.Uint64
	binlog *binlog.Binlog
}

// NewTemporalManager creates a TemporalManager backed by the given BoltDB.
func NewTemporalManager(db *bolt.DB) *TemporalManager {
	return &TemporalManager{
		db:   db,
		ch:   make(chan temporalJob, 2000),
		done: make(chan struct{}),
	}
}

// SetBinlog attaches a binlog for replication.
func (tm *TemporalManager) SetBinlog(bl *binlog.Binlog) {
	tm.binlog = bl
}

// EnsureBuckets creates the required BoltDB buckets.
func (tm *TemporalManager) EnsureBuckets() error {
	return tm.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketTemporal)
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists(bucketTemporalHot)
		return err
	})
}

// Start begins the background event writer goroutine.
func (tm *TemporalManager) Start() {
	tm.once.Do(func() {
		go tm.run()
	})
}

// Stop signals the background goroutine to exit.
func (tm *TemporalManager) Stop() {
	close(tm.done)
}

// RecordAsync enqueues an event for async persistence. Drops silently if the
// channel is full — this is acceptable for analytics.
func (tm *TemporalManager) RecordAsync(collection, docID string, et TemporalEventType, actor string) {
	select {
	case tm.ch <- temporalJob{
		collection: collection,
		docID:      docID,
		eventType:  et,
		actor:      actor,
		ts:         time.Now().UnixNano(),
		seq:        tm.seq.Add(1),
	}:
	default:
		// channel full — drop
	}
}

// run drains the job channel and persists events using db.Batch for efficiency.
func (tm *TemporalManager) run() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var pending []temporalJob
	flush := func() {
		if len(pending) == 0 {
			return
		}
		jobs := pending
		pending = nil
		_ = tm.db.Batch(func(tx *bolt.Tx) error {
			bEvt := tx.Bucket(bucketTemporal)
			bHot := tx.Bucket(bucketTemporalHot)
			if bEvt == nil || bHot == nil {
				return nil
			}
			for _, j := range jobs {
				// Key: nanoseconds + monotonic seq suffix to guarantee uniqueness.
				// Public Timestamp is in Unix seconds for readability.
				evtKey := fmt.Sprintf("evt|%s|%s|%s|%020d-%016d", j.collection, j.docID, string(j.eventType), j.ts, j.seq)
				evtVal, _ := json.Marshal(TemporalEvent{
					DocID:     j.docID,
					EventType: j.eventType,
					Timestamp: j.ts / 1e9, // convert to seconds for the public API
					Actor:     j.actor,
				})
				_ = bEvt.Put([]byte(evtKey), evtVal)

				// Update hot-doc counter (only for access events)
				if j.eventType == EventAccess {
					hotKey := fmt.Sprintf("hot|%s|%s", j.collection, j.docID)
					hotKeyB := []byte(hotKey)
					existing := bHot.Get(hotKeyB)
					var count uint64
					var lastTS int64
					if len(existing) == 16 {
						count = binary.LittleEndian.Uint64(existing[:8])
						raw := binary.LittleEndian.Uint64(existing[8:])
						if raw <= uint64(math.MaxInt64) {
							lastTS = int64(raw) //nolint:gosec
						}
					}
					count++
					if j.ts > lastTS {
						lastTS = j.ts
					}
					var buf [16]byte
					binary.LittleEndian.PutUint64(buf[:8], count)
					binary.LittleEndian.PutUint64(buf[8:], uint64(lastTS))
					_ = bHot.Put(hotKeyB, buf[:])
				}
			}
			return nil
		})
	}

	for {
		select {
		case <-tm.done:
			flush()
			return
		case j := <-tm.ch:
			pending = append(pending, j)
			if len(pending) >= 500 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// QueryRange returns events for a specific document in a time range.
// Pass eventType="" to retrieve all event types.
func (tm *TemporalManager) QueryRange(collection, docID string, from, to int64, eventType string, limit int) ([]TemporalEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	var events []TemporalEvent
	err := tm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTemporal)
		if b == nil {
			return nil
		}
		// When filtering by event type we can use timestamp-based seek.
		// Without event type we must scan the full doc prefix and filter by ts.
		// Keys use nanosecond timestamps for uniqueness; filter by second-based from/to in code.
		var prefix string
		if eventType != "" {
			prefix = fmt.Sprintf("evt|%s|%s|%s|", collection, docID, eventType)
		} else {
			prefix = fmt.Sprintf("evt|%s|%s|", collection, docID)
		}
		c := b.Cursor()
		for k, v := c.Seek([]byte(prefix)); k != nil; k, v = c.Next() {
			ks := string(k)
			if len(ks) < len(prefix) || ks[:len(prefix)] != prefix {
				break
			}
			var evt TemporalEvent
			if err := json.Unmarshal(v, &evt); err != nil {
				continue
			}
			if evt.Timestamp < from || evt.Timestamp >= to {
				continue
			}
			events = append(events, evt)
			if len(events) >= limit {
				break
			}
		}
		return nil
	})
	return events, err
}

// HotEntry is a hot-docs leaderboard entry.
type HotEntry struct {
	DocID        string `json:"docId"`
	Collection   string `json:"collection"`
	AccessCount  uint64 `json:"accessCount"`
	LastAccessAt int64  `json:"lastAccessAt"`
}

// GetHotDocs returns the top-N most accessed documents in a collection since
// the given unix timestamp. Pass since=0 to include all time.
func (tm *TemporalManager) GetHotDocs(collection string, topN int, since int64) ([]HotEntry, error) {
	if topN <= 0 {
		topN = 10
	}
	var entries []HotEntry
	err := tm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTemporalHot)
		if b == nil {
			return nil
		}
		prefix := fmt.Sprintf("hot|%s|", collection)
		c := b.Cursor()
		for k, v := c.Seek([]byte(prefix)); k != nil; k, v = c.Next() {
			ks := string(k)
			if len(ks) < len(prefix) || ks[:len(prefix)] != prefix {
				break
			}
			if len(v) != 16 {
				continue
			}
			count := binary.LittleEndian.Uint64(v[:8])
			rawTS := binary.LittleEndian.Uint64(v[8:])
			var lastTS int64
			if rawTS <= uint64(math.MaxInt64) {
				lastTS = int64(rawTS) //nolint:gosec
			}
			if since > 0 && lastTS < since {
				continue
			}
			docID := ks[len(prefix):]
			entries = append(entries, HotEntry{
				DocID:        docID,
				Collection:   collection,
				AccessCount:  count,
				LastAccessAt: lastTS,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].AccessCount > entries[j].AccessCount
	})
	if len(entries) > topN {
		entries = entries[:topN]
	}
	return entries, nil
}

// TemporalHistogramBucket is a time-bucket with an event count.
type TemporalHistogramBucket struct {
	Label string `json:"label"` // e.g. "2026-04-01" or "2026-W14"
	From  int64  `json:"from"`
	To    int64  `json:"to"`
	Count int    `json:"count"`
}

// scanCollectionEvents returns all events in a collection within a time range.
// It scans the full collection prefix and applies optional event-type filtering.
func (tm *TemporalManager) scanCollectionEvents(collection, eventType string, from, to int64, limit int) ([]TemporalEvent, error) {
	if limit <= 0 {
		limit = 100000
	}
	var events []TemporalEvent
	err := tm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTemporal)
		if b == nil {
			return nil
		}
		collPrefix := fmt.Sprintf("evt|%s|", collection)
		c := b.Cursor()
		for k, v := c.Seek([]byte(collPrefix)); k != nil; k, v = c.Next() {
			ks := string(k)
			if len(ks) < len(collPrefix) || ks[:len(collPrefix)] != collPrefix {
				break
			}
			var evt TemporalEvent
			if err := json.Unmarshal(v, &evt); err != nil {
				continue
			}
			if evt.Timestamp < from || evt.Timestamp >= to {
				continue
			}
			if eventType != "" && string(evt.EventType) != eventType {
				continue
			}
			events = append(events, evt)
			if len(events) >= limit {
				break
			}
		}
		return nil
	})
	return events, err
}

// ComputeHistogram aggregates event counts by day/week/month over a time range.
// interval: "day", "week", "month"
func (tm *TemporalManager) ComputeHistogram(collection, eventType, interval string, from, to int64) ([]TemporalHistogramBucket, error) {
	events, err := tm.scanCollectionEvents(collection, eventType, from, to, 0)
	if err != nil {
		return nil, err
	}

	type bucketKey struct{ from, to int64 }
	counts := map[bucketKey]int{}
	labels := map[bucketKey]string{}

	for _, evt := range events {
		ts := time.Unix(evt.Timestamp, 0).UTC()
		var bk bucketKey
		var label string
		switch interval {
		case "week":
			year, week := ts.ISOWeek()
			monday := isoWeekStart(year, week)
			bk = bucketKey{monday.Unix(), monday.Add(7 * 24 * time.Hour).Unix()}
			label = fmt.Sprintf("%d-W%02d", year, week)
		case "month":
			start := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 1, 0)
			bk = bucketKey{start.Unix(), end.Unix()}
			label = start.Format("2006-01")
		default: // "day"
			start := time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, time.UTC)
			end := start.Add(24 * time.Hour)
			bk = bucketKey{start.Unix(), end.Unix()}
			label = start.Format("2006-01-02")
		}
		counts[bk]++
		labels[bk] = label
	}

	var buckets []TemporalHistogramBucket
	for bk, count := range counts {
		buckets = append(buckets, TemporalHistogramBucket{
			Label: labels[bk],
			From:  bk.from,
			To:    bk.to,
			Count: count,
		})
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].From < buckets[j].From
	})
	return buckets, nil
}

// isoWeekStart returns the Monday of the given ISO year/week.
func isoWeekStart(year, week int) time.Time {
	// Jan 4 is always in week 1
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	// Offset to Monday of that week
	weekday := int(jan4.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	week1Monday := jan4.AddDate(0, 0, -(weekday - 1))
	return week1Monday.AddDate(0, 0, (week-1)*7)
}
