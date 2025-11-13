package gmaps

import (
	"encoding/json"
	"fmt"
	"iter"
	"math"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Image struct {
	Title string `json:"title"`
	Image string `json:"image"`
}

// Enhanced image struct for multiple images per category
type BusinessImage struct {
	URL          string          `json:"url"`
	ThumbnailURL string          `json:"thumbnail_url,omitempty"`
	AltText      string          `json:"alt_text"`
	Category     string          `json:"category"` // "business", "menu", "user", "street"
	Index        int             `json:"index"`
	Dimensions   ImageDimensions `json:"dimensions,omitempty"`
	Attribution  string          `json:"attribution,omitempty"`
}

// ImageDimensions holds width and height information
type ImageDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ScrapingMetadata contains metadata about the scraping process
type ScrapingMetadata struct {
	ScrapedAt     time.Time `json:"scraped_at"`
	ImageCount    int       `json:"image_count"`
	LoadTime      int       `json:"load_time_ms"`
	ScrollActions int       `json:"scroll_actions"`
}

// ImageCategory represents a category with multiple images
type ImageCategory struct {
	Title  string          `json:"title"`
	Images []BusinessImage `json:"images"`
}

type LinkSource struct {
	Link   string `json:"link"`
	Source string `json:"source"`
}

type Owner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"`
}

type Address struct {
	Borough    string `json:"borough"`
	Street     string `json:"street"`
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
	State      string `json:"state"`
	Country    string `json:"country"`
}

type Option struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type About struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Options []Option `json:"options"`
}

type Review struct {
	Name           string
	ProfilePicture string
	Rating         int
	Description    string
	Images         []string
	When           string
}

type Entry struct {
	ID         string              `json:"input_id"`
	Link       string              `json:"link"`
	Cid        string              `json:"cid"`
	Title      string              `json:"title"`
	Categories []string            `json:"categories"`
	Category   string              `json:"category"`
	Address    string              `json:"address"`
	OpenHours  map[string][]string `json:"open_hours"`
	// PopularTImes is a map with keys the days of the week
	// and value is a map with key the hour and value the traffic in that time
	PopularTimes            map[string]map[int]int `json:"popular_times"`
	WebSite                 string                 `json:"web_site"`
	Phone                   string                 `json:"phone"`
	PlusCode                string                 `json:"plus_code"`
	ReviewCount             int                    `json:"review_count"`
	ReviewRating            float64                `json:"review_rating"`
	ReviewsPerRating        map[int]int            `json:"reviews_per_rating"`
	Latitude                float64                `json:"latitude"`
	Longtitude              float64                `json:"longtitude"`
	Status                  string                 `json:"status"`
	Description             string                 `json:"description"`
	ReviewsLink             string                 `json:"reviews_link"`
	Thumbnail               string                 `json:"thumbnail"`
	Timezone                string                 `json:"timezone"`
	PriceRange              string                 `json:"price_range"`
	DataID                  string                 `json:"data_id"`
	Images                  []Image                `json:"images"`
	ImageCategories         []ImageCategory        `json:"image_categories,omitempty"` // New: Multiple images per category
	EnhancedImages          []BusinessImage        `json:"enhanced_images,omitempty"`  // New: Browser-extracted images with metadata
	ImageExtractionMetadata *ScrapingMetadata      `json:"image_metadata,omitempty"`   // New: Extraction metadata
	Reservations            []LinkSource           `json:"reservations"`
	OrderOnline             []LinkSource           `json:"order_online"`
	Menu                    LinkSource             `json:"menu"`
	Owner                   Owner                  `json:"owner"`
	CompleteAddress         Address                `json:"complete_address"`
	About                   []About                `json:"about"`
	UserReviews             []Review               `json:"user_reviews"`
	UserReviewsExtended     []Review               `json:"user_reviews_extended"`
	Emails                  []string               `json:"emails"`
}

