package saas

// Environment variable names
const (
	// DefaultDatabaseURL is the local-development PostgreSQL endpoint.
	DefaultDatabaseURL = "postgres://postgres:postgres@localhost:5432/gmaps_pro?sslmode=disable" //nolint:gosec // Non-production local default; deployments override it via DATABASE_URL.

	// Server
	EnvAddr              = "ADDR"
	EnvDatabaseURL       = "DATABASE_URL"
	EnvDBMaxConns        = "DB_MAX_CONNS"
	EnvDBMinConns        = "DB_MIN_CONNS"
	EnvDBMaxConnLifetime = "DB_MAX_CONN_LIFETIME"
	EnvDBMaxConnIdleTime = "DB_MAX_CONN_IDLE_TIME"
	EnvEncryptionKey     = "ENCRYPTION_KEY"

	// Worker
	EnvConcurrency     = "CONCURRENCY"
	EnvFastMode        = "FAST_MODE"
	EnvDebug           = "DEBUG"
	EnvMaxJobsPerCycle = "MAX_JOBS_PER_CYCLE"
	EnvProxies         = "PROXIES"
	EnvRiverMaxWorkers = "RIVER_MAX_WORKERS"
)
