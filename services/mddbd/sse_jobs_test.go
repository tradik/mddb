package main

import (
	"strings"
	"testing"

	"mddb/internal/testsync"
	"time"

	proto "mddb/proto"

	json "mddb/internal/jsonx"
)

// GO-030: a client waiting for a large import used to poll its status
// endpoint. These cover the events that replace the polling, the throttling
// that keeps a chunked import from flooding a connection, and the filters that
// decide who hears what.

// collectSSE subscribes a fake client to the hub and returns the events it
// received, so a test can assert on the wire format rather than internals.
func collectSSE(h *SSEHub, collection, jobID string) (*sseClient, func() []SSEJobEvent) {
	c := &sseClient{ch: make(chan []byte, 64), collection: collection, job: jobID}
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()

	return c, func() []SSEJobEvent {
		var out []SSEJobEvent
		for {
			select {
			case msg := <-c.ch:
				_, data, found := strings.Cut(string(msg), "data: ")
				if !found {
					continue
				}
				var evt SSEJobEvent
				if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &evt); err == nil {
					out = append(out, evt)
				}
			default:
				return out
			}
		}
	}
}

func testJob(id, collection string, status BulkJobStatus) *BulkIngestJob {
	return &BulkIngestJob{ID: id, Collection: collection, Status: status, Total: 10, Processed: 5}
}

func TestBroadcastJobReachesAWatchingClient(t *testing.T) {
	h := NewSSEHub(true, 10, 5)
	_, drain := collectSSE(h, "", "job-1")

	h.BroadcastJob(SSEJobProgress, testJob("job-1", "docs", BulkJobProcessing), nil)

	events := drain()
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].Event != SSEJobProgress {
		t.Errorf("event = %q", events[0].Event)
	}
	if events[0].Job == nil || events[0].Job.ID != "job-1" {
		t.Errorf("the event should carry the job record, got %+v", events[0].Job)
	}
	if events[0].Job.Processed != 5 || events[0].Job.Total != 10 {
		t.Errorf("counters lost in transit: %+v", events[0].Job)
	}
	if events[0].Timestamp == 0 {
		t.Error("event has no timestamp")
	}
}

func TestBroadcastJobFiltersByJobAndCollection(t *testing.T) {
	h := NewSSEHub(true, 10, 5)
	_, watchingJob1 := collectSSE(h, "", "job-1")
	_, watchingJob2 := collectSSE(h, "", "job-2")
	_, watchingDocs := collectSSE(h, "docs", "")
	_, watchingOther := collectSSE(h, "other", "")
	_, watchingAll := collectSSE(h, "", "")

	h.BroadcastJob(SSEJobStarted, testJob("job-1", "docs", BulkJobProcessing), nil)

	if got := len(watchingJob1()); got != 1 {
		t.Errorf("the client watching job-1 got %d events, want 1", got)
	}
	if got := len(watchingJob2()); got != 0 {
		t.Errorf("a client watching another job got %d events, want 0", got)
	}
	if got := len(watchingDocs()); got != 1 {
		t.Errorf("the client watching the job's collection got %d events, want 1", got)
	}
	if got := len(watchingOther()); got != 0 {
		t.Errorf("a client watching another collection got %d events, want 0", got)
	}
	if got := len(watchingAll()); got != 1 {
		t.Errorf("the unfiltered client got %d events, want 1", got)
	}
}

func TestBroadcastJobIgnoresNothingToSend(t *testing.T) {
	h := NewSSEHub(true, 10, 5)
	_, drain := collectSSE(h, "", "")

	h.BroadcastJob(SSEJobProgress, nil, nil)                                         // no job
	h.BroadcastJob("", testJob("j", "c", BulkJobProcessing), nil)                    // no event name
	NewSSEHub(false, 10, 5).BroadcastJob(SSEJobProgress, testJob("j", "c", ""), nil) // hub disabled

	if got := len(drain()); got != 0 {
		t.Errorf("nothing should have been sent, got %d events", got)
	}
}

func TestJobProgressIsThrottledButTerminalEventsAreNot(t *testing.T) {
	throttle := newJobEventThrottle()
	start := time.Now()

	if !throttle.allow("j", start) {
		t.Fatal("the first progress event should pass")
	}
	if throttle.allow("j", start.Add(200*time.Millisecond)) {
		t.Error("a second event within the interval should be dropped")
	}
	if !throttle.allow("j", start.Add(jobProgressInterval+time.Millisecond)) {
		t.Error("an event after the interval should pass")
	}
	// A different job has its own budget.
	if !throttle.allow("other", start.Add(200*time.Millisecond)) {
		t.Error("throttling must be per job, not global")
	}
}

func TestThrottleForgetsFinishedJobs(t *testing.T) {
	throttle := newJobEventThrottle()
	throttle.allow("j", time.Now())
	throttle.forget("j")

	throttle.mu.Lock()
	_, still := throttle.last["j"]
	throttle.mu.Unlock()
	if still {
		t.Error("a finished job's throttle state should be dropped, or the map grows forever")
	}
}

