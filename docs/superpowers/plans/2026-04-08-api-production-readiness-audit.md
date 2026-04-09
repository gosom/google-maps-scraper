# API Production-Readiness Audit & Remediation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every critical and high-severity gap in the BrezelScraper public API surface and ship a single, consistent convention for integer cap parameters before public launch.

**Architecture:** Audit was performed on `feat/admin-role-system`. The remediation work touches three layers — handlers (`web/handlers/`), validation (`web/utils/validation.go` + struct tags in `models/`), and services (`web/services/`, `billing/`). The cap-parameter convention is enforced once in the model struct tags + service-layer validator and surfaced uniformly through the OpenAPI spec.

**Tech Stack:** Go 1.22+, gorilla/mux, go-playground/validator/v10, pgx + database/sql, Stripe Go SDK, Clerk Go SDK v2, OpenAPI 3.1 (ReDoc).

---

## Revision Notes (2026-04-08, post-review)

The first draft of this plan went through senior review. The following corrections, downgrades, and additions are applied below — read them before executing any chunk.

**Update — C-2 RESOLVED OUT-OF-BAND.** The dev-auth bypass has been **completely removed from the codebase** rather than tightened. There is no longer any `BRAZA_DEV_AUTH_BYPASS` env var, no `DevUserHeaderName` constant, no `X-Braza-Dev-User` / `X-Braza-Dev-Role` header handling, no `main.go` startup guard, and no integration-test bypass path. Files touched in the deletion: `web/auth/auth.go`, `main.go`, `web/handlers/api_scrape_integration_test.go`, `.env.example`, `CLAUDE.md`, `docs/integration-testing.md`. `go build ./...` is clean. Integration tests now require one of `BRAZA_AUTH_TOKEN`, `BRAZA_SESSION_COOKIE`, or `BRAZA_CLERK_SECRET_KEY+BRAZA_CLERK_SESSION_ID`. Committed in `3264a86` on `feat/admin-role-system`. **C-2 is dropped from the P0 table and Task 1.2 is deleted from Chunk 1** — the entire vulnerability class no longer exists.

**Second-round review fixes (2026-04-08, applied to this revision):**

- **H-1 (`Job.Name` unbounded)** had a finding row but no implementation step. Task 2.2 now has an explicit **Step 5** that caps `apiScrapeRequest.Name` at `validate:"required,min=1,max=200"` with a dedicated test.
- **H-4 keyword length — byte vs rune clarification.** `validator/v10`'s `max=200` counts bytes via `len()`, not runes. A 200-byte cap is ~200 ASCII characters or ~50-66 CJK characters. Intentional (the purpose is DoS prevention on downstream `LIKE` queries, not a human-centric limit); documented in Task 2.2 and to be noted in the OpenAPI `keywords` field description.
- **Task 3.5 — DNS TOCTOU limitation.** `ValidateProxyURL` resolves at validation time; an attacker who controls a DNS record can alternate between a public IP (passes validation) and `169.254.169.254` (used at scrape time). The complete fix lives at the HTTP dialer inside `scrapemate`, which we don't own. Task now documents this explicitly, files an upstream-issue action item, and proposes a feature-flag workaround (disable `proxies` for free-tier users) as the practical mitigation until the dialer can be patched.
- **Task 7.1 / 7.2 — idempotency concurrency race.** The first-draft middleware (`repo.Get` → if nil, run handler → `repo.Insert`) was broken under concurrent retries: both requests would get nil from `Get` and both would run the handler. Rewritten to the Stripe two-phase pattern: `INSERT ... ON CONFLICT DO NOTHING` with a `status='started'` row **before** the handler runs, updated to `status='completed'` after. Concurrent retries hit the conflict path and get 409 `idempotency_key_in_use` (if still in flight) or the cached response (if completed). Schema updated in Task 7.1 with a nullable `status_code`/`response_body` and a `status` column; cleanup job reaps stuck `started` rows after 15 min. A new `TestIdempotency_ConcurrentRequests_HandlerRunsExactlyOnce` test asserts the handler runs exactly once across 20 concurrent requests — if the implementation doesn't reserve the key first, this test fails.
- **`responseCapture` type is now defined** in full in Task 7.2 (the first draft referenced it without a body). Captures status + body, delegates `Header()` and `Write()` through to the real writer so the client still receives the real response in real time.
- **Chunk 8 Task 8.2 — not "mechanical".** The first draft understated the scope. Many error sites live inside services and bubble up as bare `error`, with handlers string-matching the message to decide status codes — exactly what the Problem envelope is supposed to eliminate. Task 8.2 now introduces a typed `ProblemError` implementing `error` that services return and handlers unwrap via `errors.As`. Migration is split into seven steps: add the type, extend `Problem` with a `fields` array for multi-field validation, audit service error sites in one no-behavior-change commit, migrate handlers file-by-file, deprecate `models.APIError`, add a grep guard test that prevents handlers from importing `models.APIError` again, final suite run.

**Severity reclassifications:**

- **C-3 → P1 (was P0).** The `reviews_max=9999` sentinel is API design quality, not a security vulnerability. No money is lost, no data leaks, no escalation. It must still be fixed before public docs ship (i.e. before launch), but it does not belong alongside an auth bypass and unbounded Stripe sessions in the P0 tier. Renumbered as **H-12** in the P1 table. Implementation chunk (Chunk 2) is unchanged.
- **H-5 (lat/lon range) → DELETED.** Verified against `github.com/go-playground/validator/v10@v10.28.0/regexes.go:54-55`: both `latitudeRegexString` and `longitudeRegexString` already enforce `[-90,90]` / `[-180,180]` ranges. The struct already uses `validate:"omitempty,latitude"` / `"omitempty,longitude"`, so this is already covered. No code change needed.
- **H-7 (handler-level sort allowlist) → P2.** The repo layer (`postgres/repository.go:174-184`) is the authoritative enforcement point and has the allowlist with a safe-default fallback. A second allowlist at the handler is defense-in-depth, not a release blocker. Moved to Chunk 6 / Task 6.4.
- **H-10 (pagination overflow) → kept, but flagged as marginal.** Go's default `int` is 64-bit on every platform we ship to; `(page-1)*limit` overflowing requires `page > 9.2×10^16`. The fix is one line so we still ship it as defense-in-depth, but it is not driving the launch decision.

**Reframings (no severity change, just accurate descriptions):**

- **M-3 is NOT a TOCTOU.** Job ownership doesn't mutate — jobs don't transfer between users — so there is no time-of-check-vs-time-of-use window in the classical sense. The fix (scope `GetJobCosts` query by `user_id`) is still correct as defense-in-depth and consistency with the new policy from §4. Reworded throughout.

**Bug fixes in already-drafted tasks:**

- **Task 3.1 `decodeStrict` is broken** — passing `nil` as the first arg to `http.MaxBytesReader` means the writer used to send the 413 response is missing. Verified via `web/middleware/middleware.go:230` that the project already has a `MaxBodySize` middleware applied at the router boundary. Therefore the per-handler size limit in `decodeStrict` is redundant. **Updated Task 3.1: drop the size-limit line entirely and have `decodeStrict` only call `DisallowUnknownFields()` + reject trailing data.** The router-level `MaxBodySize` is already the canonical defense.
- **Task 4.2 test contradicts itself** — `require.Empty(t, repo.lastGetUserID)` immediately followed by `require.Equal(t, "user-B", repo.lastGetUserID)` cannot both be true. The whole spy approach is overengineered for a behavior test. **Updated Task 4.2 to drop the spy and assert behavior directly: cross-tenant `Delete` returns `ErrNotFound` and the row is unchanged in the repo.**
- **Task 2.3 (`images_max` plumbing) had no migration plan.** Existing rows in `jobs.data` have `images: true` but no `images_max` key. After deserialization the new struct field defaults to `0`, which under the new convention means *skip images* — the opposite of intent. **Updated Task 2.3 with a defensive default-on-read in the runner AND a one-shot DB backfill migration. Decision: pre-launch we have no public users, so we can also drop the `images` boolean entirely and use `images_max > 0` as the on/off signal — the cleanest API. See revised Task 2.3 below.**

**New tasks added (the reviewer flagged real gaps):**

- **Task 3.5 — Proxies SSRF validation.** Verified at `runner/webrunner/webrunner.go:1229`: job-supplied `proxies` are passed straight to `scrapemateapp.WithProxies(...)` and used for outbound HTTP requests by the scraper. An attacker can supply proxy URLs that target internal services (`http://169.254.169.254/...` for cloud metadata, `http://localhost:5432/...` etc.). Added to Chunk 3.
- **Task 3.6 — Tighter per-endpoint rate limit on POST `/api/v1/jobs`.** The global `PerUserRateLimit` middleware (`middleware.go:382`) IS applied (5 req/s burst 20), so the "no rate limit" framing in the review is partially incorrect. However, 5 req/s is too lenient for a billable write endpoint that takes a `SELECT ... FOR UPDATE` lock per request. Add a tighter per-endpoint limiter (e.g. 1 req/s burst 3) on `POST /api/v1/jobs` specifically.
- **Chunk 7 — Job creation idempotency.** Stripe-style `Idempotency-Key` header support on `POST /api/v1/jobs`. For a billable API where each job costs credits, a network retry can double-charge. This is a real P1 gap.
- **Chunk 8 — RFC 7807 error envelope.** Define one structured error shape and use it everywhere. Clients need to parse errors programmatically, not by string-matching `"exceeds maximum"`.

**Findings that the reviewer missed but were already in the original plan (clarified for visibility):**

- **H-3 (Proxies array unbounded) and H-4 (per-keyword length cap)** were folded into Task 2.2's struct-tag rewrite (`Proxies`: `max=100,dive,url,max=2048`; `Keywords`: `dive,min=1,max=200`). The reviewer flagged them as "missing" because they aren't standalone tasks. **Action: split them out so they have explicit acceptance tests.** H-4 now has its own assertion in Task 2.2; H-3 expands into Task 3.5 (SSRF validation) since the reviewer's concern is broader than just length.

---

## Executive Summary of Findings

The audit covered every HTTP route under `web/web.go`, the auth + admin middleware, the credit/billing pipeline, the Stripe webhook, the API key + webhook subscription flows, and the job creation parameter surface. Findings are graded P0 (must fix before launch), P1 (fix before launch), and P2 (fix shortly after launch).

### P0 — Critical (release blockers)

| ID | File:line | Issue |
|----|-----------|-------|
| ~~**C-2**~~ | ~~`web/auth/auth.go:106-129`~~ | **RESOLVED OUT-OF-BAND.** Dev-auth bypass entirely deleted from the codebase (see Revision Notes above). No code path for `BRAZA_DEV_AUTH_BYPASS` or `X-Braza-Dev-User` headers exists anymore. Vulnerability class is gone, not just mitigated. |

> **Note:** The original draft listed `reviews_max=9999` as **C-3** here. Per the Revision Notes above, it has been **downgraded to P1** and is now **H-12** in the table below. The fix still ships in Chunk 2 — only the severity changed.

### P1 — High (fix before launch)

| ID | File:line | Issue |
|----|-----------|-------|
| **H-1** | `web/handlers/api.go:42-45` | `apiScrapeRequest.Name` accepts unbounded strings. No `max` tag. Memory/DB pressure DoS vector. Implementation: **Task 2.2, Step 5** — `validate:"required,min=1,max=200"`. |
| **H-2** | `models/job.go:20` | `Radius` has `min=0` but no `max`. `INT_MAX` accepted. |
| **H-3** | `models/job.go:23`, `runner/webrunner/webrunner.go:1229` | `Proxies` array has no element count cap, no per-element URL/length validation, **and** the URLs are passed directly to the scraper for outbound HTTP via `scrapemateapp.WithProxies(...)`. SSRF vector — an attacker can supply `http://169.254.169.254/...` (cloud metadata) or `http://localhost:5432/...` (internal services). Implementation lives in **Task 3.5** below. |
| **H-4** | `models/job.go:10` | `Keywords` has `min=1,max=5` for the array but no per-keyword length cap. Each keyword can be 100KB. Worse than the search-length issue because keywords feed into `LIKE` queries downstream. Implementation: tag added in Task 2.2 + dedicated assertion. |
| ~~**H-5**~~ | ~~`models/job.go:17-18`~~ | ~~`lat`/`lon` validated as format only, no range check.~~ **DELETED** — verified that `validator/v10` `latitude`/`longitude` regexes already enforce `[-90,90]` / `[-180,180]` (`regexes.go:54-55`). The struct already uses these tags. No code change needed. |
| **H-6** | `web/utils/validation.go:49-53` | `lang` requires exactly 2 chars but no allowlist — accepts `"xx"`, `"@@"`, `"!!"`. Should be ISO 639-1 allowlist. |
| ~~**H-7**~~ | ~~`web/handlers/api.go:282-290`~~ | ~~`sort` query param defense-in-depth allowlist at handler.~~ **Downgraded to P2**: the repo allowlist (`postgres/repository.go:174-184`) is the authoritative defense and has a safe-default fallback. Moved to **Task 6.4**. |
| **H-8** | `web/handlers/api.go:292` | `search` query param has no length cap. Repository builds `LOWER(name) LIKE '%<input>%'` — slow-LIKE DoS. |
| **H-9** | `web/handlers/api.go` (every handler) | All `json.NewDecoder(r.Body).Decode(...)` calls omit `DisallowUnknownFields()`. Unknown JSON fields are silently ignored — request-smuggling and confusion-attack vector. Note: router-level body size limit ALREADY exists via `web/middleware/middleware.go:230` `MaxBodySize`, so the helper only needs `DisallowUnknownFields()` + trailing-data check. |
| **H-10** | `web/handlers/api.go:417-422`, `:588-590` | Pagination `limit` capped at 1000 in `GetJobResults`/`GetUserResults` but 100 elsewhere. Cap inconsistency. Also `offset := (page-1)*limit` has no overflow guard. **Marginal in practice** — Go `int` is 64-bit, overflow needs `page > 9.2×10^16` — but the fix is one line and ships as defense-in-depth. |
| **H-11** | `web/handlers/api.go:401-410` | `GetJobResults` does not validate `jobID` is a UUID before querying (other handlers do via `uuid.Parse`). |
| **H-12** | `models/job.go:15`, `web/utils/validation.go:15`, `web/services/estimation.go:29` | (Was C-3, downgraded from P0.) `reviews_max` uses `9999` as a magic "unlimited" sentinel. The frontend hardcodes `unlimitedReviewsMax = 9999`. The estimation service treats anything `>= 1000` as unlimited. **Not a security vulnerability** — no money lost, no data leak, no escalation — but incompatible with public API documentation, OpenAPI 3.1 typing, and every major REST style guide. Must be fixed before public docs ship (which is launch). Replaced by the unified cap-parameter convention in §2 and rolled out in Chunk 2. |
| **H-13** | `web/handlers/api.go:48-178` (POST `/api/v1/jobs`) | No tighter per-endpoint rate limit on job creation. Global `PerUserRateLimit` (`middleware.go:382`) is applied at 5 req/s burst 20, but for a billable write endpoint that takes a `SELECT ... FOR UPDATE` lock per request and queues a scraping job, that's too lenient. Add a per-endpoint limiter (≈1 req/s burst 3). Implementation in **Task 3.6**. |
| **H-14** | `web/handlers/api.go:48-178` (POST `/api/v1/jobs`) | No idempotency support. A network retry on job creation can double-charge a user. Stripe-style `Idempotency-Key` header. Implementation in **Chunk 7**. |

