# Brezel Scraper v2 Architecture — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Plan scale note:** This is a 16-week, 4-6 engineer architecture build, not a single-feature TDD plan. Tasks are at workstream-milestone granularity (1-3 days each), not test-step granularity. Per the writing-plans skill convention, chunk boundaries (`## Chunk N`) delimit independently-reviewable sections. Run the plan-document-reviewer subagent per chunk.

**Goal:** Replace the static `google_cookies.json` MVP with an adversarial-engineering-grade scraping platform — a three-tier acquisition system (HTTP-replay + stealth-browser fleet + search-nav fallback) fed by a token mint pool, supported by a SearchGuard deobfuscation pipeline and a consensual fingerprint harvest extension.

**Architecture:** Go orchestrator (existing surface preserved) facades over three acquisition tiers exposed via Connect-RPC to a Python browser-fleet sidecar. Token economy: a small Tier-2 browser fleet mints attestation tokens that are replayed by a large Tier-1 HTTP-worker fleet (20-50x cheaper per scrape). Per-request degradation detector routes between tiers. Valkey for hot path, Postgres for durable state.

**Tech Stack (verified May 2026):**
- **Go**: 1.25, `connectrpc.com/connect-go` v1.19.2, `Noooste/azuretls-client` v1.13.2, `refraction-networking/utls` v1.8.2+, `panjf2000/ants` v2.12.0, `jackc/pgx/v5`, `valkey-io/valkey-go` v1.0.74, `go-redis/redis_rate/v10`, OTel v1.43
- **Python**: 3.12, `grpcio` 1.78+, FastAPI (admin plane), `playwright` (driver). **Stealth portfolio (co-equal, routed per signal class — see §1.3):** `cloverlabs-camoufox` (engine-fingerprint route), `patchright==1.56.0` (CDP-leak route), `nodriver` (CDP-minimal route). Behavioral humanizer (`ghost-cursor` + `CDP-Patches`) required on all three.
- **Infrastructure**: Valkey 8.x (managed: ElastiCache Serverless or Memorystore), Postgres 16, S3 cold archive, k8s for sidecar pool
- **Proxies**: Decodo residential (default), Bright Data residential (premium), mobile carrier IPs (token-mint warming)

---

## Chunk 1 — Architecture Overview & Library Decisions

### 1.1 System diagram

```
                 Customer ──► Next.js Dashboard (unchanged) ──► Go API (unchanged surface)
                                                                       │
                                                                       ▼
                                                       ┌──────────────────────────────┐
                                                       │  Orchestrator (rewritten)    │
                                                       │  PlaceFetcher facade         │
                                                       │  Cache → Acquire → Detect    │
                                                       └──┬──────────┬─────────┬──────┘
                                                          │          │         │
                                                          ▼          ▼         ▼
                              ┌──────────────────────┐  ┌────────────────────┐  ┌────────────────────┐
                              │ Tier 1: HTTP Replay  │  │ Tier 2: Browser    │  │ Tier 3: Search-Nav │
                              │ Go-native            │  │ Python sidecar     │  │ Same as T2 +       │
                              │ azuretls + utls      │  │ Portfolio routed   │  │ google.com/search  │
                              │ Consumes tokens      │  │ per signal class:  │  │ ─► click result    │
                              │ ~200-500ms / req     │  │ Camoufox / Patch-  │  │ When direct URL    │
                              │                      │  │ right / Nodriver   │  │ degraded           │
                              │                      │  │ Mints tokens       │  │                    │
                              └──────────┬───────────┘  └─────────┬──────────┘  └──────────┬─────────┘
                                         │                        │                        │
                                         │  consumes              │  mints                 │
                                         ▼                        ▼                        │
                                 ┌─────────────────────────────────────┐                   │
                                 │  Token Mint Pool                    │                   │
                                 │  Valkey sorted set + ZPOPMIN        │                   │
                                 │  Postgres durable audit             │                   │
                                 └─────────────────────────────────────┘                   │
                                         │                                                 │
                                         ▼                                                 │
                                 ┌─────────────────────────────────────────────────────────┘
                                 │  Detector + Retry Router
                                 │  HTML completeness + per-tier success telemetry
                                 ▼
                          ┌──────────────────────────────────────────┐
                          │  Proxy Router (Decodo + Bright Data)     │
                          │  ASN-diversity policy enforced           │
                          └──────────────────────────────────────────┘

                          Side stream (cross-cutting):
                          ┌──────────────────────────────────────────┐
                          │  SearchGuard Deobfuscation Pipeline      │
                          │  jsmon watcher → opcode extractor →      │
                          │  signal inventory → Tier-2 validator     │
                          └──────────────────────────────────────────┘

                          ┌──────────────────────────────────────────┐
                          │  Fingerprint Store                       │
                          │  Synthetic (chrome-stats + AmIUnique) +  │
                          │  consensual harvest (Hola pattern)       │
                          └──────────────────────────────────────────┘
```

### 1.2 Verified library decisions

Every load-bearing dependency below was verified May 2026 via GitHub stars/last-release/maintainer activity + at least one independent benchmark or community source. Decisions are *not* defaults — alternatives were explicitly weighed.

