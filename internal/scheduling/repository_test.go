package scheduling_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/soju06/codex-lb/internal/config"
	dbpkg "github.com/soju06/codex-lb/internal/db"
	"github.com/soju06/codex-lb/internal/scheduling"
)

func newTestStore(t *testing.T) *dbpkg.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "store.db")
	store, err := dbpkg.Open(config.Config{DatabasePath: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RunMigrations("../../migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return store
}

func TestSchedulerLeaderElection(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repo := scheduling.NewRepository(store)

	acquired, err := repo.TryAcquireLeader(ctx, "node-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire leader: %v", err)
	}
	if !acquired {
		t.Fatalf("expected node-a to acquire leadership")
	}

	acquired, err = repo.TryAcquireLeader(ctx, "node-b", time.Minute)
	if err != nil {
		t.Fatalf("acquire leader (node-b): %v", err)
	}
	if acquired {
		t.Fatalf("expected node-b to not acquire leadership while node-a holds an active lease")
	}

	renewed, err := repo.RenewLeader(ctx, "node-a", time.Minute)
	if err != nil {
		t.Fatalf("renew leader: %v", err)
	}
	if !renewed {
		t.Fatalf("expected node-a to renew its lease")
	}

	renewed, err = repo.RenewLeader(ctx, "node-b", time.Minute)
	if err != nil {
		t.Fatalf("renew leader (node-b): %v", err)
	}
	if renewed {
		t.Fatalf("expected node-b renew to fail since it isn't the leader")
	}
}
