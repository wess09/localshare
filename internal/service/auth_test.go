package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"localshare/internal/config"
	"localshare/internal/domain"
	"localshare/internal/store"
)

func TestAuthSetupAndLogin(t *testing.T) {
	repo := &fakeRepo{}
	auth := NewAuthService(repo, NewMetrics())
	ctx := context.Background()
	if !auth.SetupRequired(ctx) {
		t.Fatal("expected setup to be required")
	}
	if err := auth.Setup(ctx, "correct horse battery staple"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if auth.SetupRequired(ctx) {
		t.Fatal("expected setup to be complete")
	}
	sid, expires, err := auth.Login(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if sid == "" || time.Until(expires) <= 0 {
		t.Fatalf("invalid session: %q %v", sid, expires)
	}
	if _, _, err := auth.Login(ctx, "wrong password"); err == nil {
		t.Fatal("wrong password unexpectedly succeeded")
	}
}

func TestClusterScheduleDelegatesToRepository(t *testing.T) {
	repo := &fakeRepo{selected: domain.Node{NodeID: "master"}}
	cfg := testConfig()
	cluster := NewClusterService(cfg, repo, NewState(), NewMetrics(), discardLogger())
	node, reason, err := cluster.Schedule(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("schedule failed: %v", err)
	}
	if node.NodeID != "master" || reason != "weighted_least_connection" {
		t.Fatalf("unexpected schedule result: %#v %s", node, reason)
	}
}

func TestClusterCapacity(t *testing.T) {
	repo := &fakeRepo{nodes: []domain.Node{
		{
			NodeID:               "a",
			Healthy:              true,
			Eligible:             true,
			CurrentTunnels:       2,
			MaxTunnels:           10,
			ActiveConnections:    3,
			MaxActiveConnections: 30,
		},
		{
			NodeID:               "b",
			Healthy:              true,
			Eligible:             false,
			CurrentTunnels:       4,
			MaxTunnels:           20,
			ActiveConnections:    5,
			MaxActiveConnections: 0,
		},
	}}
	cluster := NewClusterService(testConfig(), repo, NewState(), NewMetrics(), discardLogger())

	got, err := cluster.Capacity(context.Background())
	if err != nil {
		t.Fatalf("capacity failed: %v", err)
	}
	if got.Nodes != 2 || got.HealthyNodes != 2 || got.EligibleNodes != 1 {
		t.Fatalf("unexpected node counts: %#v", got)
	}
	if got.CurrentTunnels != 6 || got.MaxTunnels != 30 {
		t.Fatalf("unexpected tunnel capacity: %#v", got)
	}
	if got.ActiveConnections != 8 || got.MaxActiveConnections != 30 || got.UnlimitedActiveNodes != 1 {
		t.Fatalf("unexpected connection capacity: %#v", got)
	}
	if got.TunnelUtilization != 0.2 {
		t.Fatalf("unexpected utilization: %#v", got)
	}
}

func TestNodeHeartbeatUsesNodeWeight(t *testing.T) {
	cfg := testConfig()
	cfg.Role = domain.RoleNode
	cfg.MasterWorkerWeight = 100
	cfg.NodeWorkerWeight = 7
	cfg.NodeMaxTunnels = 9
	cfg.NodeMaxActiveConns = 11
	cluster := NewClusterService(cfg, &fakeRepo{}, NewState(), NewMetrics(), discardLogger())

	got := cluster.HeartbeatPayload()
	if got["weight"] != 7 {
		t.Fatalf("weight = %v, want node weight", got["weight"])
	}
	if got["max_tunnels"] != 9 {
		t.Fatalf("max_tunnels = %v, want node tunnel capacity", got["max_tunnels"])
	}
	if got["max_active_connections"] != 11 {
		t.Fatalf("max_active_connections = %v, want node active capacity", got["max_active_connections"])
	}
}

func testConfig() *config.Config {
	return &config.Config{
		Role:                    domain.RoleMaster,
		LocalNodeID:             "master",
		ServerName:              "example.com",
		ServerPort:              1022,
		ClusterPublicBaseURL:    "https://example.com",
		NodePublicBaseURL:       "https://example.com",
		RouteTTL:                time.Minute,
		NodeHeartbeatInterval:   time.Second,
		NodeHeartbeatTimeout:    30 * time.Second,
		ExpiredRouteRetention:   time.Hour,
		MaxSSHConnections:       100,
		MaxSignalConnections:    100,
		MaxSignalViewersPerPeer: 64,
		MasterWorkerWeight:      100,
		MasterMaxTunnels:        100,
		MasterMaxActiveConns:    100,
		NodeWorkerWeight:        100,
		NodeMaxTunnels:          100,
		NodeMaxActiveConns:      100,
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeRepo struct {
	adminHash string
	sessions  map[string]time.Time
	selected  domain.Node
	nodes     []domain.Node
}

func (f *fakeRepo) UpsertNode(context.Context, domain.Node) (domain.Node, error) {
	return domain.Node{}, nil
}
func (f *fakeRepo) PatchNode(context.Context, string, domain.NodePatch) (domain.Node, error) {
	return domain.Node{}, nil
}
func (f *fakeRepo) DeleteNode(context.Context, string) error { return nil }
func (f *fakeRepo) UpdateHeartbeat(context.Context, string, domain.Node) (domain.Node, error) {
	return domain.Node{}, nil
}
func (f *fakeRepo) UpdateLocalCounts(context.Context, string, int, int) (domain.Node, error) {
	return domain.Node{}, nil
}
func (f *fakeRepo) GetNode(context.Context, string) (domain.Node, error) {
	return domain.Node{}, domain.ErrNotFound
}
func (f *fakeRepo) ListNodes(context.Context) ([]domain.Node, error) { return f.nodes, nil }
func (f *fakeRepo) RegisterRoute(context.Context, domain.Route) (domain.Route, error) {
	return domain.Route{}, nil
}
func (f *fakeRepo) DeleteRoute(context.Context, string) error { return nil }
func (f *fakeRepo) GetRoute(context.Context, string) (domain.Route, error) {
	return domain.Route{}, domain.ErrNotFound
}
func (f *fakeRepo) ListRoutes(context.Context) ([]domain.Route, error) { return nil, nil }
func (f *fakeRepo) CleanupExpiredRoutes(context.Context, time.Duration) error {
	return nil
}
func (f *fakeRepo) SelectNodeForToken(context.Context, string) (domain.Node, string, error) {
	return f.selected, "weighted_least_connection", nil
}
func (f *fakeRepo) EnsureAdminUser(context.Context, string, string) error { return nil }
func (f *fakeRepo) SetAdminPassword(_ context.Context, _ string, hash string) error {
	f.adminHash = hash
	return nil
}
func (f *fakeRepo) ValidateAdminPassword(context.Context, string) (bool, error) { return true, nil }
func (f *fakeRepo) AdminPasswordHash(context.Context, string) (string, error) {
	if f.adminHash == "" {
		return "", domain.ErrNotFound
	}
	return f.adminHash, nil
}
func (f *fakeRepo) CreateAdminSession(_ context.Context, sid string, expires time.Time) error {
	if f.sessions == nil {
		f.sessions = map[string]time.Time{}
	}
	f.sessions[sid] = expires
	return nil
}
func (f *fakeRepo) GetAdminSession(_ context.Context, sid string) (string, time.Time, error) {
	expires, ok := f.sessions[sid]
	if !ok {
		return "", time.Time{}, domain.ErrUnauthorized
	}
	return "admin", expires, nil
}
func (f *fakeRepo) DeleteAdminSession(context.Context, string) error { return nil }
func (f *fakeRepo) CleanupAdminSessions(context.Context, time.Time) error {
	return nil
}
func (f *fakeRepo) UpsertClusterSetting(context.Context, string, string) error { return nil }
func (f *fakeRepo) GetClusterSetting(context.Context, string) (string, error) {
	return "", domain.ErrNotFound
}
func (f *fakeRepo) ListClusterSettings(context.Context) ([]store.ClusterSetting, error) {
	return nil, nil
}
func (f *fakeRepo) ListAuditEvents(context.Context, int) ([]store.AuditEvent, error) {
	return nil, nil
}
func (f *fakeRepo) LogAuditEvent(context.Context, store.AuditEvent) error { return nil }
func (f *fakeRepo) Close() error                                          { return nil }
