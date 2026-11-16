package indexqueue

import (
	"context"
	"sync"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/fairq"

	bolt "go.etcd.io/bbolt"
)

// orderStore records the collections it indexes, in order, and blocks until
// released, so a test can fill the queue before any worker runs.
type orderStore struct {
	mu    sync.Mutex
	order []string
	gate  chan struct{}
}

func (o *orderStore) DBUpdate(collection string, _ func(*bolt.Tx) error) error {
	<-o.gate
	o.mu.Lock()
	o.order = append(o.order, collection)
	o.mu.Unlock()
	return nil
}
func (o *orderStore) IdxMetaBucket() []byte  { return []byte("idxmeta") }
func (o *orderStore) Binlog() *binlog.Binlog { return nil }

// A metadata key the database refuses (longer than its key limit) fails the
// job instead of half-writing it; Pending is the tracker the job went through.
func TestProcessJobRefusedKeyAndPending(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	iq := NewIndexQueue(s, 1)
	defer iq.Shutdown()
	huge := make([]byte, 40000)
	for i := range huge {
		huge[i] = 'x'
	}
	if err := iq.processJob(&IndexJob{Collection: "c", DocID: string(huge), NewMeta: map[string][]string{"k": {"v"}}}); err == nil {
		t.Fatal("an oversized key was accepted")
	}
	if iq.Pending() == nil {
		t.Fatal("no pending tracker")
	}
}

// GO-054: a tenant with a backlog does not hold another tenant's job behind
// all of it — one worker alternates between them — and the observer hears
// each job's tenant.
func TestIndexQueueSharesWorkersBetweenTenants(t *testing.T) {
	st := &orderStore{gate: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	iq := &IndexQueue{store: st, queue: fairq.New[*IndexJob](QueueCapacity), ctx: ctx, cancel: cancel}
	var seen sync.Map
	iq.SetObserver(func(tenant string, waited time.Duration) {
		if waited < 0 {
			t.Errorf("negative wait")
		}
		seen.Store(tenant, true)
	})
	for i := 0; i < 20; i++ {
		if err := iq.Enqueue(&IndexJob{Collection: "noisy/c", DocID: "d"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := iq.Enqueue(&IndexJob{Collection: "quiet/c", DocID: "d"}); err != nil {
		t.Fatal(err)
	}
	if d := iq.TenantDepths(); d["noisy"] != 20 || d["quiet"] != 1 {
		t.Fatalf("depths = %v", d)
	}
	iq.wg.Add(1)
	go iq.worker(0)
	close(st.gate)
	waitFor(t, func() bool { p, _, _, _ := iq.Stats(); return p == 21 }, "queue did not drain")
	iq.Shutdown()

	pos := -1
	for i, c := range st.order {
		if c == "quiet/c" {
			pos = i
		}
	}
	if pos < 0 || pos > 1 {
		t.Fatalf("quiet tenant's job ran at position %d of %v; want within the first two", pos, st.order)
	}
	if _, ok := seen.Load("quiet"); !ok {
		t.Error("observer did not hear the quiet tenant")
	}
}
