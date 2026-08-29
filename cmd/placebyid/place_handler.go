package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// buildPlaceURL converts a place ID to the right Google Maps URL.
// Handles three formats:
//   - ChIJ...      → place_id: navigation
//   - 0xHEX:0xHEX  → CID decimal navigation (DataID format from SearchJob)
//   - anything else → place_id: navigation (fallback)
func buildPlaceURL(placeID string) string {
	if strings.HasPrefix(placeID, "0x") && strings.Contains(placeID, ":") {
		parts := strings.SplitN(placeID, ":", 2)
		hexCID := strings.TrimPrefix(parts[1], "0x")
		if cid, err := strconv.ParseUint(hexCID, 16, 64); err == nil {
			return fmt.Sprintf("https://maps.google.com/?cid=%d", cid)
		}
	}
	return fmt.Sprintf("https://www.google.com/maps/place/?q=%s",
		url.QueryEscape("place_id:"+placeID))
}

// ====================================================================
// GET /v1/places/{placeId} handler
// ====================================================================

func placeHandler(eng *httpEngine, langCode string, extractEmail, extraReviews bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		placeID := r.PathValue("placeId")
		if placeID == "" {
			http.Error(w, "missing placeId", http.StatusBadRequest)
			return
		}

		// Propagate the request context directly — NestJS enforces the 180s deadline.
		gp, err := eng.scrapePlace(r.Context(), placeID, langCode, extractEmail, extraReviews)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			if r.Context().Err() != nil || err == context.DeadlineExceeded || err == context.Canceled {
				// Caller's deadline fired before we finished all retries.
				w.WriteHeader(http.StatusGatewayTimeout)
				fmt.Fprintf(w, `{"error":"scrape timeout","placeId":%q}`, placeID)
			} else {
				// All retries exhausted within our budget — likely bot-detection or bad place ID.
				w.WriteHeader(http.StatusUnprocessableEntity)
				fmt.Fprintf(w, `{"error":"place_unavailable","placeId":%q}`, placeID)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gp)
	}
}
