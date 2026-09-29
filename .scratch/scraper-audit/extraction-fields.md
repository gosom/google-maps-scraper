# Scraper extraction-field audit

Scope: what business data each scraped place carries, and where CSV/JSON output is weak or ambiguous for a lead-management consumer. Everything below is read-only analysis of the code as it stands; no Go file was modified. Line numbers refer to the file paths given, e.g. `gmaps/entry.go:90`.

## Full field table

`Entry` struct is at `gmaps/entry.go:90-138`; CSV headers at `gmaps/entry.go:239-279`; CSV row at `gmaps/entry.go:281-321`. Headers and row are positionally locked (a test enforces equal length, `gmaps/entry_status_test.go:91-113`).

CSV formatting helper `stringify` (`gmaps/entry.go:985-997`): strings pass through verbatim; `float64` is `fmt.Sprintf("%f")` (6 decimals); everything else (ints, maps, slices, structs) is `json.Marshal`'d into the cell.

| Go field | CSV column | JSON key | Go type | Empty/zero in CSV | Empty in JSON |
|---|---|---|---|---|---|
| ID | `input_id` | `input_id` | string | empty cell | `""` |
| Link | `link` | `link` | string | empty cell (PlaceJob backfills with the job URL, `gmaps/place.go:66-68`) | `""` |
| Cid | `cid` | `cid` | string | empty cell | `""` |
| Title | `title` | `title` | string | empty cell | `""` |
| Categories | — (no CSV column) | `categories` | []string | n/a | `null` (no omitempty) |
| Category | `category` | `category` | string | empty cell | `""` |
| Address | `address` | `address` | string | empty cell | `""` |
| OpenHours | `open_hours` | `open_hours` | map[string][]string | `{}` | `null` (no omitempty; `getHours` can also return data) |
| PopularTimes | `popular_times` | `popular_times` | map[string]map[int]int | `{}` | `null` |
| WebSite | `website` | `web_site` | string | empty cell | `""` |
| Phone | `phone` | `phone` | string | empty cell | `""` |
| PlusCode | `plus_code` | `plus_code` | string | empty cell | `""` |
| ReviewCount | `review_count` | `review_count` | int | `0` (via json.Marshal, `entry.go:293`) | `0` |
| ReviewRating | `review_rating` | `review_rating` | float64 | `0.000000` (`%f`, `entry.go:989-990`) | `0` |
| ReviewsPerRating | `reviews_per_rating` | `reviews_per_rating` | map[int]int | `{}` | `null` |
| Latitude | `latitude` | `latitude` | float64 | `0.000000` | `0` |
| Longtitude | `longitude` | `longtitude` (+ alias `longitude`) | float64 | `0.000000` | `0` |
| Status | `status` | `status` | string | empty cell | `""` |
| Description | `descriptions` | `description` | string | empty cell | `""` |
| Descriptor | `descriptor` (last column, `entry.go:277`) | `descriptor` (struct order, `entry.go:119`) | string | empty cell | `""` |
| ReviewsLink | `reviews_link` | `reviews_link` | string | empty cell | `""` |
| Thumbnail | `thumbnail` | `thumbnail` | string | empty cell | `""` |
| Timezone | `timezone` | `timezone` | string | empty cell | `""` |
| PriceRange | `price_range` | `price_range` | string | empty cell | `""` |
| DataID | `data_id` | `data_id` | string | empty cell | `""` |
| StreetViewURL | `street_view_url` | `street_view_url` | string | empty cell | `""` |
| PlaceID | `place_id` | `place_id` | string | empty cell | `""` |
| Images | `images` | `images` | []Image (`entry.go:20-23`, keys `title`,`image`) | `null` | `null` |
| Reservations | `reservations` | `reservations` | []LinkSource (`entry.go:25-28`, keys `link`,`source`) | `null` | `null` |
| OrderOnline | `order_online` | `order_online` | []LinkSource | `null` | `null` |
| Menu | `menu` | `menu` | LinkSource | `{"link":"","source":""}` (no omitempty) | object with empty strings |
| Owner | `owner` | `owner` | Owner (`entry.go:30-34`, keys `id`,`name`,`link`) | `{"id":"","name":"","link":""}` | object with empty strings |
| CompleteAddress | `complete_address` | `complete_address` | Address (`entry.go:36-43`) | `{"borough":"","street":"","city":"","postal_code":"","state":"","country":""}` | object with empty strings |
| CreditCardsAccepted | `credit_cards_accepted` | `credit_cards_accepted` | []string | empty cell (joined, `entry.go:314,938-940`) | `null` |
| About | `about` | `about` | []About (`entry.go:45-55`) | `null` | `null` |
| UserReviews | `user_reviews` | `user_reviews` | []Review (`entry.go:57-86`) | `[]` or `null` (set to `[]` by parser, `entry.go:559,568`) | `[]` after parse; `null` if hand-built |
| UserReviewsExtended | `user_reviews_extended` | `user_reviews_extended` | []Review | `null` | `null` |
| Emails | `emails` | `emails` | []string | empty cell | `null` |

