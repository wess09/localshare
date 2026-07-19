package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"golang.org/x/sync/errgroup"

	"localshare/internal/config"
	"localshare/internal/domain"
	"localshare/internal/service"
	"localshare/internal/store"
	"localshare/internal/transport"
)

type App struct {
	cfg     *config.Config
	log     *slog.Logger
	repo    store.Repository
	state   *service.State
	metrics *service.Metrics
	cluster *service.ClusterService
	auth    *service.AuthService
	signal  *service.SignalHub
	ssh     *service.SSHServer
	http    *http.Server
}

func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	repo, err := store.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	state := service.NewState()
	metrics := service.NewMetrics()
	cluster := service.NewClusterService(cfg, repo, state, metrics, log)
	auth := service.NewAuthService(repo, metrics)
	signal := service.NewSignalHub(cfg, state, metrics, log)
	sshServer := service.NewSSHServer(cfg, cluster, state, metrics, log)
	httpHandler, err := transport.NewHTTPServer(cfg, repo, cluster, auth, signal)
	if err != nil {
		_ = repo.Close()
		return nil, err
	}
	app := &App{
		cfg:     cfg,
		log:     log,
		repo:    repo,
		state:   state,
		metrics: metrics,
		cluster: cluster,
		auth:    auth,
		signal:  signal,
		ssh:     sshServer,
		http: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           httpHandler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      0,
			IdleTimeout:       120 * time.Second,
		},
	}
	if err := app.setupCluster(ctx); err != nil {
		_ = repo.Close()
		return nil, err
	}
	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.repo.Close()
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() (err error) {
		defer service.RecoverError(a.log, "http server", &err)
		a.log.Info("http server started", "addr", a.cfg.HTTPAddr)
		err = a.http.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
	group.Go(func() (err error) {
		defer service.RecoverError(a.log, "ssh server", &err)
		return a.ssh.ListenAndServe(ctx)
	})
	group.Go(func() (err error) {
		defer service.RecoverError(a.log, "signal cleanup", &err)
		a.signal.CleanupLoop(ctx)
		return nil
	})
	if a.cfg.Role == domain.RoleMaster || a.cfg.Role == domain.RoleNode {
		group.Go(func() (err error) {
			defer service.RecoverError(a.log, "cluster maintenance", &err)
			a.cluster.MaintenanceLoop(ctx)
			return nil
		})
	}
	group.Go(func() (err error) {
		defer service.RecoverError(a.log, "http shutdown", &err)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return a.http.Shutdown(shutdownCtx)
	})
	return group.Wait()
}

func (a *App) setupCluster(ctx context.Context) error {
	switch a.cfg.Role {
	case domain.RoleMaster:
		if a.cfg.MasterWorkerEnabled {
			_, err := a.repo.UpsertNode(ctx, domain.Node{
				NodeID:               a.cfg.LocalNodeID,
				SSHServer:            a.cfg.ServerName + ":" + itoa(a.cfg.ServerPort),
				PublicBaseURL:        a.cfg.NodePublicBaseURL,
				Weight:               a.cfg.MasterWorkerWeight,
				Enabled:              true,
				Maintenance:          false,
				MaxTunnels:           a.cfg.MasterMaxTunnels,
				MaxActiveConnections: a.cfg.MasterMaxActiveConns,
				Region:               "default",
				Token:                os.Getenv("MASTER_NODE_TOKEN"),
				IsLocal:              true,
			})
			if err != nil {
				return err
			}
		} else {
			enabled := false
			maintenance := true
			zero := 0
			_, _ = a.repo.PatchNode(ctx, a.cfg.LocalNodeID, domain.NodePatch{Enabled: &enabled, Maintenance: &maintenance, MaxTunnels: &zero})
		}
		return a.loadNodesFile(ctx)
	case domain.RoleStandalone:
		return nil
	case domain.RoleNode:
		return nil
	default:
		return nil
	}
}

func (a *App) loadNodesFile(ctx context.Context) error {
	data, err := os.ReadFile(a.cfg.NodesConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var wrapper struct {
		Nodes []domain.Node `json:"nodes"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		var nodes []domain.Node
		if err2 := json.Unmarshal(data, &nodes); err2 != nil {
			return err
		}
		wrapper.Nodes = nodes
	}
	for _, node := range wrapper.Nodes {
		node.IsLocal = false
		if _, err := a.repo.UpsertNode(ctx, node); err != nil {
			return err
		}
	}
	return nil
}

func itoa(v int) string {
	return strconvFormatInt(int64(v))
}

func strconvFormatInt(v int64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v%10]
		v /= 10
	}
	return string(buf[i:])
}
