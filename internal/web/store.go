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
	mu      sync.Mutex
	meta    Meta
	byID    map[uint64]*Transaction
	order   []uint64 // insertion order, trimmed to maxTxns
	subs    map[chan []byte]string
	maxTxns int
	synth   atomic.Uint64
}

func newStore(meta Meta) *store {
	return &store{
		meta:    meta,
		byID:    make(map[uint64]*Transaction),
		subs:    make(map[chan []byte]string),
		maxTxns: defaultMaxTxns,
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

func encodeTxn(t *Transaction) []byte {
	data, _ := json.Marshal(event{Type: "txn", Txn: t})
	return data
}
