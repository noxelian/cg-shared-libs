package middleware

import (
	"net"
	"net/http"

	"github.com/4ubak/cg-shared-libs/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// IPAllowlistMiddleware returns a Gin middleware that allows only requests
// whose client IP belongs to one of the supplied CIDR ranges.
//
// CIDRs are parsed exactly once at construction time. Both an empty cidrs
// slice and any malformed CIDR entry panic at init — fail-fast: a
// misconfigured allowlist must NOT silently degrade into allow-all (security
// trap) or block-all (silent outage). An empty list is treated as a
// configuration error, not as "allow nothing".
//
// Client IP is extracted via GetClientIP (defined in ratelimit.go), which
// honors X-Forwarded-For and X-Real-IP headers in that order before falling
// back to Gin's c.ClientIP().
//
// IMPORTANT — Trusted proxies (X-Forwarded-For trust model):
//
//	This middleware trusts X-Forwarded-For / X-Real-IP headers to extract the
//	real client IP. To prevent spoofing by untrusted upstream callers, the
//	caller MUST configure Gin's trusted-proxies list via
//	    engine.SetTrustedProxies([]string{"<reverse-proxy CIDR>"})
//	Without this configuration Gin's c.ClientIP() will honor XFF from any
//	caller, allowing an attacker to bypass the allowlist by setting the
//	header. See https://gin-gonic.com/docs/examples/security/ for details.
//
// Known limitation: whitespace inside comma-separated X-Forwarded-For values
// is NOT trimmed (matches the existing GetClientIP behavior). Reverse proxies
// SHOULD emit canonical "ip1, ip2" format; aberrant " ip1 ,ip2" may parse to
// an invalid IP and fail-closed (403).
//
// IPv4-mapped IPv6 addresses (e.g. ::ffff:10.0.0.1) are matched against IPv4
// CIDRs automatically by net.IPNet.Contains (Go stdlib normalises to the
// 4-byte form when needed).
//
// Composition contract (caller responsibility):
//   - Mandatory chain order: IPAllowlistMiddleware → HMACSignatureMiddleware → handler.
//
// On block (or unparsable client IP): HTTP 403 + body
//
//	{"error":"ip not allowed"}
//
// This middleware does NOT touch tls.Config.InsecureSkipVerify and never
// logs request headers or body bytes — only client IP, request path, and
// path-level diagnostics.
func IPAllowlistMiddleware(cidrs []string) gin.HandlerFunc {
	if len(cidrs) == 0 {
		panic("middleware: IPAllowlistMiddleware requires at least one CIDR (default-deny semantics)")
	}

	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("middleware: invalid CIDR in allowlist: " + cidr + " (" + err.Error() + ")")
		}
		nets = append(nets, network)
	}

	return func(c *gin.Context) {
		clientIPStr := GetClientIP(c)
		clientIP := net.ParseIP(clientIPStr)
		if clientIP == nil {
			logger.Warn("ip allowlist: cannot parse client IP",
				zap.String("client_ip_raw", clientIPStr),
				zap.String("path", c.Request.URL.Path))
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "ip not allowed",
			})
			return
		}

		for _, n := range nets {
			if n.Contains(clientIP) {
				c.Next()
				return
			}
		}

		logger.Warn("ip allowlist: blocked request",
			zap.String("client_ip", clientIPStr),
			zap.String("path", c.Request.URL.Path))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "ip not allowed",
		})
	}
}
