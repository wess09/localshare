package store

import (
	"context"
	"strings"
	"time"

	"localshare/internal/domain"
	"localshare/internal/store/ent"
	"localshare/internal/store/ent/node"
	"localshare/internal/store/ent/route"
)

func (s *Store) RegisterRoute(ctx context.Context, item domain.Route) (domain.Route, error) {
	item.Token = strings.ToLower(item.Token)
	if !domain.IsHashToken(item.Token) {
		return domain.Route{}, domain.ErrInvalidInput
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return domain.Route{}, err
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback()
		}
	}()

	nodeValue, err := tx.Node.Query().Where(node.NodeIDEQ(item.NodeID)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Route{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Route{}, err
	}
	_ = nodeValue

	existingEnt, err := tx.Route.Query().Where(route.TokenEQ(item.Token)).Only(ctx)
	if err == nil {
		existing := fromEntRoute(existingEnt)
		if s.routeActive(existing) && existing.NodeID != item.NodeID {
			existingNode, err := tx.Node.Query().Where(node.NodeIDEQ(existing.NodeID)).Only(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return domain.Route{}, err
			}
			if err == nil && s.availableForExistingRoute(s.formatNode(fromEntNode(existingNode), true)) {
				return domain.Route{}, domain.ErrRouteOnAnotherNode
			}
		}
	} else if !ent.IsNotFound(err) {
		return domain.Route{}, err
	}
	if item.PeerID == "" {
		item.PeerID = item.Token
	}
	if item.Status == "" {
		item.Status = domain.RouteStatusActive
	}
	if item.ExpiresAt.IsZero() {
		item.ExpiresAt = time.Now().Add(s.cfg.RouteTTL)
	}

	var saved *ent.Route
	if existingEnt == nil {
		saved, err = tx.Route.Create().
			SetToken(item.Token).
			SetNodeID(item.NodeID).
			SetTargetURL(item.TargetURL).
			SetPublicURL(item.PublicURL).
			SetPeerID(item.PeerID).
			SetStatus(item.Status).
			SetExpiresAt(item.ExpiresAt).
			Save(ctx)
	} else {
		saved, err = existingEnt.Update().
			SetNodeID(item.NodeID).
			SetTargetURL(item.TargetURL).
			SetPublicURL(item.PublicURL).
			SetPeerID(item.PeerID).
			SetStatus(item.Status).
			SetExpiresAt(item.ExpiresAt).
			Save(ctx)
	}
	if err != nil {
		return domain.Route{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Route{}, err
	}
	commit = true
	return fromEntRoute(saved), nil
}

func (s *Store) DeleteRoute(ctx context.Context, token string) error {
	_, err := s.client.Route.Delete().Where(route.TokenEQ(strings.ToLower(token))).Exec(ctx)
	return err
}

func (s *Store) GetRoute(ctx context.Context, token string) (domain.Route, error) {
	r, err := s.client.Route.Query().Where(route.TokenEQ(strings.ToLower(token))).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Route{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Route{}, err
	}
	return fromEntRoute(r), nil
}

func (s *Store) ListRoutes(ctx context.Context) ([]domain.Route, error) {
	routes, err := s.client.Route.Query().Order(ent.Desc(route.FieldUpdatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Route, 0, len(routes))
	for _, r := range routes {
		out = append(out, fromEntRoute(r))
	}
	return out, nil
}

func (s *Store) CleanupExpiredRoutes(ctx context.Context, retention time.Duration) error {
	now := time.Now()
	if _, err := s.client.Route.Update().
		Where(route.StatusEQ(domain.RouteStatusActive), route.ExpiresAtLT(now)).
		SetStatus(domain.RouteStatusExpired).
		Save(ctx); err != nil {
		return err
	}
	_, err := s.client.Route.Delete().
		Where(route.StatusEQ(domain.RouteStatusExpired), route.UpdatedAtLT(now.Add(-retention))).
		Exec(ctx)
	return err
}

func (s *Store) routeActive(r domain.Route) bool {
	return r.Token != "" && r.Status == domain.RouteStatusActive && !r.ExpiresAt.Before(time.Now())
}

func fromEntRoute(r *ent.Route) domain.Route {
	return domain.Route{
		ID:        r.ID,
		Token:     r.Token,
		NodeID:    r.NodeID,
		TargetURL: r.TargetURL,
		PublicURL: r.PublicURL,
		PeerID:    r.PeerID,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		ExpiresAt: r.ExpiresAt,
	}
}
