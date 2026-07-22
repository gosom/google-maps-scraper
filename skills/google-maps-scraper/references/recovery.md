# Failure Recovery

## Docker is missing

Explain that local execution requires Docker. Ask the user to install Docker Desktop or Docker Engine for their supported platform, then repeat validation.

## Docker daemon is stopped

Ask the user to start Docker. Re-run the validation only after `docker info` succeeds.

## Image download fails

The run helper retries once. If both attempts fail, show the concise network or registry error and suggest checking connectivity before retrying.

## Proxy setup fails

Never display the proxy value. Explain whether the file is missing, empty, unreadable, or contains an unsupported protocol. Offer to run the masked configuration again, select a fresh sponsor set when explicitly requested, or validate without a proxy.

## Crawl returns no results

Try these in order:

1. Verify the business type and location wording.
2. Use the local search language.
3. Try a broader location or nearby neighborhoods.
4. Reduce concurrency or change the proxy when blocking is suspected.
5. Increase depth only after the query itself is sound.

## Crawl is interrupted or fails

Preserve the output directory and stopped container for inspection. Summarize the actionable error without dumping logs. Explain that Release 1 reruns the affected crawl; it does not provide checkpoint-level resume.
