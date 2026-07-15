package security

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// URLValidatorConfig holds URL validator configuration
type URLValidatorConfig struct {
	// AllowedSchemes defines which URL schemes are allowed
	AllowedSchemes []string `yaml:"allowed_schemes"`
	// AllowedHosts whitelist of allowed hosts (supports wildcards like *.example.com)
	AllowedHosts []string `yaml:"allowed_hosts"`
	// BlockPrivateIPs blocks requests to private IP ranges
	BlockPrivateIPs bool `yaml:"block_private_ips"`
	// BlockLocalhost blocks requests to localhost/127.0.0.1
	BlockLocalhost bool `yaml:"block_localhost"`
	// MaxRedirects maximum number of redirects to follow
	MaxRedirects int `yaml:"max_redirects"`
}

// DefaultURLValidatorConfig returns default secure configuration
func DefaultURLValidatorConfig() URLValidatorConfig {
	return URLValidatorConfig{
		AllowedSchemes:  []string{"https"},
		AllowedHosts:    []string{},
		BlockPrivateIPs: true,
		BlockLocalhost:  true,
		MaxRedirects:    3,
	}
}

// URLValidator validates URLs to prevent SSRF attacks
type URLValidator struct {
	config URLValidatorConfig
}

// NewURLValidator creates a new URL validator
func NewURLValidator(cfg URLValidatorConfig) *URLValidator {
	if len(cfg.AllowedSchemes) == 0 {
		cfg.AllowedSchemes = []string{"https"}
	}
	return &URLValidator{config: cfg}
}

// ValidationError represents a URL validation error
type ValidationError struct {
	URL     string
	Reason  string
	Details string
}

func (e *ValidationError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("URL validation failed for %q: %s (%s)", e.URL, e.Reason, e.Details)
	}
	return fmt.Sprintf("URL validation failed for %q: %s", e.URL, e.Reason)
}

// Validate checks if a URL is safe to request
func (v *URLValidator) Validate(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return &ValidationError{URL: rawURL, Reason: "invalid URL format", Details: err.Error()}
	}

	// Check scheme
	if err := v.validateScheme(parsed); err != nil {
		return err
	}

	// Check host
	if err := v.validateHost(parsed); err != nil {
		return err
	}

	// Resolve IP and check for private ranges
	if err := v.validateResolvedIP(parsed); err != nil {
		return err
	}

	return nil
}

// validateScheme checks if the URL scheme is allowed
func (v *URLValidator) validateScheme(parsed *url.URL) error {
	scheme := strings.ToLower(parsed.Scheme)

	for _, allowed := range v.config.AllowedSchemes {
		if strings.EqualFold(allowed, scheme) {
			return nil
		}
	}

	return &ValidationError{
		URL:     parsed.String(),
		Reason:  "scheme not allowed",
		Details: fmt.Sprintf("allowed: %v, got: %s", v.config.AllowedSchemes, scheme),
	}
}

// validateHost checks if the host is allowed
func (v *URLValidator) validateHost(parsed *url.URL) error {
	host := parsed.Hostname()

	if host == "" {
		return &ValidationError{URL: parsed.String(), Reason: "empty host"}
	}

	// If whitelist is configured, check against it
	if len(v.config.AllowedHosts) > 0 {
		if !v.isHostAllowed(host) {
			return &ValidationError{
				URL:    parsed.String(),
				Reason: "host not in allowed list",
			}
		}
	}

	// Check for localhost
	if v.config.BlockLocalhost && isLocalhost(host) {
		return &ValidationError{
			URL:    parsed.String(),
			Reason: "localhost not allowed",
		}
	}

	return nil
}

// validateResolvedIP resolves the hostname and checks for private IPs
func (v *URLValidator) validateResolvedIP(parsed *url.URL) error {
	host := parsed.Hostname()

	// Try to parse as IP directly
	if ip := net.ParseIP(host); ip != nil {
		if err := v.checkIP(ip, parsed.String()); err != nil {
			return err
		}
		return nil
	}

	// Resolve hostname
	addresses, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		// DNS resolution failure - could be intentional SSRF bypass
		return &ValidationError{
			URL:     parsed.String(),
			Reason:  "hostname resolution failed",
			Details: err.Error(),
		}
	}

	// Check all resolved IPs
	for _, address := range addresses {
		if err := v.checkIP(address.IP, parsed.String()); err != nil {
			return err
		}
	}

	return nil
}

// checkIP validates a single IP address
func (v *URLValidator) checkIP(ip net.IP, rawURL string) error {
	// Check localhost
	if v.config.BlockLocalhost && ip.IsLoopback() {
		return &ValidationError{
			URL:    rawURL,
			Reason: "loopback address not allowed",
		}
	}

	// Check private IPs
	if v.config.BlockPrivateIPs {
		if isPrivateIP(ip) {
			return &ValidationError{
				URL:     rawURL,
				Reason:  "private IP address not allowed",
				Details: ip.String(),
			}
		}
	}

	return nil
}

