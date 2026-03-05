package relay

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}

func mapUpstreamError(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "internal server error"
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return http.StatusBadGateway, "upstream timeout"
		}
		return http.StatusBadGateway, "upstream network error"
	}

	return http.StatusBadGateway, fmt.Sprintf("upstream request failed: %v", err)
}
