# App bundle audit: `/Applications/NNH Maps Scraper.app` vs repo

**Verdict: the installed app is effectively current.** It was built 2026-09-20 09:22; exactly one commit landed afterward (8aeb27e, 2026-09-20 23:19:31 +0400) and it only adds the `macos/` packaging scripts themselves — zero Go code. No `*.go` file in the repo is newer than the build, and the binary embeds the latest web assets. A rebuild is possible (go 1.27.1 matches `go.mod`), but see the `.build` cache mismatch caveat in Q4.

Note: the brief said the latest commit is from 2026-09-22; git shows the real HEAD commit is 2026-09-20 23:19:31 +0400 on `feat/web-ui-parity`. The Sep-22 dates on disk are in `~/webdata` (usage artifacts), not git. `git log --all` has no Sep-22 commit (only Droppy Code checkpoint refs from Sep 29).

---

## 1. What builds/packages the .app

Entry point: **`macos/build.sh`** (committed in 8aeb27e). Supporting files: `macos/Info.plist`, `macos/Launcher.swift`, `macos/theme.css`, `macos/assets/AppIcon.icns`. The Makefile has no .app target (`grep -i -E 'app|macos|dmg' Makefile` only shows plain `go build`/docker/cross-compile targets); `cmd/` and `scripts/` (DB migrations only) are not involved. `.gitignore` ignores the artifacts (`NNH Maps Scraper.app/`, `NNH-Maps-Scraper-arm64.dmg`, `macos/.build/`). `MacOS instructions.md` documents an older, superseded manual `GoogleMapsScraper.app` flow; the repo-root `GoogleMapsScraper.app` (Sep 19) is that earlier iteration.

Commands quoted from `macos/build.sh`:

```bash
# Go binary (skipped if cache exists, unless REBUILD_GO=1)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$BIN_CACHE" .)
#   BIN_CACHE="$ROOT/macos/.build/google_maps_scraper", APP="$ROOT/NNH Maps Scraper.app"

# assemble bundle
rm -rf "$APP"; mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$ROOT/macos/Info.plist"   "$APP/Contents/Info.plist"
cp "$ROOT/macos/theme.css"    "$APP/Contents/Resources/theme.css"
cp "$ROOT/macos/assets/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
cp "$BIN_CACHE"               "$APP/Contents/MacOS/google_maps_scraper"
xcrun swiftc -O "$ROOT/macos/Launcher.swift" -o "$APP/Contents/MacOS/GoogleMapsScraper" -framework Cocoa -framework WebKit

# ad-hoc signing
codesign --force --sign - "$APP/Contents/MacOS/google_maps_scraper"
codesign --force --sign - "$APP/Contents/MacOS/GoogleMapsScraper"
codesign --force --sign - "$APP"
codesign --verify --deep --strict "$APP"

# DMG
ditto "$APP" "$STAGE/NNH Maps Scraper.app"; ln -s /Applications "$STAGE/Applications"
hdiutil create -volname "NNH Maps Scraper" -srcfolder "$STAGE" -ov -format UDZO "$DMG"
codesign --force --sign - "$DMG"; hdiutil verify "$DMG"
```

Bundle identity matches `macos/Info.plist` byte-for-byte (`diff` → IDENTICAL): bundle id `com.local.googlemapsscraper`, CFBundleVersion `1.1`, name `NNH Maps Scraper`, executable `GoogleMapsScraper`. Resources also match the repo sources (theme.css sha `7ac2bfcf…`, AppIcon.icns sha `8c43ddcb…` in both places).

## 2. The launcher (`Contents/MacOS/GoogleMapsScraper`, 114,928 B)

It is the compiled `macos/Launcher.swift` (Swift/Cocoa + WebKit, ad-hoc signed). It is a native WebKit window that spawns the Go binary as a child process. Evidence from `strings` on the installed launcher:

```
Contents/MacOS/google_maps_scraper
http://127.0.0.1:
google_maps_scraper.log
No free local port between 8080 and 8199.
<html><body ...>Starting NNH Maps Scraper
```

From the source, on launch it:
- runs the sibling binary with **arguments: `-web -addr 127.0.0.1:<port> -data-folder $HOME/webdata`** (i.e. `process.arguments = ["-web", "-addr", "127.0.0.1:\(port)", "-data-folder", home.appendingPathComponent("webdata").path]`);
- **port: first free port in 8080…8199**, preferring 8080 (the log confirms: `listen tcp 127.0.0.1:8080: bind: address already in use`);
- **opens `http://127.0.0.1:<port>`** in a WKWebView, injecting `Contents/Resources/theme.css` as a "Deep Black" user style, single-instance, external links go to the default browser, downloads via NSSavePanel;
- redirects the server's **stdout/stderr to `~/google_maps_scraper.log`** (exists, 6,464,353 B, last write Sep 24 23:46:48 — `…Received signal, shutting down... server stopped`).

