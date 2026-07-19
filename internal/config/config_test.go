package config

import "testing"

func TestURLJoinAndNormalize(t *testing.T) {
	got := URLJoin("https://example.com/base/?x=1", "/p2p/", "abc")
	want := "https://example.com/base/p2p/abc"
	if got != want {
		t.Fatalf("URLJoin() = %q, want %q", got, want)
	}
}
