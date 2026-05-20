# Proxy Pool Smoke Test (post-deploy validation)

This runbook validates the `proxypool.Pool` feature shipped in PR #83.
Run it on staging (or production with a small test user) immediately
after deploying the proxy-pool branch.

**Expected wall-time:** 5-10 minutes.

## Prerequisites

- Backend is deployed and serving requests
- `PROXIES` env var is populated (at least 2 entries — production has 10 Decodo URLs)
- You can submit a job via the API or frontend on behalf of a test user
- SSH access to the prod host for `curl` against the internal listener
  (`127.0.0.1:9090`), or kubectl/equivalent for whatever deploy target

## Step 1 — Pool initialized at startup

After backend startup, the logs should contain exactly one line per process:

```
proxy_pool_initialized  pool_size=N
```

Where `N` matches `wc -l` of comma-separated proxies in the `PROXIES`
env. If this log line is missing, the pool was not constructed and
`scrapeJob` falls through to the legacy `pickProxyURL` path —
proxy-health features are effectively disabled.

```bash
# On the prod host:
sudo journalctl -u brezel-backend --since="5 minutes ago" | grep proxy_pool_initialized
```

## Step 2 — Internal stats endpoint serves JSON

The pool's snapshot is exposed on the internal listener at
`/internal/proxy/stats`. From the prod host (the endpoint is bound to
`127.0.0.1:9090` and is NOT publicly accessible — see `web/web.go` for
the bind):

```bash
curl -s http://127.0.0.1:9090/internal/proxy/stats | jq
```

Expected:

```json
{
  "total_proxies": 10,
  "healthy": 10,
  "cooling": 0,
  "quarantined": 0,
  "entries": [
    {
      "host": "gate.decodo.com:10001",
      "state": "healthy",
      "consecutive_fails": 0,
      "cumulative_fails": 0,
      "total_successes": 0,
      "last_transition_at": "2026-05-20T16:30:00Z"
    },
    ...
  ]
}
```

**Credential safety check:** none of the `host` fields should contain
`@` (the userinfo separator). If any do, that's a credential leak —
abort the smoke test and file a bug.

```bash
# This must print 0:
curl -s http://127.0.0.1:9090/internal/proxy/stats | jq -r '.entries[].host' | grep -c '@'
```

## Step 3 — A small successful scrape

Submit a job via the frontend or API. Suggested params:
- Keywords: `cafe mitte berlin` (a known-good query)
- Language: `en`
- Depth: 1 (keep it fast)
- Max reviews: 5

While the job runs, watch the logs for:

```
proxy_assigned    job_id=... proxy_host=gate.decodo.com:NNNN index=N of=10
```

And after the job completes, you should see EXACTLY ONE outcome line:

```
proxy_outcome_reported  job_id=... proxy_host=gate.decodo.com:NNNN outcome=success
```

**Pass criteria:**
- `proxy_assigned` and `proxy_outcome_reported` reference the SAME `proxy_host`
- Outcome is `success` (not `failure`)
- No `proxy_lease_reported_on_panic` events
- No `proxy_pool_exhausted` events

## Step 4 — Stats reflect the activity

```bash
curl -s http://127.0.0.1:9090/internal/proxy/stats | jq '.entries[] | select(.total_successes > 0)'
```

Expected: the proxy used in Step 3 now has `total_successes >= 1` and
state `healthy`. Other proxies should be unchanged.

## Step 5 — (Optional) Induce a controlled failure

This step is OPTIONAL and only safe in staging. Skip if you're testing
on production with real user traffic.

Temporarily prepend a bogus proxy to the `PROXIES` env:

```
PROXIES="http://invalid:invalid@1.2.3.4:80,<your real Decodo URLs>"
```

Restart the backend. Submit ~5 small scrape jobs (the bad proxy will
get assigned to some of them via round-robin). Watch the logs:

```
proxy_outcome_reported   proxy_host=1.2.3.4:80 outcome=failure reason=network_err
```

After 3 such events, the bad proxy should transition to cooling:

```bash
curl -s http://127.0.0.1:9090/internal/proxy/stats | jq '.entries[] | select(.host == "1.2.3.4:80")'
```

Expected: `"state": "cooling"`, `"consecutive_fails": >= 3`, and a
`next_ok` timestamp in the future.

After 10 cumulative failures on that proxy, state should transition to
`quarantined` and remain so for the rest of the process lifetime.

Restore the real `PROXIES` and restart when done.

## Rollback

If any step fails, revert the PR-83 deploy. The proxy pool is purely
additive — rolling back to the pre-pool revision restores the legacy
`pickProxyURL` rotation without state loss (the pool was in-memory
anyway).

## Reference

- Plan & full design rationale: `docs/superpowers/plans/2026-05-20-proxy-pool-with-health-tracking.md`
- Pool implementation: `proxypool/`
- Webrunner integration: `runner/webrunner/webrunner.go` (look for `proxyPool`, `classifyProxyOutcome`, and the lease-report defer in `scrapeJob`)
- Stats endpoint: `runner/webrunner/proxy_stats_handler.go`
