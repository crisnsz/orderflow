package database

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	connectMaxAttempts = 30
	connectRetryDelay  = 2 * time.Second

	migrationLockID = 727274
)

func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < connectMaxAttempts; attempt++ {
		if lastErr = pool.Ping(ctx); lastErr == nil {
			return pool, nil
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(connectRetryDelay):
		}
	}
	pool.Close()
	return nil, fmt.Errorf("database not reachable: %w", lastErr)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) error {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return err
	}
	defer func() { _, _ = connection.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID) }()

	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	fileNames, err := listMigrationFiles(fsys, dir)
	if err != nil {
		return err
	}
	for _, fileName := range fileNames {
		alreadyApplied, err := isMigrationApplied(ctx, connection, fileName)
		if err != nil {
			return err
		}
		if alreadyApplied {
			continue
		}
		if err := applyMigration(ctx, connection, fsys, dir, fileName); err != nil {
			return err
		}
		slog.InfoContext(ctx, "migration applied", "version", fileName)
	}
	return nil
}

func listMigrationFiles(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var fileNames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			fileNames = append(fileNames, entry.Name())
		}
	}
	sort.Strings(fileNames)
	return fileNames, nil
}

func isMigrationApplied(ctx context.Context, connection *pgxpool.Conn, version string) (bool, error) {
	var applied bool
	err := connection.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied)
	return applied, err
}

func applyMigration(ctx context.Context, connection *pgxpool.Conn, fsys fs.FS, dir, fileName string) error {
	migrationSQL, err := fs.ReadFile(fsys, dir+"/"+fileName)
	if err != nil {
		return err
	}
	tx, err := connection.Begin(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, string(migrationSQL)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("migration %s: %w", fileName, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, fileName); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
