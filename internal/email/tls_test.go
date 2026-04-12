package email

import (
	"crypto/tls"
	"testing"
)

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},
		{"smtp.fastmail.com", false},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := isLoopback(tt.host); got != tt.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestTLSSkipVerify_OverridesLoopbackCheck(t *testing.T) {
	// Verify that TLSSkipVerify=true results in InsecureSkipVerify=true
	// even for non-loopback hosts (the Docker host.docker.internal case).
	tests := []struct {
		name         string
		host         string
		skipVerify   bool
		wantInsecure bool
	}{
		{"loopback without skip", "127.0.0.1", false, true},
		{"loopback with skip", "127.0.0.1", true, true},
		{"remote without skip", "host.docker.internal", false, false},
		{"remote with skip", "host.docker.internal", true, true},
		{"fastmail without skip", "imap.fastmail.com", false, false},
		{"fastmail with skip", "imap.fastmail.com", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.skipVerify || isLoopback(tt.host)
			if got != tt.wantInsecure {
				t.Errorf("TLSSkipVerify=%v || isLoopback(%q) = %v, want %v",
					tt.skipVerify, tt.host, got, tt.wantInsecure)
			}
		})
	}
}

func TestTLSConfig_InsecureSkipVerify(t *testing.T) {
	// Build a tls.Config the same way imap.go and smtp.go do, and
	// verify the InsecureSkipVerify field.
	cfg := &Config{
		IMAPHost:      "host.docker.internal",
		TLSSkipVerify: true,
	}
	tlsCfg := &tls.Config{
		ServerName:         cfg.IMAPHost,
		InsecureSkipVerify: cfg.TLSSkipVerify || isLoopback(cfg.IMAPHost),
	}
	if !tlsCfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true for TLSSkipVerify=true with non-loopback host")
	}

	cfg.TLSSkipVerify = false
	tlsCfg = &tls.Config{
		ServerName:         cfg.IMAPHost,
		InsecureSkipVerify: cfg.TLSSkipVerify || isLoopback(cfg.IMAPHost),
	}
	if tlsCfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=false for TLSSkipVerify=false with non-loopback host")
	}
}
