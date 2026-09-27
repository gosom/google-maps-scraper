package gmaps

// What the email search found for one listing, written to the email_status column by both
// collectors, so "the site had no address" is told apart from "the site could not be read".
const (
	// EmailFound is a listing with at least one address.
	EmailFound = "found"
	// EmailNone is a website that was read and had no usable address.
	EmailNone = "none"
	// EmailNoWebsite is a listing without a website.
	EmailNoWebsite = "no_website"
	// EmailSkippedSocial is a "website" that is a social profile or directory page, not the
	// business's own site.
	EmailSkippedSocial = "skipped_social"
	// EmailSkippedBudget is a website v2 did not reach before its per-search time ran out.
	EmailSkippedBudget = "skipped_budget"
	// EmailFetchFailed prefixes why the website could not be read: timeout, redirect, http_4xx,
	// http_5xx, not_html or other.
	EmailFetchFailed = "fetch_failed:"
)

// emailOutcome is EmailFound or EmailNone for a site that was read.
func emailOutcome(emails []string) string {
	if len(emails) > 0 {
		return EmailFound
	}

	return EmailNone
}
