package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"localshare/internal/config"
)

const (
	maxSignalMessageBytes = 1 << 20
	signalWriteQueueSize  = 32
	signalWriteTimeout    = 5 * time.Second
)

type SignalHub struct {
	cfg      *config.Config
	state    *State
	metrics  *Metrics
	log      *slog.Logger
	upgrader websocket.Upgrader
}

type wsClient struct {
	conn      *websocket.Conn
	out       chan []byte
	done      chan struct{}
	closed    atomic.Bool
	closeOnce sync.Once
}

func NewSignalHub(cfg *config.Config, state *State, metrics *Metrics, log *slog.Logger) *SignalHub {
	return &SignalHub{
		cfg:     cfg,
		state:   state,
		metrics: metrics,
		log:     log,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

func (h *SignalHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.state.SignalPeerCount()+h.state.SignalViewerCount() >= h.cfg.MaxSignalConnections {
		h.metrics.signalRejected.Add(1)
		http.Error(w, "Too many signaling connections", http.StatusServiceUnavailable)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("signal upgrade failed", "err", err)
		return
	}
	client := newWSClient(conn)
	conn.SetReadLimit(maxSignalMessageBytes)
	h.metrics.signalTotal.Add(1)
	Go(r.Context(), h.log, "signal writer", func() {
		h.writeLoop(r.Context(), client)
	})

	var role, peerID, viewerID string
	defer func() {
		h.cleanup(role, peerID, viewerID, client)
		_ = client.close(websocket.CloseNormalClosure, "closed")
	}()

	for {
		typ, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if typ != websocket.TextMessage {
			continue
		}
		h.metrics.signalIn.Add(1)
		h.metrics.signalBytesIn.Add(int64(len(data)))
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			h.send(client, map[string]any{"type": "error", "message": "Invalid JSON"})
			continue
		}
		msgType, _ := msg["type"].(string)
		if role == "ap" && peerID != "" {
			h.touchPeer(peerID)
		}
		switch msgType {
		case "register":
			peerID, _ = msg["peer_id"].(string)
			peerID = normalizePeerID(peerID)
			if peerID == "" {
				h.send(client, map[string]any{"type": "error", "message": "Invalid peer id"})
				continue
			}
			role = "ap"
			fallback, _ := msg["fallback_url"].(string)
			if fallback == "" {
				fallback = h.cfg.PublicURL(peerID)
			}
			h.registerPeer(peerID, fallback, client)
			h.send(client, map[string]any{
				"type":         "registered",
				"peer_id":      peerID,
				"address":      h.cfg.PublicURL("p2p", peerID),
				"fallback_url": fallback,
				"ice_servers":  h.cfg.ICEServers(),
				"status":       "success",
			})
		case "browser":
			peerID, _ = msg["peer_id"].(string)
			peerID = normalizePeerID(peerID)
			if peerID == "" {
				h.send(client, map[string]any{"type": "error", "message": "Invalid peer id"})
				continue
			}
			peer := h.peer(peerID)
			if peer == nil {
				h.send(client, map[string]any{"type": "error", "message": "Peer is offline"})
				continue
			}
			if h.viewerCount(peerID) >= h.cfg.MaxSignalViewersPerPeer {
				h.send(client, map[string]any{"type": "error", "message": "Too many viewers"})
				_ = client.close(websocket.CloseTryAgainLater, "too many viewers")
				continue
			}
			viewerID, _ = msg["viewer_id"].(string)
			if viewerID == "" {
				viewerID, _ = randomToken(12)
			}
			role = "browser"
			h.registerViewer(viewerID, peerID, client)
			h.send(peer, map[string]any{"type": "peer_join", "viewer_id": viewerID})
			h.send(client, map[string]any{"type": "browser_registered", "viewer_id": viewerID})
		case "offer", "candidate":
			id, _ := msg["peer_id"].(string)
			if id == "" {
				id = peerID
			}
			peer := h.peer(normalizePeerID(id))
			if peer == nil {
				h.send(client, map[string]any{"type": "error", "message": "Peer is offline"})
				continue
			}
			msg["ice_servers"] = h.cfg.ICEServers()
			h.send(peer, msg)
		case "answer":
			id, _ := msg["viewer_id"].(string)
			viewer := h.viewer(id)
			if viewer != nil {
				h.send(viewer, msg)
			}
		case "viewer_state":
			id, _ := msg["peer_id"].(string)
			peer := h.peer(normalizePeerID(id))
			if peer != nil {
				h.send(peer, msg)
			}
		case "ping":
			h.touchPeer(peerID)
			h.send(client, map[string]any{"type": "pong"})
		default:
			h.send(client, map[string]any{"type": "error", "message": "Unknown message type"})
		}
	}
}

func (h *SignalHub) CleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.cleanupClosed()
		}
	}
}

func newWSClient(conn *websocket.Conn) *wsClient {
	return &wsClient{
		conn: conn,
		out:  make(chan []byte, signalWriteQueueSize),
		done: make(chan struct{}),
	}
}

