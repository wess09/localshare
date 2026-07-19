package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"localshare/internal/domain"
)

type Config struct {
	ConfigDir  string
	SocketDir  string
	ServerName string
	ServerPort int
	HTTPS      bool

	SignalHost string
	SignalPort int
	HTTPAddr   string

	Role domain.Role

	DatabaseURL               string
	DevAllowInsecureMasterAPI bool

	MaxSSHConnections       int
	MaxSignalConnections    int
	MaxSignalViewersPerPeer int
	NodeHeartbeatInterval   time.Duration
	NodeHeartbeatTimeout    time.Duration
	RouteTTL                time.Duration
	ExpiredRouteRetention   time.Duration

	MasterWorkerEnabled    bool
	MasterWorkerWeight     int
	MasterMaxTunnels       int
	MasterMaxActiveConns   int
	NodeWorkerWeight       int
	NodeMaxTunnels         int
	NodeMaxActiveConns     int
	ClusterPublicBaseURL   string
	NodePublicBaseURL      string
	MasterAPIURL           string
	LocalNodeID            string
	NodeToken              string
	AdminAPIToken          string
	NodeRegistrationToken  string
	NodeRegistrationBearer string
	NodesConfigFile        string
	TurnServers            []string
	TurnUsername           string
	TurnPassword           string
	Version                string
}

func Load(args []string, buildVersion string) (*Config, error) {
	cfg := &Config{
		ConfigDir:               ".",
		SocketDir:               "/tmp/localshare",
		ServerName:              "remote.nanoda.work",
		ServerPort:              1022,
		SignalHost:              "127.0.0.1",
		SignalPort:              8080,
		MaxSSHConnections:       envInt("MAX_SSH_CONNECTIONS", 100000),
		MaxSignalConnections:    envInt("MAX_SIGNAL_CONNECTIONS", 100000),
		MaxSignalViewersPerPeer: envInt("MAX_SIGNAL_VIEWERS_PER_PEER", 64),
		NodeHeartbeatInterval:   time.Duration(envInt("NODE_HEARTBEAT_INTERVAL_SECONDS", 10)) * time.Second,
		NodeHeartbeatTimeout:    time.Duration(envInt("NODE_HEARTBEAT_TIMEOUT_SECONDS", 30)) * time.Second,
		RouteTTL:                time.Duration(envInt("ROUTE_TTL_SECONDS", 60)) * time.Second,
		ExpiredRouteRetention:   time.Duration(envInt("EXPIRED_ROUTE_RETENTION_SECONDS", 3600)) * time.Second,
		MasterWorkerEnabled:     envBool("MASTER_WORKER_ENABLED", true),
		MasterWorkerWeight:      envInt("MASTER_WORKER_WEIGHT", 100),
		MasterMaxTunnels:        envInt("MASTER_MAX_TUNNELS", envInt("MAX_SSH_CONNECTIONS", 100000)),
		MasterMaxActiveConns:    envInt("MASTER_MAX_ACTIVE_CONNECTIONS", 0),
		NodeWorkerWeight:        envInt("NODE_WORKER_WEIGHT", 100),
		NodeMaxTunnels:          envInt("NODE_MAX_TUNNELS", envInt("MAX_SSH_CONNECTIONS", 100000)),
		NodeMaxActiveConns:      envInt("NODE_MAX_ACTIVE_CONNECTIONS", 0),
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		AdminAPIToken:           os.Getenv("ADMIN_API_TOKEN"),
		NodeRegistrationToken:   os.Getenv("NODE_REGISTRATION_TOKEN"),
		NodeRegistrationBearer:  firstNonEmpty(os.Getenv("NODE_REGISTRATION_BEARER"), os.Getenv("NODE_REGISTRATION_TOKEN")),
		TurnUsername:            os.Getenv("TURN_USERNAME"),
		TurnPassword:            os.Getenv("TURN_PASSWORD"),
		Version:                 firstNonEmpty(os.Getenv("LOCALSHARE_VERSION"), buildVersion, "dev"),
		DevAllowInsecureMasterAPI: envBool(
			"LOCALSHARE_ALLOW_INSECURE_MASTER_API",
			false,
		),
	}
	cfg.Role = parseRole(os.Getenv("LOCALSHARE_ROLE"))

	fs := flag.NewFlagSet("localshare", flag.ContinueOnError)
	fs.StringVar(&cfg.ConfigDir, "config-dir", cfg.ConfigDir, "configuration directory")
	fs.StringVar(&cfg.SocketDir, "socket-dir", cfg.SocketDir, "Unix socket directory")
	fs.IntVar(&cfg.ServerPort, "port", cfg.ServerPort, "SSH server port")
	fs.StringVar(&cfg.SignalHost, "signal-host", cfg.SignalHost, "local HTTP host")
	fs.IntVar(&cfg.SignalPort, "signal-port", cfg.SignalPort, "local HTTP port")
	fs.BoolVar(&cfg.HTTPS, "https", false, "build public URLs with https")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		cfg.ServerName = fs.Arg(0)
	}
	cfg.ConfigDir = filepath.Clean(cfg.ConfigDir)
	cfg.SocketDir = filepath.Clean(cfg.SocketDir)
	cfg.HTTPAddr = fmt.Sprintf("%s:%d", cfg.SignalHost, cfg.SignalPort)
	if envBool("HTTPS", false) {
		cfg.HTTPS = true
	}
	if raw := os.Getenv("TURN_SERVERS"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.TurnServers)
	}

	cfg.NodesConfigFile = firstNonEmpty(os.Getenv("NODES_CONFIG_FILE"), filepath.Join(cfg.ConfigDir, "nodes.json"))
	cfg.ClusterPublicBaseURL = NormalizeBaseURL(firstNonEmpty(os.Getenv("REMOTE_PUBLIC_BASE_URL"), cfg.PublicURL()))

	switch cfg.Role {
	case domain.RoleStandalone:
		cfg.NodePublicBaseURL = cfg.PublicURL()
	case domain.RoleMaster:
		cfg.LocalNodeID = firstNonEmpty(os.Getenv("MASTER_NODE_ID"), "master")
		cfg.NodePublicBaseURL = NormalizeBaseURL(firstNonEmpty(os.Getenv("MASTER_PUBLIC_BASE_URL"), cfg.PublicURL()))
	case domain.RoleNode:
		cfg.LocalNodeID = strings.TrimSpace(os.Getenv("NODE_ID"))
		cfg.NodeToken = os.Getenv("NODE_TOKEN")
		cfg.MasterAPIURL = NormalizeBaseURL(os.Getenv("MASTER_API_URL"))
		cfg.NodePublicBaseURL = NormalizeBaseURL(firstNonEmpty(os.Getenv("NODE_PUBLIC_BASE_URL"), cfg.PublicURL()))
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.ServerName == "" {
		return fmt.Errorf("server name is required")
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required for %s role", c.Role)
	}
	if c.Role == domain.RoleNode {
		if os.Getenv("REMOTE_PUBLIC_BASE_URL") == "" {
			return fmt.Errorf("REMOTE_PUBLIC_BASE_URL is required in node role")
		}
		if c.LocalNodeID == "" {
			return fmt.Errorf("NODE_ID is required in node role")
		}
		if c.NodeToken == "" {
			return fmt.Errorf("NODE_TOKEN is required in node role")
		}
		if c.MasterAPIURL == "" {
			return fmt.Errorf("MASTER_API_URL is required in node role")
		}
		if !c.DevAllowInsecureMasterAPI && strings.HasPrefix(c.MasterAPIURL, "http://") {
			return fmt.Errorf("MASTER_API_URL must use https unless LOCALSHARE_ALLOW_INSECURE_MASTER_API=true")
		}
	}
	return nil
}

