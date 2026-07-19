package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
