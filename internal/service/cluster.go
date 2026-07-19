package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"localshare/internal/config"
	"localshare/internal/domain"
	"localshare/internal/store"
)

type ClusterService struct {
	cfg    *config.Config
	repo   store.Repository
	state  *State
	metric *Metrics
	log    *slog.Logger

	mu sync.RWMutex
}

func NewClusterService(cfg *config.Config, repo store.Repository, state *State, metric *Metrics, log *slog.Logger) *ClusterService {
	return &ClusterService{cfg: cfg, repo: repo, state: state, metric: metric, log: log}
}

func (s *ClusterService) Now() time.Time { return time.Now() }

func (s *ClusterService) SockName(username string) string {
	return domain.SockName(username)
}

func (s *ClusterService) BuildSuccessPayload(sockName string) domain.SuccessPayload {
	return domain.SuccessPayload{
		Address:         config.URLJoin(s.cfg.PublicBaseURL(), "p2p", sockName),
		FallbackAddress: config.URLJoin(s.cfg.PublicBaseURL(), sockName),
		PeerID:          sockName,
		SignalURL:       s.cfg.SignalURL(),
		ICEServers:      s.cfg.ICEServers(),
		Status:          "success",
	}
}

func (s *ClusterService) RoutePayload(sockName string) domain.Route {
	return domain.Route{
		Token:     sockName,
		NodeID:    s.cfg.LocalNodeID,
		TargetURL: config.URLJoin(s.cfg.NodePublicBaseURL, sockName),
		PublicURL: config.URLJoin(s.cfg.PublicBaseURL(), sockName),
		PeerID:    sockName,
		Status:    domain.RouteStatusActive,
		ExpiresAt: time.Now().Add(s.cfg.RouteTTL),
	}
}

func (s *ClusterService) Schedule(ctx context.Context, sockName string) (domain.Node, string, error) {
	if s.repo == nil {
		return domain.Node{}, "no_repository", domain.ErrUnavailable
	}
	s.metric.schedulerTotal++
	node, reason, err := s.repo.SelectNodeForToken(ctx, sockName)
	if err != nil {
		s.metric.schedulerFail++
		return domain.Node{}, reason, err
	}
	if node.NodeID == s.cfg.LocalNodeID {
		s.metric.schedulerLocal++
	} else {
		s.metric.schedulerRedirect++
	}
	return node, reason, nil
}

func (s *ClusterService) RegisterRoute(ctx context.Context, sockName string) error {
	if s.cfg.Role == domain.RoleStandalone {
		return nil
	}
	payload := s.RoutePayload(sockName)
	var err error
	switch s.cfg.Role {
	case domain.RoleMaster:
		_, err = s.repo.RegisterRoute(ctx, payload)
	case domain.RoleNode:
		err = s.registerRouteRemote(ctx, payload)
	default:
		err = nil
	}
	if err != nil {
		s.metric.routeRegisterFail++
		s.log.Error("route registration failed", "sock", sockName, "err", err)
		return err
	}
	s.metric.routeRegisterTotal++
	return nil
}

func (s *ClusterService) DeleteRoute(ctx context.Context, sockName string) error {
	if s.cfg.Role == domain.RoleStandalone {
		return nil
	}
	var err error
	switch s.cfg.Role {
	case domain.RoleMaster:
		err = s.repo.DeleteRoute(ctx, sockName)
	case domain.RoleNode:
		err = s.deleteRouteRemote(ctx, sockName)
	}
	if err != nil {
		s.log.Error("route delete failed", "sock", sockName, "err", err)
		return err
	}
	s.metric.routeDeleteTotal++
	return nil
}

