package gmaps

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	olc "github.com/google/open-location-code/go"
)

func ParseSearchResults(raw []byte) ([]*Entry, error) {
	var data []any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("empty JSON data")
	}

	container, ok := data[0].([]any)
	if !ok || len(container) == 0 {
		return nil, fmt.Errorf("invalid business list structure")
	}

	if len(container) < 2 {
		return nil, fmt.Errorf("missing business list")
	}

	items, ok := container[1].([]any)
	if !ok && container[1] != nil {
		return nil, fmt.Errorf("invalid business list")
	}

	if len(items) < 2 {
		return []*Entry{}, nil
	}

	entries := make([]*Entry, 0, len(items)-1)

	for i := 1; i < len(items); i++ {
		arr, ok := items[i].([]any)
		if !ok {
			continue
		}

		business := getNthElementAndCast[[]any](arr, 14)

		var entry Entry

		entry.ID = getNthElementAndCast[string](business, 0)
		entry.Title = getNthElementAndCast[string](business, 11)

		entry.Categories = toStringSlice(getNthElementAndCast[[]any](business, 13))
		if len(entry.Categories) > 0 {
			entry.Category = entry.Categories[0]
		}

		entry.WebSite = getNthElementAndCast[string](business, 7, 0)

		entry.ReviewRating = getNthElementAndCast[float64](business, 4, 7)
		entry.ReviewCount = int(getNthElementAndCast[float64](business, 4, 8))

		fullAddress := getNthElementAndCast[[]any](business, 2)

		entry.Address = func() string {
			sb := strings.Builder{}

			for i, part := range fullAddress {
				if i > 0 {
					sb.WriteString(", ")
				}

				fmt.Fprintf(&sb, "%v", part)
			}

			return sb.String()
		}()

		entry.Latitude = getNthElementAndCast[float64](business, 9, 2)
		entry.Longtitude = getNthElementAndCast[float64](business, 9, 3)
		entry.Phone = strings.ReplaceAll(getNthElementAndCast[string](business, 178, 0, 0), " ", "")
		entry.OpenHours = getHours(business)
		entry.CompleteAddress = completeAddressFromPlaceArray(business)
		entry.Status = getNthElementAndCast[string](business, 203, 1, 4, 0)
		entry.Timezone = getNthElementAndCast[string](business, 30)
		entry.DataID = getNthElementAndCast[string](business, 10)
		entry.PlaceID = getNthElementAndCast[string](business, 78)

		entry.Cid = cidFromDataID(entry.DataID)
		if entry.PlaceID != "" {
			query := url.Values{
				"api":            {"1"},
				"query":          {entry.Title},
				"query_place_id": {entry.PlaceID},
			}
			entry.Link = "https://www.google.com/maps/search/?" + query.Encode()
		} else if entry.Cid != "" {
			entry.Link = "https://www.google.com/maps?cid=" + entry.Cid
		}

		entry.ReviewsLink = getNthElementAndCast[string](business, 4, 3, 0)
		entry.Description = getNthElementAndCast[string](business, 32, 1, 1)
		entry.About, entry.CreditCardsAccepted = aboutFromPlaceArray(business)
		entry.Reservations, entry.OrderOnline = searchActions(business)

		entry.PlusCode = olc.Encode(entry.Latitude, entry.Longtitude, 10)

		entries = append(entries, &entry)
	}

	return entries, nil
}

func toStringSlice(arr []any) []string {
	ans := make([]string, 0, len(arr))
	for _, v := range arr {
		ans = append(ans, fmt.Sprintf("%v", v))
	}

	return ans
}

// cidFromDataID converts the unsigned hexadecimal feature ID to a decimal CID.
func cidFromDataID(dataID string) string {
	location, feature, ok := strings.Cut(dataID, ":")
	if !ok || !strings.HasPrefix(location, "0x") || !strings.HasPrefix(feature, "0x") {
		return ""
	}

	if _, err := strconv.ParseUint(strings.TrimPrefix(location, "0x"), 16, 64); err != nil {
		return ""
	}

	cid, err := strconv.ParseUint(strings.TrimPrefix(feature, "0x"), 16, 64)
	if err != nil {
		return ""
	}

	return strconv.FormatUint(cid, 10)
}