func (c *Config) PublicScheme() string {
	if c.HTTPS {
		return "https"
	}
	return "http"
}

func (c *Config) WebSocketScheme() string {
	if c.HTTPS {
		return "wss"
	}
	return "ws"
}

func (c *Config) PublicURL(parts ...string) string {
	return URLJoin(c.PublicScheme()+"://"+c.ServerName, parts...)
}

func (c *Config) PublicBaseURL() string {
	if c.ClusterPublicBaseURL != "" {
		return c.ClusterPublicBaseURL
	}
	return c.PublicURL()
}

func (c *Config) SignalURL() string {
	base := c.PublicBaseURL()
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		return scheme + "://" + u.Host + "/signal"
	}
	return c.WebSocketScheme() + "://" + c.ServerName + "/signal"
}

func (c *Config) ICEServers() []domain.ICEServer {
	if len(c.TurnServers) == 0 {
		return []domain.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}
	}
	out := make([]domain.ICEServer, 0, len(c.TurnServers))
	for _, server := range c.TurnServers {
		item := domain.ICEServer{URLs: []string{server}}
		if c.TurnUsername != "" {
			item.Username = c.TurnUsername
		}
		if c.TurnPassword != "" {
			item.Credential = c.TurnPassword
		}
		out = append(out, item)
	}
	return out
}

func parseRole(value string) domain.Role {
	switch domain.Role(strings.ToLower(strings.TrimSpace(value))) {
	case domain.RoleMaster:
		return domain.RoleMaster
	case domain.RoleNode:
		return domain.RoleNode
	default:
		return domain.RoleStandalone
	}
}

func NormalizeBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" {
		return value
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

func URLJoin(base string, parts ...string) string {
	base = NormalizeBaseURL(base)
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), "/")
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return base
	}
	return strings.TrimRight(base, "/") + "/" + strings.Join(clean, "/")
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch raw {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
