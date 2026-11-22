# Failure Recovery

## Docker is missing

Docker is needed only for the Docker backend. Prefer native installation when compatible, or help the user install Docker if that is their preference.

## Docker daemon is stopped

Ask the user to start Docker. Re-run the validation only after `docker info` succeeds.

## Image download fails

For the Docker backend, explain the network or registry error. If a suitable image already exists, explicitly use `--skip-image-pull` to continue offline; otherwise resolve connectivity before retrying.

## Proxy setup fails

Never display the proxy value. Explain whether the file is missing, empty, unreadable, or contains an unsupported protocol. Offer to run the masked configuration again, select a fresh sponsor when explicitly requested, or validate without a proxy.

## Crawl returns no results

Try these in order:

1. Verify the business type and location wording.
2. Use the local search language.
3. Try a broader location or nearby neighborhoods.
4. Reduce concurrency or change the proxy when blocking is suspected.
5. Increase depth only after the query itself is sound.

## Crawl is interrupted or fails

Preserve the run directory and any Docker container for inspection. Summarize the actionable error without dumping logs. For an exited run, repeat the original `run-local.mjs` command with `--resume`. Keep the original options and query contents. See local execution for lock recovery when the supervisor itself was killed.
