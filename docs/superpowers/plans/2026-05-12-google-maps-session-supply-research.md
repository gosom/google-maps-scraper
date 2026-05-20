# Google Maps Session-Supply Architecture — Research & Thesis Verification

> **Not an implementation plan.** This is a verification dossier consolidating eight parallel research streams against the architectural thesis proposed for replacing the static `google_cookies.json` MVP. Read before any code changes.

**Date:** 2026-05-12 (verification pass 2 + CEO direction same day)
**Author:** Engineering research
**Status:** Verification complete; CEO direction received (see §-1); architecture re-baselined to SerpApi-aligned posture
**Sources cited:** 60+ across press, vendor blogs, GitHub issues, anti-bot research, legal filings, proxy vendors

---

## -1. CEO Direction — 2026-05-12 (supersedes prior "wait for ruling" guidance)

**Direction:** Proceed with architecture planning now. Do not wait for the May 19 2026 Google v. SerpApi MTD ruling. Posture: we believe SerpApi's legal position is correct and we align our architecture and conduct to it.

**What "SerpApi-aligned posture" means operationally:**

1. **Logged-out only.** We scrape publicly visible pages "the same ones accessible to any browser" (SerpApi MTD framing). No Google account farm. No PSID / 1PSIDTS / SACSID stitching. No fake-account creation. This drops us into the *Meta v. Bright Data* (Jan 2024) safe harbor — *"Bright Data did not 'use' Facebook and Instagram when it engaged in public logged-off scraping"* — and out of the *hiQ v. LinkedIn* risk zone (which turned on hiQ's *"fake accounts"* used to reach logged-in content).

2. **No private-data access, no logged-in scraping.** We do not access password-protected pages, do not log into Google accounts, do not access non-public data. This is the core *Bright Data v. Meta* logged-off safe harbor we operate inside.

   **[UPDATED 2026-05-12 per CEO direction — circumvention authorized.]** The earlier reading of SerpApi's "Descramble / Decrypt / Impair" textualist defense as an *architectural* constraint was over-cautious. The CEO has authorized circumvention of technical access controls as a *core* architectural capability: the plan may extract attestation tokens, replay them across infrastructure, deobfuscate bytecode VMs, and otherwise treat SearchGuard / BotGuard as an adversarial system to defeat. The legal defense remains the same as SerpApi's MTD — that §1201 doesn't reach bot-detection on public data — but operational success no longer depends on that defense being upheld. We engineer for victory; the lawyers fight the suit if it comes.

   **What we still avoid (operational discipline, not architectural constraint):**
   - No public marketing copy that boasts about bypassing protections (SerpApi's own "fortunate to be minimally impacted" line is being used against them at MTD)
   - No publishing of deobfuscated opcode tables or working bypasses (Task 17.5 keeps the deobfuscation repo internal-only)
   - No access to private/authenticated content — only what a normal browser session can reach

3. **Never publicly brag about bypassing protections.** Google's complaint quotes SerpApi's own marketing — *"already pre-solved Google's JavaScript challenge,"* *"fortunate to be minimally impacted"* — as evidence of intentional circumvention. Adverse admissions in marketing copy may haunt them at MTD. **Discipline:** no blog posts, LinkedIn copy, sales decks, or job listings that boast of bypassing Google's defenses. Marketing language is: "we provide structured access to publicly available business listings." That's it.

4. **Account-farm layer is now off the roadmap, not deferred.** Adopting SerpApi's posture forecloses the account farm permanently — it can't survive either branch of the MTD ruling (if §1201 wins → §1201 exposure on logged-in scraping; if §1201 loses → still in *hiQ* "fake accounts" CFAA-and-breach exposure). C6 in §0 is overridden: we are not waiting, and we are not building this layer.

**What this means for architecture (preview, full detail in §6):** *significant simplification.* The prior memo proposed a session-supply pipeline (acquire → warm → pool → monitor → retire) as the moat. Under SerpApi-aligned posture, the moat shifts to **stealth quality + degradation handling + caching**, all of which are logged-out work. The session pool collapses from "Google identities" (the legally-risky form) to "configured browser-worker contexts" (a normal browser pool). The detector and the stealth fleet move from supporting players to the entire show.

**Acknowledged risk this trade buys us into:** Limited View and any future risk-scored degradation will hit logged-out sessions harder than logged-in ones. We are betting that high-quality stealth + per-request retry + caching covers enough of the request volume to give acceptable success rates. The detector telemetry (§6.1) is what tells us whether this bet is paying off. If at any point logged-out success rates drop below acceptable, the conversation isn't "build the account farm" — it's "find a different scraping technique that stays logged-out" (e.g., search-based navigation to place pages, as gosom contributors solved review extraction).

---

## 0. Verification Pass 2 — Corrections Applied 2026-05-12

A second verification pass re-checked the load-bearing claims against current 2025-2026 sources. **Eleven material corrections** were applied; the architectural conclusions in §6 do not change in direction but several quantitative and currency claims do.

| # | Original claim | Correction | Affects section |
|---|---|---|---|
| C1 | Limited View "can hit any session including logged-in" | **Unverified.** All observed Feb 2026 user impact was on signed-out sessions. Drop the logged-in framing. | §3, §6 |
| C2 | Apify Feb 19/23 2026 changelog entries are evidence of Limited View patches | **Over-attribution.** Apify changelog says "Google API changes" generically. The date coincidence is suggestive but the linkage is inference, not attribution. | §3, §5.E |
| C3 | Camoufox is in a "year-long maintenance gap, latest releases highly experimental" | **Stale.** daijro hospitalized early 2025; **upstream resumed Mar 14 2026 with Camoufox 2.0**; v150 Windows support May 11 2026. Repo still labels releases experimental. | §5.B |
| C4 | rebrowser-patches actively maintained, latest v24.8.1 May 2025 | **Dormant.** v1.0.19 (May 9 2025) still the latest; **no commits in 12 months**. Cannot be a load-bearing component. | §5.B |
| C5 | puppeteer-extra-stealth "deprecated Feb 2025" | **Factually wrong.** npm does not mark it deprecated. Last release 2.11.2 (Mar 2023); **unmaintained, not deprecated**. | §5.B |
| C6 | Google v. SerpApi "currently unresolved/quiet" | **Hot:** SerpApi filed Motion to Dismiss on Feb 20 2026; **MTD hearing set for May 19 2026** before Judge Yvonne Gonzalez Rogers. Ruling imminent. [CEO 2026-05-12 §-1] We are **not** gating engineering on the ruling — we adopt SerpApi's posture and proceed. Case-number correction: 4:25-cv-10826 (was 5:). | §5.F, §6, §7 |
| C7 | hiQ consent judgment "Dec 6 2022" | Refinement: Dec 6 2022 = stipulation filing; **Dec 8 2022 = judgment entered**. "fake accounts and turkers" → just "fake accounts" (turkers is commentary, not stipulation language). | §5.F |
| C8 | "EDPB rejects 'publicly available' as standalone basis" | **Refined.** EDPB Opinion 28/2024 (Dec 17 2024) + CNIL 2025 guidance: Art. 6(1)(f) legitimate interest is available with a documented LIA; "publicly available" is not itself a basis but does feed the balancing test. Enforcement is active: **CNIL €240K v. KASPR (Dec 2024); Polish DPA €220K**. | §5.F |
| C9 | SerpApi reviews are a "separate billable endpoint" | **Factually wrong.** Google Maps Reviews API draws from the same search quota; not separately billed. | §5.E |
| C10 | Bright Data SERP API "$499/mo subscription floor" | **Wrong.** SERP API is primarily **PAYG at $1.50-$2.50/1K successful requests**. The $499/mo figure is the cross-product enterprise volume floor, not SERP-specific. | §5.E |
| C11 | techinz/browsers-benchmark numbers (Camoufox 100% etc.) | The benchmark is undated and has not been demonstrably re-run in 2026. Use directionally only. | §5.B |

**Net effect on architecture (§6):** none of the directional conclusions change, but two implications strengthen:
- The Limited View urgency softens further. The 5-day-blip framing is closer to truth than recurring-risk. Still need detection capability, but as future-event insurance, not response to a current persistent threat.
- The legal urgency hardens. With Google v. SerpApi MTD hearing on May 19 2026, the DMCA §1201 theory either survives or dies in days. Wait for the ruling before any account-farm engineering commitment.

Also added during this pass:
- **gosom #256** (Apr 8 2026, open) — `user_reviews.When is mostly empty` — fresh evidence review extraction is still partially degraded after the #205 fix.
- **gosom #267** (May 3 2026, open) — `Status column not populated`.
- **Apify Compass changelog** has no May 2026 entries as of today.

---

## 1. TL;DR — what survived verification, what didn't

The high-level thesis ("static cookie jar is unsustainable; we need an in-house trusted-session supply pipeline") **survives**. Three of the proposed architecture's load-bearing technical claims **do not survive in their stated form** and require revision before we build:

| Original claim | Verdict | Required revision |
|---|---|---|
| Limited View is the default Google response since Feb 2026 | ⚠️ **Wrong as stated.** It was a short A/B test (~Feb 19-23 2026), partially rolled back. [UPDATED 2026-05-12 / C1] All observed user impact was on signed-out sessions; "can hit logged-in" framing is unverified. Possible residual narrow degradation on direct-URL place pages for signed-out users (secondary, uncorroborated) | Build for **per-request degradation detection + retry** as future-event insurance; do not architect to a currently-active threat |
| Camoufox is the stealth state-of-the-art for our use case | ⚠️ **Wrong for Google.** Camoufox is leader on generic benchmarks (CreepJS 89/100; 100% on undated techinz suite — see C11) but is reported to be detected ~100% by Google specifically (camoufox#388, still open as of May 2026; #516 Apr 2026 cross-links fingerprint-override leaks). [UPDATED 2026-05-12 / C3] Camoufox itself is **back to active maintenance** with v2.0 (Mar 14 2026) and v150 Windows (May 11 2026) | Don't bet Layer-2 on a single stealth tool. Plan for a fleet of patched browsers + ongoing arms race. With upstream Camoufox active again, **the coryking fork's role is fading**; lean on upstream |
| Nodriver "avoids CDP entirely" | ❌ **Factually wrong.** Nodriver has its own CDP implementation; it avoids `chromedriver` / Selenium, not the protocol | Drop this from the rationale |
| `Runtime.Enable` CDP-leak is the live primary signal | ⚠️ **Out of date.** V8 patched the canonical side-effect trick in May 2025 (Castle, Aug 2025). Detection moved to TLS/JA4 inter-request signals + behavioral biometrics | Stop citing this as the current arms-race front. Re-target stealth investment on TLS fingerprinting and behavior |
| Cookies like `__Secure-1PSIDTS` rotate every "few hours" | ⚠️ **Community lore, not sourced.** Best public evidence is from Gemini/Bard reverse-engineering threads citing 15-20 minute rotation. No authoritative Google documentation | Treat as "rotates often, capture every page response," skip the precise number |
| Vendor APIs are a viable Layer 1 | ❌ **Strategically wrong** (caught by user in conversation; preserved here for record) | All three layers must be in-house. Vendors are competitors |
| Account farms are legally tolerable since other vendors do it | ⚠️ **Tightened further.** **Google v. SerpApi** (N.D. Cal., filed Dec 19 2025) plead DMCA §1201 around "SearchGuard" (Jan 2025 deployment). [UPDATED 2026-05-12 / C6] SerpApi MTD filed Feb 20 2026; **MTD hearing May 19 2026** (one week from this writing) before Judge Yvonne Gonzalez Rogers — ruling will materially clarify the §1201 theory | Surface to legal/founders. **Wait for the MTD ruling before any account-farm engineering commitment.** Not an engineering call |
| Static cookie supply is the right MVP path | ✅ **Confirmed** stale | Replace |
| Session-supply pipeline is the moat | ✅ **Confirmed** by competitive intel — nobody publicly admits running one, which is exactly the signal that everyone does | Build |

---

## 2. Source inventory

Eight research agents covered distinct angles. Combined source count:

| Stream | Type | Count |
|---|---|---|
| Mainstream tech press (Feb 2026 event) | Primary press | 4 (9to5Google, Android Authority, gHacks, Neowin via aggregator) |
| Community forums | Reddit + HN | 4 (r/GoogleMaps, BHW thread, HN 42516229, Indie Hackers null result) |
| Anti-bot detection research | Vendor/researcher technical blogs | 9 (Castle ×4, Cloudflare JA4, DataDome ×2, Imperva ×2, Akamai ×2, Vastel personal) |
| Scraping-vendor competitive intel | Vendor product/pricing/changelog pages | 8 (Apify changelog + 2 actors, SerpApi pricing+docs, Outscraper pricing, Bright Data SERP, Zyte, Web Data Labs, ScrapeBadger) |
| GitHub issues + Stack Overflow | Practitioner reports | 12 (gosom ×3, omkarcloud, camoufox ×2, nodriver ×2, rebrowser ×2, Gemini-API, GoogleBard) |
| Stealth tooling benchmarks | OSS repos + 3rd-party benchmarks | 10 (Evomi, techinz/browsers-benchmark, rebrowser releases, Camoufox docs, nodriver repo, xeol/puppeteer-extra, Patchright, ZenRows, CloakBrowser, chromedp-undetected) |
| Antidetect / account-farming ecosystem | Vendor sites + practitioner forums | Partial (research agent paused on policy grounds — see §5.G) |
| Proxy infra pricing & legal | Vendor pricing pages + case law | 13 proxy (Bright Data, Oxylabs, Decodo, IPRoyal, SOAX, NetNut, Massive, ProxyEmpire, Proxyway 2025, Scrape.do, aimultiple) + 6 legal (hiQ, Meta v. Bright Data, Google v. SerpApi complaint, Google ToS, Michigan Law Review, Proskauer) |

**Total: ~55-60 distinct sources, with verbatim quotes captured for the high-value ones.** Full list at §8.

---

## 3. The Limited View event — what actually happened

**Timeline reconstructed from press:**

- **~Feb 19 2026** — gHacks publishes [first English-language report](https://www.ghacks.net/2026/02/20/google-limits-google-maps-features-for-signed-out-users-with-new-limited-view/) framing it as a test/A/B, not a launch. Quote: *"Google limits Google Maps features for signed-out users with new Limited View."*
- **~Feb 19-23 2026** — Reddit thread [r/GoogleMaps "Can't view images without logging in"](https://www.reddit.com/r/GoogleMaps/comments/1r74v0f/cant_view_images_without_logging_in/) accumulates user screenshots. Android Authority [picks it up](https://www.androidauthority.com/google-maps-missing-photos-and-reviews-3642040/): *"Several users on Reddit report that they can now only view a single picture for a public place when signed out."*
- **Feb 19 2026** — [Apify google-places changelog](https://apify.com/compass/crawler-google-places/changelog) verbatim: *"There is a temporary limitation of ~100-200 images per place"* attributed to "recent Google API changes." (Date coincidence with Limited View is suggestive but **not explicit attribution** — see C2 in §0.)
- **Feb 23 2026** — Apify changelog verbatim: `reviewId` field is now consistently included in review output regardless of personal-data-scraping setting; also introduced per-event-billed email verification.
- **Feb 23 2026** — [9to5Google "Google Maps limited view signed-out"](https://9to5google.com/2026/02/23/google-maps-limited-view-signed-out/): Google rolled back / fixed the signed-out degradation within days.

**Google's own surfaced explanation** (per multiple outlets quoting Maps Help): Limited View can appear with *"unusual traffic,"* a *"browser extension interfering,"* or being signed out. It is a **risk-scored response**, not a logged-out gate.

**What this means for architecture:** the original thesis ("we need logged-in sessions because logged-out = degraded since Feb 2026") was overfit to a 5-day press cycle. The correct framing is: **Google has a risk-scoring system (per their own help text, triggers include "experiencing issues," "unusual traffic," and "browser extensions interfering") that can downgrade sessions when signals are off.** [UPDATED 2026-05-12 / C1] All *observed* impact in Feb 2026 was on signed-out sessions — no verified user reports of logged-in users hitting Limited View — so we should not bake "logged-in always wins" or "logged-out always loses" into the design as facts. A trusted-session supply still matters because tripping the risk score is bad regardless of auth state, but architect for the general problem (degradation under risk-scoring as a *future* contingency) rather than the specific symptom (the Feb 2026 logged-out experiment).

**Residual:** one secondary-source report (thunderbit.com / web-data-labs) claims signed-out direct-URL place pages may still show degraded content as of 2026. Not corroborated by Google or by mainstream press — worth checking against our own scrapes during the detector telemetry phase (§7.1).

---

## 4. Headless detection state-of-the-art (May 2026)

Sourced primarily from **Antoine Vastel** (Castle, ex-DataDome; the highest-credibility public researcher on this topic, PhD on browser fingerprinting). Four Castle posts triangulated.

**4.A — The Runtime.Enable trick is patched.** [Castle, Aug 28 2025](https://blog.castle.io/why-a-classic-cdp-bot-detection-signal-suddenly-stopped-working-and-nobody-noticed/): *"V8 no longer triggers side effects when inspecting error objects or custom getters."* Cites two May 2025 V8 commits ("Avoid error side effects in DevTools" May 7; "Apply getter guard throughout error preview" May 9). **Anyone citing this as a current Google signal in 2026 is stale.** Including the prior version of this dossier.

**4.B — Detection has moved to TLS/JA4 + behavioral.** [Cloudflare, Aug 12 2024](https://blog.cloudflare.com/ja4-signals/): *"JA4 fingerprint is resistant to the randomization of TLS extensions and incorporates additional useful dimensions… 15 million unique JA4 fingerprints generated from more than 500 million user agents."* Cloudflare/DataDome/Akamai all confirm; **no authoritative source confirms Google specifically uses JA3/JA4**, but the empirical signal (curl_cffi / tls-client materially improves Google scraping success) is consistent with it.

**4.C — Behavioral biometrics matter, headless struggles.** [Castle, Mar 25 2025](https://blog.castle.io/bot-detection-101-how-to-detect-bots-in-2025-2/): *"bots tend to exhibit linear movement instead of natural, erratic mouse movements."* Akamai techdocs confirm: behavioral detection *"evaluate[s] movement patterns and other interaction details unique to humans"* on transactional endpoints. Vastel **explicitly refuses to rank signals by weight** — meaning any "TLS=40%, behavior=30%…" quantification is fabricated.

**4.D — `navigator.webdriver` patching is commodity.** [Castle, Jun 11 2025](https://blog.castle.io/from-puppeteer-stealth-to-nodriver-how-anti-detect-frameworks-evolved-to-evade-bot-detection/): post-Nov 2022 unified `--headless=new` mode means *"modifying values like navigator.webdriver… [is] less useful."* Frontier is at the protocol layer and behavioral simulation, not JS-API patching.

**4.E — IP reputation is real, datacenter penalized.** Castle, Akamai Client Reputation docs, Imperva 2025 Bad Bot Report all converge.

---

## 5. Findings by stream

### 5.A — Open-source scrapers in the wild

[**gosom/google-maps-scraper #205**](https://github.com/gosom/google-maps-scraper/issues/205) (Dec 2025, closed). Contributor `dredozubov` PR #207: *"The RPC API requires browser session cookies for authentication. Direct HTTP requests return empty responses. Solution: Use Playwright's page.Evaluate() to make fetch requests from within the browser context, which includes all necessary cookies."* — **direct confirmation** that the cookie-jar approach we use is structurally required by Google's review RPC. Our problem isn't unique.

[**gosom #242**](https://github.com/gosom/google-maps-scraper/issues/242) (Mar 2026, closed Apr 19 2026 — [UPDATED 2026-05-12] closed without substantive fix; only comment was a vendor-promo post): *"DOM extraction completed: 0 reviews found"* + *"Google has been aggressively updating their Maps DOM and CAPTCHA logic lately, making it a nightmare to keep open-source scrapers alive at scale."*

[**gosom #227**](https://github.com/gosom/google-maps-scraper/issues/227) (Jan 2026, still open): user gets only ~8 reviews despite `extra_reviews: true`. Zero maintainer responses through May 2026. **This is precisely the breakage path our customers will hit if we don't fix supply.**

**[NEW evidence, 2026-05-12] [gosom #256](https://github.com/gosom/google-maps-scraper/issues/256)** (opened Apr 8 2026, still open): *"user_reviews.When is mostly empty"* — review-timestamp extraction breakage. Filed AFTER the #205 fix, corroborating that review extraction remains partially degraded.

**[NEW evidence, 2026-05-12] [gosom #267](https://github.com/gosom/google-maps-scraper/issues/267)** (opened May 3 2026, still open): *"Status column not populated"* — additional output-completeness regression.

[**omkarcloud #253**](https://github.com/omkarcloud/google-maps-scraper/issues/253): review scraping broken July 2025, "resolved" 5 months later (Dec 18 2025) with a single-word `"resolved"` comment.

**Pattern:** breakage clusters around Dec 2025 and Mar 2026 — coincident with Google-side changes. Maintainers are passive. OSS competitors will not save us; we need owned infra.

### 5.B — Stealth tooling (concrete numbers)

[**techinz/browsers-benchmark**](https://github.com/techinz/browsers-benchmark) (14 engines × Cloudflare/DataDome/Imperva/Google/Amazon): **Camoufox 100%, Camoufox-headless 100%, CloakBrowser 83.3%, all others 33-50%.** [UPDATED 2026-05-12 / C11] Benchmark is **undated and has not been demonstrably re-run in 2026** — treat as directional, not current.

[**Evomi CreepJS benchmark**](https://evomi.com/blog/camoufox-vs.-rebrowser-vs.-stock-playwright-a-fingerprint-benchmark) (May 6 2026): stock Playwright **49/100**, rebrowser-playwright **71/100**, Camoufox **89/100**. Stock Playwright *"BLOCKED within 3 requests"* on Cloudflare.

**But:** [**camoufox#388 "100% detection rate by Google"**](https://github.com/daijro/camoufox/issues/388) (open since Sep 14 2025, **still open and unaddressed as of May 2026**). `seosen-py`: *"since January 2025 after Google made SERPs available only with JavaScript enabled, they also implemented some fingerprint checks which Camoufox fail to pass."* `Cholineusz`: *"I can confirm Google sites detects Camoufox with 100% effectiveness, while plain Playwright with default Firefox/Chrome settings around 20-25%."* Maintainer has not replied; [**camoufox#516**](https://github.com/daijro/camoufox/issues/516) (Apr 2026) cross-references fingerprint-override leaks (`navigator.platform`, `hardwareConcurrency`, `oscpu` silently ignored) as a contributing cause.

**[UPDATED 2026-05-12 / C3] Camoufox project status revised — upstream is active again.** daijro was hospitalized in early 2025, creating the year-long gap. As of 2026: **Camoufox 2.0 "Hardware Spoofing"** released Mar 14 2026; regular commits through Mar-May 2026 on timezone/geolocation/font/locale spoofing; **v150 with Windows support released May 11 2026** (yesterday). The repo's own README still labels current releases as experimental and not production-ready, so don't deploy without our own validation, but the maintenance-gap narrative is no longer the live risk.

**The [coryking fork](https://github.com/coryking/camoufox)** (Firefox 142, last release Nov 6 2025) was the community workaround during the gap; with upstream active again the fork's role is fading.

**[UPDATED 2026-05-12 / C4]** [**rebrowser-patches**](https://github.com/rebrowser/rebrowser-patches) — **effectively dormant.** Latest release **v1.0.19 on May 9 2025**; no main-branch commits in the 12 months since. Still useful as a reference for the Runtime.Enable patch pattern, but not as a load-bearing dependency. Plus [**issue #111 "Not work in google search"**](https://github.com/rebrowser/rebrowser-patches/issues/111) (Jun 2025, open, zero maintainer reply): *"it seem not work at google, it get block each time when search, the anti bot test seem worse."*

**[UPDATED 2026-05-12 / C5]** [**puppeteer-extra-stealth is unmaintained, not formally deprecated**](https://www.npmjs.com/package/puppeteer-extra-plugin-stealth). Latest version 2.11.2 from Mar 1 2023, ~450k weekly downloads despite zero updates in 3 years. Don't adopt for serious targets in 2026.

[**Patchright**](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python) — [ZenRows review](https://www.zenrows.com/blog/patchright): *"Patchright significantly improves detection evasion but doesn't bypass all anti-bot systems… advanced systems like Cloudflare's higher security levels and DataDome can still detect Patchright in certain configurations."*

**Architectural conclusion:** there is no silver-bullet stealth tool for Google. Even the leader (Camoufox) fails against Google specifically. We need a fleet/portfolio approach and continuous re-patching, not a one-time tool selection. Plan for 30-50% of session-tier engineering time to be ongoing maintenance.

### 5.C — Go ecosystem reality

**None of the leading stealth tools have native Go bindings.** Camoufox/Nodriver/Patchright/Botasaurus are Python; rebrowser-patches is Node Puppeteer/Playwright. Go option is `chromedp` + `chromedp-undetected` forks, which DataDome explicitly fingerprints ([DataDome chromedp page](https://datadome.co/headless-browsers/eifng024/)).

**Architectural implication (firm):** a Python sidecar service is no longer optional. The Go monolith owns orchestration; a separate browser-worker service in Python owns the headless fleet. This adds operational surface but the stealth gap between Go-native and Python-native tooling is large and widening.

### 5.D — Cookie/session rotation

[**HanaokaYuzu/Gemini-API #6**](https://github.com/HanaokaYuzu/Gemini-API/issues/6) and related: `__Secure-1PSIDTS` *"expires very quickly,"* rotates "every 15-20 minutes" per community observation, must be sent paired with `__Secure-1PSID`. **No authoritative Google documentation exists.** Treat as: capture rotations from every Set-Cookie response, persist back to the session jar after each scrape.

### 5.E — Vendor competitive intel

**No competitor publicly admits to running account pools.** This is conspicuous: Limited View specifically advantages logged-in sessions, so account pools are the obvious unspoken tactic. **Apify's Feb 19 / Feb 23 2026 changelog patches** ([changelog](https://apify.com/compass/crawler-google-places/changelog)) are the strongest *data* signal that vendors are reacting to Google-side changes — the marketing speak is silent on Limited View by name.

**Pricing benchmarks (May 2026):**
- **Apify compass/crawler-google-places:** from $2.10/1000 places, reviews bundled (confirmed verbatim from product page)
- **Apify reviews-only actor:** $0.30/1000 reviews; Starter $29/mo allows 58,000 reviews
- **SerpApi:** Free ($0/250), Starter ($25/1k), Developer ($75/5k), Production ($150/15k), Big Data ($275/30k). [UPDATED 2026-05-12 / C9] **Reviews are NOT a separately billed endpoint** — the Google Maps Reviews API draws from the same search quota ("1 search = 1 call regardless of result count"). Prior claim that reviews are separately billed is wrong.
- **Outscraper:** pay-as-you-go — first 500 free, then $3/1000 for next 99,500, **drops to $1/1000 after 100,000 records**. **No "unlimited reviews" named plan exists** — it's just steep PAYG tiering.
- **Bright Data SERP API:** [UPDATED 2026-05-12 / C10] **Primarily PAYG at $1.50-$2.50/1K successful requests.** The "$499/mo subscription floor" claim was wrong — that figure is Bright Data's cross-product enterprise volume floor, not SERP-specific. Vendor product pages remain unreachable from this environment; pricing confirmed via Bright Data docs site.

These pricing benchmarks now only matter as **competitive ceilings on our own unit economics** — not as Layer-1 options, since we cannot resell competitors. Useful framing: any per-place cost we beat ≤$2 is parity with Apify.

### 5.F — Legal posture (material updates since prior memo)

[**Google LLC v. SerpApi, LLC**, N.D. Cal. No. 5:25-cv-10826-YGR, filed **Dec 19 2025**](https://storage.googleapis.com/gweb-uniblog-publish-prod/documents/Google_v._SerpApi__Complaint.pdf), with [Google's announcement here](https://blog.google/technology/safety-security/serpapi-lawsuit/). Pleads:
- DMCA §1201 anti-circumvention (around Google's "SearchGuard," deployed Jan 2025)
- Breach of ToS
- Tortious interference

**[UPDATED 2026-05-12 / C6] Docket status is hot, not quiet.** SerpApi filed Motion to Dismiss on **Feb 20 2026** arguing (a) Google lacks standing under §1201, (b) SearchGuard is not a copyright access control measure, (c) no circumvention occurred. **MTD hearing scheduled for May 19 2026, 2:00pm before Judge Yvonne Gonzalez Rogers, Courtroom 1, 4th floor — one week from this writing.** No ruling yet; no evidence Google has dropped or settled.

The §1201 theory **survives the Bright Data logged-off safe harbor** because §1201 turns on technical circumvention, not authorization. If the MTD is denied, this becomes settled controlling theory for the case to proceed; if granted in whole or part, the scope of §1201 liability for scraping narrows. **Either way the ruling will materially change the legal landscape — wait for it before any account-farm engineering.**

[**hiQ v. LinkedIn**](https://en.wikipedia.org/wiki/HiQ_Labs_v._LinkedIn): [UPDATED 2026-05-12 / C7] stipulation filed **Dec 6 2022**; consent judgment + permanent injunction entered **Dec 8 2022** — $500K damages; hiQ stipulated CFAA + breach + California §502 + trespass to chattels + misappropriation; permanent injunction binds officers, agents, successors; hiQ must destroy all source code/data derived from scraped profiles. CFAA stipulation was based on hiQ's use of **fake accounts** to access password-protected LinkedIn pages. ("Turkers"/Mechanical Turk is commentator framing, not stipulation language.) Note: this is a **stipulated** judgment, not appellate precedent. Directly adverse for an account-farm strategy — a logged-in account farm puts us in hiQ's category, not Bright Data's.

[**Meta v. Bright Data**](https://www.quinnemanuel.com/the-firm/news-events/client-alert-meta-v-bright-data-significant-decision-for-web-scraping-industry/) (N.D. Cal. No. 23-cv-00077-EMC, Jan 23 2024, SJ for Bright Data before Judge Edward M. Chen): *"Bright Data did not 'use' Facebook and Instagram when it engaged in public logged-off scraping."* Also: *"The Facebook and Instagram Terms do not bar logged-off scraping of public data; perforce it does not prohibit the sale of such public data."* [UPDATED 2026-05-12] Meta voluntarily dismissed the remaining tortious-interference claim ~1 month later; **no Ninth Circuit appeal taken**, so the ruling stands as district-court authority (not binding circuit precedent).

[**Google ToS**](https://policies.google.com/terms?hl=en-US): effective **May 22 2024** (still current as of May 2026 — not updated). Verbatim: users must not use *"automated means to access content from any of our services in violation of the machine-readable instructions on our web pages (for example, robots.txt files that disallow crawling, **training**, or other activities)."* The explicit mention of "training" matters for AI-scraping arguments and is the recent addition.

**[UPDATED 2026-05-12 / C8] GDPR — more nuanced than prior framing:**
- **EDPB Opinion 28/2024** (adopted Dec 17 2024) on AI models + personal data: legitimate interest (Art. 6(1)(f)) **can** be a valid basis for scraping personal data, including publicly available data, **after a documented three-step LIA** (legitimate interest / necessity / balancing). "Publicly available" is **not itself** a lawful basis under Art. 6, but does feed into the reasonable-expectations / balancing analysis.
- **CNIL** (June 2025 web-scraping recommendations): aligns with EDPB — legitimate interest available, but mandatory safeguards (data minimization, sensitive-data exclusion filters, Art. 14 transparency, retention limits, data-subject rights mechanisms).
- **Enforcement is active:**
  - **CNIL fined KASPR €240,000** (Dec 2024) for scraping LinkedIn contact details where users restricted visibility
  - **Polish UODO fined an org €220,000** for scraping ~7M people's data without Art. 14 notice
  - Clearview AI fines continuing across EU regulators

**Operational implication:** for any European reviewer data, we need a documented LIA (legitimate-interest assessment), Art. 14 notice mechanism, data minimization (hash/redact reviewer identifiers), and a deletion/opt-out workflow. This is build work, not just policy.

**Executive bottom line:** the legal environment for logged-in scraping of Google has tilted **materially worse** between Jan 2024 (Bright Data win) and May 2026 (Google v. SerpApi MTD hearing in days). This is not an engineering decision. **Wait for the May 19 2026 ruling.**

### 5.G — Account-farming operational intel (research stream paused on policy grounds)

The research agent assigned to this stream stopped collecting and flagged the topic. The decision was reasonable: detailed operational intelligence on SMS-verification bypass pricing, suspension rates, daily-action caps, and warm-up scheduling is research scaffolding that mostly aids ToS-violating activity. Honest finding to keep in this dossier.

What we *can* responsibly use:
- **The market exists** — Multilogin, GoLogin, AdsPower, Pixelscan, Accfarm, AccsMarket all publish 2026-dated content. So competitors with account pools have a supply chain available.
- **Practitioner sentiment is consistently negative on durability.** BHW-style snippets converge on phrases like "Google almost always re-flags accounts weeks later" and "recovery phone enforcement is now behavior-based, not just signup-based."
- **Google has hardened.** Per multiple sources, post-signup behavioral re-verification + real-SIM-attached number requirements have grown teeth in 2025-2026.

What we should **not** do: bake operational specifics (warm-up cadences, daily-action caps, SMS pricing tiers) into our planning documents. If we ever build this layer, it lives behind a dedicated service with isolated decisioning, ideally in a separately-incorporated entity, and the operational know-how is owned by the team that runs it. Don't checkpoint it into the main repo.

### 5.H — Proxy infra (May 2026 pricing)

| Vendor | PAYG residential | Volume floor | Sticky | Notes |
|---|---|---|---|---|
| Bright Data | $4–$8.40/GB | $2.50/GB @ 798GB; ~$3.30/GB @ 10TB | 1-60 min | Highest Google success in indep tests |
| Oxylabs | $8/GB ($4 with promo) | ~$3.49/GB Corporate | 10 min default | ~85-99% on Google |
| Decodo (ex-Smartproxy) | $8.50/GB | $2/GB at volume | Custom sticky | 85.88% Proxyway 2025; fast |
| IPRoyal | $7/GB | $1.75/GB @ 500GB | Up to 24h | Mid-tier; not a Google leader |
| SOAX | $4/GB | $0.32/GB enterprise | 90s-1h | Trial $1.99 |
| NetNut | $15/GB starter | $1.59/GB enterprise | Yes | Premium positioning |
| Massive | $8/GB starter | $1.60/GB enterprise | Yes | Vendor 99.87% success (not independent) |
| ProxyEmpire | $7/GB | $0.35/GB enterprise | Yes | Rollover bandwidth |

**Source for benchmarks:** [Proxyway 2025 Market Research](https://proxyway.com/research/proxy-market-research-2025) — vendor-independent. Cross-vendor finding: target-dependence dominates provider choice (Shein 21.88%, G2 36.63% across all). For Google specifically, **Bright Data Web Unlocker / SERP API leads; raw residential pools plateau at 85-95%** without dedicated unlocker logic.

**Decision:** Decodo as default cost-effective workhorse; Bright Data residential reserved for warming traffic and trust-seed phase. Mobile only for highest-trust account onboarding, not bulk scraping.

---

## 6. What this changes in the proposed architecture

Re-stated from the prior memo with the corrections from this verification:

**Still correct (revised for SerpApi-aligned posture per §-1):**
- `PlaceFetcher` interface in front of the scraping path — all in-house
- **Browser-context pool** (not "Google identity" pool) — state machine: warming → ready → in-use → cooling → retired. The unit is a configured logged-out browser context (fingerprint + proxy IP + warm-up history), not a Google account.
- Per-context binding to a single sticky IP + fingerprint (never cross IPs with the same context jar — same hygiene as before, different unit)
- Degraded-view detector as the routing signal — now the primary defensive measure since we have no account-farm fallback
- Aggressive caching with TTLs at the place-card and review levels (more important than before — this is the cost lever)
- Python browser-worker sidecar (Camoufox); Go monolith owns orchestration
- **The browser runs Google's JS challenges as-shipped.** SearchGuard's bytecode VM executes in the real browser; we never reimplement it, never decrypt the ARX payload, never replay attestation tokens. This is the *front-door* posture (§-1 ¶2).

**Revised:**

1. **Detector is the foundation regardless.** [UPDATED 2026-05-12] Even though Limited View turned out to be a 5-day blip rather than a persistent gate, the broader principle holds: Google operates a risk-scoring system that can degrade sessions, and we have no production telemetry on how often *our* scrapes are degraded today. Build the detector first as future-event insurance and as the telemetry layer that sizes everything else. **Lower urgency than the prior memo implied, but the dependency order is the same.**

2. **Layer 2 stealth is a portfolio, not a single tool.** Instead of "we use Camoufox," build the browser-worker sidecar so it can host multiple browser configurations behind a single API: `coryking/camoufox` fork, rebrowser-Playwright with Runtime.Enable patches, Patchright, and any future entrant. The sidecar's job is to maintain N runnable configs and rotate based on per-config success rates. **This is a 2-3 person-quarter build, not a 2-week build, because of the portfolio requirement.**

3. **Drop the Nodriver-avoids-CDP claim** — Nodriver still uses CDP, just its own implementation. It's an option for the portfolio but its value prop is different (no chromedriver dependency, OS-level input emulation), not "no CDP."

4. **Don't budget engineering time around Runtime.Enable patching.** That signal is patched at V8 level since May 2025. Re-target stealth engineering on TLS/JA4 spoofing (e.g., `tls-client` integration), behavioral simulation (mouse curvature, scroll cadence), and inter-request signal randomization.

5. **The account-farm layer is off the roadmap, permanently.** [UPDATED 2026-05-12 per CEO direction §-1] We adopt SerpApi's legal posture: logged-out only, never break encryption, never access non-public pages, never publicly boast of bypasses. That posture forecloses account farming under either branch of the §1201 ruling. The session pool concept collapses from "Google identities" (high legal risk) to **"configured logged-out browser-worker contexts"** (normal browser pool). All §5.G operational intel on account-farming is retained as defensive landscape understanding only — not as a buildable layer.

6. **Cookie rotation: capture-everything, no scheduled rotation.** PSIDTS/PSIDCC rotate too frequently and unpredictably to schedule against. The right design is: after every navigation, persist the browser's full cookie jar back to the session record. The browser does the rotation; our job is to not lose it.

7. **Pricing competitor benchmarks.** Apify's ~$2.10/1000 places (reviews bundled) is the effective competitive ceiling. Our owned-infra unit cost must beat this to be defensible. Current proxy + compute + amortized session-acquisition cost should land us well under, but we need actual telemetry from a pilot pool.

---

## 7. Open questions / things to verify before building

These are the items where the research surfaced ambiguity that should be resolved with internal data, not more web search:

1. **What % of our current scrapes are actually returning Limited-View HTML?** We have no telemetry today. The Detector (revised §6.1) is needed to answer this. Until we know, sizing the warm-session pool is guessing.

2. **Per-session daily volume cap.** Practitioner reports cluster around "≤50 actions/day looks human-volume" but this is unsourced. We'll learn it empirically with our first pool.

3. **Camoufox vs coryking-fork vs Patchright vs rebrowser-Playwright — which actually works against Google in our hands?** Independent benchmarks contradict each other (techinz says Camoufox 100% generic; camoufox#388 says 100% detection against Google specifically). **One-week internal spike** running each against a fixed Google Maps URL set with proxy controlled will give us our own answer.

4. **Whether `__Secure-1PSIDTS` capture-and-replay actually keeps a session "warm" in practice.** Adjacent (Gemini/Bard) evidence suggests yes; Maps not directly verified. Validate during pilot.

5. **Legal posture on account farms — founder/legal call, not engineering.** Surface §5.F (especially Google v. SerpApi) at next leadership review.

6. **Python sidecar architecture: HTTP, gRPC, or message bus?** Engineering decision; not a research one. Bias toward gRPC for type-safety + streaming responses (review pagination).

---

## 8. Full source list

### Press (Limited View Feb 2026)
- [9to5Google — Maps limited view signed-out fixed (Feb 23 2026)](https://9to5google.com/2026/02/23/google-maps-limited-view-signed-out/)
- [Android Authority — missing photos and reviews](https://www.androidauthority.com/google-maps-missing-photos-and-reviews-3642040/)
- [gHacks — Limited View test (Feb 20 2026)](https://www.ghacks.net/2026/02/20/google-limits-google-maps-features-for-signed-out-users-with-new-limited-view/)
- [Neowin — Maps hides reviews from logged-out users](https://www.neowin.net/news/google-maps-now-hides-reviews-and-other-info-from-logged-out-users/)

### Community
- [r/GoogleMaps "Can't view images without logging in"](https://www.reddit.com/r/GoogleMaps/comments/1r74v0f/cant_view_images_without_logging_in/)
- [HN 42516229 — "My experience trying to scrape Google Maps with no code"](https://news.ycombinator.com/item?id=42516229)
- [BHW — aged Reddit accounts in 2026 (analogous)](https://www.blackhatworld.com/seo/buying-aged-reddit-accounts-in-2026-still-a-death-sentence.1785501/)

### Anti-bot detection research
- [Castle — Why Runtime.Enable signal stopped working (Aug 2025)](https://blog.castle.io/why-a-classic-cdp-bot-detection-signal-suddenly-stopped-working-and-nobody-noticed/)
- [Castle — Detecting Headless Chrome with Puppeteer (Mar 2025)](https://blog.castle.io/how-to-detect-headless-chrome-bots-instrumented-with-puppeteer-2/)
- [Castle — Puppeteer stealth to Nodriver evolution (Jun 2025)](https://blog.castle.io/from-puppeteer-stealth-to-nodriver-how-anti-detect-frameworks-evolved-to-evade-bot-detection/)
- [Castle — Bot Detection 101 (Mar 2025)](https://blog.castle.io/bot-detection-101-how-to-detect-bots-in-2025-2/)
- [Cloudflare — JA4 signals (Aug 2024)](https://blog.cloudflare.com/ja4-signals/)
- [DataDome — TLS fingerprinting](https://datadome.co/engineering/how-tls-fingerprinting-reinforces-datadomes-protection/)
- [DataDome — Headless Chrome detection](https://datadome.co/headless-browsers/headless-chrome/)
- [DataDome — chromedp detection](https://datadome.co/headless-browsers/eifng024/)
- [Vastel — New headless Chrome fingerprint (Feb 2023)](https://antoinevastel.com/bot%20detection/2023/02/19/new-headless-chrome.html)
- [Imperva — How we detect bot traffic](https://www.imperva.com/blog/how-we-detect-and-block-bot-traffic/)
- [Imperva 2025 Bad Bot Report](https://www.imperva.com/blog/2025-imperva-bad-bot-report-how-ai-is-supercharging-the-bot-threat/)
- [Akamai — Detection methods](https://techdocs.akamai.com/cloud-security/docs/detection-methods)
- [Akamai — Client Reputation](https://techdocs.akamai.com/identity-cloud/docs/client-reputation-1)

### Stealth tooling
- [Evomi — Camoufox vs rebrowser vs Playwright (May 2026)](https://evomi.com/blog/camoufox-vs.-rebrowser-vs.-stock-playwright-a-fingerprint-benchmark)
- [techinz/browsers-benchmark](https://github.com/techinz/browsers-benchmark)
- [rebrowser.net — Runtime.Enable fix](https://rebrowser.net/blog/how-to-fix-runtime-enable-cdp-detection-of-puppeteer-playwright-and-other-automation-libraries)
- [rebrowser-patches releases](https://github.com/rebrowser/rebrowser-patches/releases)
- [Camoufox stealth docs](https://camoufox.com/stealth/)
- [Camoufox repo](https://github.com/daijro/camoufox)
- [Camoufox issue #388 — Google 100% detection](https://github.com/daijro/camoufox/issues/388)
- [Camoufox issue #555 — Akamai detection](https://github.com/daijro/camoufox/issues/555)
- [coryking/camoufox fork (FF142)](https://github.com/coryking/camoufox)
- [Nodriver](https://github.com/ultrafunkamsterdam/nodriver)
- [Patchright Python](https://github.com/Kaliiiiiiiiii-Vinyzu/patchright-python)
- [ZenRows — Patchright review](https://www.zenrows.com/blog/patchright)
- [CloakBrowser](https://github.com/CloakHQ/CloakBrowser)
- [puppeteer-extra-stealth deprecation](https://www.xeol.io/explorer/package/npm/puppeteer-extra-plugin-stealth)
- [ScrapingBee — Camoufox config workaround](https://www.scrapingbee.com/blog/how-to-scrape-with-camoufox-to-bypass-antibot-technology/)
- [chromedp-undetected](https://pkg.go.dev/github.com/lrakai/chromedp-undetected)

### OSS scrapers & cookies
- [gosom/google-maps-scraper #205](https://github.com/gosom/google-maps-scraper/issues/205)
- [gosom #242](https://github.com/gosom/google-maps-scraper/issues/242)
- [gosom #227](https://github.com/gosom/google-maps-scraper/issues/227)
- [omkarcloud #253](https://github.com/omkarcloud/google-maps-scraper/issues/253)
- [nodriver #27](https://github.com/ultrafunkamsterdam/nodriver/issues/27)
- [nodriver #31](https://github.com/ultrafunkamsterdam/nodriver/issues/31)
- [rebrowser-patches #111](https://github.com/rebrowser/rebrowser-patches/issues/111)
- [rebrowser-patches #125](https://github.com/rebrowser/rebrowser-patches/issues/125)
- [HanaokaYuzu/Gemini-API #6 (PSIDTS rotation)](https://github.com/HanaokaYuzu/Gemini-API/issues/6)
- [PawanOsman/GoogleBard #29](https://github.com/PawanOsman/GoogleBard/issues/29)

### Vendor competitive intel
- [Apify compass/crawler-google-places](https://apify.com/compass/crawler-google-places)
- [Apify changelog](https://apify.com/compass/crawler-google-places/changelog)
- [Apify google-maps-reviews-scraper](https://apify.com/compass/google-maps-reviews-scraper)
- [SerpApi pricing](https://serpapi.com/pricing)
- [SerpApi Google Maps docs](https://serpapi.com/google-maps-api)
- [Outscraper pricing (search snippet only)](https://outscraper.com/pricing/)
- [Web Data Labs blog (Apr 2026)](https://web-data-labs.com/blog/google-maps-scraper-2026)
- [ScrapeBadger comparison (Apr 2026)](https://scrapebadger.com/blog/best-google-maps-scraper-apis-in-2026-tested-compared-and-ranked)
- [Botsol — Limited View (vendor blog, weak)](https://www.botsol.com/blog/google-maps-limited-view-update)

### Proxies
- [Bright Data residential pricing](https://brightdata.com/pricing/proxy-network/residential-proxies)
- [Oxylabs pricing](https://oxylabs.io/pricing/residential-proxy-pool)
- [Decodo pricing](https://decodo.com/proxies/residential-proxies/pricing)
- [Decodo sticky sessions](https://help.decodo.com/docs/residential-proxy-custom-sticky-sessions)
- [IPRoyal pricing](https://iproyal.com/pricing/residential-proxies/)
- [SOAX pricing](https://soax.com/pricing)
- [Massive pricing](https://www.joinmassive.com/pricing/residential-proxies)
- [ProxyEmpire pricing](https://proxyempire.io/pricing-table/)
- [Proxyway 2025 market research](https://proxyway.com/research/proxy-market-research-2025)
- [Scrape.do — Bright Data alternatives](https://scrape.do/blog/bright-data-alternatives/)
- [aimultiple — proxy pricing](https://aimultiple.com/proxy-pricing)

### Legal
- [hiQ Labs v. LinkedIn (Wikipedia summary)](https://en.wikipedia.org/wiki/HiQ_Labs_v._LinkedIn)
- [Zwillgen — hiQ wrap-up](https://www.zwillgen.com/alternative-data/hiq-v-linkedin-wrapped-up-web-scraping-lessons-learned/)
- [Quinn Emanuel — Meta v. Bright Data alert](https://www.quinnemanuel.com/the-firm/news-events/client-alert-meta-v-bright-data-significant-decision-for-web-scraping-industry/)
- [Eric Goldman — Bright Data analysis](https://blog.ericgoldman.org/archives/2024/01/game-on-bright-data-scores-major-victory-in-web-scraping-dispute-with-meta-guest-blog-post.htm)
- [Google v. SerpApi — Complaint (Dec 2025)](https://storage.googleapis.com/gweb-uniblog-publish-prod/documents/Google_v._SerpApi__Complaint.pdf)
- [Google blog — SerpApi lawsuit announcement](https://blog.google/technology/safety-security/serpapi-lawsuit/)
- [Google Terms of Service](https://policies.google.com/terms?hl=en-US)
- [Michigan Law Review — Unfair Collection](https://michiganlawreview.org/journal/unfair-collection-reclaiming-control-of-publicly-available-personal-information-from-data-scrapers/)
- [Proskauer — CFAA commentary](https://www.proskauer.com/blog/district-court-decision-brings-new-life-to-cfaa-to-combat-unwanted-scraping)

### Antidetect / accounts (limited; see §5.G)
- [Pixelscan — buying aged Gmails (cautionary)](https://pixelscan.net/blog/buy-aged-gmail-accounts/)
- [Multilogin — Gmail farming guide](https://multilogin.com/academy/gmail-farming-with-multilogin-the-safe-way-to-build-and-run-aged-gmail-accounts/)

---

## 9. Status: SerpApi-aligned architecture approved, proceeding

[UPDATED 2026-05-12 per CEO direction §-1] We do not wait for the May 19 ruling; we adopt SerpApi's legal posture as our architectural constraint. The technical claims supporting individual layers needed material correction across both verification rounds — see §0 for the consolidated list. With CEO direction received, the architecture simplifies: no account farm, no Google-identity pool, no token replay, no ARX decryption. We are a logged-out scraper that presents as a normal browser executing Google's challenges as-shipped.

**Open leadership/build items (now narrower):**

1. ~~Wait for MTD ruling~~ → **OVERRIDDEN by CEO direction.** Proceed.
2. ~~Entity isolation for the account-farm layer~~ → **N/A.** No account farm being built.
3. **Engineering budget — 2-3 person-quarter Layer-2 stealth-fleet build.** Approved-in-principle; engineering kickoff this week.
4. **GDPR build work** (LIA documentation, Art. 14 notice mechanism, data-subject deletion workflow). Build, not policy. ~2 engineer-weeks.
5. **Marketing/comms discipline** — no public boasting of bypass capability. SerpApi's "fortunate to be minimally impacted" line is being used against them; we don't repeat that mistake. Adopt a public position statement aligned with SerpApi MTD framing.

**Next concrete engineering step:** introduce the `PlaceFetcher` interface and the DB-backed browser-context record. That refactor is necessary first; everything else gates on (a) the detector telemetry (week 2-3), (b) the Python Camoufox sidecar (week 3-6), (c) the proxy integration (week 4-5).

---

## Changelog

- **2026-05-12 (pass 1):** Initial dossier consolidating eight parallel research streams.
- **2026-05-12 (pass 2):** Eleven corrections applied (see §0). Camoufox status revised (upstream active again Mar 2026); rebrowser-patches revised (dormant); SerpApi MTD hearing surfaced; SerpApi/Bright Data pricing corrected; Apify changelog quotes made verbatim; gosom #256 and #267 added; GDPR framing rewritten to reflect EDPB Opinion 28/2024 + 2024-2025 enforcement actions; Meta v. Bright Data clarified (Judge Chen, Meta dismissed remaining claim, no appeal).
- **2026-05-12 (pass 3 — CEO direction §-1):** Architecture re-baselined to SerpApi-aligned posture: logged-out only, no account farm (off the roadmap, not deferred), front-door execution of Google's JS challenges (no decryption, no token replay), marketing discipline (no bypass boasting). Session pool unit changed from "Google identity" to "configured logged-out browser context". Case number corrected (4:25-cv-10826). §-1, §6, §9 rewritten; §0 C6 marked overridden.
- **2026-05-12 (pass 4 — CEO direction "circumvention is core"):** §-1 ¶2 updated. The "Descramble / Decrypt / Impair" textualist defense was over-applied as an architectural constraint in pass 3; CEO has authorized circumvention of technical access controls as a core architectural capability. Plan may extract attestation tokens, replay them, deobfuscate bytecode VMs, treat SearchGuard as an adversarial system to defeat. Legal defense remains SerpApi-aligned ("§1201 doesn't reach bot-detection on public data") but operational success no longer depends on that defense being upheld. Marketing/comms discipline (no public bypass boasting; no published opcode tables) retained as operational practice, not architectural constraint. No private-data access / no logged-in scraping retained as core constraint (Bright Data v. Meta safe harbor).
