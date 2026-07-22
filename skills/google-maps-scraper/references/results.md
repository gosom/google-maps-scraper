# Result Handling

CSV output contains a header followed by one business per row. JSON output is JSON Lines with one object per line.

Count the complete file and preview at most 20 records. Prefer these fields when present: `title`, `category`, `review_rating`, `review_count`, `phone`, `website`, `address`, and `emails`.

After the preview, offer:

1. Save or copy the complete results to a chosen location.
2. Analyze ratings, categories, neighborhoods, opening hours, websites, or missing contact data.
3. Filter by rating, review count, category, area, website, phone, or email availability.
4. Convert between CSV, JSON Lines, and a Markdown preview.
5. Run additional queries or broader coverage.

Do not imply that absent contact information means the business has none; it means the crawl did not capture it. Email extraction is slower because it visits business websites.
