# Query Planning

## Defaults

- Language: infer from the location when obvious; otherwise `en`
- Coverage: normal search
- Depth: `1`
- Format: CSV
- Email extraction: disabled
- Extra reviews: disabled
- Proxy: user choice

Ask only about missing information that changes the run. Do not make the user approve defaults they did not question.

## Writing useful searches

Use the business type plus a specific city, region, or country. Prefer `dentists in Berlin, Germany` over `dentists`.

For broad cities, create neighborhood queries when normal coverage is requested:

```text
dentists in Berlin Mitte
dentists in Berlin Kreuzberg
dentists in Berlin Charlottenburg
dentists in Berlin Prenzlauer Berg
```

Use the local search language when it improves Google Maps matching. Keep one query per line and do not add blank lines.

## Coverage levels

- Quick sample: one or a few queries at depth 1
- Normal search: location-specific or neighborhood queries at depth 1
- Comprehensive area: grid search with an explicit bounding box; read `advanced-coverage.md`

Do not describe a scrape as exhaustive. Google Maps ranking, blocking, coverage, and listing changes prevent completeness guarantees.