### P2 — Medium (fix immediately after launch, ideally before)

| ID | File:line | Issue |
|----|-----------|-------|
| **M-1** | `web/services/results.go:163-216` | `GetEnhancedJobResultsPaginated` query is `WHERE job_id = $1` only — no `user_id` filter. Currently mitigated by the handler-level `App.Get(ctx, jobID, userID)` ownership check at `api.go:428` (which returns 404 on cross-user access), but if any other call site invokes the service directly, it leaks. Defense-in-depth: scope the query by `user_id`. |
| **M-2** | `web/service.go:62-67` | `Service.Delete` calls `s.repo.Get(ctx, id, "")` with empty userID to fetch status before cancelling. The final delete at line 87 does pass userID, so it's **not** a delete-IDOR. The realistic worst case is a 404-vs-500 timing leak across tenants, and since job IDs are UUIDv7 (unguessable) the attacker gets nothing actionable. **Marginal severity, trivial fix** — just pass userID through. Don't over-test it. |
| **M-3** | `web/handlers/api.go:454-495` | `GetJobCosts` verifies ownership at line 476 then calls `cs.GetJobCosts(ctx, jobID)` at line 488 without userID. **This is NOT a TOCTOU** — job ownership doesn't change (jobs don't transfer between users), so there's no time-of-check-vs-time-of-use window. The fix (scope the cost query by `user_id`) is correct as **defense-in-depth and consistency** with the new policy that every result-bearing query is user-scoped. |
| **M-5** | (missing) | No `/api/v1/admin/credits/{userID}/grant` endpoint exists. Customer-service operations have no audit-trailed credit grant path. |
| **M-6** | `web/handlers/validation.go:12-40` | `formatValidationErrors` lowercases struct field names — leaks Go field naming and is inconsistent with the JSON tag name returned in the spec. |
| **M-8** | `web/handlers/api.go:282-290` | (Was H-7, downgraded.) `sort` query param defense-in-depth allowlist at the handler boundary. Repo layer is authoritative; this is belt-and-suspenders. Implementation in **Task 6.4**. |
| **M-9** | every error site | No structured error response contract. Errors are ad-hoc strings (`"reviews_max exceeds maximum of 500"`, `"missing keywords"`). Public API clients need to parse errors programmatically, not by string-matching English. Adopt RFC 7807 (Problem Details for HTTP APIs) — one envelope, used everywhere. Implementation in **Chunk 8**. |

### Verified-not-an-issue (false positives from initial sweep)

- **Stripe webhook signature verification** — correctly uses `webhook.ConstructEvent` (`billing/service.go:131-141`); rejects with 400 if the signing key is empty. ✓
- **Webhook idempotency** — `processed_webhook_events` table with primary key on `event_id`, dedup inside SERIALIZABLE transaction. ✓
- **API key storage** — dual-hash (HMAC-SHA256 lookup + Argon2id verification), constant-time compare, dummy-salt for non-existent keys. ✓
- **Concurrent job race** — `SELECT ... FOR UPDATE` lock on user row inside the create transaction. ✓
- **Admin role bypass via API key** — `requireAdminSession` rejects requests with `GetAPIKeyID(ctx) != ""`. ✓
- **Job cost spoofing** — cost is computed server-side from validated job params; never read from request body. ✓
- **SQL injection** — all queries parameterized; sort allowlist at repo layer. ✓
- **SSRF in webhook URL creation** — `ValidateWebhookURL` resolves DNS, blocks private/loopback IPs, blocks redirects. ✓

---

## §2 — Cap Parameter Convention (THE Decision)

The user asked for the absolute gold standard for representing "no cap" in a public API. After reviewing OpenAPI 3.1 / JSON Schema 2020-12, Google AIP-158, Microsoft Azure REST guidelines, Zalando, and how Stripe / GitHub / Google Cloud / AWS actually ship this:

### Decision: **Required concrete integer with a published hard maximum. NO "unlimited" sentinel.**

This is what every major billable public API does. Stripe `limit` is `1..100`. GitHub `per_page` is `1..100`. Google AIP-158 explicitly forbids "unlimited" — `0` means *server picks default*, never *no cap*. AWS `MaxResults` always has a documented ceiling.

For a billable API where every result costs the customer money, exposing an "unlimited" value is a footgun: one bug in a client SDK drains a customer's credits. We refuse to make that possible.

### The rule (applies to every cap field in the API)

> Every integer cap field — `max_results`, `reviews_max`, `images_max`, `depth`, `radius`, future fields — **must** declare:
> 1. `minimum` (the smallest meaningful value, normally `1`)
> 2. an explicit `maximum` (the published hard ceiling)
> 3. an explicit `default` (what the server uses when the field is omitted)
> 4. a `description` that names the billing unit
>
> The field is **optional** in the request body. Omission uses the default. There is **no sentinel for unlimited**. To retrieve large result sets, clients paginate or issue multiple jobs. Values above the max are rejected with **HTTP 400** and a precise error message naming the field and the cap — **not** silently coerced.

### OpenAPI 3.1 schema template

```yaml
reviews_max:
  type: integer
  format: int32
  minimum: 0          # 0 is allowed iff "skip reviews entirely" is a meaningful semantic
  maximum: 500
  default: 10
  description: |
    Maximum number of reviews to scrape per place. Each review counts toward
    billing. The hard ceiling is 500 reviews/place; requests above this are
    rejected with HTTP 400. Set to 0 to skip review scraping entirely.
    Omit the field to use the default of 10.
```

