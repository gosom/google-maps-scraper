package gmaps

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
)

type Entry struct {
	Link         string
	Cid          string
	Title        string
	Categories   []string
	Category     string
	Address      string
	OpenHours    map[string][]string
	WebSite      string
	Phone        string
	PlusCode     string
	ReviewCount  int
	ReviewRating float64
	Latitude     float64
	Longtitude   float64
	Status       string
	Description  string
	ReviewsLink  string
	Thumbnail    string
	Timezone     string
	PriceRange   string
	DataID       string
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
		"link",
		"title",
		"category",
		"address",
		"open_hours",
		"website",
		"phone",
		"plus_code",
		"review_count",
		"review_rating",
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
	}
}

func (e *Entry) CsvRow() []string {
	return []string{
		e.Link,
		e.Title,
		e.Category,
		e.Address,
		stringify(e.OpenHours),
		e.WebSite,
		e.Phone,
		e.PlusCode,
		stringify(e.ReviewCount),
		stringify(e.ReviewRating),
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
	}
}

//nolint:gomnd // it's ok, I need the indexes
func EntryFromJSON(raw []byte) (entry Entry, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic: %v stack: %s", r, debug.Stack())

			return
		}
	}()

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

	entry.Address = getNthElementAndCast[string](darray, 18)
	entry.OpenHours = getHours(darray)
	entry.WebSite = getNthElementAndCast[string](darray, 7, 0)
	entry.Phone = getNthElementAndCast[string](darray, 178, 0, 0)
	entry.PlusCode = getNthElementAndCast[string](darray, 183, 2, 2, 0)
	entry.ReviewCount = int(getNthElementAndCast[float64](darray, 4, 8))
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

	return entry, nil
}

//nolint:gomnd // it's ok, I need the indexes
func getHours(darray []any) map[string][]string {
	items := getNthElementAndCast[[]any](darray, 34, 1)
	hours := make(map[string][]string, len(items))

	for _, item := range items {
		day := getNthElementAndCast[string](item.([]any), 0)
		timesI := getNthElementAndCast[[]any](item.([]any), 1)
		times := make([]string, len(timesI))

		for i := range timesI {
			times[i], _ = timesI[i].(string)
		}

		hours[day] = times
	}

	return hours
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

	ans, ok := arr[indexes[0]].(T)
	if !ok {
		return defaultVal
	}

	return ans
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
