package gmaps

import "net/url"

// searchActions reads reservation and action links without visiting place pages.
func searchActions(business []any) (reservations, orders []LinkSource) {
	const (
		reservationGroup = 1
		orderGroup       = 3
		bookingAction    = 21633 // Appointment bookings share group 3 with ordering actions.
	)

	for _, value := range getNthElementAndCast[[]any](business, 46) {
		item, ok := value.([]any)
		if !ok {
			continue
		}

		reservations = appendSearchLink(reservations,
			getNthElementAndCast[string](item, 0), getNthElementAndCast[string](item, 1))
	}

	for _, value := range getNthElementAndCast[[]any](business, 75, 0) {
		group, ok := value.([]any)
		if !ok {
			continue
		}

		kind := getNthElementAndCast[float64](group, 0)
		if kind != reservationGroup && kind != orderGroup {
			continue
		}

		target := &orders
		if kind == reservationGroup || getNthElementAndCast[float64](group, 4) == bookingAction {
			target = &reservations
		}

		for _, value := range getNthElementAndCast[[]any](group, 2) {
			item, ok := value.([]any)
			if !ok {
				continue
			}

			*target = appendSearchLink(*target,
				getNthElementAndCast[string](item, 1, 2, 0), getNthElementAndCast[string](item, 0, 0))
		}

		// Some actions carry a single button URL instead of a provider list.
		*target = appendSearchLink(*target, getNthElementAndCast[string](group, 5, 1, 2, 0), "")
	}

	return reservations, orders
}

func appendSearchLink(links []LinkSource, rawURL, source string) []LinkSource {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" {
		return links
	}

	for _, link := range links {
		if link.Link == rawURL {
			return links
		}
	}

	if source == "" {
		source = parsed.Hostname()
	}

	return append(links, LinkSource{Link: rawURL, Source: source})
}
