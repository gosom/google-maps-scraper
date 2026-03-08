#!/usr/bin/env python3
"""Batch Maps scraper client.

Submits keywords in parallel (up to 20 at a time), polls for results,
and saves each completed job to a JSON file.

Usage:
    # Keywords as arguments
    python scrape.py --base-url https://example.com --api-key gms_... "cafes in athens" "hotels in berlin"

    # Keywords from stdin (one per line)
    cat keywords.txt | python scrape.py --base-url https://example.com --api-key gms_...

    # Custom output directory
    python scrape.py --base-url https://example.com --api-key gms_... -o results "cafes in athens"

    # Skip TLS certificate verification (e.g. self-signed certs)
    python scrape.py --base-url https://example.com --api-key gms_... --insecure "cafes in athens"
"""

import argparse
import json
import os
import re
import ssl
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from urllib.error import HTTPError
from urllib.request import Request, urlopen


def safe_filename(s: str) -> str:
    """Convert a string to a safe filename component."""
    s = s.strip().lower()
    s = re.sub(r"[^\w\s-]", "", s)
    s = re.sub(r"[\s]+", "_", s)
    return s[:100]


def api_request(base_url: str, api_key: str, method: str, path: str, body=None,
                ssl_ctx=None):
    """Make an API request and return parsed JSON."""
    url = base_url.rstrip("/") + path
    headers = {
        "X-API-Key": api_key,
        "Content-Type": "application/json",
    }
    data = json.dumps(body).encode() if body else None
    req = Request(url, data=data, headers=headers, method=method)

    resp = urlopen(req, timeout=30, context=ssl_ctx)
    return json.loads(resp.read())


def submit_job(base_url: str, api_key: str, keyword: str, lang: str = "en",
               max_depth: int = 1, ssl_ctx=None) -> dict:
    """Submit a scrape job and return {"job_id": ..., "keyword": ...}."""
    resp = api_request(base_url, api_key, "POST", "/api/v1/scrape", {
        "keyword": keyword,
        "lang": lang,
        "max_depth": max_depth,
    }, ssl_ctx=ssl_ctx)
    return {"job_id": resp["job_id"], "keyword": keyword}


def poll_job(base_url: str, api_key: str, job_id: str, keyword: str,
             output_dir: str, poll_interval: float = 5.0, ssl_ctx=None):
    """Poll a job until completion, then save results."""
    while True:
        resp = api_request(base_url, api_key, "GET", f"/api/v1/jobs/{job_id}",
                           ssl_ctx=ssl_ctx)
        status = resp.get("status", "")

        if status == "completed":
            fname = f"{job_id}-{safe_filename(keyword)}.json"
            path = os.path.join(output_dir, fname)
            with open(path, "w") as f:
                json.dump(resp.get("results", []), f, indent=2)
            count = resp.get("result_count", 0)
            print(f"  [done] {keyword!r} -> {count} results -> {fname}")
            return

        if status == "failed":
            err = resp.get("error", "unknown error")
            print(f"  [fail] {keyword!r}: {err}", file=sys.stderr)
            return

        time.sleep(poll_interval)


def process_keyword(base_url: str, api_key: str, keyword: str, output_dir: str,
                    lang: str = "en", max_depth: int = 1, ssl_ctx=None):
    """Submit a keyword, poll until done, save results."""
    try:
        job = submit_job(base_url, api_key, keyword, lang=lang, max_depth=max_depth,
                         ssl_ctx=ssl_ctx)
    except HTTPError as e:
        body = e.read().decode()
        print(f"  [error] submit {keyword!r}: HTTP {e.code} {body}", file=sys.stderr)
        return
    except Exception as e:
        print(f"  [error] submit {keyword!r}: {e}", file=sys.stderr)
        return

    print(f"  [submitted] {keyword!r} -> job {job['job_id']}")
    poll_job(base_url, api_key, job["job_id"], keyword, output_dir, ssl_ctx=ssl_ctx)


def main():
    parser = argparse.ArgumentParser(
        description="Batch Maps scraper client",
    )
    parser.add_argument("--base-url", required=True, help="API base URL")
    parser.add_argument("--api-key", required=True, help="API key")
    parser.add_argument("-o", "--output", default="map-outputs",
                        help="Output directory (default: map-outputs)")
    parser.add_argument("-w", "--workers", type=int, default=20,
                        help="Max parallel jobs (default: 20)")
    parser.add_argument("--lang", default="en",
                        help="Language for results (default: en)")
    parser.add_argument("--max-depth", type=int, default=1,
                        help="Max scrape depth (default: 1)")
    parser.add_argument("-k", "--insecure", action="store_true",
                        help="Skip TLS certificate verification (e.g. for self-signed certs)")
    parser.add_argument("keywords", nargs="*",
                        help="Keywords to scrape (reads from stdin if none given)")

    args = parser.parse_args()

    ssl_ctx = None
    if args.insecure:
        ssl_ctx = ssl.create_default_context()
        ssl_ctx.check_hostname = False
        ssl_ctx.verify_mode = ssl.CERT_NONE

    keywords = args.keywords
    if not keywords:
        if sys.stdin.isatty():
            parser.error("provide keywords as arguments or pipe them via stdin")
        keywords = [line.strip() for line in sys.stdin if line.strip()]

    if not keywords:
        parser.error("no keywords provided")

    os.makedirs(args.output, exist_ok=True)

    print(f"Scraping {len(keywords)} keyword(s), max {args.workers} parallel, output -> {args.output}/")

    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = {
            pool.submit(process_keyword, args.base_url, args.api_key, kw, args.output,
                        args.lang, args.max_depth, ssl_ctx): kw
            for kw in keywords
        }
        for future in as_completed(futures):
            exc = future.exception()
            if exc:
                kw = futures[future]
                print(f"  [error] {kw!r}: {exc}", file=sys.stderr)

    print("Done.")


if __name__ == "__main__":
    main()
