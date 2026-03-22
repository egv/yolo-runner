package opencode

import "testing"

func TestResolveServeBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		port     int
		want     string
	}{
		{
			name:     "defaults empty hostname to loopback",
			hostname: "",
			port:     4096,
			want:     "http://127.0.0.1:4096",
		},
		{
			name:     "preserves explicit loopback hostname",
			hostname: "127.0.0.1",
			port:     4096,
			want:     "http://127.0.0.1:4096",
		},
		{
			name:     "rewrites ipv4 wildcard hostname to localhost",
			hostname: "0.0.0.0",
			port:     4096,
			want:     "http://localhost:4096",
		},
		{
			name:     "rewrites ipv6 wildcard hostname to localhost",
			hostname: "::",
			port:     4096,
			want:     "http://localhost:4096",
		},
		{
			name:     "formats ipv6 host with brackets",
			hostname: "::1",
			port:     4096,
			want:     "http://[::1]:4096",
		},
		{
			name:     "preserves named hosts",
			hostname: "opencode.internal",
			port:     4096,
			want:     "http://opencode.internal:4096",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveServeBaseURL(tt.hostname, tt.port); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestResolveServeHealthURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "derives global health path from resolved base url",
			baseURL: "http://127.0.0.1:4096",
			want:    "http://127.0.0.1:4096/global/health",
		},
		{
			name:    "trims trailing slash from base url",
			baseURL: "http://localhost:4096/",
			want:    "http://localhost:4096/global/health",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveServeHealthURL(tt.baseURL); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