Note: `nil` slices/maps marshal as `null` (no `omitempty` anywhere on Entry), so JSON consumers must handle `null` vs `[]`.

## 1. CSV column names vs JSON keys

Where they differ, a consumer matching on names silently drops the field:

- `WebSite`: CSV `website` (`entry.go:248`) vs JSON `web_site` (`entry.go:102`).
- `Longtitude`: CSV `longitude` (`entry.go:255`) vs JSON `longtitude` (`entry.go:113`). JSON additionally emits a corrected `longitude` alias alongside the legacy key (`entry.go:149-157`, test `gmaps/entry_test.go:295-306`), so the JSON object contains both `longtitude` and `longitude`.
- `Description`: CSV `descriptions` (plural, `entry.go:258`) vs JSON `description` (singular, `entry.go:115`).
- `Categories`: JSON-only, `entry.go:95`; there is no CSV column for it anywhere in `CsvHeaders()` (`entry.go:240-278`). CSV consumers never see the full category list, only `Category` (first category, `entry.go:407-409`).
- `Descriptor` exists in both under the same name but sits in different positions (last CSV column `entry.go:277` vs mid-struct in JSON) — harmless for name-matched consumers, but positional consumers of one format must not assume the other's order.

## 2. `status` — exact possible values

Status can only ever be the empty string or the literal `"CLOSED"`. There is no "permanently closed", "temporarily closed", or "open" value anywhere; a project-wide search for `temporarily|permanently` finds nothing.

Population site, `gmaps/entry.go:432-439`:

```go
hoursLine := getNthElementAndCast[string](darray, 34, 4, 4)
closedEnum := getNthElementAndCast[string](darray, 88, 0)

switch {
case knownBusinessStatus(hoursLine):
	entry.Status = hoursLine
case knownBusinessStatus(closedEnum):
	entry.Status = closedEnum
}
```

Gatekeeper, `gmaps/entry.go:365-370`:

```go
func knownBusinessStatus(s string) bool {
	return s == "CLOSED"
}
```

Everything that is *not* exactly `"CLOSED"` (operating-hours lines like `Open ⋅ Closes 11 pm`, attributes like `Brunch`, prices like `AED 146`) is diverted into `Descriptor` (`entry.go:442-447`), and the tests pin this: `gmaps/entry_status_test.go:23-72`, `gmaps/entry_test.go:319-335`.

Consequences for a lead consumer: an operational business has `status = ""`; a closed one has `status = "CLOSED"`; "temporarily closed" is **not represented as a status** — if Google only exposes a temporary-closure marker through those slots, it arrives as free text in `descriptor` (e.g. a `Closed ⋅ Opens …` line, `entry_status_test.go:47-58`) and cannot be distinguished from editorial noise without parsing.

## 3. `owner` — shape in CSV vs JSON

The struct, `gmaps/entry.go:30-34`:

```go
type Owner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"`
}
```

Populated at `gmaps/entry.go:500-507`; `Link` is synthesized as `https://www.google.com/maps/contrib/<ID>` when an ID exists.

