package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"localshare/internal/config"
	"localshare/internal/domain"
	"localshare/internal/service"
	"localshare/internal/store"
	adminassets "localshare/web/admin"
)

type HTTPServer struct {
	cfg      *config.Config
	repo     store.Repository
	cluster  *service.ClusterService
	auth     *service.AuthService
	signal   *service.SignalHub
	adminFS  http.Handler
	upgrader websocket.Upgrader
}

func NewHTTPServer(cfg *config.Config, repo store.Repository, cluster *service.ClusterService, auth *service.AuthService, signal *service.SignalHub) (*HTTPServer, error) {
	sub, err := fs.Sub(adminassets.Assets, "dist")
	if err != nil {
		return nil, err
	}
	return &HTTPServer{
		cfg:      cfg,
		repo:     repo,
		cluster:  cluster,
		auth:     auth,
		signal:   signal,
		adminFS:  http.FileServer(http.FS(sub)),
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}, nil
}

func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/signal":
		s.signal.ServeHTTP(w, r)
	case strings.HasPrefix(path, "/p2p/"):
		s.p2pPage(w, r)
	case path == "/admin":
		http.Redirect(w, r, "/admin/", http.StatusFound)
	case strings.HasPrefix(path, "/admin/api/"):
		s.adminAPI(w, r)
	case path == "/api/v1/admin/ws":
		s.adminWS(w, r)
	case strings.HasPrefix(path, "/api/v1/"):
		s.v1API(w, r)
	case strings.HasPrefix(path, "/admin/"):
		http.StripPrefix("/admin/", s.adminFS).ServeHTTP(w, r)
	case path == "/__route_unavailable__":
		writeRouteUnavailablePage(w, http.StatusBadGateway, "", "客户端连接已断开或暂时不可用。")
	case path == "/api/health":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": s.cfg.Role, "node_id": s.cfg.LocalNodeID, "now": time.Now()})
	case path == "/api/nodes" || strings.HasPrefix(path, "/api/nodes/"):
		s.nodesAPI(w, r)
	case path == "/api/routes" || strings.HasPrefix(path, "/api/routes/"):
		s.routesAPI(w, r)
	case strings.HasPrefix(path, "/__cluster_route__/"):
		s.clusterRouteEntry(w, r)
	default:
		http.Redirect(w, r, "https://yc.nanoda.work", http.StatusFound)
	}
}

