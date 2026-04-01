package handlers

import (
	"net"
	"strings"
	"testing"
)

func TestCheckIPBlocklist(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		wantErr bool
		errMsg  string
	}{
		{"public IP allowed", "8.8.8.8", false, ""},
		{"loopback rejected", "127.0.0.1", true, "loopback"},
		{"loopback IPv6 rejected", "::1", true, "loopback"},
		{"private 10.x rejected", "10.0.0.1", true, "private"},
		{"private 172.16.x rejected", "172.16.0.1", true, "private"},
		{"private 192.168.x rejected", "192.168.1.1", true, "private"},
		{"link-local rejected", "169.254.1.1", true, "link-local"},
		{"AWS metadata rejected (link-local)", "169.254.169.254", true, "link-local"},
		{"unspecified rejected", "0.0.0.0", true, "unspecified"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			err := checkIPBlocklist(ip)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{"valid HTTPS URL", "https://example.com/webhook", false, ""},
		{"HTTP rejected", "http://example.com/webhook", true, "HTTPS"},
		{"FTP rejected", "ftp://example.com/file", true, "HTTPS"},
		{"empty scheme rejected", "://example.com/webhook", true, "invalid URL"},
		{"no host rejected", "https:///path", true, "hostname"},
		{"localhost rejected (resolves to loopback)", "https://localhost/webhook", true, "loopback"},
		{"SSRF metadata IP via URL", "https://169.254.169.254/latest/meta-data/", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateWebhookURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestValidateWebhookURL_ReturnsIP(t *testing.T) {
	// example.com should resolve to a public IP
	ip, err := ValidateWebhookURL("https://example.com/webhook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip == nil {
		t.Fatal("expected non-nil IP")
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		t.Errorf("expected public IP, got %v", ip)
	}
}