Notes:
- **No `nullable`** — OAS 3.1 dropped the keyword. We do not accept `null` for cap fields.
- **`min: 0` only when "skip" is semantically distinct from "default"** — for `reviews_max` (per-place) it is (skip-this-place's-reviews vs. some). For `images_max` (per-job total) it is also distinct (skip-all-images vs. some, where "skip all" is the billing-safe default). For `max_results` and `depth` it isn't, so `minimum: 1`.
- **Reject, don't coerce.** Google AIP coerces silently; for a billing API that's a support-ticket generator. We reject with 400 so the client knows exactly what they sent and how the bill will be calculated.
- **Most caps are per-job; `reviews_max` is per-place; `images_max` is per-job total.** The `Scope` column in the table below makes this explicit. Per-place caps multiply with `max_results` to give the per-job worst case; per-job-total caps don't multiply.

### Concrete caps for our API (initial values — tune before launch)

| Field | min | max | default | "skip" allowed | Scope | Notes |
|-------|----:|----:|--------:|---------------:|-------|-------|
| `max_results` | 1 | 500 | 20 | no | per-job | Places per job. Today 0=unlimited; we change `0` semantics to `400 invalid` and require an explicit number. Real-world test: 1 search × depth=20 yielded 112 places, so 500 is a comfortable headroom over typical jobs. |
| `reviews_max` | 0 | 500 | 10 | yes (`0`) | **per-place** | Reviews per place. Replaces today's 9999 sentinel. `0` keeps the existing "skip reviews" semantic. A single business rarely has more than a few hundred reviews; 500 is the safe ceiling. |
| `images_max` | 0 | 20000 | 0 | yes (`0`) | **per-job total** | NEW field — today images are bool + hardcoded behavior. Cap is the **total number of images across all places** in the job, NOT per-place. Real-world test: 112 places × ~79 avg images/place = 8870 total images. The 20k ceiling allows ~250 places worth of imagery before triggering, which covers any normal job at the 500-place ceiling. Default `0` skips image scraping entirely (billing-safe default — image events are the largest cost line item per the test job). |
| `depth` | 1 | 20 | 5 | no | per-job | Search depth. Already correct, keep as-is. |
| `radius` | 0 | 50000 | 0 | yes (`0`) | per-job | Meters. `0` = no radius constraint. Add the missing `max=50000`. |
| `max_time` | 60 | 14400 | 1800 | no | per-job | Seconds. Already capped at 4h in service-layer; surface it in struct tags. |

**Why `images_max` is per-job total, not per-place** — this is the only cap field with non-uniform scope, and the choice is deliberate:

- A single business listing on Google Maps can have anywhere from 0 to several hundred images (popular restaurants, hotels, tourist attractions). A per-place cap that's high enough to cover real businesses (e.g. 100/place) lets a 500-place job produce 50k images, which is far beyond any user's billing intent.
- Reviews are naturally bounded per place (we either scrape what's there up to a limit, and the limit is rarely binding). Images are not — Google often returns hundreds per place. The per-job total cap is the only way to bound total billing for image scraping.
- Test data confirms this: a single search "Cafe Mitte Berlin" at depth 20 produced 112 places and 8870 images (~79 avg/place). At max_results=500 with the same density, an uncapped job would produce ~39,500 images. The 20,000 cap is the billing-side safety net that prevents a single job from consuming more than ~$X worth of image credits (substitute actual unit price at launch).
- Implementation note: the runner must track running image count across places and stop scraping additional images (not stop scraping places — the place metadata is much cheaper than images) once `images_max` is reached. Implementation lives in **Task 2.3**.

### Backward compatibility

The `9999` sentinel today is a request-side convention only — the database stores whatever was sent. We need a one-shot migration of inflight job creation requests:

1. Backend treats incoming `reviews_max == 9999` as an error after this change ships → frontend must be updated in lockstep.
2. Frontend must stop sending `9999` and instead send the user's actual chosen value, defaulted to `10`.
3. Existing rows in the `jobs` table with `reviews_max=9999` are historical only (no impact, the scraper has already finished them).
4. Public docs: this is a pre-launch change, so we have no third-party clients to migrate.

This change ships as a **breaking** change from the frontend's perspective. Coordinate with the frontend PR before merging this branch.

---

## File Structure (what this plan touches)

**Modified:**
- `models/job.go` — tighten struct tags (cap, length, range, allowlist) for every cap field
- `web/utils/validation.go` — replace `maxReviewsMax = 9999` with the new caps; add lang allowlist; add `images_max` validation; add lat/lon range checks; remove "unlimited" sentinel handling
- `web/handlers/api.go` — add `DisallowUnknownFields`, sort allowlist at handler boundary, search length cap, pagination overflow guard, UUID validation in `GetJobResults`, unify pagination caps
- `web/handlers/validation.go` — fix error messages to use JSON tag names instead of struct field names
- `web/services/results.go` — add `user_id` filter to `GetEnhancedJobResultsPaginated` (defense-in-depth)
- `web/service.go` — fix `Delete` to use authenticated userID in the status-check Get instead of empty string
- `web/auth/auth.go` — tighten dev-bypass env check (require explicit `development` or `test`, refuse on empty)
- `web/services/estimation.go` — drop the `>=1000` "unlimited reviews" branch; consume validated `reviews_max` directly
- `docs/api.md` (or wherever the OpenAPI spec lives) — update spec to reflect new caps and document the convention

**Created:**
- `web/utils/cap_params.go` — single source of truth for cap constants (`CapMaxResults`, `CapReviewsMax`, `CapImagesMaxTotal`, `CapRadiusMeters`, …) and the lang allowlist
- `web/utils/cap_params_test.go` — table-driven tests for the validator
- `web/handlers/decode.go` — `decodeStrict` helper (DisallowUnknownFields + trailing-data check; body size handled by middleware)
- `web/handlers/pagination.go` — `parsePagination` helper with overflow guard and unified caps
- `web/handlers/problem.go` — RFC 7807 `Problem` envelope and `WriteProblem` (Chunk 8)
- `web/middleware/idempotency.go` — `Idempotency-Key` middleware (Chunk 7)
- `postgres/idempotency.go` — postgres impl of `IdempotencyRepo`
- `scripts/migrations/<NNN>_drop_images_bool_default_images_max.up.sql` — Task 2.3 backfill
- `scripts/migrations/<NNN>_add_idempotency_keys.up.sql` — Task 7.1
- `web/handlers/admin_credits.go` (P2 — Task 6.2) — `POST /api/v1/admin/credits/{userID}/grant` with audit logging

**Tests (modified or created):**
- `web/handlers/api_scrape_integration_test.go` — drop `unlimitedReviewsMax = 9999`; add table cases for the new caps and rejection behavior
- `web/handlers/api_test.go` — pagination overflow, sort allowlist, search length cap, DisallowUnknownFields
- `web/auth/auth_test.go` — dev-bypass env check (rejects empty `APP_ENV`)
- `web/services/results_test.go` — cross-user query returns zero rows when user_id filter is in place

---

## Chunk 2: Cap Parameter Convention Rollout — ✅ COMPLETE (2026-04-09)

This is the §2 decision applied. **Coordinate with the frontend** before merging — `reviews_max=9999` and `max_results=0` (the legacy "unlimited" sentinels) will become 400 errors, and the `max_time` ceiling is now 1 hour (was 4 hours).

**Status:** Four tasks landed.

- Task 2.1 — `fa30aa4` `feat(api): add unified cap-parameter constants and lang allowlist`
- Task 2.2 — `79845e8` `feat(api): unified cap-parameter convention; cap job name`
- Task 2.3 — `84f687a` `feat(scraper): replace images bool with images_max cap; backfill in-flight jobs`
- Task 2.4 — *(this commit)* `feat(api): REST defaults + headless-realistic max_time` — applied the brainstorm outcome (REST best-practice posture, ApplyJobDataDefaults helper, max_time ceiling 4h→1h, images_max ceiling 20k→40k for production concurrency 8)

### REST best-practice resolution (the brainstorm outcome — 2026-04-09)

The pay-as-you-go business model creates tension between two design goals:
- **REST best practice**: conservative defaults so clients hitting the API directly don't accidentally trigger expensive operations (Stripe/GitHub/Google Places all do this).
- **Pay-as-you-go revenue alignment**: when a user does NOT cap a field, scrape generously rather than miserably.

The resolution: **API stays REST-compliant; the frontend handles the revenue nudging.**

- API defaults are conservative (small, cheap, fail-safe). A client hitting the API with only `name`/`keywords`/`lang` gets a 50-place job at depth 5 with no enrichment — small bill, surprising-bill-free.
- Hard ceilings exist on every resource-consuming parameter and exceeding them returns a descriptive 400.
- "Missing" never means "unlimited" — `web/utils.ApplyJobDataDefaults` fills in the documented defaults at the API entry point. There's no magic semantic.
- The frontend's "no cap" UX toggles send the hard ceiling explicitly. Users who want generous scraping get it; users who hit the API directly without context get a safe default.

### Final cap values (locked after the brainstorm)

| Field | Default | Ceiling | Required? | Notes |
|---|---|---|---|---|
| `keywords` | — | 5 (≤200 bytes each) | **yes** | minimum viable request |
| `lang` | — | 35-code ISO 639-1 allowlist | **yes** | minimum viable request |
| `name` | — | 200 chars | **yes** | apiScrapeRequest top-level |
| `depth` | **5** | **20** | optional | filled by ApplyJobDataDefaults |
| `max_results` | **50** | **500** | optional | per-job total across all keywords |
| `max_time` | **30 min** | **1 hour** | optional | headless-browser realistic ceiling |
| `reviews_max` | **0 (skip)** | **500/place** | optional | toggle semantic — frontend sends positive value when user enables reviews |
| `images_max` | **0 (skip)** | **40 000 total** | optional | per-job total; sized for production concurrency 8 × 1h × ~80 imgs/place |
| `radius` | 0 (no constraint) | 50 000 m | optional | |
| `zoom` | 0 (no zoom override) | 21 | optional | |
| `proxies` | — | 100 (each ≤2048 bytes) | optional | |

Why these specific numbers (the math the user and I worked through):

- **`max_results` default = 50**: depth=5 (the depth default) naturally returns 40-50 places. The default matches the natural yield, so a client hitting the API with no parameters gets a complete (rather than truncated) job at the conservative depth.
- **`max_results` ceiling = 500**: covers 5-keyword power-user jobs at depth=20 (~600 natural ceiling, clipped to 500).
- **`max_time` ceiling = 1 hour**: headless Chromium scraping Google Maps degrades sharply over time — Chromium memory creep, Google's anti-bot escalation, session staleness, container supervisor SIGTERMs. 4 hours was unrealistic. 1 hour is the practical wall-clock limit; users who need more should split into multiple jobs.
- **`images_max` ceiling = 40 000**: at production concurrency 8 with max_time = 1h and ~60s per place at full enrichment, the realistic worst case is ~480 places × ~80 images/place ≈ 38 400 images. 40 000 covers this with small headroom.
- **`reviews_max` and `images_max` defaults = 0**: enrichments are opt-in. The frontend toggle sends a positive value when the user enables the toggle; the API default is "off."

### What's wired up

- `web/utils/cap_params.go` is the single source of truth for every cap and default.
- `web/utils/validation.go` adds `ApplyJobDataDefaults(d *models.JobData)` — fills zero-valued optional fields with their defaults. Idempotent and nil-safe.
- The HTTP handlers (`Scrape`, `EstimateJobCost`, admin `CreateJob`) all call `ApplyJobDataDefaults` between JSON decode and `validate.Struct`. This is the API safety net.
- `models.JobData` struct tags drop `required` from `Depth`, `MaxResults`, `MaxTime`. The minimum viable request is now just `name` + `keywords` + `lang`.
- `runner/jobs.go` plumbs an `*atomic.Int64` per-job total image budget through `gmaps.GmapJob` → `gmaps.PlaceJob`. `gmaps.PlaceJob.extractImages` checks the budget before scraping and decrements after, stopping image extraction once the budget is exhausted.
- Migration `000033_drop_images_bool_default_images_max` backfills in-flight rows.
- Test coverage: `web/utils/validation_test.go` (24 cases — 18 from Task 2.2 + 6 added by Task 2.4 for the new defaults/ceilings), `runner/jobs_test.go` (3 cases for budget plumbing), `gmaps/image_budget_test.go` (3 cases for the option attach behavior), `web/handlers/validation_test.go` (24 cases including a "minimal valid request" case that locks the new REST posture).

### Known runtime gap (intentional)

The `Load()`/`Add(-N)` race in `extractImages` allows concurrent PlaceJobs to overshoot the budget by `concurrency × images_per_place`. At production concurrency 8 × ~80 imgs/place, worst-case overshoot is ~640 images. This is acceptable for billing exposure (the goal is bounding, not exact accounting), and was an explicit design decision — exact accounting would require a mutex on the hot path.

### Task 2.1: Create the central caps file

**Files:**
- Create: `web/utils/cap_params.go`
- Create: `web/utils/cap_params_test.go`

- [ ] **Step 1: Write the failing test**

```go
package utils

import "testing"

func TestCapConstants_AreSane(t *testing.T) {
    require.Equal(t, 500, CapMaxResults)
    require.Equal(t, 500, CapReviewsMax)
    require.Equal(t, 20_000, CapImagesMaxTotal)
    require.Equal(t, 50_000, CapRadiusMeters)
    require.Equal(t, 14_400, CapMaxTimeSeconds)
}

func TestSupportedLangs_ContainsCommon(t *testing.T) {
    for _, lang := range []string{"en", "de", "fr", "es", "it", "pt", "nl"} {
        require.Truef(t, IsSupportedLang(lang), "expected %q to be supported", lang)
    }
    require.False(t, IsSupportedLang("xx"))
    require.False(t, IsSupportedLang("@@"))
    require.False(t, IsSupportedLang(""))
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./web/utils/ -run TestCap -v
```

- [ ] **Step 3: Create `cap_params.go`**

```go
package utils

// Cap parameter constants — single source of truth for handler validation,
// service-layer validation, and the OpenAPI spec generator. Values are tuned
// for cost and operational ceilings, not for the underlying scraper limits.
//
// Convention (see docs/superpowers/plans/2026-04-08-api-production-readiness-audit.md §2):
// every integer cap field has min, max, and default. There is NO sentinel for
// "unlimited" — clients paginate or run multiple jobs.
const (
    // CapMaxResults bounds places per job. Per-job. min 1.
    // Real-world test: 1 search × depth=20 yielded 112 places, so 500 is
    // a comfortable headroom over typical jobs.
    CapMaxResults     = 500
    DefaultMaxResults = 20

    // CapReviewsMax bounds reviews PER PLACE. min 0 — 0 means "skip reviews".
    // A single business rarely has more than a few hundred reviews; 500 is
    // the safe per-place ceiling.
    CapReviewsMax     = 500
    DefaultReviewsMax = 10

    // CapImagesMaxTotal bounds the TOTAL number of images across all places
    // in a job — NOT per place. Image counts on Google Maps are unbounded
    // per business (popular venues return hundreds), so a per-place cap that
    // covers real businesses would let a 500-place job produce ~50k images.
    // The per-job total cap is the only way to bound total billing for image
    // scraping. Real-world test: 112 places × ~79 avg images/place = 8870
    // total. The 20k ceiling allows ~250 places-worth of imagery before
    // triggering, which covers any normal job at the 500-place ceiling.
    //
    // The runner must stop scraping additional IMAGES (not stop scraping
    // places — place metadata is much cheaper) once this cap is reached.
    //
    // min 0 — 0 means "skip all image scraping". The default is 0 (the
    // billing-safe default — image events were the largest cost line item
    // in the test job).
    CapImagesMaxTotal     = 20_000
    DefaultImagesMaxTotal = 0

    // CapDepth bounds search depth. min 1.
    CapDepth     = 20
    DefaultDepth = 5

    // CapRadiusMeters bounds search radius in meters. min 0 — 0 means
    // "no radius constraint".
    CapRadiusMeters     = 50_000
    DefaultRadiusMeters = 0

    // CapMaxTimeSeconds bounds wall-clock job duration. min 60.
    CapMaxTimeSeconds     = 14_400 // 4 hours
    DefaultMaxTimeSeconds = 1_800  // 30 minutes
)

// supportedLangs is the ISO 639-1 allowlist of language codes the Google Maps
// scraper supports. Two-character codes only. Add to this list when launching
// new locales.
var supportedLangs = map[string]struct{}{
    "en": {}, "de": {}, "fr": {}, "es": {}, "it": {}, "pt": {}, "nl": {},
    "pl": {}, "tr": {}, "sv": {}, "no": {}, "da": {}, "fi": {}, "cs": {},
    "sk": {}, "hu": {}, "ro": {}, "el": {}, "bg": {}, "hr": {}, "sl": {},
    "et": {}, "lv": {}, "lt": {}, "ja": {}, "ko": {}, "zh": {}, "ar": {},
    "he": {}, "th": {}, "vi": {}, "id": {}, "ms": {}, "uk": {}, "ru": {},
}

// IsSupportedLang reports whether the 2-char ISO 639-1 code is in the allowlist.
func IsSupportedLang(code string) bool {
    _, ok := supportedLangs[code]
    return ok
}
```

- [ ] **Step 4: Run, confirm pass; commit**

```bash
go test ./web/utils/
git add web/utils/cap_params.go web/utils/cap_params_test.go
git commit -m "feat(api): add unified cap-parameter constants and lang allowlist"
```

---

### Task 2.2: Apply caps to job validation

**Files:**
- Modify: `models/job.go` — add struct tags using the new constants where possible (struct tags can't reference Go consts; use literal values matching the constants and a comment pointing at `cap_params.go`)
- Modify: `web/utils/validation.go` — replace `maxReviewsMax = 9999` block with calls to the new caps; add lang allowlist check; add lat/lon range check; add `images_max` validation
- Modify: `web/services/estimation.go` — remove the `>= 1000` "unlimited" branch; consume `reviews_max` as-is

- [ ] **Step 1: Write the failing tests** — `web/utils/validation_test.go`

```go
func TestValidateJobData_RejectsReviewsMaxAbove500(t *testing.T) {
    d := &models.JobData{
        Keywords: []string{"pizza"}, Lang: "en", Depth: 5,
        MaxTime: 60 * time.Second, MaxResults: 10, ReviewsMax: 9999,
    }
    err := ValidateJobData(d)
    require.Error(t, err)
    require.Contains(t, err.Error(), "reviews_max")
    require.Contains(t, err.Error(), "500")
}

func TestValidateJobData_RejectsMaxResultsAbove500(t *testing.T) {
    d := newValidJobData()
    d.MaxResults = 1000
    require.Error(t, ValidateJobData(d))
}

func TestValidateJobData_RejectsZeroMaxResults(t *testing.T) {
    d := newValidJobData()
    d.MaxResults = 0
    err := ValidateJobData(d)
    require.Error(t, err, "0 must be invalid for max_results — no unlimited sentinel")
}

func TestValidateJobData_RejectsLangNotInAllowlist(t *testing.T) {
    d := newValidJobData()
    d.Lang = "xx"
    require.Error(t, ValidateJobData(d))
}

func TestValidateJobData_RejectsLatOutOfRange(t *testing.T) {
    d := newValidJobData()
    d.Lat = "100.0"
    require.Error(t, ValidateJobData(d))
}

func TestValidateJobData_RejectsLonOutOfRange(t *testing.T) {
    d := newValidJobData()
    d.Lon = "200.0"
    require.Error(t, ValidateJobData(d))
}

func TestValidateJobData_RejectsRadiusAboveCap(t *testing.T) {
    d := newValidJobData()
    d.Radius = 60_000
    require.Error(t, ValidateJobData(d))
}

func TestValidateJobData_AcceptsZeroReviewsMaxAsSkip(t *testing.T) {
    d := newValidJobData()
    d.ReviewsMax = 0
    require.NoError(t, ValidateJobData(d))
}
```

- [ ] **Step 2: Run, confirm fail**

```bash
go test ./web/utils/ -run TestValidateJobData -v
```

- [ ] **Step 3: Update `web/utils/validation.go`**

Replace the constants block:

```go
const (
    // Caps now live in cap_params.go — references kept here for clarity.
    minDepth          = 1
    maxDepthCap       = CapDepth
    maxResultsCap     = CapMaxResults
    maxReviewsCap     = CapReviewsMax
    maxImagesTotalCap = CapImagesMaxTotal // per-job total, NOT per-place
    maxRadiusCap      = CapRadiusMeters
    maxTimeCap        = time.Duration(CapMaxTimeSeconds) * time.Second
)
```

Rewrite `ValidateJobData` body to:
- Reject `Lang` not in `IsSupportedLang`
- Reject `Depth < 1 || > CapDepth`
- Reject `MaxResults < 1 || > CapMaxResults` (note: `MaxResults < 1` is the change — no more 0 = unlimited)
- Reject `ReviewsMax < 0 || > CapReviewsMax` (per-place cap)
- Reject `ImagesMax < 0 || > CapImagesMaxTotal` (new field, **per-job total**, see Task 2.3)
- Parse `Lat`/`Lon` as floats and reject if out of `[-90,90]`/`[-180,180]`
- Reject `Radius < 0 || > CapRadiusMeters`
- Reject `MaxTime < 60s || > 4h`

Also drop the `extra-reviews` `>= 1000` branch in `web/services/estimation.go:255` — `reviews_max` is now bounded so this defensive coercion is dead code.

- [ ] **Step 4: Update `models/job.go` struct tags**

```go
type JobData struct {
    Keywords   []string      `json:"keywords"     validate:"required,min=1,max=5,dive,min=1,max=200"`
    Lang       string        `json:"lang"         validate:"required,len=2"`
    Depth      int           `json:"depth"        validate:"required,min=1,max=20"`
    Email      bool          `json:"email"`
    // ImagesMax is the TOTAL number of images across all places in the job
    // — NOT per place. See §2 of this plan for the rationale. 20000 is the
    // hard ceiling; 0 means skip image scraping entirely (billing-safe).
    ImagesMax  int           `json:"images_max"   validate:"omitempty,min=0,max=20000"`
    // ReviewsMax is the cap on reviews per place. 500 is a safe per-place
    // ceiling; 0 means skip reviews.
    ReviewsMax int           `json:"reviews_max"  validate:"omitempty,min=0,max=500"`
    MaxResults int           `json:"max_results"  validate:"required,min=1,max=500"`
    Lat        string        `json:"lat"          validate:"omitempty,latitude"`
    Lon        string        `json:"lon"          validate:"omitempty,longitude"`
    Zoom       int           `json:"zoom"         validate:"omitempty,min=0,max=21"`
    Radius     int           `json:"radius"       validate:"omitempty,min=0,max=50000"`
    MaxTime    time.Duration `json:"max_time"     validate:"required"`
    FastMode   bool          `json:"fast_mode"`
    Proxies    []string      `json:"proxies"      validate:"omitempty,max=100,dive,max=2048"`
}
```

(Add `ImagesMax` to the struct, with the per-job-total semantic. The `Images bool` field is removed in Task 2.3 — `ImagesMax > 0` is now the on/off signal. The runner config in `runner/webrunner/webrunner.go:750` will need a corresponding `ImagesBudgetTotal int` field plus the cross-place enforcement counter described in Task 2.3 Step 4. The per-proxy URL content check — scheme allowlist + private-IP block — lives in `ValidateProxyURL` in Task 3.5, not in the struct tag.)

**Byte-vs-rune semantics note (H-4):** `validator/v10`'s `max=200` on a string counts **bytes** via `len()`, not runes. A 200-byte cap on a UTF-8 keyword is ~200 ASCII characters or ~50-66 CJK characters. For search keywords this is fine and intentional — the point is to prevent a 100KB keyword from hitting a downstream `LIKE` query, not to enforce a human-centric character limit. Document this in the OpenAPI spec description so international users aren't surprised when they hit the cap earlier than expected.

- [ ] **Step 5: Cap `apiScrapeRequest.Name` (H-1)**

The `Name` field lives on the request struct (`web/handlers/api.go:42-45`), not on `JobData`, so it needs its own tag fix. One-line change:

```go
type apiScrapeRequest struct {
    Name string `validate:"required,min=1,max=200"`
    models.JobData
}
```

Add a test case in the handler tests:

```go
func TestScrape_RejectsOverlongName(t *testing.T) {
    h := newTestHandler(t)
    body := fmt.Sprintf(`{"name":%q,"keywords":["pizza"],"lang":"en","depth":5,"max_results":10,"max_time":60}`,
        strings.Repeat("a", 500))
    req := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(body))
    req = req.WithContext(authCtx("user-1"))
    rr := httptest.NewRecorder()
    h.Scrape(rr, req)
    require.Equal(t, http.StatusBadRequest, rr.Code)
}
```

- [ ] **Step 6: Run validation tests**

```bash
go test ./web/utils/...
```

- [ ] **Step 7: Update integration test fixtures**

`web/handlers/api_scrape_integration_test.go:30-31` — delete `unlimitedMaxResults = 0` and `unlimitedReviewsMax = 9999`. Use concrete numbers (e.g., `100` and `50`). Add a new test case `TestScrape_RejectsLegacy9999ReviewsMax` that confirms a request body with `reviews_max: 9999` is rejected with 400 + a message naming the cap.

- [ ] **Step 8: Run the full handler test suite**

```bash
go test ./web/handlers/...
```

- [ ] **Step 9: Commit**

```bash
git add models/job.go web/handlers/api.go web/utils/validation.go web/utils/validation_test.go \
        web/services/estimation.go web/handlers/api_scrape_integration_test.go
git commit -m "feat(api): unified cap-parameter convention; cap job name; remove reviews_max=9999 sentinel"
```

---

### Task 2.3: Plumb `images_max` (per-job total) through the runner — with migration

**Files:**
- Modify: `runner/webrunner/webrunner.go:750-766` — pass `job.Data.ImagesMax` into `GmapJobConfig` and enforce the per-job total cap by passing a running counter the scraper can decrement
- Modify: `gmaps/job.go` (or wherever `GmapJobConfig` is defined) — accept `ImagesBudgetTotal int` and a budget tracker; the scraper consults the tracker before scraping each place's images
- Modify: `models/job.go` — drop the `Images` boolean (see decision below); add `ImagesMax int`
- Modify: `web/handlers/api.go` — drop `Images` from the request struct
- Create: `scripts/migrations/<NNN>_drop_images_bool_default_images_max.up.sql` (and `.down.sql`) — backfill in-flight rows
- Test: `runner/webrunner/webrunner_test.go`

**Critical semantic note (changed from first draft):** `images_max` is the **TOTAL number of images across the entire job**, NOT per-place. See §2 of this plan for the rationale and the worked test data (1 search × depth=20 → 112 places × ~79 avg images = 8870 total). The cap is 20,000 to bound total billing for image scraping.

**Why per-job-total instead of per-place:**
- Image counts on Google Maps are unbounded per business — popular venues return hundreds. A per-place cap that covers real businesses (e.g. 100/place) would let a 500-place job produce ~50k images, far beyond any user's billing intent.
- The runner must track running image count across places and stop scraping additional images (not stop scraping places — place metadata is much cheaper) once `images_max` is reached.
- The cap is enforced in the runner via a shared atomic counter passed into the scraper; once the counter reaches `images_max`, image extraction is skipped for all subsequent places in the job. The job continues to scrape place metadata, reviews, and contact details — only image extraction stops.

**Design problem the first draft missed:** existing rows in `jobs.data` have `images: true` but no `images_max` key. After deserialization the new struct field defaults to `0`, which under the new convention means *skip images* — opposite of intent. Resolution:

- **Drop the `images` boolean entirely** (cleanest API): use `images_max > 0` as the on/off signal. One field, no ambiguity. API-breaking — only viable pre-launch. **We are pre-launch, so we pick this option.**
- The migration backfills `images_max = 1000` (a sane mid-job-total default) on in-flight rows that previously had `images: true`. Historical completed rows are untouched.

**Decision: drop `Images` boolean.** Use `images_max > 0` as the toggle. Frontend coordinates the change in the same release as the `reviews_max` rename (Chunk 2).

- [ ] **Step 1: Write the migration SQL** — `scripts/migrations/<NNN>_drop_images_bool_default_images_max.up.sql`

```sql
-- Backfill images_max for in-flight jobs that used the old `images` boolean.
-- 1000 is a sane mid-default per-job total: enough to cover a typical 20-50
-- place job at ~20 images/place average without hitting the cap, but well
-- under the 20000 hard ceiling. Only touches rows that haven't completed yet
-- — historical rows are read-only.
UPDATE jobs
SET data = jsonb_set(
    data,
    '{images_max}',
    to_jsonb(1000),
    true  -- create the key if missing
)
WHERE status IN ('pending', 'working')
  AND COALESCE((data->>'images')::bool, false) = true
  AND data->>'images_max' IS NULL;

-- Drop the now-unused `images` key from all in-flight rows.
UPDATE jobs
SET data = data - 'images'
WHERE status IN ('pending', 'working')
  AND data ? 'images';
```

`.down.sql`:

```sql
-- Best-effort revert: restore `images: true` where images_max > 0. Not lossless.
UPDATE jobs
SET data = jsonb_set(data, '{images}', 'true'::jsonb, true)
WHERE status IN ('pending', 'working')
  AND COALESCE((data->>'images_max')::int, 0) > 0;
```

- [ ] **Step 2: Write the failing runner tests**

```go
func TestRunJob_PassesImagesBudgetToScraper(t *testing.T) {
    job := models.Job{Data: models.JobData{
        Keywords: []string{"pizza"}, Lang: "en", Depth: 5,
        MaxResults: 10, ImagesMax: 700, MaxTime: 60 * time.Second,
    }}
    cfg := buildGmapJobConfig(job)
    require.Equal(t, 700, cfg.ImagesBudgetTotal)
    require.True(t, cfg.ExtractImages, "ExtractImages must be true when ImagesMax > 0")
}

func TestRunJob_ImagesMaxZeroDisablesImages(t *testing.T) {
    job := models.Job{Data: models.JobData{
        Keywords: []string{"pizza"}, Lang: "en", Depth: 5,
        MaxResults: 10, ImagesMax: 0, MaxTime: 60 * time.Second,
    }}
    cfg := buildGmapJobConfig(job)
    require.False(t, cfg.ExtractImages)
    require.Equal(t, 0, cfg.ImagesBudgetTotal)
}

// TestRunJob_ImagesBudgetEnforcedAcrossPlaces is the key test for the
// per-job-total semantic: a job with 5 places where each place has 100
// candidate images and a budget of 250 must produce exactly 250 images
// across all places, not 500 (which would be 100 × 5).
func TestRunJob_ImagesBudgetEnforcedAcrossPlaces(t *testing.T) {
    // Requires either a real scraper integration or a mock that returns a
    // fixed image count per place. Skip if integration test infra is missing.
    // The assertion is on total images in the resulting job results.
    t.Skip("integration test — requires scraper mock or stripe-mock-equivalent")
}
```

- [ ] **Step 3: Update the runner config builder**

In `runner/webrunner/webrunner.go` around line 750:

```go
cfg := &gmaps.GmapJobConfig{
    MaxDepth:          job.Data.Depth,
    ReviewsMax:        job.Data.ReviewsMax,
    ExtraReviews:      job.Data.ReviewsMax > 0,
    MaxResults:        job.Data.MaxResults,
    ImagesBudgetTotal: job.Data.ImagesMax, // per-job total, NOT per-place
    ExtractImages:     job.Data.ImagesMax > 0,
    FastMode:          job.Data.FastMode,
    ExtractEmails:     job.Data.Email,
}
```

- [ ] **Step 4: Update `gmaps/job.go` — accept the per-job total budget and enforce cross-place**

The scraper currently has no concept of a per-job image budget. Add:

1. `ImagesBudgetTotal int` field on `GmapJobConfig`.
2. A shared `*atomic.Int64` counter in the job runner that the scraper decrements as it extracts images per place.
3. Before extracting images for a new place, the scraper checks `if counter.Load() <= 0 { skip image extraction for this place }`.
4. After extracting N images for a place, the scraper does `counter.Add(-int64(N))`.
5. The counter is initialized to `ImagesBudgetTotal` when the job starts.

The exact integration depends on how `gmaps/job.go` and the scrapemate integration handle per-place result yields — read the existing code before designing the counter-pass mechanism. The unit test from Step 2 (`TestRunJob_ImagesBudgetEnforcedAcrossPlaces`) is the acceptance test for this.

- [ ] **Step 5: Update `models/job.go`** — remove the `Images bool` field, add `ImagesMax int`

- [ ] **Step 5: Run the migration locally, run all tests, commit**

```bash
go test ./runner/... ./web/... ./models/...
git add scripts/migrations/<NNN>_*.sql models/job.go runner/webrunner/webrunner.go \
        gmaps/job.go runner/webrunner/webrunner_test.go web/handlers/api.go
git commit -m "feat(scraper): replace images bool with images_max cap; backfill in-flight jobs"
```

---

## Chunk 3: P1 Input Validation Hardening — ✅ COMPLETE (2026-04-09)

**Status:** All six tasks landed.

- Task 3.1 — `4b73c16` `fix(api): reject unknown JSON fields and trailing data via shared decoder` — `web/handlers/decode.go` with `decodeStrict` (required body) and `decodeStrictOptional` (empty body OK), wired into every JSON decode site (api.go, billing.go, admin.go, apikey.go, webhook.go, integration.go). Sentinel `ErrInvalidJSONBody` keeps `encoding/json`'s "unknown field {attacker-controlled name}" message out of the wire response — XSS / log-injection defense.
- Task 3.2 — `9e6eef2` `fix(api): handler-level sort allowlist and search length cap` — closed allowlist `{created_at, name, status, updated_at}` for `GetUserJobs?sort=`, 200-byte cap on `?search=`. Schema fingerprinting via sort=password is now a 400.
- Task 3.3 — `a815044` `fix(api): unify pagination caps at 100 and add overflow guard` — `web/handlers/pagination.go` with `parsePagination` (page-based) and `parseOffsetPagination` (offset-based) sharing `parseLimitParam`. `MaxPageLimit = 100` unified across job-list, results, and user-results endpoints (was 100/1000/1000). `(page-1)*limit` overflow guard against `math.MaxInt32` BEFORE multiplication.
- Task 3.4 — `6ae1cda` `fix(api): centralize UUID parsing in parseJobID` — `parseJobID(r)` helper used by GetJob, DeleteJob, CancelJob, GetJobResults, GetJobCosts. Closes the missing-validation gap on the latter two (previously leaked Postgres "invalid input syntax for uuid" errors back to clients, fingerprinting the database).
- Task 3.5 — `cee69b3` `fix(api): SSRF-validate job.proxies and cap at 100 entries` — moved `checkIPBlocklist` and the full DNS+blocklist defense from `web/handlers/webhook_url.go` to a shared `web/utils/private_ip.go` (`CheckIPBlocklist`, `AssertPublicHost`). Added `ValidateProxyURL` with the closed scheme allowlist `{http, https, socks5, socks5h}`. Wired into `ValidateJobData` with a 100-element cap. **Known DNS TOCTOU limitation documented in commit + plan §3.5** — the complete fix lives at the HTTP transport layer inside scrapemate (upstream) and the recommended interim defense is a feature flag disabling `proxies` for free-tier users.
- Task 3.6 — `fb91a08` `fix(api): tighter per-endpoint rate limit on POST /api/v1/jobs` — `PerUserRateLimit(rate.Limit(1), 3)` wraps only the POST /jobs route on top of the global chain. Burst 3 / refill 1 req/s — humans submit small batches OK, automation gets throttled.

**Bonus deliverable:** the OAuth state token in `web/handlers/integration.go` was migrated to `uuid.NewV7()` per the codebase convention but then explicitly reverted to `uuid.New()` (v4) with an inline security comment — UUIDv7 embeds a sortable timestamp that would leak the OAuth flow start time, and v4's 122 bits of randomness give exactly the cryptographic property a CSRF token wants. Test fixtures in `api_ownership_test.go` were migrated to v7 (no security implications for random test IDs).

**Test coverage added:** 9 unit tests for `decodeStrict`/`decodeStrictOptional`, 2 integration tests on the Scrape handler (`unknown_fields`, `trailing_data`), 4 cases for sort allowlist + search cap, 6 cases for pagination overflow + cap unification, 4 cases for UUID validation (including parseJobID round-trip + the 2 missing-validation gaps), 9 cases for proxy SSRF (per-element + per-job), 2 cases for the per-user rate limit (burst-then-deny + per-user scoping). Total: 36 new test cases.



### Task 3.1: DisallowUnknownFields on every JSON decoder

**Files:**
- Modify: `web/handlers/api.go`, `web/handlers/billing.go`, `web/handlers/admin.go`, `web/handlers/apikey.go`, `web/handlers/webhook.go`, `web/handlers/integration.go` — every site that calls `json.NewDecoder(r.Body).Decode(...)` 
- Create: `web/handlers/decode.go` — small helper

- [ ] **Step 1: Write the failing test**

```go
func TestScrape_RejectsUnknownFields(t *testing.T) {
    h := newTestHandler(t)
    body := `{"name":"test","keywords":["pizza"],"lang":"en","depth":5,"max_results":10,"max_time":60,"admin_override":true}`
    req := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(body))
    req = req.WithContext(authCtx("user-1"))
    rr := httptest.NewRecorder()
    h.Scrape(rr, req)
    require.Equal(t, http.StatusUnprocessableEntity, rr.Code)
    require.Contains(t, rr.Body.String(), "unknown field")
}
```

- [ ] **Step 2: Run, confirm fail**

- [ ] **Step 3: Add the helper** — `web/handlers/decode.go`

The router-level `MaxBodySize` middleware (`web/middleware/middleware.go:230`) already wraps `r.Body` in `http.MaxBytesReader(w, r.Body, 1<<20)` — confirmed via code read. So this helper does **not** call `MaxBytesReader` itself (avoiding the `nil` writer bug from the first draft of this plan and avoiding the redundancy with middleware). Its only job is unknown-field rejection and trailing-data rejection.

```go
package handlers

import (
    "encoding/json"
    "fmt"
    "net/http"
)

// decodeStrict decodes a JSON request body into v with the defensive settings
// the middleware doesn't cover: unknown fields are rejected and trailing
// non-whitespace bytes after the document are rejected. The body size limit
// is enforced by the MaxBodySize middleware at the router boundary, not here.
func decodeStrict(r *http.Request, v interface{}) error {
    d := json.NewDecoder(r.Body)
    d.DisallowUnknownFields()
    if err := d.Decode(v); err != nil {
        return fmt.Errorf("invalid request body: %w", err)
    }
    if d.More() {
        return fmt.Errorf("invalid request body: unexpected trailing data")
    }
    return nil
}
```

- [ ] **Step 4: Replace every `json.NewDecoder(r.Body).Decode(&req)` site with `decodeStrict(r, &req)`**

Search:

```bash
# Use Grep tool, not bash
```

For each handler, change the decode call. Confirm the test passes.

- [ ] **Step 5: Run all handler tests; commit**

```bash
go test ./web/handlers/...
git add web/handlers/decode.go web/handlers/api.go web/handlers/billing.go \
        web/handlers/admin.go web/handlers/apikey.go web/handlers/webhook.go \
        web/handlers/integration.go web/handlers/api_test.go
git commit -m "fix(api): reject unknown JSON fields and enforce body size at decoder"
```

---

### Task 3.2: Sort allowlist and search length cap

**Files:**
- Modify: `web/handlers/api.go:282-293` (`GetUserJobs`)
- Test: `web/handlers/api_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestGetUserJobs_RejectsInvalidSort(t *testing.T) {
    h := newTestHandler(t)
    req := httptest.NewRequest("GET", "/api/v1/jobs/user?sort=password", nil)
    req = req.WithContext(authCtx("user-1"))
    rr := httptest.NewRecorder()
    h.GetUserJobs(rr, req)
    require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetUserJobs_RejectsLongSearch(t *testing.T) {
    h := newTestHandler(t)
    req := httptest.NewRequest("GET", "/api/v1/jobs/user?search="+strings.Repeat("a", 300), nil)
    req = req.WithContext(authCtx("user-1"))
    rr := httptest.NewRecorder()
    h.GetUserJobs(rr, req)
    require.Equal(t, http.StatusBadRequest, rr.Code)
}
```

- [ ] **Step 2: Run, confirm fail; implement**

```go
var allowedJobSorts = map[string]struct{}{
    "created_at": {}, "name": {}, "status": {}, "updated_at": {},
}
const maxSearchLen = 200

// inside GetUserJobs after parsing limit:
sort := "created_at"
if v := q.Get("sort"); v != "" {
    if _, ok := allowedJobSorts[v]; !ok {
        renderJSON(w, http.StatusBadRequest, models.APIError{
            Code: http.StatusBadRequest,
            Message: "invalid sort field",
        })
        return
    }
    sort = v
}

search := q.Get("search")
if len(search) > maxSearchLen {
    renderJSON(w, http.StatusBadRequest, models.APIError{
        Code: http.StatusBadRequest,
        Message: fmt.Sprintf("search exceeds maximum length of %d", maxSearchLen),
    })
    return
}
```

- [ ] **Step 3: Run, confirm pass; commit**

```bash
go test ./web/handlers/ -run TestGetUserJobs
git add web/handlers/api.go web/handlers/api_test.go
git commit -m "fix(api): handler-level sort allowlist and search length cap"
```

---

### Task 3.3: Pagination overflow guard and cap unification

**Files:**
- Modify: `web/handlers/api.go:401-450` (`GetJobResults`), `:577-605` (`GetUserResults`), `:251-330` (`GetUserJobs`)
- Test: `web/handlers/api_test.go`

The current code does `offset := (page - 1) * limit` with no overflow check. With `limit=1000` and `page=2147483`, the multiplication overflows int32. Also, the `limit ≤ 1000` cap on results endpoints is inconsistent with the `limit ≤ 100` cap on job lists. Unify at 100.

- [ ] **Step 1: Write the failing test**

```go
func TestGetJobResults_RejectsOverflowPage(t *testing.T) {
    h := newTestHandler(t)
    req := httptest.NewRequest("GET", "/api/v1/jobs/abc/results?page=2147483647&limit=100", nil)
    req = mux.SetURLVars(req, map[string]string{"id": validJobUUID})
    req = req.WithContext(authCtx("user-1"))
    rr := httptest.NewRecorder()
    h.GetJobResults(rr, req)
    require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetJobResults_LimitCappedAt100(t *testing.T) {
    h := newTestHandler(t)
    req := httptest.NewRequest("GET", "/api/v1/jobs/abc/results?limit=500", nil)
    req = mux.SetURLVars(req, map[string]string{"id": validJobUUID})
    req = req.WithContext(authCtx("user-1"))
    rr := httptest.NewRecorder()
    h.GetJobResults(rr, req)
    require.Equal(t, http.StatusBadRequest, rr.Code)
}
```

- [ ] **Step 2: Run, confirm fail; implement a shared pagination helper** — `web/handlers/pagination.go`

```go
package handlers

import (
    "fmt"
    "math"
    "net/http"
    "strconv"
)

const (
    DefaultPageLimit = 50
    MaxPageLimit     = 100
)

// parsePagination parses page/limit query params with bounds checks and
// overflow-safe offset calculation. Returns (page, limit, offset, error).
// On error the caller should respond with 400 and the error message.
func parsePagination(r *http.Request, defaultLimit int) (int, int, int, error) {
    page := 1
    if v := r.URL.Query().Get("page"); v != "" {
        p, err := strconv.Atoi(v)
        if err != nil || p < 1 {
            return 0, 0, 0, fmt.Errorf("page must be a positive integer")
        }
        page = p
    }
    limit := defaultLimit
    if v := r.URL.Query().Get("limit"); v != "" {
        l, err := strconv.Atoi(v)
        if err != nil || l < 1 || l > MaxPageLimit {
            return 0, 0, 0, fmt.Errorf("limit must be between 1 and %d", MaxPageLimit)
        }
        limit = l
    }
    // Overflow guard: (page-1)*limit must fit in a positive int.
    if page > (math.MaxInt32/limit)+1 {
        return 0, 0, 0, fmt.Errorf("page out of range")
    }
    offset := (page - 1) * limit
    return page, limit, offset, nil
}
```

Replace each handler's hand-rolled pagination block with `page, limit, offset, err := parsePagination(r, 50)`.

- [ ] **Step 3: Run, confirm pass; commit**

```bash
go test ./web/handlers/...
git add web/handlers/pagination.go web/handlers/api.go web/handlers/api_test.go
git commit -m "fix(api): unify pagination caps at 100 and add overflow guard"
```

---

### Task 3.4: UUID validation in `GetJobResults`, `GetJobCosts`, `CancelJob`

**Files:**
- Modify: `web/handlers/api.go` — all handlers that read `mux.Vars(r)["id"]` and pass it to a service should validate as UUID
- Test: `web/handlers/api_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestGetJobResults_RejectsMalformedID(t *testing.T) {
    h := newTestHandler(t)
    req := httptest.NewRequest("GET", "/api/v1/jobs/not-a-uuid/results", nil)
    req = mux.SetURLVars(req, map[string]string{"id": "not-a-uuid"})
    req = req.WithContext(authCtx("user-1"))
    rr := httptest.NewRecorder()
    h.GetJobResults(rr, req)
    require.Equal(t, http.StatusBadRequest, rr.Code)
}
```

- [ ] **Step 2: Add a helper and use it**

```go
// in api.go
func parseJobID(r *http.Request) (string, error) {
    id := mux.Vars(r)["id"]
    if id == "" {
        return "", fmt.Errorf("missing job id")
    }
    if _, err := uuid.Parse(id); err != nil {
        return "", fmt.Errorf("invalid job id")
    }
    return id, nil
}
```

Replace `jobID := mux.Vars(r)["id"]` in `GetJobResults`, `GetJobCosts`, `CancelJob`, `DeleteJob`, `GetJob` with the helper.

- [ ] **Step 3: Run, commit**

```bash
go test ./web/handlers/...
git add web/handlers/api.go web/handlers/api_test.go
git commit -m "fix(api): validate UUID in all job-id path parameters"
```

---

### Task 3.5: Proxies — SSRF validation + element/length caps

**Files:**
- Modify: `models/job.go:23` — tighten the struct tag for the `Proxies` field
- Modify: `web/utils/validation.go` — add `ValidateProxyURL` per-element check that mirrors the existing `web/utils/webhook_url.go` SSRF defense
- Test: `web/utils/validation_test.go`

**Why this is real, not paranoid:** confirmed at `runner/webrunner/webrunner.go:1229` that `job.Data.Proxies` is passed directly to `scrapemateapp.WithProxies(...)`, which uses them for outbound HTTP from the scraper. An attacker can supply `http://169.254.169.254/latest/meta-data/iam/security-credentials/` (AWS instance metadata), `http://localhost:5432/`, or `http://127.0.0.1:6379/` and read the response back through the scraper's error path or cause secondary effects. We already have `ValidateWebhookURL` in `web/utils/webhook_url.go` doing the right thing for webhook subscriptions — reuse that logic.

- [ ] **Step 1: Write the failing tests**

```go
func TestValidateJobData_RejectsTooManyProxies(t *testing.T) {
    d := newValidJobData()
    d.Proxies = make([]string, 101)
    for i := range d.Proxies {
        d.Proxies[i] = "http://proxy.example.com:8080"
    }
    require.Error(t, ValidateJobData(d))
}

func TestValidateJobData_RejectsProxyToPrivateIP(t *testing.T) {
    cases := []string{
        "http://127.0.0.1:5432",
        "http://localhost:5432",
        "http://169.254.169.254/latest/meta-data/",
        "http://10.0.0.1:8080",
        "http://192.168.1.1:8080",
        "http://[::1]:8080",
    }
    for _, p := range cases {
        d := newValidJobData()
        d.Proxies = []string{p}
        require.Errorf(t, ValidateJobData(d), "expected %q to be rejected", p)
    }
}

func TestValidateJobData_RejectsProxyWithBadScheme(t *testing.T) {
    for _, p := range []string{
        "file:///etc/passwd",
        "gopher://attacker.example.com/",
        "javascript:alert(1)",
    } {
        d := newValidJobData()
        d.Proxies = []string{p}
        require.Error(t, ValidateJobData(d))
    }
}

func TestValidateJobData_AcceptsValidPublicProxies(t *testing.T) {
    d := newValidJobData()
    d.Proxies = []string{
        "http://user:pass@proxy.example.com:8080",
        "socks5://proxy.example.com:1080",
    }
    require.NoError(t, ValidateJobData(d))
}
```

- [ ] **Step 2: Update the struct tag**

```go
Proxies []string `json:"proxies" validate:"omitempty,max=100,dive,max=2048"`
```

(Length-only at the tag layer; the SSRF + scheme allowlist runs in the service-layer validator where it has access to DNS resolution.)

- [ ] **Step 3: Add `ValidateProxyURL` to `web/utils/validation.go`**

```go
const maxProxiesPerJob = 100

// allowedProxySchemes is the closed set of URL schemes the scraper accepts as
// proxies. Anything else is rejected.
var allowedProxySchemes = map[string]struct{}{
    "http":   {},
    "https":  {},
    "socks5": {},
    "socks5h":{},
}

// ValidateProxyURL parses a proxy URL string and rejects it if:
//   - the URL is malformed
//   - the scheme is not in the allowlist
//   - the host resolves to a private, loopback, link-local, or unspecified IP
//   - the host is empty
//
// Reuses the same private-IP block list as ValidateWebhookURL — see
// web/utils/webhook_url.go for the canonical implementation.
func ValidateProxyURL(raw string) error {
    if len(raw) > 2048 {
        return fmt.Errorf("proxy URL exceeds 2048 bytes")
    }
    u, err := url.Parse(raw)
    if err != nil {
        return fmt.Errorf("invalid proxy URL: %w", err)
    }
    if _, ok := allowedProxySchemes[strings.ToLower(u.Scheme)]; !ok {
        return fmt.Errorf("proxy scheme %q not allowed", u.Scheme)
    }
    host := u.Hostname()
    if host == "" {
        return fmt.Errorf("proxy URL missing host")
    }
    // Reject if any resolved IP is private/loopback/link-local. Reuse the
    // exact same predicate as ValidateWebhookURL so the two are kept in sync.
    if err := assertPublicHost(host); err != nil {
        return fmt.Errorf("proxy host %q rejected: %w", host, err)
    }
    return nil
}
```

In `ValidateJobData`, after the existing field checks, add:

```go
if len(d.Proxies) > maxProxiesPerJob {
    return fmt.Errorf("proxies exceeds maximum of %d", maxProxiesPerJob)
}
for i, p := range d.Proxies {
    if err := ValidateProxyURL(p); err != nil {
        return fmt.Errorf("proxies[%d]: %w", i, err)
    }
}
```

(`assertPublicHost` should be the predicate already used by `ValidateWebhookURL`. If it's currently inlined into the webhook validator, extract it into a shared helper as part of this task.)

- [ ] **Step 4: Run all tests; commit**

```bash
go test ./web/utils/...
git add models/job.go web/utils/validation.go web/utils/webhook_url.go web/utils/validation_test.go
git commit -m "fix(api): SSRF-validate job.proxies and cap at 100 entries"
```

**Known limitation — DNS TOCTOU.** `ValidateProxyURL` resolves the hostname at validation time. An attacker who controls a DNS record can have it return a public IP at validation and `169.254.169.254` (or another internal address) at scrape time — bypassing this check. This is a fundamental weakness of all validate-then-use SSRF defenses. The complete fix lives at the HTTP transport layer: a custom `net.Dialer.Control` or `http.Transport.DialContext` that re-validates the resolved IP immediately before the TCP connect. That layer lives inside `scrapemate` (the library that consumes `WithProxies(...)`), not in our code — we cannot fix it here without forking. **Action items:**

1. Document this limitation in the commit message and in `docs/api.md` under the `proxies` field description.
2. File an upstream issue against `scrapemate` (or whatever library owns the proxy dialer) asking for a pluggable IP-validation hook on the dialer. Link the issue from this plan so it isn't lost.
3. In the meantime, treat proxy URLs from free-tier users as untrusted: add a feature flag that disables the `proxies` field entirely for users without a paid plan or explicit whitelist. Cheap, effective, and closes the TOCTOU for the population that actually matters.

The validation-time check is still valuable — it blocks the naive case and forces the attacker into an active DNS attack rather than a passive one — so this task ships. Just don't claim it's a complete SSRF defense.

---

### Task 3.6: Per-endpoint rate limit on POST `/api/v1/jobs`

**Files:**
- Modify: `web/web.go` — wrap the `POST /api/v1/jobs` route with a tighter limiter
- Test: `web/web_routing_test.go` (or wherever route wiring is exercised)

The global `PerUserRateLimit(rate.Limit(5), 20)` middleware is already applied (`web/middleware/middleware.go:382`). For job creation specifically — billable, takes `SELECT ... FOR UPDATE`, queues a scraping worker — 5 req/s burst 20 is too lenient. Add a tighter per-endpoint limiter on top.

- [ ] **Step 1: Failing test**

```go
func TestCreateJob_PerEndpointRateLimit(t *testing.T) {
    h, _ := newTestServer(t)
    body := `{"name":"t","keywords":["pizza"],"lang":"en","depth":5,"max_results":10,"max_time":60}`

    var rejected int
    for i := 0; i < 10; i++ {
        rr := httptest.NewRecorder()
        req := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(body))
        req = req.WithContext(authCtx("user-1"))
        h.ServeHTTP(rr, req)
        if rr.Code == http.StatusTooManyRequests {
            rejected++
        }
    }
    require.GreaterOrEqual(t, rejected, 5,
        "expected the per-endpoint limiter to reject most of 10 burst requests")
}
```

- [ ] **Step 2: Wire the limiter at the route**

In `web/web.go`, locate the `POST /api/v1/jobs` route registration and wrap it:

```go
jobCreateLimiter := middleware.PerUserRateLimit(rate.Limit(1), 3)
authedAPI.Handle("/jobs", jobCreateLimiter(http.HandlerFunc(h.Scrape))).Methods("POST")
```

(Adapt to gorilla/mux's actual subrouter pattern in this codebase.)

- [ ] **Step 3: Run, commit**

```bash
go test ./web/...
git add web/web.go web/web_routing_test.go
git commit -m "fix(api): add tighter per-endpoint rate limit on POST /api/v1/jobs"
```

---

## Chunk 4: Defense-in-Depth & IDOR Hardening — ✅ COMPLETE (2026-04-09)

**Status:** All three tasks landed.

- Task 4.2 (priority — real active vulnerability) — `web/service.go` `Service.Delete` was passing `userID=""` to `repo.Get` and `repo.Cancel`, which the repo treats as an admin-bypass sentinel (`WHERE id = $1 AND (user_id = $2 OR $2 = '')`). An attacker who knew or guessed a victim's job UUID could trigger cross-tenant cancellation of the running job AND `os.Remove` the victim's CSV file from `dataFolder` — only the final `repo.Delete` call enforced ownership, by which point the destructive side effects had already executed. Replaced both empty-userID calls with `userID`-scoped equivalents and changed the swallowed `if err == nil` pattern to an early `return err` so cross-tenant requests fail fast before any side effects.
- Task 4.1 (defense-in-depth) — `web/services/results.go` `GetEnhancedJobResultsPaginated` now takes `userID` and the COUNT + SELECT both carry `AND user_id = $2`. Handler at `web/handlers/api.go` already pre-checks ownership via `App.Get` (which it must keep for the StatusFailed billing gate), so this is belt-and-suspenders against future regressions and against `results` rows ever drifting out of sync with their parent job's `user_id`.
- Task 4.3 (defense-in-depth + plan correction) — `web/services/costs.go` `GetJobCosts` now takes `userID`. **Plan correction**: the `job_cost_breakdown` table has no `user_id` column (see migration 000017), so the original `AND user_id = $2` cannot apply there. Instead the **totals** query (which hits the `jobs` table) now reads `WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, and the query order was swapped so totals runs first — the totals query now functions as the ownership gate, returning `ErrNoRows`/"job not found" before any breakdown rows are read. Added `errors.Is(err, sql.ErrNoRows)` mapping for clean not-found handling.

**Test coverage added:** `web/service_test.go` introduces `fakeJobRepo` (a tiny stub via `models.JobRepository` interface embedding — only the three methods `Service.Delete` calls are implemented, anything else panics) and two regression tests:
- `TestService_Delete_RejectsCrossTenantAccess` — proves the IDOR is closed: a cross-tenant DELETE returns the not-found error, never invokes `Cancel`, never invokes `repo.Delete`, and leaves the victim's seeded CSV file untouched on disk.
- `TestService_Delete_OwnerHappyPath` — proves the owner can still delete their own running job, with `Cancel` and `repo.Delete` both called exactly once with the owner's userID and the CSV removed.

All existing handler ownership tests (`TestGetJob_*`, `TestGetJobResults_*`, `TestGetJobCosts_*`, `TestDeleteJob_*`, `TestCancelJob_*`) continue to pass against the updated service signatures. The pre-existing `TestCreateWebhook_HTTPRejected` failure on HEAD `7a6e0bc` is unrelated to this chunk.

### Task 4.1: Scope results query by user_id

**Files:**
- Modify: `web/services/results.go:163-216` — `GetEnhancedJobResultsPaginated` signature and query
- Modify: `web/handlers/api.go:443` — pass userID into the call
- Test: `web/services/results_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestGetEnhancedJobResultsPaginated_OnlyReturnsCallerResults(t *testing.T) {
    db := newTestDB(t)
    // seed two jobs for two different users with results each
    seedJobAndResults(t, db, "job-A", "user-A", 3)
    seedJobAndResults(t, db, "job-B", "user-B", 3)
    svc := NewResultsService(db, slog.Default())

    // user A asking for job B (which they don't own) returns no rows
    results, total, err := svc.GetEnhancedJobResultsPaginated(ctx, "job-B", "user-A", 100, 0)
    require.NoError(t, err)
    require.Equal(t, 0, total)
    require.Empty(t, results)

    // user B asking for their own job returns the seeded rows
    results, total, err = svc.GetEnhancedJobResultsPaginated(ctx, "job-B", "user-B", 100, 0)
    require.NoError(t, err)
    require.Equal(t, 3, total)
    require.Len(t, results, 3)
}
```

- [ ] **Step 2: Run, confirm fail**

- [ ] **Step 3: Update the service signature and queries**

```go
func (s *ResultsService) GetEnhancedJobResultsPaginated(
    ctx context.Context, jobID, userID string, limit, offset int,
) ([]models.EnhancedResult, int, error) {
    if s.db == nil {
        return nil, 0, fmt.Errorf("database not available")
    }
    if userID == "" {
        return nil, 0, fmt.Errorf("user id required")
    }
    const countQ = `SELECT COUNT(1) FROM results WHERE job_id = $1 AND user_id = $2`
    var total int
    if err := s.db.QueryRowContext(ctx, countQ, jobID, userID).Scan(&total); err != nil {
        return nil, 0, fmt.Errorf("failed to count results: %w", err)
    }
    // ... rest of query body, change WHERE to:
    // WHERE job_id = $1 AND user_id = $2
    // ORDER BY created_at DESC
    // LIMIT $3 OFFSET $4
    // and pass jobID, userID, limit, offset
}
```

Update the call site at `web/handlers/api.go:443`:

```go
results, total, err := h.Deps.ResultsSvc.GetEnhancedJobResultsPaginated(
    r.Context(), jobID, userID, limit, offset)
```

- [ ] **Step 4: Run, confirm pass; commit**

```bash
go test ./web/services/...
git add web/services/results.go web/services/results_test.go web/handlers/api.go
git commit -m "fix(results): scope GetEnhancedJobResultsPaginated by user_id"
```

---

### Task 4.2: Fix `Service.Delete` ownership for status read

**Files:**
- Modify: `web/service.go:55-88`
- Test: `web/service_test.go`

- [ ] **Step 1: Write the failing test**

Behavior test, no spy. Assert that a cross-tenant `Delete` returns `ErrNotFound` (or whatever the not-found sentinel is) and that the row is unchanged afterwards. The first draft had a contradictory `require.Empty` immediately followed by `require.Equal "user-B"` on the same field — deleted.

```go
func TestService_Delete_RejectsCrossTenantAccess(t *testing.T) {
    svc, repo := newTestService(t)
    repo.seed(Job{ID: "job-A", UserID: "user-A", Status: StatusWorking})

    // user-B tries to delete user-A's job
    err := svc.Delete(context.Background(), "job-A", "user-B")
    require.ErrorIs(t, err, ErrNotFound)

    // The job must still be there, untouched.
    job, err := repo.Get(context.Background(), "job-A", "user-A")
    require.NoError(t, err)
    require.Equal(t, "job-A", job.ID)
    require.Equal(t, StatusWorking, job.Status)
}
```

- [ ] **Step 2: Run, confirm fail**

- [ ] **Step 3: Replace the empty-userID Get/Cancel calls with userID-scoped ones**

```go
func (s *Service) Delete(ctx context.Context, id string, userID string) error {
    if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
        return fmt.Errorf("invalid file name")
    }
    log := pkglogger.FromContext(ctx)

    // Ownership-scoped status check. If the job doesn't belong to userID this
    // returns ErrNotFound and we abort early — no information leak about other
    // tenants' jobs.
    job, err := s.repo.Get(ctx, id, userID)
    if err != nil {
        return err
    }
    if job.Status == StatusWorking || job.Status == StatusPending {
        if cancelErr := s.repo.Cancel(ctx, id, userID); cancelErr != nil {
            log.Warn("cancel_before_delete_failed",
                slog.String("job_id", id), slog.Any("error", cancelErr))
        }
    }
    // ... rest unchanged ...
    return s.repo.Delete(ctx, id, userID)
}
```

- [ ] **Step 4: Run, commit**

```bash
go test ./web/...
git add web/service.go web/service_test.go
git commit -m "fix(jobs): scope status check in Service.Delete by userID"
```

---

### Task 4.3: Scope `GetJobCosts` query by `user_id`

**Files:**
- Modify: `web/handlers/api.go:454-495` and the cost service signature

This is **not** a TOCTOU fix (the first draft mislabeled it). Job ownership doesn't mutate, so there's no time-of-check-vs-time-of-use window. This is a **defense-in-depth and consistency** change: every result-bearing query in the API must be scoped by `user_id` after the §4 policy change. Pass userID into `cs.GetJobCosts(ctx, jobID, userID)` and add `AND user_id = $2` to the underlying query.

- [ ] **Step 1: Write the failing test**

```go
func TestGetJobCosts_OnlyReturnsOwnersCosts(t *testing.T) {
    svc := newTestCostService(t)
    seedJobCosts(t, svc, "job-A", "user-A")

    _, err := svc.GetJobCosts(context.Background(), "job-A", "user-B")
    require.ErrorIs(t, err, ErrNotFound)

    costs, err := svc.GetJobCosts(context.Background(), "job-A", "user-A")
    require.NoError(t, err)
    require.NotNil(t, costs)
}
```

- [ ] **Step 2: Implement, run, commit**

```bash
git add web/handlers/api.go web/services/costs.go web/services/costs_test.go
git commit -m "fix(costs): scope GetJobCosts query by user_id (defense-in-depth)"
```

---

## Chunk 5: OpenAPI Spec & Public Documentation

### Task 5.1: Update the OpenAPI spec to reflect the cap convention

**Files:**
- Modify: `docs/api.md` (or wherever the OpenAPI YAML/JSON lives — find via `grep -r 'openapi: 3' docs/`)

- [ ] **Step 1: Locate the spec file and read its current `JobData` schema definition**

- [ ] **Step 2: Replace the `JobData` schema with the new cap fields**

For every cap field, set `minimum`, `maximum`, `default`, and a `description` that names the billing unit and the **scope** (per-place, per-job, or per-job-total). The two non-uniform-scope fields are `reviews_max` (per-place) and `images_max` (per-job total) — both descriptions must call this out explicitly so API consumers don't assume the default.

```yaml
reviews_max:
  type: integer
  format: int32
  minimum: 0
  maximum: 500
  default: 10
  description: |
    Maximum number of reviews to scrape PER PLACE. Each review counts toward
    billing. Hard ceiling is 500 reviews/place — values above this are
    rejected with HTTP 400. Set to 0 to skip review scraping entirely.

    Note: this cap is per-place, not per-job. A job that scrapes 100 places
    with reviews_max=500 can produce up to 50,000 reviews total. Use
    max_results to bound the total.

images_max:
  type: integer
  format: int32
  minimum: 0
  maximum: 20000
  default: 0
  description: |
    Maximum TOTAL number of images to scrape across ALL places in the job.
    NOT per-place. The runner stops scraping additional images (but
    continues scraping place metadata) once this budget is reached.

    Image events are billed per image. The hard ceiling of 20000 is the
    safety net that prevents a high-place job (max_results=500) from
    consuming runaway image credits. Real-world reference: a typical
    100-place job at depth=20 produces ~8000 images at the natural Google
    Maps density (~80 images/place average), so the 20000 cap kicks in
    around 250 places-worth of imagery.

    Set to 0 (the default) to skip all image scraping. This is the
    billing-safe default — image events are typically the largest cost
    line item per job.

    UNUSUAL SCOPE: this is the only cap field with a per-job-total scope
    rather than per-place or per-job. The choice is deliberate because
    image counts on Google Maps are unbounded per-business and a per-place
    cap would not bound total billing.
```

Repeat for `max_results`, `depth`, `radius`, `max_time` (all per-job, conventional scope). Add a section at the top of the spec titled **"Cap parameter convention"** that links to this plan and explains the rule once — including the scope-column distinction (per-place vs per-job vs per-job-total) so future API additions are consistent.

- [ ] **Step 3: Verify ReDoc renders the new spec without warnings**

```bash
# Whatever local command is used to validate the spec — for example:
npx @redocly/cli lint docs/api.md
```

- [ ] **Step 4: Commit**

```bash
git add docs/api.md
git commit -m "docs(api): document unified cap-parameter convention in OpenAPI spec"
```

---

### Task 5.2: Validation error messages use JSON tag names

**Files:**
- Modify: `web/handlers/validation.go:12-40`
- Test: `web/handlers/validation_test.go`

- [ ] **Step 1: Failing test**

```go
func TestFormatValidationErrors_UsesJSONTagNames(t *testing.T) {
    type req struct {
        ReviewsMax int `json:"reviews_max" validate:"min=0,max=500"`
    }
    err := validator.New().Struct(req{ReviewsMax: 9999})
    msg := formatValidationErrors(err)
    require.Contains(t, msg, "reviews_max")
    require.NotContains(t, msg, "ReviewsMax")
}
```

- [ ] **Step 2: Implement using the validator's `RegisterTagNameFunc`**

In the validator construction (probably in `web/handlers/handlers.go` or the deps wiring):

```go
v := validator.New()
v.RegisterTagNameFunc(func(fld reflect.StructField) string {
    name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
    if name == "-" {
        return ""
    }
    return name
})
```

Then in `formatValidationErrors`, use `fe.Field()` directly (which now returns the JSON name) instead of lowercasing the struct field.

- [ ] **Step 3: Run, commit**

```bash
go test ./web/handlers/...
git add web/handlers/validation.go web/handlers/validation_test.go web/handlers/handlers.go
git commit -m "fix(api): use JSON tag names in validation error messages"
```

---

## Chunk 6: P2 Operational Hardening (post-launch acceptable)

These can ship in a separate PR within the launch week. They are documented here so they don't get lost.

### Task 6.2: Admin credit grant endpoint

Implement `POST /api/v1/admin/credits/{userID}/grant` with:
- `requireAdminSession` middleware
- Body: `{credits: int (1..10000), reason: string (1..500), reference_id: string?}`
- Inserts a `credit_transactions` row with `type='adjustment'`, `metadata = {"granted_by": adminID, "reason": ..., "reference_id": ...}`
- Logs at WARN: `admin_credit_grant` with admin ID, target user ID, amount, reason
- Per-admin rate limit: max 5 grants/user/24h, max 100 grants/admin/24h (DB CHECK or app-level)

### Task 6.3: Drop unused dev defaults to fail-closed

Audit `runner/webrunner/webrunner.go` for any other env vars whose absence in production should be fatal (`DSN`, `CLERK_SECRET_KEY`, etc.). Roll them into `validateProductionSecrets`.

### Task 6.4: Handler-level sort allowlist (defense-in-depth)

(Was H-7 / P1 in the original draft; downgraded to P2 because the repo layer is the authoritative defense and already enforces an allowlist with safe-default fallback at `postgres/repository.go:174-184`.)

**Files:**
- Modify: `web/handlers/api.go` — `GetUserJobs`
- Test: `web/handlers/api_test.go`

Already covered as a side effect of Task 3.2 (which adds `allowedJobSorts` at the handler boundary). If Task 3.2 already shipped this, **delete this task entirely** during execution. Listed here only so the audit-trail to H-7 is preserved.

---

## Chunk 7: Job Creation Idempotency

For a billable API, network retries on `POST /api/v1/jobs` can double-charge a user. Stripe-style `Idempotency-Key` header support. **P1**, ships before launch.

### Task 7.1: Schema for the idempotency table

**Files:**
- Create: `scripts/migrations/<NNN>_add_idempotency_keys.up.sql` (and `.down.sql`)

- [ ] **Step 1: Write the up migration**

```sql
CREATE TABLE IF NOT EXISTS idempotency_keys (
    -- Composite key: a key is scoped to a single user, so two users using the
    -- same key string don't collide. UUIDv7 from Go for the row ID.
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL,
    key           TEXT NOT NULL,
    method        TEXT NOT NULL,
    path          TEXT NOT NULL,
    request_hash  TEXT NOT NULL, -- sha256 of the canonical request body

    -- Two-phase lifecycle: row is inserted with status='started' BEFORE the
    -- handler runs (reserves the key atomically via the UNIQUE constraint),
    -- then updated to status='completed' with the captured response AFTER
    -- the handler returns. See Task 7.2 for the middleware flow and why
    -- this is necessary to close the concurrent-replay race.
    status        TEXT NOT NULL CHECK (status IN ('started', 'completed')),
    status_code   INTEGER,       -- populated when status='completed'
    response_body BYTEA,         -- populated when status='completed'

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ NOT NULL,

    UNIQUE (user_id, key)
);

CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);
```

The `expires_at` column drives a periodic cleanup job (24 h TTL is the Stripe default; use the same). A stuck `started` row (process crashed mid-handler) is reaped by the same cleanup job after a shorter grace period (e.g. 15 minutes) so a crashed request doesn't permanently block its key.

- [ ] **Step 2: Write the down migration; commit**

```bash
git add scripts/migrations/<NNN>_add_idempotency_keys.*.sql
git commit -m "feat(idempotency): schema for idempotency_keys table"
```

### Task 7.2: Idempotency middleware

**Files:**
- Create: `web/middleware/idempotency.go`
- Create: `web/middleware/idempotency_test.go`
- Modify: `web/web.go` — apply the middleware to `POST /api/v1/jobs`

**Design — the concurrency trap we must avoid.** A naive implementation (query for existing row → if none, run handler → insert row) is **broken under concurrent retries**. Two retries arrive at the same time, both query an empty state, both run the handler, both try to insert. The second insert hits the unique constraint and errors out, but by then the inner handler has already run twice — the user has been double-charged.

The fix is the **Stripe two-phase pattern**: insert a row with `status='started'` **before** running the handler, using `INSERT ... ON CONFLICT DO NOTHING` as an atomic reservation. The unique `(user_id, key)` constraint is the lock. Only the request that successfully inserts the row owns the key and is allowed to run the handler. Concurrent retries hit the conflict path, check whether they match (same body hash), and either return the cached response (if the first request has completed) or return 409 `idempotency_key_in_use` (if still in flight). When the handler returns, the row is updated to `status='completed'` with the captured response. See [Stripe's implementation note](https://stripe.com/blog/idempotency) for the reference design.

- [ ] **Step 1: Failing tests — including the concurrent-replay case**

```go
func TestIdempotency_ReplayReturnsCachedResponse(t *testing.T) {
    var calls int32
    inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&calls, 1)
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte(`{"id":"job-1"}`))
    })
    mw := Idempotency(testRepo(t))(inner)

    body := `{"name":"t"}`
    do := func() *httptest.ResponseRecorder {
        rr := httptest.NewRecorder()
        req := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(body))
        req.Header.Set("Idempotency-Key", "abc-123")
        req = req.WithContext(authCtx("user-1"))
        mw.ServeHTTP(rr, req)
        return rr
    }

    first := do()
    second := do()

    require.Equal(t, http.StatusOK, first.Code)
    require.Equal(t, http.StatusOK, second.Code)
    require.Equal(t, first.Body.String(), second.Body.String())
    require.Equal(t, int32(1), atomic.LoadInt32(&calls), "inner handler must run exactly once")
}