func (e *Entry) haversineDistance(lat, lon float64) float64 {
	const R = 6371e3 // earth radius in meters

	clat := lat * math.Pi / 180
	clon := lon * math.Pi / 180

	elat := e.Latitude * math.Pi / 180
	elon := e.Longtitude * math.Pi / 180

	dlat := elat - clat
	dlon := elon - clon

	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(clat)*math.Cos(elat)*
			math.Sin(dlon/2)*math.Sin(dlon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}

func (e *Entry) isWithinRadius(lat, lon, radius float64) bool {
	distance := e.haversineDistance(lat, lon)

	return distance <= radius
}

func (e *Entry) IsWebsiteValidForEmail() bool {
	if e.WebSite == "" {
		return false
	}

	needles := []string{
		"facebook",
		"instragram",
		"twitter",
	}

	for i := range needles {
		if strings.Contains(e.WebSite, needles[i]) {
			return false
		}
	}

	return true
}

func (e *Entry) Validate() error {
	if e.Title == "" {
		return fmt.Errorf("title is empty")
	}

	if e.Category == "" {
		return fmt.Errorf("category is empty")
	}

	return nil
}

func (e *Entry) CsvHeaders() []string {
	return []string{
		"input_id",
		"link",
		"title",
		"category",
		"address",
		"open_hours",
		"popular_times",
		"website",
		"phone",
		"plus_code",
		"review_count",
		"review_rating",
		"reviews_per_rating",
		"latitude",
		"longitude",
		"cid",
		"status",
		"descriptions",
		"reviews_link",
		"thumbnail",
		"timezone",
		"price_range",
		"data_id",
		"images",
		"reservations",
		"order_online",
		"menu",
		"owner",
		"complete_address",
		"about",
		"user_reviews",
		"user_reviews_extended",
		"emails",
	}
}

func (e *Entry) CsvRow() []string {
	// DEBUG: Log final image count being written to CSV
	if len(e.Images) > 0 {
		fmt.Printf("DEBUG: Writing %d images to CSV for business: %s\n", len(e.Images), e.Title)
		if len(e.EnhancedImages) > 0 {
			fmt.Printf("DEBUG: Enhanced images available: %d, Image extraction metadata available: %v\n", len(e.EnhancedImages), e.ImageExtractionMetadata != nil)
		}
	}

	return []string{
		e.ID,
		e.Link,
		e.Title,
		e.Category,
		e.Address,
		stringify(e.OpenHours),
		stringify(e.PopularTimes),
		e.WebSite,
		e.Phone,
		e.PlusCode,
		stringify(e.ReviewCount),
		stringify(e.ReviewRating),
		stringify(e.ReviewsPerRating),
		stringify(e.Latitude),
		stringify(e.Longtitude),
		e.Cid,
		e.Status,
		e.Description,
		e.ReviewsLink,
		e.Thumbnail,
		e.Timezone,
		e.PriceRange,
		e.DataID,
		stringify(e.Images),
		stringify(e.Reservations),
		stringify(e.OrderOnline),
		stringify(e.Menu),
		stringify(e.Owner),
		stringify(e.CompleteAddress),
		stringify(e.About),
		stringify(e.UserReviews),
		stringify(e.UserReviewsExtended),
		stringSliceToString(e.Emails),
	}
}

func (e *Entry) AddExtraReviews(pages [][]byte) {
	if len(pages) == 0 {
		return
	}

	for _, page := range pages {
		reviews := extractReviews(page)
		if len(reviews) > 0 {
			e.UserReviewsExtended = append(e.UserReviewsExtended, reviews...)
		}
	}
}

func extractReviews(data []byte) []Review {
	if len(data) >= 4 && string(data[0:4]) == `)]}'` {
		data = data[4:] // Skip security prefix
	}

	var jd []any
	if err := json.Unmarshal(data, &jd); err != nil {
		fmt.Printf("Error unmarshalling JSON: %v\n", err)
		return nil
	}

	reviewsI := getNthElementAndCast[[]any](jd, 2)

	return parseReviews(reviewsI)
}

// extractMultipleImages extracts all images per category from the data array
func extractMultipleImages(darray []any) []ImageCategory {
	// Try to get all images from various indices where Google Maps might store them
	var allCategories []ImageCategory

	// Try the standard image location (171)
	if categories := extractImagesFromIndex(darray, 171); len(categories) > 0 {
		allCategories = append(allCategories, categories...)
	}

	// Try alternative indices where images might be stored
	alternativeIndices := []int{170, 172, 173, 174}
	for _, idx := range alternativeIndices {
		if categories := extractImagesFromIndex(darray, idx); len(categories) > 0 {
			allCategories = append(allCategories, categories...)
		}
	}

	return allCategories
}

// extractImagesFromIndex extracts images from a specific data array index
func extractImagesFromIndex(darray []any, index int) []ImageCategory {
	var categories []ImageCategory

	// Get the main image data array
	imageData := getNthElementAndCast[[]any](darray, index, 0)
	if len(imageData) == 0 {
		return categories
	}

	// Process each category
	for categoryIndex, categoryData := range imageData {
		categoryArray, ok := categoryData.([]any)
		if !ok {
			continue
		}

		// Extract category title
		categoryTitle := ""
		if len(categoryArray) > 2 {
			if title, titleOk := categoryArray[2].(string); titleOk {
				categoryTitle = title
			}
		}

		if categoryTitle == "" {
			categoryTitle = fmt.Sprintf("Category %d", categoryIndex+1)
		}

		// Extract all images in this category
		var categoryImages []BusinessImage

		// Try to find image arrays within the category
		for elementIndex := 0; elementIndex < len(categoryArray); elementIndex++ {
			if element, elemOk := categoryArray[elementIndex].([]any); elemOk {
				// Look for nested image data
				if images := extractImagesFromElement(element); len(images) > 0 {
					for imgIndex, img := range images {
						categoryImages = append(categoryImages, BusinessImage{
							URL:      img,
							Category: categoryTitle,
							Index:    imgIndex,
							AltText:  fmt.Sprintf("%s image %d", categoryTitle, imgIndex+1),
						})
					}
				}
			}
		}

		// If we found images, add this category
		if len(categoryImages) > 0 {
			categories = append(categories, ImageCategory{
				Title:  categoryTitle,
				Images: categoryImages,
			})
		}
	}

	return categories
}

// extractImagesFromElement extracts image URLs from a data element
func extractImagesFromElement(element []any) []string {
	var images []string

	// Try different patterns for image URL extraction
	for i := 0; i < len(element); i++ {
		// Pattern 1: Direct URL at various indices
		if url := getNthElementAndCast[string](element, i); url != "" && isValidImageURL(url) {
			images = append(images, url)
		}

		// Pattern 2: Nested array with URLs
		if nested, ok := element[i].([]any); ok && len(nested) > 0 {
			for j := 0; j < len(nested); j++ {
				if nestedUrl := getNthElementAndCast[string](nested, j); nestedUrl != "" && isValidImageURL(nestedUrl) {
					images = append(images, nestedUrl)
				}

				// Pattern 3: Deep nested URLs (common pattern: [3][0][6][0])
				if deepUrl := getNthElementAndCast[string](nested, 3, 0, 6, 0); deepUrl != "" && isValidImageURL(deepUrl) {
					images = append(images, deepUrl)
				}
			}
		}
	}

	return images
}

// isValidImageURL checks if a URL looks like a valid Google image URL
func isValidImageURL(url string) bool {
	return strings.Contains(url, "googleusercontent.com") || strings.Contains(url, "gstatic.com")
}

//nolint:gomnd // it's ok, I need the indexes
func EntryFromJSON(raw []byte, reviewCountOnly ...bool) (entry Entry, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic: %v stack: %s", r, debug.Stack())

			return
		}
	}()

	onlyReviewCount := false

	if len(reviewCountOnly) == 1 && reviewCountOnly[0] {
		onlyReviewCount = true
	}

	var jd []any
	if err := json.Unmarshal(raw, &jd); err != nil {
		return entry, err
	}

	if len(jd) < 7 {
		return entry, fmt.Errorf("invalid json")
	}

	darray, ok := jd[6].([]any)
	if !ok {
		return entry, fmt.Errorf("invalid json")
	}

	entry.ReviewCount = int(getNthElementAndCast[float64](darray, 4, 8))

	if onlyReviewCount {
		return entry, nil
	}

	entry.Link = getNthElementAndCast[string](darray, 27)
	entry.Title = getNthElementAndCast[string](darray, 11)

	categoriesI := getNthElementAndCast[[]any](darray, 13)

	entry.Categories = make([]string, len(categoriesI))
	for i := range categoriesI {
		entry.Categories[i], _ = categoriesI[i].(string)
	}

	if len(entry.Categories) > 0 {
		entry.Category = entry.Categories[0]
	}

	entry.Address = strings.TrimSpace(
		strings.TrimPrefix(getNthElementAndCast[string](darray, 18), entry.Title+","),
	)
	entry.OpenHours = getHours(darray)
	entry.PopularTimes = getPopularTimes(darray)
	entry.WebSite = cleanGoogleRedirectURL(getNthElementAndCast[string](darray, 7, 0))
	entry.Phone = getNthElementAndCast[string](darray, 178, 0, 0)
	entry.PlusCode = getNthElementAndCast[string](darray, 183, 2, 2, 0)
	entry.ReviewRating = getNthElementAndCast[float64](darray, 4, 7)
	entry.Latitude = getNthElementAndCast[float64](darray, 9, 2)
	entry.Longtitude = getNthElementAndCast[float64](darray, 9, 3)
	entry.Cid = getNthElementAndCast[string](jd, 25, 3, 0, 13, 0, 0, 1)
	entry.Status = getNthElementAndCast[string](darray, 34, 4, 4)
	entry.Description = getNthElementAndCast[string](darray, 32, 1, 1)
	entry.ReviewsLink = getNthElementAndCast[string](darray, 4, 3, 0)
	entry.Thumbnail = getNthElementAndCast[string](darray, 72, 0, 1, 6, 0)
	entry.Timezone = getNthElementAndCast[string](darray, 30)
	entry.PriceRange = getNthElementAndCast[string](darray, 4, 2)
	entry.DataID = getNthElementAndCast[string](darray, 10)

	items := getLinkSource(getLinkSourceParams{
		arr:    getNthElementAndCast[[]any](darray, 171, 0),
		link:   []int{3, 0, 6, 0},
		source: []int{2},
	})

	entry.Images = make([]Image, len(items))

	for i := range items {
		entry.Images[i] = Image{
			Title: items[i].Source,
			Image: items[i].Link,
		}
	}

	// NEW: Extract multiple images per category
	entry.ImageCategories = extractMultipleImages(darray)

	entry.Reservations = getLinkSource(getLinkSourceParams{
		arr:    getNthElementAndCast[[]any](darray, 46),
		link:   []int{0},
		source: []int{1},
	})

	orderOnlineI := getNthElementAndCast[[]any](darray, 75, 0, 1, 2)

	if len(orderOnlineI) == 0 {
		orderOnlineI = getNthElementAndCast[[]any](darray, 75, 0, 0, 2)
	}

	entry.OrderOnline = getLinkSource(getLinkSourceParams{
		arr:    orderOnlineI,
		link:   []int{1, 2, 0},
		source: []int{0, 0},
	})

	entry.Menu = LinkSource{
		Link:   getNthElementAndCast[string](darray, 38, 0),
		Source: getNthElementAndCast[string](darray, 38, 1),
	}

	entry.Owner = Owner{
		ID:   getNthElementAndCast[string](darray, 57, 2),
		Name: getNthElementAndCast[string](darray, 57, 1),
	}

	if entry.Owner.ID != "" {
		entry.Owner.Link = fmt.Sprintf("https://www.google.com/maps/contrib/%s", entry.Owner.ID)
	}

	entry.CompleteAddress = Address{
		Borough:    getNthElementAndCast[string](darray, 183, 1, 0),
		Street:     getNthElementAndCast[string](darray, 183, 1, 1),
		City:       getNthElementAndCast[string](darray, 183, 1, 3),
		PostalCode: getNthElementAndCast[string](darray, 183, 1, 4),
		State:      getNthElementAndCast[string](darray, 183, 1, 5),
		Country:    getNthElementAndCast[string](darray, 183, 1, 6),
	}

	aboutI := getNthElementAndCast[[]any](darray, 100, 1)

	for i := range aboutI {
		el := getNthElementAndCast[[]any](aboutI, i)
		about := About{
			ID:   getNthElementAndCast[string](el, 0),
			Name: getNthElementAndCast[string](el, 1),
		}

		optsI := getNthElementAndCast[[]any](el, 2)

		for j := range optsI {
			opt := Option{
				Enabled: (getNthElementAndCast[float64](optsI, j, 2, 1, 0, 0)) == 1,
				Name:    getNthElementAndCast[string](optsI, j, 1),
			}

			if opt.Name != "" {
				about.Options = append(about.Options, opt)
			}
		}

		entry.About = append(entry.About, about)
	}

	entry.ReviewsPerRating = map[int]int{
		1: int(getNthElementAndCast[float64](darray, 175, 3, 0)),
		2: int(getNthElementAndCast[float64](darray, 175, 3, 1)),
		3: int(getNthElementAndCast[float64](darray, 175, 3, 2)),
		4: int(getNthElementAndCast[float64](darray, 175, 3, 3)),
		5: int(getNthElementAndCast[float64](darray, 175, 3, 4)),
	}

	reviewsI := getNthElementAndCast[[]any](darray, 175, 9, 0, 0)
	entry.UserReviews = make([]Review, 0, len(reviewsI))

	return entry, nil
}

