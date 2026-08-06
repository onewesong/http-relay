package web

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// defaultMaxTxns bounds retained history; oldest entries are evicted.
	defaultMaxTxns = 1000
	// subBuffer is the per-subscriber queue depth before a slow client is dropped.
	subBuffer = 256
	// synthBase keeps synthetic ids (for seq==0 access entries) clear of real seqs.
	synthBase = uint64(1) << 32
)

// store accumulates transactions keyed by seq and fans updates out to SSE
// subscribers. All access is guarded by mu; broadcast marshals under the lock so
// a concurrent relay mutation can't race the JSON encoder.
type store struct {
	mu        sync.Mutex
	meta      Meta
	byID      map[uint64]*Transaction
	order     []uint64 // insertion order, trimmed to maxTxns
	subs      map[chan []byte]string
	adminSubs map[chan struct{}]struct{}
	maxTxns   int
	synth     atomic.Uint64
}

func newStore(meta Meta) *store {
	return &store{
		meta:      meta,
		byID:      make(map[uint64]*Transaction),
		subs:      make(map[chan []byte]string),
		adminSubs: make(map[chan struct{}]struct{}),
		maxTxns:   defaultMaxTxns,
	}
}

// synthID allocates an id for access entries that carry no dump seq.
func (s *store) synthID() uint64 {
	return synthBase + s.synth.Add(1)
}

// mutate upserts the transaction for seq, applies fn, then broadcasts the
// updated transaction to all subscribers.
func (s *store) mutate(seq uint64, namespace string, fn func(*Transaction)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := s.upsertLocked(seq)
	t.Namespace = namespace
	fn(t)
	s.broadcastLocked(namespace, encodeTxn(t))
	s.notifyAdminsLocked()
}

func (s *store) upsertLocked(seq uint64) *Transaction {
	if t, ok := s.byID[seq]; ok {
		return t
	}
	t := &Transaction{Seq: seq, At: time.Now()}
	s.byID[seq] = t
	s.order = append(s.order, seq)
	if len(s.order) > s.maxTxns {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.byID, oldest)
	}
	return t
}

// broadcastLocked sends data to every subscriber without blocking; a subscriber
// whose queue is full is dropped (its channel closed) so one slow page can't
// stall the relay.
func (s *store) broadcastLocked(namespace string, data []byte) {
	for ch, subscribedNamespace := range s.subs {
		if subscribedNamespace != namespace {
			continue
		}
		select {
		case ch <- data:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// subscribe registers a new SSE client. It returns the live channel, the
// current history pre-encoded as SSE-ready events (so callers never touch a
// mutable transaction pointer), a copy of the meta, and an idempotent cancel.
func (s *store) subscribe(namespace string) (ch <-chan []byte, replay [][]byte, meta Meta, cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := make(chan []byte, subBuffer)
	s.subs[c] = namespace
	s.notifyAdminsLocked()
	meta = s.meta

	replay = make([][]byte, 0, len(s.order))
	for _, id := range s.order {
		if s.byID[id].Namespace == namespace {
			replay = append(replay, encodeTxn(s.byID[id]))
		}
	}
	meta.Namespace = namespace

	cancel = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Only delete here; broadcastLocked owns closing dropped channels, so a
		// double close can't happen.
		delete(s.subs, c)
		s.notifyAdminsLocked()
	}
	return c, replay, meta, cancel
}

// transactions returns a snapshot of retained history for the JSON API.
func (s *store) transactions(namespace string) []*Transaction {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Transaction, 0, len(s.order))
	for _, id := range s.order {
		if s.byID[id].Namespace != namespace {
			continue
		}
		cp := *s.byID[id] // shallow copy; Body pointers are immutable once set
		out = append(out, &cp)
	}
	return out
}

// clear removes retained transactions in namespace and tells every live
// subscriber of that namespace to clear its local projection as well.
func (s *store) clear(namespace string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := s.order[:0]
	for _, id := range s.order {
		if s.byID[id].Namespace == namespace {
			delete(s.byID, id)
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept
	s.broadcastLocked(namespace, encodeClear())
	s.notifyAdminsLocked()
}

type namespaceMetric struct {
	Namespace   string
	Records     int
	LastAt      time.Time
	Subscribers int
}

func (s *store) namespaceMetrics(configured []string) []namespaceMetric {
	s.mu.Lock()
	defer s.mu.Unlock()

	metrics := make(map[string]*namespaceMetric, len(configured)+1)
	ensure := func(namespace string) *namespaceMetric {
		if metric := metrics[namespace]; metric != nil {
			return metric
		}
		metric := &namespaceMetric{Namespace: namespace}
		metrics[namespace] = metric
		return metric
	}
	ensure("")
	for _, namespace := range configured {
		ensure(namespace)
	}
	for _, id := range s.order {
		txn := s.byID[id]
		metric := ensure(txn.Namespace)
		metric.Records++
		if txn.At.After(metric.LastAt) {
			metric.LastAt = txn.At
		}
	}
	for _, namespace := range s.subs {
		ensure(namespace).Subscribers++
	}
	out := make([]namespaceMetric, 0, len(metrics))
	for _, metric := range metrics {
		out = append(out, *metric)
	}
	return out
}

func (s *store) subscribeAdmin() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := make(chan struct{}, 1)
	s.adminSubs[c] = struct{}{}
	c <- struct{}{}
	return c, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.adminSubs, c)
	}
}

func (s *store) notifyAdminsLocked() {
	for ch := range s.adminSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func encodeTxn(t *Transaction) []byte {
	data, _ := json.Marshal(event{Type: "txn", Txn: t})
	return data
}

func encodeClear() []byte {
	data, _ := json.Marshal(event{Type: "clear"})
	return data
}
