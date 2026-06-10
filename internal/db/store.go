package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/soju06/codex-lb/internal/config"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(cfg config.Config) (*Store, error) {
	conn, err := sql.Open("sqlite", cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("configure sqlite busy timeout: %w", err)
	}
	return &Store{db: conn}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) RunMigrations(dir string) error {
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(s.db, dir); err != nil {
		return fmt.Errorf("run goose migrations: %w", err)
	}
	return nil
}
