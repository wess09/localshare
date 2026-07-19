package domain

import "time"

type Role string

const (
	RoleStandalone Role = "standalone"
	RoleMaster     Role = "master"
	RoleNode       Role = "node"
)

const (
	RouteStatusActive  = "active"
	RouteStatusExpired = "expired"
)

const SockNameLength = 16

type Node struct {
	ID                   int
	NodeID               string    `json:"node_id"`
	SSHServer            string    `json:"ssh_server"`
	PublicBaseURL        string    `json:"public_base_url"`
	Weight               int       `json:"weight"`
	Enabled              bool      `json:"enabled"`
	Maintenance          bool      `json:"maintenance"`
	MaxTunnels           int       `json:"max_tunnels"`
	CurrentTunnels       int       `json:"current_tunnels"`
	MaxActiveConnections int       `json:"max_active_connections"`
	ActiveConnections    int       `json:"active_connections"`
	Region               string    `json:"region"`
	Token                string    `json:"token,omitempty"`
	LastHeartbeat        time.Time `json:"last_heartbeat"`
	IsLocal              bool      `json:"is_local"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	Healthy              bool      `json:"healthy,omitempty"`
	Eligible             bool      `json:"eligible,omitempty"`
	Score                float64   `json:"score,omitempty"`
}

type Route struct {
	ID        int       `json:"-"`
	Token     string    `json:"token"`
	NodeID    string    `json:"node_id"`
	TargetURL string    `json:"target_url"`
	PublicURL string    `json:"public_url"`
	PeerID    string    `json:"peer_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type NodePatch struct {
	Enabled              *bool
	Maintenance          *bool
	Weight               *int
	MaxTunnels           *int
	MaxActiveConnections *int
	Region               *string
}

type SuccessPayload struct {
	Address         string      `json:"address"`
	FallbackAddress string      `json:"fallback_address"`
	PeerID          string      `json:"peer_id"`
	SignalURL       string      `json:"signal_url"`
	ICEServers      []ICEServer `json:"ice_servers"`
	Status          string      `json:"status"`
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type Stats struct {
	Now     time.Time `json:"now"`
	Uptime  float64   `json:"uptime"`
	Role    Role      `json:"role"`
	NodeID  string    `json:"node_id"`
	Limits  Limits    `json:"limits"`
	SSH     SSHStats  `json:"ssh"`
	Signal  SigStats  `json:"signal"`
	HTTP    HTTPStats `json:"http"`
	Admin   AdminStat `json:"admin"`
	Cluster Cluster   `json:"cluster"`
	Peers   []Peer    `json:"peers"`
}

type Limits struct {
	SSH            int `json:"ssh"`
	Signal         int `json:"signal"`
	ViewersPerPeer int `json:"viewers_per_peer"`
}

type SSHStats struct {
	Active   int `json:"active"`
	Peers    int `json:"peers"`
	Total    int `json:"total"`
	Rejected int `json:"rejected"`
	Replaced int `json:"replaced"`
}

type SigStats struct {
	Peers       int   `json:"peers"`
	Viewers     int   `json:"viewers"`
	Total       int   `json:"total"`
	Rejected    int   `json:"rejected"`
	MessagesIn  int64 `json:"messages_in"`
	MessagesOut int64 `json:"messages_out"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
	ViewerTotal int64 `json:"viewer_total"`
}

type HTTPStats struct {
	P2PPages     int64 `json:"p2p_pages"`
	P2PPageBytes int64 `json:"p2p_page_bytes"`
}

type AdminStat struct {
	Logins      int64 `json:"logins"`
	FailedLogin int64 `json:"failed_logins"`
}

type Cluster struct {
	SchedulerTotal     int64  `json:"scheduler_total"`
	SchedulerRedirect  int64  `json:"scheduler_redirect"`
	SchedulerLocal     int64  `json:"scheduler_local"`
	SchedulerFail      int64  `json:"scheduler_fail"`
	RouteRegisterTotal int64  `json:"route_register_total"`
	RouteRegisterFail  int64  `json:"route_register_fail"`
	RouteDeleteTotal   int64  `json:"route_delete_total"`
	RouteRedirectTotal int64  `json:"route_redirect_total"`
	RouteLookupMiss    int64  `json:"route_lookup_miss"`
	HeartbeatTotal     int64  `json:"heartbeat_total"`
	HeartbeatFail      int64  `json:"heartbeat_fail"`
	Nodes              []Node `json:"nodes"`
	RoutesActive       int    `json:"routes_active"`
	RoutesTotal        int    `json:"routes_total"`
}

type Peer struct {
	PeerID            string    `json:"peer_id"`
	SSH               bool      `json:"ssh"`
	Signal            bool      `json:"signal"`
	Viewers           int       `json:"viewers"`
	FallbackURL       string    `json:"fallback_url"`
	CreatedAt         time.Time `json:"created_at"`
	LastSeen          time.Time `json:"last_seen"`
	SSHConnectedAt    time.Time `json:"ssh_connected_at"`
	SignalConnectedAt time.Time `json:"signal_connected_at"`
	SSHConnections    int64     `json:"ssh_connections"`
	SignalConnections int64     `json:"signal_connections"`
	ViewersTotal      int64     `json:"viewers_total"`
}