## 3. Where scraped results are written

The launcher passes `-data-folder ~/webdata`, so **all results go to `~/webdata`**, as `<UUID>.json` / `<UUID>.csv` next to a `jobs.db` SQLite database. `Contents/Resources` holds only `AppIcon.icns` and `theme.css` (no data). There is **no** `com.local.googlemapsscraper` directory under `~/Library/Application Support` (the `NNH*` entries there belong to other apps); the repo has no local `webdata/` or `gmaps-output/`. `~/Documents` contains downloaded copies of three of the UUID jsons (Sep 20 08:21) from the launcher's save-panel export.

All 9 entries in `~/webdata` (there are fewer than 10; newest first):

```
-rw-r--r--  32768 B  Sep 24 23:43  jobs.db-shm
-rw-r--r--      0 B  Sep 22 00:01  jobs.db-wal
-rw-r--r--  24576 B  Sep 22 00:01  jobs.db
-rw-r--r-- 378797 B  Sep 21 23:43  2d3bcee1-3d08-4b72-890d-8798771a3ec1.json   <- newest result
-rw-r--r-- 1272065 B Sep 20 08:18  e0086b93-d2c7-49b5-a37b-fee2cc8ccd44.json
-rw-r--r-- 1682704 B Sep 19 02:19  c358faa1-7407-47be-966e-25179039c0cd.json
-rw-r--r--  663078 B Sep 19 01:26  21c2d970-b7bb-454c-862e-1f4b7e0af2a6.json
-rw-r--r--  163296 B Jul  6 05:52  132f5e03-ff57-458a-9f44-b202d94d763d.csv
-rw-r--r-- 2148373 B Jul  6 05:48  58c44db9-59b3-4d2f-8aad-0bddddcf7393.csv
```

Last actual use: Sep 24 23:46 (log shutdown).

## 4. Is the installed binary stale?

Build vs git:

```
$ git log -1 --format='%H%n%cd'   (branch feat/web-ui-parity)
8aeb27eee76f404f1eb0492b0284ac837606d6aa
Sun Sep 20 23:19:31 2026 +0400

$ git log --since='2026-09-20 09:22' --format='%h %cd %s'
8aeb27e 2026-09-20 23:19:31 chore: commit pending local work
46a5b6a 2026-09-20 09:22:11 feat(web): the logo in the header, and as the favicon
```

- App bundle files all mtime **Sep 20 09:22**; every `*.go` file in the repo is **older** than the build (`find … -name '*.go' -newer Info.plist` → none; `gmaps/*.go` newer: 0). Working tree is clean (`git status --short | wc -l` → 0).
- **46a5b6a (09:22:11)** is inside the binary despite the same-minute timestamp: the installed Go binary contains `nnh-logo` (3 string hits), i.e. it was built from a working tree that already had `web/static/img/nnh-logo.png` (embedded via `web/web.go` go:embed).
- **8aeb27e (23:19:31)** changes only `macos/Info.plist`, `macos/Launcher.swift`, `macos/build.sh`, `macos/theme.css`, `macos/assets/AppIcon.icns`, `.gitignore`, and deletes `runner/.DS_Store` — all of which are already present in the installed bundle (plist identical, resources sha-identical). It touches no Go code.

**Plain answer: yes — the installed app contains the repository's current code.** The only commit it technically post-dates (8aeb27e) is the packaging commit whose outputs are already in the bundle; it cannot change app behavior. Nothing of substance is missing.

**Caveat for rebuilds:** `macos/.build/google_maps_scraper` (59,166,514 B, sha256 `afa40153…`) is **not** the binary inside the installed app (58,822,720 B, sha256 `19dade6c…`), even though both are mtime Sep 20 09:22 — apparently an earlier build iteration. The repo-root `NNH Maps Scraper.app` copy is sha-identical to the installed app (`19dade6c…` / launcher `6f544ca8…`). Since `build.sh` reuses the cache, a plain `macos/build.sh` re-run would package the stale cache binary — use `REBUILD_GO=1 macos/build.sh` for a faithful rebuild.

## 5. Go toolchain

`go.mod`: **`go 1.27.1`**. Machine: **`go version go1.27.1 darwin/arm64`** — satisfies the directive exactly. The installed binary was itself built with go1.27.1 (`strings` → `go1.27.1`). A rebuild works with the installed toolchain, e.g. `REBUILD_GO=1 macos/build.sh`.

---

*Read-only audit; no source files modified, nothing rebuilt, no scraper run. `gmaps/` was not read (only counted via `find -newer`).*
