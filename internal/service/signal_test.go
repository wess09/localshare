package service

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"localshare/internal/config"
)

func TestSignalHubRelaysLegacyMessages(t *testing.T) {
	hub := NewSignalHub(signalTestConfig(), NewState(), NewMetrics(), signalTestLogger())
	server := httptest.NewServer(hub)
	defer server.Close()

	peer := dialSignal(t, server.URL)
	defer peer.Close()
	viewer := dialSignal(t, server.URL)
	defer viewer.Close()

	writeSignal(t, peer, map[string]any{
		"type":         "register",
		"peer_id":      "abc12345",
		"fallback_url": "https://example.com/abc12345",
	})
	requireSignalType(t, readSignal(t, peer), "registered")

	writeSignal(t, viewer, map[string]any{
		"type":      "browser",
		"peer_id":   "abc12345",
		"viewer_id": "viewer1",
	})
	requireSignalType(t, readSignal(t, peer), "peer_join")
	requireSignalType(t, readSignal(t, viewer), "browser_registered")

	writeSignal(t, viewer, map[string]any{
		"type":    "offer",
		"peer_id": "abc12345",
		"sdp":     "offer-sdp",
	})
	offer := readSignal(t, peer)
	requireSignalType(t, offer, "offer")
	if offer["sdp"] != "offer-sdp" || offer["ice_servers"] == nil {
		t.Fatalf("offer payload mismatch: %#v", offer)
	}

	writeSignal(t, peer, map[string]any{
		"type":      "answer",
		"viewer_id": "viewer1",
		"sdp":       "answer-sdp",
	})
	answer := readSignal(t, viewer)
	requireSignalType(t, answer, "answer")
	if answer["sdp"] != "answer-sdp" {
		t.Fatalf("answer payload mismatch: %#v", answer)
	}

	writeSignal(t, viewer, map[string]any{
		"type":      "candidate",
		"peer_id":   "abc12345",
		"candidate": "candidate-1",
	})
	candidate := readSignal(t, peer)
	requireSignalType(t, candidate, "candidate")
	if candidate["candidate"] != "candidate-1" {
		t.Fatalf("candidate payload mismatch: %#v", candidate)
	}

	writeSignal(t, viewer, map[string]any{
		"type":    "viewer_state",
		"peer_id": "abc12345",
		"state":   "connected",
	})
	viewerState := readSignal(t, peer)
	requireSignalType(t, viewerState, "viewer_state")
	if viewerState["state"] != "connected" {
		t.Fatalf("viewer_state payload mismatch: %#v", viewerState)
	}

	writeSignal(t, peer, map[string]any{"type": "ping"})
	requireSignalType(t, readSignal(t, peer), "pong")
}

func TestSignalHubRejectsViewerOverLimit(t *testing.T) {
	cfg := signalTestConfig()
	cfg.MaxSignalViewersPerPeer = 1
	hub := NewSignalHub(cfg, NewState(), NewMetrics(), signalTestLogger())
	server := httptest.NewServer(hub)
	defer server.Close()

	peer := dialSignal(t, server.URL)
	defer peer.Close()
	viewer := dialSignal(t, server.URL)
	defer viewer.Close()
	overLimit := dialSignal(t, server.URL)
	defer overLimit.Close()

	writeSignal(t, peer, map[string]any{"type": "register", "peer_id": "abc12345"})
	requireSignalType(t, readSignal(t, peer), "registered")

	writeSignal(t, viewer, map[string]any{"type": "browser", "peer_id": "abc12345", "viewer_id": "viewer1"})
	requireSignalType(t, readSignal(t, peer), "peer_join")
	requireSignalType(t, readSignal(t, viewer), "browser_registered")

	writeSignal(t, overLimit, map[string]any{"type": "browser", "peer_id": "abc12345", "viewer_id": "viewer2"})
	_ = overLimit.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]any
	err := overLimit.ReadJSON(&msg)
	if err == nil {
		requireSignalType(t, msg, "error")
		if msg["message"] != "Too many viewers" {
			t.Fatalf("unexpected over-limit message: %#v", msg)
		}
	}
}

func signalTestConfig() *config.Config {
	return &config.Config{
		ServerName:              "example.com",
		MaxSignalConnections:    10,
		MaxSignalViewersPerPeer: 4,
	}
}

func signalTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func dialSignal(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http"), nil)
	if err != nil {
		t.Fatalf("dial signal: %v", err)
	}
	return conn
}

func writeSignal(t *testing.T, conn *websocket.Conn, payload map[string]any) {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(payload); err != nil {
		t.Fatalf("write signal: %v", err)
	}
}

func readSignal(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read signal: %v", err)
	}
	return msg
}

func requireSignalType(t *testing.T, msg map[string]any, want string) {
	t.Helper()
	if msg["type"] != want {
		t.Fatalf("message type = %v, want %q, payload = %#v", msg["type"], want, msg)
	}
}