func (s *HTTPServer) v1API(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/stats" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, s.cluster.Stats(r.Context()))
	case r.URL.Path == "/api/v1/capacity" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		capacity, err := s.cluster.Capacity(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"capacity": capacity})
	case r.URL.Path == "/api/v1/nodes" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		nodes, err := s.repo.ListNodes(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	case strings.HasPrefix(r.URL.Path, "/api/v1/nodes/") && strings.HasSuffix(r.URL.Path, "/capacity") && r.Method == http.MethodPatch:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		nodeID := nodeIDFromSubresource(r.URL.Path, "/capacity")
		if nodeID == "" {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		var raw map[string]any
		if !readJSON(w, r, &raw) {
			return
		}
		patch := domain.NodePatch{}
		if v, ok := intField(raw["max_tunnels"]); ok {
			patch.MaxTunnels = &v
		}
		if v, ok := intField(raw["max_active_connections"]); ok {
			patch.MaxActiveConnections = &v
		}
		if patch.MaxTunnels == nil && patch.MaxActiveConnections == nil {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		node, err := s.repo.PatchNode(r.Context(), nodeID, patch)
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), "node.capacity", nodeID, raw)
		writeJSON(w, http.StatusOK, map[string]any{"node": node})
	case strings.HasPrefix(r.URL.Path, "/api/v1/nodes/") && strings.HasSuffix(r.URL.Path, "/weight") && r.Method == http.MethodPatch:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		nodeID := nodeIDFromSubresource(r.URL.Path, "/weight")
		if nodeID == "" {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		var raw map[string]any
		if !readJSON(w, r, &raw) {
			return
		}
		weight, ok := intField(raw["weight"])
		if !ok {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		node, err := s.repo.PatchNode(r.Context(), nodeID, domain.NodePatch{Weight: &weight})
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), "node.weight", nodeID, map[string]any{"weight": weight})
		writeJSON(w, http.StatusOK, map[string]any{"node": node})
	case strings.HasPrefix(r.URL.Path, "/api/v1/nodes/") && strings.HasSuffix(r.URL.Path, "/maintenance") && r.Method == http.MethodPatch:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		nodeID := nodeIDFromSubresource(r.URL.Path, "/maintenance")
		if nodeID == "" {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		var req struct {
			Maintenance bool `json:"maintenance"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		node, err := s.repo.PatchNode(r.Context(), nodeID, domain.NodePatch{Maintenance: &req.Maintenance})
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), "node.maintenance", nodeID, map[string]any{"maintenance": req.Maintenance})
		writeJSON(w, http.StatusOK, map[string]any{"node": node})
	case strings.HasPrefix(r.URL.Path, "/api/v1/nodes/") && r.Method == http.MethodDelete:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		nodeID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/"), "/")
		if nodeID == "" || strings.Contains(nodeID, "/") {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		if err := s.repo.DeleteNode(r.Context(), nodeID); err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), "node.delete", nodeID, nil)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case strings.HasPrefix(r.URL.Path, "/api/v1/nodes/") && r.Method == http.MethodPatch:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		nodeID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/"), "/")
		if nodeID == "" || strings.Contains(nodeID, "/") {
			http.NotFound(w, r)
			return
		}
		var raw map[string]any
		if !readJSON(w, r, &raw) {
			return
		}
		node, err := s.repo.PatchNode(r.Context(), nodeID, nodePatch(raw))
		if err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), "node.patch", nodeID, raw)
		writeJSON(w, http.StatusOK, map[string]any{"node": node})
	case r.URL.Path == "/api/v1/routes" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		routes, err := s.repo.ListRoutes(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
	case strings.HasPrefix(r.URL.Path, "/api/v1/routes/") && r.Method == http.MethodDelete:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/routes/"), "/")
		if !domain.IsHashToken(token) {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		if err := s.repo.DeleteRoute(r.Context(), token); err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), "route.delete", token, nil)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/api/v1/settings" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		settings, err := s.repo.ListClusterSettings(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	case strings.HasPrefix(r.URL.Path, "/api/v1/settings/") && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch):
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		key := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/settings/"), "/")
		if key == "" || strings.Contains(key, "/") {
			writeError(w, domain.ErrInvalidInput)
			return
		}
		var req struct {
			Value string `json:"value"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		if err := s.repo.UpsertClusterSetting(r.Context(), key, req.Value); err != nil {
			writeError(w, err)
			return
		}
		s.audit(r.Context(), "setting.upsert", key, map[string]any{"value": req.Value})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/api/v1/audit-events" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		if !s.requireRepo(w) {
			return
		}
		events, err := s.repo.ListAuditEvents(r.Context(), 100)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	default:
		http.NotFound(w, r)
	}
}

