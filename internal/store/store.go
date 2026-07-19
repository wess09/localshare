package store

import (
	"context"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"localshare/internal/config"
	"localshare/internal/domain"
	"localshare/internal/store/ent"
)

type Repository interface {
	UpsertNode(context.Context, domain.Node) (domain.Node, error)
	PatchNode(context.Context, string, domain.NodePatch) (domain.Node, error)
	DeleteNode(context.Context, string) error
	UpdateHeartbeat(context.Context, string, domain.Node) (domain.Node, error)
	UpdateLocalCounts(context.Context, string, int, int) (domain.Node, error)
	GetNode(context.Context, string) (domain.Node, error)
	ListNodes(context.Context) ([]domain.Node, error)
	RegisterRoute(context.Context, domain.Route) (domain.Route, error)
	DeleteRoute(context.Context, string) error
	GetRoute(context.Context, string) (domain.Route, error)
	ListRoutes(context.Context) ([]domain.Route, error)
	CleanupExpiredRoutes(context.Context, time.Duration) error
	SelectNodeForToken(context.Context, string) (domain.Node, string, error)
	EnsureAdminUser(context.Context, string, string) error
	SetAdminPassword(context.Context, string, string) error
	ValidateAdminPassword(context.Context, string) (bool, error)
	AdminPasswordHash(context.Context, string) (string, error)
	CreateAdminSession(context.Context, string, time.Time) error
	GetAdminSession(context.Context, string) (string, time.Time, error)
	DeleteAdminSession(context.Context, string) error
	CleanupAdminSessions(context.Context, time.Time) error
	UpsertClusterSetting(context.Context, string, string) error
	GetClusterSetting(context.Context, string) (string, error)
	ListClusterSettings(context.Context) ([]ClusterSetting, error)
	ListAuditEvents(context.Context, int) ([]AuditEvent, error)
	LogAuditEvent(context.Context, AuditEvent) error
	Close() error
}

type ClusterSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AuditEvent struct {
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Detail    map[string]any `json:"detail"`
	CreatedAt time.Time      `json:"created_at"`
}

type Store struct {
	client *ent.Client
	cfg    *config.Config
}

func New(ctx context.Context, cfg *config.Config) (*Store, error) {
	pgCfg, err := pgx.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	pgConn := stdlib.OpenDB(*pgCfg)
	driver := entsql.OpenDB(dialect.Postgres, pgConn)
	client := ent.NewClient(ent.Driver(driver))
	s := &Store{client: client, cfg: cfg}
	if err := s.client.Schema.Create(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	if err := s.bootstrap(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *Store) bootstrap(ctx context.Context) error {
	return nil
}