func TestIdempotency_DifferentBodySameKeyRejected(t *testing.T) {
    inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK); _, _ = w.Write([]byte(`ok`))
    })
    mw := Idempotency(testRepo(t))(inner)

    do := func(body string) *httptest.ResponseRecorder {
        rr := httptest.NewRecorder()
        req := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(body))
        req.Header.Set("Idempotency-Key", "abc-123")
        req = req.WithContext(authCtx("user-1"))
        mw.ServeHTTP(rr, req)
        return rr
    }

    do(`{"name":"a"}`)
    second := do(`{"name":"b"}`)
    require.Equal(t, http.StatusConflict, second.Code,
        "same key with different body must 409")
}

func TestIdempotency_NoKeyHeaderPassesThrough(t *testing.T) {
    var called bool
    inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
    mw := Idempotency(testRepo(t))(inner)

    rr := httptest.NewRecorder()
    req := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(`{}`))
    req = req.WithContext(authCtx("user-1"))
    mw.ServeHTTP(rr, req)

    require.True(t, called)
}

// This is the critical test — it must pass or the whole design is broken.
// Fires N concurrent requests with the same key and body; asserts that the
// inner handler runs exactly once and that every caller gets the same response
// body. Any implementation that doesn't reserve the key BEFORE running the
// handler will fail this test.
func TestIdempotency_ConcurrentRequests_HandlerRunsExactlyOnce(t *testing.T) {
    var calls int32
    inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        atomic.AddInt32(&calls, 1)
        // Simulate handler work so concurrent requests overlap.
        time.Sleep(50 * time.Millisecond)
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte(`{"id":"job-1"}`))
    })
    mw := Idempotency(testRepo(t))(inner)

    const N = 20
    var wg sync.WaitGroup
    bodies := make([]string, N)
    statuses := make([]int, N)

    for i := 0; i < N; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            rr := httptest.NewRecorder()
            req := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(`{"name":"t"}`))
            req.Header.Set("Idempotency-Key", "concurrent-key")
            req = req.WithContext(authCtx("user-1"))
            mw.ServeHTTP(rr, req)
            bodies[i] = rr.Body.String()
            statuses[i] = rr.Code
        }(i)
    }
    wg.Wait()

    require.Equal(t, int32(1), atomic.LoadInt32(&calls),
        "inner handler must run EXACTLY once across %d concurrent requests", N)

    // Every caller must get either the cached 200 response OR a 409
    // idempotency_key_in_use (if they arrived while the first was still
    // running). No other status codes are acceptable.
    var okCount, conflictCount int
    for i, code := range statuses {
        switch code {
        case http.StatusOK:
            require.Equal(t, `{"id":"job-1"}`, bodies[i])
            okCount++
        case http.StatusConflict:
            conflictCount++
        default:
            t.Fatalf("request %d: unexpected status %d body=%q", i, code, bodies[i])
        }
    }
    require.GreaterOrEqual(t, okCount, 1, "at least one request must get the OK response")
    require.Equal(t, N, okCount+conflictCount)
}
```

- [ ] **Step 2: Implement the middleware — Stripe two-phase pattern**

```go
package middleware

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "io"
    "net/http"
    "strings"
    "time"

    "github.com/google/uuid"
    "github.com/gosom/google-maps-scraper/web/auth"
)