| Decision | Choice | Why this over alternatives | Verified source |
|---|---|---|---|
| Tier-1 TLS client | **`Noooste/azuretls-client`** v1.13.2 | Highest release velocity (Apr 17 2026), native HTTP/2 frame fingerprinting + post-quantum keyshare awareness. `bogdanfinn/tls-client` (1.6k stars, more popular) lags Chrome stable by ~2 weeks; `CycleTLS` is slowing; `imroc/req` is not an impersonation tool. | [github.com/Noooste/azuretls-client](https://github.com/Noooste/azuretls-client); [Scrapfly post-quantum TLS](https://scrapfly.io/blog/posts/post-quantum-tls-bot-detection) |
| TLS foundation | **`refraction-networking/utls`** v1.8.2+ (pinned ≥) | Required minimum after GHSA-7m29-f4hw-g2vx and GHSA-rrxv-pmq9-x67r CVEs; anything older is flagged by Cloudflare/Akamai. | [utls advisories](https://github.com/refraction-networking/utls/security/advisories) |
| Tier-1 hardest-target fallback | **`go-curl-impersonate`** (cgo wrapper) | Byte-identical ClientHello via BoringSSL when azuretls can't beat a target. cgo deployment overhead is the tradeoff. | [github.com/lwthiker/curl-impersonate](https://github.com/lwthiker/curl-impersonate) |
| Tier-2 engine portfolio | **Per-target routing, not "primary/secondary"** — see §1.3 deep-dive | Source-code verification (May 2026) showed the two engines defeat fundamentally different signal classes. No universal winner. | See §1.3 |
| Tier-2 Camoufox build | **cloverlabs-camoufox FF145+** (NOT daijro mainline v135) | Owner daijro (#570) admits v135 is dead for Google flows; collaborator `icepaq` explicitly directs users to `cloverlabs-camoufox` pip package. Mainline base is 11 Firefox versions behind. | [Camoufox #570](https://github.com/daijro/camoufox/issues/570) (collaborator admission); pip `cloverlabs-camoufox` |
| Tier-2 Patchright pin | **`patchright==1.56.0`** (do NOT use latest) | Patchright shipped a CDP-detectability regression in late Dec 2025 (#94, #161); maintainer Vinyzu explicitly told users to pin 1.56.0 while fix is in progress. Maintainers on exam hiatus. | [patchright-python #94](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/94) |
| Tier-2 tertiary browser | **Nodriver** (ultrafunkamsterdam) | Smallest CDP attack surface — drives real Chrome stable install (clean JA4) with own CDP impl. Tertiary for cases where Patchright's residual attach footprint is flagged. | [github.com/ultrafunkamsterdam/nodriver](https://github.com/ultrafunkamsterdam/nodriver) |
| **Behavioral humanizer** (REQUIRED, non-negotiable) | **`ghost-cursor`** + **`CDP-Patches`** OS-level input injection | Both Camoufox and Patchright dispatch events with `isTrusted=false` (CDP-Patches author admission in Patchright README). SearchGuard's Welford-variance and reservoir-sampling behavioral signals catch any constant-velocity path. Without humanizer, neither engine clears the behavioral gate. | [Patchright README](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright) (CDP-Patches reference); [ghost-cursor](https://github.com/Xetera/ghost-cursor) |
| **Headed mode** (REQUIRED) | xvfb-backed, NOT `--headless=new` | Patchright README + #46 + #84 confirm headless on Google = burned. Camoufox owner admits the same in #505. Sidecar container runs xvfb. | [Patchright #46](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/46) |
| Rejected stealth tools | Botasaurus, Selenium-Driverless, DrissionPage, playwright-stealth, Helium | Botasaurus = framework not engine; Selenium-Driverless = CC BY-NC-SA non-commercial license (blocker); DrissionPage = commercial-use prohibited license (blocker); playwright-stealth = JS-layer shim, not engine-level; Helium = wrong shape (just a higher-level API). | All verified via GitHub repos |
| Sidecar transport | **`connectrpc.com/connect-go`** v1.19.2 + `grpcio` 1.78+ on Python | Wire-compatible with gRPC but one handler speaks gRPC+gRPC-Web+HTTP/JSON; trivially curl-debuggable; same `.proto` codegen pipeline. grpc-go is fine but Connect is cleaner for internal services. Python `grpcio` 1.78.0 released Feb 2026 — sustained official cadence. | [connectrpc/connect-go](https://github.com/connectrpc/connect-go); [Buf engineering writeup](https://buf.build/blog/connect-a-better-grpc); [PyPI grpcio](https://pypi.org/project/grpcio/); [grpc/grpc releases](https://github.com/grpc/grpc/releases) |
| Go worker pool | **`golang.org/x/sync/errgroup`** + **`panjf2000/ants`** v2.12.0 | errgroup for cancellation/error propagation; ants for bounded pre-allocated goroutine reuse (used by Tencent/ByteDance/Shopify/Milvus). `sourcegraph/conc` is effectively frozen since 2023 layoffs. | [github.com/panjf2000/ants](https://github.com/panjf2000/ants) |
| Replace scrapemate | **Yes, retire** | 199 stars, single maintainer. Once architecture is "job queue → fan-out → result writer," scrapemate's abstraction is in the way. Colly is HTTP-centric (wrong shape). | [github.com/gosom/scrapemate](https://github.com/gosom/scrapemate) |
| Postgres driver | **`jackc/pgx/v5`** (keep) | Still right answer in 2026. No v6 announced. | Already in go.mod |
| Hot-path KV store | **Valkey** (managed: ElastiCache Serverless or GCP Memorystore) | Post-2024 Redis license fork — Linux Foundation, AWS/Google/Oracle backed; ~83% enterprise adoption in 2026. Drop-in Redis protocol. DragonflyDB is impressive perf but single-vendor BSL. | [Linux Foundation Valkey announcement](https://thenewstack.io/linux-foundation-forks-the-open-source-redis-as-valkey/); [DEV 2026 comparison](https://dev.to/synsun/redis-vs-valkey-in-2026-what-the-license-fork-actually-changed-1kni) |
| Valkey/Redis client | **`valkey-io/valkey-go`** v1.0.74 (== `redis/rueidis`, same code) | Auto-pipelining gives ~14× throughput over go-redis/v9 in published benchmarks; server-assisted client-side cache built in. Pick valkey-go for license-neutral branding. | [github.com/valkey-io/valkey-go](https://github.com/valkey-io/valkey-go) |
| Rate limiting | **`go-redis/redis_rate/v10`** (GCRA algorithm) | GCRA via Lua = single-key atomic, smoother than fixed-window token bucket. Brandur Leach/Stripe writeup is the canonical reference. | [github.com/go-redis/redis_rate](https://github.com/go-redis/redis_rate); [Brandur on GCRA](https://brandur.org/rate-limiting) |
| Observability | OpenTelemetry-go v1.43+ (traces+metrics stable; logs still beta, use slog) + pprof endpoints | Traces and metrics GA; defer logs to slog→OTel bridge when GA. | [opentelemetry-go releases](https://github.com/open-telemetry/opentelemetry-go/releases) |
| SearchGuard reference | **`think.resoneo.com/botguard-google/`** + **`LuanRT/BgUtils`** | Resoneo is the deepest public technical writeup (Olivier de Segonzac, Jan 19 2026 analyzing v41 of the script). BgUtils is the only production-grade open BotGuard attestation runner. | [SEL: Inside SearchGuard](https://searchengineland.com/inside-google-searchguard-467676); [github.com/LuanRT/BgUtils](https://github.com/LuanRT/BgUtils) |
| JS rotation monitor | **`robre/jsmon`** | Mature bug-bounty tool: fetches configured JS URLs, diffs against prior version, Telegram alert. Fits `/js/bg/{HASH}.js` rotation tracking directly. | [github.com/robre/jsmon](https://github.com/robre/jsmon) |
| Bytecode VM deobfuscator | **None exists generic** — build per-target referencing `notemrovsky/tiktok-reverse-engineering` (best public template) and `dsekz/botguard-reverse` (Cypa's seminal BotGuard VM reverse) | Universal consensus across deobfuscation research: every VM has unique opcode tables; per-target work is required. webcrack/restringer/synchrony all handle obfuscator.io style, not custom VMs. | [github.com/dsekz/botguard-reverse](https://github.com/dsekz/botguard-reverse); [github.com/notemrovsky/tiktok-reverse-engineering](https://github.com/notemrovsky/tiktok-reverse-engineering) |
| Fingerprint dataset | **Build, don't buy** — no legitimate marketplace exists in 2026 | Genesis Market (criminal) was takedown Apr 2023; antidetect vendors (Multilogin, Kameleo, GoLogin, AdsPower) sell *profile-as-a-service* not raw datasets. AmIUnique academic dataset is not bulk-downloadable. | [Multilogin pricing](https://multilogin.com/pricing/); [AmIUnique](https://www.amiunique.org/) |
| Fingerprint acquisition | **Phase 1: synthetic** (Chrome platform stats + AmIUnique aggregates, validated against creepjs); **Phase 2: consensual harvest extension** (Hola pattern, fingerprintjs as collector, GDPR Art. 6(1)(a) consent UX, $0.50 one-time incentive) | Only sustainable long-term option. CAC ~$0.50-2/user. EU AI Act Art. 5 (Aug 2025) does not restrict non-biometric fingerprint harvest. | [Hola SDK legal](https://hola.org/legal/sdk); [fingerprintjs](https://github.com/fingerprintjs/fingerprintjs) |
| Browser pool supervisor | **Custom asyncio pool inside container** modeled on browserless `BrowserManager` pattern | k8s pod-per-browser adds 2-10s startup overhead (Camoufox cold-start is ~1s); supervisord/systemd manage PIDs not browser semantics. Camoufox itself recommends Docker but no published image; we build. | [Browserless scaling post](https://www.browserless.io/blog/scaling-browser-automation-architecture-1000-sessions); [Camoufox Dockerfile](https://github.com/daijro/camoufox/blob/main/Dockerfile) |
| Proxy primary | **Decodo residential** | $2-8/GB, 85.88% Proxyway 2025 Google success, custom sticky sessions. Workhorse. | [Decodo pricing](https://decodo.com/proxies/residential-proxies/pricing) |
| Proxy premium tier | **Bright Data residential** (token-mint warming + hardest targets) | $4-8.40/GB but highest Google success in independent tests. Reserve for trust-seed phase, not bulk scraping. | [Proxyway 2025 research](https://proxyway.com/research/proxy-market-research-2025) |
| Mobile proxies | **Bright Data mobile** or **Soax mobile** | For the most-trusted token-mint paths only. ~3-10× cost of residential. | Verified pricing pages |

### 1.3 Camoufox vs Patchright — engine-level source verification (May 2026)

The prior version of this plan made Patchright "primary" and Camoufox "Firefox hedge." Deep verification of both repos' actual patching code shows that framing was wrong in shape. The truth is **the two tools defeat fundamentally different signal classes** — we need both, routed per-target.

**Camoufox actually patches at C++ source level** (38 patches verified in [daijro/camoufox/patches/](https://github.com/daijro/camoufox/tree/main/patches)):
- WebGL — `ClientWebGLContext::GetParameter` at native binding; not a JS shim, not detectable via descriptor introspection
- Audio — sample-level LCG noise inside `AnalyserNode::GetFloatFrequencyData` etc. (stronger than Brave's 0.1-0.2%)
- Fonts — 3-layer engine patch (`gfxPlatformFontList` filter + glyph-shaping advance jitter + `FontVisibilityProvider`); affects `measureText`/`getBoundingClientRect`/line geometry consistently
- Navigator — patched in `Navigator.cpp` AND `WorkerNavigator.cpp` (worker context most JS-shim tools miss)
- WebRTC — `PeerConnectionImpl::SendLocalIceCandidateToContent` rewrites srflx/host candidates at engine, before any JS observer
- Uses Juggler instead of CDP — `Runtime.enable` doesn't get sent at all, so the leak doesn't exist by construction
- Free wins on Chrome-only markers (`$cdc_`, `chrome.runtime`, `$chrome_asyncScriptInfo`, `__selenium_unwrapped`) — wrong engine, markers don't apply
- TLS = vanilla stock Firefox of build version (clean JA4 match if version is current)

**Camoufox holes** (verified absent from patch tree):
- ❌ No Canvas 2D pixel noise (only Canvas2D edit is a font hint in `CanvasBidiProcessor`; `toDataURL`/`getImageData` flow through unmodified Skia)
- ❌ Zero TLS/network-stack modification (`security/nss/`, `netwerk/protocol/http2/` untouched)
- ❌ No mouse/keyboard/scroll humanizer
- ❌ Mainline daijro/v135 is 11 versions behind FF stable; cloverlabs fork at FF145+ is the current working build per maintainer admission ([#570](https://github.com/daijro/camoufox/issues/570))

**Patchright is narrow but genuinely structural** ([patches verified](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright/tree/main/driver_patches)):
- ✅ **The real Runtime.enable fix**: in `crPagePatch.ts` and `crServiceWorkerPatch.ts`, every `session.send('Runtime.enable')` and the `Runtime.addBinding` call gated on it are excised. Replaced by `Runtime.evaluate { expression: "globalThis", serializationOptions: { serialization: "idOnly" }}` to harvest the executionContextId from the objectId. Independent of V8's May 2025 fix; structural removal, not cosmetic patch.
- ✅ Console.enable disabled entirely
- ✅ Removes `--enable-automation`, `--disable-popup-blocking`, and ~14 other automation switches via `chromiumSwitchesPatch.ts`; explicitly adds `--disable-blink-features=AutomationControlled` (so `navigator.webdriver` doesn't exist at Blink level — not a JS override)
- ✅ Init scripts injected via HTML response rewriting instead of `Page.addScriptToEvaluateOnNewDocument` (no CDP artifacts)
- ✅ Custom selector engine pierces closed shadow roots (not stealth, but useful)

**Patchright holes** (verified untouched in code):
- ❌ WebGL renderer/vendor — passthrough; leaks real GPU or SwiftShader
- ❌ Canvas — no noise, deterministic per device
- ❌ AudioContext — passthrough
- ❌ Fonts — untouched
- ❌ **MouseEvent/KeyboardEvent.isTrusted** — always false on CDP-dispatched events. README explicitly redirects to separate `CDP-Patches` library for OS-level input. SearchGuard, Cloudflare Turnstile, hCaptcha, reCAPTCHA v3 all check isTrusted.
- ❌ `chrome.runtime` shape, `navigator.plugins.length` — passthrough
- ❌ TLS / HTTP/2 — out of scope

### 1.3.1 Per-target routing rules (SEED rules — refined by Detector data)

These rules are **hypotheses derived from the patch-level analysis above and the Sept-Nov 2025 issue cluster**, not measured findings. There is no public benchmark to anchor them against Google (see §1.3.3). The Wave 2 Detector + Wave 3 Task 11.5 canary rig together produce the data to confirm or invert each rule within the first 2 weeks of pilot operation. Treat as initial routing, expect refinement.

```
Engine-fingerprint-bound target (Maps direct-URL with canvas/audio/font/WebGL checks)
  → Camoufox (cloverlabs FF145+) + ghost-cursor humanizer + residential proxy

CDP-leak-bound target (Google search, account flows where automation-flag detection dominates)
  → Patchright (pinned 1.56.0) + CDP-Patches + Chrome channel + headed + residential proxy

CDP-attach-footprint-unacceptable target
  → Nodriver + ghost-cursor + Chrome stable channel
```

The Detector (Wave 2, Task 5) is what observes which signal class is causing degradation on a given target, so the router can be informed by real data not a guess. Initial routing rules are seeded from this matrix; the router refines per-config success rates over time (Wave 3 Task 16).

### 1.3.2 Google-specific issue cluster (the dated evidence)

A synchronized detection-tightening wave hit **Sept-Nov 2025** across both tools:
- [Camoufox #388](https://github.com/daijro/camoufox/issues/388) (Sept 14 2025) "100% detection by Google" — still open
- [Camoufox #410](https://github.com/daijro/camoufox/issues/410) (Nov 2 2025) "Google is catching up"
- [Patchright #135](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright/issues/135) (Oct 20 2025) "Cannot skip Google search recaptcha"
- Quote from Camoufox #388 commenter Perseusmx: *"It's not only Camoufox, it's also pretty much all of the 'premium' Chromium forks as well last couple of weeks… Google likely pushed a silent update."*

Second wave **late Dec 2025**: [Patchright #94](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/94) — CDP-detectability regression broke Google logins; maintainer Vinyzu directed users to pin 1.56.0 while a fix is worked on. Maintainers since on exam hiatus.

Owner admissions:
- daijro (Camoufox) [#505](https://github.com/daijro/camoufox/issues/505): *"paid solutions are the most reliable for business use cases… OS spoofing has become damn near impossible."*
- daijro [#514](https://github.com/daijro/camoufox/issues/514): *"Nothing could spoof tests that render out shaders and check if they match up with the claimed fingerprint."*

**Fork pin verification — cloverlabs-camoufox vs daijro mainline.** Collaborator `icepaq` in [Camoufox #570](https://github.com/daijro/camoufox/issues/570) (Apr 11 2026): *"v135 is being detected by all major anti bot platforms. You'll need to use the experimental v145 build… `cloverlabs-camoufox` pip package… Google signups are very tough nowadays."* Cross-referenced in [#522](https://github.com/daijro/camoufox/issues/522) (resolved via cloverlabs build) and [#555](https://github.com/daijro/camoufox/issues/555) (resolution = "switch to coryking fork v142.0.1-fork.27"). **Spec dossier §5.B references only the coryking fork (Nov 6 2025); cloverlabs is a divergence from the spec, based on issue-tracker evidence dated Apr 2026 — newer than the spec's research window.** We adopt cloverlabs because (a) it's at FF145+ vs coryking's FF142, and (b) it has more recent issue-tracker validation. **Treat as a moving pin** — re-verify at each sprint boundary whether cloverlabs remains the recommended fork or upstream daijro v150+ (May 11 2026) has caught up.

**Patchright pin verification — 1.56.0.** Maintainer Vinyzu in [patchright-python #94](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/94) (Dec 31 2025): *"Please still use 1.56.0… we're actively working on an update."* Cross-referenced in [patchright #161](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright/issues/161) where collaborator `faaizalam` confirms *"1.56.0 works on Windows, but on mac it got detected; 1.50.0 worked on mac."* **Spec dossier §5.B references Patchright only via ZenRows review without version pin; the 1.56.0 pin is added in this plan based on Dec 2025–May 2026 issue tracker evidence.** Maintainers reportedly on exam hiatus — subscribe to release notifications to know when fix lands.

**These are real signals.** They drive three commitments in our plan:
1. Treat the Detector as the priority, not the stealth-tool choice. We can swap engines; we cannot operate without telemetry.
2. Use the cloverlabs fork, not daijro mainline, for Camoufox.
3. Pin Patchright at 1.56.0 until upstream regression is fixed; subscribe to release notifications.

### 1.3.3 No public head-to-head benchmark exists

Despite extensive 2026 search, **no dated, methodology-disclosed, independent head-to-head benchmark of Patchright vs Camoufox vs Nodriver vs Playwright against Google exists**. The closest items (techinz/browsers-benchmark, Kahtaf 2026-03-04, scrapewise 2026-04-20, proxies.sx 2026-02-06) all either omit Google or omit one of the tools or omit the run date. The strongest dated Google-specific signals are the *negative* issues above.

This means: **we must build an internal benchmark rig**. Wave 2 Task 5 + Wave 3 acceptance tests are partially this benchmark; we should also stand up a continuous canary rig that runs all three engines against a fixed Maps URL set every hour, alerting on any change.

### 1.4 Verified non-decisions (things we intentionally did NOT pick)

- ❌ **No vendor SERP API** (Bright Data SERP / Outscraper / SerpApi / Apify). These are competitors; reselling them is margin-stacking with no moat. Documented in prior dossier §0 C0.
- ❌ **No account farm / no logged-in scraping.** Architecturally constrained — CEO direction (prior dossier §-1), though the engineering posture here is more aggressive on bypass than the prior plan implied.
- ❌ **No `chromedp` + `chromedp-undetected`.** DataDome explicitly fingerprints it ([source](https://datadome.co/headless-browsers/eifng024/); spec dossier §5.C). Go-native stealth path is dead-end for 2026 Google — this is precisely why the Python sidecar is structural, not optional.
- ❌ **No `puppeteer-extra-stealth`.** Unmaintained since Mar 2023 despite 450k weekly downloads. Verified npm state.
- ❌ **No DragonflyDB.** Single-vendor BSL license, foundational infra risk despite the perf advantage.
- ❌ **No k8s-pod-per-browser.** Adds 2-10s pod startup vs ~1s Camoufox cold-start.

---

## Chunk 2 — File Structure & Workstream Map

### 2.1 New repositories and directories

```
brezelscraper-backend/                          # existing Go repo
├── internal/
│   ├── fetcher/                                # NEW — PlaceFetcher facade + tier router
│   │   ├── fetcher.go                          # PlaceFetcher interface + functional options
│   │   ├── router.go                           # Tier selection logic
│   │   ├── retry.go                            # Per-tier retry with degradation routing
│   │   └── fetcher_test.go
│   ├── tier1/                                  # NEW — HTTP replay tier
│   │   ├── client.go                           # azuretls + utls wiring
│   │   ├── replay.go                           # Token-replay against Maps RPC
│   │   ├── parser.go                           # HTML/JSON response parsing
│   │   └── tier1_test.go
│   ├── tokens/                                 # NEW — token mint pool
│   │   ├── pool.go                             # Valkey sorted-set operations
│   │   ├── lifecycle.go                        # Mint, lease, consume, retire
│   │   └── pool_test.go
│   ├── detector/                               # NEW — degradation detection
│   │   ├── detector.go                         # HTML completeness checks
│   │   ├── signals.go                          # Per-marker detection (Limited View etc.)
│   │   └── detector_test.go
│   ├── contexts/                               # NEW — browser-context pool (DB-backed)
│   │   ├── store.go                            # CRUD on contexts table
│   │   ├── lifecycle.go                        # warming/ready/in-use/cooling/retired
│   │   └── store_test.go
│   ├── proxy/                                  # NEW — proxy router
│   │   ├── router.go                           # Decodo + Bright Data + mobile
│   │   ├── asn_policy.go                       # ASN-diversity enforcement
│   │   └── router_test.go
│   ├── cache/                                  # NEW — Valkey-backed cache
│   │   ├── place_cache.go                      # Place card 24h TTL
│   │   ├── review_cache.go                     # Reviews 7d TTL
│   │   └── cache_test.go
│   └── sidecar/                                # NEW — Connect-RPC client to Python sidecar
│       ├── client.go
│       └── client_test.go
├── proto/                                      # NEW — protobuf definitions
│   └── browser_fleet.proto                     # Tier 2 RPC contract
├── runner/webrunner/                           # MODIFY (gradually replace scrapemate path)
├── gmaps/                                      # MODIFY — retire cookies.json paths
│   ├── cookies.go                              # DELETE (after cutover)
│   └── job.go                                  # MODIFY to call fetcher
├── pkg/config/                                 # MODIFY — add sidecar URL, valkey URL, proxy config
├── scripts/migrations/                         # NEW migrations
│   ├── 20260513_create_contexts.sql
│   ├── 20260513_create_scrape_events.sql
│   ├── 20260513_create_tokens_audit.sql
│   └── 20260513_create_cache_warm.sql
└── docs/superpowers/plans/
    └── 2026-05-12-scraper-v2-architecture.md   # this file

brezelscraper-sidecar/                          # NEW — separate Python repo
├── pyproject.toml                              # uv/poetry
├── sidecar/
│   ├── main.py                                 # FastAPI admin + grpcio server entry
│   ├── pool/
│   │   ├── manager.py                          # Custom asyncio browser pool
│   │   ├── browser.py                          # Per-browser lifecycle
│   │   └── health.py                           # CDP health checks
│   ├── engines/
│   │   ├── patchright_engine.py                # CDP-leak route (pinned 1.56.0)
│   │   ├── camoufox_engine.py                  # Engine-fingerprint route (cloverlabs build)
│   │   └── nodriver_engine.py                  # CDP-minimal fallback
│   ├── fingerprint/
│   │   ├── store.py                            # Fingerprint store client
│   │   └── apply.py                            # Inject fingerprint into engine
│   ├── token/
│   │   └── mint.py                             # Mint, persist to Valkey
│   └── proto/                                  # Generated from .proto
├── Dockerfile                                  # Custom Camoufox + Patchright + Nodriver image
└── tests/

brezel-deobfuscation/                           # NEW — separate Python repo, internal-only
├── pyproject.toml
├── deobfuscation/
│   ├── watcher.py                              # jsmon-style /js/bg/{HASH}.js rotation tracker
│   ├── extractor.py                            # Bytecode opcode extraction
│   ├── signal_inventory.py                     # What does SearchGuard collect?
│   └── reports/                                # Per-version analysis reports (internal)
└── tests/

brezel-fingerprint-extension/                   # NEW — browser extension repo
├── manifest.json                               # MV3
├── src/
│   ├── consent.html                            # GDPR Art. 6(1)(a) consent UX
│   ├── collector.js                            # fingerprintjs-based collector
│   └── uploader.js                             # Sends to fingerprint store API
└── README.md
```

### 2.2 Workstream map and dependencies

```
Wave 1 (Weeks 1-4):     W1 Foundation ──┐
                                         │
Wave 2 (Weeks 3-6):     W2 Detector ─────┼──► (telemetry available)
                                         │
Wave 3 (Weeks 5-10):    W3 Sidecar T2 ───┼──► (browsers running)
                                         │
Wave 3 (Weeks 7-12):    W4 Tier 1 ───────┼──► (tokens being minted & consumed)
                        W7 Cache ────────┘
                                         │
Wave 4 (Weeks 9-16):    W5 Deobfuscation ─► (informs W3 fingerprint correctness)
                        W6 Proxy ──────────► (ASN policy + sticky sessions)
                        W8 Fingerprint ────► (synthetic first, harvest extension Phase 2)
                                         │
Wave 5 (Weeks 14-16):   W9 Cutover ──────► retire cookies.json
```

---

## Chunk 3 — Wave 1 (Foundation): PlaceFetcher Interface, DB Schema, Cookies Retirement Prep

### Task 1: PlaceFetcher interface definition

**Files:**
- Create: `internal/fetcher/fetcher.go`
- Create: `internal/fetcher/fetcher_test.go`
- Modify: `gmaps/job.go` (later — call site refactor)

- [ ] **Step 1.1: Write interface contract test first.** Define an in-memory mock fetcher; assert it returns a `Place` with expected fields and respects context cancellation. ~30 lines test code.
- [ ] **Step 1.2: Implement `PlaceFetcher` interface.**

**Module path correction:** the actual Go module per `go.mod` is `github.com/gosom/google-maps-scraper`. The canonical place struct in this codebase is `gmaps.Entry` (defined at `gmaps/entry.go:93`, ~50 fields incl. reviews/photos/popular_times) — there is no `models.Place` package. Return `*gmaps.Entry` directly to preserve compatibility with existing job code; if/when we add non-Maps fetchers, extract a neutral `models.Place` interface then.

```go
package fetcher

import (
    "context"
    "github.com/gosom/google-maps-scraper/gmaps"
)

type PlaceFetcher interface {
    Fetch(ctx context.Context, q Query) (*gmaps.Entry, error)
    Close() error
}

type Query struct {
    PlaceID        string
    URL            string
    IncludeReviews bool
    MaxReviews     int
    ReviewCursor   string  // pagination state for review continuation
}

type Option func(*config)

func WithCache(c Cache) Option            { return func(cfg *config) { cfg.cache = c } }
func WithDetector(d Detector) Option       { return func(cfg *config) { cfg.detector = d } }
func WithTier1(t Tier1Client) Option       { return func(cfg *config) { cfg.tier1 = t } }
func WithTier2Sidecar(s SidecarClient) Option { return func(cfg *config) { cfg.tier2 = s } }
func WithProxyRouter(p ProxyRouter) Option { return func(cfg *config) { cfg.proxy = p } }
```

- [ ] **Step 1.3:** Run test → PASS.
- [ ] **Step 1.4:** Commit `feat(fetcher): define PlaceFetcher interface with functional options`.

**Acceptance:** Mock implementation satisfies interface; tests cover context cancellation and error paths. **Effort: 1 day.**

### Task 2: Browser-context DB schema

**Note on filename convention:** project uses zero-padded sequential migrations (`000037_*` style), not date-prefixed. Reference: existing `scripts/migrations/000017_billing_system.up.sql`. Adopt the convention. Next sequential number to be determined by checking the directory at implementation time.

**Note on `fingerprints` FK:** the table doesn't exist yet (Wave 4 W8 Task 19 creates it). For Wave 1, ship `fingerprint_id UUID` *without* the FK constraint; add the FK in a follow-up migration after Task 19 ships. This avoids blocking Wave 1 on Wave 4 work.

**Files:**
- Create: `scripts/migrations/000037_create_browser_contexts.up.sql` + `.down.sql` (sequence number TBD at impl time)
- Create: `scripts/migrations/000038_create_scrape_events.up.sql` + `.down.sql`
- Create: `scripts/migrations/000039_create_tokens_audit.up.sql` + `.down.sql`
- Create: `internal/contexts/store.go`
- Create: `internal/contexts/store_test.go`
- Modify: `go.mod` — add `github.com/testcontainers/testcontainers-go` + `testcontainers-go/modules/postgres` (sqlmock cannot verify `SELECT FOR UPDATE SKIP LOCKED` semantics; real Postgres required for state-machine tests)

- [ ] **Step 2.0: Inventory existing migrations.** Run `ls scripts/migrations/` to determine next sequence number. Confirm `pgcrypto` extension is already enabled (`gen_random_uuid()` used elsewhere).

- [ ] **Step 2.1: Migration SQL (up + down).** "Context" is a logged-out browser configuration, not a Google identity.

```sql
-- 000037_create_browser_contexts.up.sql
CREATE TYPE IF NOT EXISTS context_state AS ENUM ('warming', 'ready', 'in_use', 'cooling', 'retired');

CREATE TABLE IF NOT EXISTS browser_contexts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engine          TEXT NOT NULL CHECK (engine IN ('patchright', 'camoufox', 'nodriver')),
    fingerprint_id  UUID,                       -- FK added in later migration after Wave 4 Task 19 creates fingerprints table
    proxy_endpoint  TEXT NOT NULL,
    proxy_asn       INTEGER,
    state           context_state NOT NULL DEFAULT 'warming',
    storage_state   JSONB,                       -- Playwright storage_state for session persistence
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ,
    cooling_until   TIMESTAMPTZ,                 -- after degraded scrape
    retired_at      TIMESTAMPTZ,
    retired_reason  TEXT,
    CONSTRAINT cooling_until_in_future CHECK (state != 'cooling' OR cooling_until > created_at)
);

CREATE INDEX IF NOT EXISTS idx_browser_contexts_state ON browser_contexts(state) WHERE state IN ('ready', 'in_use');
CREATE INDEX IF NOT EXISTS idx_browser_contexts_engine_state ON browser_contexts(engine, state);
```

```sql
-- 000037_create_browser_contexts.down.sql
DROP TABLE IF EXISTS browser_contexts CASCADE;
DROP TYPE IF EXISTS context_state;
```

```sql
-- 000038_create_scrape_events.up.sql
CREATE TABLE IF NOT EXISTS scrape_events (
    id              BIGSERIAL PRIMARY KEY,
    job_id          UUID NOT NULL REFERENCES jobs(id),  -- jobs.id is UUID per 000005_web_jobs_table.up.sql
    place_id        TEXT NOT NULL,
    tier            SMALLINT NOT NULL CHECK (tier IN (1, 2, 3)),
    context_id      UUID REFERENCES browser_contexts(id),
    token_id        UUID,
    proxy_asn       INTEGER,
    success         BOOLEAN NOT NULL,
    degraded        BOOLEAN NOT NULL DEFAULT false,
    duration_ms     INTEGER NOT NULL,
    bytes_egress    INTEGER,
    error_kind      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_scrape_events_job ON scrape_events(job_id);
CREATE INDEX IF NOT EXISTS idx_scrape_events_recent_degraded ON scrape_events(created_at DESC) WHERE degraded;
```

```sql
-- 000038_create_scrape_events.down.sql
DROP TABLE IF EXISTS scrape_events CASCADE;
```

Companion `000039_create_tokens_audit` follows the same pattern.

- [ ] **Step 2.2: Write store_test.go** — table-driven tests using testcontainers-go (real Postgres). Required test cases:
  - Basic CRUD per state
  - Atomic state transitions via `SELECT FOR UPDATE SKIP LOCKED`
  - Concurrent acquire-ready-context (2 workers, 1 row, exactly 1 wins)
  - State-transition invariants (cannot go `retired → ready`; `cooling_until` future when state is cooling — enforced by CHECK)
  - `cooling_until` expiry promotion back to `ready`

- [ ] **Step 2.3:** Implement `internal/contexts/store.go` with CRUD + atomic state transitions via `SELECT FOR UPDATE SKIP LOCKED`. Package import path: `github.com/gosom/google-maps-scraper/internal/contexts`.
- [ ] **Step 2.4:** Run migration locally; run tests → PASS.
- [ ] **Step 2.5:** Commit `feat(contexts): browser-context DB-backed pool, no Google identities`.

**Acceptance:** Migrations apply cleanly *and reverse cleanly*; tests pass against testcontainers Postgres; state-machine transitions atomic under concurrent load. **Effort: 2.5 days** (revised from 2 to account for testcontainers integration + down migrations).

### Task 3: Wire existing webrunner to PlaceFetcher facade (no behavior change)

**Architectural clarification:** `gmaps.GmapJob.BrowserActions(ctx, page) scrapemate.Response` is invoked *by scrapemate itself* as a callback — it receives a Playwright page from the engine. A facade cannot intercept the callback in-place without replacing the scrapemate engine. The `LegacyScrapemateFetcher` is therefore an **outer wrapper around `mate.Start()`**, not a per-page intercept. This is the correct integration boundary for "zero behavior change."

**Files:**
- Modify: `runner/webrunner/webrunner.go` line **394** (current `gmaps.SetCookiesFile(cookiesFile)` call site; **not** line 388 as earlier draft suggested)
- Modify: `gmaps/job.go` lines 312, 351 (cookie-injection sites; remain unchanged in Wave 1 — facade wraps the entry point, not the per-page callback)
- Modify: `pkg/config/config.go` — add `Fetcher FetcherConfig \`envPrefix:"FETCHER_"\`` matching existing `envPrefix` convention used by `GoogleConfig`
- Create: `internal/fetcher/legacy.go` — `LegacyScrapemateFetcher` wrapping `mate.Start()`

- [ ] **Step 3.0: Inventory all scrapemate entry points** before refactoring. Run `grep -rn "mate.Start\|scrapemate\.New" runner/ gmaps/` to list every entry point. Document each in a comment in `legacy.go`.

- [ ] **Step 3.1:** Add a `LegacyScrapemateFetcher` in `internal/fetcher/legacy.go` that wraps the current `mate.Start()` invocation behind the `PlaceFetcher` interface — pure facade, zero behavior change. Implementation: starts mate, sends one `GmapJob` via the input chan, reads one result from the output chan.

- [ ] **Step 3.2:** Add `FetcherConfig` to `pkg/config/config.go`:
```go
type FetcherConfig struct {
    Impl string `env:"IMPL" envDefault:"legacy"`  // "legacy" | "v2"
}
// ... in Config struct:
Fetcher FetcherConfig `envPrefix:"FETCHER_"`
```

- [ ] **Step 3.3:** In `runner/webrunner/webrunner.go`, replace direct `mate.Start()` usage with `fetcher.Fetch()` calls. The `gmaps.SetCookiesFile()` call at line 394 stays in Wave 1 (legacy path still uses it); retired in Wave 5.

- [ ] **Step 3.4:** Out-of-scope confirmation: `runner/databaserunner/`, `runner/lambdaaws/`, `runner/filerunner/` do NOT migrate to PlaceFetcher in Wave 1 — they continue using scrapemate directly. Document this explicitly.

- [ ] **Step 3.5:** Run full integration test suite (`go test ./...`); verify zero behavior change against existing test fixtures.

- [ ] **Step 3.6:** Commit `refactor(scraping): route through PlaceFetcher facade, legacy path unchanged`.

**Acceptance:** Existing production behavior unchanged; abstraction layer exists; `FETCHER_IMPL=legacy` is the only valid value at end of Wave 1. **Effort: 2.5 days** (revised from 2 to account for inventory step + config plumbing).

### Task 4: Cookies-file retirement prep (do not delete yet)

**Files:**
- Modify: `gmaps/cookies.go` — add deprecation warning log at LoadGoogleCookies()
- Modify: `pkg/config/config.go` — mark `GoogleConfig.CookiesFile` as deprecated in comment

- [ ] **Step 4.1:** Add `log.Warn("DEPRECATED: cookies.json path will be retired in v2; see docs/superpowers/plans/2026-05-12-scraper-v2-architecture.md")` at the load site.
- [ ] **Step 4.2:** Tag `google_cookies.json` itself with a comment-PR note that it's slated for deletion.
- [ ] **Step 4.3:** Do not delete yet — wait for cutover (Wave 5).
- [ ] **Step 4.4:** Commit `chore(cookies): mark static cookie path deprecated, retire in v2`.

**Acceptance:** Deprecation visible in logs and code. **Effort: 0.5 days.**

---

## Chunk 4 — Wave 2 (Detector + Telemetry)

### Task 5: Limited-View / degradation signal detector

**Files:**
- Create: `internal/detector/detector.go`
- Create: `internal/detector/signals.go`
- Create: `internal/detector/detector_test.go`
- Create: `testdata/fixtures/maps_html/` (real captured HTML — both clean and degraded)

- [ ] **Step 5.1: Capture fixtures.** Use existing scrape path to capture **50-100 clean HTML responses** across known-good places, save under `testdata/fixtures/maps_html/clean_*.html`. For degraded fixtures (Limited View was a Feb 2026 blip that Google rolled back, cannot reproduce on demand): sources are (a) Internet Archive Wayback captures of Feb 18-23 2026 Maps URLs, (b) synthetic stripping with documented transformation rules applied to clean fixtures, (c) any Limited-View variants still active in non-US locales — discover via canary scrapes from EU/APAC residential IPs. **Target: 20+ degraded fixtures.** Budget 2 days, not half-day. [REVISED per Chunk 4 review — original 5-10 degraded fixtures was undersized for ≥95% precision/recall acceptance.]
- [ ] **Step 5.2: Define signal markers** using a **section-presence checklist** with multiple fallback selectors plus text/structural heuristics per signal — NOT hardcoded named selectors. [REVISED per Chunk 4 review — selectors like `Pa9hud` and `[data-rrm]` cited in the spec dossier could not be verified externally as live May 2026 selectors; the Resoneo/SEL writeups don't publish them either.] Signals to detect (each via multiple fallbacks): review-block presence, review-count text, menu link, popular-times widget, photos carousel, hours, price level. The detector framework is data-driven (config-loadable signal definitions versioned per signal pack), with **hot-reload** so re-tuning doesn't require a deploy.
- [ ] **Step 5.3: Write detector_test.go** — table-driven over all fixtures, asserting per-signal flag accuracy. Target: ≥95% precision, ≥90% recall on degraded fixtures. **Plus a CI golden-file regression test:** any signal-pack change must replay against full fixture corpus before merge.
- [ ] **Step 5.4: Implement `Detect(html []byte) DetectionResult`** returning per-signal flags + a `Degraded bool` + reason + `degradation_score` (multi-signal weighted score, 0.0-1.0) for tuning the binary cutoff later without recapturing fixtures.
- [ ] **Step 5.5: Emit OTel metrics per current semconv guidance:** `scrape.responses{degraded=true|false, signal_class, tier, target_class, proxy_pool}` (counter; rename from `scrape.degraded_total` — units belong in metadata not name). Plus `scrape.degradation_score` (histogram). Avoid per-URL or per-account-ID labels (cardinality explosion).
- [ ] **Step 5.6: Detector self-health alert.** If `degraded_rate > 50%` over rolling 1h window, page on-call — this indicates the detector itself is broken (e.g., Google changed HTML and every page now looks "degraded"). Without this alarm, we silently corrupt the baseline.
- [ ] **Step 5.7:** Run tests, run linter, commit `feat(detector): Limited-View signal detection with fixture corpus`.

**Acceptance:** ≥95% precision on degraded fixtures; ≥90% recall; OTel metrics emitting per semconv; signals are config-loadable + hot-reloadable + version-tagged; detector self-health alert wired. **Effort: 5-6 days** [REVISED from 4-5 — fixture gathering takes 2 days not 0.5; hot-reload + golden-file CI adds ~1 day].

### Task 6: Wire detector into legacy fetcher (telemetry-only initially)

**Files:**
- Modify: `internal/fetcher/fetcher.go` — after every scrape, call detector before returning result; emit metric; do not yet act on result.

- [ ] **Step 6.1:** Add detector call to the legacy fetcher path.
- [ ] **Step 6.2:** Add Grafana dashboard panel for `scrape.degraded_total` by tier and signal.
- [ ] **Step 6.3:** Run for 48-72 hours in production; collect baseline degradation rate.

**Acceptance:** We have a real number for "what % of our current scrapes are returning Limited-View HTML." This is the metric every subsequent decision sizes against. **Effort: 1 day implementation + 3 days observation window.**

### Decision gate after Wave 2

**If baseline degradation < 5%:** confirm Limited-View remains the 5-day blip framing; deprioritize aggressive bypass work, focus on cost-engineering Tier 1.

**If baseline degradation 5-30%:** proceed with full plan as written.

**If baseline degradation > 30%:** escalate to CEO — implies Google has materially changed behavior since dossier was written; plan needs re-baselining before Wave 3 commitment.

---

## Chunk 5 — Wave 3 (Tier 2: Python Stealth Sidecar)

### Task 7: Sidecar repo scaffold + protobuf contract

**Files:**
- Create new repo: `brezelscraper-sidecar/`
- Create: `proto/browser_fleet.proto` (in main repo)
- Create: `brezelscraper-sidecar/pyproject.toml`
- Create: `brezelscraper-sidecar/Dockerfile`

- [ ] **Step 7.1: Define proto contract.**

```protobuf
syntax = "proto3";
package brezel.fleet.v1;

service BrowserFleet {
  rpc Fetch(FetchRequest) returns (FetchResponse);
  rpc MintToken(MintTokenRequest) returns (MintTokenResponse);
  rpc Health(HealthRequest) returns (HealthResponse);
  rpc PoolStatus(PoolStatusRequest) returns (PoolStatusResponse);
}

message FetchRequest {
  string url = 1;
  string engine_preference = 2;  // 'patchright' | 'camoufox' | 'nodriver' | 'auto'
  string fingerprint_id = 3;     // empty = auto-select
  string proxy_endpoint = 4;
  int32 timeout_ms = 5;
  bool include_screenshot = 6;
}

message FetchResponse {
  string html = 1;
  bytes screenshot = 2;
  string final_url = 3;
  map<string, string> cookies = 4;
  string attestation_token = 5;  // for token-mint flow
  int32 duration_ms = 6;
  string engine_used = 7;
  string context_id = 8;
}

// ... MintToken, Health, PoolStatus messages
```

- [ ] **Step 7.2: Generate Python and Go bindings.** Buf workflow recommended (`buf generate`).
- [ ] **Step 7.3: Container image** — base off `python:3.12-slim`, install `patchright==1.56.0` + `cloverlabs-camoufox` (NOT daijro mainline — see §1.3.2) + `nodriver` + `ghost-cursor` + `CDP-Patches` + `tf-playwright-stealth` + xvfb. Pin specific cloverlabs SHA (project still labels releases experimental). Container build CI re-verifies pins at each release.
- [ ] **Step 7.4:** Commit proto + scaffold.

**Acceptance:** Proto compiles; sidecar image builds. **Effort: 2 days.**

### Task 8: Asyncio browser pool manager

**Files:**
- Create: `brezelscraper-sidecar/sidecar/pool/manager.py`
- Create: `brezelscraper-sidecar/sidecar/pool/browser.py`
- Create: `brezelscraper-sidecar/sidecar/pool/health.py`

- [ ] **Step 8.1:** Model after the [browserless.io BrowserManager pattern](https://www.browserless.io/blog/scaling-browser-automation-architecture-1000-sessions). Per-browser lifecycle: spawn → health-check (via CDP) → ready → in-use → cooling → terminate. 30-60 min TTL per browser (matches fingerprint coherency window).
- [ ] **Step 8.2: Critical health invariant** — "process alive ≠ browser healthy." Health check pings CDP `Target.getTargets` every 30s; PID-only checks are insufficient.
- [ ] **Step 8.3: Memory budget** — Camoufox idle ~40MB, ~300MB binary, ~150-300MB RSS active. Per node: 20-50 browsers, 8-16GB RAM, `shm_size: 4g` (pool mode).
- [ ] **Step 8.4:** Tests: load 50 browsers, kill 10 at random, assert manager replaces them within 5s; assert OOM-killed browser triggers replacement.

**Acceptance:** 50 concurrent browsers run stably for 4+ hours; chaos-kill 10 random workers and pool self-heals in <5s. **Effort: 5-6 days.**

### Task 9: Camoufox engine (engine-fingerprint route)

**Why this is one of two co-equal Tier-2 engines, not "secondary":** see §1.3. Camoufox is the right tool when target detection is engine-fingerprint-bound (Canvas/Audio/WebGL/Font/Navigator coherence) — its C++ source patches defeat the entire descriptor-introspection class of detection that Patchright passes through.

**Files:**
- Create: `brezelscraper-sidecar/sidecar/engines/camoufox_engine.py`

- [ ] **Step 9.1: Pin cloverlabs build, not daijro mainline.** `uv add cloverlabs-camoufox` (NOT `camoufox`). daijro v135 mainline is functionally dead for Google per maintainer admission in [#570](https://github.com/daijro/camoufox/issues/570). Recheck at start of every sprint whether cloverlabs is still the recommended fork.
- [ ] **Step 9.2:** Implement engine interface (start, fetch, mint_token, stop). Use `Camoufox(headless=False, ...)` — headless is burned per [#46](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/46) cross-reference and [#505](https://github.com/daijro/camoufox/issues/505) owner thread.
- [ ] **Step 9.3:** Apply fingerprint via Camoufox's BrowserForge integration. BrowserForge generates Bayesian-sampled fingerprints from real-world distributions — coverage is navigator/screen/WebGL/fonts/headers, NOT TLS/JA4 (Camoufox inherits Firefox-of-build version), NOT canvas pixel noise.
- [ ] **Step 9.4: Bolt on ghost-cursor humanizer.** Camoufox does not ship behavioral simulation; Welford-variance gate fails without it. Wrap mouse moves with bezier curves; scroll with variable cadence; dwell randomization on click.
- [ ] **Step 9.5: Patch Camoufox-known holes ourselves.**
  - Canvas 2D: inject `toDataURL`/`getImageData` noise via init script (Camoufox doesn't patch this; see §1.3 holes list)
  - Verify `navigator.platform`/`hardwareConcurrency`/`oscpu` actually apply (per [#516](https://github.com/daijro/camoufox/issues/516), config can silently fail to override)
  - WebRTC: add explicit `media.peerconnection.ice.no_host` user.js if cloverlabs build hasn't already
- [ ] **Step 9.6: Test against Detector** — known-good Maps URL, 50 runs through Decodo residential. Track success rate. Hard expectation: ≥80% clean HTML rate. If <70%, escalate and audit.

**Acceptance:** ≥65% clean HTML rate over 50 runs against known-good place via residential proxy + cloverlabs build + ghost-cursor + canvas patch. **Effort: 7-8 days.** [REVISED 2026-05-12 per meta-verification — original 80% threshold was aspirational; realistic with Camoufox #388 still open + #516 fingerprint-override leaks + Firefox cohort tax is 60-75%. We can re-tighten the threshold once the canary rig (Task 11.5) gives us real numbers; gating Wave 5 on 80% would block cutover indefinitely.]

### Task 10: Patchright engine (CDP-leak route)

**Why this is one of two co-equal Tier-2 engines, not "primary":** see §1.3. Patchright is the right tool when target detection is CDP-leak-bound (Runtime.enable, isolated worlds, automation flags, navigator.webdriver) — it structurally removes those leaks. It does NOT touch engine-fingerprint surfaces.

**Files:**
- Create: `brezelscraper-sidecar/sidecar/engines/patchright_engine.py`

- [ ] **Step 10.1: Pin patchright==1.56.0.** `uv add patchright==1.56.0`. Latest versions shipped a CDP-detectability regression per [#94](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/94) + [#161](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright/issues/161) that broke Google logins. Maintainer-directed pin. Subscribe to release notifications to know when fix lands.
- [ ] **Step 10.2:** Implement engine interface using `patchright` package. Use `channel="chrome"` (real Chrome, not Chromium) per [#19](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/19) maintainer requirement. Use `headless=False` (xvfb-backed in container) — [#46](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python/issues/46) confirms headless on Google = burned.
- [ ] **Step 10.3: Bolt on CDP-Patches addon for `isTrusted`.** Patchright README explicitly redirects to this library for OS-level input injection. Without it, `MouseEvent.isTrusted=false` on every dispatched event, which SearchGuard's behavioral gate catches.
- [ ] **Step 10.4: Bolt on `ghost-cursor` for trajectory humanization.** CDP-Patches fixes the `isTrusted` flag at OS-input level; it does NOT generate bezier paths, dwell randomization, or variable scroll cadence. SearchGuard's Welford-variance + reservoir-sampling behavioral signals catch constant-velocity paths even with `isTrusted=true`. Both layers required: CDP-Patches for the flag, ghost-cursor for the trajectory shape.
- [ ] **Step 10.5: Apply fingerprint via `context.add_init_script()`.** Patchright doesn't patch fingerprint surfaces (Canvas, WebGL, Audio, Fonts all passthrough). This means our init script is doing real work, not redundancy. **W8 dependency posture:** Workstream 8 (synthetic fingerprint generator) is Wave 4 weeks 9-16; Task 10 is Wave 3 weeks 5-10. **Mitigation:** Task 10 ships with a stubbed in-repo fingerprint set (10-20 hand-curated init scripts covering common Chrome 144 on Win/macOS configurations). W8 phase-1 synthetic generator replaces the stub when available without Task 10 code changes (interface is `fingerprint_store.get(engine='patchright')`).
- [ ] **Step 10.6:** Apply `tf-playwright-stealth` (Mattwmaster58 fork — pin to commit SHA at adoption time; project has no semver releases) as additional JS-layer shim on top — covers a few residual leaks Patchright doesn't.
- [ ] **Step 10.7: Test against Detector** — Google search-then-navigate flow, 50 runs through Decodo residential + Chrome channel + headed (xvfb-backed). Hard expectation: ≥80% clean HTML rate.

**Acceptance:** ≥65% clean HTML rate over 50 runs against Google search → Maps result flow via pinned 1.56.0 + Chrome channel + headed + CDP-Patches + ghost-cursor + residential proxy. **Effort: 7-8 days.** [REVISED 2026-05-12 per meta-verification — original 80% threshold was aspirational; Patchright #135 (Oct 2025, open) "Cannot skip Google search recaptcha" + ZenRows benchmark capping at ~67% headless detection reduction + 1.56.0 pin being a hold-line not a green light all point to 60-70% realistic.]

### Task 11: Nodriver engine (CDP-minimal fallback route)

**Files:**
- Create: `brezelscraper-sidecar/sidecar/engines/nodriver_engine.py`

- [ ] **Step 11.1:** Implement using `nodriver` package. Drives real Chrome stable install with own minimal CDP impl — cleanest JA4 (matches Chrome stable verbatim) and smallest CDP attach footprint of the three. **Headed mode mandatory, xvfb-backed in container** (same constraint as Camoufox and Patchright; headless on Google = burned across all engines per §1.3).
- [ ] **Step 11.2:** Bolt on ghost-cursor humanizer (same Welford-variance / behavioral concern). isTrusted is less of a concern here than Patchright because Nodriver's input dispatch differs, but trajectory humanization is still required.
- [ ] **Step 11.3:** Apply fingerprint via init script (no engine patches). Same W8 stub-then-swap posture as Task 10 Step 10.5.
- [ ] **Step 11.4: Test against Detector** — same 50-run methodology as Tasks 9.6 / 10.7 through Decodo residential + headed (xvfb-backed). Hard expectation: ≥70% clean HTML rate (lower than Camoufox/Patchright because this is the fallback route).

**Acceptance:** ≥55% clean HTML rate over 50 runs against known-good place via headed + ghost-cursor + residential proxy. **Effort: 4 days.** [REVISED 2026-05-12 per meta-verification — Nodriver is the fallback tier so threshold is lowest by design; 55% reflects that it's a hedge engine, not a primary.]

### Task 11.5: Continuous canary benchmark rig

**Why new:** §1.3.3 — no public head-to-head benchmark exists. We must run our own continuously.

**Files:**
- Create: `brezelscraper-sidecar/sidecar/canary/runner.py`
- Create: `brezelscraper-sidecar/sidecar/canary/places.yml` — fixed set of canary URLs

- [ ] **Step 11.5.1:** Fixed canary list — 20-50 Google Maps URLs across regions, place types, languages. Stored as `canary/places.yml`, version-controlled, reviewed quarterly.
- [ ] **Step 11.5.2:** Hourly cron runs each engine (Camoufox, Patchright, Nodriver) against each canary URL through a known proxy. Per-run: success/degraded/error + duration + bytes. **Each canary HTML response passes through the Detector (Wave 2 Task 5)** to produce per-signal-class labels (engine-fingerprint vs CDP-leak vs behavioral vs unknown).
- [ ] **Step 11.5.3:** Emit OTel metric `canary.success_rate{engine,target,signal_class}`; alert if any engine drops >10% week-over-week.
- [ ] **Step 11.5.4: Wire canary into the Router (Task 16) control loop.** Canary results write to the same per-target / per-engine success-rate store that the Acquisition Router reads. Initial seed rules from §1.3.1 are subject to override by canary-derived rules within 14 days of pilot. Without this wiring, Step 11.5 is observability-only theatre; with it, the canary is the ground-truth feed that informs routing.
- [ ] **Step 11.5.5:** Per-engine pass-rate panel on Grafana — this is our internal benchmark, surfaced to CTO weekly.

**Acceptance:** (a) Canary runs hourly with Detector-labeled outcomes; (b) results wired into Router store; (c) **all three engines achieve their target threshold (Camoufox ≥65%, Patchright ≥65%, Nodriver ≥55%) on canary URLs for 7 consecutive days** before Wave 3 is declared complete. Without this acceptance, Wave 5 (Cutover) does not start. **Effort: 4 days.** [REVISED 2026-05-12 — thresholds lowered per meta-verification.]

---

## Chunk 6 — Wave 3 (Tier 1: HTTP Replay) and Wave 3 (Cache)

### Task 12: azuretls + utls HTTP client wrapper

**Files:**
- Create: `internal/tier1/client.go`
- Create: `internal/tier1/client_test.go`

- [ ] **Step 12.1:** Pin `Noooste/azuretls-client` v1.13.2+ and `refraction-networking/utls` v1.8.2+ in `go.mod`.
- [ ] **Step 12.2:** Wrap azuretls with Chrome 144 profile preset. Customize HTTP/2 SETTINGS frame order, INITIAL_WINDOW_SIZE, pseudo-header order to match Chrome stable.
- [ ] **Step 12.3:** Test: hit https://tls.peet.ws/api/all from the wrapper and assert JA4 matches Chrome stable. Hit https://tools.scrapfly.io/api/fp to verify HTTP/2 fingerprint.
- [ ] **Step 12.4:** Commit `feat(tier1): azuretls Chrome 144 impersonation wrapper`.

**Acceptance:** JA4 matches Chrome current stable; HTTP/2 fingerprint passes Scrapfly's tester. **Effort: 3 days.**

### Task 13: Token mint pool (Valkey-backed)

**Files:**
- Create: `internal/tokens/pool.go`
- Create: `internal/tokens/lifecycle.go`
- Create: `internal/tokens/pool_test.go`

- [ ] **Step 13.1:** Pin `valkey-io/valkey-go` v1.0.74+. Provision a Valkey instance locally for tests (Docker `valkey/valkey:8.0`).
- [ ] **Step 13.2:** Schema: sorted set per (proxy_asn, fingerprint_cohort), score = expiry timestamp, member = `token_id`. Hash per token: `token:{id}` storing attestation, cookie jar, mint metadata, TTL.
- [ ] **Step 13.3:** Operations: `Mint(token, ttl)` → `ZADD` + `HSET`; `Lease()` → `ZRANGEBYSCORE` for fresh tokens, atomic `ZPOPMIN`; `Consume(id)` → mark used; `Expire()` periodic Lua → `ZREMRANGEBYSCORE`.
- [ ] **Step 13.4:** Persist mint events to Postgres `tokens_audit` table for forensic replay.
- [ ] **Step 13.5:** Test: 10k concurrent leases against pool of 100 tokens with 15-min TTL; assert no double-leases.

**Acceptance:** 10k goroutines lease without race; expired tokens evicted within 1s of TTL. **Effort: 4 days.**

### Task 14: Tier-1 replay against Maps RPC

**Files:**
- Create: `internal/tier1/replay.go`
- Create: `internal/tier1/parser.go`

- [ ] **Step 14.1: Spike first** — before full build, do a 2-day spike to verify replay actually works against Maps. Mint a token via Tier 2 (manual driving of Patchright), capture full cookie jar + attestation token, replay via azuretls against the Maps detail RPC endpoint. **If replay fails (Google ties tokens cryptographically to client IP/fingerprint at mint time), Tier 1 is not viable and the plan needs revision.**
- [ ] **Step 14.2:** Assuming spike succeeds: implement `Replay(ctx, place, token) → response`.
- [ ] **Step 14.3:** Parse Maps RPC response (mix of HTML + protobuf-like batchexecute envelope). Reference `dredozubov`'s PR #207 in `gosom/google-maps-scraper` for the cookie-context fetch pattern (we adapt: instead of `page.Evaluate()` fetch, we use captured cookies + IP-matching proxy).
- [ ] **Step 14.4:** Test: 100 sequential replays through Decodo residential, assert ≥95% success rate.

**Acceptance:** Replay path returns same data as Tier 2 browser for the same place at ≥95% parity. **Effort: spike 2 days + build 5 days.**

### Task 15: Valkey cache layer

**Files:**
- Create: `internal/cache/place_cache.go`
- Create: `internal/cache/review_cache.go`
- Create: `internal/cache/cache_test.go`

- [ ] **Step 15.1:** Schema: `place:v{scraper_version}:{place_id}` → Valkey hash with 24h TTL. Versioned keys for invalidation on parser change.
- [ ] **Step 15.2:** Reviews: `reviews:v{scraper_version}:{place_id}` → Valkey hash with 7d TTL. Reviews change slowly; this is the main cost lever.
- [ ] **Step 15.3:** Use valkey-go's auto-pipelining + client-side caching for read-heavy path.
- [ ] **Step 15.4:** Postgres `cache_warm` table as tier-2 cache (queryable for analytics, survives Valkey flush). S3 cold archive for compliance (>30 day data).
- [ ] **Step 15.5:** Wire cache lookup at top of `fetcher.Fetch()`. Target 60-80% L1 hit rate.

**Acceptance:** Cache hit returns in <5ms p95; miss path measured separately. **Effort: 4 days.**

### Task 16: Wire all tiers into router

**Files:**
- Create: `internal/fetcher/router.go`
- Modify: `internal/fetcher/fetcher.go`

- [ ] **Step 16.1:** Implement tier selection: cache → Tier 1 (if fresh token available) → Tier 2 (if Tier 1 fails or no token) → Tier 3 (if Tier 2 returns degraded). Configurable per-tenant cost/quality knob.
- [ ] **Step 16.2:** Emit per-tier success metrics. Adaptive routing: temporarily downweight Tier 1 if success rate drops below threshold in a 5-min window.
- [ ] **Step 16.3:** Behind feature flag — `FETCHER_IMPL=v2` for one canary tenant first.

**Acceptance:** Canary tenant runs entirely on v2 with success rate ≥ legacy path. **Effort: 3 days.**

---

## Chunk 7 — Wave 4 (Cross-Cutting: Deobfuscation, Proxy, Fingerprints)

### Task 17: SearchGuard deobfuscation pipeline (separate repo)

**Files:**
- Create new repo: `brezel-deobfuscation/` — internal-only, do not publish publicly (Google v. SerpApi §1201 exposure)

- [ ] **Step 17.1: Script rotation watcher.** ~~Adapt `robre/jsmon`~~ — **`robre/jsmon` is unmaintained since 2020-10-01 (verified May 2026)**. Build a minimal in-house watcher instead: cron job (every 10 min) fetches `//www.google.com/js/bg/*.js` against a header-rotating Chrome User-Agent + residential proxy; SHA-256 hash compared against last-known; on change, fire Slack alert + queue extraction job + commit script to internal `brezel-deobfuscation` private repo for diff history. Effort: ~1 day. Tool dependency removed.
- [ ] **Step 17.2: Bytecode extractor.** Primary reference: **Resoneo's Jan 2026 v41 analysis** (`think.resoneo.com/botguard-google/`) — the only deep technical writeup with current opcode mapping. Secondary references: `dsekz/botguard-reverse` (README-level only, last touched 2025-09-15) and `notemrovsky/tiktok-reverse-engineering` (best public template for bytecode-VM analysis methodology, last commit 2025-12-07). Output: opcode table for current version. **Expect this to be the slowest part of Task 17**; 512-register VM with rotating ARX constants does not yield to standard JS deobfuscators (webcrack, restringer, synchrony all confirmed non-applicable per verification).
- [ ] **Step 17.3: Signal inventory.** For each script version, produce a structured report: what signals does this version collect? (navigator props, screen, WebGL, audio, behavioral metrics, timing thresholds). Resoneo's reference identifies mouse-velocity-variance threshold 10, key-press-duration variance 5ms, scroll-delta variance 5px, event-rate 200/sec.
- [ ] **Step 17.4: Tier-2 validator.** Inventory feeds an automated test: spin up Patchright, drive a canary scrape, assert every signal SearchGuard collects has a plausible value emitted. Drift detection: if a new signal is collected and our browsers don't emit it correctly, alert.
- [ ] **Step 17.5: Operational risk mitigation.** Repo is internal-only, access-controlled, no publishing of opcode tables or full deobfuscated payloads. Discussed with legal counsel.

**Acceptance:** Watcher catches rotations within 10 min; per-version signal inventory generated automatically; Tier-2 validator flags any new signal within one rotation cycle. **Effort: ~5-7 weeks senior RE engineer** (revised from 3 — reviewer flagged the original estimate as optimistic given Resoneo took multiple researchers months to produce v41 analysis; reverse engineers at this tier don't produce maintained opcode tables for continuously-rotated VMs in 15 working days). Realistic deobfuscation cadence assumption: useful analysis window per script rotation is ~3 days, not 14. Plan deobfuscation pipeline for **continuous integration loop**, not one-shot reverse.

### Task 18: Proxy router with ASN-diversity policy

**Files:**
- Create: `internal/proxy/router.go`
- Create: `internal/proxy/asn_policy.go`

- [ ] **Step 18.1:** Multi-vendor router: Decodo (default, 70-80% of traffic), Bright Data residential (premium tier, 15-25%), mobile carriers (Bright Data mobile or Soax, 5%, token-mint warming only).
- [ ] **Step 18.2:** Sticky session windowing: 10-30 min sticky to match context lifetime (W2 contexts pinned to one IP for their TTL).
- [ ] **Step 18.3:** ASN-diversity policy: cap egress through any single ASN at <N% (configurable, target 15%). Track per-ASN success rate; auto-deprioritize underperforming ASNs.
- [ ] **Step 18.4:** Cost accounting per-request, emit OTel metric, expose in /api/v1/dashboard.

**Acceptance:** Router enforces ASN cap; per-ASN success rate visible in Grafana; cost-per-scrape metric live. **Effort: 5 days.**

### Task 19: Synthetic fingerprint generator (Phase 1 of W8)

**Files:**
- Create: `brezelscraper-sidecar/sidecar/fingerprint/store.py`
- Create: `brezelscraper-sidecar/sidecar/fingerprint/synthetic.py`
- Create: `internal/fingerprints/store.go` (Go-side store API)

- [ ] **Step 19.1: Schema** — Postgres `fingerprints` table: id, source (`synthetic`/`harvested`/`vendor`), engine_target (`patchright`/`camoufox`/`nodriver`), payload (JSONB with canvas, WebGL, audio, fonts, navigator props, screen, timezone, languages, hardware concurrency, device memory).
- [ ] **Step 19.2: Adopt BrowserForge as the generator (NOT build from scratch).** [`daijro/browserforge`](https://github.com/daijro/browserforge) is actively maintained (last commit 2026-02-26, Apache 2.0), uses a Bayesian generative network that samples from real-world distributions covering navigator/screen/WebGL/fonts/headers. **Camoufox already integrates BrowserForge natively** (see Task 9.3); we adopt it for Patchright and Nodriver paths too. Integration approach: install `browserforge` PyPI package; call `Fingerprint.from_browser_specs(...)` per session; persist generated fingerprint into our `fingerprints` table for reuse and audit. Effort drops from 6-7 days to ~4 days integration + tuning.
- [ ] **Step 19.3: Consistency hardening on top of BrowserForge.** BrowserForge samples are internally consistent within their model but don't enforce *cross-coordinate* consistency: canvas hash ↔ GPU vendor, font list ↔ OS, timezone ↔ proxy egress geo. Add a post-generation validator that enforces these — reject samples where they don't align.
- [ ] **Step 19.4: Validation harness** — spin up Patchright with each generated fingerprint, navigate to a self-hosted CreepJS instance, assert score ≥ 75. Reject fingerprints that fail.
- [ ] **Step 19.5: Seed pool** with 1000-10000 validated fingerprints. Refresh quarterly when Chrome ships new APIs. **W3 Wave 3 stub-then-swap (per Task 10.5 dependency posture):** ship 10-20 hand-curated init scripts for Tier-2 Patchright/Nodriver in Wave 3; replace with this generator output in Wave 4 without engine code changes.

**Acceptance:** 1000+ fingerprints in store, 95%+ pass CreepJS at threshold ≥ 75, cross-coordinate consistency enforced. **Effort: 4-5 days** (revised from 6-7 to reflect BrowserForge adoption).

### Task 20: Consensual fingerprint harvest extension (Phase 2 of W8 — defer to Q3)

**Files:**
- Create new repo: `brezel-fingerprint-extension/`

- [ ] **Step 20.1:** Manifest V3 browser extension. Consent flow modeled on Hola SDK legal template. GDPR Art. 6(1)(a) explicit opt-in. CCPA opt-out + sale disclosure.
- [ ] **Step 20.2:** Collector based on `fingerprintjs` (MIT license, no derivative restrictions). Uploads to `fingerprints` table via authenticated API.
- [ ] **Step 20.3:** Distribution: bundle in a free utility we offer (e.g., a Maps-data quality checker), or pay $0.50 one-time incentive (modeled on IPRoyal Pawns / EarnApp economics).
- [ ] **Step 20.4:** Legal review with counsel before launch. CAC budget approval needed.

**Acceptance:** N/A in initial scope — Phase 2 work, schedule for Q3 2026 if Phase 1 synthetics show cohort-detection issues. **Effort: 4 weeks (Q3).**

---

## Chunk 8 — Wave 5 (Cutover, Operational Concerns, Risks, Open Decisions)

### Task 21: Per-tenant gradual cutover

**Files:**
- Modify: `pkg/config/config.go` — add `FetcherV2Rollout` map for per-tenant feature flag
- Modify: `internal/fetcher/router.go`

- [ ] **Step 21.1:** Rollout list-based: start with internal test tenant only, expand to one paying canary, then 10%, 50%, 100%.
- [ ] **Step 21.2: Per-tenant success-rate gating with full hysteresis rules:**
  - **Trigger:** v2 success rate < legacy success rate × 0.95
  - **Minimum sample size:** 500 scrapes per tenant before the rule can fire (prevents early-noise reverts)
  - **Time window:** rolling 24h comparison
  - **Auto-revert:** triggers feature flag flip to `FETCHER_IMPL=legacy` for that tenant
  - **Re-promote rule:** manual only, requires CTO sign-off + 48h of legacy-side issues OR explicit v2 fix deploy
  - **Anti-oscillation:** once auto-reverted, tenant locked out of v2 for 7 days minimum
- [ ] **Step 21.3:** 2-week observation window per cohort.

**Acceptance:** 100% of tenants on v2 with success rate ≥ legacy parity × 0.95 sustained over 2 weeks. **Effort: 4 weeks rollout calendar time, ~3 engineer-days of actual work.**

### Task 22: Retire `google_cookies.json`

**Files (complete list — reviewer-verified May 2026):**
- Delete: `google_cookies.json` at **repo root** (after 100% cutover + 2 weeks parity)
- Delete: `gmaps/cookies.go`
- Modify: `pkg/config/config.go` — remove `GoogleConfig.CookiesFile`
- Modify: `runner/webrunner/webrunner.go` lines **394-397** — remove `gmaps.SetCookiesFile()` call and `google_cookies_configured`/`google_cookies_not_configured` log lines
- Modify: `gmaps/job.go` lines 312, 351 — remove `InjectCookiesIntoPage(page)` call sites
- Modify: `.env.example` line **156** — remove `GOOGLE_COOKIES_FILE=/path/to/google_cookies.json`
- Modify: `docker-compose.staging.yaml` line **16** — remove `GOOGLE_COOKIES_FILE` env default
- Modify: `docker-compose.staging.yaml` line **29** — remove bind-mount `./google_cookies.json:/app/google_cookies.json:ro`
- Audit: run `grep -rn "google_cookies\|GOOGLE_COOKIES\|LoadGoogleCookies\|SetCookiesFile\|CookiesFile\|InjectCookiesIntoPage" .` and remove any remaining references

- [ ] **Step 22.1:** Remove file from disk + repo. The Google account it was exported from is considered burned; do not re-export.
- [ ] **Step 22.2:** Delete all loading code (`gmaps/cookies.go`).
- [ ] **Step 22.3:** Update `.env.example` and `docker-compose.staging.yaml`.
- [ ] **Step 22.4:** Run audit grep; ensure zero references remain.
- [ ] **Step 22.5:** Squash-commit `chore(cookies): retire static cookies.json after v2 cutover complete`.

**Acceptance:** Audit grep returns no matches; integration tests pass without the file. **Effort: 1 day.**

### Task 23: GDPR build work (parallel to v2 build)

**Files:**
- Modify: `web/handlers/*` — add Art. 14 transparency notice on reviewer data
- Create: `web/handlers/data_deletion.go` — DSAR + opt-out endpoint
- Create: `docs/legal/lia-google-maps.md` — Legitimate Interest Assessment document

- [ ] **Step 23.1:** LIA document for EU/EEA reviewer data scraping under Art. 6(1)(f). Three-step structure: legitimate interest / necessity / balancing.
- [ ] **Step 23.2:** Art. 14 notice — when reviewer data is first stored, log a notice obligation event; surface via dashboard.
- [ ] **Step 23.3:** Hash/redact reviewer names + photos at storage time (data minimization).
- [ ] **Step 23.4:** Implement deletion-request endpoint: `POST /api/v1/privacy/delete` with email-verification flow.

**Acceptance:** LIA documented; Art. 14 notice mechanism live; deletion endpoint round-trips. **Effort: 2 engineer-weeks (parallel).**

### Task 24: Marketing/comms discipline

**Files:**
- Create: `docs/internal/external-comms-policy.md`

- [ ] **Step 24.1:** Draft policy: no public blog posts, LinkedIn copy, sales decks, job listings, or conference talks that claim to bypass Google's defenses. Marketing language is "structured access to publicly available business listings." Period.
- [ ] **Step 24.2:** Distribute to marketing, sales, recruiting. Sign-off required from CEO before any external-comms touches scraping topic.

**Acceptance:** Policy distributed and acknowledged by relevant leads. **Effort: 0.5 days.**

### 24.1 Operational concerns we own once this is live

- **On-call rotation** for the deobfuscation pipeline. When `/js/bg/{HASH}.js` rotates and our signal inventory is stale, scrapes start failing within hours. Page-out path required.
- **Stealth-tool watch list (NEW per Chunk 1 reviewer feedback).** Subscribe to GitHub release notifications for: daijro/camoufox (whether upstream v150+ stabilizes and becomes preferred over cloverlabs fork), cloverlabs-camoufox (whether the fork stays fresh against new Firefox releases), Kaliiiiiiiiii-Vinyzu/patchright (post-exam-hiatus return; resolution of Dec 2025 CDP regression → release that unpins 1.56.0), Noooste/azuretls-client (Chrome stable tracking), refraction-networking/utls (CVEs). Sprint kickoff includes 15-min watch-list review. Triage process documented.
- **Capacity planning.** Token-mint pool depth needs to track customer demand; under-provisioning fails Tier 1 → falls back to Tier 2 → cost spikes. Auto-scaling rules + budget alerts.
- **Cost dashboards.** Per-tenant per-tier cost-per-successful-scrape. Surface in CEO dashboard.
- **Browser pool memory monitoring.** Camoufox/Patchright memory leak history is real; aggressive RSS thresholds + auto-restart. 24h soak test required before Wave 3 acceptance (per Chunk 5 reviewer recommendation).
- **Postgres connection budget.** With scrapemate dropped and connection pooling under our control, set sane defaults.

### 24.2 Hiring (the load-bearing decision)

- **1× Senior Adversarial-Engineering Lead.** This is the highest-leverage hire. Profile: ex-Bright Data / ex-Multilogin / ex-DataDome research / ex-FAANG anti-abuse / CTF reverse-engineering. Owns SearchGuard deobfuscation + fingerprint engineering + the arms race. Budget $400-600K total comp.
- **1× Senior Backend Engineer (Go).** Owns Wave 1, 2, parts of 4, 6. Existing repo experience preferred.
- **1× Senior Backend Engineer (Python).** Owns sidecar (Waves 3, 5). Browser-automation production experience required.
- **1× Mid-level Engineer (full-stack).** Cache + telemetry + proxy router + GDPR pieces.
- **0.5 FTE ongoing (post-launch)** for deobfuscation maintenance + fingerprint refresh + arms-race chase.

**Talent pool for the lead role is small.** Candidates cluster around Cypa (dsekz), LuanRT, glizzykingdreko, voidstar0, notemrovsky, j4k0xb on GitHub, plus the Scraping Enthusiasts Discord. Plan for 3-month search if going from cold start.

### 24.3 Risks (technical, not legal — legal is CEO's call)

1. **Tier-1 replay viability.** If Google ties tokens cryptographically to client IP/fingerprint at mint time (Resoneo's signal inventory suggests this is plausible; tomkabel/google-botguard-security-research notes defenders SHOULD do this), Tier 1 collapses and we lose the unit-economics advantage. **Spike in Wave 3 Task 14.1 must validate this before full Tier-1 build.** If spike fails: re-baseline plan (Tier 2 covers all volume, costs are 20-50× higher per scrape, cache hit rate becomes existential). **Budget contingency: if Tier-1 collapses, proxy spend reforecasts to ~$50-100K/yr at scale; total budget envelope rises by ~$100-200K.**
2. **Detector drift.** Limited-View markers will shift as Google updates the page structure. Detector framework is data-driven (configurable signals) to enable rapid response; budget 0.5 engineer-week per quarter for re-tuning.
3. **Stealth-tool fingerprinting at the fleet level.** Even with per-browser fingerprint variance, if Camoufox/Patchright's underlying engines get cohort-detected, success rate drops across the fleet simultaneously. Portfolio approach (3 engines) is the mitigation; if all 3 fail at once, we're in arms-race-loss territory and the playbook is "ship a new engine in the portfolio within X days."
4. **ASN burn.** Residential proxy ASNs get blacklisted; provider rotates pools, but rotation lags. Multi-vendor (Decodo + Bright Data + Soax) is the mitigation.
5. **Camoufox upstream disappearing again.** daijro has had health issues; v2.0 resumed Mar 2026 but durability is unknown. We pin cloverlabs fork for resilience; Patchright track is the Chromium hedge.
6. **EU enforcement.** CNIL fined KASPR €240K (Dec 2024) for LinkedIn scraping; Polish DPA fined €220K for no Art. 14 notice. Our GDPR build work (Task 23) is the mitigation; without it we're materially exposed for EU reviewer data.
7. **Legal action against Brezel directly.** Google v. SerpApi precedent shows §1201 plus tortious-interference is a credible attack pattern. Risk of being named in a similar suit is non-zero given our architectural posture (per CEO direction §-1). Mitigation: E&O / cyber-liability insurance pre-launch; legal counsel on retainer; runway buffer for defense costs. **Surface as D8 (see §24.4).**
8. **Google blocks entire residential ASN ranges we depend on.** ASN burn (R4) is per-pool; ASN-range bans are structural and would force vendor switching mid-cycle. Multi-vendor mitigation partially addresses but cannot fully prevent.
9. **Single-vendor proxy outage or coordinated takedown.** Multi-vendor is the mitigation per R4 (Decodo + Bright Data + Soax), but the risk of a coordinated abuse-team takedown across vendors (rare but happened to Luminati customers in 2020) is distinct. Plan for graceful degradation with prioritized customer fairness queue.
10. **Senior RE lead bus-factor.** Deobfuscation pipeline + arms-race maintenance is 0.5 FTE on one person initially. Mitigation: pair-program with a mid engineer during Wave 4 (4 weeks) to build a second deobfuscation operator; contract a second RE specialist on retainer for surge capacity.
11. **Device-integrity attestation emergence.** May 2026 reCAPTCHA Play Services attestation requirement (cybersecuritynews.com) signals Google may roll out WebAuthn/TPM-style device attestation for web flows by Q3 2026. If this happens, the entire Tier-2 portfolio approach degrades; current plan has no answer. **Mitigation: Wave-2.5 research spike (1 engineer-week) to scope what an attestation-bypass roadmap would look like; track Google's reCAPTCHA Play Services and WebAuthn rollouts as canary indicators.**

### 24.4 Open decisions for CTO/CEO discussion

- [ ] **D1 — Tier 1 spike outcome.** Before Wave 3 starts in earnest, schedule Task 14.1 spike. CTO present. Spike acceptance must be quantitative: N≥500 replays, measured TTL, IP-binding behavior across {same-IP, same-ASN, different-ASN}, explicit failure-mode classification. If replay fails, full plan re-baseline before continuing.
- [ ] **D2 — Hiring sequence.** Adversarial-engineering lead first (longest pipeline) or backend engineers first (shorter pipeline, can start Wave 1 immediately)? Recommendation: start lead search now (parallel to Wave 1), bring on backend engineers from existing team or contractors.
- [ ] **D3 — Connect-Go vs grpc-go for sidecar.** Recommendation: Connect-Go. Decision affects all proto-generated code.
- [ ] **D4 — Fingerprint harvesting extension legal review.** Counsel review needed before Phase 2 (Q3). Distribution model: bundle in free utility vs paid incentive. CAC implications differ materially.
- [ ] **D5 — Managed Valkey vs self-hosted.** Recommendation: managed (ElastiCache Serverless or Memorystore) for ops simplicity. Pricing trade-off depends on volume.
- [ ] **D6 — Cache TTL knobs as customer-configurable?** Affects unit economics 5-10× for review-heavy tenants. Recommendation: not initially; revisit after 90 days of telemetry.
- [ ] **D7 — Public position statement.** Should we publish a position aligned with SerpApi's framing? CEO judgment. Adoption of marketing discipline (Task 24) is the floor; a public posture is a separate decision.
- [ ] **D8 — Insurance / E&O coverage** for scraping liability before launch. Cyber-liability + media-liability policies; budget $20-50K/yr depending on coverage limits. CEO/legal call. References R7.
- [ ] **D9 — Data retention default for reviewer PII.** Interacts with GDPR Art. 14 + deletion workflows. Currently undefined. Not a build decision but blocks D4 and GDPR LIA finalization (Task 23). Recommendation: 90-day default retention with explicit opt-in extension for paying customers.

---

## Chunk 9 — Effort Summary and Wave Calendar

### Calendar (assumes 4 engineers + 0.5 senior RE lead from week 1)

| Week | Wave | Workstreams active |
|---|---|---|
| 1-2 | W1 Foundation | PlaceFetcher interface, DB migrations, facade refactor, cookies deprecation |
| 3-4 | W2 Detector | Fixtures, signals, telemetry, baseline measurement |
| 4 | **Decision gate** | Read baseline degradation rate; confirm/adjust plan |
| 4-6 | W3 (parallel) | Proto contract, sidecar scaffold, asyncio pool, Patchright engine |
| 6-8 | W3 (cont) + W4 spike | Camoufox + Nodriver engines; **Tier-1 replay viability spike (D1 gate)** |
| 8-10 | W4 + W7 | Tier 1 azuretls client, token mint pool, Valkey cache, router |
| 9-12 | W5 + W6 | Deobfuscation pipeline (senior lead lead), proxy router + ASN policy |
| 11-14 | W8 + GDPR | Synthetic fingerprints + GDPR build work in parallel |
| 14-16 | W9 Cutover | Per-tenant rollout, cookies.json retirement, ops handoff |
| Q3 2026 | W8 Phase 2 | Consensual harvest extension (separate engineering effort) |

### Effort summary

| Category | Effort |
|---|---|
| Core build | ~14-16 calendar weeks, 4 engineers |
| Senior RE lead | ~5-7 weeks deobfuscation pipeline (within 16-week window) + 0.5 FTE ongoing post-launch [REVISED upward per Chunk 7 review] |
| GDPR | 2 engineer-weeks (parallel) |
| Cutover + retirement | 4 weeks calendar (mostly observation) |
| **Total to v2 launch** | **~16 weeks, 4-6 engineers** + 10-15% schedule contingency |
| Ongoing arms-race maintenance post-launch | ~1 FTE |
| Q3 Phase 2 (harvest extension) | 4 engineer-weeks + **$50-200K CAC budget** (paid incentive route) or ~$10-30K (bundled free utility) |

### Budget envelope [REVISED 2026-05-12 per Chunk 9 review — original $600-900K omitted recruiter fees, dev-phase proxy, infra, legal]

- **Engineering payroll:** 4 engineers × 4 months = ~16 engineer-months at $300K loaded cost each / 12 = ~$400K
- **Senior RE lead:** $400-600K total comp (year 1) + **recruiter/agency fees $80-150K** (20-30% of base; load-bearing — this hire is scarce and we'll likely need an external recruiter)
- **Proxy spend during dev:** ~$2-5K/mo × 5 months = **~$10-25K** (previously omitted)
- **Proxy spend at launch volume:** ~$2-5K/mo, scaling to ~$5-15K/mo at production
- **Valkey managed:** $200-1000/mo depending on tier
- **Other infra:** Kubernetes/EKS + observability stack (Datadog or Grafana Cloud) + CI runners = **~$2-5K/mo** for 16 weeks = ~$10-25K (previously omitted)
- **Legal review:** GDPR build + harvest extension Phase 2 + E&O insurance = **~$20-50K outside counsel** (previously omitted)
- **Equipment** for 4 new engineers if applicable: **~$15K** (previously omitted)
- **Contractor backfill contingency** if senior RE search slips: **+10-15%** of senior RE budget reserved
- **Camoufox/Patchright/Nodriver:** open-source, $0
- **Fingerprint Phase 2 CAC:** $50-200K (paid incentive route) or ~$10-30K (bundled free utility), Q3 budget

**Revised all-in v2 launch budget: ~$800K-$1.2M** including the senior hire, recruiter fees, dev-phase proxy, infra/observability, legal, and equipment. The original $600-900K figure covered engineering + senior hire only.

**Contingency line: if D1 Tier-1 spike fails, proxy spend reforecasts +$100-200K/yr at production scale; total envelope shifts to $1.0-1.4M.**

### Plan-document review

After each chunk above is finalized, dispatch `plan-document-reviewer` subagent per the writing-plans skill convention. Reviewer prompt should include the chunk contents only — no session history.

---

## Sources

All library and architecture decisions in this plan were verified against the following sources May 12 2026. Full citations and longer quotes live in the companion dossier `2026-05-12-google-maps-session-supply-research.md`.

**Go libraries:**
- [Noooste/azuretls-client](https://github.com/Noooste/azuretls-client)
- [refraction-networking/utls](https://github.com/refraction-networking/utls) + [security advisories](https://github.com/refraction-networking/utls/security/advisories)
- [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client)
- [connectrpc/connect-go](https://github.com/connectrpc/connect-go) + [Buf Connect post](https://buf.build/blog/connect-a-better-grpc)
- [panjf2000/ants](https://github.com/panjf2000/ants)
- [valkey-io/valkey-go](https://github.com/valkey-io/valkey-go) / [redis/rueidis](https://github.com/redis/rueidis)
- [go-redis/redis_rate](https://github.com/go-redis/redis_rate) + [Brandur GCRA](https://brandur.org/rate-limiting)
- [Linux Foundation Valkey](https://thenewstack.io/linux-foundation-forks-the-open-source-redis-as-valkey/)
- [Redis vs Valkey 2026](https://dev.to/synsun/redis-vs-valkey-in-2026-what-the-license-fork-actually-changed-1kni)

**Stealth browser libraries:**
- [Kaliiiiiiiiii-Vinyzu/patchright-python](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python)
- [daijro/camoufox](https://github.com/daijro/camoufox) + [issue #388](https://github.com/daijro/camoufox/issues/388)
- [ultrafunkamsterdam/nodriver](https://github.com/ultrafunkamsterdam/nodriver)
- [Evomi 2026 fingerprint benchmark](https://evomi.com/blog/camoufox-vs.-rebrowser-vs.-stock-playwright-a-fingerprint-benchmark)
- [techinz/browsers-benchmark](https://github.com/techinz/browsers-benchmark)
- [Browserless scaling architecture](https://www.browserless.io/blog/scaling-browser-automation-architecture-1000-sessions)

**SearchGuard / BotGuard reference:**
- [Resoneo BotGuard deep dive](https://think.resoneo.com/botguard-google/)
- [Search Engine Land "Inside SearchGuard"](https://searchengineland.com/inside-google-searchguard-467676)
- [dsekz/botguard-reverse](https://github.com/dsekz/botguard-reverse)
- [LuanRT/BgUtils](https://github.com/LuanRT/BgUtils)
- [chris124567/commercial-bot-detectors](https://github.com/chris124567/commercial-bot-detectors)
- [notemrovsky/tiktok-reverse-engineering](https://github.com/notemrovsky/tiktok-reverse-engineering)

**Monitoring + deobfuscation:**
- [robre/jsmon](https://github.com/robre/jsmon)
- [HumanSecurity/restringer](https://github.com/HumanSecurity/restringer)
- [j4k0xb/webcrack](https://github.com/j4k0xb/webcrack)

**Fingerprints:**
- [Multilogin pricing](https://multilogin.com/pricing/)
- [AmIUnique academic project](https://www.amiunique.org/)
- [creepjs](https://github.com/abrahamjuliot/creepjs)
- [fingerprintjs](https://github.com/fingerprintjs/fingerprintjs)
- [Hola SDK legal template](https://hola.org/legal/sdk)

**Proxies:**
- [Decodo pricing](https://decodo.com/proxies/residential-proxies/pricing)
- [Bright Data residential pricing](https://brightdata.com/pricing/proxy-network/residential-proxies)
- [Proxyway 2025 market research](https://proxyway.com/research/proxy-market-research-2025)

**Legal:**
- [Google v. SerpApi complaint](https://storage.googleapis.com/gweb-uniblog-publish-prod/documents/Google_v._SerpApi__Complaint.pdf)
- [SerpApi MTD blog](https://serpapi.com/blog/google-v-serpapi-motion-to-dismiss-why-were-in-the-right/)
- [Eric Goldman / McCarthy guest post](https://blog.ericgoldman.org/archives/2026/01/relitigating-hiq-labs-and-scraping-through-the-lens-of-the-dmca-1201-anti-circumvention-guest-blog-post.htm)
- [EDPB Opinion 28/2024](https://edpb.europa.eu/) (full URL in companion dossier)

**Anti-bot research (state of the art):**
- [Castle blog — Antoine Vastel](https://blog.castle.io/)
- [Cloudflare JA4 signals](https://blog.cloudflare.com/ja4-signals/)
- [DataDome threat research](https://datadome.co/threat-research/)
- [Scrapfly post-quantum TLS](https://scrapfly.io/blog/posts/post-quantum-tls-bot-detection)

---

**Plan complete and saved to `docs/superpowers/plans/2026-05-12-scraper-v2-architecture.md`.**

**Ready to execute?** Recommendation: do not start coding until D1 (Tier-1 spike), D2 (hiring sequence), and D5 (managed Valkey) decisions are made. Wave 1 can begin in parallel with those decisions.

---

## Plan Changelog

- **2026-05-12 (initial):** Plan written across 9 chunks; Chunks 1 + 5 deep-dive added for Camoufox/Patchright source-level decision.
- **2026-05-12 (pass 1 reviewer fixes):** Chunks 1 + 5 reviewed by plan-document-reviewer subagent; 17 issues resolved (system diagram language, source anchors for cloverlabs and Patchright pins, §1.3 numbering, ghost-cursor added to Task 10, headed/xvfb mandate, canary→Router wiring, numeric SLO).
- **2026-05-12 (pass 2 — full plan review with 2026 verification):** Chunks 2-9 + meta-verification reviewed in parallel. Material fixes applied:
  - Spec §-1 updated to reflect CEO "circumvention is core" direction (resolves Tier-1 internal contradiction)
  - Chunk 3: Go module path corrected (`github.com/gosom/google-maps-scraper`), `gmaps.Entry` instead of nonexistent `models.Place`, migration filename convention (`000037_*` not `20260513_*`), `.down.sql` companions, `testcontainers-go` added, webrunner line corrected (394 not 388), facade integration boundary clarified
  - Chunk 4: detector strategy revised — selectors via section-presence checklist with fallbacks (not hardcoded `Pa9hud`/`[data-rrm]` which couldn't be verified externally), fixture target raised to 50-100 clean + 20 degraded, OTel metric renamed per semconv (`scrape.responses` not `scrape.degraded_total`), detector self-health alert added, golden-file CI regression
  - Chunk 6: spec §-1 update resolves contradiction; Tier 1 retained as core architecture per CEO direction
  - Chunk 7: `robre/jsmon` (unmaintained since 2020) replaced with in-house watcher; BrowserForge adopted instead of building synthetic generator from scratch; Task 17 effort revised to 5-7 weeks (from 3); ePrivacy Directive Art. 5(3) added to consent stack
  - Chunk 8: Task 22 file list expanded (`.env.example:156`, `docker-compose.staging.yaml:16,29`, root `google_cookies.json`), Task 21 cutover threshold with hysteresis, R7-R11 risks added (legal action against us, ASN range bans, vendor outages, bus-factor, device-integrity attestation), D8-D9 decisions added (E&O insurance, PII retention)
  - Chunk 9: budget envelope revised to $800K-$1.2M (was $600-900K; missing recruiter fees, dev-phase proxy, infra, legal, equipment); Tier-1-fails contingency line added
  - Stealth acceptance thresholds lowered per meta-verification: Camoufox/Patchright 65% (was 80%), Nodriver 55% (was 70%)
- Pending follow-ups (advisory, non-blocking): Wave 2.5 device-integrity attestation research spike, Wave 1 staffing realism (4 engineers can't all start week 1), 24h memory-leak soak in Task 8 acceptance, canvas-noise sub-task in Task 9 broken out with explicit acceptance.
