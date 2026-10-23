package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Scanner-only traps. Real panel, gateway, payment return, canvas, and assets are not in this list.
var honeypotPrefixes = []string{
	"/.aws",
	"/.bzr",
	"/.dockerenv",
	"/.ds_store",
	"/.env",
	"/.git",
	"/.hg",
	"/.ssh",
	"/.svn",
	"/_ignition",
	"/actuator",
	"/adminer",
	"/api/install",
	"/autodiscover",
	"/boaform",
	"/cgi-bin",
	"/cfide",
	"/dana-na",
	"/debug/pprof",
	"/debug/vars",
	"/druid",
	"/ecp",
	"/elmah.axd",
	"/exchange",
	"/goform",
	"/gponform",
	"/heapdump",
	"/hnap1",
	"/horizon",
	"/id_rsa",
	"/invoker",
	"/jenkins",
	"/jmx-console",
	"/jndi",
	"/jolokia",
	"/manager/html",
	"/manager/text",
	"/myadmin",
	"/owa",
	"/phpmyadmin",
	"/pma",
	"/rdweb",
	"/remote/login",
	"/server-info",
	"/server-status",
	"/solr",
	"/sslvpn",
	"/telescope",
	"/trace.axd",
	"/v2/api-docs",
	"/v3/api-docs",
	"/vendor/phpunit",
	"/web-console",
	"/web.config",
	"/wp-admin",
	"/wp-config.php",
	"/wp-content",
	"/wp-includes",
	"/wp-login.php",
	"/xmlrpc.php",
	"/swagger",
	"/swagger-ui",
	"/swagger-ui.html",
}

var honeypotExts = []string{".php", ".asp", ".aspx", ".jsp", ".cgi", ".axd", ".sql"}

const (
	honeypotBanTTL       = 24 * time.Hour
	honeypotBanKeyPrefix = "kedaya:security:v1:ban:"
)

// honeypotExemptNets holds the operator's own edge CIDRs. A request that
// appears to come from one of these must not be banned, otherwise the gateway
// bans its own reverse proxy on the first scanner hit.
//
// The list is deliberately configuration, not source: this repository is
// public, so real addresses must never be compiled into it. Configure with
// security.honeypot_exempt_nets (or SECURITY_HONEYPOT_EXEMPT_NETS).
var honeypotExemptNets atomic.Pointer[[]netip.Prefix]

// SetHoneypotExemptNets replaces the exempt CIDR set. Accepts CIDRs and bare
// IPs; invalid entries are ignored so a typo cannot silently disable the ban
// path.
func SetHoneypotExemptNets(cidrs []string) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			if prefix, err := netip.ParsePrefix(raw); err == nil {
				prefixes = append(prefixes, prefix.Masked())
			}
			continue
		}
		if addr, err := netip.ParseAddr(raw); err == nil {
			addr = addr.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
	honeypotExemptNets.Store(&prefixes)
}

func Honeypot(rdb *redis.Client, exemptNets ...string) gin.HandlerFunc {
	if len(exemptNets) > 0 {
		SetHoneypotExemptNets(exemptNets)
	}
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.URL == nil {
			c.Next()
			return
		}
		class := honeypotClass(c)
		if class == "" {
			c.Next()
			return
		}
		MarkIngressRejected(c, IngressRejectHoneypot)
		noteHoneypotBan(rdb, SecurityClientIP(c), class)
		c.Header("Cache-Control", "no-store")
		c.AbortWithStatus(http.StatusNotFound)
	}
}

func honeypotClass(c *gin.Context) string {
	path, rawPath, rawQuery := honeypotRequestParts(c)
	if honeypotTraversal(path) || honeypotTraversal(rawPath) || honeypotQuery(rawQuery) {
		return "scanner"
	}
	if honeypotPath(path) {
		// SigV4 只在扫描器特征路径上才封。正常 API 路径上的预签名回链交给后面的鉴权。
		if honeypotSignature(c) {
			return "scanner"
		}
		if strings.HasSuffix(honeypotNorm(path), ".php") {
			return "unknown_php"
		}
		return "trap"
	}
	return ""
}

func noteHoneypotBan(rdb *redis.Client, ip, class string) {
	if rdb == nil {
		return
	}
	key, value, ok := honeypotBanSpec(ip, class)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// NX keeps a longer edge ban intact. ponytail: fixed 24h, edge escalates on its own hits.
	_ = rdb.SetNX(ctx, key, value, honeypotBanTTL).Err()
}

func honeypotBanSpec(ip, class string) (key, value string, ok bool) {
	switch class {
	case "trap", "unknown_php", "scanner":
	default:
		return "", "", false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return "", "", false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || honeypotExempt(addr) {
		return "", "", false
	}
	return honeypotBanKeyPrefix + addr.String(), class, true
}

func honeypotExempt(addr netip.Addr) bool {
	prefixes := honeypotExemptNets.Load()
	if prefixes == nil {
		return false
	}
	for _, prefix := range *prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func honeypotRequestParts(c *gin.Context) (path, rawPath, rawQuery string) {
	path = c.Request.URL.Path
	rawQuery = c.Request.URL.RawQuery
	rawPath = c.Request.RequestURI
	if i := strings.IndexByte(rawPath, '?'); i >= 0 {
		rawPath = rawPath[:i]
	}
	if c.Request.URL.RawPath != "" {
		rawPath = c.Request.URL.RawPath
	}
	return path, rawPath, rawQuery
}

func honeypotTraversal(s string) bool {
	if s == "" {
		return false
	}
	if strings.Contains(s, "..") || strings.Contains(s, "\x00") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "%2e%2e") || strings.Contains(lower, "%00") || strings.Contains(lower, "%252e")
}

func honeypotQuery(raw string) bool {
	if raw == "" {
		return false
	}
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "php://") ||
		strings.Contains(lower, "phar://") ||
		strings.Contains(lower, "expect://") ||
		strings.Contains(lower, "gopher://") ||
		strings.Contains(lower, "dict://") ||
		strings.Contains(lower, "xdebug_session") ||
		strings.Contains(lower, "auto_prepend_file") ||
		strings.Contains(lower, "allow_url_include") ||
		strings.Contains(lower, "auto_append_file")
}

func honeypotSignature(c *gin.Context) bool {
	if c == nil {
		return false
	}
	auth := strings.ToLower(strings.TrimSpace(c.GetHeader("Authorization")))
	if strings.HasPrefix(auth, "aws4-hmac-sha256") || strings.HasPrefix(auth, "aws ") {
		return true
	}
	if c.GetHeader("X-Amz-Algorithm") != "" || c.GetHeader("X-Amz-Credential") != "" {
		return true
	}
	if c.Request != nil && c.Request.URL != nil {
		raw := strings.ToLower(c.Request.URL.RawQuery)
		return strings.Contains(raw, "x-amz-algorithm") || strings.Contains(raw, "x-amz-credential")
	}
	return false
}

func honeypotPath(path string) bool {
	p := honeypotNorm(path)
	if p == "" || p == "/" {
		return false
	}
	for _, prefix := range honeypotPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") || strings.HasPrefix(p, prefix+".") {
			return true
		}
	}
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	for _, ext := range honeypotExts {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

func honeypotNorm(path string) string {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return strings.TrimRight(p, "/")
}
