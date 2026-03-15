package postgres

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/gosom/scrapemate"

    "github.com/gosom/google-maps-scraper/gmaps"
)

func NewResultWriter(db *sql.DB) scrapemate.ResultWriter {
    return &resultWriter{db: db}
}

type resultWriter struct {
    db *sql.DB
}

func (r *resultWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
    const maxBatchSize = 50

    buff := make([]*gmaps.Entry, 0, 50)
    lastSave := time.Now().UTC()

    for result := range in {
        entry, ok := result.Data.(*gmaps.Entry)

        if !ok {
            return errors.New("invalid data type")
        }

        buff = append(buff, entry)

        if len(buff) >= maxBatchSize || time.Now().UTC().Sub(lastSave) >= time.Minute {
            err := r.batchSave(ctx, buff)
            if err != nil {
                return err
            }

            buff = buff[:0]
            lastSave = time.Now().UTC()
        }
    }

    if len(buff) > 0 {
        err := r.batchSave(ctx, buff)
        if err != nil {
            return err
        }
    }

    return nil
}

func mustJSON(v any) []byte {
    data, err := json.Marshal(v)
    if err != nil {
        return []byte("null")
    }
    return data
}

func pgTextArray(items []string) string {
    if len(items) == 0 {
        return "{}"
    }

    escaped := make([]string, len(items))
    for i, item := range items {
        item = strings.ReplaceAll(item, `\`, `\\`)
        item = strings.ReplaceAll(item, `"`, `\"`)
        escaped[i] = `"` + item + `"`
    }

    return "{" + strings.Join(escaped, ",") + "}"
}

func (r *resultWriter) batchSave(ctx context.Context, entries []*gmaps.Entry) error {
    if len(entries) == 0 {
        return nil
    }

    columns := []string{
        "input_id", "link", "title", "category", "categories",
        "address", "open_hours", "website", "phone", "plus_code",
        "review_count", "review_rating", "reviews_per_rating",
        "latitude", "longitude", "cid", "status", "description",
        "reviews_link", "thumbnail", "timezone", "price_range",
        "data_id", "place_id", "images", "reservations", "order_online",
        "menu", "owner", "complete_address", "about",
        "user_reviews", "user_reviews_extended", "emails",
    }

    numCols := len(columns)
    valuePlaceholders := make([]string, 0, len(entries))
    args := make([]interface{}, 0, len(entries)*numCols)

    for i, entry := range entries {
        placeholders := make([]string, numCols)
        for j := 0; j < numCols; j++ {
            placeholders[j] = fmt.Sprintf("$%d", i*numCols+j+1)
        }
        valuePlaceholders = append(valuePlaceholders, fmt.Sprintf("(%s)", strings.Join(placeholders, ", ")))

        categories := entry.Categories
        if categories == nil {
            categories = []string{}
        }
        emails := entry.Emails
        if emails == nil {
            emails = []string{}
        }

        args = append(args,
            entry.ID,
            entry.Link,
            entry.Title,
            entry.Category,
            pgTextArray(categories),
            entry.Address,
            mustJSON(entry.OpenHours),
            entry.WebSite,
            entry.Phone,
            entry.PlusCode,
            entry.ReviewCount,
            entry.ReviewRating,
            mustJSON(entry.ReviewsPerRating),
            entry.Latitude,
            entry.Longtitude,
            entry.Cid,
            entry.Status,
            entry.Description,
            entry.ReviewsLink,
            entry.Thumbnail,
            entry.Timezone,
            entry.PriceRange,
            entry.DataID,
            entry.PlaceID,
            mustJSON(entry.Images),
            mustJSON(entry.Reservations),
            mustJSON(entry.OrderOnline),
            mustJSON(entry.Menu),
            mustJSON(entry.Owner),
            mustJSON(entry.CompleteAddress),
            mustJSON(entry.About),
            mustJSON(entry.UserReviews),
            mustJSON(entry.UserReviewsExtended),
            pgTextArray(emails),
        )
    }

    q := fmt.Sprintf(`INSERT INTO grounding_data (%s) VALUES %s ON CONFLICT DO NOTHING`,
        strings.Join(columns, ", "),
        strings.Join(valuePlaceholders, ", "),
    )

    tx, err := r.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }

    defer func() {
        _ = tx.Rollback()
    }()

    _, err = tx.ExecContext(ctx, q, args...)
    if err != nil {
        return err
    }

    return tx.Commit()
}
