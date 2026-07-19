package domain

import "testing"

func TestSockNameCompatibility(t *testing.T) {
	got := SockName("alice")
	want := "2bd806c97f0e00af"
	if got != want {
		t.Fatalf("SockName() = %q, want %q", got, want)
	}
}

func TestParseSSHArguments(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"-- --output json", "json"},
		{"--output=json", "json"},
	}
	for _, tc := range cases {
		got := ParseSSHArguments(tc.in)["output"]
		if got != tc.want {
			t.Fatalf("ParseSSHArguments(%q) output = %q, want %q", tc.in, got, tc.want)
		}
	}
}
