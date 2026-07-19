package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"text/template"
	"time"

	"localshare/internal/config"
	"localshare/internal/domain"
	"localshare/internal/service"
	"localshare/internal/store"
)

func TestV1AdminControlEndpoints(t *testing.T) {
	repo := &fakeRepo{
		nodes: map[string]domain.Node{
			"master": {
				NodeID:               "master",
				Enabled:              true,
				Healthy:              true,
				Eligible:             true,
				Weight:               100,
				MaxTunnels:           10,
				CurrentTunnels:       2,
				MaxActiveConnections: 20,
				ActiveConnections:    4,
			},
		},
	}
	server := newTestHTTPServer(t, repo)

	res := doJSON(server, http.MethodGet, "/api/v1/capacity", "secret", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("capacity status = %d, body = %s", res.Code, res.Body.String())
	}
	var capacity struct {
		Capacity domain.Capacity `json:"capacity"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &capacity); err != nil {
		t.Fatalf("decode capacity: %v", err)
	}
	if capacity.Capacity.Nodes != 1 || capacity.Capacity.CurrentTunnels != 2 {
		t.Fatalf("unexpected capacity: %#v", capacity.Capacity)
	}

	res = doJSON(server, http.MethodPatch, "/api/v1/nodes/master/weight", "secret", map[string]any{"weight": 7})
	if res.Code != http.StatusOK {
		t.Fatalf("weight status = %d, body = %s", res.Code, res.Body.String())
	}
	res = doJSON(server, http.MethodPatch, "/api/v1/nodes/master/maintenance", "secret", map[string]any{"maintenance": true})
	if res.Code != http.StatusOK {
		t.Fatalf("maintenance status = %d, body = %s", res.Code, res.Body.String())
	}
	res = doJSON(server, http.MethodPatch, "/api/v1/nodes/master/capacity", "secret", map[string]any{"max_tunnels": 3, "max_active_connections": 4})
	if res.Code != http.StatusOK {
		t.Fatalf("capacity patch status = %d, body = %s", res.Code, res.Body.String())
	}

	node := repo.nodes["master"]
	if node.Weight != 7 || !node.Maintenance || node.MaxTunnels != 3 || node.MaxActiveConnections != 4 {
		t.Fatalf("node patch mismatch: %#v", node)
	}
	res = doJSON(server, http.MethodDelete, "/api/v1/nodes/master", "secret", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", res.Code, res.Body.String())
	}
	if _, ok := repo.nodes["master"]; ok {
		t.Fatal("node still exists after delete")
	}
	if len(repo.audit) != 4 {
		t.Fatalf("audit events = %d, want 4", len(repo.audit))
	}
}

func TestV1AdminRequiresAuth(t *testing.T) {
	server := newTestHTTPServer(t, &fakeRepo{nodes: map[string]domain.Node{}})
	res := doJSON(server, http.MethodGet, "/api/v1/capacity", "", nil)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}

func TestClusterRouteMissReturnsHTMLPage(t *testing.T) {
	server := newTestHTTPServer(t, &fakeRepo{nodes: map[string]domain.Node{}, routes: map[string]domain.Route{}})
	res := doJSON(server, http.MethodGet, "/__cluster_route__/0123456789abcdef", "", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
	if contentType := res.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("content-type = %q, want html", contentType)
	}
	body := res.Body.String()
	if !strings.Contains(body, "隧道睡着了") || !strings.Contains(body, "/static/error-icons/client-down.svg") || strings.Contains(body, `"error"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestNginxNodeTunnelFailuresUseHTMLPage(t *testing.T) {
	var out bytes.Buffer
	tpl, err := template.New("nginx").Parse(nginxTemplate)
	if err != nil {
		t.Fatalf("parse nginx template: %v", err)
	}
	err = tpl.Execute(&out, nginxConfig{SocketDir: "/tmp/localshare", Role: "node", ServerName: "example.com"})
	if err != nil {
		t.Fatalf("execute nginx template: %v", err)
	}
	conf := out.String()
	for _, want := range []string{
		"location = /__route_unavailable__",
		"proxy_pass http://127.0.0.1:8080/__route_unavailable__;",
		"error_page 502 504 = /__route_unavailable__;",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("nginx config missing %q:\n%s", want, conf)
		}
	}
}

func newTestHTTPServer(t *testing.T, repo *fakeRepo) *HTTPServer {
	t.Helper()
	cfg := &config.Config{
		ServerName:              "example.com",
		ServerPort:              1022,
		ClusterPublicBaseURL:    "https://example.com",
		NodePublicBaseURL:       "https://example.com",
		LocalNodeID:             "master",
		Role:                    domain.RoleMaster,
		AdminAPIToken:           "secret",
		RouteTTL:                time.Minute,
		NodeHeartbeatTimeout:    time.Minute,
		MaxSSHConnections:       100,
		MaxSignalConnections:    100,
		MaxSignalViewersPerPeer: 64,
	}
	state := service.NewState()
	metrics := service.NewMetrics()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cluster := service.NewClusterService(cfg, repo, state, metrics, log)
	auth := service.NewAuthService(repo, metrics)
	signal := service.NewSignalHub(cfg, state, metrics, log)
	server, err := NewHTTPServer(cfg, repo, cluster, auth, signal)
	if err != nil {
		t.Fatalf("new HTTP server: %v", err)
	}
	return server
}

func doJSON(handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	var payload bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	req := httptest.NewRequest(method, path, &payload)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

type fakeRepo struct {
	nodes    map[string]domain.Node
	routes   map[string]domain.Route
	settings map[string]string
	audit    []store.AuditEvent
}

func (f *fakeRepo) UpsertNode(_ context.Context, n domain.Node) (domain.Node, error) {
	if f.nodes == nil {
		f.nodes = map[string]domain.Node{}
	}
	f.nodes[n.NodeID] = n
	return n, nil
}

func (f *fakeRepo) PatchNode(_ context.Context, nodeID string, patch domain.NodePatch) (domain.Node, error) {
	n, ok := f.nodes[nodeID]
	if !ok {
		return domain.Node{}, domain.ErrNotFound
	}
	if patch.Enabled != nil {
		n.Enabled = *patch.Enabled
	}
	if patch.Maintenance != nil {
		n.Maintenance = *patch.Maintenance
	}
	if patch.Weight != nil {
		n.Weight = *patch.Weight
	}
	if patch.MaxTunnels != nil {
		n.MaxTunnels = *patch.MaxTunnels
	}
	if patch.MaxActiveConnections != nil {
		n.MaxActiveConnections = *patch.MaxActiveConnections
	}
	if patch.Region != nil {
		n.Region = *patch.Region
	}
	f.nodes[nodeID] = n
	return n, nil
}

func (f *fakeRepo) DeleteNode(_ context.Context, nodeID string) error {
	if _, ok := f.nodes[nodeID]; !ok {
		return domain.ErrNotFound
	}
	delete(f.nodes, nodeID)
	for token, route := range f.routes {
		if route.NodeID == nodeID {
			delete(f.routes, token)
		}
	}
	return nil
}

func (f *fakeRepo) UpdateHeartbeat(_ context.Context, nodeID string, n domain.Node) (domain.Node, error) {
	current, ok := f.nodes[nodeID]
	if !ok {
		return domain.Node{}, domain.ErrNotFound
	}
	current.CurrentTunnels = n.CurrentTunnels
	current.ActiveConnections = n.ActiveConnections
	current.LastHeartbeat = time.Now()
	f.nodes[nodeID] = current
	return current, nil
}

func (f *fakeRepo) UpdateLocalCounts(_ context.Context, nodeID string, tunnels, connections int) (domain.Node, error) {
	n, ok := f.nodes[nodeID]
	if !ok {
		return domain.Node{}, domain.ErrNotFound
	}
	n.CurrentTunnels = tunnels
	n.ActiveConnections = connections
	f.nodes[nodeID] = n
	return n, nil
}

func (f *fakeRepo) GetNode(_ context.Context, nodeID string) (domain.Node, error) {
	n, ok := f.nodes[nodeID]
	if !ok {
		return domain.Node{}, domain.ErrNotFound
	}
	return n, nil
}

func (f *fakeRepo) ListNodes(context.Context) ([]domain.Node, error) {
	out := make([]domain.Node, 0, len(f.nodes))
	for _, n := range f.nodes {
		out = append(out, n)
	}
	return out, nil
}

func (f *fakeRepo) RegisterRoute(_ context.Context, r domain.Route) (domain.Route, error) {
	if f.routes == nil {
		f.routes = map[string]domain.Route{}
	}
	f.routes[r.Token] = r
	return r, nil
}

func (f *fakeRepo) DeleteRoute(_ context.Context, token string) error {
	delete(f.routes, token)
	return nil
}

func (f *fakeRepo) GetRoute(_ context.Context, token string) (domain.Route, error) {
	r, ok := f.routes[token]
	if !ok {
		return domain.Route{}, domain.ErrNotFound
	}
	return r, nil
}

func (f *fakeRepo) ListRoutes(context.Context) ([]domain.Route, error) {
	out := make([]domain.Route, 0, len(f.routes))
	for _, r := range f.routes {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRepo) CleanupExpiredRoutes(context.Context, time.Duration) error { return nil }

func (f *fakeRepo) SelectNodeForToken(context.Context, string) (domain.Node, string, error) {
	for _, n := range f.nodes {
		return n, "weighted_least_connection", nil
	}
	return domain.Node{}, "no_available_node", domain.ErrUnavailable
}

func (f *fakeRepo) EnsureAdminUser(context.Context, string, string) error  { return nil }
func (f *fakeRepo) SetAdminPassword(context.Context, string, string) error { return nil }
func (f *fakeRepo) ValidateAdminPassword(context.Context, string) (bool, error) {
	return false, domain.ErrUnauthorized
}
func (f *fakeRepo) AdminPasswordHash(context.Context, string) (string, error) {
	return "", domain.ErrNotFound
}
func (f *fakeRepo) CreateAdminSession(context.Context, string, time.Time) error { return nil }
func (f *fakeRepo) GetAdminSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, domain.ErrUnauthorized
}
func (f *fakeRepo) DeleteAdminSession(context.Context, string) error { return nil }
func (f *fakeRepo) CleanupAdminSessions(context.Context, time.Time) error {
	return nil
}

func (f *fakeRepo) UpsertClusterSetting(_ context.Context, key, value string) error {
	if f.settings == nil {
		f.settings = map[string]string{}
	}
	f.settings[key] = value
	return nil
}

func (f *fakeRepo) GetClusterSetting(_ context.Context, key string) (string, error) {
	value, ok := f.settings[key]
	if !ok {
		return "", domain.ErrNotFound
	}
	return value, nil
}

func (f *fakeRepo) ListClusterSettings(context.Context) ([]store.ClusterSetting, error) {
	out := make([]store.ClusterSetting, 0, len(f.settings))
	for k, v := range f.settings {
		out = append(out, store.ClusterSetting{Key: k, Value: v, UpdatedAt: time.Now()})
	}
	return out, nil
}

func (f *fakeRepo) ListAuditEvents(context.Context, int) ([]store.AuditEvent, error) {
	return f.audit, nil
}

func (f *fakeRepo) LogAuditEvent(_ context.Context, event store.AuditEvent) error {
	f.audit = append(f.audit, event)
	return nil
}

func (f *fakeRepo) Close() error { return nil }
