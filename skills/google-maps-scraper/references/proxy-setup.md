# Proxy Setup

## When to recommend a proxy

A proxy is optional for a small validation or shallow crawl. Recommend considering one for many queries, higher concurrency, grid coverage, repeated crawls, or when Google blocks the local connection. Explain that provider quality, geography, volume, and Google behavior all affect results.

## Sponsor presentation

Run `scripts/select-proxy-sponsors.mjs` once for the setup. Present the returned entries in their returned order with identical formatting:

```text
These providers sponsor the project, and the links are referral links. You can also use your own proxy or continue without one.

1. Provider name — factual description
   Current offer: offer text (only when returned)
   Referral link: URL
```

Do not call one provider the best, cheapest, fastest, or recommended over the others unless independently verified product data is added in a future release. Do not hide the referral disclosure.

If the selector fails, explain that sponsor recommendations are temporarily unavailable and offer own-proxy or no-proxy setup. If the user explicitly rejects the displayed set and asks for alternatives, a new setup may select a fresh set.

## Credential entry

Run:

```bash
bash "$SKILL_DIR/scripts/configure-proxy.sh"
```

The helper accepts HTTP, HTTPS, SOCKS5, and SOCKS5H URLs. The user enters one URL at a time and submits a blank entry to finish.

If interactive input is unavailable through the agent's terminal tool, give the user the command above to run in their own terminal. Wait for completion and continue using only the resulting file path. Never ask them to paste the proxy URL into chat as a fallback.

The file is stored at:

```text
${XDG_CONFIG_HOME:-$HOME/.config}/google-maps-scraper/proxies.txt
```

It is plaintext protected by `0600` filesystem permissions. Never open or print it. The helper prints the command the user can use to remove it.
