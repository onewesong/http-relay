package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onewesong/http-relay/internal/relay"
)

func TestEventsStreamsMetaThenTxn(t *testing.T) {
	handler, reporter := New(Meta{Addr: "1.2.3.4:80"})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	readData := func() string {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read stream: %v", err)
			}
			if after, ok := strings.CutPrefix(line, "data: "); ok {
				return strings.TrimSpace(after)
			}
		}
	}

	// The first event is always the relay meta.
	var first event
	if err := json.Unmarshal([]byte(readData()), &first); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if first.Type != "meta" || first.Meta == nil || first.Meta.Addr != "1.2.3.4:80" {
		t.Fatalf("expected meta first, got %+v", first)
	}

	// After subscription exists (meta received), a pushed transaction streams in.
	reporter.Access(relay.AccessRecord{Seq: 7, Method: "GET", Target: "https://h/x", Status: 200})

	var ev event
	if err := json.Unmarshal([]byte(readData()), &ev); err != nil {
		t.Fatalf("decode txn: %v", err)
	}
	if ev.Type != "txn" || ev.Txn == nil || ev.Txn.Seq != 7 || ev.Txn.Method != "GET" || !ev.Txn.Done {
		t.Fatalf("expected txn event for seq 7, got %+v", ev)
	}
}

func TestTransactionsAPI(t *testing.T) {
	handler, reporter := New(Meta{})
	reporter.RequestDump(3, "GET / HTTP/1.1\r\n", []byte("hi"), "", "")
	reporter.Access(relay.AccessRecord{Seq: 3, Method: "GET", Status: 200})

	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got []Transaction
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Seq != 3 || got[0].ReqBody == nil || got[0].ReqBody.Text != "hi" {
		t.Fatalf("unexpected api result: %+v", got)
	}
}
