# Advanced Coverage

## Grid search

Use grid search when the user explicitly requests broad area coverage or a normal search clearly under-covers a large city. Obtain an approximate bounding box and choose a cell size:

- Dense urban area: `0.5` km
- Large city: `1.0` km
- Small town or sparse area: `2.0` km

Grid input contains the business type without neighborhood expansion. Use `-grid-bbox "minLat,minLon,maxLat,maxLon"`, `-grid-cell NUMBER`, and depth 5 or 10. Warn that a large grid can take 30 minutes or longer and cannot guarantee every listing.

Use `run-local.mjs --grid-bbox "minLat,minLon,maxLat,maxLon" --grid-cell NUMBER`. Both backends use the same validated options and status interface. Use `--concurrency NUMBER` to control concurrency. Other advanced flags below are scraper CLI flags; do not pass unsupported flags to the helper.

## Advanced flags

| Need | Flag |
|---|---|
| Center on coordinates | `-geo "lat,lng"` |
| Set map zoom | `-zoom NUMBER` |
| Set search radius | `-radius NUMBER` |
| Quick extraction | `-fast-mode` |
| Increase concurrent jobs | `-c NUMBER` |
| Extract emails | `-email` |
| Collect extra reviews | `-extra-reviews -json` |

Never use depth above 10 unless the user explicitly requests it. Increase concurrency gradually and explain the CPU, memory, and blocking trade-offs.

## Available fields

Common fields include `title`, `category`, `address`, `open_hours`, `website`, `phone`, `review_count`, `review_rating`, `latitude`, `longitude`, `status`, `description`, `price_range`, `place_id`, `images`, `owner`, `complete_address`, `user_reviews`, `user_reviews_extended`, and `emails`.