func parseReviews(reviewsI []any) []Review {
	ans := make([]Review, 0, len(reviewsI))

	for i := range reviewsI {
		el := getNthElementAndCast[[]any](reviewsI, i, 0)

		time := getNthElementAndCast[[]any](el, 2, 2, 0, 1, 21, 6, 8)

		profilePic, err := decodeURL(getNthElementAndCast[string](el, 1, 4, 5, 1))
		if err != nil {
			profilePic = ""
		}

		review := Review{
			Name:           getNthElementAndCast[string](el, 1, 4, 5, 0),
			ProfilePicture: profilePic,
			When: func() string {
				if len(time) < 3 {
					return ""
				}

				return fmt.Sprintf("%v-%v-%v", time[0], time[1], time[2])
			}(),
			Rating:      int(getNthElementAndCast[float64](el, 2, 0, 0)),
			Description: getNthElementAndCast[string](el, 2, 15, 0, 0),
		}

		if review.Name == "" {
			continue
		}

		optsI := getNthElementAndCast[[]any](el, 2, 2, 0, 1, 21, 7)

		for j := range optsI {
			val := getNthElementAndCast[string](optsI, j)
			if val != "" {
				review.Images = append(review.Images, val[2:])
			}
		}

		ans = append(ans, review)
	}

	return ans
}

