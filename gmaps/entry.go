package gmaps

import (
	"encoding/json"
	"fmt"
	"iter"
	"math"
	"net/url"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
)

var panoidRegex = regexp.MustCompile(`panoid=([^&]+)`)

type Image struct {
	Title string `json:"title"`
	Image string `json:"image"`
	Date  string `json:"date,omitempty"`
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
	PopularTimes        map[string]map[int]int `json:"popular_times"`
	WebSite             string                 `json:"web_site"`
	Phone               string                 `json:"phone"`
	PlusCode            string                 `json:"plus_code"`
	ReviewCount         int                    `json:"review_count"`
	ReviewRating        float64                `json:"review_rating"`
	ReviewsPerRating    map[int]int            `json:"reviews_per_rating"`
	Latitude            float64                `json:"latitude"`
	Longtitude          float64                `json:"longtitude"`
	Status              string                 `json:"status"`
	Description         string                 `json:"description"`
	ReviewsLink         string                 `json:"reviews_link"`
	Thumbnail           string                 `json:"thumbnail"`
	Timezone            string                 `json:"timezone"`
	PriceRange          string                 `json:"price_range"`
	DataID              string                 `json:"data_id"`
	PhotosCount         int                    `json:"photos_count"`
	PlaceID             string                 `json:"place_id"`
	StreetViewURL       string                 `json:"street_view_url"`
	Images              []Image                `json:"images"`
	Reservations        []LinkSource           `json:"reservations"`
	OrderOnline         []LinkSource           `json:"order_online"`
	Menu                LinkSource             `json:"menu"`
	Owner               Owner                  `json:"owner"`
	CompleteAddress     Address                `json:"complete_address"`
	About               []About                `json:"about"`
	UserReviews         []Review               `json:"user_reviews"`
	UserReviewsExtended []Review               `json:"user_reviews_extended"`
	Emails              []string               `json:"emails"`
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
		"photos_count",
		"place_id",
		"street_view_url",
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
		stringify(e.PhotosCount),
		e.PlaceID,
		e.StreetViewURL,
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

// AddExtraPhotos extracts additional photo data including dates for existing images
// and individual photos from the raw JSON data.
func (e *Entry) AddExtraPhotos(raw []byte) {
	var jd []any
	if err := json.Unmarshal(raw, &jd); err != nil {
		return
	}

	if len(jd) < 7 {
		return
	}

	darray, ok := jd[6].([]any)
	if !ok {
		return
	}

	// Extract dates for existing category images from darray[171][0]
	catImages := getNthElementAndCast[[]any](darray, 171, 0)
	for i := range e.Images {
		if i < len(catImages) {
			cat := getNthElementAndCast[[]any](catImages, i)
			// Date is at cat[3][0][21][6][8]
			dateArr := getNthElementAndCast[[]any](cat, 3, 0, 21, 6, 8)
			e.Images[i].Date = formatPhotoDate(dateArr)
		}
	}

	// Extract individual photos from darray[37][0]
	photoObjects := getNthElementAndCast[[]any](darray, 37, 0)
	for i := range photoObjects {
		photo := getNthElementAndCast[[]any](photoObjects, i)
		if len(photo) == 0 {
			continue
		}

		photoID := getNthElementAndCast[string](photo, 0)
		photoURL := getNthElementAndCast[string](photo, 6, 0)
		photoLabel := getNthElementAndCast[string](photo, 20)
		dateArr := getNthElementAndCast[[]any](photo, 21, 6, 8)

		if photoURL == "" {
			continue
		}

		// Use label as title, fallback to "Photo"
		title := photoLabel
		if title == "" {
			title = fmt.Sprintf("Photo %d", i+1)
		}

				// Check if this photo is already in Images (by URL or ID)

				alreadyExists := false

		

				for _, img := range e.Images {
			if strings.Contains(img.Image, photoID) {
				alreadyExists = true
				break
			}
		}

		if !alreadyExists {
			e.Images = append(e.Images, Image{
				Title: title,
				Image: photoURL,
				Date:  formatPhotoDate(dateArr),
			})
		}
	}
}

// formatPhotoDate converts a date array [year, month, day, hour] to "YYYY-MM-DD" format.
func formatPhotoDate(dateArr []any) string {
	if len(dateArr) < 3 {
		return ""
	}

	year := int(getNthElementAndCast[float64](dateArr, 0))
	month := int(getNthElementAndCast[float64](dateArr, 1))
	day := int(getNthElementAndCast[float64](dateArr, 2))

	if year == 0 || month == 0 || day == 0 {
		return ""
	}

	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

func extractReviews(data []byte) []Review {
	// Skip the security prefix
	prefix := ")]}'\n"
	if len(data) >= len(prefix) && string(data[:len(prefix)]) == prefix {
		data = data[len(prefix):]
	} else if len(data) >= 4 && string(data[0:4]) == `)]}'` {
		data = data[4:]
	}

	var jd []any
	if err := json.Unmarshal(data, &jd); err != nil {
		fmt.Printf("Error unmarshalling RPC JSON: %v (data len: %d)\n", err, len(data))
		return nil
	}

	if len(jd) < 3 {
		return nil
	}

	reviewsI := getNthElementAndCast[[]any](jd, 2)
	if len(reviewsI) == 0 {
		// Try alternative indices - Google may have changed the structure
		reviewsI = getNthElementAndCast[[]any](jd, 0)
	}

	return parseReviews(reviewsI)
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
	entry.WebSite = extractActualURL(getNthElementAndCast[string](darray, 7, 0))
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
	entry.PhotosCount = int(getNthElementAndCast[float64](darray, 37, 1))
	entry.PlaceID = getNthElementAndCast[string](darray, 78)

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

	// Extract Street View URL from images
	entry.StreetViewURL = extractStreetViewURL(entry.Images)

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

	// Parse inline reviews from the page data
	reviewsI := getNthElementAndCast[[]any](darray, 175, 9, 0, 0)
	if len(reviewsI) > 0 {
		entry.UserReviews = parseReviews(reviewsI)
	} else {
		// Try alternative location for reviews
		reviewsI = getNthElementAndCast[[]any](darray, 175, 9, 0)
		if len(reviewsI) > 0 {
			entry.UserReviews = parseReviews(reviewsI)
		} else {
			entry.UserReviews = make([]Review, 0)
		}
	}

	return entry, nil
}

func parseReviews(reviewsI []any) []Review {
	ans := make([]Review, 0, len(reviewsI))

	for i := range reviewsI {
		el := getNthElementAndCast[[]any](reviewsI, i, 0)
		if len(el) == 0 {
			// Try alternative structure
			el = getNthElementAndCast[[]any](reviewsI, i)
			if len(el) == 0 {
				continue
			}
		}

		// Try multiple paths for the timestamp
		time := getNthElementAndCast[[]any](el, 2, 2, 0, 1, 21, 6, 8)
		if len(time) == 0 {
			time = getNthElementAndCast[[]any](el, 2, 2, 0, 1, 6, 8)
		}

		// Try multiple paths for profile picture
		profilePic, err := decodeURL(getNthElementAndCast[string](el, 1, 4, 5, 1))
		if err != nil || profilePic == "" {
			profilePic = getNthElementAndCast[string](el, 1, 2, 0)
			if profilePic == "" {
				profilePic = getNthElementAndCast[string](el, 0, 2, 0)
			}
		}

		// Try multiple paths for author name
		authorName := getNthElementAndCast[string](el, 1, 4, 5, 0)
		if authorName == "" {
			authorName = getNthElementAndCast[string](el, 1, 4, 4)
			if authorName == "" {
				authorName = getNthElementAndCast[string](el, 0, 1)
			}
		}

		// Try multiple paths for rating
		rating := int(getNthElementAndCast[float64](el, 2, 0, 0))
		if rating == 0 {
			rating = int(getNthElementAndCast[float64](el, 2, 0))
			if rating == 0 {
				rating = int(getNthElementAndCast[float64](el, 1, 0, 0))
			}
		}

		// Try multiple paths for description
		description := getNthElementAndCast[string](el, 2, 15, 0, 0)
		if description == "" {
			description = getNthElementAndCast[string](el, 2, 15, 0)
			if description == "" {
				description = getNthElementAndCast[string](el, 3, 0)
			}
		}

		review := Review{
			Name:           authorName,
			ProfilePicture: profilePic,
			When: func() string {
				if len(time) < 3 {
					return ""
				}

				return fmt.Sprintf("%v-%v-%v", time[0], time[1], time[2])
			}(),
			Rating:      rating,
			Description: description,
		}

		if review.Name == "" {
			continue
		}

		// Try multiple paths for images
		optsI := getNthElementAndCast[[]any](el, 2, 2, 0, 1, 21, 7)
		if len(optsI) == 0 {
			optsI = getNthElementAndCast[[]any](el, 2, 2, 0, 1, 7)
		}

		for j := range optsI {
			val := getNthElementAndCast[string](optsI, j)
			if val != "" && len(val) > 2 {
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

	if indexes[0] >= len(arr) {
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

// extractStreetViewURL finds the Street View image and extracts the panoid to create a proper URL
func extractStreetViewURL(images []Image) string {
	for _, img := range images {
		if strings.Contains(img.Title, "Street View") {
			matches := panoidRegex.FindStringSubmatch(img.Image)
			if len(matches) > 1 {
				return fmt.Sprintf("https://www.google.com/maps/@?api=1&map_action=pano&pano=%s", matches[1])
			}
		}
	}

	return ""
}

func decodeURL(url string) (string, error) {
	quoted := `"` + strings.ReplaceAll(url, `"`, `\"`) + `"`

	unquoted, err := strconv.Unquote(quoted)
	if err != nil {
		return "", fmt.Errorf("failed to decode URL: %v", err)
	}

	return unquoted, nil
}

func extractActualURL(googleURL string) string {
	if googleURL == "" || !strings.HasPrefix(googleURL, "/url?q=") {
		return googleURL
	}

	parsedURL, err := url.Parse(googleURL)
	if err != nil {
		return googleURL
	}

	actualURL := parsedURL.Query().Get("q")
	if actualURL == "" {
		return googleURL
	}

	return actualURL
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
