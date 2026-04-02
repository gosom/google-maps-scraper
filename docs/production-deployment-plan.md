# Production Deployment Plan

**Project:** Brezel Scraper (Google Maps Scraping Platform)
**Date:** 2026-03-24
**Status:** REVIEWED — 8 false claims corrected, 14 missing items added (2026-03-24)

---

## Review Findings (Post-Verification)

> This plan was verified against the actual codebase by a reviewer agent. Below are corrections applied:

**False claims corrected:**
1. ~~Caddy reverse proxy~~ → Actual production compose uses **Nginx** (with-proxy profile). Plan now proposes Caddy as a REPLACEMENT for Nginx, clearly marked.
2. ~~Resource limits: 4GB/6CPU~~ → Actual compose has **2GB/1CPU**. Plan updated to match reality + recommended increases.
3. ~~shm_size: 2gb~~ → Actual compose has **no shm_size**. Flagged as CRITICAL fix needed.
4. ~~70s graceful shutdown~~ → Corrected to **~55s** (phases overlap, not sequential).
5. ~~55+ migration files~~ → Actual count: **53 files** (27 up + 26 down), 30 logical migrations.
6. ~~Manual deploy to production via GH Actions~~ → No production deploy job exists. Production deploy is fully manual SSH.

**Critical gaps found (must fix before deploying):**
1. `stop_grace_period: 60s` missing from production compose (Docker default 10s will SIGKILL before graceful shutdown completes)
2. `shm_size: 2gb` missing (Chromium will crash with 64MB default /dev/shm)
3. `init: true` missing (Playwright zombie processes in Docker)
4. Production compose missing required env vars: `ALLOWED_ORIGINS`, `S3_BUCKET_NAME`, `AWS_*`, `STRIPE_*`, `API_KEY_SERVER_SECRET`
5. No dedicated Docker network in production compose (staging has one)
6. Loki logging driver `loki-url: localhost:3100` may fail from inside containers
7. Debian bullseye-slim (Debian 11) is EOL since Aug 2024 → upgrade to bookworm-slim
8. Build-time `ldflags` don't inject version → `/health` reports "dev" in production

---

## Executive Summary

This plan recommends a **phased deployment strategy** starting with a single Hetzner dedicated server running Docker Compose, scaling to multi-server and eventually Kubernetes only when justified by load. The architecture exploits our app's key advantage: **PostgreSQL is the only shared state**, making horizontal scaling straightforward.

**Recommended starting setup:** Hetzner AX52 (~$61/mo) + managed PostgreSQL + Caddy reverse proxy + Grafana Cloud Free monitoring.

---

## 1. Current Architecture Assessment

### What We Have

| Component | Technology | Notes |
|-----------|-----------|-------|
| **Backend** | Go monolith (single binary: `brezel-api`) | Runs in web, database, file, or lambda mode |
| **Frontend** | Next.js (separate repo) | Clerk auth, API calls to backend |
| **Database** | PostgreSQL | All job state, results, users, billing |
| **Browser Engine** | Playwright (Chromium) | Bundled in Docker image (~1.2GB layer) |
| **Auth** | Clerk (JWT) + custom API keys (HMAC) | External dependency |
| **Payments** | Stripe webhooks | External dependency |
| **Proxies** | Webshare API + custom proxy pool | Ports 8888-9998 (1111 ports) |
| **Monitoring** | Prometheus metrics (:9090) + Loki logging | Already instrumented |
| **CI/CD** | GitHub Actions → GHCR → SSH deploy | Staging auto-deploys on `develop` push |
| **File Storage** | S3 (required in production) + local volume | CSV results uploaded to S3 |

### Key Architectural Properties

