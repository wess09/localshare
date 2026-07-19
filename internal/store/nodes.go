package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"localshare/internal/config"
	"localshare/internal/domain"
	"localshare/internal/store/ent"
	"localshare/internal/store/ent/node"
	"localshare/internal/store/ent/route"
)

func (s *Store) UpsertNode(ctx context.Context, item domain.Node) (domain.Node, error) {
	now := time.Now()
	item.NodeID = strings.TrimSpace(item.NodeID)
	if item.NodeID == "" {
		return domain.Node{}, domain.ErrInvalidInput
	}
	if item.Weight <= 0 {
		item.Weight = 100
	}
	if item.MaxTunnels < 0 {
		item.MaxTunnels = 0
	}
	if item.Region == "" {
		item.Region = "default"
	}
	item.PublicBaseURL = config.NormalizeBaseURL(item.PublicBaseURL)
	existing, err := s.client.Node.Query().Where(node.NodeIDEQ(item.NodeID)).Only(ctx)
	if ent.IsNotFound(err) {
		created, err := s.client.Node.Create().
			SetNodeID(item.NodeID).
			SetSSHServer(item.SSHServer).
			SetPublicBaseURL(item.PublicBaseURL).
			SetWeight(item.Weight).
			SetEnabled(item.Enabled).
			SetMaintenance(item.Maintenance).
			SetMaxTunnels(max(0, item.MaxTunnels)).
			SetCurrentTunnels(max(0, item.CurrentTunnels)).
			SetMaxActiveConnections(max(0, item.MaxActiveConnections)).
			SetActiveConnections(max(0, item.ActiveConnections)).
			SetRegion(item.Region).
			SetToken(item.Token).
			SetLastHeartbeat(nonZeroTime(item.LastHeartbeat, now)).
			SetIsLocal(item.IsLocal).
			Save(ctx)
		if err != nil {
			return domain.Node{}, err
		}
		return s.formatNode(fromEntNode(created), false), nil
	}
	if err != nil {
		return domain.Node{}, err
	}
	updated, err := existing.Update().
		SetSSHServer(item.SSHServer).
		SetPublicBaseURL(item.PublicBaseURL).
		SetWeight(item.Weight).
		SetEnabled(item.Enabled).
		SetMaintenance(item.Maintenance).
		SetMaxTunnels(max(0, item.MaxTunnels)).
		SetCurrentTunnels(max(0, item.CurrentTunnels)).
		SetMaxActiveConnections(max(0, item.MaxActiveConnections)).
		SetActiveConnections(max(0, item.ActiveConnections)).
		SetRegion(item.Region).
		SetToken(item.Token).
		SetLastHeartbeat(nonZeroTime(item.LastHeartbeat, now)).
		SetIsLocal(item.IsLocal).
		Save(ctx)
	if err != nil {
		return domain.Node{}, err
	}
	return s.formatNode(fromEntNode(updated), false), nil
}

func (s *Store) PatchNode(ctx context.Context, nodeID string, patch domain.NodePatch) (domain.Node, error) {
	n, err := s.client.Node.Query().Where(node.NodeIDEQ(nodeID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Node{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Node{}, err
	}
	up := n.Update()
	if patch.Enabled != nil {
		up.SetEnabled(*patch.Enabled)
	}
	if patch.Maintenance != nil {
		up.SetMaintenance(*patch.Maintenance)
	}
	if patch.Weight != nil {
		up.SetWeight(max(1, *patch.Weight))
	}
	if patch.MaxTunnels != nil {
		up.SetMaxTunnels(max(0, *patch.MaxTunnels))
	}
	if patch.MaxActiveConnections != nil {
		up.SetMaxActiveConnections(max(0, *patch.MaxActiveConnections))
	}
	if patch.Region != nil {
		region := strings.TrimSpace(*patch.Region)
		if region == "" {
			region = "default"
		}
		up.SetRegion(region)
	}
	updated, err := up.Save(ctx)
	if err != nil {
		return domain.Node{}, err
	}
	return s.formatNode(fromEntNode(updated), false), nil
}

func (s *Store) DeleteNode(ctx context.Context, nodeID string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback()
		}
	}()
	deleted, err := tx.Node.Delete().Where(node.NodeIDEQ(nodeID)).Exec(ctx)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return domain.ErrNotFound
	}
	if _, err := tx.Route.Delete().Where(route.NodeIDEQ(nodeID)).Exec(ctx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	commit = true
	return nil
}

