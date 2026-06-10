package settings

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/soju06/codex-lb/internal/httputil"
)

var loopbackHosts = map[string]struct{}{
	"localhost": {},
	"127.0.0.1": {},
	"::1":       {},
	"[::1]":     {},
}

type Handler struct {
	repo Repository
}

func NewHandler(repo Repository) Handler {
	return Handler{repo: repo}
}

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	settings, err := h.repo.Get(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, settings)
}

func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	current, err := h.repo.Get(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	var payload UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	updated, err := h.repo.Update(r.Context(), current, payload)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, updated)
}

func (h Handler) ConnectAddress(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, RuntimeConnectAddressResponse{
		ConnectAddress: resolveRuntimeConnectAddress(r),
	})
}

func (h Handler) UpstreamProxyAdmin(w http.ResponseWriter, r *http.Request) {
	admin, err := h.repo.UpstreamProxyAdmin(r.Context())
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, admin)
}

func (h Handler) CreateProxyEndpoint(w http.ResponseWriter, r *http.Request) {
	var payload UpstreamProxyEndpointCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	endpoint, err := h.repo.CreateProxyEndpoint(r.Context(), payload)
	if err != nil {
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, endpoint)
}

func (h Handler) CreateProxyPool(w http.ResponseWriter, r *http.Request) {
	var payload UpstreamProxyPoolCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	pool, err := h.repo.CreateProxyPool(r.Context(), payload)
	if err != nil {
		if errors.Is(err, ErrProxyEndpointNotFound) {
			httputil.WriteError(w, http.StatusBadRequest, "proxy_endpoint_not_found", "Proxy endpoint not found")
			return
		}
		httputil.WriteServerError(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, pool)
}

func (h Handler) AddProxyPoolMember(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolID")
	var payload UpstreamProxyPoolMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	pool, err := h.repo.AddProxyPoolMember(r.Context(), poolID, payload)
	if err != nil {
		switch {
		case errors.Is(err, ErrProxyPoolNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "proxy_pool_not_found", "Proxy pool not found")
		case errors.Is(err, ErrProxyEndpointNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "proxy_endpoint_not_found", "Proxy endpoint not found")
		default:
			httputil.WriteServerError(w, err)
		}
		return
	}
	httputil.WriteJSON(w, http.StatusOK, pool)
}

func (h Handler) PutAccountProxyBinding(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "accountID")
	var payload AccountProxyBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}
	binding, err := h.repo.PutAccountProxyBinding(r.Context(), accountID, payload)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "account_not_found", "Account not found")
		case errors.Is(err, ErrProxyPoolNotFound):
			httputil.WriteError(w, http.StatusBadRequest, "proxy_pool_not_found", "Proxy pool not found")
		default:
			httputil.WriteServerError(w, err)
		}
		return
	}
	httputil.WriteJSON(w, http.StatusOK, binding)
}

func resolveRuntimeConnectAddress(r *http.Request) string {
	if override := strings.TrimSpace(os.Getenv("CODEX_LB_CONNECT_ADDRESS")); override != "" {
		return override
	}
	host := r.URL.Hostname()
	if isNonLoopbackIPv4(host) {
		return host
	}
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized != "" {
		if _, isLoopback := loopbackHosts[normalized]; !isLoopback {
			if resolved := resolveHostnameIPv4(host); resolved != "" {
				return resolved
			}
			return host
		}
	}
	return "<codex-lb-ip-or-dns>"
}

func isNonLoopbackIPv4(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return false
	}
	return !ip.IsLoopback() && !ip.IsUnspecified()
}

func resolveHostnameIPv4(hostname string) string {
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			return ip.String()
		}
	}
	return ""
}