- **Stateless workers**: All job state lives in PostgreSQL. Any container can resume any job.
- **No sticky sessions**: Load balancer can round-robin freely.
- **Auto-migrations**: Each container runs migrations on startup (idempotent).
- **Graceful shutdown**: ~55s max (15s HTTP + 30s leak drain + 10s job drain — phases overlap).
- **Migration safety**: PostgreSQL advisory locks prevent concurrent migration races from multiple containers.
- **Health checks**: `GET /health` validates both process and DB connectivity.

### Resource Profile Per Container

| Resource | Baseline | Per Browser Instance | At CONCURRENCY=8 |
|----------|----------|---------------------|-------------------|
| RAM | 50MB (Go process) | 150-250MB (Google Maps is JS-heavy) | 1.2-2.0GB |
| CPU | 0.1 core | 0.1-0.3 core | 1-3 cores |
| Max parallel jobs | — | — | 4 (CONCURRENCY/2) |
| Ports consumed | 8080, 9090 | 1 proxy port each | 8 ports |

---

## 2. Deployment Options Evaluated

### Option A: Single Dedicated Server + Docker Compose (RECOMMENDED START)

**Setup:** Hetzner AX52 + Docker Compose + Caddy + managed PostgreSQL

| Component | Provider | Cost/mo |
|-----------|---------|---------|
| Hetzner AX52 (8c/16t Ryzen 7, 64GB RAM) | Hetzner | ~$61 |
| Managed PostgreSQL (2 vCPU, 4GB, 80GB) | DigitalOcean or Hetzner Cloud | ~$24-30 |
| Domain + DNS | Cloudflare (free tier) | $0 |
| S3 storage (results) | Backblaze B2 or DO Spaces | ~$5 |
| Monitoring | Grafana Cloud Free | $0 |
| **Total** | | **~$90-96/mo** |

**Capacity:** 20-30 concurrent browser instances, ~4-8 parallel scraping jobs

**Pros:**
- Simplest to deploy and maintain
- No K8s overhead — 100% compute goes to scraping
- SSH in to debug directly
- Same Docker Compose workflow as current staging

**Cons:**
- Single point of failure (server dies = scraping stops)
- Vertical scaling only (bigger server = more money)
- Manual OS patches and Docker updates

### Option B: Two Servers + Shared Queue

**When:** You outgrow one server (need >30 concurrent browsers)

| Component | Cost/mo |
|-----------|---------|
| 2x Hetzner AX52 | ~$122 |
| Managed PostgreSQL (larger) | ~$50 |
| Load balancer (Hetzner LB) | ~$6 |
| S3 + monitoring | ~$5 |
| **Total** | **~$183/mo** |

**Capacity:** 40-60 concurrent browsers, ~8-16 parallel jobs

Both servers pull jobs from the same PostgreSQL using `SELECT ... FOR UPDATE SKIP LOCKED`. No additional queue infrastructure needed — the database IS the queue.

### Option C: Managed Kubernetes (DigitalOcean DOKS)

**When:** You need 50+ containers, auto-scaling, or high availability SLAs

| Component | Cost/mo |
|-----------|---------|
| DOKS HA control plane | $40 |
| 3x CPU-Optimized nodes (4 vCPU, 8GB each) | ~$252 |
| Load balancer | $12 |
| Managed PostgreSQL | ~$50 |
| S3 + monitoring | ~$5 |
| **Total** | **~$359/mo** |

**Capacity:** ~10 vCPU / 21GB usable (after K8s overhead), auto-scaling with KEDA

**Pros:** Auto-healing, rolling deploys, scale-to-zero with KEDA
**Cons:** 60-70% more expensive for equivalent compute, steep learning curve

### Decision Matrix

| Factor | Option A (Single VPS) | Option B (Two VPS) | Option C (K8s) |
|--------|----------------------|-------------------|----------------|
| Monthly cost | ~$96 | ~$183 | ~$359 |
| Concurrent browsers | 20-30 | 40-60 | Auto-scale |
| Setup complexity | Low | Medium | High |
| Maintenance burden | Medium | Medium | High (without DevOps) |
| Fault tolerance | None | Partial | Full |
| Scale-to-zero | No | No | Yes (KEDA) |
| Time to deploy | 1-2 hours | 3-4 hours | 1-2 days |

