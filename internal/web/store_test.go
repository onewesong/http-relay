package web

import (
	"encoding/json"
	"testing"

	"github.com/onewesong/http-relay/internal/relay"
)

func decodeTxn(t *testing.T, data []byte) *Transaction {
	t.Helper()
	var ev event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.Type != "txn" || ev.Txn == nil {
		t.Fatalf("expected txn event, got %q", ev.Type)
	}
	return ev.Txn
}

func TestWebOptionSetsPerNamespaceLimit(t *testing.T) {
	_, reporter := New(Meta{}, Options{MaxTransactionsPerNamespace: 2})
	for seq := uint64(1); seq <= 3; seq++ {
		reporter.Access(relay.AccessRecord{Seq: seq, Namespace: "team-a"})
	}
	store := reporter.(*webReporter).store
	got := store.transactions("team-a")
	if len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 2 {
		t.Fatalf("configured per-namespace limit not applied: %+v", got)
	}
}

func TestStoreMergesBySeq(t *testing.T) {
	s := newStore(Meta{})
	s.mutate(1, "", func(tx *Transaction) { tx.HasReq = true; tx.Method = "POST" })
	s.mutate(1, "", func(tx *Transaction) { tx.HasResp = true; tx.Status = 201 })

	if len(s.orders[""]) != 1 {
		t.Fatalf("want 1 merged txn, got %d", len(s.orders[""]))
	}
	got := s.byID[1]
	if !got.HasReq || !got.HasResp || got.Method != "POST" || got.Status != 201 {
		t.Fatalf("merge lost fields: %+v", got)
	}
}

func TestStoreEvictsOldestPerNamespace(t *testing.T) {
	s := newStore(Meta{})
	s.maxTxnsPerNamespace = 2
	for seq := uint64(1); seq <= 3; seq++ {
		s.mutate(seq, "team-a", func(tx *Transaction) {})
	}
	s.mutate(4, "team-b", func(tx *Transaction) {})
	s.mutate(5, "team-b", func(tx *Transaction) {})
	if len(s.orders["team-a"]) != 2 || len(s.orders["team-b"]) != 2 {
		t.Fatalf("each namespace should retain two: %+v", s.orders)
	}
	if _, ok := s.byID[1]; ok {
		t.Fatal("oldest team-a record should have been evicted")
	}
	for _, seq := range []uint64{2, 3, 4, 5} {
		if _, ok := s.byID[seq]; !ok {
			t.Fatalf("record %d should be retained", seq)
		}
	}
}

func TestStoreSubscribeReplaysThenStreams(t *testing.T) {
	s := newStore(Meta{Addr: "x"})
	s.mutate(1, "", func(tx *Transaction) { tx.Method = "GET" })
	s.mutate(2, "", func(tx *Transaction) { tx.Method = "POST" })

	ch, replay, meta, cancel := s.subscribe("")
	defer cancel()

	if meta.Addr != "x" {
		t.Fatalf("meta not propagated: %+v", meta)
	}
	if len(replay) != 2 {
		t.Fatalf("want 2 replayed events, got %d", len(replay))
	}
	if newest, oldest := decodeTxn(t, replay[0]), decodeTxn(t, replay[1]); newest.Seq != 2 || oldest.Seq != 1 {
		t.Fatalf("replay must be newest-first: newest=%+v oldest=%+v", newest, oldest)
	}

	// A live update reaches the subscriber.
	s.mutate(3, "", func(tx *Transaction) { tx.Method = "DELETE" })
	select {
	case data := <-ch:
		if tx := decodeTxn(t, data); tx.Seq != 3 || tx.Method != "DELETE" {
			t.Fatalf("bad live txn: %+v", tx)
		}
	default:
		t.Fatal("expected a live event on the channel")
	}
}

func TestStoreDropsSlowSubscriber(t *testing.T) {
	s := newStore(Meta{})
	ch, _, _, cancel := s.subscribe("")
	defer cancel()

	// Never drain: fill the buffer, then one more broadcast drops + closes us.
	for i := 0; i <= subBuffer; i++ {
		s.mutate(uint64(i+1), "", func(tx *Transaction) {})
	}

	// Drain the buffered messages; the channel must end up closed.
	closed := false
	for range subBuffer + 1 {
		if _, ok := <-ch; !ok {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("slow subscriber channel should be closed after overflow")
	}
	if len(s.subs) != 0 {
		t.Fatalf("dropped subscriber should be removed, %d remain", len(s.subs))
	}
}

func TestStoreFiltersNamespaceReplayAndLiveUpdates(t *testing.T) {
	s := newStore(Meta{})
	s.mutate(1, "team-a", func(tx *Transaction) { tx.Method = "GET" })
	s.mutate(2, "team-b", func(tx *Transaction) { tx.Method = "POST" })

	ch, replay, meta, cancel := s.subscribe("team-a")
	defer cancel()
	if meta.Namespace != "team-a" {
		t.Fatalf("meta namespace = %q", meta.Namespace)
	}
	if len(replay) != 1 || decodeTxn(t, replay[0]).Seq != 1 {
		t.Fatalf("unexpected replay: %v", replay)
	}

	s.mutate(3, "team-b", func(tx *Transaction) {})
	select {
	case data := <-ch:
		t.Fatalf("received another namespace event: %s", data)
	default:
	}
	s.mutate(4, "team-a", func(tx *Transaction) {})
	select {
	case data := <-ch:
		if got := decodeTxn(t, data); got.Seq != 4 || got.Namespace != "team-a" {
			t.Fatalf("unexpected live event: %+v", got)
		}
	default:
		t.Fatal("expected matching namespace event")
	}

	got := s.transactions("team-b")
	if len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 2 {
		t.Fatalf("unexpected filtered transactions: %+v", got)
	}
}

func TestStoreSynthIDAboveRealSeqs(t *testing.T) {
	s := newStore(Meta{})
	if id := s.synthID(); id <= synthBase {
		t.Fatalf("synth id %d should exceed base %d", id, synthBase)
	}
}

func TestStoreClearOnlyOneNamespaceAndBroadcasts(t *testing.T) {
	s := newStore(Meta{})
	s.mutate(1, "team-a", func(tx *Transaction) {})
	s.mutate(2, "team-b", func(tx *Transaction) {})
	aCh, _, _, cancelA := s.subscribe("team-a")
	defer cancelA()
	bCh, _, _, cancelB := s.subscribe("team-b")
	defer cancelB()

	s.clear("team-a")
	if got := s.transactions("team-a"); len(got) != 0 {
		t.Fatalf("team-a was not cleared: %+v", got)
	}
	if got := s.transactions("team-b"); len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("team-b should remain: %+v", got)
	}

	select {
	case data := <-aCh:
		var ev event
		if err := json.Unmarshal(data, &ev); err != nil || ev.Type != "clear" {
			t.Fatalf("unexpected clear event: data=%s err=%v", data, err)
		}
	default:
		t.Fatal("team-a subscriber did not receive clear event")
	}
	select {
	case data := <-bCh:
		t.Fatalf("team-b subscriber received event: %s", data)
	default:
	}
}
