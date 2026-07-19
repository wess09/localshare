package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"localshare/internal/domain"
	"localshare/internal/store/ent"
	"localshare/internal/store/ent/route"
)

func (s *Store) RegisterRoute(ctx context.Context, item domain.Route) (domain.Route, error) {
	item.Token = strings.ToLower(item.Token)
	if !domain.IsHashToken(item.Token) {
		return domain.Route{}, domain.ErrInvalidInput
	}
	nodeValue, err := s.GetNode(ctx, item.NodeID)
	if err != nil {
		return domain.Route{}, err
	}
	_ = nodeValue
	existing, err := s.GetRoute(ctx, item.Token)
	if err == nil && s.routeActive(existing) && existing.NodeID != item.NodeID {
		existingNode, err := s.GetNode(ctx, existing.NodeID)
		if err == nil && s.availableForExistingRoute(existingNode) {
			return domain.Route{}, domain.ErrRouteOnAnotherNode
		}
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
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
	r, err := s.client.Route.Query().Where(route.TokenEQ(item.Token)).Only(ctx)
	if ent.IsNotFound(err) {
		created, err := s.client.Route.Create().
			SetToken(item.Token).
			SetNodeID(item.NodeID).
			SetTargetURL(item.TargetURL).
			SetPublicURL(item.PublicURL).
			SetPeerID(item.PeerID).
			SetStatus(item.Status).
			SetExpiresAt(item.ExpiresAt).
			Save(ctx)
		if err != nil {
			return domain.Route{}, err
		}
		return fromEntRoute(created), nil
	}
	if err != nil {
		return domain.Route{}, err
	}
	updated, err := r.Update().
		SetNodeID(item.NodeID).
		SetTargetURL(item.TargetURL).
		SetPublicURL(item.PublicURL).
		SetPeerID(item.PeerID).
		SetStatus(item.Status).
		SetExpiresAt(item.ExpiresAt).
		Save(ctx)
	if err != nil {
		return domain.Route{}, err
	}
	return fromEntRoute(updated), nil
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
