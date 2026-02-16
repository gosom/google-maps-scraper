# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **Image extraction restored after Google Maps DOM changes** (2026-02-15 / 2026-02-16)
  - **Critical fix:** Extract images from initial view BEFORE attempting scroll (previous logic tried to scroll first, failed, exited with 0 images)
  - Updated image selectors twice as Google rolled out new classes:
    - `img.kSOdnb.Lyrzac` — main place photos (Feb 16)
    - `img.QUPxxe` — additional place photos (Feb 16)
    - `button.xUc6Hf img`, `div.y5gUld img` — previous structure (Feb 15, now deprecated)
  - Added hard safety limits to prevent infinite loops:
    - 10-second hard timeout on extraction
    - Max 5 scrolls (down from 10)
    - Exit after 1 stable scroll with no new images
  - Improved debug logging: selector match counts, scroll attempts, image totals
  - Photos tab panel (`div[role="tabpanel"][aria-label="Photos"]`) no longer exists — images are now on the main view

### Technical Details
- Modified `gmaps/images/optimized_extractor.go`:
  - `ScrollAllTabMethod.scrollAndCollectImages()` — extract visible images before scroll loop
  - `ScrollAllTabMethod.extractVisibleImages()` — updated selectors for Feb 2026 DOM
- JSON parsing fallback still creates minimal entries when data structure changes
- Core scraping functionality (name, address, phone, etc.) unaffected

### Known Issues
- JSON parsing crash (`index out of range [8]` in `gmaps/entry.go:806`) — falls back to minimal entry (URL only)
- `results_written: 0` on staging — dual writer not persisting scraped data (under investigation)
- APP_INITIALIZATION_STATE extraction not yet implemented

---

## [Previous versions]
_(Add past releases here)_
