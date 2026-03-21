---
name: google-maps-scraper
description: Scrape Google Maps business listings using Docker. Use when the user wants to find businesses, extract leads, emails, reviews, or ratings from Google Maps. Triggers on requests like "find all <business type> in <city>", "scrape Google Maps for <keyword>", "get leads from Google Maps". Keywords: google maps, scrape, business, leads, restaurants, shops, places, reviews, ratings, emails, contacts.
license: MIT
compatibility: Requires Docker installed and running.
metadata:
  author: gosom
  version: "1.11.0"
  repository: "https://github.com/gosom/google-maps-scraper"
allowed-tools: Bash(docker:*) Bash(touch:*) Bash(wc:*) Bash(mkdir:*) Read Write
---

# Google Maps Scraper

Scrape Google Maps to extract business listings, contact details, reviews, and leads using Docker.

## Interaction Flow

When the user requests a Google Maps scrape, follow this exact flow:

### Phase 1: Gather Requirements

Ask the user the following questions in a single message. Present them as a short numbered list. Use sensible defaults so the user can just confirm.

1. **What to search?** (already provided by the user — confirm it)
2. **Language** — What language should results be in? (default: `en`)
3. **Extract emails?** — Should we visit each business website to extract emails? This is slower but gives you contact emails. (default: no)
4. **Depth** — How many results per query? `shallow` (~20), `medium` (~100), `deep` (~200+) (default: shallow)
5. **Output format** — CSV or JSON? (default: CSV)
6. **Extra reviews?** — Collect up to ~300 reviews per business? (default: no)

Do NOT ask about Docker, proxies, geo coordinates, or other advanced flags unless the user brings them up.

### Phase 2: Prepare and Run

Once the user confirms (even a simple "go" or "yes" is enough):

**Step 1 — Build queries file**

Interpret the user's request into effective Google Maps search queries. Write one query per line to `/tmp/gmaps_queries.txt`.

Query writing tips:
- Be specific with location: "coffee shops in Manhattan, New York" not just "coffee shops"
- For broad city searches, split into neighborhoods for better coverage
- Use the target language when appropriate for the location

Example — user says "find dentists in Berlin":
```
dentists in Berlin Mitte
dentists in Berlin Kreuzberg
dentists in Berlin Charlottenburg
dentists in Berlin Prenzlauer Berg
dentists in Berlin Friedrichshain
dentists in Berlin Neukölln
dentists in Berlin Schöneberg
dentists in Berlin Tempelhof
```

**Step 2 — Map user choices to flags**

| Choice | Flag |
|--------|------|
| Language `XX` | `-lang XX` |
| Extract emails | `-email` |
| Depth: shallow | `-depth 1` |
| Depth: medium | `-depth 5` |
| Depth: deep | `-depth 10` |
| JSON output | `-json -results /results.json` |
| CSV output | `-results /results.csv` |
| Extra reviews | `-extra-reviews -json -results /results.json` (reviews require JSON) |

Never use a depth value higher than 10 unless the user explicitly requests it.

**Step 3 — Run the scraper in the background**

Always use `-exit-on-inactivity 3m` so the container stops automatically when done.

Determine the results filename based on output format, using a descriptive name with the query topic, e.g., `/tmp/gmaps_dentists_berlin.csv`.

```bash
touch /tmp/gmaps_<topic>_<city>.<ext>

docker run --rm \
  -v /tmp/gmaps_queries.txt:/queries.txt \
  -v /tmp/gmaps_<topic>_<city>.<ext>:/results.<ext> \
  gosom/google-maps-scraper \
  -input /queries.txt \
  -results /results.<ext> \
  -exit-on-inactivity 3m \
  <additional flags>
```

Run the docker command **in the background** so the user is not blocked. Tell the user:
- The scrape has started
- Estimated time (roughly 1 minute per query at shallow depth, longer with email extraction)
- You will notify them when it finishes

**Step 4 — Monitor and notify**

Once the background process completes, notify the user immediately and move to Phase 3.

### Phase 3: Present Results

When the scrape finishes:

1. **Read the results file** and count total results
2. **Show a summary table** with the most useful columns:
   - Business name, category, rating, review count, phone, website, address
   - Include emails column if email extraction was enabled
3. **Limit the table to 20 rows** — tell the user the total count
4. **Announce options:**

> Scraping complete! Found **N** businesses.
>
> Here's a preview of the top results: [table]
>
> What would you like to do?
> 1. **Save** — I'll save the full results to a location you choose
> 2. **Analyze** — Ask me anything about the data (e.g., "which have the best ratings?", "group by category", "find ones with websites but no email")
> 3. **Filter** — Narrow down by rating, category, area, or any criteria
> 4. **Export** — Convert to a different format (CSV/JSON/markdown table)

### Phase 4: Post-Processing

Handle the user's choice:

**Save**: Ask where they want the file saved, then copy it there.

**Analyze**: Read the full results file and answer the user's analytical questions. Examples:
- "Which businesses have the highest ratings?"
- "Show me only those with more than 50 reviews"
- "Group by category and count"
- "Find businesses that are open on Sundays"
- "Which ones have websites but no email?"
- "Calculate the average rating per neighborhood"

**Filter**: Apply the user's criteria and present a filtered table. Offer to save the filtered results.

**Export**: Convert between CSV, JSON, or markdown table format.

The user can keep asking for more analysis or follow-up scrapes. Stay in this phase until they're done.

## Advanced Options (only if user asks)

These flags can be added to the docker command:

| Flag | Description |
|------|-------------|
| `-geo "lat,lng"` | Center search on coordinates |
| `-zoom N` | Zoom level 0-21 (default: 15) |
| `-radius N` | Search radius in meters |
| `-fast-mode` | Quick extraction, up to 21 results per query |
| `-proxies "url1,url2"` | Comma-separated proxy URLs |
| `-c N` | Concurrency level (default: 2) |
| `-grid-bbox "minLat,minLon,maxLat,maxLon"` | Systematic area coverage |
| `-grid-cell N` | Grid cell size in km (default: 1.0) |

## CSV Columns Reference

The full list of available CSV columns:
`input_id`, `link`, `title`, `category`, `address`, `open_hours`, `popular_times`, `website`, `phone`, `plus_code`, `review_count`, `review_rating`, `reviews_per_rating`, `latitude`, `longitude`, `cid`, `status`, `description`, `reviews_link`, `thumbnail`, `timezone`, `price_range`, `data_id`, `images`, `reservations`, `order_online`, `menu`, `owner`, `complete_address`, `about`, `user_reviews`, `emails`

## Error Handling

- **Docker not found**: Tell the user to install Docker and ensure it's running
- **Empty results**: Suggest broadening the query, trying different neighborhoods, or checking language
- **Container errors**: Check if the Docker image needs pulling with `docker pull gosom/google-maps-scraper`
- **Slow performance**: Suggest reducing depth or disabling email extraction
