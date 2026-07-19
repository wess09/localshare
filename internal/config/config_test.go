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