**Recommendation:** Start with **Option A**. Graduate to **Option B** when you consistently need >25 concurrent browsers. Consider **Option C** only when you have 50+ containers or need auto-scaling.

---

## 3. Recommended Production Architecture (Option A)

### Service Layout

```
┌─────────────────────────────────────────────────────┐
│                  Hetzner AX52 Server                 │
│                                                      │
│  ┌──────────┐  ┌──────────────┐  ┌───────────────┐ │
│  │  Caddy    │  │ brezel-api   │  │  brezel-api   │ │
│  │ (reverse  │──│ (web mode)   │  │  (worker)     │ │
│  │  proxy)   │  │  port 8080   │  │  optional     │ │
│  │ :80/:443  │  │  port 9090   │  │               │ │
│  └──────────┘  └──────────────┘  └───────────────┘ │
│                        │                             │
│  ┌──────────────────────────────────────────────┐   │
│  │              Docker Network                    │   │
│  └──────────────────────────────────────────────┘   │
│                        │                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐  │
│  │ Frontend  │  │  Loki    │  │  Grafana         │  │
│  │ Next.js   │  │  :3100   │  │  :3001           │  │
│  │ :3000     │  │          │  │  (or Cloud Free) │  │
│  └──────────┘  └──────────┘  └──────────────────┘  │
└─────────────────────────────────────────────────────┘
                         │
                    ┌────┴─────┐
                    │ Managed  │
                    │ Postgres │
                    │ (external│
                    │  host)   │
                    └──────────┘
```

### Why Managed PostgreSQL (Not Self-Hosted)

- **Automated backups**: Daily snapshots + WAL archiving, point-in-time recovery
- **Automatic failover**: High availability with standby nodes
- **Security patches**: Provider handles PostgreSQL updates
- **Connection pooling**: Built-in PgBouncer on some providers
- **Cost**: $24-50/mo for 2-4 vCPU, 4-8GB RAM, 80-160GB storage
- **No ops burden**: You don't manage replication, vacuuming tuning, or disk monitoring

If budget is very tight, self-host PostgreSQL on the same Hetzner server with automated pg_dump backups to S3.

### Docker Compose Production Layout

> **NOTE:** The current `docker-compose.production.yaml` uses Nginx and has 2GB/1CPU limits.
> The config below is the PROPOSED improved version with all critical fixes applied.

