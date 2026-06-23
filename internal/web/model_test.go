package web

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewBodyText(t *testing.T) {
	b := newBody([]byte(`{"ok":true}`))
	if b == nil {
		t.Fatal("expected a body")
	}
	if b.Text != `{"ok":true}` {
		t.Fatalf("text = %q", b.Text)
	}
	if b.Base64 != "" || b.Truncated || b.Size != 11 {
		t.Fatalf("unexpected body: %+v", b)
	}
}

func TestNewBodyBinary(t *testing.T) {
	raw := []byte{0xff, 0xfe, 0x00, 0x01}
	b := newBody(raw)
	if b.Text != "" {
		t.Fatalf("binary body should not set text: %q", b.Text)
	}
	if got, _ := base64.StdEncoding.DecodeString(b.Base64); string(got) != string(raw) {
		t.Fatalf("base64 round-trip mismatch")
	}
}

func TestNewBodyTruncation(t *testing.T) {
	raw := []byte(strings.Repeat("a", maxBodyBytes+100))
	b := newBody(raw)
	if !b.Truncated {
		t.Fatal("expected truncated")
	}
	if len(b.Text) != maxBodyBytes {
		t.Fatalf("text len = %d, want %d", len(b.Text), maxBodyBytes)
	}
	if b.Size != maxBodyBytes+100 {
		t.Fatalf("size should report full length, got %d", b.Size)
	}
}

func TestNewBodyEmpty(t *testing.T) {
	if newBody(nil) != nil || newBody([]byte{}) != nil {
		t.Fatal("empty body should be nil")
	}
}