type getLinkSourceParams struct {
	arr    []any
	source []int
	link   []int
}

func getLinkSource(params getLinkSourceParams) []LinkSource {
	var result []LinkSource

	for i := range params.arr {
		item := getNthElementAndCast[[]any](params.arr, i)

		el := LinkSource{
			Source: getNthElementAndCast[string](item, params.source...),
			Link:   getNthElementAndCast[string](item, params.link...),
		}
		if el.Link != "" && el.Source != "" {
			result = append(result, el)
		}
	}

	return result
}

//nolint:gomnd // it's ok, I need the indexes
func getHours(darray []any) map[string][]string {
	// Try new structure first (as of Nov 2025) - darray[203][0]
	items := getNthElementAndCast[[]any](darray, 203, 0)
	if len(items) == 0 {
		// Fall back to old structure - darray[34][1]
		items = getNthElementAndCast[[]any](darray, 34, 1)
	}

	hours := make(map[string][]string, len(items))

	for _, item := range items {
		itemArray, ok := item.([]any)
		if !ok {
			continue
		}

		// New structure: [0] = day name, [3] = time slots array
		day := getNthElementAndCast[string](itemArray, 0)
		if day == "" {
			continue
		}

		// Try new structure for times
		timeSlotsI := getNthElementAndCast[[]any](itemArray, 3)
		if len(timeSlotsI) > 0 {
			// New format: each slot is [formatted_string, [[hour, min], [hour, min]]]
			times := make([]string, 0, len(timeSlotsI))
			for _, slot := range timeSlotsI {
				slotArray, ok := slot.([]any)
				if !ok || len(slotArray) == 0 {
					continue
				}
				// Get the formatted time string (e.g., "11 am–1:30 pm")
				timeStr := getNthElementAndCast[string](slotArray, 0)
				if timeStr != "" {
					times = append(times, timeStr)
				}
			}
			if len(times) > 0 {
				hours[day] = times
			}
		} else {
			// Fall back to old structure: [1] = times array
			timesI := getNthElementAndCast[[]any](itemArray, 1)
			times := make([]string, 0, len(timesI))
			for i := range timesI {
				if timeStr, ok := timesI[i].(string); ok {
					times = append(times, timeStr)
				}
			}
			if len(times) > 0 {
				hours[day] = times
			}
		}
	}

	return hours
}