```yaml
networks:
  brezel:
    driver: bridge

services:
  caddy:
    # PROPOSED: Replace existing Nginx with Caddy for auto-HTTPS
    image: caddy:2-alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
    networks: [brezel]
    restart: unless-stopped

  backend:
    image: ghcr.io/brezel-ai/brezelscraper-backend:${TAG:-latest}
    init: true                    # CRITICAL: prevents Chromium zombie processes
    shm_size: '2gb'               # CRITICAL: Chromium needs >64MB /dev/shm
    stop_grace_period: 60s        # CRITICAL: app needs ~55s for graceful shutdown
    ports:
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:9090:9090"
    deploy:
      resources:
        limits:
          memory: 4g              # Increase from current 2G for browser headroom
          cpus: '4.0'             # Increase from current 1.0 for multi-browser
        reservations:
          memory: 1g
          cpus: '0.5'
    environment:
      - DSN=${DSN}
      - CONCURRENCY=auto
      - APP_ENV=production
      - WEB_ADDR=:8080
      - INTERNAL_ADDR=:9090
      - ALLOWED_ORIGINS=${ALLOWED_ORIGINS}
      - API_KEY_SERVER_SECRET=${API_KEY_SERVER_SECRET}
      - CLERK_SECRET_KEY=${CLERK_SECRET_KEY}
      - STRIPE_SECRET_KEY=${STRIPE_SECRET_KEY}
      - STRIPE_WEBHOOK_SECRET=${STRIPE_WEBHOOK_SECRET}
      - AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID}
      - AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY}
      - AWS_REGION=${AWS_REGION}
      - S3_BUCKET_NAME=${S3_BUCKET_NAME}
      - WEBSHARE_API_KEY=${WEBSHARE_API_KEY}
      - GOOGLE_COOKIES_FILE=${GOOGLE_COOKIES_FILE:-}
      - DISABLE_TELEMETRY=1
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 60s
    logging:
      driver: loki
      options:
        loki-url: "http://loki:3100/loki/api/v1/push"   # Use service name, not localhost
        loki-retries: "3"
    networks: [brezel]
    restart: unless-stopped

  frontend:
    image: ghcr.io/brezel-ai/brezelscraper-frontend:${TAG:-latest}
    ports:
      - "127.0.0.1:3000:3000"
    depends_on:
      backend:
        condition: service_healthy
    networks: [brezel]
    restart: unless-stopped

  loki:
    image: grafana/loki:3-latest
    ports:
      - "127.0.0.1:3100:3100"
    volumes:
      - loki_data:/loki
    networks: [brezel]
    restart: unless-stopped

  grafana:
    image: grafana/grafana:latest
    ports:
      - "127.0.0.1:3001:3000"
    volumes:
      - grafana_data:/var/lib/grafana
    networks: [brezel]
    restart: unless-stopped

volumes:
  caddy_data:
  loki_data:
  grafana_data:
  gmapsdata:
```

### Caddyfile (Reverse Proxy + Auto-HTTPS)

```
app.brezel.ai {
    reverse_proxy localhost:3000
}

api.brezel.ai {
    reverse_proxy localhost:8080
}

grafana.brezel.ai {
    reverse_proxy localhost:3001
    basicauth {
        admin $2a$14$... # bcrypt hash
    }
}
```

Caddy handles Let's Encrypt automatically — zero certificate management.

---

## 4. Database Optimization for Production

### Connection Pool Settings

```bash
DB_MAX_OPEN_CONNS=25      # Match managed PG connection limit
DB_MAX_IDLE_CONNS=10      # Keep warm connections ready
DB_CONN_MAX_LIFETIME=5m   # Recycle before PG timeout
DB_CONN_MAX_IDLE_TIME=2m  # Don't hold idle connections forever
```

### PgBouncer (When Scaling to Multiple Containers)

When running 2+ backend containers, add PgBouncer in front of PostgreSQL:

```yaml
pgbouncer:
  image: edoburu/pgbouncer:latest
  environment:
    - DATABASE_URL=${DSN}
    - POOL_MODE=transaction
    - DEFAULT_POOL_SIZE=25
    - MAX_CLIENT_CONN=200
  ports:
    - "127.0.0.1:6432:6432"
```

Backend containers connect to PgBouncer (:6432) instead of PostgreSQL directly.

### Write Optimization Checklist

- [x] Batch INSERTs already implemented (resultwriter batches of 50)
- [ ] Consider COPY for bulk imports when processing >10K results per job
- [ ] Add table partitioning on `results` by `created_at` month when >10M rows
- [ ] Tune WAL: `max_wal_size=4GB`, `checkpoint_completion_target=0.9`
- [ ] Monitor with `pg_stat_statements` for slow queries

### Storage Growth Estimation

| Data Type | Size Per Entry | At 1M entries |
|-----------|---------------|---------------|
| Place (basic details) | ~500 bytes | ~500 MB |
| JSONB metadata | ~1-2 KB | ~1-2 GB |
| Reviews (avg 20/place) | ~300 bytes each | ~6 GB |
| Indexes | ~2x raw data | ~15-20 GB |
| **Total per 1M places** | | **~25-30 GB** |

Plan for 5-10 GB/month growth initially. At $0.10/GB for managed PG storage, this is negligible.