func (s *ClusterService) HeartbeatPayload() map[string]any {
	weight := s.cfg.MasterWorkerWeight
	maxTunnels := s.cfg.MasterMaxTunnels
	maxActive := s.cfg.MasterMaxActiveConns
	if s.cfg.Role == domain.RoleNode {
		weight = s.cfg.MasterWorkerWeight
		maxTunnels = s.cfg.NodeMaxTunnels
		maxActive = s.cfg.NodeMaxActiveConns
	}
	return map[string]any{
		"node_id":                s.cfg.LocalNodeID,
		"token":                  s.cfg.NodeToken,
		"ssh_server":             fmt.Sprintf("%s:%d", s.cfg.ServerName, s.cfg.ServerPort),
		"public_base_url":        s.cfg.NodePublicBaseURL,
		"weight":                 weight,
		"region":                 "default",
		"current_tunnels":        s.state.ActiveTunnelCount(),
		"max_tunnels":            maxTunnels,
		"active_connections":     s.state.ActiveConnectionCount(),
		"max_active_connections": maxActive,
		"version":                s.cfg.Version,
	}
}

func (s *ClusterService) Stats() domain.Stats {
	nodes := []domain.Node{}
	routes := []domain.Route{}
	if s.repo != nil {
		if list, err := s.repo.ListNodes(context.Background()); err == nil {
			nodes = list
		}
		if list, err := s.repo.ListRoutes(context.Background()); err == nil {
			routes = list
		}
	}
	return domain.Stats{
		Now:    time.Now(),
		Uptime: time.Since(s.metric.startedAt).Seconds(),
		Role:   s.cfg.Role,
		NodeID: s.cfg.LocalNodeID,
		Limits: domain.Limits{
			SSH:            s.cfg.MaxSSHConnections,
			Signal:         s.cfg.MaxSignalConnections,
			ViewersPerPeer: s.cfg.MaxSignalViewersPerPeer,
		},
		SSH: domain.SSHStats{
			Active:   s.state.ActiveConnectionCount(),
			Peers:    s.state.ActivePeerCount(),
			Total:    int(s.metric.sshTotal),
			Rejected: int(s.metric.sshRejected),
			Replaced: int(s.metric.sshReplaced),
		},
		Signal: domain.SigStats{
			Peers:       s.state.SignalPeerCount(),
			Viewers:     s.state.SignalViewerCount(),
			Total:       int(s.metric.signalTotal),
			Rejected:    int(s.metric.signalRejected),
			MessagesIn:  s.metric.signalIn,
			MessagesOut: s.metric.signalOut,
			BytesIn:     s.metric.signalBytesIn,
			BytesOut:    s.metric.signalBytesOut,
			ViewerTotal: s.metric.viewerTotal,
		},
		HTTP: domain.HTTPStats{
			P2PPages:     s.metric.p2pPages,
			P2PPageBytes: s.metric.p2pPageBytes,
		},
		Admin: domain.AdminStat{
			Logins:      s.metric.adminLogins,
			FailedLogin: s.metric.adminFailedLogins,
		},
		Cluster: domain.Cluster{
			SchedulerTotal:     s.metric.schedulerTotal,
			SchedulerRedirect:  s.metric.schedulerRedirect,
			SchedulerLocal:     s.metric.schedulerLocal,
			SchedulerFail:      s.metric.schedulerFail,
			RouteRegisterTotal: s.metric.routeRegisterTotal,
			RouteRegisterFail:  s.metric.routeRegisterFail,
			RouteDeleteTotal:   s.metric.routeDeleteTotal,
			RouteRedirectTotal: s.metric.routeRedirectTotal,
			RouteLookupMiss:    s.metric.routeLookupMiss,
			HeartbeatTotal:     s.metric.heartbeatTotal,
			HeartbeatFail:      s.metric.heartbeatFail,
			Nodes:              nodes,
			RoutesActive:       activeRoutes(routes),
			RoutesTotal:        len(routes),
		},
		Peers: s.peerSummaries(),
	}
}

func activeRoutes(routes []domain.Route) int {
	now := time.Now()
	n := 0
	for _, route := range routes {
		if route.Status == domain.RouteStatusActive && !route.ExpiresAt.Before(now) {
			n++
		}
	}
	return n
}