JSON: a real object, e.g. `{"id":"102769814432182832009","name":"Kipriakon (Owner)","link":"https://www.google.com/maps/contrib/102769814432182832009"}` (test fixture expectation `gmaps/entry_test.go:160-165`).

CSV: `stringify(e.Owner)` (`entry.go:312`) hits the default branch and marshals the whole struct to a JSON string (`entry.go:993-996`). The CSV cell therefore contains the serialized object `{"id":"...","name":"...","link":"..."}` — not a plain name. Because that text contains commas and double quotes, the encoding/csv writer wraps and escapes it, e.g. `"{""id"":""1027…"",""name"":""Kipriakon (Owner)"",""link"":""https://…""}"`. When the owner is unknown the cell is the literal string `{"id":"","name":"","link":""}`. A consumer that treats `owner` as a display name will show raw JSON; it must parse the cell (CSV unquote → JSON decode) to get `name` and the profile `link`.

## 4. `emails` — collection, optionality, multi-value CSV

Collection (`gmaps/emailjob.go`): a separate `EmailExtractJob` fetches the business website and runs `docEmailExtractor` (all `mailto:` links, validated and deduplicated, `emailjob.go:100-120`); if that finds nothing it falls back to `regexEmailExtractor` over the raw body (`emailjob.go:88-96`, extractor at `122-136`). The result is assigned wholesale: `j.Entry.Emails = emails` (`emailjob.go:87`). If the site fetch fails, the entry is returned with whatever it had — i.e. empty emails, no error (`emailjob.go:78-84`).

Optional: yes, flag-gated. `-email` CLI flag (`runner/runner.go:124`); `PlaceJob.ExtractEmail` (`gmaps/place.go:24`, set at `place.go:48`), checked in `gmaps/place.go:111`: `if j.ExtractEmail && entry.IsWebsiteValidForEmail()`. `IsWebsiteValidForEmail` (`gmaps/entry.go:207-223`) skips sites that are empty or contain `facebook`/`instragram` (sic — the typo means "instagram" is actually missed)/`twitter`, so those places keep empty emails even with the flag on. While the email job runs, the place's own result is withheld (`j.UsageInResults = false`, `gmaps/place.go:115`) and the entry is emitted only after the email job completes — so `emails` is always final when the row is written.

