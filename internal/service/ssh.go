package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"localshare/internal/config"
	"localshare/internal/domain"
)

type SSHServer struct {
	cfg     *config.Config
	cluster *ClusterService
	state   *State
	metric  *Metrics
	log     *slog.Logger
}

type sshConnState struct {
	conn     *ssh.ServerConn
	username string
	sockName string
	session  *SSHSession
	ready    chan struct{}
	once     sync.Once
}

func NewSSHServer(cfg *config.Config, cluster *ClusterService, state *State, metric *Metrics, log *slog.Logger) *SSHServer {
	return &SSHServer{cfg: cfg, cluster: cluster, state: state, metric: metric, log: log}
}

func (s *SSHServer) ListenAndServe(ctx context.Context) error {
	signer, err := s.loadHostSigner()
	if err != nil {
		return err
	}
	serverCfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			return s.authPermissions(ctx, meta.User()), nil
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
			return s.authPermissions(ctx, meta.User()), nil
		},
		ServerVersion: "SSH-2.0-localshare-go",
	}
	serverCfg.AddHostKey(signer)
	addr := fmt.Sprintf("0.0.0.0:%d", s.cfg.ServerPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	Go(ctx, s.log, "ssh listener shutdown", func() {
		<-ctx.Done()
		_ = listener.Close()
	})
	s.log.Info("ssh server started", "addr", addr)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if s.state.ActiveConnectionCount() >= s.cfg.MaxSSHConnections {
			s.metric.sshRejected.Add(1)
			_ = conn.Close()
			continue
		}
		Go(ctx, s.log, "ssh connection", func() {
			s.handleConn(ctx, conn, serverCfg)
		})
	}
}

func (s *SSHServer) authPermissions(ctx context.Context, username string) *ssh.Permissions {
	ext := map[string]string{"username": username}
	if s.cfg.Role == domain.RoleMaster {
		sock := domain.SockName(username)
		node, reason, err := s.cluster.Schedule(ctx, sock)
		if err != nil {
			ext["schedule_error"] = "no available node"
		} else if node.NodeID != s.cfg.LocalNodeID {
			ext["redirect_node"] = node.NodeID
			ext["redirect_ssh_server"] = node.SSHServer
			ext["schedule_reason"] = reason
		}
	}
	return &ssh.Permissions{Extensions: ext}
}

func (s *SSHServer) handleConn(ctx context.Context, raw net.Conn, serverCfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(raw, serverCfg)
	if err != nil {
		_ = raw.Close()
		return
	}
	username := conn.Permissions.Extensions["username"]
	st := &sshConnState{
		conn:     conn,
		username: username,
		sockName: domain.SockName(username),
		ready:    make(chan struct{}),
	}
	st.session = &SSHSession{Conn: conn, SockName: st.sockName, Username: username, Ready: st.ready}
	st.session.RedirectNode = conn.Permissions.Extensions["redirect_ssh_server"]
	st.session.ScheduleErr = conn.Permissions.Extensions["schedule_error"]
	s.state.AddSSHConnection(conn)
	s.metric.sshTotal.Add(1)
	defer func() {
		removed := s.state.RemoveSSHConnection(conn)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, sock := range removed {
			_ = s.cluster.DeleteRoute(cleanupCtx, sock)
			s.closeListener(st.session.Listener)
		}
		_ = conn.Close()
	}()
	Go(ctx, s.log, "ssh global requests", func() {
		s.handleGlobalRequests(ctx, st, reqs)
	})
	for ch := range chans {
		switch ch.ChannelType() {
		case "session":
			Go(ctx, s.log, "ssh session channel", func() {
				s.handleSessionChannel(ctx, st, ch)
			})
		default:
			_ = ch.Reject(ssh.UnknownChannelType, "unsupported channel")
		}
	}
}

func (s *SSHServer) handleGlobalRequests(ctx context.Context, st *sshConnState, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "tcpip-forward":
			var payload struct {
				BindAddr string
				BindPort uint32
			}
			ssh.Unmarshal(req.Payload, &payload)
			err := s.startForward(ctx, st, "forwarded-tcpip", payload.BindAddr, payload.BindPort)
			_ = req.Reply(err == nil, nil)
		case "streamlocal-forward@openssh.com":
			var payload struct {
				SocketPath string
			}
			ssh.Unmarshal(req.Payload, &payload)
			err := s.startForward(ctx, st, "forwarded-streamlocal@openssh.com", payload.SocketPath, 0)
			_ = req.Reply(err == nil, nil)
		case "cancel-tcpip-forward", "cancel-streamlocal-forward@openssh.com":
			_ = req.Reply(true, nil)
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

func (s *SSHServer) startForward(ctx context.Context, st *sshConnState, channelType, requestedPath string, requestedPort uint32) error {
	if st.session.RedirectNode != "" || st.session.ScheduleErr != "" {
		st.markReady()
		return nil
	}
	if err := os.MkdirAll(s.cfg.SocketDir, 0o755); err != nil {
		return err
	}
	sockPath := filepath.Join(s.cfg.SocketDir, st.sockName+".sock")
	_ = os.Remove(sockPath)
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	st.session.Listener = listener
	old := s.state.SetSSHPeer(st.sockName, st.session)
	if old != nil && old.Conn != st.conn {
		s.metric.sshReplaced.Add(1)
		s.closeListener(old.Listener)
		if oldConn, ok := old.Conn.(*ssh.ServerConn); ok {
			_ = oldConn.Close()
		}
	}
	if err := s.cluster.RegisterRoute(ctx, st.sockName); err != nil {
		_ = listener.Close()
		_ = os.Remove(sockPath)
		return err
	}
	st.markReady()
	Go(ctx, s.log, "ssh unix accept loop", func() {
		s.acceptUnixLoop(ctx, st, listener, channelType, requestedPath, requestedPort)
	})
	return nil
}

func (s *SSHServer) acceptUnixLoop(ctx context.Context, st *sshConnState, listener net.Listener, channelType, requestedPath string, requestedPort uint32) {
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		Go(ctx, s.log, "ssh forward bridge", func() {
			s.bridgeForward(ctx, st, local, channelType, requestedPath, requestedPort)
		})
	}
}

