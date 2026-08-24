package audit

import (
	"sync"
	"sync/atomic"
)

// AuditExporter mirrors audit events to an external sink — typically
// a SIEM webhook (Splunk HEC, Datadog Logs, ELK) or a syslog daemon.
// Local BoltDB persistence remains the source of truth; exporters are
// best-effort, fire-and-forget, and never block the audit hot path.
type AuditExporter interface {
	// Export queues an event for delivery. Must never block beyond a
	// short channel push and must drop with a counter when the buffer
	// is full so a stalled SIEM cannot back-pressure the database.
	Export(ev AuditEvent)
	// Close stops the worker and waits for the buffer to drain. Safe
	// to call multiple times.
	Close()
	// Name returns a stable identifier ("webhook", "syslog", ...) for
	// status reporting.
	Name() string
	// Stats returns a snapshot of the delivery counters; intended for
	// /v1/audit/exporters and the panel.
	Stats() ExporterStats
}

// ExporterStats is what /v1/audit/exporters serves: counts that an
// operator needs to decide whether the sink is healthy.
type ExporterStats struct {
	Name       string `json:"name"`
	Target     string `json:"target,omitempty"`
	Queued     uint64 `json:"queued"`
	Delivered  uint64 `json:"delivered"`
	Failed     uint64 `json:"failed"`
	Dropped    uint64 `json:"dropped"`
	LastError  string `json:"lastError,omitempty"`
	BufferSize int    `json:"bufferSize"`
}

// exporterCore is shared scaffolding for the concrete exporters: a
// bounded channel, a single delivery goroutine, drop-on-full
// behaviour with a counter, and a graceful Close.
//
// Concrete exporters embed this struct and provide a `deliver` func
// that handles a single event (Webhook does HTTP POST, Syslog does a
// UDP/TCP write). The error returned by `deliver` increments Failed
// and is exposed as LastError; nil increments Delivered.
type exporterCore struct {
	name      string
	target    string
	bufSize   int
	ch        chan AuditEvent
	stopCh    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	queued    uint64
	delivered uint64
	failed    uint64
	dropped   uint64
	mu        sync.Mutex // protects lastErr only
	lastErr   string
}

func newExporterCore(name, target string, bufSize int) *exporterCore {
	if bufSize <= 0 {
		bufSize = 1024
	}
	return &exporterCore{
		name:    name,
		target:  target,
		bufSize: bufSize,
		ch:      make(chan AuditEvent, bufSize),
		stopCh:  make(chan struct{}),
	}
}

// pushOrDrop is called by exporters' Export() — non-blocking enqueue
// that bumps the right counter.
func (c *exporterCore) pushOrDrop(ev AuditEvent) {
	select {
	case c.ch <- ev:
		atomic.AddUint64(&c.queued, 1)
	default:
		atomic.AddUint64(&c.dropped, 1)
	}
}

// run loops over the channel and invokes deliver for every event.
// The goroutine exits when stopCh is closed AND the channel is empty.
func (c *exporterCore) run(deliver func(AuditEvent) error) {
	defer c.wg.Done()
	for {
		select {
		case <-c.stopCh:
			// Drain remaining events on shutdown.
			for {
				select {
				case ev := <-c.ch:
					c.handleOne(deliver, ev)
				default:
					return
				}
			}
		case ev := <-c.ch:
			c.handleOne(deliver, ev)
		}
	}
}

func (c *exporterCore) handleOne(deliver func(AuditEvent) error, ev AuditEvent) {
	if err := deliver(ev); err != nil {
		// lastErr before the counter, not after. The counter is what anyone
		// watching this exporter polls on — a test, or a monitor scraping
		// Stats() — and incrementing it first publishes "one delivery failed"
		// while LastError still says whatever it said before, or nothing.
		c.mu.Lock()
		c.lastErr = err.Error()
		c.mu.Unlock()
		atomic.AddUint64(&c.failed, 1)
		return
	}
	atomic.AddUint64(&c.delivered, 1)
}

// Close signals shutdown and waits for the drain. Safe for concurrent and
// repeated calls (GO-017): the previous check-then-act on stopCh let two
// goroutines both reach close(c.stopCh) and panic with "close of closed
// channel". sync.Once makes the close happen exactly once.
func (c *exporterCore) Close() {
	c.closeOnce.Do(func() { close(c.stopCh) })
	c.wg.Wait()
}

// Stats returns a snapshot of the counters.
func (c *exporterCore) Stats() ExporterStats {
	c.mu.Lock()
	last := c.lastErr
	c.mu.Unlock()
	return ExporterStats{
		Name:       c.name,
		Target:     c.target,
		Queued:     atomic.LoadUint64(&c.queued),
		Delivered:  atomic.LoadUint64(&c.delivered),
		Failed:     atomic.LoadUint64(&c.failed),
		Dropped:    atomic.LoadUint64(&c.dropped),
		LastError:  last,
		BufferSize: c.bufSize,
	}
}

// Name returns the exporter's stable identifier.
func (c *exporterCore) Name() string { return c.name }
