package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
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
		writeJSON(w, http.StatusOK, s.cluster.Stats())
	case r.URL.Path == "/api/v1/nodes" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		nodes, err := s.repo.ListNodes(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	case r.URL.Path == "/api/v1/routes" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
			return
		}
		routes, err := s.repo.ListRoutes(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
	case r.URL.Path == "/api/v1/audit-events" && r.Method == http.MethodGet:
		if !s.requireAdmin(w, r) {
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
	_, _ = w.Write([]byte(html))
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
		writeJSON(w, http.StatusOK, s.cluster.Stats())
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
		http.NotFound(w, r)
		return
	}
	routeValue, err := s.repo.GetRoute(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Route not found"})
		return
	}
	if routeValue.Status != domain.RouteStatusActive || routeValue.ExpiresAt.Before(time.Now()) {
		writeJSON(w, http.StatusGone, map[string]any{"error": "Route expired"})
		return
	}
	nodeValue, err := s.repo.GetNode(r.Context(), routeValue.NodeID)
	if err != nil || !nodeValue.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Node unavailable"})
		return
	}
	if nodeValue.NodeID == s.cfg.LocalNodeID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Local route is not available through control endpoint"})
		return
	}
	target := config.URLJoin(nodeValue.PublicBaseURL, token)
	if len(parts) == 2 {
		target = config.URLJoin(target, parts[1])
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		payload := map[string]any{"type": "stats", "data": s.cluster.Stats()}
		if err := conn.WriteJSON(payload); err != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *HTTPServer) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.isAdmin(r) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Unauthorized"})
	return false
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

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(data)
}

var p2pHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Localshare P2P</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f6f8fb;color:#18202a;font:15px/1.6 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.panel{width:min(680px,calc(100% - 32px));border:1px solid #dfe4ea;border-radius:8px;background:#fff;padding:28px;box-shadow:0 18px 42px rgba(20,30,50,.09)}h1{margin:0 0 12px;font-size:28px}.muted{color:#687385}.btn{display:inline-flex;margin-top:18px;padding:9px 13px;border:1px solid #2563eb;border-radius:6px;background:#2563eb;color:#fff;text-decoration:none}</style></head>
<body><main class="panel"><h1>正在建立连接</h1><p class="muted" id="status">正在连接信令服务，若 P2P 不可用会自动切换到 SSH 转发。</p><a class="btn" href="/__PEER_ID__">打开 SSH 转发入口</a></main>
<script>(()=>{const peerId="__PEER_ID__";const fallback="/"+peerId;const signalUrl=(location.protocol==="https:"?"wss://":"ws://")+location.host+"/signal";const status=document.getElementById("status");let done=false;function fail(x){if(done)return;done=true;status.textContent="P2P 当前不可用，切换到 SSH 转发："+x;setTimeout(()=>location.replace(fallback),500)}try{const ws=new WebSocket(signalUrl);ws.onopen=()=>{status.textContent="信令服务已连接，等待客户端 P2P 能力。";ws.send(JSON.stringify({type:"browser",peer_id:peerId,viewer_id:(crypto.randomUUID?crypto.randomUUID():String(Date.now()))}))};ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.type==="error")fail(m.message||"信令错误")};ws.onerror=()=>fail("信令连接失败");setTimeout(()=>fail("10 秒内未完成 P2P 协商"),10000)}catch(e){fail(String(e))}})();</script></body></html>`

var _ = fmt.Sprintf