const (
    IdempotencyHeader = "Idempotency-Key"
    idempotencyTTL    = 24 * time.Hour
    maxBodyForHash    = 1 << 20 // mirror the MaxBodySize middleware
    maxKeyLen         = 255
)

// IdempotencyRecord mirrors the idempotency_keys table. StatusCode/ResponseBody
// are zero-valued while Status == "started".
type IdempotencyRecord struct {
    ID           string
    UserID       string
    Key          string
    Method       string
    Path         string
    RequestHash  string
    Status       string // "started" | "completed"
    StatusCode   int
    ResponseBody []byte
    CreatedAt    time.Time
    CompletedAt  *time.Time
    ExpiresAt    time.Time
}

// ErrIdempotencyConflict is returned by InsertStarted when another request
// already owns the key. The caller must then fetch the existing row to decide
// whether to replay the cached response or return 409 in-progress.
var ErrIdempotencyConflict = errors.New("idempotency key already in use")

type IdempotencyRepo interface {
    // InsertStarted atomically inserts a row with status='started' using
    // INSERT ... ON CONFLICT DO NOTHING. Returns ErrIdempotencyConflict when
    // the unique (user_id, key) constraint rejects the insert.
    InsertStarted(ctx context.Context, rec IdempotencyRecord) error

    // Get returns the existing row for (user_id, key), or (nil, nil) if none.
    Get(ctx context.Context, userID, key string) (*IdempotencyRecord, error)

    // Complete updates an existing 'started' row to 'completed' with the
    // captured response. Called from the happy path after the inner handler
    // returns.
    Complete(ctx context.Context, id string, statusCode int, body []byte) error
}

