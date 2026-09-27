package web

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AxioIntel/Axio-CRED/gmaps"
)

// collectorFromForm reads the scrape form's collector choice and, for v2, its settings. The
// dashboard form is v1 unless it says v2, so a form from before the choice existed is v1.
func collectorFromForm(get func(string) string) (collector string, spec *SteadySpec, err error) {
	switch c := strings.TrimSpace(get("collector")); c {
	case "", gmaps.CollectorV1:
		return gmaps.CollectorV1, nil, nil
	case gmaps.CollectorV2:
	default:
		return "", nil, fmt.Errorf("unknown collector %q", c)
	}

	num := func(name string, def int) (int, error) {
		raw := strings.TrimSpace(get(name))
		if raw == "" {
			return def, nil
		}

		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid %s", strings.ReplaceAll(strings.TrimPrefix(name, "v2_"), "_", " "))
		}

		return n, nil
	}

	spec = &SteadySpec{}

	fields := []struct {
		name string
		def  int
		dst  *int
	}{
		{"v2_pages", 3, &spec.Pages},
		{"v2_contact_pages", 0, &spec.ContactPages},
		{"v2_delay_ms", int(gmaps.DefaultSteadyDelay.Milliseconds()), &spec.DelayMs},
		{"v2_site_timeout_sec", int(gmaps.DefaultSteadySiteTimeout.Seconds()), &spec.SiteTimeoutSec},
		{"v2_query_budget_sec", int(gmaps.DefaultSteadyQueryBudget.Seconds()), &spec.QueryBudgetSec},
	}

	for _, f := range fields {
		if *f.dst, err = num(f.name, f.def); err != nil {
			return "", nil, err
		}
	}

	retries, err := num("v2_retries", gmaps.DefaultSteadyRetries)
	if err != nil {
		return "", nil, err
	}

	spec.Retries = &retries
	spec.NoRedirects = get("v2_follow_redirects") != formOn

	return gmaps.CollectorV2, spec, nil
}