func TestEventForStatus(t *testing.T) {
	for _, tc := range []struct {
		status BulkJobStatus
		want   string
	}{
		{BulkJobProcessing, SSEJobStarted},
		{BulkJobCompleted, SSEJobCompleted},
		{BulkJobFailed, SSEJobFailed},
		{BulkJobCancelled, SSEJobCancelled},
		{BulkJobPending, ""}, // queued is not worth an event
		{BulkJobStatus("nonsense"), ""},
	} {
		if got := eventForStatus(tc.status); got != tc.want {
			t.Errorf("eventForStatus(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestIsTerminalJobEvent(t *testing.T) {
	for evt, want := range map[string]bool{
		SSEJobCompleted: true,
		SSEJobFailed:    true,
		SSEJobCancelled: true,
		SSEJobStarted:   false,
		SSEJobProgress:  false,
		"doc.added":     false,
	} {
		if got := isTerminalJobEvent(evt); got != want {
			t.Errorf("isTerminalJobEvent(%q) = %v, want %v", evt, got, want)
		}
	}
}

func TestPublishJobEventThrottlesProgressAndClearsOnTerminal(t *testing.T) {
	h := NewSSEHub(true, 10, 5)
	_, drain := collectSSE(h, "", "")
	m := &BulkIngestManager{server: &Server{SSEHub: h}, jobEvents: newJobEventThrottle()}
	job := testJob("job-9", "docs", BulkJobProcessing)

	m.publishJobEvent(SSEJobProgress, job)
	m.publishJobEvent(SSEJobProgress, job) // within the interval: dropped
	m.publishJobEvent(SSEJobCompleted, job)

	events := drain()
	if len(events) != 2 {
		t.Fatalf("expected progress + completed, got %d events", len(events))
	}
	if events[0].Event != SSEJobProgress || events[1].Event != SSEJobCompleted {
		t.Errorf("unexpected sequence: %q then %q", events[0].Event, events[1].Event)
	}

	m.jobEvents.mu.Lock()
	_, still := m.jobEvents.last["job-9"]
	m.jobEvents.mu.Unlock()
	if still {
		t.Error("the terminal event should have cleared the job's throttle state")
	}
}

func TestPublishJobEventWithoutAHubIsSafe(t *testing.T) {
	(&BulkIngestManager{jobEvents: newJobEventThrottle()}).publishJobEvent(SSEJobProgress, testJob("j", "c", ""))
	(&BulkIngestManager{server: &Server{}, jobEvents: newJobEventThrottle()}).
		publishJobEvent(SSEJobProgress, testJob("j", "c", ""))
}

// TestJobLifecycleReachesSSEClients is the end-to-end case GO-030 exists for:
// submit a job, watch it on the same connection document events use, and see
// the whole run without polling anything.
func TestJobLifecycleReachesSSEClients(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	s.SSEHub = NewSSEHub(true, 10, 5)

	m := NewBulkIngestManager(s, 8)
	m.Start()
	defer m.Stop()

	_, drain := collectSSE(s.SSEHub, "", "")

	docs := []*proto.BatchDocument{protoBatchDoc("a"), protoBatchDoc("b"), protoBatchDoc("c")}
	job, err := m.Submit("watched", docs, "")
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, m, job.ID, 5*time.Second, BulkJobCompleted, BulkJobFailed)

	// The status is not the signal. It is set before the terminal event reaches
	// subscribers, so draining as soon as the job reports completion races the
	// delivery — the first Windows CI run drained a stream ending in
	// job.progress and failed here. drain() is non-blocking and returns
	// whatever has arrived, so the events are accumulated until the terminal
	// one shows up.
	var events []SSEJobEvent
	testsync.Wait(t, "the job's terminal event to reach the subscriber", func() bool {
		for _, e := range drain() {
			if strings.HasPrefix(e.Event, "job.") { // an unfiltered client also hears doc.*
				events = append(events, e)
			}
		}
		return len(events) > 0 && isTerminalJobEvent(events[len(events)-1].Event)
	})

	var seen []string
	for _, e := range events {
		if e.Job == nil || e.Job.ID != job.ID {
			t.Errorf("event %q carries the wrong job: %+v", e.Event, e.Job)
			continue
		}
		seen = append(seen, e.Event)
	}

	if len(seen) < 2 {
		t.Fatalf("expected at least a start and a terminal event, got %v", seen)
	}
	if seen[0] != SSEJobStarted {
		t.Errorf("the first event should announce the start, got %q", seen[0])
	}
	if last := seen[len(seen)-1]; !isTerminalJobEvent(last) {
		t.Errorf("the stream should end on a terminal event, got %q", last)
	}
}

// A client watching a different job must not see this one's lifecycle.
func TestJobLifecycleRespectsTheJobFilter(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	s.SSEHub = NewSSEHub(true, 10, 5)

	m := NewBulkIngestManager(s, 8)
	m.Start()
	defer m.Stop()

	_, drainOther := collectSSE(s.SSEHub, "", "some-other-job")

	job, err := m.Submit("watched", []*proto.BatchDocument{protoBatchDoc("a")}, "")
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, m, job.ID, 5*time.Second, BulkJobCompleted, BulkJobFailed)

	if got := len(drainOther()); got != 0 {
		t.Errorf("a client watching another job received %d events", got)
	}
}

// Job counters describe a collection's contents, so a client without read
// permission on that collection must not receive them — the same rule the
// document events follow.
func TestBroadcastJobRespectsReadPermission(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	h := NewSSEHub(true, 10, 5)

	am := &AuthManager{enabled: true, db: s.DB}
	allowed := &sseClient{ch: make(chan []byte, 8), claims: &JWTClaims{Username: "reader", Admin: true}}
	denied := &sseClient{ch: make(chan []byte, 8), claims: &JWTClaims{Username: "nobody", Admin: false}}
	h.mu.Lock()
	h.clients[allowed] = true
	h.clients[denied] = true
	h.mu.Unlock()

	h.BroadcastJob(SSEJobCompleted, testJob("j", "private", BulkJobCompleted), am)

	if len(allowed.ch) != 1 {
		t.Errorf("a reader with permission should receive the event, got %d", len(allowed.ch))
	}
	if len(denied.ch) != 0 {
		t.Errorf("a client without read permission received %d events", len(denied.ch))
	}
}
