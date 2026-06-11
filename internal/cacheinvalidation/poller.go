package cacheinvalidation

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/soju06/codex-lb/internal/config"
	"github.com/soju06/codex-lb/internal/db"
)

const (
	NamespaceAPIKey   = "api_key"
	NamespaceFirewall = "firewall"
	NamespaceSettings = "settings"
)

type Poller struct {
	store    *db.Store
	logger   *slog.Logger
	interval time.Duration
	enabled  bool

	mu          sync.Mutex
	callbacks   map[string][]func()
	known       map[string]int64
	initialized bool
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewPoller(store *db.Store, logger *slog.Logger, cfg config.Config) *Poller {
	interval := cfg.CacheInvalidationInterval
	if interval <= 0 {
		interval = time.Second
	}
	return &Poller{
		store:     store,
		logger:    logger,
		interval:  interval,
		enabled:   cfg.CacheInvalidationEnabled,
		callbacks: map[string][]func(){},
		known:     map[string]int64{},
	}
}

func (p *Poller) OnInvalidation(namespace string, callback func()) {
	if p == nil || namespace == "" || callback == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callbacks[namespace] = append(p.callbacks[namespace], callback)
}

func (p *Poller) Start(ctx context.Context) {
	if p == nil || !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})
	go p.run(runCtx)
}

func (p *Poller) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	p.cancel = nil
	p.done = nil
	p.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Poller) Bump(ctx context.Context, namespace string) error {
	if p == nil || namespace == "" {
		return nil
	}
	_, err := p.store.DB().ExecContext(ctx, `
		INSERT INTO cache_invalidation (namespace, version)
		VALUES (?, 1)
		ON CONFLICT (namespace) DO UPDATE SET version = cache_invalidation.version + 1
	`, namespace)
	if err != nil {
		return fmt.Errorf("bump cache invalidation %s: %w", namespace, err)
	}
	return nil
}

func (p *Poller) run(ctx context.Context) {
	defer close(p.done)
	p.pollOnce(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *Poller) pollOnce(ctx context.Context) {
	rows, err := p.store.DB().QueryContext(ctx, `
		SELECT namespace, version FROM cache_invalidation
	`)
	if err != nil {
		p.logger.Debug("cache invalidation poll failed", "error", err)
		return
	}
	defer rows.Close()

	versions := map[string]int64{}
	for rows.Next() {
		var namespace string
		var version int64
		if err := rows.Scan(&namespace, &version); err != nil {
			p.logger.Debug("cache invalidation scan failed", "error", err)
			return
		}
		versions[namespace] = version
	}
	if err := rows.Err(); err != nil {
		p.logger.Debug("cache invalidation row iteration failed", "error", err)
		return
	}

	callbacks := p.changedCallbacks(versions)
	for _, callback := range callbacks {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					p.logger.Debug("cache invalidation callback panic", "panic", recovered)
				}
			}()
			callback()
		}()
	}
}

func (p *Poller) changedCallbacks(versions map[string]int64) []func() {
	p.mu.Lock()
	defer p.mu.Unlock()

	var callbacks []func()
	for namespace, version := range versions {
		prev, known := p.known[namespace]
		changed := (known && version != prev) || (!known && p.initialized && version > 0)
		if changed {
			callbacks = append(callbacks, p.callbacks[namespace]...)
		}
		p.known[namespace] = version
	}
	p.initialized = true
	return callbacks
}
