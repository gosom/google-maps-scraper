package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ConnectOptions struct {
	pingTimeout     time.Duration
	maxConns        int32
	minConns        int32
	maxConnLifetime time.Duration
	maxConnIdleTime time.Duration
}

type ConnectOption func(*ConnectOptions)

func WithPingTimeout(d time.Duration) ConnectOption {
	return func(o *ConnectOptions) {
		o.pingTimeout = d
	}
}

func WithMaxConns(n int32) ConnectOption {
	return func(o *ConnectOptions) {
		o.maxConns = n
	}
}

func WithMinConns(n int32) ConnectOption {
	return func(o *ConnectOptions) {
		o.minConns = n
	}
}

func WithMaxConnLifetime(d time.Duration) ConnectOption {
	return func(o *ConnectOptions) {
		o.maxConnLifetime = d
	}
}

func WithMaxConnIdleTime(d time.Duration) ConnectOption {
	return func(o *ConnectOptions) {
		o.maxConnIdleTime = d
	}
}

// Connect establishes a connection pool to the PostgreSQL database using the provided DSN and options.
func Connect(ctx context.Context, dsn string, opts ...ConnectOption) (*pgxpool.Pool, error) {
	options := &ConnectOptions{
		pingTimeout:     5 * time.Second,
		maxConns:        10,
		minConns:        2,
		maxConnLifetime: time.Hour,
		maxConnIdleTime: 30 * time.Minute,
	}
	for _, opt := range opts {
		opt(options)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	config.MaxConns = options.maxConns
	config.MinConns = options.minConns
	config.MaxConnLifetime = options.maxConnLifetime
	config.MaxConnIdleTime = options.maxConnIdleTime

	dbPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, options.pingTimeout)
	defer cancel()

	if err = dbPool.Ping(pingCtx); err != nil {
		dbPool.Close()
		return nil, err
	}

	return dbPool, nil
}
