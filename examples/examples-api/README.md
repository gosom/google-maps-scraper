# API Examples

Batch scraper clients in multiple languages. Each example submits keywords in parallel, polls for results, and saves completed jobs as JSON files.

## Setup

Replace `BASE_URL` and `API_KEY` with your values:

```bash
export BASE_URL="https://your-instance.example.com"
export API_KEY="gms_your_api_key_here"
```

## Python

No dependencies required (stdlib only). Requires Python 3.7+.

```bash
# Keywords as arguments
python python/scrape.py --base-url "$BASE_URL" --api-key "$API_KEY" "cafes in athens" "hotels in berlin"

# Keywords from file
cat keywords.txt | python python/scrape.py --base-url "$BASE_URL" --api-key "$API_KEY"

# Custom output directory and parallelism
python python/scrape.py --base-url "$BASE_URL" --api-key "$API_KEY" -o results -w 10 "cafes in athens"
```

## TypeScript

No dependencies required (Node.js builtins only). Requires Node.js 18+ and [tsx](https://github.com/privatenumber/tsx).

```bash
# Install tsx (one time)
npm install -g tsx

# Keywords as arguments
npx tsx typescript/scrape.ts --base-url "$BASE_URL" --api-key "$API_KEY" "cafes in athens" "hotels in berlin"

# Keywords from file
cat keywords.txt | npx tsx typescript/scrape.ts --base-url "$BASE_URL" --api-key "$API_KEY"

# Custom output directory and parallelism
npx tsx typescript/scrape.ts --base-url "$BASE_URL" --api-key "$API_KEY" -o results -w 10 "cafes in athens"
```

## Go

No dependencies required (stdlib only). Requires Go 1.21+.

```bash
# Keywords as arguments
go run go/main.go -base-url "$BASE_URL" -api-key "$API_KEY" "cafes in athens" "hotels in berlin"

# Keywords from file
cat keywords.txt | go run go/main.go -base-url "$BASE_URL" -api-key "$API_KEY"

# Custom output directory and parallelism
go run go/main.go -base-url "$BASE_URL" -api-key "$API_KEY" -o results -w 10 "cafes in athens"
```

## Options

All examples support the same options:

| Flag | Default | Description |
|---|---|---|
| `--base-url` / `-base-url` | *(required)* | API server URL |
| `--api-key` / `-api-key` | *(required)* | API key |
| `-o` / `--output` | `map-outputs` | Output directory for result files |
| `-w` / `--workers` | `20` | Maximum parallel jobs |

## Output

Results are saved as `{job_id}-{keyword_slug}.json` in the output directory. Each file contains the array of scraped places for that keyword.

## Sample Keywords

A `keywords.txt` file with 30 sample keywords is included. Use it to test:

```bash
cat keywords.txt | python python/scrape.py --base-url "$BASE_URL" --api-key "$API_KEY"
```