func getPopularTimes(darray []any) map[string]map[int]int {
	items := getNthElementAndCast[[]any](darray, 84, 0) //nolint:gomnd // it's ok, I need the indexes
	popularTimes := make(map[string]map[int]int, len(items))

	dayOfWeek := map[int]string{
		1: "Monday",
		2: "Tuesday",
		3: "Wednesday",
		4: "Thursday",
		5: "Friday",
		6: "Saturday",
		7: "Sunday",
	}

	for ii := range items {
		item, ok := items[ii].([]any)
		if !ok {
			return nil
		}

		day := int(getNthElementAndCast[float64](item, 0))

		timesI := getNthElementAndCast[[]any](item, 1)

		times := make(map[int]int, len(timesI))

		for i := range timesI {
			t, ok := timesI[i].([]any)
			if !ok {
				return nil
			}

			v, ok := t[1].(float64)
			if !ok {
				return nil
			}

			h, ok := t[0].(float64)
			if !ok {
				return nil
			}

			times[int(h)] = int(v)
		}

		popularTimes[dayOfWeek[day]] = times
	}

	return popularTimes
}

func getNthElementAndCast[T any](arr []any, indexes ...int) T {
	var (
		defaultVal T
		idx        int
	)

	if len(indexes) == 0 {
		return defaultVal
	}

	for len(indexes) > 1 {
		idx, indexes = indexes[0], indexes[1:]

		if idx >= len(arr) {
			return defaultVal
		}

		next := arr[idx]

		if next == nil {
			return defaultVal
		}

		var ok bool

		arr, ok = next.([]any)
		if !ok {
			return defaultVal
		}
	}

	if len(indexes) == 0 || len(arr) == 0 {
		return defaultVal
	}

	ans, ok := arr[indexes[0]].(T)
	if !ok {
		return defaultVal
	}

	return ans
}

