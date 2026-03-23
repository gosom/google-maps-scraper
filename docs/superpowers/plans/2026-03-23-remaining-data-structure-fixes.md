# Remaining Data Structure Fixes — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 8 remaining data structure issues from the verified review (post-CRITICAL/HIGH cleanup).

**Architecture:** Targeted fixes to existing packages. No new packages. Each task is independent — can be done in any order. All are MEDIUM or LOW severity.

**Tech Stack:** Go 1.25, pgx/v5, slog, sync primitives, standard library only (no new deps).

---

## File Structure

| Task | Files to Modify | New Files |
|------|----------------|-----------|
| 1. Leaked mates cap | `runner/webrunner/webrunner.go` | None |
| 2. Rate limiter cap | `web/middleware/middleware.go` | None |
| 3. Remove redundant dataJSON | `postgres/resultwriter.go` | None |
| 4. Config struct decomposition | `runner/runner.go`, `runner/jobs.go`, `main.go`, all runner `New()` functions | None |
| 5. Telemetry machine ID | `tlmt/tlmt.go` | None |
| 6. Encryptor nonce cache | `pkg/encryption/encryption.go` | None |
| 7. Config cache double-check | `config/config.go` | None |
| 8. Port allocation channel | `proxy/pool.go` | None |

---

## Chunk 1: Quick Wins (Tasks 1-3, 6-7)

### Task 1: Cap Leaked Mates Slice

**Files:**
- Modify: `runner/webrunner/webrunner.go` (struct definition + `trackLeakedMate`)

- [ ] **Step 1: Read current code**

Find `leakedMates []<-chan mateResult` field and `trackLeakedMate` method.

- [ ] **Step 2: Add max cap constant and modify trackLeakedMate**

```go
const maxLeakedMates = 100

func (w *webrunner) trackLeakedMate(ch <-chan mateResult, jobID string) {
    w.leakedMateCount.Add(1)
    w.leakedMu.Lock()
    defer w.leakedMu.Unlock()
    if len(w.leakedMates) >= maxLeakedMates {
        w.logger.Warn("leaked_mates_cap_reached", slog.Int("cap", maxLeakedMates), slog.String("job_id", jobID))
        return // drop oldest would require more complex data structure; just cap
    }
    w.leakedMates = append(w.leakedMates, ch)
    w.logger.Warn("mate_goroutine_leaked",
        slog.String("job_id", jobID),
        slog.Int64("lifetime_leaked", w.leakedMateCount.Load()),
    )
}
```

- [ ] **Step 3: Build and verify**

Run: `go build ./runner/...`
Expected: PASS

- [ ] **Step 4: Commit**

```
git add runner/webrunner/webrunner.go
git commit -m "fix: cap leaked mates slice at 100 to prevent unbounded growth"
```

---

### Task 2: Add Eviction Cap to Rate Limiter

**Files:**
- Modify: `web/middleware/middleware.go` (`keyRateLimiter` struct + `cleanup` method)

- [ ] **Step 1: Read current cleanup logic**

Find `cleanup()` method in `keyRateLimiter`.

- [ ] **Step 2: Add max entries constant and eviction cap**

After the existing TTL-based cleanup loop, add:

```go
const maxRateLimiterEntries = 50000

func (krl *keyRateLimiter) cleanup() {
    ticker := time.NewTicker(krl.ttl)
    defer ticker.Stop()
    for range ticker.C {
        krl.mu.Lock()
        // Existing: delete expired
        cutoff := time.Now().Add(-krl.ttl)
        for key, e := range krl.limiters {
            if e.lastSeen.Before(cutoff) {
                delete(krl.limiters, key)
            }
        }
        // New: evict if over cap (delete oldest 20%)
        if len(krl.limiters) > maxRateLimiterEntries {
            toDelete := len(krl.limiters) / 5
            deleted := 0
            for key := range krl.limiters {
                if deleted >= toDelete {
                    break
                }
                delete(krl.limiters, key)
                deleted++
            }
            slog.Warn("rate_limiter_eviction",
                slog.Int("evicted", deleted),
                slog.Int("remaining", len(krl.limiters)),
            )
        }
        krl.mu.Unlock()
    }
}
```

Note: Go map iteration is random, so "delete first N" effectively deletes random entries, which is fair enough for rate limiters.

- [ ] **Step 3: Build and verify**

Run: `go build ./web/...`
Expected: PASS

- [ ] **Step 4: Commit**

```
git add web/middleware/middleware.go
git commit -m "fix: add 50K entry cap to rate limiter map with eviction"
```

---

### Task 3: Remove Redundant `dataJSON` Marshal