func (s *HTTPServer) p2pPage(w http.ResponseWriter, r *http.Request) {
	peerID := strings.TrimPrefix(r.URL.Path, "/p2p/")
	peerID = strings.Trim(peerID, "/")
	if !domain.IsHashToken(peerID) {
		http.NotFound(w, r)
		return
	}
	html := strings.ReplaceAll(p2pHTML, "__PEER_ID__", peerID)
	html = strings.ReplaceAll(html, "__ICE_SERVERS__", mustJSON(s.cfg.ICEServers()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	n, _ := w.Write([]byte(html))
	s.cluster.RecordP2PPage(int64(n))
}

func (s *HTTPServer) adminAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/api/session" && r.Method == http.MethodGet:
		_, _, err := s.auth.Session(r.Context(), adminCookie(r))
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": err == nil, "setup_required": s.auth.SetupRequired(r.Context())})
	case r.URL.Path == "/admin/api/setup" && r.Method == http.MethodPost:
		var req struct {
			Password string `json:"password"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		if err := s.auth.Setup(r.Context(), req.Password); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/admin/api/login" && r.Method == http.MethodPost:
		var req struct {
			Password string `json:"password"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		sid, expires, err := s.auth.Login(r.Context(), req.Password)
		if err != nil {
			writeError(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "localshare_admin", Value: sid, Path: "/", HttpOnly: true, Secure: s.cfg.HTTPS, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/admin/api/logout" && r.Method == http.MethodPost:
		_ = s.auth.Logout(r.Context(), adminCookie(r))
		http.SetCookie(w, &http.Cookie{Name: "localshare_admin", Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/admin/api/stats" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, s.cluster.Stats(r.Context()))
	default:
		http.NotFound(w, r)
	}
}

func (s *HTTPServer) nodesAPI(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster store is not initialized"})
		return
	}
	if r.URL.Path == "/api/nodes" && r.Method == http.MethodGet {
		if !s.requireAdmin(w, r) {
			return
		}
		nodes, err := s.repo.ListNodes(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodPatch {
		if !s.requireAdmin(w, r) {
			return
		}
		var raw map[string]any
		if !readJSON(w, r, &raw) {
			return
		}
		node, err := s.repo.PatchNode(r.Context(), parts[0], nodePatch(raw))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"node": node})
		return
	}
	if len(parts) == 2 && parts[1] == "heartbeat" && r.Method == http.MethodPost {
		s.heartbeat(w, r, parts[0])
		return
	}
	http.NotFound(w, r)
}

func (s *HTTPServer) heartbeat(w http.ResponseWriter, r *http.Request, nodeID string) {
	var payload domain.Node
	if !readJSON(w, r, &payload) {
		return
	}
	if !s.requireNode(w, r, nodeID, true) {
		return
	}
	node, err := s.repo.UpdateHeartbeat(r.Context(), nodeID, payload)
	if errors.Is(err, domain.ErrNotFound) {
		if !constantEqual(bearer(r), s.cfg.NodeRegistrationToken) {
			writeError(w, err)
			return
		}
		if payload.Token == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "node token is required"})
			return
		}
		payload.NodeID = nodeID
		node, err = s.repo.UpsertNode(r.Context(), payload)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "registered": true, "node": node})
			return
		}
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": node})
}

func (s *HTTPServer) routesAPI(w http.ResponseWriter, r *http.Request) {
	if s.repo == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster store is not initialized"})
		return
	}
	if r.URL.Path == "/api/routes" && r.Method == http.MethodGet {
		if !s.requireAdmin(w, r) {
			return
		}
		routes, err := s.repo.ListRoutes(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
		return
	}
	if r.URL.Path == "/api/routes" && r.Method == http.MethodPost {
		var payload domain.Route
		if !readJSON(w, r, &payload) {
			return
		}
		if !s.requireNode(w, r, payload.NodeID, false) {
			return
		}
		route, err := s.repo.RegisterRoute(r.Context(), payload)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "route": route})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/routes/") && r.Method == http.MethodDelete {
		token := strings.TrimPrefix(r.URL.Path, "/api/routes/")
		route, err := s.repo.GetRoute(r.Context(), token)
		if err == nil {
			if !s.requireNode(w, r, route.NodeID, false) {
				return
			}
		} else if !s.isAdmin(r) && !s.bearerMatchesAnyNode(r.Context(), bearer(r)) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Unauthorized"})
			return
		}
		if err := s.repo.DeleteRoute(r.Context(), token); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	http.NotFound(w, r)
}

func (s *HTTPServer) clusterRouteEntry(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/__cluster_route__/")
	parts := strings.SplitN(rest, "/", 2)
	token := strings.ToLower(parts[0])
	if !domain.IsHashToken(token) {
		writeRouteUnavailablePage(w, http.StatusNotFound, token, "链接不存在或格式不正确。")
		return
	}
	routeValue, err := s.repo.GetRoute(r.Context(), token)
	if err != nil {
		s.cluster.RecordRouteLookupMiss()
		writeRouteUnavailablePage(w, http.StatusNotFound, token, "客户端链接不存在或已经断开。")
		return
	}
	if routeValue.Status != domain.RouteStatusActive || routeValue.ExpiresAt.Before(time.Now()) {
		s.cluster.RecordRouteLookupMiss()
		writeRouteUnavailablePage(w, http.StatusGone, token, "客户端链接已过期或已经断开。")
		return
	}
	nodeValue, err := s.repo.GetNode(r.Context(), routeValue.NodeID)
	if err != nil || !nodeValue.Enabled {
		s.cluster.RecordRouteLookupMiss()
		writeRouteUnavailablePage(w, http.StatusServiceUnavailable, token, "承载该链接的节点暂时不可用。")
		return
	}
	if nodeValue.NodeID == s.cfg.LocalNodeID {
		s.cluster.RecordRouteLookupMiss()
		writeRouteUnavailablePage(w, http.StatusBadGateway, token, "客户端连接已断开或本机转发暂时不可用。")
		return
	}
	target := config.URLJoin(nodeValue.PublicBaseURL, token)
	if len(parts) == 2 {
		target = config.URLJoin(target, parts[1])
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	s.cluster.RecordRouteRedirect()
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *HTTPServer) adminWS(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	commands := make(chan adminWSCommand, 8)
	readErr := make(chan error, 1)
	service.Go(r.Context(), nil, "admin ws reader", func() {
		defer close(commands)
		conn.SetReadLimit(1 << 20)
		for {
			var cmd adminWSCommand
			if err := conn.ReadJSON(&cmd); err != nil {
				select {
				case readErr <- err:
				default:
				}
				return
			}
			select {
			case commands <- cmd:
			case <-r.Context().Done():
				return
			}
		}
	})

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	write := func(payload any) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteJSON(payload) == nil
	}
	if !write(map[string]any{"type": "stats", "data": s.cluster.Stats(r.Context())}) {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-readErr:
			return
		case cmd, ok := <-commands:
			if !ok {
				return
			}
			if !write(s.handleAdminWSCommand(r.Context(), cmd)) {
				return
			}
		case <-ticker.C:
			if !write(map[string]any{"type": "stats", "data": s.cluster.Stats(r.Context())}) {
				return
			}
		}
	}
}

