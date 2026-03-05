package relay

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

type networkErr struct{}

func (networkErr) Error() string   { return "network" }
func (networkErr) Timeout() bool   { return false }
func (networkErr) Temporary() bool { return true }

func TestMapUpstreamError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantSubstr string
	}{
		{name: "timeout", err: timeoutErr{}, wantStatus: http.StatusBadGateway, wantSubstr: "timeout"},
		{name: "network", err: networkErr{}, wantStatus: http.StatusBadGateway, wantSubstr: "network"},
		{name: "generic", err: errors.New("boom"), wantStatus: http.StatusBadGateway, wantSubstr: "boom"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, msg := mapUpstreamError(tt.err)
			if status != tt.wantStatus {
				t.Fatalf("status=%d want=%d", status, tt.wantStatus)
			}
			if !strings.Contains(msg, tt.wantSubstr) {
				t.Fatalf("msg=%q want contains %q", msg, tt.wantSubstr)
			}
		})
	}

	var _ net.Error = timeoutErr{}
	var _ net.Error = networkErr{}
}
