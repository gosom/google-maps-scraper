# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **Image extraction now works after Google Maps DOM changes** (2026-02-15)
  - Updated selectors to match new Google Maps structure:
    - Container: `div.m6QErb.XiKgde`, `div.EGN8xd`, `div[role="tabpanel"][aria-label="Photos"]`
    - Images: `button.xUc6Hf img[src*="googleusercontent.com"]`, `div.y5gUld img`
  - **Critical fix:** Extract images from initial view BEFORE attempting scroll (previous logic tried to scroll first and exited with 0 images)
  - Added hard safety limits to prevent infinite loops:
    - 10-second hard timeout on extraction
    - Max 5 scrolls (down from 10)
    - Exit after 1 stable scroll with no new images
  - Improved debug logging to track scroll attempts and image counts
  - Result: Now successfully extracts 8-15+ images per place (was finding only 1 thumbnail before)

### Technical Details
- Modified `gmaps/images/optimized_extractor.go`:
  - `ScrollAllTabMethod.scrollAndCollectImages()` - Added initial image extraction before scroll loop
  - `ScrollAllTabMethod.scrollGallery()` - Updated container selectors
  - `ScrollAllTabMethod.extractVisibleImages()` - Updated image element selectors
- JSON parsing fallback still creates minimal entries when APP_INITIALIZATION_STATE structure changes
- Core scraping functionality (name, address, phone, etc.) unaffected

### Known Issues
- JSON parsing crash (`index out of range [8]` in `gmaps/entry.go:806`) when Google's data structure differs - falls back to minimal entry creation (URL only)
- APP_INITIALIZATION_STATE extraction method not yet implemented

---

## [Previous versions]
_(Add past releases here)_
