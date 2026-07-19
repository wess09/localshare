package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"localshare/internal/config"
	"localshare/internal/domain"
)

func TestRepositoryIntegrationNodesAndRoutes(t *testing.T) {
	dsn := os.Getenv("LOCALSHARE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LOCALSHARE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	repo, err := New(ctx, &config.Config{
		DatabaseURL:          dsn,
		RouteTTL:             time.Minute,
		NodeHeartbeatTimeout: time.Hour,
	})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	suffix := time.Now().UTC().Format("20060102150405")
	nodeID := "it-node-" + suffix
	otherNodeID := "it-node-other-" + suffix
	token := domain.SockName(nodeID)

	nodeValue := domain.Node{
		NodeID:               nodeID,
		SSHServer:            "127.0.0.1:1022",
		PublicBaseURL:        "https://node.example.com",
		Weight:               10,
		Enabled:              true,
		MaxTunnels:           100,
		MaxActiveConnections: 200,
		Region:               "test",
		Token:                "node-token",
		LastHeartbeat:        time.Now(),
	}
	if _, err := repo.UpsertNode(ctx, nodeValue); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	enabled := false
	if _, err := repo.PatchNode(ctx, nodeID, domain.NodePatch{Enabled: &enabled}); err != nil {
		t.Fatalf("patch node: %v", err)
	}
	gotNode, err := repo.GetNode(ctx, nodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if gotNode.Enabled {
		t.Fatal("node enabled after patch")
	}
	enabled = true
	if _, err := repo.PatchNode(ctx, nodeID, domain.NodePatch{Enabled: &enabled}); err != nil {
		t.Fatalf("enable node: %v", err)
	}
	if _, err := repo.UpsertNode(ctx, domain.Node{
		NodeID:        otherNodeID,
		SSHServer:     "127.0.0.1:2022",
		PublicBaseURL: "https://other.example.com",
		Weight:        10,
		Enabled:       true,
		MaxTunnels:    100,
		LastHeartbeat: time.Now(),
	}); err != nil {
		t.Fatalf("upsert other node: %v", err)
	}

	routeValue := domain.Route{
		Token:     token,
		NodeID:    nodeID,
		TargetURL: "https://node.example.com/" + token,
		PublicURL: "https://example.com/" + token,
		PeerID:    token,
		Status:    domain.RouteStatusActive,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if _, err := repo.RegisterRoute(ctx, routeValue); err != nil {
		t.Fatalf("register route: %v", err)
	}
	routeValue.NodeID = otherNodeID
	if _, err := repo.RegisterRoute(ctx, routeValue); !errors.Is(err, domain.ErrRouteOnAnotherNode) {
		t.Fatalf("register conflicting route error = %v, want ErrRouteOnAnotherNode", err)
	}
	if err := repo.DeleteRoute(ctx, token); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if err := repo.DeleteNode(ctx, nodeID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	if err := repo.DeleteNode(ctx, otherNodeID); err != nil {
		t.Fatalf("delete other node: %v", err)
	}
}

func TestRepositoryIntegrationHeartbeatMetadata(t *testing.T) {
	dsn := os.Getenv("LOCALSHARE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LOCALSHARE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	repo, err := New(ctx, &config.Config{
		DatabaseURL:          dsn,
		RouteTTL:             time.Minute,
		NodeHeartbeatTimeout: time.Hour,
	})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	suffix := time.Now().UTC().Format("20060102150405")
	nodeID := "it-heartbeat-" + suffix
	_, err = repo.UpsertNode(ctx, domain.Node{
		NodeID:        nodeID,
		SSHServer:     "127.0.0.1:1022",
		PublicBaseURL: "https://old.example.com",
		Weight:        10,
		Enabled:       true,
		MaxTunnels:    100,
		LastHeartbeat: time.Now(),
	})
	if err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	updated, err := repo.UpdateHeartbeat(ctx, nodeID, domain.Node{
		SSHServer:            "10.0.0.1:1022",
		PublicBaseURL:        "https://new.example.com/base/",
		Weight:               7,
		Region:               "shanghai",
		CurrentTunnels:       3,
		ActiveConnections:    4,
		MaxTunnels:           9,
		MaxActiveConnections: 11,
	})
	if err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}
	if updated.SSHServer != "10.0.0.1:1022" {
		t.Fatalf("ssh_server = %q, want updated value", updated.SSHServer)
	}
	if updated.PublicBaseURL != "https://new.example.com/base" {
		t.Fatalf("public_base_url = %q, want normalized updated value", updated.PublicBaseURL)
	}
	if updated.Weight != 7 || updated.Region != "shanghai" {
		t.Fatalf("metadata mismatch: %#v", updated)
	}
	if updated.CurrentTunnels != 3 || updated.ActiveConnections != 4 {
		t.Fatalf("count mismatch: %#v", updated)
	}
	if updated.MaxTunnels != 9 || updated.MaxActiveConnections != 11 {
		t.Fatalf("capacity mismatch: %#v", updated)
	}
	if err := repo.DeleteNode(ctx, nodeID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
}