**Files:**
- Modify: `postgres/resultwriter.go` (2 functions: `batchSaveEnhanced`, `batchSaveEnhancedWithCount`)

- [ ] **Step 1: Read current code**

Find `dataJSON := mustMarshalJSON(entry)` in both functions. Verify the `data` column is still used in the INSERT.

- [ ] **Step 2: Check if `data` column is read anywhere**

Run: `grep -r "\.Data\b" --include="*.go" postgres/ web/ | grep -v _test.go | grep -v "JobData\|DataFolder"`

If the `data` column is read by any query, we can't remove it. If it's write-only (backup column), we can remove the INSERT field and let it be NULL.

- [ ] **Step 3: If data column is write-only, remove it from INSERT**

Remove `dataJSON := mustMarshalJSON(entry)` and remove the `data` placeholder from the VALUES clause and column list. Update the parameter numbering.

If data column IS read: instead of removing, reuse the individual marshaled fields to construct it:
```go
// Build data JSON from already-marshaled fields instead of re-marshaling entire entry
dataJSON := mustMarshalJSON(entry) // Keep but add TODO to remove when data column is deprecated
```

- [ ] **Step 4: Build and run tests**

Run: `go build ./postgres/... && go test ./postgres/... -v -count=1 -timeout 60s`
Expected: PASS

- [ ] **Step 5: Commit**

```
git add postgres/resultwriter.go
git commit -m "perf: remove redundant full-entry dataJSON marshal in result writers"
```

---

### Task 6: Cache Encryptor Nonce Size

**Files:**
- Modify: `pkg/encryption/encryption.go`

- [ ] **Step 1: Read current Encryptor struct**

- [ ] **Step 2: Add nonceSize field and cache it in New()**

```go
type Encryptor struct {
    gcm       cipher.AEAD
    nonceSize int
}

func New(key string) (*Encryptor, error) {
    // ... existing code ...
    return &Encryptor{gcm: gcm, nonceSize: gcm.NonceSize()}, nil
}
```

- [ ] **Step 3: Replace `e.gcm.NonceSize()` calls with `e.nonceSize`**

In `Encrypt()` and `Decrypt()`.

- [ ] **Step 4: Build**

Run: `go build ./pkg/encryption/...`
Expected: PASS

- [ ] **Step 5: Commit**

```
git add pkg/encryption/encryption.go
git commit -m "perf: cache GCM nonce size in Encryptor struct"
```

---

### Task 7: Fix Config Cache Double-Check

**Files:**
- Modify: `config/config.go` (`getFromCache` method)

- [ ] **Step 1: Read current getFromCache**

- [ ] **Step 2: Add double-check after acquiring write lock**

```go
func (s *Service) getFromCache(key string) (string, bool) {
    s.mu.RLock()
    entry, ok := s.cache[key]
    s.mu.RUnlock()
    if !ok {
        return "", false
    }
    if time.Now().After(entry.expiresAt) {
        s.mu.Lock()
        // Double-check: another goroutine may have refreshed
        entry, ok = s.cache[key]
        if ok && time.Now().After(entry.expiresAt) {
            delete(s.cache, key)
            ok = false
        }
        s.mu.Unlock()
        if !ok {
            return "", false
        }
    }
    return entry.value, true
}
```

- [ ] **Step 3: Build**

Run: `go build ./config/...`
Expected: PASS

- [ ] **Step 4: Commit**

```
git add config/config.go
git commit -m "fix: add double-check in config cache to prevent redundant DB queries"
```

---

## Chunk 2: Structural Refactors (Tasks 4-5, 8)

### Task 4: Config Struct Decomposition (Phase 2)

**Files:**
- Modify: `runner/runner.go` (Config struct + ParseConfig)
- Modify: `runner/jobs.go` (CreateSeedJobs signature)
- Modify: `main.go` (runnerFactory)
- Modify: `runner/filerunner/filerunner.go` (New + callers)
- Modify: `runner/databaserunner/databaserunner.go` (New + callers)

**This is a larger refactor. Break into sub-steps:**

- [ ] **Step 1: Create ScrapeParams struct in runner/runner.go**

```go
type ScrapeParams struct {
    Concurrency              int
    MaxDepth                 int
    LangCode                 string
    Debug                    bool
    FastMode                 bool
    Email                    bool
    Images                   bool
    GeoCoordinates           string
    Zoom                     int
    Radius                   float64
    ExtraReviews             bool
    MaxResults               int
    DisablePageReuse         bool
    ExitOnInactivityDuration time.Duration
}
```

- [ ] **Step 2: Add helper to extract ScrapeParams from Config**