func (s *ClusterService) peerSummaries() []domain.Peer {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	ids := make([]string, 0, len(s.state.peerStats))
	for id := range s.state.peerStats {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]domain.Peer, 0, len(ids))
	for _, id := range ids {
		stat := s.state.peerStats[id]
		peer := domain.Peer{
			PeerID:            id,
			SSH:               s.state.activeSSHPeer(id) != nil,
			Signal:            s.state.signalPeer(id) != nil,
			Viewers:           s.state.viewerCount(id),
			FallbackURL:       s.cfg.PublicURL(id),
			CreatedAt:         stat.CreatedAt,
			LastSeen:          stat.LastSeen,
			SSHConnectedAt:    stat.SSHConnectedAt,
			SignalConnectedAt: stat.SignalConnectedAt,
			SSHConnections:    stat.SSHConnections,
			SignalConnections: stat.SignalConnections,
			ViewersTotal:      stat.ViewersTotal,
		}
		if peer.FallbackURL == "" {
			peer.FallbackURL = s.cfg.PublicURL(id)
		}
		out = append(out, peer)
	}
	return out
}

func (s *ClusterService) marshal(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (s *ClusterService) MaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(maxDuration(time.Second, s.cfg.NodeHeartbeatInterval))
	defer ticker.Stop()
	for {
		if err := s.maintenanceOnce(ctx); err != nil {
			s.metric.heartbeatFail++
			s.log.Error("cluster maintenance failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *ClusterService) maintenanceOnce(ctx context.Context) error {
	switch s.cfg.Role {
	case domain.RoleMaster:
		if s.repo == nil {
			return nil
		}
		if s.cfg.LocalNodeID != "" {
			if _, err := s.repo.UpdateLocalCounts(ctx, s.cfg.LocalNodeID, s.state.ActiveTunnelCount(), s.state.ActiveConnectionCount()); err != nil && err != domain.ErrNotFound {
				return err
			}
		}
		if err := s.repo.CleanupExpiredRoutes(ctx, s.cfg.ExpiredRouteRetention); err != nil {
			return err
		}
		for _, sock := range s.state.ActiveSockNames() {
			_ = s.RegisterRoute(ctx, sock)
		}
	case domain.RoleNode:
		if err := s.callMaster(ctx, http.MethodPost, "nodes/"+s.cfg.LocalNodeID+"/heartbeat", s.HeartbeatPayload(), s.cfg.NodeToken, nil); err != nil {
			if s.cfg.NodeRegistrationBearer != "" {
				if retryErr := s.callMaster(ctx, http.MethodPost, "nodes/"+s.cfg.LocalNodeID+"/heartbeat", s.HeartbeatPayload(), s.cfg.NodeRegistrationBearer, nil); retryErr == nil {
					s.metric.heartbeatTotal++
					return nil
				}
			}
			return err
		}
		s.metric.heartbeatTotal++
		for _, sock := range s.state.ActiveSockNames() {
			_ = s.RegisterRoute(ctx, sock)
		}
	}
	return nil
}

func (s *ClusterService) registerRouteRemote(ctx context.Context, route domain.Route) error {
	return s.callMaster(ctx, http.MethodPost, "routes", route, s.cfg.NodeToken, nil)
}

func (s *ClusterService) deleteRouteRemote(ctx context.Context, token string) error {
	return s.callMaster(ctx, http.MethodDelete, "routes/"+token, nil, s.cfg.NodeToken, nil)
}

func (s *ClusterService) callMaster(ctx context.Context, method, relPath string, body any, bearer string, out any) error {
	payload := []byte{}
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = data
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, config.URLJoin(s.cfg.MasterAPIURL, relPath), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("master api %s %s failed: %d %s", method, relPath, resp.StatusCode, string(respBody))
	}
	if out != nil && len(respBody) > 0 {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
