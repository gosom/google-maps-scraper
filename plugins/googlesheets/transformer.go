//go:build plugin
// +build plugin

package main

import (
	"encoding/json"
	"strings"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func getDefaultSchema() []string {
	return []string{
		"input_id", "link", "title", "category", "address", "open_hours", "popular_times", "website", "phone", "plus_code",
		"review_count", "review_rating", "reviews_per_rating", "latitude", "longitude", "cid", "status", "descriptions",
		"reviews_link", "thumbnail", "timezone", "price_range", "data_id", "images", "reservations", "order_online", "menu",
		"owner", "complete_address", "about", "user_reviews", "user_reviews_extended", "emails",
	}
}

func transformEntry(entry *gmaps.Entry) map[string]interface{} {
	transformed := make(map[string]interface{})

	// Direct mappings
	transformed["input_id"] = entry.ID
	transformed["link"] = entry.Link
	transformed["title"] = entry.Title
	transformed["category"] = entry.Category
	transformed["address"] = entry.Address
	transformed["plus_code"] = entry.PlusCode
	transformed["review_count"] = entry.ReviewCount
	transformed["review_rating"] = entry.ReviewRating
	transformed["latitude"] = entry.Latitude
	transformed["cid"] = entry.Cid
	transformed["status"] = entry.Status
	transformed["reviews_link"] = entry.ReviewsLink
	transformed["thumbnail"] = entry.Thumbnail
	transformed["timezone"] = entry.Timezone
	transformed["price_range"] = entry.PriceRange
	transformed["data_id"] = entry.DataID

	// Field mappings as per Google Apps Script requirements
	transformed["website"] = entry.WebSite        // website ← web_site
	transformed["longitude"] = entry.Longtitude   // longitude ← longtitude (typo in source)
	transformed["descriptions"] = entry.Description // descriptions ← description

	// Phone: Remove leading '+' if present
	phone := entry.Phone
	if strings.HasPrefix(phone, "+") {
		phone = phone[1:]
	}
	transformed["phone"] = phone

	// Convert complex objects to JSON strings
	if entry.OpenHours != nil && len(entry.OpenHours) > 0 {
		transformed["open_hours"] = toJSONString(entry.OpenHours)
	} else {
		transformed["open_hours"] = ""
	}

	if entry.PopularTimes != nil && len(entry.PopularTimes) > 0 {
		transformed["popular_times"] = toJSONString(entry.PopularTimes)
	} else {
		transformed["popular_times"] = ""
	}

	if entry.ReviewsPerRating != nil && len(entry.ReviewsPerRating) > 0 {
		transformed["reviews_per_rating"] = toJSONString(entry.ReviewsPerRating)
	} else {
		transformed["reviews_per_rating"] = ""
	}

	if entry.Images != nil && len(entry.Images) > 0 {
		transformed["images"] = toJSONString(entry.Images)
	} else {
		transformed["images"] = ""
	}

	if entry.Reservations != nil && len(entry.Reservations) > 0 {
		transformed["reservations"] = toJSONString(entry.Reservations)
	} else {
		transformed["reservations"] = ""
	}

	if entry.OrderOnline != nil && len(entry.OrderOnline) > 0 {
		transformed["order_online"] = toJSONString(entry.OrderOnline)
	} else {
		transformed["order_online"] = ""
	}

	// Menu
	if entry.Menu.Link != "" || entry.Menu.Source != "" {
		transformed["menu"] = toJSONString(entry.Menu)
	} else {
		transformed["menu"] = ""
	}

	// Owner
	if entry.Owner.ID != "" || entry.Owner.Name != "" || entry.Owner.Link != "" {
		transformed["owner"] = toJSONString(entry.Owner)
	} else {
		transformed["owner"] = ""
	}

	// Complete Address
	if entry.CompleteAddress.Borough != "" || entry.CompleteAddress.Street != "" || 
	   entry.CompleteAddress.City != "" || entry.CompleteAddress.PostalCode != "" ||
	   entry.CompleteAddress.State != "" || entry.CompleteAddress.Country != "" {
		transformed["complete_address"] = toJSONString(entry.CompleteAddress)
	} else {
		transformed["complete_address"] = ""
	}

	// About
	if entry.About != nil && len(entry.About) > 0 {
		transformed["about"] = toJSONString(entry.About)
	} else {
		transformed["about"] = ""
	}

	// Reviews
	if entry.UserReviews != nil && len(entry.UserReviews) > 0 {
		transformed["user_reviews"] = toJSONString(entry.UserReviews)
	} else {
		transformed["user_reviews"] = ""
	}

	if entry.UserReviewsExtended != nil && len(entry.UserReviewsExtended) > 0 {
		transformed["user_reviews_extended"] = toJSONString(entry.UserReviewsExtended)
	} else {
		transformed["user_reviews_extended"] = ""
	}

	// Emails
	if entry.Emails != nil && len(entry.Emails) > 0 {
		transformed["emails"] = toJSONString(entry.Emails)
	} else {
		transformed["emails"] = ""
	}

	return transformed
}

func toJSONString(v interface{}) string {
	if v == nil {
		return ""
	}
	
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	
	return string(data)
}