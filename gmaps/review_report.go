package gmaps

// Why a place's review collection stopped. Every run with `-extra-reviews` ends on exactly one.
const (
	// stopDone: every page the listing served was read and the union reaches the reported total.
	stopDone = "done"
	// stopExhausted: the listing ran out of pages (or the page budget did) short of the reported
	// total -- Google's displayed count includes reviews it does not serve. Not complete.
	stopExhausted = "exhausted"
	// stopBudget: the review path's wall clock ran out; what was collected is kept.
	stopBudget = "budget"
	// stopBlocked: Google refused -- 403, 429, a /sorry/ page, a CAPTCHA -- and nothing after it
	// finished the job.
	stopBlocked = "blocked"
	// stopCap: the most reviews one collection keeps (`ReviewConfig.MaxReviews`) was reached.
	stopCap = "cap"
	// stopParseError: a page came back in a shape the parser cannot read -- the `pb` request or
	// the protobuf indices in `parseReviews` have drifted. Retrying elsewhere does not fix it.
	stopParseError = "parse_error"
	// stopCountUnknown: the listing's review count could not be read, but the place shows reviews.
	// Whatever was collected is kept; it is never complete, since complete against what?
	stopCountUnknown = "count_unknown"
	// stopNoReviews: the listing reports no reviews and shows none. Nothing to collect.
	stopNoReviews = "no_reviews"
	// stopError: anything else -- a transport failure that outlasted its retries.
	stopError = "error"
	// stopNewest: a newest-only collection (`ReviewConfig.NewestOnly`) read the reviews the public
	// listing shows sorted by Newest. Never the whole list, so never complete.
	stopNewest = "newest"
	// stopUnsorted: a newest-only collection could not confirm the Newest sort took. What the
	// listing showed may be its "most relevant" reviews, which would pass for the newest ones, so
	// nothing is kept and nothing is sent.
	stopUnsorted = "unsorted"
)

// Which part of the review path a collection stopped in.
const (
	stagePlace      = "place"
	stageRPCBrowser = "rpc_browser"
	stageRPCHTTP    = "rpc_http"
	stageDOM        = "dom"
)

// ReviewCollection is how one place's review collection went, written beside the entry in the
// results so whoever reads them -- `collect.sh`, the sender, AxioIntel -- can tell a whole
// collection from a partial one without re-deriving it. Additive only: every field existing
// consumers read is untouched, and an entry collected without `-extra-reviews` carries none of it.
type ReviewCollection struct {
	Reported      int  `json:"reported"`
	ReportedKnown bool `json:"reported_known"`
	// Collected is the distinct union of `user_reviews` and `user_reviews_extended`, by review id.
	Collected int  `json:"collected"`
	Complete  bool `json:"complete"`
	// WithoutID counts reviews kept that carry no id: written out, never part of Collected.
	WithoutID int `json:"without_id,omitempty"`

	StopReason string   `json:"stop_reason"`
	StopStage  string   `json:"stop_stage,omitempty"`
	StopDetail string   `json:"stop_detail,omitempty"`
	Stages     []string `json:"stages"`

	RPCPages          int `json:"rpc_pages"`
	RPCReviews        int `json:"rpc_reviews"`
	DOMReviews        int `json:"dom_reviews"`
	Blocks            int `json:"blocks"`
	IdentityRotations int `json:"identity_rotations"`
	Restarts          int `json:"restarts"`

	BudgetSeconds  float64 `json:"budget_seconds"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
}

// finalize settles the report once the place's inline reviews are counted in: `collected` is the
// distinct union. A run that read every page it was served is `done` when that union reaches the
// reported total, and stays `exhausted` when it does not. Complete means exactly that: done,
// against a count that was known.
func (r *ReviewCollection) finalize(collected int) {
	r.Collected = collected
	if r.StopReason == stopExhausted && r.ReportedKnown && collected >= r.Reported {
		r.StopReason = stopDone
		r.StopStage = ""
		r.StopDetail = ""
	}

	r.Complete = (r.StopReason == stopDone && r.ReportedKnown && collected >= r.Reported) ||
		(r.StopReason == stopNoReviews && collected == 0)
}
