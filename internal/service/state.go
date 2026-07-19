package service

import (
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	startedAt time.Time

	sshTotal    atomic.Int64
	sshRejected atomic.Int64
	sshReplaced atomic.Int64

	signalTotal    atomic.Int64
	signalRejected atomic.Int64
	signalIn       atomic.Int64
	signalOut      atomic.Int64
	signalBytesIn  atomic.Int64
	signalBytesOut atomic.Int64
	viewerTotal    atomic.Int64

	p2pPages     atomic.Int64
	p2pPageBytes atomic.Int64

	adminLogins       atomic.Int64
	adminFailedLogins atomic.Int64

	schedulerTotal    atomic.Int64
	schedulerRedirect atomic.Int64
	schedulerLocal    atomic.Int64
	schedulerFail     atomic.Int64

	routeRegisterTotal atomic.Int64
	routeRegisterFail  atomic.Int64
	routeDeleteTotal   atomic.Int64
	routeRedirectTotal atomic.Int64
	routeLookupMiss    atomic.Int64
	heartbeatTotal     atomic.Int64
	heartbeatFail      atomic.Int64
}

func NewMetrics() *Metrics {
	return &Metrics{startedAt: time.Now()}
}

type PeerStats struct {
	CreatedAt         time.Time
	SSHConnectedAt    time.Time
	SignalConnectedAt time.Time
	LastSeen          time.Time
	SSHConnections    int64
	SignalConnections int64
	ViewersTotal      int64
}

type SSHSession struct {
	Conn         any
	SockName     string
	Username     string
	Ready        chan struct{}
	RedirectNode string
	ScheduleErr  string
	Listener     any
}

type SignalPeer struct {
	PeerID      string
	Conn        any
	SeenAt      time.Time
	FallbackURL string
}

type SignalViewer struct {
	ViewerID string
	PeerID   string
	Conn     any
}

type State struct {
	mu sync.RWMutex

	activeSSHConnections map[any]struct{}
	activeSSHPeers       map[string]*SSHSession
	signalPeers          map[string]*SignalPeer
	signalViewers        map[string]*SignalViewer
	peerStats            map[string]*PeerStats
}

func NewState() *State {
	return &State{
		activeSSHConnections: make(map[any]struct{}),
		activeSSHPeers:       make(map[string]*SSHSession),
		signalPeers:          make(map[string]*SignalPeer),
		signalViewers:        make(map[string]*SignalViewer),
		peerStats:            make(map[string]*PeerStats),
	}
}

func (s *State) PeerStat(peerID string) *PeerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stat := s.peerStats[peerID]
	if stat == nil {
		stat = &PeerStats{CreatedAt: time.Now()}
		s.peerStats[peerID] = stat
	}
	return stat
}

func (s *State) ActiveConnectionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.activeSSHConnections)
}

func (s *State) ActiveTunnelCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.activeSSHPeers)
}

func (s *State) ActivePeerCount() int {
	return s.ActiveTunnelCount()
}

func (s *State) SignalPeerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.signalPeers)
}

func (s *State) SignalViewerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.signalViewers)
}

func (s *State) activeSSHPeer(peerID string) *SSHSession {
	return s.activeSSHPeers[peerID]
}

func (s *State) signalPeer(peerID string) *SignalPeer {
	return s.signalPeers[peerID]
}

func (s *State) viewerCount(peerID string) int {
	n := 0
	for _, viewer := range s.signalViewers {
		if viewer.PeerID == peerID {
			n++
		}
	}
	return n
}

func (s *State) ActiveSockNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.activeSSHPeers))
	for sock := range s.activeSSHPeers {
		out = append(out, sock)
	}
	return out
}

func (s *State) AddSSHConnection(conn any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeSSHConnections[conn] = struct{}{}
}

func (s *State) RemoveSSHConnection(conn any) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.activeSSHConnections, conn)
	removed := []string{}
	for sock, session := range s.activeSSHPeers {
		if session.Conn == conn {
			removed = append(removed, sock)
			delete(s.activeSSHPeers, sock)
		}
	}
	return removed
}

func (s *State) SetSSHPeer(sockName string, session *SSHSession) *SSHSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.activeSSHPeers[sockName]
	s.activeSSHPeers[sockName] = session
	stat := s.peerStats[sockName]
	if stat == nil {
		stat = &PeerStats{CreatedAt: time.Now()}
		s.peerStats[sockName] = stat
	}
	stat.SSHConnectedAt = time.Now()
	stat.LastSeen = time.Now()
	stat.SSHConnections++
	return old
}

func (s *State) DeleteSSHPeer(sockName string, conn any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.activeSSHPeers[sockName]
	if session == nil || session.Conn != conn {
		return false
	}
	delete(s.activeSSHPeers, sockName)
	return true
}

func (s *State) SSHSession(sockName string) *SSHSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeSSHPeers[sockName]
}
