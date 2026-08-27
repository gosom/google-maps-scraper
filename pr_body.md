### Description
This PR resolves Issue #86 by adding proper pagination to the Web UI jobs list.

### Implementation Details
* **Backend:** Server-side pagination has been introduced. `JobRepository` now supports `Count`, and `SelectParams` includes an `Offset`. `Service` has a new `ListJobs` method, while `All` remains untouched to maintain backward compatibility for external API consumers.
* **Frontend:** The `/jobs` page was updated to parse the `?page=` query parameter. The HTML templates were adjusted to return HTMX `<tbody id="job-tbody" hx-swap="outerHTML">` bundled with out-of-band updates `<div id="pagination-container" hx-swap-oob="true">` for seamless pagination controls.

### Validation
- Validated that `?page=0` or invalid pages correctly default to page 1.
- Ensured existing filters and states are preserved.
- Existing tests compile and pass. Run `go test ./...` yields success.