// responseCapture is an http.ResponseWriter that mirrors every write to an
// inner buffer so the middleware can persist the exact bytes the client saw.
// Headers written via WriteHeader are captured in status; the Content-Type
// and any other headers set on the real ResponseWriter are preserved because
// we delegate Header() and Write() through to it.
type responseCapture struct {
    http.ResponseWriter
    status      int
    wroteHeader bool
    buf         bytes.Buffer
}

func (r *responseCapture) WriteHeader(code int) {
    if r.wroteHeader {
        return
    }
    r.status = code
    r.wroteHeader = true
    r.ResponseWriter.WriteHeader(code)
}

func (r *responseCapture) Write(p []byte) (int, error) {
    if !r.wroteHeader {
        r.WriteHeader(http.StatusOK)
    }
    r.buf.Write(p) // best-effort capture; ignore short-write from the copy
    return r.ResponseWriter.Write(p)
}

func Idempotency(repo IdempotencyRepo) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            key := strings.TrimSpace(r.Header.Get(IdempotencyHeader))
            if key == "" {
                next.ServeHTTP(w, r)
                return
            }
            if len(key) > maxKeyLen {
                http.Error(w, "Idempotency-Key too long", http.StatusBadRequest)
                return
            }
            userID, err := auth.GetUserID(r.Context())
            if err != nil {
                // No authenticated user — idempotency is moot; fall through.
                next.ServeHTTP(w, r)
                return
            }

            // Buffer and hash the body so we can compare on replay and so we
            // can hand a fresh reader to the inner handler.
            body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyForHash+1))
            if err != nil || len(body) > maxBodyForHash {
                // Oversize or unreadable body: fall through to the inner
                // handler unidempotent. MaxBodySize middleware will reject it.
                next.ServeHTTP(w, r)
                return
            }
            r.Body = io.NopCloser(bytes.NewReader(body))
            sum := sha256.Sum256(body)
            hash := hex.EncodeToString(sum[:])

            // Phase 1 — atomic reservation.
            rec := IdempotencyRecord{
                ID:          uuid.Must(uuid.NewV7()).String(),
                UserID:      userID,
                Key:         key,
                Method:      r.Method,
                Path:        r.URL.Path,
                RequestHash: hash,
                Status:      "started",
                CreatedAt:   time.Now().UTC(),
                ExpiresAt:   time.Now().UTC().Add(idempotencyTTL),
            }
            err = repo.InsertStarted(r.Context(), rec)
            if err == nil {
                // We own the key. Run the handler, capture the response,
                // then mark it completed.
                rw := &responseCapture{ResponseWriter: w, status: http.StatusOK}
                next.ServeHTTP(rw, r)
                _ = repo.Complete(r.Context(), rec.ID, rw.status, rw.buf.Bytes())
                return
            }
            if !errors.Is(err, ErrIdempotencyConflict) {
                // Real DB error. Fall through to the handler to avoid hard-failing
                // legitimate traffic on a transient repo outage.
                next.ServeHTTP(w, r)
                return
            }

            // Phase 2 — conflict path. Someone else owns the key. Fetch the
            // existing row and decide what to return.
            existing, err := repo.Get(r.Context(), userID, key)
            if err != nil || existing == nil {
                // Shouldn't happen (we just got a conflict) but be defensive.
                http.Error(w, "idempotency lookup failed", http.StatusInternalServerError)
                return
            }
            if existing.RequestHash != hash {
                // Same key, different body — programming error on the client.
                http.Error(w, "idempotency_key_in_use_with_different_body", http.StatusConflict)
                return
            }
            if existing.Status == "started" {
                // First request is still in flight. Tell the client to retry
                // after the in-flight request completes. Stripe's behavior.
                w.Header().Set("Retry-After", "1")
                http.Error(w, "idempotency_key_in_use", http.StatusConflict)
                return
            }
            // Completed — replay the cached response.
            w.WriteHeader(existing.StatusCode)
            _, _ = w.Write(existing.ResponseBody)
        })
    }
}
```

Important correctness notes for the engineer implementing this:

1. **Do not use `context.Background()` for the `Complete` call** — use `r.Context()`. If the client disconnects mid-response the context cancels and we don't complete the row; the cleanup job will reap the stuck `started` row after the grace period. That's safer than completing in a detached context and desynchronizing DB state from what the client received.
2. **`responseCapture.buf.Write` is best-effort** — if the buffer runs out of memory we still want the real response to go to the client. The buf is sized by body writes and is naturally bounded by handler response sizes.
3. **The handler can still call `WriteHeader(500)`** — the capture records whatever the handler actually wrote, including errors. A replayed error response is still idempotent (the client learns the request was tried and failed consistently).
4. **Handler panics** — if the inner handler panics, `Complete` is never called and the row stays in `started` until the cleanup job reaps it. The recovery middleware will send a 500 to the client. The next retry will hit the conflict path, see `started`, and get a 409; after the grace period expires, the cleanup removes the row and a retry can start fresh. Document this trade-off: a panicked request cannot be replayed, only retried.

- [ ] **Step 3: Write the postgres repo**

```go
// InsertStarted uses ON CONFLICT DO NOTHING to atomically reserve the key.
// A row-count of 0 means the unique constraint rejected the insert — return
// ErrIdempotencyConflict so the caller knows to look up the existing row.
func (r *IdempotencyRepo) InsertStarted(ctx context.Context, rec middleware.IdempotencyRecord) error {
    const q = `
        INSERT INTO idempotency_keys
            (id, user_id, key, method, path, request_hash, status, created_at, expires_at)
        VALUES ($1, $2, $3, $4, $5, $6, 'started', $7, $8)
        ON CONFLICT (user_id, key) DO NOTHING`
    res, err := r.db.ExecContext(ctx, q,
        rec.ID, rec.UserID, rec.Key, rec.Method, rec.Path, rec.RequestHash, rec.CreatedAt, rec.ExpiresAt)
    if err != nil {
        return err
    }
    n, _ := res.RowsAffected()
    if n == 0 {
        return middleware.ErrIdempotencyConflict
    }
    return nil
}
```

`Complete` is a straight UPDATE SET status='completed', status_code=$1, response_body=$2, completed_at=NOW() WHERE id=$3. `Get` is a straight SELECT.

- [ ] **Step 4: Run all tests; commit**

```bash
go test ./web/middleware/... ./postgres/... -race
git add web/middleware/idempotency.go web/middleware/idempotency_test.go \
        postgres/idempotency.go postgres/idempotency_test.go web/web.go
