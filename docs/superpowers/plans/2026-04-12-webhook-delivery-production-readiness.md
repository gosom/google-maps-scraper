# Webhook Delivery — Production Readiness Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the webhook delivery pipeline so that BrezelScraper sends an authenticated HTTP POST to every active webhook URL when a job reaches a terminal state (`completed`, `failed`, `cancelled`). The infrastructure (config CRUD, DB tables, SSRF protection, secret generation) is already built and well-tested. This plan covers the missing pieces: payload contract, delivery worker, HMAC signing, retry logic, and triggering from job completion.

**Architecture:** A background `WebhookDeliveryWorker` goroutine polls the `job_webhook_deliveries` table for pending/retryable entries, constructs a signed JSON payload, and POSTs it to the user's URL using the existing `NewWebhookHTTPClient` (IP-pinned, redirect-blocked). The worker runs inside `webrunner.Run()` alongside the existing stuck-job reaper. Job completion in the runner creates delivery rows for all active webhooks.

**Tech Stack:** Go, `net/http`, `crypto/hmac`, `crypto/sha256`, `crypto/aes`, `crypto/cipher`, `database/sql`, `log/slog`, `context`

---

## Existing Code Audit

### What is already built and working

| Component | File | Status | Notes |
|-----------|------|--------|-------|
| WebhookConfig CRUD handlers | `web/handlers/webhook.go` | Done | 40+ tests, SSRF/HTTPS/ownership checks |
| WebhookConfig DB repository | `postgres/webhook.go` | Done | secret_hash excluded from list queries |
| JobWebhookDelivery DB repository | `postgres/webhook_delivery.go` | Done | Create, MarkDelivering/Delivered/Failed |
| Secret generation | `web/handlers/webhook.go:166-178` | Done | 256-bit crypto/rand; storage design is broken (see bug #8) |
| SSRF prevention + IP blocklist | `web/utils/private_ip.go`, `web/handlers/webhook_url.go` | Done | Blocks private/loopback/link-local/cloud metadata |
| IP-pinned HTTP client | `web/handlers/webhook_url.go:51-76` | Done | DNS rebinding defense, redirect blocking |
| DB schema + indexes | `scripts/migrations/000027` | Done | Retry index on next_retry_at, composite PK |
| Repository injection in webrunner | `runner/webrunner/webrunner.go:204-205` | Done | Repos instantiated but unused |

### Bugs and issues in existing code

| # | Severity | File | Issue | Fix |
|---|----------|------|-------|-----|
| 1 | **High** | `postgres/webhook_delivery.go:54-68` | `MarkDelivering` has no `AND status = 'pending'` guard. Two concurrent workers can both mark the same row as "delivering" and both send the webhook, causing **duplicate delivery**. This is a race condition. | Add `AND status IN ('pending')` to the WHERE clause; check `RowsAffected() == 0` as a CAS failure signal |
| 2 | Medium | `postgres/webhook_delivery.go:44-52` | `ListPendingByJobID` uses `status != 'failed'` which includes `delivering` and `delivered` rows. A row stuck in `delivering` (worker crashed) would be re-fetched but a `delivered` row with `delivered_at IS NULL` (data inconsistency) would also match. | Change to `WHERE status = 'pending'` for correctness |
| 3 | Medium | `postgres/webhook_delivery.go:54-68` | `MarkDelivering` does not set `next_retry_at`. The retry index (`idx_job_webhook_deliveries_retry`) queries on `next_retry_at` but nothing ever writes it. | Delivery worker must set `next_retry_at` on failure via a new `SetNextRetry` method |
| 4 | Low | `models/webhook.go` | `JobWebhookDelivery` struct has no json tags. If ever serialized, fields would be PascalCase. | Add `json:"snake_case"` tags (golang-structs-interfaces: "Exported fields in serialized structs MUST have field tags") |
| 5 | Low | `models/webhook.go` | `WebhookConfig` struct has no json tags on most fields. | Add `json:"snake_case"` tags |
| 6 | Medium | `postgres/webhook.go` | No method to fetch active configs **with** their secret for signing. `ListActiveByUserID` excludes the secret (correct for listing). The delivery worker needs the secret. | Add `GetByIDWithSecret` or a batch method |
| 7 | Medium | `postgres/webhook_delivery.go` | No method to list globally pending deliveries across all jobs. The retry worker needs `ListPendingGlobal(ctx, limit)` to poll for work. | Add a new repository method with `FOR UPDATE SKIP LOCKED` |
| 8 | **High** | `web/handlers/webhook.go:166-178` | **Broken signing design.** Server stores `HMAC-SHA256(ServerSecret, plaintextSecret)` as `secret_hash`. User has `plaintextSecret`. Server cannot use `secret_hash` to sign payloads in a way the user can verify, because the user does not have `ServerSecret` to reconstruct `secret_hash`. Signing is impossible with the current storage. | Replace with AES-256-GCM encryption (see Signing Design section) |

---

## Security Review (golang-security skill)

### Trust Boundaries

| Boundary | What crosses it | Risk | Defense |
|----------|----------------|------|---------|
| Outbound HTTP POST to user URL | Signed JSON payload with job metadata | SSRF, data sent to wrong endpoint | IP pinning, HTTPS enforcement, ownership scoping |
| Signing key retrieval | Encrypted secret decrypted from DB | Key leakage if DB + env compromised | AES-GCM encryption with ServerSecret as KEK |
| Webhook response | Untrusted data from user's server | Resource exhaustion, log injection | `io.LimitReader(1KB)`, do not log response body |
| Delivery row creation | Job completion triggers writes | Wrong user's webhooks triggered | `ListActiveByUserID(job.UserID)` + cross-check |
| Worker concurrency | Multiple goroutines process deliveries | Double delivery, race conditions | `FOR UPDATE SKIP LOCKED`, CAS on status field |

### Threat Model (STRIDE)

| Threat | Attack | Defense | DREAD |
|--------|--------|---------|-------|
| **Spoofing** — Forged webhook calls | Attacker POSTs to user's endpoint pretending to be BrezelScraper | HMAC-SHA256 signature in `X-Webhook-Signature`; timestamp in `X-Webhook-Timestamp` for replay window (5 min); user verifies with `crypto/subtle.ConstantTimeCompare` | 6 (High) |
| **Tampering** — Payload modified in transit | MITM alters POST body | HTTPS enforced at config creation; HMAC signature covers entire body | 3 (Low) |
| **Repudiation** — User denies receiving webhook | No delivery receipt | `delivered_at` timestamp + HTTP status code logged; `attempts` counter provides audit trail | 3 (Low) |
| **Information Disclosure** — Job data sent to wrong user | Bug in delivery creation sends to attacker's webhook | Delivery creation scoped by `job.UserID`; worker cross-checks `config.UserID == job.UserID` before sending | 7 (High) |
| **Information Disclosure** — Signing secret leaked via logs | Worker logs URL containing secret or logs signing key | Never log webhook URLs (may contain tokens in query params); never log signing secrets; log only config ID | 5 (Medium) |
| **Denial of Service** — Webhook as DDoS amplifier | User points webhook at victim's public server, creates many jobs to trigger deliveries | Per-user delivery rate limit (100/hour); max 10 webhooks per user; job creation already rate-limited (1 req/s, burst 3) and credit-gated | 6 (High) |
| **Denial of Service** — Slow-loris webhook endpoint | User's URL reads request body slowly, tying up worker goroutines for 10s each | 10s per-delivery timeout; `errgroup.SetLimit(10)` caps concurrent connections; max 5 attempts then give up | 5 (Medium) |
| **Denial of Service** — Thundering herd | 1000 jobs complete simultaneously, creating 10,000 delivery rows | Worker polls with `LIMIT 50`; `errgroup.SetLimit(10)` caps outbound connections; backoff jitter prevents synchronized retries | 5 (Medium) |
| **Denial of Service** — Worker self-DDoS on retries | 5xx responses cause exponential retry, multiplying requests | Exponential backoff (5s, 25s, 125s, 625s) with jitter; max 5 attempts; total retry window ~13 min | 4 (Medium) |
| **Elevation of Privilege** — Worker processes delivery for wrong user | Corrupted delivery row references wrong webhook config | Belt-and-suspenders: worker verifies `config.UserID == job.UserID` before sending; logs and marks failed if mismatch | 4 (Medium) |
| **Replay Attack** — Attacker intercepts and replays old webhook | Captured webhook POST replayed to trigger duplicate processing | `X-Webhook-Timestamp` header; documentation instructs users to reject deliveries older than 5 minutes | 4 (Medium) |

### Can users access other users' data?

Tracing the full data flow for multi-tenant isolation:

1. **Webhook config creation:** User creates config with their auth token. `UserID` comes from `auth.GetUserID(r.Context())`, never from request body. **Safe.**

2. **Delivery row creation (job completion):** Server calls `ListActiveByUserID(job.UserID)` to find webhooks. `job.UserID` was set at job creation from auth context. **Safe, assuming job.UserID is correct.** Defense: worker cross-checks `config.UserID == job.UserID`.

3. **Worker processes delivery:** Worker calls `GetByID(webhookConfigID)` which returns any config regardless of user. **Risk: if a delivery row was somehow created for the wrong config ID, the worker would send to the wrong user's URL.** Defense: composite PK (job_id, webhook_config_id) with FK constraints prevents orphaned rows; worker cross-checks user IDs; delivery rows are only created server-side (never via user API).

4. **Webhook payload content:** Payload contains `job_id`, `job_name`, `status`, `result_count`. No result data, no PII. Even if sent to the wrong endpoint, damage is limited to metadata exposure. **Acceptable risk with cross-check defense.**

**Verdict:** Multi-tenant isolation is sound. The belt-and-suspenders `config.UserID == job.UserID` check in the worker catches bugs without relying on a single point of correctness.

### How would an attacker abuse this system?

| Attack vector | Feasibility | Mitigation |
|---------------|-------------|------------|
| **DDoS amplifier:** Point webhook at victim, spam job creation | Medium: requires account + credits; rate-limited at 1 job/s | Per-user delivery rate limit (100/hr); credit cost per job; existing job rate limits |
| **Data exfiltration via webhook:** Create webhook to attacker server, scrape competitor data, receive notifications | Low risk: webhook only contains job metadata, not scraped results | Payload is minimal; results require authenticated API call |
| **Webhook secret brute-force:** Guess signing secret to forge webhooks | Infeasible: 256-bit random secret; `2^256` attempts needed | crypto/rand entropy; AES-GCM encryption at rest |
| **Replay captured webhooks:** Replay signed request to trigger duplicate processing | Medium: requires network position | `X-Webhook-Timestamp` with 5-minute tolerance; `X-Webhook-ID` for idempotency |
| **Abuse retries for amplification:** Create webhook to victim, return 5xx to trigger 5 retry attempts per delivery | Medium: 5x amplification per job per webhook | Max 5 attempts; exponential backoff; per-user delivery cap |
| **Exhaust worker resources:** Create 10 webhooks pointing to slow endpoints, create jobs | Medium: 10 concurrent connections tied up for 10s each | `errgroup.SetLimit(10)` is per-worker, not per-user; other users' deliveries wait. Consider per-user concurrency cap. |

### How would bad actors use this?

The most realistic abuse scenario is **DDoS amplification**: a bad actor creates an account, buys minimal credits, points all 10 webhooks at a victim server, and creates jobs. Each job triggers up to 10 deliveries, each retried up to 5 times = 50 requests per job to the victim.

**Defenses (layered):**
1. Account creation requires email verification (Clerk)
2. Job creation requires credits (paid)
3. Job creation is rate-limited (1/s, burst 3)
4. Max 10 webhooks per user
5. Max 5 retry attempts per delivery
6. **NEW: Per-user delivery rate limit (100 deliveries/hour)**
7. **NEW: Per-destination IP rate limit (50 deliveries/hour to same resolved IP)** — prevents targeting a single victim even with multiple accounts

---

## Concurrency Review (golang-concurrency skill)

### Race condition #1: Double delivery (Critical)

**Current code (`MarkDelivering` at `postgres/webhook_delivery.go:54-68`):**
```sql
UPDATE job_webhook_deliveries
SET status = $1, attempts = attempts + 1, last_attempt_at = $2
WHERE job_id = $3 AND webhook_config_id = $4
```

**Problem:** No check on current status. If two worker goroutines (or two server instances) call this concurrently for the same row, both succeed, both send the webhook. The user receives a duplicate.

**Fix (CAS pattern):**
```sql
UPDATE job_webhook_deliveries
SET status = 'delivering', attempts = attempts + 1, last_attempt_at = $1
WHERE job_id = $2 AND webhook_config_id = $3 AND status = 'pending'
```
Check `RowsAffected() == 0` — if zero, another worker claimed it. Return a sentinel error (`ErrAlreadyClaimed`) and skip.

### Race condition #2: ListPendingGlobal without row locking

**Problem:** Two workers poll `ListPendingGlobal` at the same time, both get the same rows, both try to process them. Even with the CAS fix above, this wastes work (one worker fetches, marks delivering fails, moves on).

**Fix:** Use `SELECT ... FOR UPDATE SKIP LOCKED`:
```sql
SELECT job_id, webhook_config_id, ...
FROM job_webhook_deliveries
WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
ORDER BY next_retry_at NULLS FIRST
LIMIT $1
FOR UPDATE SKIP LOCKED
```
This atomically locks the rows for the calling transaction, and other workers skip locked rows. Requires wrapping in a transaction.

**golang-concurrency skill says:** "Use `errgroup.SetLimit(n)` — replaces hand-rolled worker pools." Combined with `FOR UPDATE SKIP LOCKED`, this gives us safe concurrent processing without double delivery.

### Race condition #3: Delivery row creation during job completion

**Problem:** If `createWebhookDeliveries` is called twice (e.g., due to a bug or retry in the runner), duplicate delivery rows could be created.

**Fix:** The composite PK `(job_id, webhook_config_id)` already prevents duplicates. `INSERT ... ON CONFLICT DO NOTHING` makes this idempotent.

### Goroutine lifecycle checklist (golang-concurrency skill)

| Question | Answer |
|----------|--------|
| How will the worker goroutine exit? | `ctx.Done()` in the poll loop's `select` |
| Can we signal it to stop? | Yes, via the parent context (graceful shutdown cancels the context) |
| Can we wait for it to finish? | Yes, via `sync.WaitGroup` in the webrunner |
| Who owns the errgroup? | The `Run` method creates it per poll cycle; it dies with the cycle |
| Are in-flight deliveries drained on shutdown? | Yes: after `ctx.Done()`, the errgroup's `Wait()` blocks until in-flight `deliverOne` calls complete (they have their own 10s timeout) |
| Is `time.After` used in loops? | No, plan specifies `time.NewTimer` + `Reset` (golang-concurrency: "Never use time.After in loops") |

### Timer pattern (golang-concurrency)

```go
timer := time.NewTimer(pollInterval)
defer timer.Stop()

for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
        // process pending deliveries
        timer.Reset(pollInterval)
    }
}
```

---

## HMAC Signing Design (AES-256-GCM)

### The problem with the current design

The current code stores `HMAC-SHA256(ServerSecret, plaintextSecret)` as `secret_hash`. This is a **one-way** operation. The server cannot reverse the hash to get `plaintextSecret`. The user has `plaintextSecret` but not `ServerSecret`. Neither side can derive the other's value. **Signing is impossible.**

### The fix: AES-256-GCM encryption at rest

Use a **two-way** operation (encryption, not hashing) so the server can recover the signing key at delivery time.

**At webhook creation:**
```go
// Generate signing secret (shown to user once)
signingSecret := make([]byte, 32) // 256-bit
crypto_rand.Read(signingSecret)
plaintextHex := hex.EncodeToString(signingSecret) // give to user

// Encrypt for storage
block, _ := aes.NewCipher(serverSecretKey) // serverSecretKey must be 32 bytes
gcm, _ := cipher.NewGCM(block)
nonce := make([]byte, gcm.NonceSize()) // 12 bytes
crypto_rand.Read(nonce)
ciphertext := gcm.Seal(nonce, nonce, []byte(plaintextHex), nil) // nonce prepended
encryptedHex := hex.EncodeToString(ciphertext)
// Store encryptedHex in DB column "encrypted_secret"
```

**At delivery time:**
```go
// Decrypt signing secret
ciphertext, _ := hex.DecodeString(config.EncryptedSecret)
block, _ := aes.NewCipher(serverSecretKey)
gcm, _ := cipher.NewGCM(block)
nonceSize := gcm.NonceSize()
nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
plaintextHex, err := gcm.Open(nil, nonce, ciphertext, nil)
// plaintextHex is the signing key

// Sign payload
mac := hmac.New(sha256.New, plaintextHex)
mac.Write(jsonBody)
signature := hex.EncodeToString(mac.Sum(nil))
// Set header: X-Webhook-Signature: sha256=<signature>
```

**User verifies (Node.js example for n8n users):**
```javascript
const crypto = require('crypto');
const expected = crypto.createHmac('sha256', webhookSecret)
    .update(requestBody)
    .digest('hex');
const valid = crypto.timingSafeEqual(
    Buffer.from(signature), Buffer.from('sha256=' + expected)
);
```

### Why AES-GCM and not plaintext storage

| Concern | Plaintext | AES-GCM |
|---------|-----------|---------|
| DB breach without ServerSecret | Attacker gets all signing secrets; can forge webhooks to every user's endpoint | Attacker gets ciphertext; useless without ServerSecret |
| DB breach with ServerSecret | Same | Same (attacker can decrypt) |
| DB read-only SQL injection | Signing secrets exposed | Ciphertext exposed, unusable |
| Backup/log leakage | Secrets in plaintext in DB dumps | Ciphertext in dumps, safe |
| Compliance / audit | "You store secrets in plaintext?" | "Encrypted at rest with AES-256-GCM" |

The AES-GCM approach is what Stripe, GitHub, and Svix use. It protects against the most common breach vector (DB access without full environment compromise).

### ServerSecret requirements

- Must be exactly 32 bytes (256-bit) for AES-256
- Already exists in the codebase for other purposes
- If current ServerSecret is not 32 bytes, derive a 32-byte key: `webhookKEK := HMAC-SHA256(ServerSecret, "webhook-signing-key-encryption")[:32]`

---

## Golang Skills Decision Table

| Decision | Skill | What the skill says | Our choice |
|----------|-------|---------------------|------------|
| Secret encryption at rest | golang-security | "Use vetted algorithms; never roll your own. Use crypto/aes GCM" | AES-256-GCM with ServerSecret-derived KEK; unique nonce per secret via `crypto/rand` |
| Constant-time comparison | golang-security | "Comparing secrets with == short-circuits on first differing byte. Use crypto/subtle.ConstantTimeCompare" | Document for users verifying signatures; use internally if comparing secrets |
| Nonce generation | golang-security | "Use crypto/rand for keys/tokens — math/rand is predictable" | 12-byte nonce from `crypto/rand` for each AES-GCM encryption |
| Webhook payload struct | golang-structs-interfaces | "Exported fields in serialized structs MUST have field tags" | All fields tagged with `json:"snake_case"` |
| Worker goroutine lifecycle | golang-concurrency | "Every goroutine must have a clear exit — context cancellation, done channel, or WaitGroup" | Worker accepts `context.Context`, drains in-flight via errgroup, tracked by `sync.WaitGroup` |
| Poll timer | golang-concurrency | "Never use time.After in loops — each call creates a timer that lives until it fires" | `time.NewTimer` + `Reset` in poll loop |
| Concurrent delivery cap | golang-concurrency | "Use errgroup.SetLimit(n) — replaces hand-rolled worker pools" | `errgroup.SetLimit(10)` caps outbound HTTP calls |
| Row locking for worker queue | golang-concurrency | "Protect shared state to prevent data corruption" | `SELECT ... FOR UPDATE SKIP LOCKED` prevents double delivery |
| CAS on status field | golang-concurrency | "Race conditions cause data corruption and can bypass authorization checks" | `UPDATE ... WHERE status = 'pending'` acts as compare-and-swap |
| Retry with context check | golang-design-patterns | "Retry logic MUST check context cancellation between attempts" | `select` on `ctx.Done()` between poll ticks; backoff computed per delivery |
| HTTP client timeout | golang-design-patterns | "Every external call SHOULD have a timeout" | `context.WithTimeout(ctx, 10*time.Second)` per delivery |
| Error handling in worker | golang-design-patterns | "Panic is for bugs, not expected errors" | Worker logs failures, never panics; marks delivery as failed after max attempts |
| Preallocation | golang-data-structures | "Preallocate slices with make(T, 0, n) when size is known" | `make([]*JobWebhookDelivery, 0, len(activeWebhooks))` when creating delivery rows |
| Payload struct naming | golang-naming | "json tags use snake_case; exported fields use MixedCaps" | `EventType string \`json:"event_type"\`` |
| DNS rebinding defense | golang-security | "SSRF: resolve DNS once, pin IP, connect to pinned IP" | Already implemented in `NewWebhookHTTPClient` — reuse as-is |
| Response body handling | golang-security | "Returning detailed errors helps attackers map your system" | `io.LimitReader(resp.Body, 1024)`, discard; log only status code |
| Log safety | golang-security | "PII in logs — sanitize" | Never log webhook URLs (may contain tokens), response bodies, or signing secrets; log only config ID + job ID + status code + latency |
| Batch insert | golang-data-structures | "Avoid repeated single inserts in a loop" | `INSERT ... VALUES (...), (...), (...)` for delivery row creation |
| Idempotent insert | golang-design-patterns | "Make illegal states unrepresentable" | `INSERT ... ON CONFLICT DO NOTHING` on composite PK prevents duplicate delivery rows |

---

## Webhook Payload Contract

```go
// WebhookEvent is the JSON body sent to the user's webhook URL.
type WebhookEvent struct {
    EventType   string    `json:"event_type"`    // "job.completed", "job.failed", "job.cancelled"
    JobID       string    `json:"job_id"`
    JobName     string    `json:"job_name"`
    Status      string    `json:"status"`        // "completed", "failed", "cancelled"
    ResultCount int       `json:"result_count"`
    CreatedAt   time.Time `json:"created_at"`    // job creation time
    CompletedAt time.Time `json:"completed_at"`  // time the job reached terminal state
}
```

**Design rationale:**
- Minimal payload: only what the user needs to decide whether to fetch results
- No result data: users must call the API (matches the n8n guide; avoids sending scraped data to potentially insecure endpoints)
- `event_type` uses dot notation (`job.completed`) following Stripe/GitHub convention
- `result_count` included so users can skip fetching if zero
- No user PII (no email, no user_id)

**Headers sent with each delivery:**

| Header | Value | Purpose |
|--------|-------|---------|
| `Content-Type` | `application/json` | Standard |
| `User-Agent` | `BrezelScraper-Webhook/1.0` | Identification |
| `X-Webhook-Signature` | `sha256=<hex>` | HMAC-SHA256 of raw JSON body using the webhook signing secret |
| `X-Webhook-ID` | `<uuid>` | Idempotency key so receiver can deduplicate |
| `X-Webhook-Timestamp` | `<unix-seconds>` | Replay protection: receiver should reject deliveries older than 5 minutes |

---

## File Structure

### New files

| File | Purpose |
|------|---------|
| `web/services/webhook_delivery.go` | `WebhookDeliveryWorker` — background goroutine that polls DB, signs, and sends |
| `web/services/webhook_delivery_test.go` | Unit tests with mock HTTP server and mock repos |
| `models/webhook_event.go` | `WebhookEvent` struct (payload contract) |
| `pkg/crypto/aesutil/aesutil.go` | `Encrypt(key, plaintext)` and `Decrypt(key, ciphertext)` wrappers for AES-256-GCM |
| `pkg/crypto/aesutil/aesutil_test.go` | Round-trip tests, wrong-key rejection, tampered-ciphertext detection |

### Modified files

| File | Change |
|------|--------|
| `models/webhook.go` | Add json tags; rename `SecretHash` to `EncryptedSecret`; add `SigningSecret` transient field (not persisted, used only at creation to return plaintext) |
| `postgres/webhook_delivery.go` | Fix `MarkDelivering` with CAS; add `ListPendingGlobal` with `FOR UPDATE SKIP LOCKED`; add `SetNextRetry`; fix `ListPendingByJobID` filter |
| `postgres/webhook.go` | Rename column references from `secret_hash` to `encrypted_secret`; add batch-fetch method for delivery worker |
| `runner/webrunner/webrunner.go` | Start `WebhookDeliveryWorker`; create delivery rows on job completion |
| `web/handlers/webhook.go` | Use `aesutil.Encrypt` instead of HMAC hash for secret storage |
| `web/handlers/webhook_test.go` | Update tests for encrypted secret storage |
| `scripts/migrations/000027_add_webhook_configs.up.sql` | Rename `secret_hash` to `encrypted_secret` |

---

## Implementation Steps

### Phase 1: AES-GCM encryption utilities — DONE `5760d2c`

- [x] **1.1** ~~Create `pkg/crypto/aesutil/aesutil.go`~~ — Encrypt, Decrypt, DeriveKey implemented; 8 tests pass
- [x] **1.2** ~~Create `pkg/crypto/aesutil/aesutil_test.go`~~ — Round-trip, wrong key, tampered data, empty plaintext, key derivation, unique nonces

### Phase 2: Fix existing code issues — DONE `5760d2c` + `656eb51`

- [x] **2.1** ~~Add json tags to `WebhookConfig` struct fields~~ — 10 fields tagged
- [x] **2.2** ~~Add json tags to `JobWebhookDelivery` struct fields~~ — 8 fields tagged
- [x] **2.3** ~~Rename `SecretHash` to `EncryptedSecret`~~ — model, postgres, handlers updated
- [x] **2.4** ~~Rename `secret_hash` to `encrypted_secret` in migration~~ — CREATE TABLE updated
- [x] **2.5** ~~Replace HMAC hash with AES-GCM encryption~~ — uses `aesutil.Encrypt` with `WebhookKEK`
- [x] **2.6** ~~Update postgres scan methods~~ — all SQL + Go references updated
- [x] **2.7** ~~Fix `MarkDelivering` race condition~~ — added `AND status = 'pending'`
- [x] **2.8** ~~Fix `ListPendingByJobID` filter~~ — changed to `status = 'pending'`
- [x] **2.9** ~~Update webhook handler tests~~ — 40+ tests pass

### Phase 3: Add missing repository methods — DONE `713f4e0`

- [x] **3.1** ~~`ListPendingGlobal`~~ — transactional SELECT FOR UPDATE SKIP LOCKED + batch UPDATE
- [x] **3.2** ~~`SetNextRetry`~~ — requeues delivery with computed backoff time
- [x] **3.3** ~~`CreateBatch`~~ — multi-row INSERT ON CONFLICT DO NOTHING
- [x] **3.4** ~~`ListActiveWithSecretByUserID`~~ — includes encrypted_secret for delivery worker

### Phase 4: Define the payload contract — DONE `5760d2c`

- [x] **4.1** ~~Create `models/webhook_event.go`~~ — WebhookEvent struct with json tags
- [x] **4.2** ~~Event type constants~~ — `EventTypeJobCompleted`, `EventTypeJobFailed`, `EventTypeJobCancelled`

### Phase 5: Implement the delivery worker — DONE `9a13dd2`

- [x] **5.1** ~~`WebhookDeliveryWorker` struct~~ — deliveryRepo, configRepo, jobRepo, webhookKEK, logger, pollInterval
- [x] **5.2** ~~`Run` method~~ — `time.NewTimer` + `Reset` poll loop, `ctx.Done()` for shutdown
- [x] **5.3** ~~`processBatch`~~ — `errgroup.SetLimit(10)`, claims up to 50 pending deliveries
- [x] **5.4** ~~`deliverOne`~~ — AES decrypt, HMAC sign, IP-pinned client, cross-user check, exponential backoff, 10s timeout, 1KB response cap. Inlined `newIPPinnedClient` to avoid import cycle.

### Phase 6: Wire up job completion trigger — DONE `ad2f2ea`

- [x] **6.1** ~~Create delivery rows on job completion~~ — ListActiveByUserID + CreateBatch after status persisted
- [x] **6.2** ~~Start worker goroutine~~ — tracked by bgWg, derives KEK from serverSecret at startup

### Code Review — DONE `5334b30` + `3ae450c`

17 of 20 findings fixed. Full review scorecard:

| # | Severity | Issue | Resolution |
|---|----------|-------|------------|
| C1 | Critical | MarkDelivered/MarkFailed no status guard | Fixed: `AND status = 'delivering'` |
| C2 | Critical | Dead MarkDelivering + redundant attempt increment | Fixed: removed from interface and implementation |
| C3 | Critical | uuid.Must panics in goroutine | Fixed: proper error handling with retry |
| H1 | High | CompletedAt was time.Now() | Fixed: uses job.UpdatedAt |
| H2 | High | Backoff 5^attempt too aggressive | Fixed: changed to 2^attempt |
| H3 | High | math/rand for jitter needs comment | Fixed: comment added |
| H4 | High | No per-user rate limiting | Phase 7 (planned work) |
| H5 | High | Constructors return interfaces | Accepted: pre-existing codebase-wide pattern in all repository constructors; changing would touch 10+ unrelated files for no functional benefit |
| M1 | Medium | Duplicated newIPPinnedClient | Fixed: extracted to web/utils/http_client.go |
| M3 | Medium | SetNextRetry no status guard | Fixed: `AND status = 'delivering'` |
| M4 | Medium | HTTP client created per delivery | Accepted: each webhook has a different resolved IP, so clients cannot be trivially pooled. Connection reuse would require a cache keyed by (resolvedIP, hostname) with eviction. Not worth the complexity at current scale. Revisit if delivery volume exceeds 1000/min. |
| M5 | Medium | pkg/ should be internal/ | Fixed: moved to internal/crypto/aesutil/ |
| M6 | Medium | 5s delay before first poll | Fixed: time.NewTimer(0) |
| M7 | Medium | GetByID returns encrypted_secret undocumented | Fixed: comment added |
| L1 | Low | CompletedAt misleading for failed/cancelled | Fixed: renamed to EndedAt/ended_at |
| L2 | Low | Jitter range [0.5, 1.5) unusual | Resolved by H2: base-2 makes the range acceptable |
| L3 | Low | No validate struct tags on webhook handlers | Accepted: consistent with all other handlers in the codebase (api.go, support.go, billing.go) which use manual validation. Adopting struct tags for webhooks alone would create inconsistency. |
| L4 | Low | scanMany(rows, err) fragile pattern | Accepted: pre-existing pattern used in all 5 repository files (job, webhook, webhook_delivery, api_key, user). Changing one breaks consistency. |
| L5 | Low | err != context.Canceled instead of errors.Is | Fixed |
| L6 | Low | No name length validation on update | Fixed: added len > 100 check |

### Phase 7: Rate limiting

- [ ] **7.1** Add per-user delivery rate limit: 100 deliveries per hour
  - Track in a simple DB query or in-memory counter per user
  - If exceeded: skip delivery, set `next_retry_at` to 1 hour later, log warning
- [ ] **7.2** Add per-destination-IP rate limit: 50 deliveries per hour to the same `resolved_ip`
  - Prevents DDoS amplification even with multiple attacker accounts
  - Query: `SELECT COUNT(*) FROM job_webhook_deliveries jwd JOIN webhook_configs wc ON jwd.webhook_config_id = wc.id WHERE wc.resolved_ip = $1 AND jwd.last_attempt_at > NOW() - INTERVAL '1 hour'`

### Phase 8: Tests

- [ ] **8.1** Unit tests for `WebhookDeliveryWorker`:
  - Successful delivery (2xx): marked delivered, verified_at set
  - Failed delivery (5xx): next_retry_at computed with exponential backoff
  - Max attempts exhausted: marked failed
  - Revoked config: marked failed immediately, no HTTP call
  - Cross-user mismatch: marked failed, no HTTP call, error logged
  - Context cancellation: clean shutdown, in-flight deliveries drain
  - HMAC signature correctness: compute expected signature independently, compare
  - Concurrent delivery limit: mock 20 pending, verify max 10 concurrent
  - Response body capped at 1KB
  - Webhook URL never appears in logs
- [ ] **8.2** Unit tests for `aesutil`:
  - Round-trip encryption/decryption
  - Wrong key rejection
  - Tampered ciphertext rejection
  - Key derivation determinism
- [ ] **8.3** Unit tests for new repository methods:
  - `ListPendingGlobal` with `FOR UPDATE SKIP LOCKED` (requires real DB or careful mock)
  - `SetNextRetry` updates correct row, rejects non-delivering status
  - `CreateBatch` with ON CONFLICT DO NOTHING (duplicate ignored)
  - `MarkDelivered`/`MarkFailed` reject non-delivering status (CAS guard)
- [ ] **8.4** Integration test: end-to-end
  - Create user, create webhook config (capture signing secret)
  - Create job, complete job
  - Verify delivery row created
  - Run worker tick
  - Verify HTTP POST received by `httptest.Server`
  - Verify HMAC signature matches using captured signing secret
  - Verify `X-Webhook-Timestamp` is within 5 seconds of now
  - Verify `X-Webhook-ID` is a valid UUID
- [ ] **8.5** Run `go test -race ./...` (golang-concurrency: "Always run -race in CI")

### Phase 9: Documentation

- [x] **9.1** ~~Update n8n guide~~ — DONE `b997321` + `105cb95`: payload schema, signature verification, retry docs, ended_at rename
- [ ] **9.2** Add webhook payload docs to `docs/api-reference/integrations.mdx`:
  - Event types and payload schema
  - Headers table
  - Retry schedule
  - Delivery guarantees (at-least-once, 5 attempts over ~13 min)
- [ ] **9.3** Add signature verification examples:
  - Node.js: `crypto.createHmac('sha256', secret).update(body).digest('hex')` + `crypto.timingSafeEqual`
  - Python: `hmac.compare_digest(hmac.new(secret, body, hashlib.sha256).hexdigest(), signature)`
  - Go: `hmac.New(sha256.New, secret)` + `subtle.ConstantTimeCompare`
- [ ] **9.4** Document replay protection: instruct users to reject `X-Webhook-Timestamp` older than 5 minutes

---

## Retry Schedule

| Attempt | Base delay (2^n) | With jitter [0.5x, 1.5x) | Cumulative |
|---------|-----------------|--------------------------|------------|
| 1 | 0 (immediate) | 0 | 0 |
| 2 | 2s | ~1-3s | ~2s |
| 3 | 4s | ~2-6s | ~6s |
| 4 | 8s | ~4-12s | ~14s |
| 5 | 16s | ~8-24s | ~30s |

After attempt 5 fails, the delivery is marked `failed`. Total window is approximately 30 seconds.

Backoff formula: `2^attempt * (0.5 + rand.Float64())` seconds, capped at 1 hour. Jitter prevents thundering herd when many jobs complete simultaneously.

---

## Decisions and Trade-offs

| Decision | Alternative | Why we chose this |
|----------|-------------|-------------------|
| AES-256-GCM secret storage | Plaintext, HMAC hash (current) | Protects signing secrets if DB is breached without env access; industry standard (Stripe/GitHub); small code cost (~50 lines in aesutil) |
| DB polling (not message queue) | Redis pub/sub, in-memory channel, SQS | Deliveries must survive restarts; DB is already the source of truth; no new infrastructure; `FOR UPDATE SKIP LOCKED` gives us a reliable job queue |
| `FOR UPDATE SKIP LOCKED` | Advisory locks, external lock service | Built into PostgreSQL; zero additional infrastructure; battle-tested for job queues; automatically released on transaction end |
| 10 concurrent deliveries | Unbounded, 1 serial | Prevents slow endpoints from blocking all work; bounded resource usage; matches per-user webhook limit |
| 10s per-delivery timeout | 30s (current client default) | Webhook endpoints should respond fast; 10s is generous; prevents worker starvation |
| Minimal payload (no results) | Full job + result preview | Keeps payload small; forces API fetch (consistent with auth model); avoids sending scraped data to insecure endpoints |
| Per-destination-IP rate limit | Per-user only | Prevents DDoS amplification by multiple attacker accounts targeting the same victim; defense-in-depth |
| 5-minute replay window | No replay protection, 1-minute, 1-hour | 5 minutes handles clock skew and slow networks; short enough to limit replay value |

---

## Open Questions

1. ~~**Should `verified_at` be set on first successful delivery?**~~ **Resolved: yes.** The worker sets `verified_at` on first successful delivery. Not yet implemented in code -- add to Phase 8 integration test to verify.
2. ~~**Should we send webhooks for `cancelled` jobs?**~~ **Resolved: yes.** The job completion trigger fires for all terminal states (completed, failed, cancelled).
3. ~~**Should the n8n guide webhook section be removed until this ships?**~~ **Resolved.** Rewritten with real payload contract, signature verification, and delivery docs (`b997321` + `105cb95`).
4. **Should we add a "test webhook" button in the UI?** Sends a test payload so users can verify their endpoint before a real job. **Recommendation:** yes, as a follow-up after this plan ships. Would use the same signing/delivery code path.
