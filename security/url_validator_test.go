package security

import (
	"net"
	"testing"
)

func TestURLValidator_ValidateScheme(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		schemes     []string
		shouldError bool
	}{
		{
			name:        "https allowed by default",
			url:         "https://example.com",
			schemes:     []string{"https"},
			shouldError: false,
		},
		{
			name:        "http blocked by default",
			url:         "http://example.com",
			schemes:     []string{"https"},
			shouldError: true,
		},
		{
			name:        "http allowed when configured",
			url:         "http://example.com",
			schemes:     []string{"http", "https"},
			shouldError: false,
		},
		{
			name:        "ftp not allowed",
			url:         "ftp://example.com",
			schemes:     []string{"http", "https"},
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := URLValidatorConfig{
				AllowedSchemes:  tt.schemes,
				BlockPrivateIPs: false,
				BlockLocalhost:  false,
			}
			v := NewURLValidator(cfg)

			err := v.Validate(tt.url)
			if tt.shouldError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestURLValidator_BlockLocalhost(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		shouldError bool
	}{
		{
			name:        "localhost blocked",
			url:         "https://localhost/path",
			shouldError: true,
		},
		{
			name:        "127.0.0.1 blocked",
			url:         "https://127.0.0.1/path",
			shouldError: true,
		},
		{
			name:        "::1 blocked",
			url:         "https://[::1]/path",
			shouldError: true,
		},
		{
			name:        "0.0.0.0 blocked",
			url:         "https://0.0.0.0/path",
			shouldError: true,
		},
		{
			name:        "external host allowed",
			url:         "https://example.com/path",
			shouldError: false,
		},
	}

	cfg := URLValidatorConfig{
		AllowedSchemes:  []string{"https"},
		BlockPrivateIPs: false,
		BlockLocalhost:  true,
	}
	v := NewURLValidator(cfg)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.url)
			if tt.shouldError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestURLValidator_BlockPrivateIPs(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		shouldError bool
	}{
		{
			name:        "10.x.x.x blocked",
			url:         "https://10.0.0.1/path",
			shouldError: true,
		},
		{
			name:        "172.16.x.x blocked",
			url:         "https://172.16.0.1/path",
			shouldError: true,
		},
		{
			name:        "172.31.x.x blocked",
			url:         "https://172.31.255.255/path",
			shouldError: true,
		},
		{
			name:        "172.32.x.x allowed (not in private range)",
			url:         "https://172.32.0.1/path",
			shouldError: false,
		},
		{
			name:        "192.168.x.x blocked",
			url:         "https://192.168.1.1/path",
			shouldError: true,
		},
		{
			name:        "169.254.x.x blocked (link-local)",
			url:         "https://169.254.1.1/path",
			shouldError: true,
		},
		{
			name:        "public IP allowed",
			url:         "https://8.8.8.8/path",
			shouldError: false,
		},
	}

	cfg := URLValidatorConfig{
		AllowedSchemes:  []string{"https"},
		BlockPrivateIPs: true,
		BlockLocalhost:  true,
	}
	v := NewURLValidator(cfg)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.url)
			if tt.shouldError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestURLValidator_AllowedHosts(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		allowedHosts []string
		shouldError  bool
	}{
		{
			name:         "exact match allowed with IP",
			url:          "https://8.8.8.8/path",
			allowedHosts: []string{"8.8.8.8"},
			shouldError:  false,
		},
		{
			name:         "not in allowed list with IP",
			url:          "https://1.1.1.1/path",
			allowedHosts: []string{"8.8.8.8", "8.8.4.4"},
			shouldError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := URLValidatorConfig{
				AllowedSchemes:  []string{"https"},
				AllowedHosts:    tt.allowedHosts,
				BlockPrivateIPs: false,
				BlockLocalhost:  false,
			}
			v := NewURLValidator(cfg)

			err := v.Validate(tt.url)
			if tt.shouldError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestURLValidator_isHostAllowed(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		allowedHosts []string
		expected     bool
	}{
		{
			name:         "exact match",
			host:         "api.example.com",
			allowedHosts: []string{"api.example.com"},
			expected:     true,
		},
		{
			name:         "wildcard match",
			host:         "api.example.com",
			allowedHosts: []string{"*.example.com"},
			expected:     true,
		},
		{
			name:         "subdomain wildcard",
			host:         "sub.api.example.com",
			allowedHosts: []string{"*.example.com"},
			expected:     true,
		},
		{
			name:         "not in allowed list",
			host:         "evil.com",
			allowedHosts: []string{"api.example.com", "*.trusted.com"},
			expected:     false,
		},
		{
			name:         "case insensitive",
			host:         "API.EXAMPLE.COM",
			allowedHosts: []string{"api.example.com"},
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := URLValidatorConfig{
				AllowedHosts: tt.allowedHosts,
			}
			v := NewURLValidator(cfg)

			result := v.isHostAllowed(tt.host)
			if result != tt.expected {
				t.Errorf("isHostAllowed(%q) = %v, want %v", tt.host, result, tt.expected)
			}
		})
	}
}

func TestURLValidator_ValidateRedirectURL(t *testing.T) {
	cfg := DefaultURLValidatorConfig()
	v := NewURLValidator(cfg)

	tests := []struct {
		name        string
		original    string
		redirect    string
		shouldError bool
	}{
		{
			name:        "same scheme redirect allowed",
			original:    "https://example.com/start",
			redirect:    "https://example.com/end",
			shouldError: false,
		},
		{
			name:        "protocol downgrade blocked",
			original:    "https://example.com/start",
			redirect:    "http://example.com/end",
			shouldError: true,
		},
		{
			name:        "redirect to private IP blocked",
			original:    "https://example.com/start",
			redirect:    "https://192.168.1.1/evil",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateRedirectURL(tt.original, tt.redirect)
			if tt.shouldError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip       string
		isPrivate bool
	}{
		// Private ranges
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},
		{"169.254.1.1", true},
		{"127.0.0.1", true},
		{"0.0.0.1", true},

		// Public IPs
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.32.0.1", false},
		{"192.167.1.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			result := isPrivateIP(ip)
			if result != tt.isPrivate {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, result, tt.isPrivate)
			}
		})
	}
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		URL:    "https://evil.com",
		Reason: "blocked",
	}

	expected := `URL validation failed for "https://evil.com": blocked`
	if err.Error() != expected {
		t.Errorf("got %q, want %q", err.Error(), expected)
	}

	errWithDetails := &ValidationError{
		URL:     "https://evil.com",
		Reason:  "blocked",
		Details: "private IP",
	}

	expected = `URL validation failed for "https://evil.com": blocked (private IP)`
	if errWithDetails.Error() != expected {
		t.Errorf("got %q, want %q", errWithDetails.Error(), expected)
	}
}

func TestDefaultURLValidatorConfig(t *testing.T) {
	cfg := DefaultURLValidatorConfig()

	if len(cfg.AllowedSchemes) != 1 || cfg.AllowedSchemes[0] != "https" {
		t.Error("default config should only allow https")
	}

	if !cfg.BlockPrivateIPs {
		t.Error("default config should block private IPs")
	}

	if !cfg.BlockLocalhost {
		t.Error("default config should block localhost")
	}
}
