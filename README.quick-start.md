# Quick Start Guide

## Project Structure

```
google-maps-scraper/
├── input/          # Query files (.txt)
├── output/         # Results (.csv)
├── scrape.sh       # Main script
└── quick-test.sh   # Fast test script
```

## Usage

### 1. Prepare Query File

Create a text file in `input/` with one query per line:

```bash
cat > input/queries.txt << EOF
restaurant in new york
cafe in new york
bar in new york
EOF
```

### 2. Run Scraper

**Basic usage:**

```bash
./scrape.sh queries.txt
```

**With geographic targeting:**

```bash
GEO="40.7128,-74.0060" RADIUS=5000 LANG=en ./scrape.sh queries.txt
```

**Full example:**

```bash
GEO="40.7128,-74.0060" \
RADIUS=5000 \
DEPTH=2 \
LANG=en \
CONCURRENCY=4 \
TIMEOUT=5m \
./scrape.sh queries.txt
```

### 3. Quick Test

For rapid testing with minimal timeout:

```bash
./quick-test.sh queries.txt
```

## Environment Variables

| Variable    | Description                  | Default |
|-------------|------------------------------|---------|
| GEO         | Coordinates (lat,lon)        | none    |
| RADIUS      | Search radius (meters)       | none    |
| DEPTH       | Search depth                 | 1       |
| LANG        | Language code (en, it, etc.) | en      |
| CONCURRENCY | Number of threads            | 2       |
| TIMEOUT     | Inactivity timeout           | 3m      |

## Output Files

Results are saved as: `output/<query-file>_<timestamp>.csv`

Example: `output/queries_20241202_143052.csv`

## Examples

### Example 1: Local Business Search

```bash
cat > input/restaurants.txt << EOF
italian restaurant in rome
pizza in rome
trattoria in rome
EOF

GEO="41.9028,12.4964" RADIUS=3000 LANG=it ./scrape.sh restaurants.txt
```

### Example 2: Multi-City Search

```bash
cat > input/cafes.txt << EOF
coffee shop in seattle
coffee shop in portland
coffee shop in san francisco
EOF

./scrape.sh cafes.txt
```

### Example 3: Deep Search

```bash
GEO="51.5074,-0.1278" RADIUS=10000 DEPTH=3 TIMEOUT=10m ./scrape.sh queries.txt
```

## View Results

```bash
# Count records
wc -l output/queries_*.csv

# View first 10 lines
head -n 10 output/queries_*.csv

# Open in spreadsheet
open output/queries_*.csv
```

## Troubleshooting

**No results found:**
- Check query syntax
- Increase DEPTH value
- Verify GEO coordinates if used

**Script not found:**
- Run `chmod +x scrape.sh quick-test.sh`

**Timeout too short:**
- Increase TIMEOUT value: `TIMEOUT=10m ./scrape.sh queries.txt`