git commit -m "feat(api): Idempotency-Key on POST /api/v1/jobs (Stripe two-phase pattern)"
```

- [ ] **Step 5: Add the cleanup job**

A background job (cron or a `time.Ticker` goroutine in the web server) deletes expired rows. Two grace periods:

```sql
-- Completed rows: drop after TTL
DELETE FROM idempotency_keys WHERE status = 'completed' AND expires_at < NOW();

-- Stuck started rows (crashed requests): drop after 15 min
DELETE FROM idempotency_keys WHERE status = 'started' AND created_at < NOW() - INTERVAL '15 minutes';
```

Run every 5 minutes. Log the deletion counts. Commit separately.

```bash
git commit -m "feat(api): cleanup job for expired idempotency rows"
```

### Task 7.3: Document the header in the OpenAPI spec

Add a `parameters:` entry under `POST /api/v1/jobs` referencing the `Idempotency-Key` header. Document: 24 h TTL, max length 255 bytes, same key with different body returns 409.

Reuse text from the Stripe docs as the model — they got the wording right.

---

## Chunk 8: RFC 7807 Error Envelope

Define one structured error shape for the entire API and use it everywhere. Clients need to parse errors programmatically — string-matching `"exceeds maximum"` is not an API contract. **P1**, ships before launch.

### Task 8.1: Define the envelope type

**Files:**
- Create: `web/handlers/problem.go`
- Create: `web/handlers/problem_test.go`

- [ ] **Step 1: Failing test**

```go
func TestProblem_JSONShapeMatchesRFC7807(t *testing.T) {
    p := Problem{
        Type:   "https://api.brezel.ai/errors/validation",
        Title:  "Validation failed",
        Status: 400,
        Detail: "reviews_max exceeds maximum of 500",
        Code:   "VALIDATION_ERROR",
        Field:  "reviews_max",
    }
    body, _ := json.Marshal(p)
    require.JSONEq(t, `{
        "type":   "https://api.brezel.ai/errors/validation",
        "title":  "Validation failed",
        "status": 400,
        "detail": "reviews_max exceeds maximum of 500",
        "code":   "VALIDATION_ERROR",
        "field":  "reviews_max"
    }`, string(body))
}

