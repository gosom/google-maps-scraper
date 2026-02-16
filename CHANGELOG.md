# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

#### Image Extraction — Full Rewrite for Feb 2026 Google Maps DOM (2026-02-15 / 2026-02-16)

- **ROOT CAUSE FIX: Array bounds check in `entry.go:813`**
  - `getNthElementAndCast()` was missing `indexes[0] >= len(arr)` bounds check
  - Caused `index out of range [8] with length 8` panic on every place → `results_written: 0`
  - This was the primary reason staging showed 0 results despite places completing successfully

- **JavaScript-based image extraction (`extractViaJavaScript`)**
  - Google Maps now uses `background-image` CSS on `<div>` elements, NOT `<img>` tags
  - Only ~5 `<img>` elements exist at any time (virtualized list) vs 200+ background-image divs
  - New async JS runs inside browser context via `page.Evaluate()`:
    - Scrolls 1500px/step, up to 20 scrolls, 400ms lazy-load wait between scrolls
    - Stops after 3 consecutive scrolls with no new images
    - Collects from `window.getComputedStyle().backgroundImage` + `<img>` src + `<script>` tags
  - Falls back to DOM-based `scrollAndCollectImages()` if JS finds <3 images
  - Merges results from both methods for maximum coverage
  - **Result: 21–86 images per place** (up from 0)

- **Updated DOM selectors** (Google rolled out new classes twice in 24h):
  - `img.kSOdnb.Lyrzac` — main place photos (Feb 16)
  - `img.QUPxxe` — additional place photos (Feb 16)
  - `button.xUc6Hf img`, `div.y5gUld img` — previous structure (Feb 15, now deprecated)
  - Background-image selectors: `div[style*="googleusercontent.com"]`, `div[style*="background-image"][style*="lh3.goog"]`

- **Extraction robustness improvements**
  - Added hard safety limits: 15s timeout, max 8 scrolls for DOM method
  - Extract visible images BEFORE scroll loop (previous logic scrolled first, failed, exited with 0)
  - Photos tab panel (`div[role="tabpanel"][aria-label="Photos"]`) removed by Google — images now on main view
  - Improved debug logging: selector match counts, scroll attempts, image totals per method

#### Job Exit Logic — Stuck Jobs Fix (2026-02-16)

- **Exiter inactivity timeout improvements** (`exiter/exiter.go`)
  - Scaled timeout based on missing places: 30s for ≤2 missing, 60s for more
  - Added hard 2-minute inactivity cap — jobs exit regardless of missing place count
  - Previously jobs could hang indefinitely waiting for stuck Playwright workers

- **Webrunner backup timeout fix** (`runner/webrunner/webrunner.go`)
  - After backup timeout fires and context is cancelled, wait max 30s for `mate.Start()` to finish
  - If still stuck, force-close mate and proceed with partial results after 15s
  - Previously `<-done` blocked forever if Playwright workers were stuck in extraction timeout loops

### Technical Details
- Modified files:
  - `gmaps/entry.go` — bounds check fix in `getNthElementAndCast()`
  - `gmaps/images/optimized_extractor.go` — JS extraction, updated selectors, scroll logic
  - `exiter/exiter.go` — inactivity timeout scaling + hard cap
  - `runner/webrunner/webrunner.go` — backup timeout with bounded wait
- Core scraping functionality (name, address, phone, reviews, etc.) unaffected
- Test run: 35/40 places completed, avg 50+ images per place, 7 jobs/min throughput

### Known Issues
- ~10% of places get 0 images on first JS extraction attempt (JS finds 0 valid images initially), retries via scroll method — usually succeeds on second place visit by a different worker
- 3-4 places per batch may timeout on all extraction methods (places with very few or no user photos)
- APP_INITIALIZATION_STATE extraction not yet implemented (low priority — JS method covers this)

---

## [Previous versions]
_(Add past releases here)_