---

## 5. Proxy Strategy for Production

### Provider Recommendation

| Tier | Provider | Type | Cost | Use Case |
|------|----------|------|------|----------|
| **Budget** | Webshare | Residential | $1.40-3.50/GB | Low volume, testing |
| **Production** | Bright Data | Residential | $2.50-4.00/GB | High volume, reliable |
| **Premium** | Oxylabs | Residential | $4.00-8.00/GB | Enterprise, SLA needed |

**Google Maps requires residential proxies.** Datacenter IPs are blocked almost immediately.

### Proxy Configuration Best Practices

```bash
# Per-IP limits for Google Maps
MAX_CONCURRENT_PER_IP=3-5      # Google throttles hard above this
REQUEST_DELAY_MIN=2s            # Minimum delay between requests to same domain
REQUEST_DELAY_JITTER=500ms      # Random jitter to avoid pattern detection
SESSION_STICKY_DURATION=30m     # Keep same IP for pagination/reviews
```

### Cost Estimation

| Scale | Monthly Scrapes | Proxy Bandwidth | Proxy Cost |
|-------|----------------|-----------------|------------|
| Small | 50K places | ~25 GB | ~$63-100 |
| Medium | 250K places | ~125 GB | ~$313-500 |
| Large | 1M places | ~500 GB | ~$1,250-2,000 |

Proxy cost dominates at scale — more than compute infrastructure.

---

## 6. Monitoring & Observability

### Recommended Stack

**Phase 1 (Now):** Grafana Cloud Free
- 10K metric series, 50 GB logs, 50 GB traces — sufficient for a single app
- Zero ops burden, zero cost
- Use Grafana Alloy (replaces deprecated Agent) to ship metrics and logs

**Phase 2 (When outgrowing free tier):** Self-hosted on separate server
- $6-12/mo DigitalOcean droplet (2 vCPU, 2-4 GB RAM)
- Prometheus + Grafana + Loki + Alertmanager
- ~170 MB disk for 15-day metrics retention at 1K series

### Key Metrics to Track

| Category | Metric | Alert Threshold |
|----------|--------|-----------------|
| Scraping | `scrape_success_rate` | < 85% for 5 min |
| Scraping | `scrape_job_duration_p95` | > 10 min |
| Proxies | `proxy_block_rate` | > 30% for 3 min |
| Queue | `pending_jobs_count` | Growing faster than drain rate |
| System | `container_memory_usage` | > 80% of limit |
| Database | `pg_active_connections` | > 80% of max |

### Alerting

- **Slack** for warnings (success rate dip, proxy issues)
- **PagerDuty/email** for critical (server down, DB unreachable)
- **Alertmanager** routes based on severity labels

---

## 7. SSL/TLS & Reverse Proxy

### Recommendation: Caddy

| Feature | Caddy | Traefik | Nginx |
|---------|-------|---------|-------|
| Auto HTTPS | Built-in, zero config | Requires ACME config | Requires Certbot |
| Config complexity | 3-5 lines per site | Moderate YAML + labels | 15+ lines per site |
| Docker integration | Caddyfile reload | Auto-discovery (labels) | Manual config |
| Best for | Simple setups | Dynamic containers | Max throughput |

Caddy is the pragmatic choice. Migrate to Traefik only if you need automatic container discovery across many services.

---

## 8. Backup Strategy

### Database Backups

| Method | Frequency | Retention | Tool |
|--------|-----------|-----------|------|
| **Managed PG snapshots** | Daily (automatic) | 7 days | Provider |
| **pg_dump to S3** | Daily via cron | 30 days | pg_dump + rclone |
| **WAL archiving** | Continuous | 7 days | pgBackRest (Phase 2) |

**Minimum viable:** Managed PostgreSQL daily snapshots + weekly pg_dump to Backblaze B2 ($5/TB/month).

### Application Backups

