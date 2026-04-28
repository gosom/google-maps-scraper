package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// RegisterDBPoolCollector exports sql.DB connection pool statistics as
// Prometheus metrics using the standard go_sql_* metric names. Uses the
// built-in collectors.NewDBStatsCollector from client_golang.
//
// Passing nil for reg uses the default Prometheus registry.
// Safe to call multiple times — duplicate registration is silently ignored.
func RegisterDBPoolCollector(db *sql.DB, reg prometheus.Registerer) {
	if db == nil {
		panic("metrics.RegisterDBPoolCollector: db must not be nil")
	}
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	c := collectors.NewDBStatsCollector(db, "")
	if err := reg.Register(c); err != nil {
		if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
			panic(err)
		}
	}
}