type adminWSCommand struct {
	Type   string         `json:"type"`
	NodeID string         `json:"node_id"`
	Patch  map[string]any `json:"patch"`
	Token  string         `json:"token"`
	Key    string         `json:"key"`
	Value  string         `json:"value"`
}

func (s *HTTPServer) handleAdminWSCommand(ctx context.Context, cmd adminWSCommand) map[string]any {
	switch cmd.Type {
	case "", "refresh":
		return map[string]any{"type": "stats", "data": s.cluster.Stats(ctx)}
	case "capacity":
		capacity, err := s.cluster.Capacity(ctx)
		if err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		return map[string]any{"type": "capacity", "data": capacity}
	case "patch_node":
		if s.repo == nil {
			return map[string]any{"type": "error", "error": "cluster store is not initialized"}
		}
		if cmd.NodeID == "" {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		node, err := s.repo.PatchNode(ctx, cmd.NodeID, nodePatch(cmd.Patch))
		if err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		s.audit(ctx, "node.patch", cmd.NodeID, cmd.Patch)
		return map[string]any{"type": "node", "data": node}
	case "set_capacity":
		if s.repo == nil {
			return map[string]any{"type": "error", "error": "cluster store is not initialized"}
		}
		if cmd.NodeID == "" {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		patch := domain.NodePatch{}
		if v, ok := intField(cmd.Patch["max_tunnels"]); ok {
			patch.MaxTunnels = &v
		}
		if v, ok := intField(cmd.Patch["max_active_connections"]); ok {
			patch.MaxActiveConnections = &v
		}
		if patch.MaxTunnels == nil && patch.MaxActiveConnections == nil {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		node, err := s.repo.PatchNode(ctx, cmd.NodeID, patch)
		if err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		s.audit(ctx, "node.capacity", cmd.NodeID, cmd.Patch)
		return map[string]any{"type": "node", "data": node}
	case "set_weight":
		if s.repo == nil {
			return map[string]any{"type": "error", "error": "cluster store is not initialized"}
		}
		if cmd.NodeID == "" {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		weight, ok := intField(cmd.Patch["weight"])
		if !ok {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		node, err := s.repo.PatchNode(ctx, cmd.NodeID, domain.NodePatch{Weight: &weight})
		if err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		s.audit(ctx, "node.weight", cmd.NodeID, map[string]any{"weight": weight})
		return map[string]any{"type": "node", "data": node}
	case "set_maintenance":
		if s.repo == nil {
			return map[string]any{"type": "error", "error": "cluster store is not initialized"}
		}
		if cmd.NodeID == "" {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		maintenance, ok := cmd.Patch["maintenance"].(bool)
		if !ok {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		node, err := s.repo.PatchNode(ctx, cmd.NodeID, domain.NodePatch{Maintenance: &maintenance})
		if err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		s.audit(ctx, "node.maintenance", cmd.NodeID, map[string]any{"maintenance": maintenance})
		return map[string]any{"type": "node", "data": node}
	case "delete_node":
		if s.repo == nil {
			return map[string]any{"type": "error", "error": "cluster store is not initialized"}
		}
		if cmd.NodeID == "" {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		if err := s.repo.DeleteNode(ctx, cmd.NodeID); err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		s.audit(ctx, "node.delete", cmd.NodeID, nil)
		return map[string]any{"type": "node_deleted", "node_id": cmd.NodeID}
	case "delete_route":
		if s.repo == nil {
			return map[string]any{"type": "error", "error": "cluster store is not initialized"}
		}
		if !domain.IsHashToken(cmd.Token) {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		if err := s.repo.DeleteRoute(ctx, cmd.Token); err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		s.audit(ctx, "route.delete", cmd.Token, nil)
		return map[string]any{"type": "route_deleted", "token": cmd.Token}
	case "upsert_setting":
		if s.repo == nil {
			return map[string]any{"type": "error", "error": "cluster store is not initialized"}
		}
		if strings.TrimSpace(cmd.Key) == "" {
			return map[string]any{"type": "error", "error": domain.ErrInvalidInput.Error()}
		}
		if err := s.repo.UpsertClusterSetting(ctx, cmd.Key, cmd.Value); err != nil {
			return map[string]any{"type": "error", "error": err.Error()}
		}
		s.audit(ctx, "setting.upsert", cmd.Key, map[string]any{"value": cmd.Value})
		return map[string]any{"type": "setting", "key": cmd.Key, "value": cmd.Value}
	default:
		return map[string]any{"type": "error", "error": "unknown admin command"}
	}
}

func (s *HTTPServer) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.isAdmin(r) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Unauthorized"})
	return false
}

func (s *HTTPServer) requireRepo(w http.ResponseWriter) bool {
	if s.repo != nil {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster store is not initialized"})
	return false
}

func (s *HTTPServer) audit(ctx context.Context, action, target string, detail map[string]any) {
	if s.repo == nil {
		return
	}
	_ = s.repo.LogAuditEvent(ctx, store.AuditEvent{
		Actor:  "admin",
		Action: action,
		Target: target,
		Detail: detail,
	})
}

func (s *HTTPServer) isAdmin(r *http.Request) bool {
	if s.cfg.AdminAPIToken != "" && constantEqual(bearer(r), s.cfg.AdminAPIToken) {
		return true
	}
	if _, _, err := s.auth.Session(r.Context(), adminCookie(r)); err == nil {
		return true
	}
	return false
}

func (s *HTTPServer) requireNode(w http.ResponseWriter, r *http.Request, nodeID string, allowRegistration bool) bool {
	token := bearer(r)
	if s.cfg.AdminAPIToken != "" && constantEqual(token, s.cfg.AdminAPIToken) {
		return true
	}
	nodeValue, err := s.repo.GetNode(r.Context(), nodeID)
	if err == nil && nodeValue.Token != "" && constantEqual(token, nodeValue.Token) {
		return true
	}
	if allowRegistration && errors.Is(err, domain.ErrNotFound) && s.cfg.NodeRegistrationToken != "" && constantEqual(token, s.cfg.NodeRegistrationToken) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Unauthorized"})
	return false
}

func (s *HTTPServer) bearerMatchesAnyNode(ctx context.Context, token string) bool {
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		return false
	}
	for _, node := range nodes {
		if node.Token != "" && constantEqual(token, node.Token) {
			return true
		}
	}
	return false
}

func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid JSON"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Unauthorized"})
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrRouteOnAnotherNode):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
}

func bearer(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func adminCookie(r *http.Request) string {
	c, err := r.Cookie("localshare_admin")
	if err != nil {
		return ""
	}
	return c.Value
}

func constantEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func nodePatch(raw map[string]any) domain.NodePatch {
	var patch domain.NodePatch
	if v, ok := raw["enabled"].(bool); ok {
		patch.Enabled = &v
	}
	if v, ok := raw["maintenance"].(bool); ok {
		patch.Maintenance = &v
	}
	if v, ok := intField(raw["weight"]); ok {
		patch.Weight = &v
	}
	if v, ok := intField(raw["max_tunnels"]); ok {
		patch.MaxTunnels = &v
	}
	if v, ok := intField(raw["max_active_connections"]); ok {
		patch.MaxActiveConnections = &v
	}
	if v, ok := raw["region"].(string); ok {
		patch.Region = &v
	}
	return patch
}

func intField(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func nodeIDFromSubresource(path string, suffix string) string {
	raw := strings.TrimSuffix(strings.Trim(path, "/"), strings.TrimPrefix(suffix, "/"))
	raw = strings.TrimSuffix(raw, "/")
	nodeID := strings.Trim(strings.TrimPrefix(raw, "api/v1/nodes/"), "/")
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return ""
	}
	return nodeID
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(data)
}

func writeRouteUnavailablePage(w http.ResponseWriter, status int, token, detail string) {
	if detail == "" {
		detail = "客户端连接已断开或暂时不可用。"
	}
	tokenText := "unknown"
	tokenBlock := ""
	if token != "" {
		tokenText = html.EscapeString(token)
		tokenBlock = `<div class="token">route token: <code>` + tokenText + `</code></div>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	page := strings.ReplaceAll(routeUnavailableHTML, "__DETAIL__", html.EscapeString(detail))
	page = strings.ReplaceAll(page, "__TOKEN_BLOCK__", tokenBlock)
	page = strings.ReplaceAll(page, "__TOKEN_TEXT__", tokenText)
	_, _ = w.Write([]byte(page))
}

var p2pHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Localshare P2P</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f6f8fb;color:#18202a;font:15px/1.6 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.panel{width:min(680px,calc(100% - 32px));border:1px solid #dfe4ea;border-radius:8px;background:#fff;padding:28px;box-shadow:0 18px 42px rgba(20,30,50,.09)}h1{margin:0 0 12px;font-size:28px}.muted{color:#687385}.btn{display:inline-flex;margin-top:18px;padding:9px 13px;border:1px solid #2563eb;border-radius:6px;background:#2563eb;color:#fff;text-decoration:none}</style></head>
<body><main class="panel"><h1>正在建立连接</h1><p class="muted" id="status">正在连接信令服务，若 P2P 不可用会自动切换到 SSH 转发。</p><a class="btn" href="/__PEER_ID__">打开 SSH 转发入口</a></main>
<script>(()=>{const peerId="__PEER_ID__";const fallback="/"+peerId;const signalUrl=(location.protocol==="https:"?"wss://":"ws://")+location.host+"/signal";const status=document.getElementById("status");let done=false;function fail(x){if(done)return;done=true;status.textContent="P2P 当前不可用，切换到 SSH 转发："+x;setTimeout(()=>location.replace(fallback),500)}try{const ws=new WebSocket(signalUrl);ws.onopen=()=>{status.textContent="信令服务已连接，等待客户端 P2P 能力。";ws.send(JSON.stringify({type:"browser",peer_id:peerId,viewer_id:(crypto.randomUUID?crypto.randomUUID():String(Date.now()))}))};ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.type==="error")fail(m.message||"信令错误")};ws.onerror=()=>fail("信令连接失败");setTimeout(()=>fail("10 秒内未完成 P2P 协商"),10000)}catch(e){fail(String(e))}})();</script></body></html>`

var routeUnavailableHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Localshare 链接不可用</title>
  <style>
    :root {
      --ink: #10141f;
      --muted: #667085;
      --paper: #fffdf8;
      --panel: #ffffff;
      --line: #d8dee8;
      --red: #e11d48;
      --green: #16a34a;
      --orange: #f59e0b;
      --yellow: #f5c542;
      --shadow: 0 22px 55px rgba(20, 28, 43, .14);
    }

    * { box-sizing: border-box; }

    body {
      margin: 0;
      min-height: 100vh;
      color: var(--ink);
      background:
        linear-gradient(90deg, rgba(16, 20, 31, .035) 1px, transparent 1px),
        linear-gradient(rgba(16, 20, 31, .035) 1px, transparent 1px),
        var(--paper);
      background-size: 34px 34px;
      font: 15px/1.55 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      padding: 26px;
    }

    .wrap {
      width: min(1080px, 100%);
      min-height: calc(100vh - 52px);
      margin: 0 auto;
      display: grid;
      align-content: center;
      gap: 18px;
    }

    .topbar {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      color: #344054;
      font-size: 12px;
      font-weight: 800;
      letter-spacing: .08em;
      text-transform: uppercase;
    }

    .brand {
      display: flex;
      align-items: center;
      gap: 10px;
    }

    .brand-mark {
      width: 30px;
      height: 30px;
      border: 2px solid var(--ink);
      border-radius: 7px;
      background:
        linear-gradient(135deg, transparent 41%, var(--ink) 42% 50%, transparent 51%),
        linear-gradient(45deg, transparent 41%, var(--ink) 42% 50%, transparent 51%),
        #fff;
      box-shadow: 4px 4px 0 var(--yellow);
    }

    .stamp {
      border: 1px solid #cfd6e0;
      border-radius: 999px;
      padding: 5px 10px;
      background: rgba(255, 255, 255, .76);
      color: #667085;
      white-space: nowrap;
    }

    .board {
      overflow: hidden;
      border: 2px solid var(--ink);
      border-radius: 14px;
      background: var(--panel);
      box-shadow: var(--shadow), 8px 8px 0 #10141f;
    }

    .board-head {
      display: grid;
      grid-template-columns: auto 1fr auto;
      align-items: center;
      gap: 12px;
      border-bottom: 2px solid var(--ink);
      padding: 12px 16px;
      background: #f7f9fc;
    }

    .lights {
      display: flex;
      gap: 7px;
    }

    .light {
      width: 12px;
      height: 12px;
      border: 2px solid var(--ink);
      border-radius: 50%;
      background: #d0d5dd;
    }

    .light.red { background: var(--red); }
    .light.yellow { background: var(--yellow); }
    .light.green { background: var(--green); }

    .head-title {
      overflow: hidden;
      color: #475467;
      font-size: 12px;
      font-weight: 800;
      letter-spacing: .12em;
      text-overflow: ellipsis;
      text-transform: uppercase;
      white-space: nowrap;
    }

    .head-code {
      color: #98a2b3;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      font-size: 12px;
      font-weight: 800;
    }

    .main {
      display: grid;
      grid-template-columns: minmax(0, 1.05fr) minmax(300px, .95fr);
      min-height: 520px;
    }

    .copy {
      padding: 52px;
      border-right: 2px solid var(--ink);
      display: flex;
      flex-direction: column;
      justify-content: center;
    }

    .label {
      width: fit-content;
      margin-bottom: 20px;
      border: 2px solid var(--ink);
      border-radius: 999px;
      padding: 6px 12px;
      background: #fff1f2;
      color: var(--red);
      font-size: 12px;
      font-weight: 900;
      letter-spacing: .08em;
      text-transform: uppercase;
      transform: rotate(-1.5deg);
    }

    h1 {
      margin: 0;
      max-width: 600px;
      font-size: 64px;
      font-weight: 950;
      letter-spacing: 0;
      line-height: .95;
    }

    .lead {
      max-width: 560px;
      margin: 24px 0 0;
      color: var(--muted);
      font-size: 17px;
    }

    .token {
      width: fit-content;
      max-width: 100%;
      margin-top: 24px;
      border: 1px dashed #98a2b3;
      border-radius: 8px;
      padding: 10px 12px;
      background: #f8fafc;
      color: #667085;
      font-size: 13px;
    }

    code {
      color: var(--ink);
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      font-size: 13px;
      font-weight: 800;
    }

    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      margin-top: 30px;
    }

    .btn {
      min-height: 42px;
      border: 2px solid var(--ink);
      border-radius: 8px;
      padding: 9px 15px;
      color: var(--ink);
      background: #fff;
      box-shadow: 4px 4px 0 var(--ink);
      font: inherit;
      font-weight: 850;
      text-decoration: none;
      transition: transform .12s ease, box-shadow .12s ease;
    }

    .btn:hover {
      transform: translate(2px, 2px);
      box-shadow: 2px 2px 0 var(--ink);
    }

    .btn.primary {
      background: var(--yellow);
    }

    .side {
      display: grid;
      align-content: center;
      gap: 22px;
      padding: 34px;
      background: #f3f5f8;
    }

    .diag-card {
      border: 2px solid var(--ink);
      border-radius: 12px;
      background: #fff;
      box-shadow: 6px 6px 0 var(--ink);
      overflow: hidden;
    }

    .diag-title {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      border-bottom: 2px solid var(--ink);
      padding: 12px 14px;
      background: #fffdf8;
      color: #475467;
      font-size: 12px;
      font-weight: 900;
      letter-spacing: .08em;
      text-transform: uppercase;
    }

    .diag-title span:last-child {
      color: var(--red);
      white-space: nowrap;
    }

    .diag {
      position: relative;
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 12px;
      padding: 30px 18px 26px;
    }

    .diag::before {
      content: "";
      position: absolute;
      left: 15%;
      right: 15%;
      top: 72px;
      height: 2px;
      background: #cfd6e0;
    }

    .diag-step {
      position: relative;
      z-index: 1;
      display: grid;
      justify-items: center;
      gap: 8px;
      text-align: center;
      min-width: 0;
    }

    .diag-icon {
      width: 86px;
      height: 76px;
      display: block;
    }

    .diag-name {
      margin: 6px 0 0;
      color: #667085;
      font-size: 12px;
      font-weight: 800;
    }

    .diag-result {
      margin: 0;
      font-size: 15px;
      font-weight: 800;
    }

    .diag-result.ok { color: var(--green); }
    .diag-result.bad { color: var(--red); }

    .log {
      border: 2px solid var(--ink);
      border-radius: 10px;
      padding: 16px;
      background: #10141f;
      color: #b7c7d9;
      box-shadow: 5px 5px 0 var(--ink);
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
      font-size: 12px;
      overflow: hidden;
    }

    .log p {
      margin: 0;
      white-space: nowrap;
    }

    .log p + p { margin-top: 7px; }
    .log .bad { color: #ffb4c2; }
    .log .ok { color: #9df2bd; }
    .log .dim { color: #7f91a8; }

    @media (max-width: 860px) {
      body { padding: 18px; }
      .wrap { min-height: calc(100vh - 36px); }
      .main { grid-template-columns: 1fr; }
      .copy { border-right: 0; border-bottom: 2px solid var(--ink); }
      .side { padding: 24px; }
      h1 { font-size: 44px; }
    }

    @media (max-width: 560px) {
      .board { box-shadow: 5px 5px 0 #10141f; }
      .board-head { grid-template-columns: 1fr; }
      .lights { order: -1; }
      .copy { padding: 28px 22px; }
      .side { padding: 18px; }
      .diag { grid-template-columns: 1fr; }
      .diag::before { display: none; }
      h1 { font-size: 38px; }
    }
  </style>
</head>
<body>
  <div class="wrap">
    <header class="topbar">
      <div class="brand">
        <span class="brand-mark" aria-hidden="true"></span>
        <span>Localshare</span>
      </div>
      <div class="stamp">Tunnel Monitor</div>
    </header>

    <main class="board">
      <div class="board-head">
        <div class="lights" aria-hidden="true">
          <span class="light red"></span>
          <span class="light yellow"></span>
          <span class="light green"></span>
        </div>
        <div class="head-title">reverse tunnel / public fallback / route lookup</div>
        <div class="head-code">404 / 502</div>
      </div>

      <section class="main">
        <div class="copy">
          <div class="label">connection dropped</div>
          <h1>隧道睡着了</h1>
          <p class="lead">这个连接对应的客户端可能已经离线、重连、故障，或者节点暂时不可达。请检查客户端状态，确保正常连接，或稍等一会。</p>
          __TOKEN_BLOCK__
          <div class="actions">
            <a class="btn primary" href="">重新扫描</a>
            <a class="btn" href="/">返回入口</a>
          </div>
        </div>

        <aside class="side" aria-label="连接诊断">
          <div class="diag-card">
            <div class="diag-title">
              <span>route diagnostic</span>
              <span>client down</span>
            </div>

            <div class="diag">
              <div class="diag-step">
                <img class="diag-icon" src="/static/error-icons/browser-ok.svg" alt="浏览器正常">
                <p class="diag-name">浏览器</p>
                <p class="diag-result ok">正常</p>
              </div>

              <div class="diag-step">
                <img class="diag-icon" src="/static/error-icons/server-ok.svg" alt="服务器正常">
                <p class="diag-name">服务器</p>
                <p class="diag-result ok">正常</p>
              </div>

              <div class="diag-step">
                <img class="diag-icon" src="/static/error-icons/client-down.svg" alt="客户端断开">
                <p class="diag-name">客户端</p>
                <p class="diag-result bad">断开</p>
              </div>
            </div>
          </div>

          <div class="log">
            <p><span class="dim">$</span> lookup route __TOKEN_TEXT__</p>
            <p class="bad">__DETAIL__</p>
            <p class="ok">hint: reconnect the SSH tunnel and refresh</p>
          </div>
        </aside>
      </section>
    </main>
  </div>
</body>
</html>
`

var _ = fmt.Sprintf