CSV cell for multiple emails: `stringSliceToString(e.Emails)` (`entry.go:318`) → `strings.Join(s, ", ")` (`entry.go:938-940`) → `info@x.com, sales@x.com`. Because the joined string contains commas, the CSV layer (scrapemate's `csvwriter` over Go `encoding/csv`, `runner/filerunner/filerunner.go:16,284,294`) quotes the whole cell: `"info@x.com, sales@x.com"`. Separator is `, ` (comma+space) inside a single quoted cell — not a list, not one row per email. No emails found → empty cell; JSON → `null`.

## 5. Exact CSV formatting of key numeric/id fields

- `review_count` (int, `entry.go:293`): decimal via `json.Marshal` — e.g. `396`; missing → `0`.
- `review_rating` (float64, `entry.go:294`): `%f`, always 6 decimals — `4.2` becomes `4.200000`; missing → `0.000000` (`entry.go:989-990`).
- `latitude` (float64, `entry.go:296`): `%f` — `34.670595399999996` becomes `34.670595` (6-decimal truncation, precision loss vs JSON); missing → `0.000000`.
- `longitude` (float64, `entry.go:297`): same `%f` truncation; missing → `0.000000`.
- `cid` (string, `entry.go:298`): raw string, e.g. `16519582940102929223`; missing → empty cell. Extracted from `jd[25][3][0][13][0][0][1]` (`entry.go:426`).
- `place_id` (string, `entry.go:307`): raw string, e.g. `ChIJDdnwdv0y5xQRRytw1ihZQeU`; missing → empty cell (`entry.go:457`).
- `link` (string, `entry.go:284`): raw URL; the parser may leave it empty (`entry.go:401`), in which case PlaceJob backfills it with the Google Maps URL actually visited (`gmaps/place.go:66-68`), so in practice it is rarely empty.

So: numeric fields never come out empty — a consumer must treat `0`, `0.000000` as "no data", and coordinates formatted at 6 decimals cannot be round-tripped to full float precision.

## 6. Flag/job-gated fields

- `emails` — only with `-email` (`runner/runner.go:124`, `gmaps/place.go:111`), and only for websites passing the social-media exclusion (`gmaps/entry.go:207-223`).
- `user_reviews_extended` (extra/full reviews) — only with `-extra-reviews` (`runner/runner.go:144`; `gmaps/place.go:34,48,120-142` fetches pages via `FetchReviewsWithFallback`, appended by `AddExtraReviews`, `gmaps/entry.go:323-334`; DOM fallback appended at `gmaps/place.go:72-83`). The web form equivalent is the `extrareviews` checkbox (`web/web.go:349`).
- `user_reviews` (inline reviews) — present by default but **suppressed** by `-reviews-metadata-only` (`runner/runner.go:145`), which makes the parser set it to an empty slice: `if metadataOnly { entry.UserReviews = make([]Review, 0) }` (`gmaps/entry.go:557-559`; flag wired at `gmaps/place.go:62`). Review counts/ratings/`reviews_per_rating` remain populated under this flag (`gmaps/entry_test.go:250-272`).
- Everything else (`about`, `popular_times`, `open_hours`, `images`, `complete_address`, etc.) is parsed unconditionally from the place payload (`gmaps/entry.go:395-554`), though individual slots can still be empty because Google omitted them. (`-fast-mode`, `runner/runner.go:140`, switches scraping mode rather than toggling any specific output field; not traced further.)

Note on `gmaps/completion.go:4-6`: it only defines the `CompletionTracker` interface (`SeedDiscovered`), used for resume bookkeeping (`runner/jobs.go:153`, `runner/resume/progress.go:33`); it does not gate any output field.

## Fields a consumer is most likely to lose

Name mismatches (CSV ≠ JSON key — name-matching pipelines drop these silently):

1. `website` (CSV) vs `web_site` (JSON) — `entry.go:102` vs `entry.go:248`.
2. `longitude` (CSV) vs `longtitude` (JSON legacy key; corrected `longitude` alias also present in JSON, so JSON has **both**) — `entry.go:113,149-157` vs `entry.go:255`.
3. `descriptions` (CSV, plural) vs `description` (JSON, singular) — `entry.go:258` vs `entry.go:115`.
4. `categories` (JSON only; no CSV column at all) — `entry.go:95`.

Ambiguous formats:

5. `owner` in CSV is a JSON object string with CSV-escaped quotes, empty case is `{"id":"","name":"","link":""}` — consumers expecting a plain name see garbage (`entry.go:30-34,312,993-996`).
6. `emails` joined with `", "` inside one quoted cell — splitting on `,` alone yields addresses with leading spaces; splitting on `", "` is required (`entry.go:938-940`).
7. Numeric emptiness is ambiguous: `review_count` = `0`, `review_rating`/`latitude`/`longitude` = `0.000000` are indistinguishable from real values without checking other fields; floats are truncated to 6 decimals (`entry.go:989-990`).
8. `status` is only `""` or `"CLOSED"`; temporarily-closed and attribute/hours text land in `descriptor` as untyped free text (`entry.go:365-447`), so "closed-ish" leads must be detected by parsing `descriptor`.
9. JSON `null` vs `[]` vs `{}` inconsistency: slice/map fields marshal as `null` when unset (no omitempty) but as `[]`/`{}` when explicitly set (e.g. `user_reviews` after parse, `entry.go:559,568`); struct fields (`menu`, `owner`, `complete_address`) always emit full objects with empty strings.
10. `Review` JSON keys are mixed-case: `Name`, `ProfilePicture`, `Rating`, `Description`, `Images`, `When` have no json tags while the rest are snake_case (`entry.go:57-86`) — a snake_case-only consumer loses author name/rating/text inside `user_reviews`/`user_reviews_extended`.