```go
func (c *Config) ScrapeParams() ScrapeParams {
    return ScrapeParams{
        Concurrency: c.Concurrency,
        MaxDepth:    c.MaxDepth,
        // ... all fields
    }
}
```

- [ ] **Step 3: Change CreateSeedJobs to accept ScrapeParams**

```go
func CreateSeedJobs(params ScrapeParams, r io.Reader, dedup deduper.Deduper, exitMonitor exiter.Exiter) ([]scrapemate.IJob, error)
```

- [ ] **Step 4: Update all callers of CreateSeedJobs**

In `filerunner.go`, `databaserunner.go`, and any other callers — pass `cfg.ScrapeParams()` instead of 15 positional args.

- [ ] **Step 5: Build and test**

Run: `go build ./... && go vet ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```
git add runner/runner.go runner/jobs.go runner/filerunner/ runner/databaserunner/ main.go
git commit -m "refactor: extract ScrapeParams struct, collapse CreateSeedJobs to 4 params"
```

---

### Task 5: Telemetry File-Persisted Machine ID

**Files:**
- Modify: `tlmt/tlmt.go` (`generateMachineID` function)

- [ ] **Step 1: Read current generateMachineID**

- [ ] **Step 2: Replace with file-persisted UUID**

```go
func generateStableMachineID() string {
    // Try reading existing ID
    home, err := os.UserHomeDir()
    if err != nil {
        return uuid.New().String()
    }
    idDir := filepath.Join(home, ".config", "brezel")
    idFile := filepath.Join(idDir, ".machine-id")

    if data, err := os.ReadFile(idFile); err == nil {
        id := strings.TrimSpace(string(data))
        if id != "" {
            return id
        }
    }

    // Generate and persist
    id := uuid.New().String()
    os.MkdirAll(idDir, 0o700)
    os.WriteFile(idFile, []byte(id), 0o600)
    return id
}
```

- [ ] **Step 3: Remove fetchExternalIP calls from machine ID generation**

Keep `fetchExternalIP` if used elsewhere (check with grep). If only used for machine ID, remove it.

- [ ] **Step 4: Update generateMachineID to use generateStableMachineID**

Replace the SHA256(IP+arch) logic with just calling `generateStableMachineID()`. Keep arch/os in the metadata map for analytics but not in the ID itself.

- [ ] **Step 5: Build**

Run: `go build ./tlmt/...`
Expected: PASS

- [ ] **Step 6: Commit**

```
git add tlmt/tlmt.go
git commit -m "fix: use file-persisted UUID for telemetry machine ID instead of IP-based hash"
```

---

### Task 8: Port Allocation Channel Pool (Optional/LOW)

**Files:**
- Modify: `proxy/pool.go`

- [ ] **Step 1: Read current tryStartOnAvailablePort**

- [ ] **Step 2: Replace linear scan with buffered channel**

Add to Pool struct:
```go
availablePorts chan int
```

In constructor, pre-fill:
```go
p.availablePorts = make(chan int, portEnd-portStart+1)
for port := portStart; port <= portEnd; port++ {
    p.availablePorts <- port
}
```

In `tryStartOnAvailablePort`, replace the scan loop with:
```go
select {
case port := <-p.availablePorts:
    if err := server.Start(port); err != nil {
        p.availablePorts <- port // return on failure
        continue
    }
    return port, nil
default:
    return 0, fmt.Errorf("no available ports")
}
```

In `ReturnServer`, after `server.Stop()`:
```go
p.availablePorts <- server.Port // return port to pool
```

- [ ] **Step 3: Remove portMu (no longer needed)**

The channel IS the synchronization mechanism.

- [ ] **Step 4: Build and verify**

Run: `go build ./proxy/...`
Expected: PASS

- [ ] **Step 5: Commit**

```
git add proxy/pool.go
git commit -m "perf: replace linear port scan with buffered channel pool — O(1) allocation"
```

---

## Priority Order

| Task | Severity | Effort | Do First? |
|------|----------|--------|-----------|
| 1. Cap leaked mates | MEDIUM | 5 min | Yes |
| 6. Cache nonce size | LOW | 5 min | Yes |
| 7. Config cache double-check | LOW | 10 min | Yes |
| 2. Rate limiter cap | MEDIUM | 15 min | Yes |
| 3. Remove dataJSON | MEDIUM | 30 min (needs investigation) | Yes |
| 5. Telemetry machine ID | LOW | 20 min | Optional |
| 4. Config decomposition | MEDIUM | 2-3 hours | When ready for Phase 2 |
| 8. Port allocation channel | LOW | 30 min | Optional |
