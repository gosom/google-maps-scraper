package config

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Service provides access to dynamic configuration values stored in the system_config table
type Service struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string]cachedEntry
}

type cachedEntry struct {
	value     string
	expiresAt time.Time
}

const defaultTTL = time.Minute

func New(db *sql.DB) *Service {
	return &Service{db: db, cache: make(map[string]cachedEntry)}
}

// GetString returns a string config value. Environment variable overrides DB values when present.
// The env var name is derived from key by uppercasing and replacing dots with underscores.
func (s *Service) GetString(ctx context.Context, key string, defaultValue string) (string, error) {
	if v, ok := s.envOverride(key); ok {
		return v, nil
	}
	if v, ok := s.getFromCache(key); ok {
		return v, nil
	}
	const q = `SELECT value FROM system_config WHERE key = $1 LIMIT 1`
	var v string
	err := s.db.QueryRowContext(ctx, q, key).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return defaultValue, nil
		}
		return "", err
	}
	s.putInCache(key, v)
	return v, nil
}

// GetBool returns a boolean config value.
func (s *Service) GetBool(ctx context.Context, key string, defaultValue bool) (bool, error) {
	if v, ok := s.envOverride(key); ok {
		return strings.EqualFold(v, "true") || v == "1", nil
	}
	if v, ok := s.getFromCache(key); ok {
		return strings.EqualFold(v, "true") || v == "1", nil
	}
	const q = `SELECT value FROM system_config WHERE key = $1 LIMIT 1`
	var v string
	err := s.db.QueryRowContext(ctx, q, key).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return defaultValue, nil
		}
		return false, err
	}
	s.putInCache(key, v)
	return strings.EqualFold(v, "true") || v == "1", nil
}

// GetInt returns an integer config value.
func (s *Service) GetInt(ctx context.Context, key string, defaultValue int) (int, error) {
	if v, ok := s.envOverride(key); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return defaultValue, nil
		}
		return parsed, nil
	}
	if v, ok := s.getFromCache(key); ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return defaultValue, nil
		}
		return parsed, nil
	}
	const q = `SELECT value, min_value, max_value FROM system_config WHERE key = $1 LIMIT 1`
	var v, minv, maxv sql.NullString
	err := s.db.QueryRowContext(ctx, q, key).Scan(&v, &minv, &maxv)
	if err != nil {
		if err == sql.ErrNoRows {
			return defaultValue, nil
		}
		return 0, err
	}
	s.putInCache(key, v.String)
	parsed, err := strconv.Atoi(strings.TrimSpace(v.String))
	if err != nil {
		return defaultValue, nil
	}
	if minv.Valid {
		if minParsed, err := strconv.Atoi(strings.TrimSpace(minv.String)); err == nil && parsed < minParsed {
			parsed = minParsed
		}
	}
	if maxv.Valid {
		if maxParsed, err := strconv.Atoi(strings.TrimSpace(maxv.String)); err == nil && parsed > maxParsed {
			parsed = maxParsed
		}
	}
	return parsed, nil
}

// Upsert writes a configuration value with associated metadata type.
func (s *Service) Upsert(ctx context.Context, key string, value string, typ string, description string) error {
	const q = `INSERT INTO system_config (key, value, type, description, updated_at, updated_by)
	           VALUES ($1, $2, $3, $4, NOW(), 'system')
	           ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, type = EXCLUDED.type, description = EXCLUDED.description, updated_at = NOW()`
	_, err := s.db.ExecContext(ctx, q, key, value, typ, description)
	if err == nil {
		s.mu.Lock()
		delete(s.cache, key)
		s.mu.Unlock()
	}
	return err
}

func (s *Service) envOverride(key string) (string, bool) {
	envKey := strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
	if v := os.Getenv(envKey); v != "" {
		return v, true
	}
	return "", false
}

func (s *Service) getFromCache(key string) (string, bool) {
	s.mu.RLock()
	entry, ok := s.cache[key]
	s.mu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		s.mu.Lock()
		delete(s.cache, key)
		s.mu.Unlock()
		return "", false
	}
	return entry.value, true
}

func (s *Service) putInCache(key, value string) {
	s.mu.Lock()
	s.cache[key] = cachedEntry{value: value, expiresAt: time.Now().Add(defaultTTL)}
	s.mu.Unlock()
}

// GetRequiredString returns a required value or an error if missing.
func (s *Service) GetRequiredString(ctx context.Context, key string) (string, error) {
	if v, ok := s.envOverride(key); ok {
		return v, nil
	}
	v, err := s.GetString(ctx, key, "")
	if err != nil || v == "" {
		return "", fmt.Errorf("missing required config: %s", key)
	}
	return v, nil
}

// GetFloat returns a float64 config value.
func (s *Service) GetFloat(ctx context.Context, key string, defaultValue float64) (float64, error) {
	if v, ok := s.envOverride(key); ok {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return defaultValue, nil
		}
		return parsed, nil
	}
	if v, ok := s.getFromCache(key); ok {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return defaultValue, nil
		}
		return parsed, nil
	}
	const q = `SELECT value FROM system_config WHERE key = $1 LIMIT 1`
	var v string
	err := s.db.QueryRowContext(ctx, q, key).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return defaultValue, nil
		}
		return 0, err
	}
	s.putInCache(key, v)
	parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return defaultValue, nil
	}
	return parsed, nil
}
