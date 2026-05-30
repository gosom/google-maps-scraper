package jsonbsanitize

import (
	"strings"

	"github.com/gosom/google-maps-scraper/gmaps"
)

// StripNULFromEntries removes NUL bytes (\x00) from all string fields in entries.
func StripNULFromEntries(entries []*gmaps.Entry) {
	for _, entry := range entries {
		StripNULFromEntry(entry)
	}
}

// StripNULFromEntry removes NUL bytes (\x00) from all string fields in an entry.
func StripNULFromEntry(entry *gmaps.Entry) {
	if entry == nil {
		return
	}

	entry.ID = cleanString(entry.ID)
	entry.Link = cleanString(entry.Link)
	entry.Cid = cleanString(entry.Cid)
	entry.Title = cleanString(entry.Title)
	entry.Category = cleanString(entry.Category)
	entry.Address = cleanString(entry.Address)
	entry.WebSite = cleanString(entry.WebSite)
	entry.Phone = cleanString(entry.Phone)
	entry.PlusCode = cleanString(entry.PlusCode)
	entry.Status = cleanString(entry.Status)
	entry.Description = cleanString(entry.Description)
	entry.ReviewsLink = cleanString(entry.ReviewsLink)
	entry.Thumbnail = cleanString(entry.Thumbnail)
	entry.Timezone = cleanString(entry.Timezone)
	entry.PriceRange = cleanString(entry.PriceRange)
	entry.DataID = cleanString(entry.DataID)
	entry.PlaceID = cleanString(entry.PlaceID)

	cleanStringSlice(entry.Categories)
	cleanStringSlice(entry.Emails)

	entry.OpenHours = cleanOpenHours(entry.OpenHours)
	entry.PopularTimes = cleanPopularTimes(entry.PopularTimes)

	cleanImages(entry.Images)
	cleanLinkSources(entry.Reservations)
	cleanLinkSources(entry.OrderOnline)
	cleanLinkSource(&entry.Menu)
	cleanOwner(&entry.Owner)
	cleanAddress(&entry.CompleteAddress)
	cleanAbout(entry.About)
	cleanReviews(entry.UserReviews)
	cleanReviews(entry.UserReviewsExtended)
}

func cleanOpenHours(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return in
	}

	out := make(map[string][]string, len(in))

	for k, v := range in {
		key := cleanString(k)
		values := make([]string, len(v))

		copy(values, v)
		cleanStringSlice(values)
		out[key] = values
	}

	return out
}

func cleanPopularTimes(in map[string]map[int]int) map[string]map[int]int {
	if len(in) == 0 {
		return in
	}

	out := make(map[string]map[int]int, len(in))

	for k, v := range in {
		key := cleanString(k)
		inner := make(map[int]int, len(v))

		for hour, count := range v {
			inner[hour] = count
		}

		out[key] = inner
	}

	return out
}

func cleanImages(items []gmaps.Image) {
	for i := range items {
		items[i].Title = cleanString(items[i].Title)
		items[i].Image = cleanString(items[i].Image)
	}
}

func cleanLinkSources(items []gmaps.LinkSource) {
	for i := range items {
		cleanLinkSource(&items[i])
	}
}

func cleanLinkSource(item *gmaps.LinkSource) {
	if item == nil {
		return
	}

	item.Link = cleanString(item.Link)
	item.Source = cleanString(item.Source)
}

func cleanOwner(owner *gmaps.Owner) {
	if owner == nil {
		return
	}

	owner.ID = cleanString(owner.ID)
	owner.Name = cleanString(owner.Name)
	owner.Link = cleanString(owner.Link)
}

func cleanAddress(addr *gmaps.Address) {
	if addr == nil {
		return
	}

	addr.Borough = cleanString(addr.Borough)
	addr.Street = cleanString(addr.Street)
	addr.City = cleanString(addr.City)
	addr.PostalCode = cleanString(addr.PostalCode)
	addr.State = cleanString(addr.State)
	addr.Country = cleanString(addr.Country)
}

func cleanAbout(items []gmaps.About) {
	for i := range items {
		items[i].ID = cleanString(items[i].ID)
		items[i].Name = cleanString(items[i].Name)

		for j := range items[i].Options {
			items[i].Options[j].Name = cleanString(items[i].Options[j].Name)
		}
	}
}

func cleanReviews(items []gmaps.Review) {
	for i := range items {
		items[i].Name = cleanString(items[i].Name)
		items[i].ProfilePicture = cleanString(items[i].ProfilePicture)
		items[i].Description = cleanString(items[i].Description)
		cleanStringSlice(items[i].Images)
		items[i].When = cleanString(items[i].When)
	}
}

func cleanStringSlice(items []string) {
	for i := range items {
		items[i] = cleanString(items[i])
	}
}

func cleanString(s string) string {
	if strings.IndexByte(s, 0) == -1 {
		return s
	}

	return strings.ReplaceAll(s, "\x00", "")
}
