package firewall

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/httputil"
)

type MiddlewareOptions struct {
	TrustProxyHeaders bool
	TrustedProxyCIDRs []string
	CacheTTL          time.Duration
}

type Firewall struct {
	repo              Repository
	cache             *DecisionCache
	trustProxyHeaders bool
	trustedNetworks   []*net.IPNet
}

type DecisionCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	version int64
	entries map[string]cachedDecision
}

type cachedDecision struct {
	allowed   bool
	expiresAt time.Time
	version   int64
}

func NewFirewall(repo Repository, opts MiddlewareOptions) (*Firewall, error) {
	trustedNetworks, err := parseTrustedNetworks(opts.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	ttl := opts.CacheTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Firewall{
		repo:              repo,
		cache:             NewDecisionCache(ttl),
		trustProxyHeaders: opts.TrustProxyHeaders,
		trustedNetworks:   trustedNetworks,
	}, nil
}

func NewDecisionCache(ttl time.Duration) *DecisionCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &DecisionCache{ttl: ttl, entries: map[string]cachedDecision{}}
}

func (c *DecisionCache) Get(ip string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[ip]
	if !ok || time.Now().After(entry.expiresAt) || entry.version != c.version {
		return false, false
	}
	return entry.allowed, true
}

func (c *DecisionCache) Set(ip string, allowed bool, version int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if version != c.version {
		return
	}
	c.entries[ip] = cachedDecision{allowed: allowed, expiresAt: time.Now().Add(c.ttl), version: version}
}

func (c *DecisionCache) Version() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

func (c *DecisionCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version++
	c.entries = map[string]cachedDecision{}
}

func (f *Firewall) InvalidateCache() {
	if f != nil && f.cache != nil {
		f.cache.InvalidateAll()
	}
}

func (f *Firewall) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isProtectedProxyPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		allowed, err := f.IsAllowed(r.Context(), ResolveClientIP(r, f.trustProxyHeaders, f.trustedNetworks))
		if err != nil {
			httputil.WriteServerError(w, err)
			return
		}
		if !allowed {
			httputil.WriteJSON(w, http.StatusForbidden, map[string]any{
				"error": map[string]any{
					"message": "Access denied for client IP",
					"type":    "access_error",
					"code":    "ip_forbidden",
				},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (f *Firewall) IsAllowed(ctx context.Context, clientIP string) (bool, error) {
	if clientIP == "" {
		return false, nil
	}
	if allowed, ok := f.cache.Get(clientIP); ok {
		return allowed, nil
	}
	version := f.cache.Version()
	allowed, err := f.repo.IsAllowed(ctx, clientIP)
	if err != nil {
		return false, err
	}
	f.cache.Set(clientIP, allowed, version)
	return allowed, nil
}

func isProtectedProxyPath(path string) bool {
	return path == "/backend-api/codex" || strings.HasPrefix(path, "/backend-api/codex/") ||
		path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func ResolveClientIP(r *http.Request, trustProxyHeaders bool, trustedNetworks []*net.IPNet) string {
	socketIP := remoteHost(r.RemoteAddr)
	if trustProxyHeaders && socketIP != "" && isTrustedProxySource(socketIP, trustedNetworks) {
		if resolved := resolveClientIPFromXFFChain(socketIP, r.Header.Get("X-Forwarded-For"), trustedNetworks); resolved != "" {
			return resolved
		}
	}
	return socketIP
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func resolveClientIPFromXFFChain(socketIP, forwardedFor string, trustedNetworks []*net.IPNet) string {
	if strings.TrimSpace(forwardedFor) == "" {
		return ""
	}
	parts := strings.Split(forwardedFor, ",")
	hops := make([]string, 0, len(parts))
	for _, part := range parts {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip == nil {
			return ""
		}
		hops = append(hops, ip.String())
	}
	chain := append(hops, socketIP)
	resolved := socketIP
	for i := len(chain) - 1; i > 0; i-- {
		currentProxy := chain[i]
		previousHop := chain[i-1]
		if !isTrustedProxySource(currentProxy, trustedNetworks) {
			resolved = currentProxy
			break
		}
		resolved = previousHop
	}
	return resolved
}

func parseTrustedNetworks(cidrs []string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(cidrs))
	for _, raw := range cidrs {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if !strings.Contains(value, "/") {
			if ip := net.ParseIP(value); ip != nil {
				if ip.To4() != nil {
					value += "/32"
				} else {
					value += "/128"
				}
			}
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, err
		}
		networks = append(networks, network)
	}
	return networks, nil
}

func isTrustedProxySource(host string, networks []*net.IPNet) bool {
	if len(networks) == 0 {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