// isHostAllowed checks if host matches any allowed pattern
func (v *URLValidator) isHostAllowed(host string) bool {
	host = strings.ToLower(host)

	for _, pattern := range v.config.AllowedHosts {
		pattern = strings.ToLower(pattern)

		// Exact match
		if pattern == host {
			return true
		}

		// Wildcard match (*.example.com)
		if strings.HasPrefix(pattern, "*.") {
			suffix := pattern[1:] // .example.com
			if strings.HasSuffix(host, suffix) {
				return true
			}
		}
	}

	return false
}

// isLocalhost checks if a host is localhost
func isLocalhost(host string) bool {
	host = strings.ToLower(host)
	return host == "localhost" ||
		host == "127.0.0.1" ||
		host == "::1" ||
		host == "[::1]" ||
		host == "0.0.0.0"
}

// isPrivateIP checks if an IP is in a private range
func isPrivateIP(ip net.IP) bool {
	// Check for nil
	if ip == nil {
		return true // Treat as private (blocked) to be safe
	}

	// Private IPv4 ranges
	privateRanges := []struct {
		start net.IP
		end   net.IP
	}{
		// 10.0.0.0/8
		{net.ParseIP("10.0.0.0"), net.ParseIP("10.255.255.255")},
		// 172.16.0.0/12
		{net.ParseIP("172.16.0.0"), net.ParseIP("172.31.255.255")},
		// 192.168.0.0/16
		{net.ParseIP("192.168.0.0"), net.ParseIP("192.168.255.255")},
		// 169.254.0.0/16 (link-local)
		{net.ParseIP("169.254.0.0"), net.ParseIP("169.254.255.255")},
		// 127.0.0.0/8 (loopback)
		{net.ParseIP("127.0.0.0"), net.ParseIP("127.255.255.255")},
		// 0.0.0.0/8
		{net.ParseIP("0.0.0.0"), net.ParseIP("0.255.255.255")},
	}

	ip4 := ip.To4()
	if ip4 != nil {
		for _, r := range privateRanges {
			if ipInRange(ip4, r.start.To4(), r.end.To4()) {
				return true
			}
		}
	}

	// IPv6 private ranges
	if ip.To4() == nil {
		// fe80::/10 (link-local)
		if ip[0] == 0xfe && (ip[1]&0xc0) == 0x80 {
			return true
		}
		// fc00::/7 (unique local)
		if (ip[0] & 0xfe) == 0xfc {
			return true
		}
		// ::1 (loopback)
		if ip.Equal(net.IPv6loopback) {
			return true
		}
	}

	return false
}

// ipInRange checks if ip is in the range [start, end]
func ipInRange(ip, start, end net.IP) bool {
	if len(ip) != len(start) || len(ip) != len(end) {
		return false
	}

	for i := 0; i < len(ip); i++ {
		if ip[i] < start[i] || ip[i] > end[i] {
			if ip[i] < start[i] {
				return false
			}
			if ip[i] > end[i] && i < len(ip)-1 {
				return false
			}
		}
	}

	// More precise check
	gteStart := true
	lteEnd := true

	for i := 0; i < len(ip); i++ {
		if gteStart && ip[i] < start[i] {
			return false
		}
		if lteEnd && ip[i] > end[i] {
			return false
		}
		if ip[i] > start[i] {
			gteStart = true
		}
		if ip[i] < end[i] {
			lteEnd = true
		}
	}

	return true
}

// SafeHTTPClientConfig provides configuration for a safe HTTP client
type SafeHTTPClientConfig struct {
	Validator    *URLValidator
	MaxRedirects int
}

// ValidateRedirectURL validates a redirect URL during HTTP requests
func (v *URLValidator) ValidateRedirectURL(originalURL, redirectURL string) error {
	// First validate the redirect URL itself
	if err := v.Validate(redirectURL); err != nil {
		return fmt.Errorf("invalid redirect target: %w", err)
	}

	// Parse both URLs for additional checks
	original, err := url.Parse(originalURL)
	if err != nil {
		return fmt.Errorf("invalid original URL: %w", err)
	}

	redirect, err := url.Parse(redirectURL)
	if err != nil {
		return fmt.Errorf("invalid redirect URL: %w", err)
	}

	// Check for protocol downgrade (HTTPS -> HTTP)
	if strings.EqualFold(original.Scheme, "https") && strings.EqualFold(redirect.Scheme, "http") {
		return &ValidationError{
			URL:    redirectURL,
			Reason: "protocol downgrade not allowed",
		}
	}

	return nil
}
