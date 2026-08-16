// Package postgres owns PostgreSQL connection pool setup and health checks.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidDatabaseConfig indicates that the configured database connection
// string could not be parsed.
var ErrInvalidDatabaseConfig = errors.New("invalid database configuration")

// OpenPool creates a PostgreSQL connection pool from a connection string.
func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", ErrInvalidDatabaseConfig)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}

	return pool, nil
}

// Ping verifies PostgreSQL is reachable within the configured timeout.
func Ping(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) error {
	if pool == nil {
		return fmt.Errorf("postgres pool is nil")
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	return nil
}