func (h *SignalHub) send(client *wsClient, payload any) bool {
	if client == nil {
		return false
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if client.closed.Load() {
		return false
	}
	select {
	case client.out <- data:
		return true
	case <-client.done:
		return false
	default:
		h.log.Warn("signal client write queue full")
		_ = client.close(websocket.CloseTryAgainLater, "write queue full")
		return false
	}
}

func (h *SignalHub) writeLoop(ctx context.Context, client *wsClient) {
	for {
		select {
		case <-ctx.Done():
			_ = client.close(websocket.CloseGoingAway, "server shutting down")
			return
		case <-client.done:
			return
		case data := <-client.out:
			_ = client.conn.SetWriteDeadline(time.Now().Add(signalWriteTimeout))
			if err := client.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				h.log.Debug("signal write failed", "err", err)
				_ = client.close(websocket.CloseAbnormalClosure, "write failed")
				return
			}
			h.metrics.signalOut.Add(1)
			h.metrics.signalBytesOut.Add(int64(len(data)))
		}
	}
}

func (c *wsClient) close(code int, reason string) error {
	var err error
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(2*time.Second))
		err = c.conn.Close()
	})
	return err
}

func (h *SignalHub) registerPeer(peerID, fallback string, client *wsClient) {
	h.state.mu.Lock()
	old := h.state.signalPeers[peerID]
	var oldViewers []*SignalViewer
	for id, viewer := range h.state.signalViewers {
		if viewer.PeerID == peerID {
			oldViewers = append(oldViewers, viewer)
			delete(h.state.signalViewers, id)
		}
	}
	h.state.signalPeers[peerID] = &SignalPeer{PeerID: peerID, Conn: client, SeenAt: time.Now(), FallbackURL: fallback}
	stat := h.state.peerStats[peerID]
	if stat == nil {
		stat = &PeerStats{CreatedAt: time.Now()}
		h.state.peerStats[peerID] = stat
	}
	stat.SignalConnectedAt = time.Now()
	stat.LastSeen = time.Now()
	stat.SignalConnections++
	h.state.mu.Unlock()

	if old != nil {
		if c, ok := old.Conn.(*wsClient); ok && c != client {
			_ = c.close(websocket.CloseNormalClosure, "peer replaced")
		}
	}
	for _, viewer := range oldViewers {
		if c, ok := viewer.Conn.(*wsClient); ok {
			_ = c.close(websocket.CloseNormalClosure, "peer replaced")
		}
	}
}

func (h *SignalHub) registerViewer(viewerID, peerID string, client *wsClient) {
	h.state.mu.Lock()
	old := h.state.signalViewers[viewerID]
	h.state.signalViewers[viewerID] = &SignalViewer{ViewerID: viewerID, PeerID: peerID, Conn: client}
	stat := h.state.peerStats[peerID]
	if stat == nil {
		stat = &PeerStats{CreatedAt: time.Now()}
		h.state.peerStats[peerID] = stat
	}
	stat.ViewersTotal++
	h.metrics.viewerTotal.Add(1)
	h.state.mu.Unlock()
	if old != nil {
		if c, ok := old.Conn.(*wsClient); ok && c != client {
			_ = c.close(websocket.CloseNormalClosure, "viewer replaced")
		}
	}
}

func (h *SignalHub) peer(peerID string) *wsClient {
	h.state.mu.RLock()
	defer h.state.mu.RUnlock()
	peer := h.state.signalPeers[peerID]
	if peer == nil {
		return nil
	}
	c, _ := peer.Conn.(*wsClient)
	return c
}

func (h *SignalHub) viewer(viewerID string) *wsClient {
	h.state.mu.RLock()
	defer h.state.mu.RUnlock()
	viewer := h.state.signalViewers[viewerID]
	if viewer == nil {
		return nil
	}
	c, _ := viewer.Conn.(*wsClient)
	return c
}

func (h *SignalHub) viewerCount(peerID string) int {
	h.state.mu.RLock()
	defer h.state.mu.RUnlock()
	return h.state.viewerCount(peerID)
}

func (h *SignalHub) touchPeer(peerID string) {
	if peerID == "" {
		return
	}
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if peer := h.state.signalPeers[peerID]; peer != nil {
		peer.SeenAt = time.Now()
	}
	stat := h.state.peerStats[peerID]
	if stat == nil {
		stat = &PeerStats{CreatedAt: time.Now()}
		h.state.peerStats[peerID] = stat
	}
	stat.LastSeen = time.Now()
}

func (h *SignalHub) cleanup(role, peerID, viewerID string, client *wsClient) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if role == "ap" && peerID != "" {
		if peer := h.state.signalPeers[peerID]; peer != nil && peer.Conn == client {
			delete(h.state.signalPeers, peerID)
		}
	}
	if role == "browser" && viewerID != "" {
		if viewer := h.state.signalViewers[viewerID]; viewer != nil && viewer.Conn == client {
			delete(h.state.signalViewers, viewerID)
		}
	}
}

func (h *SignalHub) cleanupClosed() {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	for id, peer := range h.state.signalPeers {
		if c, ok := peer.Conn.(*wsClient); ok {
			if time.Since(peer.SeenAt) > 2*time.Minute {
				_ = c.close(websocket.CloseNormalClosure, "peer idle")
				delete(h.state.signalPeers, id)
			}
		}
	}
	for id, viewer := range h.state.signalViewers {
		if h.state.signalPeers[viewer.PeerID] == nil {
			if c, ok := viewer.Conn.(*wsClient); ok {
				_ = c.close(websocket.CloseNormalClosure, "peer offline")
			}
			delete(h.state.signalViewers, id)
		}
	}
}

func normalizePeerID(value string) string {
	if !isAlphaNum(value) {
		return ""
	}
	return value
}

func isAlphaNum(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
