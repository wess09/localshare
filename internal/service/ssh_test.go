package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"localshare/internal/config"
	"localshare/internal/domain"
)

func TestSSHServerAllowsNoClientAuth(t *testing.T) {
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	usernames := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			errs <- err
			return
		}
		defer raw.Close()
		conn, chans, reqs, err := ssh.NewServerConn(raw, serverCfg)
		if err != nil {
			errs <- err
			return
		}
		usernames <- conn.Permissions.Extensions["username"]
		go ssh.DiscardRequests(reqs)
		go func() {
			for ch := range chans {
				_ = ch.Reject(ssh.UnknownChannelType, "test")
			}
		}()
		_ = conn.Close()
		errs <- nil
	}()

	clientCfg := &ssh.ClientConfig{
		User:            "nopass-user",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := ssh.Dial("tcp", listener.Addr().String(), clientCfg)
	if err != nil {
		t.Fatalf("client handshake without auth failed: %v", err)
	}
	_ = client.Close()

	select {
	case username := <-usernames:
		if username != "nopass-user" {
			t.Fatalf("username = %q, want nopass-user", username)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server username")
	}
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("server handshake failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server result")
	}
}

func TestListenUnixSocketAllowsNginxWorkerAccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets are validated on Unix platforms")
	}
	sockPath := filepath.Join(t.TempDir(), "localshare.sock")
	listener, err := listenUnixSocket(sockPath)
	if err != nil {
		t.Fatalf("listenUnixSocket() failed: %v", err)
	}
	defer listener.Close()

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o666); got != want {
		t.Fatalf("socket mode = %v, want %v", got, want)
	}
}