func (s *SSHServer) bridgeForward(ctx context.Context, st *sshConnState, local net.Conn, channelType, requestedPath string, requestedPort uint32) {
	defer local.Close()
	var payload []byte
	if channelType == "forwarded-streamlocal@openssh.com" {
		payload = ssh.Marshal(struct {
			SocketPath string
			Reserved   string
		}{SocketPath: requestedPath, Reserved: ""})
	} else {
		payload = ssh.Marshal(struct {
			ConnectedAddr string
			ConnectedPort uint32
			OriginAddr    string
			OriginPort    uint32
		}{
			ConnectedAddr: requestedPath,
			ConnectedPort: requestedPort,
			OriginAddr:    "127.0.0.1",
			OriginPort:    0,
		})
	}
	ch, reqs, err := st.conn.OpenChannel(channelType, payload)
	if err != nil {
		s.log.Debug("open forwarded channel failed", "sock", st.sockName, "err", err)
		return
	}
	defer ch.Close()
	Go(ctx, s.log, "ssh forwarded requests discard", func() {
		ssh.DiscardRequests(reqs)
	})
	done := make(chan struct{}, 2)
	Go(ctx, s.log, "ssh forward copy client", func() {
		_, _ = io.Copy(ch, local)
		done <- struct{}{}
	})
	Go(ctx, s.log, "ssh forward copy local", func() {
		_, _ = io.Copy(local, ch)
		done <- struct{}{}
	})
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func (s *SSHServer) handleSessionChannel(ctx context.Context, st *sshConnState, newCh ssh.NewChannel) {
	ch, reqs, err := newCh.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	var command string
	Go(ctx, s.log, "ssh session requests", func() {
		for req := range reqs {
			switch req.Type {
			case "exec":
				var payload struct{ Command string }
				ssh.Unmarshal(req.Payload, &payload)
				command = payload.Command
				_ = req.Reply(true, nil)
				s.writeSessionIntro(ctx, st, ch, command)
			case "shell":
				_ = req.Reply(true, nil)
				s.writeSessionIntro(ctx, st, ch, command)
			case "env", "pty-req":
				_ = req.Reply(true, nil)
			default:
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}
	})
	_, _ = io.Copy(io.Discard, ch)
}

func (s *SSHServer) writeSessionIntro(ctx context.Context, st *sshConnState, ch ssh.Channel, command string) {
	select {
	case <-st.ready:
	case <-time.After(60 * time.Second):
		_, _ = fmt.Fprintln(ch, "route registration timeout")
		sendExitStatus(ch, 1)
		return
	case <-ctx.Done():
		return
	}
	args := domain.ParseSSHArguments(command)
	if st.session.RedirectNode != "" {
		payload := map[string]any{
			"status":     "redirect",
			"ssh_server": st.session.RedirectNode,
			"ssh_user":   nil,
			"reason":     "weighted_least_connection",
			"ttl":        60,
		}
		if args["output"] == "json" {
			data, _ := json.Marshal(payload)
			_, _ = fmt.Fprintln(ch, string(data))
		} else {
			_, _ = fmt.Fprintf(ch, "Please reconnect to %s\n", st.session.RedirectNode)
		}
		sendExitStatus(ch, 0)
		return
	}
	if st.session.ScheduleErr != "" {
		payload := map[string]any{"status": "fail", "message": st.session.ScheduleErr, "retry_after": 30}
		if args["output"] == "json" {
			data, _ := json.Marshal(payload)
			_, _ = fmt.Fprintln(ch, string(data))
		} else {
			_, _ = fmt.Fprintln(ch, st.session.ScheduleErr)
		}
		sendExitStatus(ch, 1)
		return
	}
	payload := s.cluster.BuildSuccessPayload(st.sockName)
	if args["output"] == "json" {
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintln(ch, string(data))
	} else {
		_, _ = fmt.Fprintf(ch, "The public entrypoint for your local web service is:\n%s\n", payload.Address)
	}
}

func sendExitStatus(ch ssh.Channel, code uint32) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: code}))
	_ = ch.Close()
}

func (st *sshConnState) markReady() {
	st.once.Do(func() { close(st.ready) })
}

func (s *SSHServer) closeListener(listener any) {
	if l, ok := listener.(net.Listener); ok {
		_ = l.Close()
	}
}

func (s *SSHServer) loadHostSigner() (ssh.Signer, error) {
	if err := os.MkdirAll(s.cfg.ConfigDir, 0o755); err != nil {
		return nil, err
	}
	keyFile := filepath.Join(s.cfg.ConfigDir, "ssh_host_key")
	if data, err := os.ReadFile(keyFile); err == nil {
		return ssh.ParsePrivateKey(data)
	}
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, err
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	data := pem.EncodeToMemory(block)
	if data == nil {
		return nil, errors.New("failed to encode private key")
	}
	if err := os.WriteFile(keyFile, data, 0o600); err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}