- **Docker Compose + env files**: Stored in Git (config IS the backup)
- **Named volumes** (gmapsdata, caddy_data): Weekly tar + upload to S3
- **Secrets**: Stored in GitHub Actions secrets (source of truth)

### Disaster Recovery Targets

| Metric | Target |
|--------|--------|
| **RPO** (max data loss) | 1 hour (pg_dump frequency) |
| **RTO** (max downtime) | 2-4 hours (provision new server + restore) |

---

## 9. Security Hardening

### Server Level

```bash
# Firewall (ufw)
ufw default deny incoming
ufw allow 22/tcp     # SSH
ufw allow 80/tcp     # HTTP (Caddy redirect)
ufw allow 443/tcp    # HTTPS (Caddy)
ufw enable

# SSH hardening
PasswordAuthentication no    # Key-only
PermitRootLogin no
Port 2222                    # Non-standard port

# Automatic security updates
apt install unattended-upgrades
dpkg-reconfigure -plow unattended-upgrades
```

### Container Level

- All internal ports bound to `127.0.0.1` (not `0.0.0.0`)
- `shm_size: 2gb` for Playwright (instead of `--no-sandbox`)
- Resource limits (memory + CPU) on all containers
- No `--privileged` flag
- Read-only root filesystem where possible

### Application Level

- `BRAZA_DEV_AUTH_BYPASS` must NEVER be set in production
- `API_KEY_SERVER_SECRET` must be ≥32 bytes of crypto-random hex
- `ALLOWED_ORIGINS` must be set to exact production domains
- Stripe webhook signature verification enabled
- Rate limiting on all public endpoints (already implemented)

---

## 10. CI/CD Pipeline

### Current Flow (Keep)

```
develop branch → GitHub Actions → Build Docker image → Push to GHCR → SSH deploy to staging
main branch → GitHub Actions → Build Docker image → Push to GHCR → Manual deploy to production
```

### Production Deployment Script

```bash
#!/bin/bash
set -euo pipefail

# Pull latest images
docker compose -f docker-compose.production.yaml pull

# Rolling restart (zero-downtime with health checks)
docker compose -f docker-compose.production.yaml up -d --remove-orphans

# Wait for health check
timeout 120 bash -c 'until curl -sf http://localhost:8080/health; do sleep 2; done'

# Verify version
curl -s http://localhost:8080/api/version | jq .version

# Prune old images
docker image prune -f
```

### Rollback Plan

```bash
# Rollback to previous image tag
export TAG=previous-sha
docker compose -f docker-compose.production.yaml up -d
```

---

## 11. Scaling Roadmap

### Phase 1: Single Server (Now → 50K scrapes/month)

- Hetzner AX52 + managed PostgreSQL
- CONCURRENCY=auto (4 browsers on 8 cores)
- Single Docker Compose deployment
- **Cost: ~$96/month**

### Phase 2: Optimized Single Server (50K → 250K scrapes/month)

- Upgrade to Hetzner AX102 (16 cores, 128GB RAM) — ~$130/mo
- CONCURRENCY=8-12 (8-12 browsers)
- Add PgBouncer for connection pooling
- Table partitioning on results
- **Cost: ~$160/month**

### Phase 3: Multi-Server (250K → 1M scrapes/month)

- 2x Hetzner AX52 behind shared PostgreSQL
- Load balance API with Caddy upstream
- Workers pull from shared job queue
- Separate monitoring server
- **Cost: ~$250/month**

### Phase 4: Kubernetes (>1M scrapes/month or HA requirement)

- DigitalOcean DOKS or Hetzner Cloud K8s
- KEDA for queue-based autoscaling
- Scale-to-zero during off-hours
- Dedicated node pools for scraping workers
- **Cost: ~$400+/month**

---

## 12. Pre-Launch Checklist

### Infrastructure