func TestWriteProblem_SetsContentTypeAndStatus(t *testing.T) {
    rr := httptest.NewRecorder()
    WriteProblem(rr, http.StatusBadRequest, "VALIDATION_ERROR", "reviews_max", "exceeds maximum of 500")
    require.Equal(t, http.StatusBadRequest, rr.Code)
    require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
}
```

- [ ] **Step 2: Implement**

```go
package handlers

import (
    "encoding/json"
    "net/http"
)

// Problem is the canonical error envelope for the public API. It follows
// RFC 7807 (Problem Details for HTTP APIs) with two extension fields:
//   - "code": a machine-stable error code clients can switch on
//   - "field": for VALIDATION_ERROR, the JSON field that failed
//
// Title is a short, human-readable, type-stable summary. Detail is the
// per-instance human-readable explanation. Code is what clients should
// program against — never parse Detail.
type Problem struct {
    Type   string `json:"type,omitempty"`
    Title  string `json:"title"`
    Status int    `json:"status"`
    Detail string `json:"detail,omitempty"`
    Code   string `json:"code"`
    Field  string `json:"field,omitempty"`
}

// Stable error codes — exhaustive list. Adding a new code is an API change.
const (
    CodeValidationError    = "VALIDATION_ERROR"
    CodeUnauthorized       = "UNAUTHORIZED"
    CodeForbidden          = "FORBIDDEN"
    CodeNotFound           = "NOT_FOUND"
    CodeConflict           = "CONFLICT"
    CodeRateLimited        = "RATE_LIMITED"
    CodeInsufficientCredit = "INSUFFICIENT_CREDIT"
    CodeBillingFailed      = "BILLING_FAILED"
    CodeInternalError      = "INTERNAL_ERROR"
)

// WriteProblem writes a Problem to the response in application/problem+json.
// `field` is optional; pass "" if not applicable. `detail` is the human message.
func WriteProblem(w http.ResponseWriter, status int, code, field, detail string) {
    p := Problem{
        Type:   "https://api.brezel.ai/errors/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
        Title:  problemTitle(code),
        Status: status,
        Detail: detail,
        Code:   code,
        Field:  field,
    }
    w.Header().Set("Content-Type", "application/problem+json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(p)
}

func problemTitle(code string) string {
    switch code {
    case CodeValidationError:    return "Validation failed"
    case CodeUnauthorized:       return "Unauthorized"
    case CodeForbidden:          return "Forbidden"
    case CodeNotFound:           return "Not found"
    case CodeConflict:           return "Conflict"
    case CodeRateLimited:        return "Rate limit exceeded"
    case CodeInsufficientCredit: return "Insufficient credit"
    case CodeBillingFailed:      return "Billing failed"
    default:                     return "Internal error"
    }
}
```

- [ ] **Step 3: Run, commit**

```bash
go test ./web/handlers/ -run TestProblem
git add web/handlers/problem.go web/handlers/problem_test.go
git commit -m "feat(api): RFC 7807 problem-details error envelope"
```

### Task 8.2: Typed `ProblemError` and migration of every error site

**Files:**
- Modify: `web/handlers/problem.go` — add the `ProblemError` type
- Modify: every handler under `web/handlers/` that constructs an error response
- Modify: services under `web/services/`, `billing/`, `web/service.go` — return `ProblemError` for user-facing failures

**Why this isn't mechanical.** The first draft described this task as "mechanical search-and-replace." It isn't. Some error sites are in handlers (easy: replace `renderJSON` with `WriteProblem`), but many originate deep in services — `billing.ChargeJob` returning "insufficient credit", `estimation.CheckSufficientBalance` returning a similar error, repo layers returning "not found". Today those bubble up as bare `error` values and handlers string-match on the message to decide the HTTP status. That is exactly the anti-pattern we're trying to eliminate for clients — and we shouldn't tolerate it inside our own code either.

**The fix: a typed error that services return and handlers unwrap.** Services never call HTTP code. Handlers never string-match. A small `ProblemError` type implementing `error` carries the code/field/detail, flows through `fmt.Errorf("%w", ...)` wrapping, and is unwrapped at the handler boundary via `errors.As`.

- [ ] **Step 1: Extend `web/handlers/problem.go` with `ProblemError`**

```go
// ProblemError is an error type that carries a Problem envelope through the
// service → handler boundary. Services return these instead of bare
// errors.New(...) so handlers can unwrap them with errors.As and write a
// structured response without string-matching.
//
// Example:
//
//     // in a service:
//     if balance < cost {
//         return handlers.NewProblemError(http.StatusPaymentRequired,
//             handlers.CodeInsufficientCredit, "credits",
//             fmt.Sprintf("balance %.2f is less than cost %.2f", balance, cost))
//     }
//
//     // in the handler:
//     if err := svc.DoThing(ctx); err != nil {
//         var pe *handlers.ProblemError
//         if errors.As(err, &pe) {
//             pe.Write(w)
//             return
//         }
//         internalError(w, logger, err, ...)
//     }
type ProblemError struct {
    Status int
    Code   string
    Field  string
    Detail string
    // Wrapped is the underlying error, if any, for observability. NEVER sent
    // to the client — only logged server-side.
    Wrapped error
}

func (e *ProblemError) Error() string {
    if e.Field != "" {
        return fmt.Sprintf("%s: %s (field=%s)", e.Code, e.Detail, e.Field)
    }
    return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

func (e *ProblemError) Unwrap() error { return e.Wrapped }

// Write renders the ProblemError as a Problem response. Safe to call from any
// handler; delegates to WriteProblem.
func (e *ProblemError) Write(w http.ResponseWriter) {
    WriteProblem(w, e.Status, e.Code, e.Field, e.Detail)
}

// NewProblemError is the canonical constructor. Wrapped is optional.
func NewProblemError(status int, code, field, detail string) *ProblemError {
    return &ProblemError{Status: status, Code: code, Field: field, Detail: detail}
}

// WrapAsProblem attaches a wrapped error for logging without changing the
// client-facing response. Use when the underlying cause is useful in logs
// (a DB error behind a 500, for example) but shouldn't leak to the client.
func WrapAsProblem(err error, status int, code, field, detail string) *ProblemError {
    return &ProblemError{Status: status, Code: code, Field: field, Detail: detail, Wrapped: err}
}

// handleError is the canonical error-handling helper at the handler boundary.
// If err is a *ProblemError (including wrapped), write the Problem response.
// Otherwise log at error level and write a generic 500 Problem.
func handleError(w http.ResponseWriter, logger *slog.Logger, err error, ctx ...slog.Attr) {
    var pe *ProblemError
    if errors.As(err, &pe) {
        pe.Write(w)
        return
    }
    attrs := append([]any{slog.Any("error", err)}, slogAttrsToAny(ctx)...)
    logger.Error("unhandled_error", attrs...)
    WriteProblem(w, http.StatusInternalServerError, CodeInternalError, "",
        "an unexpected error occurred")
}
```

- [ ] **Step 2: Update `formatValidationErrors` to return a structured list**

Validation errors are special — one request can fail on multiple fields. Return a slice of `ProblemError` (or a single `ProblemError` with a `Fields []string` extension) so the client gets all failures at once, not one at a time. Decision: extend the envelope with a `fields` array for multi-field validation; keep `field` for single-field errors. Update the OpenAPI schema accordingly.

```go
type Problem struct {
    Type   string   `json:"type,omitempty"`
    Title  string   `json:"title"`
    Status int      `json:"status"`
    Detail string   `json:"detail,omitempty"`
    Code   string   `json:"code"`
    Field  string   `json:"field,omitempty"`  // single-field convenience
    Fields []string `json:"fields,omitempty"` // multi-field validation
}
```

- [ ] **Step 3: Audit every service-layer error return site**

Before touching handlers, make a list:

```bash
grep -rn 'errors\.New\|fmt\.Errorf' web/services/ billing/ web/service.go
```

For each call site, decide:
- **Is this a user-facing error?** → wrap as `ProblemError` with the right code.
- **Is this an internal/DB error?** → leave as a bare error; the handler's `handleError` will generate a 500 Problem with `CodeInternalError`.

Commit this audit as a single PR with no behavior change — just the typed errors. Handlers still use `renderJSON(models.APIError{})` at this point. One-step-at-a-time migration.

```bash
git commit -m "refactor(errors): convert user-facing service errors to ProblemError"
```

- [ ] **Step 4: Migrate handlers file-by-file**

Each handler file is a separate commit. For each file:

1. Replace every `renderJSON(w, status, models.APIError{...})` with either `WriteProblem(...)` (for inline errors) or `handleError(w, logger, err)` (after a service call that might return a `ProblemError`).
2. Update the file's test fixtures to decode into `handlers.Problem` and assert on `.Code` and `.Field`.
3. Run `go test ./web/handlers/<file>_test.go`.
4. Commit:

```bash
git add web/handlers/api.go web/handlers/api_test.go
git commit -m "refactor(api): Problem envelope + handleError in jobs handlers"
# repeat for billing.go, admin.go, apikey.go, webhook.go, integration.go, dashboard.go
```

- [ ] **Step 5: Mark `models.APIError` deprecated**

Add a `Deprecated:` godoc comment pointing at `handlers.Problem`. Do not delete it yet — test fixtures or external clients may still depend on the shape. Schedule removal for the post-launch cleanup.

- [ ] **Step 6: Grep guard — no handler may import `models.APIError` after this**

Add a CI check (or a test) that fails if any file under `web/handlers/` still references `models.APIError`. This prevents silent regressions when someone adds a new handler.

```go
func TestNoHandlerImportsAPIError(t *testing.T) {
    files, _ := filepath.Glob("*.go")
    for _, f := range files {
        if strings.HasSuffix(f, "_test.go") { continue }
        b, _ := os.ReadFile(f)
        require.NotContains(t, string(b), "models.APIError",
            "%s: use handlers.Problem / WriteProblem / handleError, not models.APIError", f)
    }
}
```

- [ ] **Step 7: Run the full suite; commit**

```bash
go test ./... -race
git commit -m "refactor(errors): enforce ProblemError-only in handlers"
```

### Task 8.3: Document the error shape in the OpenAPI spec

Add a top-level `components/schemas/Problem` referencing `application/problem+json` and update every operation's `responses` block to declare `4xx` and `5xx` returns of `Problem`. Add a "Error handling" section to the docs explaining that clients should switch on `code`, never on `detail`.

---

## Pre-launch verification checklist

After all chunks merge:

- [ ] `go test ./...` passes
- [ ] `gosec ./...` clean (or noqa-annotated for confirmed false positives)
- [ ] `govulncheck ./...` clean
- [ ] `golangci-lint run` clean
- [ ] `go test -race ./...` clean
- [ ] Manually exercise: create job with `reviews_max=9999` → 400 Problem with `code=VALIDATION_ERROR`, `field=reviews_max`
- [ ] Manually exercise: create job with `max_results=0` → 400 Problem
- [ ] Manually exercise: create job with a 500-element `proxies` array → 400 Problem
- [ ] Manually exercise: create job with `proxies=["http://169.254.169.254/"]` → 400 Problem with SSRF rejection
- [ ] Manually exercise: create job with a 100KB keyword string → 400 Problem
- [ ] Manually exercise: create checkout session with `credits=100000` → 400 Problem
- [ ] Manually exercise: GET `/api/v1/jobs/<other-user-job-id>/results` → 404 Problem
- [ ] Manually exercise: GET `/api/v1/jobs/user?sort=password` → 400 Problem
- [ ] Manually exercise: POST same job twice with the same `Idempotency-Key` → second call returns the cached response, no new job row, no second credit charge
- [ ] Manually exercise: POST same `Idempotency-Key` with different body → 409 Problem
- [ ] Manually exercise: POST 10 jobs in 1s as the same user → most get 429 from the per-endpoint limiter
- [ ] Production deploy with `APP_ENV=production` and a missing `STRIPE_WEBHOOK_SECRET` → server refuses to start
- [ ] `grep -r BRAZA_DEV_AUTH_BYPASS web/ main.go runner/` returns zero matches (dev bypass is fully removed; this guards against accidental reintroduction)
- [ ] OpenAPI spec renders in ReDoc with no schema warnings; cap-convention section visible; `Idempotency-Key` documented; Problem schema referenced from every operation's `responses`
- [ ] Frontend has been updated to stop sending `reviews_max=9999` and the `images` boolean; staging E2E green
- [ ] Stripe webhook end-to-end test in staging (real Stripe test events) including refund and idempotent replay
- [ ] Load test: 100 concurrent job-create requests for the same user → at most `DefaultMaxConcurrentJobs` succeed; the rest get 429
- [ ] Load test: 100 concurrent identical-Idempotency-Key requests → exactly one job created, all 100 return the same response body
- [ ] Spot-check: every 4xx response across the API uses `application/problem+json` and a stable `code` field
