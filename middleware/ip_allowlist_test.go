package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newAllowlistRouter wires an IP-allowlist middleware onto a single test
// endpoint and returns a flag indicating whether the downstream handler
// was reached.
func newAllowlistRouter(cidrs []string, called *bool) *gin.Engine {
	r := gin.New()
	r.GET("/protected", IPAllowlistMiddleware(cidrs), func(c *gin.Context) {
		if called != nil {
			*called = true
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestIPAllowlistMiddleware_AllowedIPv4(t *testing.T) {
	called := false
	router := newAllowlistRouter([]string{"10.0.0.0/8"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "10.5.6.7:12345"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("downstream must be called for allowed IP")
	}
}

func TestIPAllowlistMiddleware_BlockedIPv4(t *testing.T) {
	called := false
	router := newAllowlistRouter([]string{"10.0.0.0/8"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "192.168.1.1:9999"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ip not allowed"`) {
		t.Fatalf("expected ip not allowed JSON, got %s", rec.Body.String())
	}
	if called {
		t.Fatal("downstream must not be called for blocked IP")
	}
}

func TestIPAllowlistMiddleware_AllowedIPv6(t *testing.T) {
	called := false
	router := newAllowlistRouter([]string{"fe80::/10"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "[fe80::1]:80"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for fe80::1 in fe80::/10, got %d", rec.Code)
	}
	if !called {
		t.Fatal("downstream not called for allowed IPv6")
	}
}

func TestIPAllowlistMiddleware_BlockedIPv6(t *testing.T) {
	called := false
	router := newAllowlistRouter([]string{"fe80::/10"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "[2001:db8::1]:80"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for 2001:db8::1 vs fe80::/10, got %d", rec.Code)
	}
	if called {
		t.Fatal("downstream must not be called for blocked IPv6")
	}
}

func TestIPAllowlistMiddleware_XForwardedFor(t *testing.T) {
	// GetClientIP picks the first XFF entry as the client.
	called := false
	router := newAllowlistRouter([]string{"10.0.0.0/8"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "1.2.3.4:80"
	req.Header.Set("X-Forwarded-For", "10.0.0.5,1.2.3.4")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 honouring XFF, got %d", rec.Code)
	}
	if !called {
		t.Fatal("downstream not called when XFF is in allowlist")
	}
}

func TestIPAllowlistMiddleware_XRealIP(t *testing.T) {
	called := false
	router := newAllowlistRouter([]string{"10.0.0.0/8"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "1.2.3.4:80"
	req.Header.Set("X-Real-IP", "10.0.0.7")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 honouring X-Real-IP, got %d", rec.Code)
	}
	if !called {
		t.Fatal("downstream not called when X-Real-IP is in allowlist")
	}
}

func TestIPAllowlistMiddleware_MultipleCIDRs(t *testing.T) {
	cidrs := []string{"10.0.0.0/8", "192.168.1.5/32"}

	// In-list /32 host
	{
		called := false
		router := newAllowlistRouter(cidrs, &called)
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.RemoteAddr = "192.168.1.5:1"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("192.168.1.5 should be allowed, got %d", rec.Code)
		}
		if !called {
			t.Fatal("downstream not called for /32 match")
		}
	}

	// Off-by-one host
	{
		called := false
		router := newAllowlistRouter(cidrs, &called)
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.RemoteAddr = "192.168.1.6:1"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("192.168.1.6 should be blocked, got %d", rec.Code)
		}
		if called {
			t.Fatal("downstream must not be called for off-by-one")
		}
	}
}

func TestIPAllowlistMiddleware_EmptyCIDRList_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty CIDR list (default-deny)")
		}
	}()
	_ = IPAllowlistMiddleware([]string{})
}

func TestIPAllowlistMiddleware_NilCIDRList_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil CIDR list")
		}
	}()
	_ = IPAllowlistMiddleware(nil)
}

func TestIPAllowlistMiddleware_MalformedCIDR_Panic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on malformed CIDR")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if !strings.Contains(msg, "not-a-cidr") {
			t.Fatalf("panic should reference offending CIDR, got %q", msg)
		}
	}()
	_ = IPAllowlistMiddleware([]string{"10.0.0.0/8", "not-a-cidr"})
}

func TestIPAllowlistMiddleware_InvalidIP(t *testing.T) {
	// Force GetClientIP to return a value net.ParseIP cannot parse: an XFF
	// header set to a clearly-invalid token.
	called := false
	router := newAllowlistRouter([]string{"10.0.0.0/8"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "1.2.3.4:80"
	req.Header.Set("X-Forwarded-For", "garbage-not-an-ip")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 fail-closed on unparsable IP, got %d", rec.Code)
	}
	if called {
		t.Fatal("downstream must not be called when client IP cannot be parsed")
	}
}

func TestIPAllowlistMiddleware_IPv4MappedIPv6(t *testing.T) {
	// IPv4-mapped IPv6 (::ffff:10.0.0.1) should match an IPv4 CIDR (10.0.0.0/8).
	// Route the address via X-Real-IP so RemoteAddr-port parsing isn't an issue.
	called := false
	router := newAllowlistRouter([]string{"10.0.0.0/8"}, &called)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = "1.2.3.4:80"
	req.Header.Set("X-Real-IP", "::ffff:10.0.0.1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for IPv4-mapped IPv6 ::ffff:10.0.0.1 in 10.0.0.0/8, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("downstream not called for IPv4-mapped IPv6")
	}
}