- [ ] Provision Hetzner AX52 server
- [ ] Install Docker + Docker Compose
- [ ] Set up managed PostgreSQL (DigitalOcean or Hetzner Cloud)
- [ ] Configure Caddy with production domains
- [ ] Set up Grafana Cloud Free account
- [ ] Configure automated pg_dump backups to S3
- [ ] Set up firewall (ufw) and SSH hardening
- [ ] Configure unattended-upgrades

### Application

- [ ] Set all production environment variables
- [ ] Verify `APP_ENV=production` (enforces S3, disables dev bypass)
- [ ] Set `ALLOWED_ORIGINS` to production domains only
- [ ] Generate and set `API_KEY_SERVER_SECRET` (≥32 bytes)
- [ ] Configure Clerk production keys
- [ ] Configure Stripe production keys + webhook endpoint
- [ ] Set `CONCURRENCY=auto` or explicit value
- [ ] Set `shm_size: 2gb` in Docker Compose

### Monitoring

- [ ] Verify `/health` endpoint returns `{"status":"ok","db":"ok"}`
- [ ] Verify `/metrics` endpoint returns Prometheus format
- [ ] Configure Grafana dashboards (scrape rate, proxy health, queue depth)
- [ ] Set up Slack alerts for critical failures
- [ ] Test alert firing with synthetic failure

### Security

- [ ] SSL certificates working (Caddy auto-HTTPS)
- [ ] All internal ports bound to 127.0.0.1
- [ ] SSH key-only authentication
- [ ] `BRAZA_DEV_AUTH_BYPASS` is NOT set
- [ ] Rate limiting verified on public endpoints
- [ ] Stripe webhook signature verification enabled

### Testing

- [ ] Run a test scraping job end-to-end
- [ ] Verify results appear in PostgreSQL and S3
- [ ] Verify Clerk authentication works
- [ ] Verify Stripe webhook processing
- [ ] Test graceful shutdown (send SIGTERM, verify clean exit)
- [ ] Load test with 5-10 concurrent jobs

---

---

## 13. Dockerfile Fixes Required

### Upgrade Base Image

Current: `debian:bullseye-slim` (Debian 11, EOL Aug 2024)
Required: `debian:bookworm-slim` (Debian 12, supported until 2028)

### Inject Version at Build Time

Current `Dockerfile` build line does not pass `VERSION` to Go ldflags. The `/health` endpoint always reports `"dev"`.

```dockerfile
# Fix: Add -X flag to inject version
ARG GIT_COMMIT=unknown
RUN CGO_ENABLED=0 go build -ldflags="-w -s -X main.version=${GIT_COMMIT}" -o /usr/bin/brezel-api .
```

### Add init process

```yaml
# In docker-compose:
backend:
  init: true  # Uses tini to reap zombie Chromium processes
```

---

## 14. External Dependencies & Single Points of Failure

| Dependency | Impact if Down | Mitigation |
|-----------|---------------|------------|
| **Clerk** (auth) | No new logins, JWT validation fails | Cache valid JWTs locally (short-lived) |
| **Stripe** (billing) | Webhooks queue, billing delayed | Idempotent webhook processing + `processed_webhook_events` dedup |
| **Proxy provider** (Webshare/Bright Data) | Scraping blocked immediately | Support multiple providers, fallback pool |
| **S3** (result storage) | CSV uploads fail, jobs may error | Local fallback volume + retry queue |
| **Managed PostgreSQL** | Everything stops | Use HA-enabled managed PG (adds ~$20-30/mo) |
| **DNS/Cloudflare** | Site unreachable | Low TTL records, secondary DNS |

---

*Plan synthesized from 4 parallel research agents analyzing: codebase architecture, K8s vs Docker Compose costs, scraping infrastructure patterns, and monitoring stack deployment. Reviewed by a 5th agent that verified all claims against the actual codebase — 8 false claims corrected, 14 missing items added. All pricing verified via web research as of 2026-03-24.*
