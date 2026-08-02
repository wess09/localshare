package service

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"localshare/internal/config"
	"localshare/internal/domain"
)

func TestProxyBasicRequest(t *testing.T) {
	backendAddr := startRawTCPBackend(t, respondAndClose)
	visitor, cleanup := startProxyTunnel(t, backendAddr)
	defer cleanup()

	if _, err := io.WriteString(visitor, "GET / HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	body, status, err := readHTTPResponse(visitor)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("status = %q, want 200", status)
	}
	if body != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}
}

func TestProxyHalfCloseStillGetsResponse(t *testing.T) {
	backendAddr := startRawTCPBackend(t, respondAndClose)
	visitor, cleanup := startProxyTunnel(t, backendAddr)
	defer cleanup()

	if _, err := io.WriteString(visitor, "GET / HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	tcp := visitor.(*net.TCPConn)
	if err := tcp.CloseWrite(); err != nil {
		t.Fatalf("half-close write: %v", err)
	}
	body, status, err := readHTTPResponse(visitor)
	if err != nil {
		t.Fatalf("read response after half-close: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("status = %q, want 200", status)
	}
	if body != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}
}

func TestProxyKeepAliveTwoRequests(t *testing.T) {
	backendAddr := startRawTCPBackend(t, keepAliveBackend)
	visitor, cleanup := startProxyTunnel(t, backendAddr)
	defer cleanup()

	for i := 1; i <= 2; i++ {
		if _, err := io.WriteString(visitor, fmt.Sprintf("GET /req%d HTTP/1.1\r\nHost: test\r\n\r\n", i)); err != nil {
			t.Fatalf("write request %d: %v", i, err)
		}
		body, status, err := readHTTPResponse(visitor)
		if err != nil {
			t.Fatalf("read response %d: %v", i, err)
		}
		if !strings.Contains(status, "200") {
			t.Fatalf("status %d = %q, want 200", i, status)
		}
		if want := fmt.Sprintf("resp%d", i); body != want {
			t.Fatalf("body %d = %q, want %q", i, body, want)
		}
	}
}

func startProxyTunnel(t *testing.T, backendAddr string) (net.Conn, func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	server := NewSSHServer(
		&config.Config{Role: domain.RoleStandalone},
		nil,
		NewState(),
		NewMetrics(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	serverCfg := server.serverConfig(context.Background(), signer)

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ssh: %v", err)
	}

	serverReady := make(chan *sshConnState, 1)
	go func() {
		raw, err := srvLn.Accept()
		if err != nil {
			return
		}
		conn, chans, reqs, err := ssh.NewServerConn(raw, serverCfg)
		if err != nil {
			_ = raw.Close()
			return
		}
		st := &sshConnState{
			conn:     conn,
			username: "testuser",
			sockName: domain.SockName("testuser"),
			ready:    make(chan struct{}),
		}
		st.session = &SSHSession{Conn: conn, SockName: st.sockName, Username: "testuser", Ready: st.ready}
		close(st.ready)
		go ssh.DiscardRequests(reqs)
		go func() {
			for ch := range chans {
				_ = ch.Reject(ssh.UnknownChannelType, "no session")
			}
		}()
		serverReady <- st
		_ = conn.Wait()
	}()
	defer srvLn.Close()

	clientCfg := &ssh.ClientConfig{
		User:            "testuser",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := ssh.Dial("tcp", srvLn.Addr().String(), clientCfg)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}

	go func() {
		for newCh := range client.HandleChannelOpen("forwarded-tcpip") {
			ch, reqs, err := newCh.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(reqs)
			backend, err := net.Dial("tcp", backendAddr)
			if err != nil {
				_ = ch.Close()
				continue
			}
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = io.Copy(backend, ch)
				closeWrite(backend)
			}()
			go func() {
				defer wg.Done()
				_, _ = io.Copy(ch, backend)
				_ = ch.CloseWrite()
			}()
			wg.Wait()
			_ = ch.Close()
			_ = backend.Close()
		}
	}()

	st := <-serverReady

	vLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen visitor: %v", err)
	}
	go func() {
		local, err := vLn.Accept()
		if err != nil {
			return
		}
		server.bridgeForward(context.Background(), st, local, "forwarded-tcpip", "127.0.0.1", 80)
	}()
	defer vLn.Close()

	visitor, err := net.Dial("tcp", vLn.Addr().String())
	if err != nil {
		t.Fatalf("visitor dial: %v", err)
	}
	cleanup := func() {
		_ = visitor.Close()
		_ = client.Close()
	}
	return visitor, cleanup
}

func startRawTCPBackend(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handler(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func respondAndClose(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	body := "hello"
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
}

func keepAliveBackend(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for i := 1; i <= 2; i++ {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" || line == "\n" {
				break
			}
		}
		body := fmt.Sprintf("resp%d", i)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
	}
}

func readHTTPResponse(conn net.Conn) (body, status string, err error) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	status, err = reader.ReadString('\n')
	if err != nil {
		return "", "", err
	}
	var length int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			length, _ = strconv.Atoi(strings.TrimSpace(trimmed[len("content-length:"):]))
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", "", err
	}
	return string(buf), status, nil
}
