package config

import "testing"

func TestURLJoinAndNormalize(t *testing.T) {
	got := URLJoin("https://example.com/base/?x=1", "/p2p/", "abc")
	want := "https://example.com/base/p2p/abc"
	if got != want {
		t.Fatalf("URLJoin() = %q, want %q", got, want)
	}
}

func TestLoadUsesBuildVersion(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localshare:localshare@postgres:5432/localshare?sslmode=disable")
	t.Setenv("LOCALSHARE_VERSION", "")

	cfg, err := Load([]string{"example.com"}, "build-sha")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Version != "build-sha" {
		t.Fatalf("Version = %q, want build version", cfg.Version)
	}
}

func TestLoadVersionEnvOverridesBuildVersion(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localshare:localshare@postgres:5432/localshare?sslmode=disable")
	t.Setenv("LOCALSHARE_VERSION", "env-version")

	cfg, err := Load([]string{"example.com"}, "build-sha")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Version != "env-version" {
		t.Fatalf("Version = %q, want env version", cfg.Version)
	}
}

func TestLoadNodePublicSSHServer(t *testing.T) {
	t.Setenv("LOCALSHARE_ROLE", "node")
	t.Setenv("DATABASE_URL", "postgres://localshare:localshare@postgres:5432/localshare?sslmode=disable")
	t.Setenv("REMOTE_PUBLIC_BASE_URL", "https://example.com")
	t.Setenv("MASTER_API_URL", "https://example.com/api")
	t.Setenv("NODE_ID", "node-a")
	t.Setenv("NODE_TOKEN", "node-secret")
	t.Setenv("NODE_SSH_SERVER", "node.example.com:11022")

	cfg, err := Load([]string{"node.example.com"}, "build-sha")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.PublicSSHServer != "node.example.com:11022" {
		t.Fatalf("PublicSSHServer = %q, want NODE_SSH_SERVER", cfg.PublicSSHServer)
	}
}