func (s *Store) UpdateHeartbeat(ctx context.Context, nodeID string, payload domain.Node) (domain.Node, error) {
	n, err := s.client.Node.Query().Where(node.NodeIDEQ(nodeID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Node{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Node{}, err
	}
	up := n.Update().
		SetCurrentTunnels(max(0, payload.CurrentTunnels)).
		SetActiveConnections(max(0, payload.ActiveConnections)).
		SetLastHeartbeat(time.Now())
	if payload.MaxTunnels > 0 {
		up.SetMaxTunnels(payload.MaxTunnels)
	}
	if payload.MaxActiveConnections >= 0 {
		up.SetMaxActiveConnections(payload.MaxActiveConnections)
	}
	updated, err := up.Save(ctx)
	if err != nil {
		return domain.Node{}, err
	}
	return s.formatNode(fromEntNode(updated), false), nil
}

func (s *Store) UpdateLocalCounts(ctx context.Context, nodeID string, tunnels, connections int) (domain.Node, error) {
	n, err := s.client.Node.Query().Where(node.NodeIDEQ(nodeID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Node{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Node{}, err
	}
	updated, err := n.Update().
		SetCurrentTunnels(max(0, tunnels)).
		SetActiveConnections(max(0, connections)).
		SetLastHeartbeat(time.Now()).
		Save(ctx)
	if err != nil {
		return domain.Node{}, err
	}
	return s.formatNode(fromEntNode(updated), false), nil
}

func (s *Store) GetNode(ctx context.Context, nodeID string) (domain.Node, error) {
	n, err := s.client.Node.Query().Where(node.NodeIDEQ(nodeID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Node{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Node{}, err
	}
	return s.formatNode(fromEntNode(n), true), nil
}

func (s *Store) ListNodes(ctx context.Context) ([]domain.Node, error) {
	nodes, err := s.client.Node.Query().Order(ent.Asc(node.FieldNodeID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, s.formatNode(fromEntNode(n), false))
	}
	return out, nil
}

func (s *Store) SelectNodeForToken(ctx context.Context, token string) (domain.Node, string, error) {
	r, err := s.GetRoute(ctx, token)
	if err == nil && s.routeActive(r) {
		n, err := s.GetNode(ctx, r.NodeID)
		if err == nil && s.availableForExistingRoute(n) {
			return n, "existing_route", nil
		}
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Node{}, "", err
	}
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return domain.Node{}, "", err
	}
	candidates := make([]domain.Node, 0, len(nodes))
	for _, n := range nodes {
		if s.eligible(n) {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return domain.Node{}, "no_available_node", domain.ErrUnavailable
	}
	sort.Slice(candidates, func(i, j int) bool {
		a := float64(candidates[i].CurrentTunnels) / float64(max(1, candidates[i].Weight))
		b := float64(candidates[j].CurrentTunnels) / float64(max(1, candidates[j].Weight))
		if a != b {
			return a < b
		}
		if candidates[i].Weight != candidates[j].Weight {
			return candidates[i].Weight > candidates[j].Weight
		}
		return candidates[i].NodeID < candidates[j].NodeID
	})
	return candidates[0], "weighted_least_connection", nil
}

func fromEntNode(n *ent.Node) domain.Node {
	return domain.Node{
		ID:                   n.ID,
		NodeID:               n.NodeID,
		SSHServer:            n.SSHServer,
		PublicBaseURL:        n.PublicBaseURL,
		Weight:               n.Weight,
		Enabled:              n.Enabled,
		Maintenance:          n.Maintenance,
		MaxTunnels:           n.MaxTunnels,
		CurrentTunnels:       n.CurrentTunnels,
		MaxActiveConnections: n.MaxActiveConnections,
		ActiveConnections:    n.ActiveConnections,
		Region:               n.Region,
		Token:                n.Token,
		LastHeartbeat:        n.LastHeartbeat,
		IsLocal:              n.IsLocal,
		CreatedAt:            n.CreatedAt,
		UpdatedAt:            n.UpdatedAt,
	}
}

func (s *Store) formatNode(n domain.Node, includeToken bool) domain.Node {
	n.Healthy = s.healthy(n)
	n.Eligible = s.eligible(n)
	n.Score = float64(n.CurrentTunnels) / float64(max(1, n.Weight))
	if !includeToken {
		n.Token = ""
	}
	return n
}

func (s *Store) healthy(n domain.Node) bool {
	if n.IsLocal {
		return true
	}
	return !n.LastHeartbeat.IsZero() && time.Since(n.LastHeartbeat) <= s.cfg.NodeHeartbeatTimeout
}

func (s *Store) eligible(n domain.Node) bool {
	if !n.Enabled || n.Maintenance || !s.healthy(n) {
		return false
	}
	if n.MaxTunnels <= n.CurrentTunnels {
		return false
	}
	if n.MaxActiveConnections > 0 && n.ActiveConnections >= n.MaxActiveConnections {
		return false
	}
	return true
}

func (s *Store) availableForExistingRoute(n domain.Node) bool {
	return n.Enabled && s.healthy(n)
}

func nonZeroTime(value time.Time, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
