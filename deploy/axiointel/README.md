# Collecting for AxioIntel

This folder runs Axio-CRED's collector on a schedule and sends what it collects to AxioIntel.
`collect.sh` first asks AxioIntel which places to collect, with `fetch_targets.py`; then, for
each place, it opens the public Google Maps listing with the collector, using the same arguments
Axio-CRED's app uses for an exact Place ID, including `-extra-reviews`. It hands the results to
`push_native.py`, AxioIntel's sender, which signs them and posts them to
`https://axiointel.com/api/ingest/native`.

Asking is a read. AxioIntel never runs, schedules or calls this collector: it answers with a
list, and this machine decides on its own clock what to do with it.

**Since 27 Sep 2026 Apify collects each place's whole review list, and this collector keeps it
current in between** (owner's decision). Google now shows signed-out visitors a *limited view* of
a place's reviews: five to eight of them, however far the list is scrolled, from every IP address
tried (all 20 proxies in the pool, 27 Sep 2026). So Apify does a place's first pull and a weekly
full re-pull, which is what removal detection needs, and every two hours this collector reads the
reviews the listing shows **sorted by Newest** (`COLLECTOR_REVIEW_MODE=newest`, the default,
which passes `-review-newest`). A read whose Newest sort cannot be confirmed from the reviews' own
dates is not sent (`unsorted`, below): the listing's "most relevant" reviews must never pass for
its newest.

A person's "Pull now" and a profile that is due mark the place *requested* or *due*;
`collect.sh daemon` polls every two minutes, collects requested places first, up to three at
once, and sends each one as soon as it is done. A newest-only read takes about a minute.

Nothing here reports, flags or appeals a review. The collector reads public pages. AxioIntel
stores what arrives and never treats it as proof that anyone owns a place.

## Lab mode (the default when nothing is set)

`COLLECTOR_MODE` is `lab` unless set otherwise; production runs `send` (below). Lab mode is for
trying a change on a list of places without AxioIntel seeing any of it. In lab mode `collect.sh`:

- collects only the places file you give it (`collect.sh places.txt`, or `COLLECTOR_PLACES_FILE`);
- never asks AxioIntel for targets and never sends it anything, so it needs no secret, and
  `push_native.py` and `fetch_targets.py` need not be on the machine;
- keeps every collection in `<work>/<place>/<time>/results.jsonl` and adds one line per place to
  `<work>/lab/summary.csv` (collected, reported, complete or partial, why it stopped, rotations,
  blocks, seconds).

The same summary line is written in send mode too (with a `sent` column): it is this machine's
own record of the collector's speed and coverage, beside the speed AxioIntel measures from what
arrives. `find_emails.sh` never sends anything, in either mode.

## Where it runs

On a machine that has **no link to AxioIntel's Google Cloud project**: its own provider
account, its own billing and its own IP addresses. Do not run it on Cloud Run, Cloud Shell or
any VM in the `axiointel` project. AxioIntel's project is the one Google is reviewing for
Business Profile API access.

A small Linux VM is enough, collecting one place at a time: Ubuntu 24.04, 2 vCPU, 4 GB RAM,
20 GB disk. Chromium is the heavy part. Hetzner and DigitalOcean both work, and
`infra/hetzner` and `infra/digitalocean` in this repository can provision one.

## Setting it up

1. **Install Docker, Python 3 and git.**
   ```bash
   sudo apt-get update && sudo apt-get install -y docker.io python3 git
   ```

2. **Build the collector image** from a checkout of this repository.
   ```bash
   git clone https://github.com/AxioIntel/Axio-CRED.git && cd Axio-CRED
   sudo docker build -t axio-cred-collector .
   ```

3. **Fetch AxioIntel's own programs** into this folder: the sender, and the one that asks what
   to collect. Neither is copied into this repository, so neither can drift from the endpoint it
   talks to. Axio-Backend's tests hold both to that contract.
   ```bash
   for f in push_native.py fetch_targets.py; do
     gh api -H "Accept: application/vnd.github.raw" \
       "repos/AxioIntel/Axio-Backend/contents/scripts/$f" > "deploy/axiointel/$f"
   done
   ```
   Fetch them again whenever Axio-Backend changes them.

4. **Give it the shared secret.** Read the value in Cloud Shell with
   `gcloud secrets versions access latest --secret native-ingest-secret --project axiointel`.
   Put it on this machine only, in a root-owned file:
   ```bash
   sudo install -d -m 700 /etc/axiointel
   sudo sh -c 'umask 077; printf "AXIOINTEL_NATIVE_INGEST_SECRET=%s\n" "PASTE-THE-VALUE" > /etc/axiointel/collector.env'
   ```
   Never commit it, never paste it into a chat, and rotate it (a new secret version, then this
   file) if it has been anywhere else.

5. **Run it once against staging**, and watch the first place come through before production
   ever sees this machine. `AXIOINTEL_BASE_URL` moves both the question and the answer to
   another deployment. It is the API's base, ending in `/api` for the site and without it for
   the Cloud Run service, which answers either way.
   ```bash
   sudo AXIOINTEL_BASE_URL=<staging base URL> deploy/axiointel/collect.sh
   ```
   Staging needs its own `native-ingest-secret`, and `collector.env` must hold that one while
   this run is pointed at it. `collect.sh` asks that deployment what it is watching, collects
   the first place on the list, and posts it back. A profile appears there under `native/<place id>`. A place with a few
   hundred reviews takes several minutes; the collector's budget is 20 minutes per place. How
   long each place took is sent with it, and AxioIntel's **Pull speed** screen sets it beside
   pulls of the same place made from the dashboard.

   To collect a list of your own instead of asking -- to try one particular place, or while a
   deployment has no targets endpoint yet -- name it:
   ```bash
   cp deploy/axiointel/places.example.txt deploy/axiointel/places.txt
   sudo COLLECTOR_PLACES_FILE=deploy/axiointel/places.txt deploy/axiointel/collect.sh
   ```

6. **Run it once against production**, the same way without `AXIOINTEL_BASE_URL`.
   ```bash
   sudo deploy/axiointel/collect.sh
   ```

7. **Run it as a service.** The daemon polls AxioIntel every `COLLECTOR_POLL_SECONDS` (120),
   collects requested places first, then places that are due, up to `COLLECTOR_PARALLEL` (3) at
   once, and never the same place twice at once. A due place that is not requested is collected
   at most every `COLLECTOR_MIN_INTERVAL_HOURS` (1); a place whose last try failed waits
   `COLLECTOR_RETRY_MINUTES` (10).
   ```bash
   sudo deploy/axiointel/collect.sh install-daemon   # systemd unit axio-collector, restarts on failure
   deploy/axiointel/collect.sh status                # the service and the last 20 summary lines
   journalctl -u axio-collector -f                   # what it is doing now
   ```
   Stopping the service (`sudo systemctl stop axio-collector`) stops its containers too. Rolling
   back is AxioIntel's `COLLECTION_PRIMARY=apify`; the service may keep sending meanwhile.

## Proxies

Google rate-limits a single datacenter IP address that loads many listings. Put one proxy URL
per line in a root-owned file and set `COLLECTOR_PROXIES_FILE=/etc/axiointel/proxies.txt` in
`collector.env`. The log is scrubbed of proxy credentials after every run.

Use ISP or residential proxies, five to ten of them. Google refuses datacenter addresses on the
review endpoint (a 403 on every page), which is what the coverage audit of 9 September 2026 saw.

With proxies set, every review request goes through one. The browser takes the first proxy. The
collector's own review requests start at the second and move to the next whenever Google refuses
one (a 403, a 429, a `/sorry/` page or a CAPTCHA), asking for the same page again. Each proxy keeps
its own browser signature, user agent and cookies, and nothing carries over from one to the next.
When every proxy has been refused, the collector scrolls the public page for whatever is left,
and reports `blocked` if that falls short too. A proxy line that does not parse turns the review
requests off altogether: the collector never falls back to this machine's own address.

## What it collects

`collect.sh` asks AxioIntel, every run, which places it is watching: every public place a
workspace watches or the house still holds, once each however many sources have read it, with
the one that has waited longest first. A list edited by hand here would go stale the moment a
workspace watched something new, so there is none to keep up to date.

The question is signed with the same secret as a push, so the collector carries one credential.
If AxioIntel answers `401` the secret here is not the one it holds, or this machine's clock is
more than five minutes out; if it answers `503`, that deployment has no secret configured. Both
are printed in full and the run stops without collecting.

## What it keeps

Each run writes `query.txt`, `results.jsonl` and `collector.log` under
`/var/lib/axiointel-collector/<place id>/<time>/`. The results contain reviewers' names and
words, and are needed only until AxioIntel has them, so runs older than 14 days are deleted at
the start of every collection. Set `COLLECTOR_KEEP_DAYS` to change that.

## When a place is not complete

When the collector times out or exits with an error, whatever it gathered is still sent, but
marked incomplete. AxioIntel then stores those reviews without treating the ones it lacks as
removed. When the collector finds fewer distinct reviews than the listing shows, the sender
also marks the collection incomplete, and for the same reason.

The collector also reports on each place itself, as `review_collection` in `results.jsonl`, and
`collect.sh` logs it as one line:

```
2026-09-25T02:41:07Z ChIJ...: 1169/1169 reviews, complete (done, stage -, 1 rotation(s), 1 block(s))
```

Anything it does not call complete is sent as incomplete, even when the container exited cleanly.
`stop_reason` says why it stopped:

| `stop_reason` | Meaning |
| --- | --- |
| `done` | Every page was read, and the reviews reach the listing's count. |
| `exhausted` | The listing ran out of pages short of its count. Google's count includes reviews it does not show. |
| `blocked` | Google refused every proxy, and scrolling the public page did not make up the rest. |
| `budget` | The collector's own clock ran out. What it had is kept. |
| `cap` | The most reviews one place keeps was reached (`-review-max`, default 5,000). |
| `parse_error` | Google changed the shape of its review pages. Rotating proxies does not fix this; the parser needs updating. |
| `count_unknown` | The listing's review count could not be read, although it shows reviews. Never complete. |
| `no_reviews` | The listing has no reviews. |
| `newest` | Newest mode: the reviews the listing shows sorted by Newest were read. Never complete. |
| `unsorted` | Newest mode: the Newest sort could not be confirmed, so nothing was kept or sent. |

## Sizing the time budgets

In newest mode (the default) a place takes about a minute and `COLLECTOR_TIMEOUT_SECONDS`
defaults to 240. The rest of this section is about `COLLECTOR_REVIEW_MODE=full`, which pages
through the whole list and is kept for lab runs.

Two clocks apply. `COLLECTOR_TIMEOUT_SECONDS` (default 900 in full mode) is when `collect.sh` kills the container. The collector's own review clock, `-review-budget`, is set four minutes under it, so
that a slow place is still written out as a partial collection rather than killed and lost. At
the default pace a place takes roughly a second per 20 reviews: about 3 minutes for 1,169 reviews
and 11 minutes for 5,000. A larger listing arrives partial, which AxioIntel never reads as
removals, and the next pass finishes it; raising `COLLECTOR_TIMEOUT_SECONDS` past 900 means a
person pulling such a place is served by Apify first. The review clock follows. Pass `-review-max` to the image as well to keep more than 5,000 per place.

## Lead jobs: collector v1 and v2

The leads dashboard (`leads_dashboard.sh`) runs every lead job on one of two collectors, chosen
per job in the scrape form's **Collector** field (or `"collector": "v2"` in `POST /api/v1/jobs`).
Every job in the list carries a `v1` or `v2` chip.

- **v1** is the browser collector the dashboard has always used, and stays the default. It
  scrolls Google Maps in Chromium and reads places and websites several at a time. Up to four
  contact pages are read per website.
- **v2** runs one search at a time with no browser. It reads Google's own results request, 20
  places a page, through the proxies. It then visits each place's website directly, one after
  another, a fixed gap apart (1 s by default), front page only unless told otherwise. Each
  search has a fixed time limit (4 minutes by default, 8 at most). A job therefore never runs
  longer than its searches times that limit; the job log states the bound when it starts. A
  search that runs out of time keeps every place it read, and websites it did not reach are
  marked `skipped_budget`.

Both collectors now write an `email_status` column: `found`, `none`, `no_website`,
`skipped_social`, `skipped_budget`, or `fetch_failed:<reason>`. The reason is one of `timeout`,
`redirect`, `http_4xx`, `http_5xx`, `not_html` or `other`. "The site had no address" is now told
apart from "the site could not be read". A job that runs out of its "Stop after" time now ends
`partial` instead of `ok`; what it found is kept either way.

### Verifying the collectors (the Verify v1 / v2 page)

- **Smoke check** after every deploy: press "Run smoke check" on the v1 and v2 cards. Each runs
  one search next in the queue, then checks it end to end. The checks cover:
  - that it finished `ok` inside its bound;
  - that every row names its place and its search;
  - that every place reached the lead list;
  - that every row has an `email_status`;
  - that there is one ledger row per search.

  v2 also checks that every search stayed inside its limit and that websites were read one at
  a time. A red card names the check that failed. `GET /api/v1/verify/smoke` returns the same
  for a cron job or CI.
- **Comparison**: pick the searches (30 US-county trade searches are filled in), the runs per
  collector (3 is enough for a verdict) and each collector's settings, then start it. The jobs
  go into the queue alternating v1, v2, v1, v2, so both meet the same hours and proxies.
  - A job stopped by the pause window or AxioIntel's yield is kept but not counted.
  - "Stop" drops the comparison's queued jobs; the one running finishes.

  The results table shows each collector's medians over its fair runs, with the range. It covers
  places and unique emails per search, websites with an email, unreadable websites, seconds per
  search (p50, p95, slowest), wall time against the bound, Google refusals, restarts, stalls, and
  CPU and RAM. v1's time per search is job-level: its searches run in parallel. Both tables
  export as CSV.
- **Verdict**: v2 is judged more predictable when all five of these hold:
  1. Its p95 search time is at most twice its p50, and every job ended inside its bound.
  2. It had no partial or failed job, stall or restart.
  3. Its run-to-run spread of places and emails per search is at most 15% and below v1's.
  4. Google refused it no more often than v1.
  5. It found at least 70% of v1's unique emails. If only this fails, the optional "v2 deep" arm
     (two contact pages per site) is checked against the same bar.

  Until each collector has three fair runs, the page says how many more it needs.

The numbers come from the run ledger in `leads.db`: `search_runs` (one row per search) and
`job_runs` (one per job). `GET /api/v1/runs?job=<id>` returns a job's rows. Time of day and proxy
health affect both collectors, so compare runs from the same comparison, not across days.

From the command line, `-collector v2` runs a queries file the same way:
`-v2-pages`, `-v2-delay`, `-v2-contact-pages`, `-v2-query-budget`, `-v2-site-timeout`,
`-v2-retries` and `-v2-no-redirects` are its settings. It prints one line per search on stderr.

## Exit status

`collect.sh` exits `0` when every place was collected and sent, and `1` when any place was
not. The log names each place that failed and why.