func stringSliceToString(s []string) string {
	return strings.Join(s, ", ")
}

func stringify(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%f", val)
	case nil:
		return ""
	default:
		d, _ := json.Marshal(v)
		return string(d)
	}
}

func decodeURL(urlStr string) (string, error) {
	quoted := `"` + strings.ReplaceAll(urlStr, `"`, `\"`) + `"`

	unquoted, err := strconv.Unquote(quoted)
	if err != nil {
		return "", fmt.Errorf("failed to decode URL: %v", err)
	}

	return unquoted, nil
}

// cleanGoogleRedirectURL extracts the actual URL from Google redirect URLs
// Example: /url?q=http://example.com&opi=123 → http://example.com
func cleanGoogleRedirectURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	// Check if it's a Google redirect URL
	if !strings.HasPrefix(rawURL, "/url?") && !strings.HasPrefix(rawURL, "https://www.google.com/url?") {
		// Not a redirect URL, return as-is
		return rawURL
	}

	// Extract the 'q=' parameter which contains the actual URL
	if idx := strings.Index(rawURL, "q="); idx != -1 {
		// Find the start of the URL after 'q='
		urlStart := idx + 2
		urlPart := rawURL[urlStart:]

		// Find the end of the URL (next '&' or end of string)
		endIdx := strings.Index(urlPart, "&")
		if endIdx != -1 {
			urlPart = urlPart[:endIdx]
		}

		// URL decode the extracted URL
		decodedURL, err := url.QueryUnescape(urlPart)
		if err != nil {
			// If decoding fails, return the URL part as-is
			return urlPart
		}

		return decodedURL
	}

	// Couldn't find 'q=' parameter, return original
	return rawURL
}

type EntryWithDistance struct {
	Entry    *Entry
	Distance float64
}

func filterAndSortEntriesWithinRadius(entries []*Entry, lat, lon, radius float64) []*Entry {
	withinRadiusIterator := func(yield func(EntryWithDistance) bool) {
		for _, entry := range entries {
			distance := entry.haversineDistance(lat, lon)
			if distance <= radius {
				if !yield(EntryWithDistance{Entry: entry, Distance: distance}) {
					return
				}
			}
		}
	}

	entriesWithDistance := slices.Collect(iter.Seq[EntryWithDistance](withinRadiusIterator))

	slices.SortFunc(entriesWithDistance, func(a, b EntryWithDistance) int {
		switch {
		case a.Distance < b.Distance:
			return -1
		case a.Distance > b.Distance:
			return 1
		default:
			return 0
		}
	})

	resultIterator := func(yield func(*Entry) bool) {
		for _, e := range entriesWithDistance {
			if !yield(e.Entry) {
				return
			}
		}
	}

	return slices.Collect(iter.Seq[*Entry](resultIterator))
}
