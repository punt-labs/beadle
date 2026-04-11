package email

import "testing"

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
