package web

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"database/sql/driver"
)

// EnhancedResult represents a single scraped result with all rich data
type EnhancedResult struct {
	ID                  int                      `json:"id"`
	UserID              string                   `json:"user_id"`
	JobID               string                   `json:"job_id"`
	InputID             string                   `json:"input_id"`
	Link                string                   `json:"link"`
	Cid                 string                   `json:"cid"`
	Title               string                   `json:"title"`
	Categories          string                   `json:"categories"`
	Category            string                   `json:"category"`
	Address             string                   `json:"address"`
	OpenHours           map[string][]string      `json:"open_hours,omitempty"`
	PopularTimes        map[string]map[int]int   `json:"popular_times,omitempty"`
	Website             string                   `json:"website"`
	Phone               string                   `json:"phone"`
	PlusCode            string                   `json:"plus_code"`
	ReviewCount         int                      `json:"review_count"`
	Rating              float64                  `json:"rating"`
	ReviewsPerRating    map[int]int              `json:"reviews_per_rating,omitempty"`
	Latitude            float64                  `json:"latitude"`
	Longitude           float64                  `json:"longitude"`
	Status              string                   `json:"status"`
	Description         string                   `json:"description"`
	ReviewsLink         string                   `json:"reviews_link"`
	Thumbnail           string                   `json:"thumbnail"`
	Timezone            string                   `json:"timezone"`
	PriceRange          string                   `json:"price_range"`
	DataID              string                   `json:"data_id"`
	Images              []map[string]interface{} `json:"images,omitempty"`
	Reservations        []map[string]interface{} `json:"reservations,omitempty"`
	OrderOnline         []map[string]interface{} `json:"order_online,omitempty"`
	Menu                map[string]interface{}   `json:"menu,omitempty"`
	Owner               map[string]interface{}   `json:"owner,omitempty"`
	CompleteAddress     map[string]interface{}   `json:"complete_address,omitempty"`
	About               []map[string]interface{} `json:"about,omitempty"`
	UserReviews         []map[string]interface{} `json:"user_reviews,omitempty"`
	UserReviewsExtended []map[string]interface{} `json:"user_reviews_extended,omitempty"`
	Emails              string                   `json:"emails"`
	CreatedAt           time.Time                `json:"created_at"`
}

// NullableJSON is a helper type for handling nullable JSONB fields
type NullableJSON struct {
	Data  interface{}
	Valid bool
}

// Scan implements the Scanner interface for database/sql
func (nj *NullableJSON) Scan(value interface{}) error {
	if value == nil {
		nj.Data = nil
		nj.Valid = false
		return nil
	}

	switch v := value.(type) {
	case []byte:
		nj.Valid = true
		if len(v) == 0 {
			nj.Data = nil
			return nil
		}
		// Try to parse as JSON, if it fails, treat as plain text
		if err := json.Unmarshal(v, &nj.Data); err != nil {
			// If it's not valid JSON, store as string
			nj.Data = string(v)
		}
		return nil
	case string:
		nj.Valid = true
		if v == "" {
			nj.Data = nil
			return nil
		}
		// Try to parse as JSON, if it fails, treat as plain text
		if err := json.Unmarshal([]byte(v), &nj.Data); err != nil {
			// If it's not valid JSON, store as string
			nj.Data = v
		}
		return nil
	default:
		return fmt.Errorf("cannot scan %T into NullableJSON", value)
	}
}

// Value implements the driver Valuer interface
func (nj NullableJSON) Value() (driver.Value, error) {
	if !nj.Valid {
		return nil, nil
	}
	return json.Marshal(nj.Data)
}

// getEnhancedJobResults retrieves enhanced results for a specific job from database
func (s *Server) getEnhancedJobResults(ctx context.Context, jobID string) ([]EnhancedResult, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	query := `
		SELECT 
			id, 
			COALESCE(user_id, '') as user_id,
			COALESCE(job_id, '') as job_id,
			COALESCE(input_id, '') as input_id,
			COALESCE(link, '') as link,
			COALESCE(cid, '') as cid,
			title, 
			COALESCE(categories, '') as categories,
			category, 
			address, 
			COALESCE(openhours, '{}') as openhours,
			COALESCE(popular_times, '{}') as popular_times,
			website, 
			phone, 
			pluscode,
			review_count, 
			rating, 
			COALESCE(reviews_per_rating, '{}') as reviews_per_rating,
			COALESCE(latitude, 0) as latitude,
			COALESCE(longitude, 0) as longitude,
			COALESCE(status_info, '') as status_info,
			COALESCE(description, '') as description,
			COALESCE(reviews_link, '') as reviews_link,
			COALESCE(thumbnail, '') as thumbnail,
			COALESCE(timezone, '') as timezone,
			COALESCE(price_range, '') as price_range,
			COALESCE(data_id, '') as data_id, 
			COALESCE(images, '[]') as images,
			COALESCE(reservations, '[]') as reservations,
			COALESCE(order_online, '[]') as order_online,
			COALESCE(menu, '{}') as menu,
			COALESCE(owner, '{}') as owner,
			COALESCE(complete_address, '{}') as complete_address,
			COALESCE(about, '[]') as about,
			COALESCE(user_reviews, '[]') as user_reviews,
			COALESCE(user_reviews_extended, '[]') as user_reviews_extended,
			COALESCE(emails, '') as emails,
			COALESCE(created_at, NOW()) as created_at
		FROM results 
		WHERE job_id = $1 
		ORDER BY created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to query enhanced results: %w", err)
	}
	defer rows.Close()

	var results []EnhancedResult
	for rows.Next() {
		var r EnhancedResult
		var openHours, popularTimes, reviewsPerRating, menu, owner, completeAddress NullableJSON
		var images, reservations, orderOnline, about, userReviews, userReviewsExtended NullableJSON

		err := rows.Scan(
			&r.ID, &r.UserID, &r.JobID, &r.InputID, &r.Link, &r.Cid, &r.Title,
			&r.Categories, &r.Category, &r.Address,
			&openHours, &popularTimes,
			&r.Website, &r.Phone, &r.PlusCode,
			&r.ReviewCount, &r.Rating, &reviewsPerRating,
			&r.Latitude, &r.Longitude, &r.Status,
			&r.Description, &r.ReviewsLink, &r.Thumbnail, &r.Timezone, &r.PriceRange,
			&r.DataID,
			&images, &reservations, &orderOnline, &menu, &owner, &completeAddress,
			&about, &userReviews, &userReviewsExtended,
			&r.Emails, &r.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan enhanced result: %w", err)
		}

		// Convert JSONB fields to proper types - handle both text and JSONB formats
		if openHours.Valid && openHours.Data != nil {
			switch data := openHours.Data.(type) {
			case string:
				// Handle text format - might be empty or simple text
				if data != "" && data != "{}" {
					r.OpenHours = map[string][]string{
						"default": {data},
					}
				}
			case map[string]interface{}:
				// Handle JSONB format
				r.OpenHours = make(map[string][]string)
				for day, times := range data {
					if timeSlice, ok := times.([]interface{}); ok {
						r.OpenHours[day] = make([]string, len(timeSlice))
						for i, t := range timeSlice {
							if timeStr, ok := t.(string); ok {
								r.OpenHours[day][i] = timeStr
							}
						}
					} else if timeStr, ok := times.(string); ok {
						r.OpenHours[day] = []string{timeStr}
					}
				}
			}
		}

		if popularTimes.Valid && popularTimes.Data != nil {
			if times, ok := popularTimes.Data.(map[string]interface{}); ok {
				r.PopularTimes = make(map[string]map[int]int)
				for day, dayTimes := range times {
					if dayTimesMap, ok := dayTimes.(map[string]interface{}); ok {
						r.PopularTimes[day] = make(map[int]int)
						for hour, traffic := range dayTimesMap {
							if hourInt, err := convertToInt(hour); err == nil {
								if trafficFloat, ok := traffic.(float64); ok {
									r.PopularTimes[day][hourInt] = int(trafficFloat)
								}
							}
						}
					}
				}
			}
		}

		if reviewsPerRating.Valid && reviewsPerRating.Data != nil {
			if ratings, ok := reviewsPerRating.Data.(map[string]interface{}); ok {
				r.ReviewsPerRating = make(map[int]int)
				for rating, count := range ratings {
					if ratingInt, err := convertToInt(rating); err == nil {
						if countFloat, ok := count.(float64); ok {
							r.ReviewsPerRating[ratingInt] = int(countFloat)
						}
					}
				}
			}
		}

		// Handle slice types
		if images.Valid && images.Data != nil {
			if imageSlice, ok := images.Data.([]interface{}); ok {
				r.Images = make([]map[string]interface{}, len(imageSlice))
				for i, img := range imageSlice {
					if imgMap, ok := img.(map[string]interface{}); ok {
						r.Images[i] = imgMap
					}
				}
			}
		}

		if reservations.Valid && reservations.Data != nil {
			if resSlice, ok := reservations.Data.([]interface{}); ok {
				r.Reservations = make([]map[string]interface{}, len(resSlice))
				for i, res := range resSlice {
					if resMap, ok := res.(map[string]interface{}); ok {
						r.Reservations[i] = resMap
					}
				}
			}
		}

		if orderOnline.Valid && orderOnline.Data != nil {
			if orderSlice, ok := orderOnline.Data.([]interface{}); ok {
				r.OrderOnline = make([]map[string]interface{}, len(orderSlice))
				for i, order := range orderSlice {
					if orderMap, ok := order.(map[string]interface{}); ok {
						r.OrderOnline[i] = orderMap
					}
				}
			}
		}

		if about.Valid && about.Data != nil {
			if aboutSlice, ok := about.Data.([]interface{}); ok {
				r.About = make([]map[string]interface{}, len(aboutSlice))
				for i, ab := range aboutSlice {
					if abMap, ok := ab.(map[string]interface{}); ok {
						r.About[i] = abMap
					}
				}
			}
		}

		if userReviews.Valid && userReviews.Data != nil {
			if reviewSlice, ok := userReviews.Data.([]interface{}); ok {
				r.UserReviews = make([]map[string]interface{}, len(reviewSlice))
				for i, review := range reviewSlice {
					if reviewMap, ok := review.(map[string]interface{}); ok {
						r.UserReviews[i] = reviewMap
					}
				}
			}
		}

		if userReviewsExtended.Valid && userReviewsExtended.Data != nil {
			if reviewSlice, ok := userReviewsExtended.Data.([]interface{}); ok {
				r.UserReviewsExtended = make([]map[string]interface{}, len(reviewSlice))
				for i, review := range reviewSlice {
					if reviewMap, ok := review.(map[string]interface{}); ok {
						r.UserReviewsExtended[i] = reviewMap
					}
				}
			}
		}

		// Handle map types
		if menu.Valid && menu.Data != nil {
			if menuMap, ok := menu.Data.(map[string]interface{}); ok {
				r.Menu = menuMap
			}
		}

		if owner.Valid && owner.Data != nil {
			if ownerMap, ok := owner.Data.(map[string]interface{}); ok {
				r.Owner = ownerMap
			}
		}

		if completeAddress.Valid && completeAddress.Data != nil {
			if addrMap, ok := completeAddress.Data.(map[string]interface{}); ok {
				r.CompleteAddress = addrMap
			}
		}

		results = append(results, r)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

// Helper function to convert string to int safely
func convertToInt(s string) (int, error) {
	switch s {
	case "1":
		return 1, nil
	case "2":
		return 2, nil
	case "3":
		return 3, nil
	case "4":
		return 4, nil
	case "5":
		return 5, nil
	case "0":
		return 0, nil
	case "6":
		return 6, nil
	case "7":
		return 7, nil
	case "8":
		return 8, nil
	case "9":
		return 9, nil
	case "10":
		return 10, nil
	case "11":
		return 11, nil
	case "12":
		return 12, nil
	case "13":
		return 13, nil
	case "14":
		return 14, nil
	case "15":
		return 15, nil
	case "16":
		return 16, nil
	case "17":
		return 17, nil
	case "18":
		return 18, nil
	case "19":
		return 19, nil
	case "20":
		return 20, nil
	case "21":
		return 21, nil
	case "22":
		return 22, nil
	case "23":
		return 23, nil
	default:
		return 0, fmt.Errorf("invalid int string: %s", s)
	}
}

// getEnhancedJobResultsPaginated retrieves paginated enhanced results for a specific job from database
func (s *Server) getEnhancedJobResultsPaginated(ctx context.Context, jobID string, limit, offset int) ([]EnhancedResult, int, error) {
	if s.db == nil {
		return nil, 0, fmt.Errorf("database not available")
	}

	// First, get the total count
	countQuery := `SELECT COUNT(*) FROM results WHERE job_id = $1`
	var totalCount int
	err := s.db.QueryRowContext(ctx, countQuery, jobID).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count results: %w", err)
	}

	// Then get the paginated results
	query := `
		SELECT 
			id, 
			COALESCE(user_id, '') as user_id,
			COALESCE(job_id, '') as job_id,
			COALESCE(input_id, '') as input_id,
			COALESCE(link, '') as link,
			COALESCE(cid, '') as cid,
			title, 
			COALESCE(categories, '') as categories,
			category, 
			address, 
			COALESCE(openhours, '{}') as openhours,
			COALESCE(popular_times, '{}') as popular_times,
			website, 
			phone, 
			pluscode,
			review_count, 
			rating, 
			COALESCE(reviews_per_rating, '{}') as reviews_per_rating,
			COALESCE(latitude, 0) as latitude,
			COALESCE(longitude, 0) as longitude,
			COALESCE(status_info, '') as status_info,
			COALESCE(description, '') as description,
			COALESCE(reviews_link, '') as reviews_link,
			COALESCE(thumbnail, '') as thumbnail,
			COALESCE(timezone, '') as timezone,
			COALESCE(price_range, '') as price_range,
			COALESCE(data_id, '') as data_id, 
			COALESCE(images, '[]') as images,
			COALESCE(reservations, '[]') as reservations,
			COALESCE(order_online, '[]') as order_online,
			COALESCE(menu, '{}') as menu,
			COALESCE(owner, '{}') as owner,
			COALESCE(complete_address, '{}') as complete_address,
			COALESCE(about, '[]') as about,
			COALESCE(user_reviews, '[]') as user_reviews,
			COALESCE(user_reviews_extended, '[]') as user_reviews_extended,
			COALESCE(emails, '') as emails,
			COALESCE(created_at, NOW()) as created_at
		FROM results 
		WHERE job_id = $1 
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.db.QueryContext(ctx, query, jobID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query enhanced results: %w", err)
	}
	defer rows.Close()

	var results []EnhancedResult
	for rows.Next() {
		var r EnhancedResult
		var openHours, popularTimes, reviewsPerRating, menu, owner, completeAddress NullableJSON
		var images, reservations, orderOnline, about, userReviews, userReviewsExtended NullableJSON

		err := rows.Scan(
			&r.ID, &r.UserID, &r.JobID, &r.InputID, &r.Link, &r.Cid, &r.Title,
			&r.Categories, &r.Category, &r.Address,
			&openHours, &popularTimes,
			&r.Website, &r.Phone, &r.PlusCode,
			&r.ReviewCount, &r.Rating, &reviewsPerRating,
			&r.Latitude, &r.Longitude, &r.Status,
			&r.Description, &r.ReviewsLink, &r.Thumbnail, &r.Timezone, &r.PriceRange,
			&r.DataID,
			&images, &reservations, &orderOnline, &menu, &owner, &completeAddress,
			&about, &userReviews, &userReviewsExtended,
			&r.Emails, &r.CreatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan enhanced result: %w", err)
		}

		// Convert JSONB fields to proper types - reuse existing logic from original function
		if openHours.Valid && openHours.Data != nil {
			switch data := openHours.Data.(type) {
			case string:
				if data != "" && data != "{}" {
					r.OpenHours = map[string][]string{
						"default": {data},
					}
				}
			case map[string]interface{}:
				r.OpenHours = make(map[string][]string)
				for day, times := range data {
					if timeSlice, ok := times.([]interface{}); ok {
						r.OpenHours[day] = make([]string, len(timeSlice))
						for i, t := range timeSlice {
							if timeStr, ok := t.(string); ok {
								r.OpenHours[day][i] = timeStr
							}
						}
					} else if timeStr, ok := times.(string); ok {
						r.OpenHours[day] = []string{timeStr}
					}
				}
			}
		}

		if popularTimes.Valid && popularTimes.Data != nil {
			if times, ok := popularTimes.Data.(map[string]interface{}); ok {
				r.PopularTimes = make(map[string]map[int]int)
				for day, dayTimes := range times {
					if dayTimesMap, ok := dayTimes.(map[string]interface{}); ok {
						r.PopularTimes[day] = make(map[int]int)
						for hour, traffic := range dayTimesMap {
							if hourInt, err := convertToInt(hour); err == nil {
								if trafficFloat, ok := traffic.(float64); ok {
									r.PopularTimes[day][hourInt] = int(trafficFloat)
								}
							}
						}
					}
				}
			}
		}

		if reviewsPerRating.Valid && reviewsPerRating.Data != nil {
			if ratings, ok := reviewsPerRating.Data.(map[string]interface{}); ok {
				r.ReviewsPerRating = make(map[int]int)
				for rating, count := range ratings {
					if ratingInt, err := convertToInt(rating); err == nil {
						if countFloat, ok := count.(float64); ok {
							r.ReviewsPerRating[ratingInt] = int(countFloat)
						}
					}
				}
			}
		}

		// Handle slice types
		if images.Valid && images.Data != nil {
			if imageSlice, ok := images.Data.([]interface{}); ok {
				r.Images = make([]map[string]interface{}, len(imageSlice))
				for i, img := range imageSlice {
					if imgMap, ok := img.(map[string]interface{}); ok {
						r.Images[i] = imgMap
					}
				}
			}
		}

		if reservations.Valid && reservations.Data != nil {
			if resSlice, ok := reservations.Data.([]interface{}); ok {
				r.Reservations = make([]map[string]interface{}, len(resSlice))
				for i, res := range resSlice {
					if resMap, ok := res.(map[string]interface{}); ok {
						r.Reservations[i] = resMap
					}
				}
			}
		}

		if orderOnline.Valid && orderOnline.Data != nil {
			if orderSlice, ok := orderOnline.Data.([]interface{}); ok {
				r.OrderOnline = make([]map[string]interface{}, len(orderSlice))
				for i, order := range orderSlice {
					if orderMap, ok := order.(map[string]interface{}); ok {
						r.OrderOnline[i] = orderMap
					}
				}
			}
		}

		if about.Valid && about.Data != nil {
			if aboutSlice, ok := about.Data.([]interface{}); ok {
				r.About = make([]map[string]interface{}, len(aboutSlice))
				for i, ab := range aboutSlice {
					if abMap, ok := ab.(map[string]interface{}); ok {
						r.About[i] = abMap
					}
				}
			}
		}

		if userReviews.Valid && userReviews.Data != nil {
			if reviewSlice, ok := userReviews.Data.([]interface{}); ok {
				r.UserReviews = make([]map[string]interface{}, len(reviewSlice))
				for i, review := range reviewSlice {
					if reviewMap, ok := review.(map[string]interface{}); ok {
						r.UserReviews[i] = reviewMap
					}
				}
			}
		}

		if userReviewsExtended.Valid && userReviewsExtended.Data != nil {
			if reviewSlice, ok := userReviewsExtended.Data.([]interface{}); ok {
				r.UserReviewsExtended = make([]map[string]interface{}, len(reviewSlice))
				for i, review := range reviewSlice {
					if reviewMap, ok := review.(map[string]interface{}); ok {
						r.UserReviewsExtended[i] = reviewMap
					}
				}
			}
		}

		// Handle map types
		if menu.Valid && menu.Data != nil {
			if menuMap, ok := menu.Data.(map[string]interface{}); ok {
				r.Menu = menuMap
			}
		}

		if owner.Valid && owner.Data != nil {
			if ownerMap, ok := owner.Data.(map[string]interface{}); ok {
				r.Owner = ownerMap
			}
		}

		if completeAddress.Valid && completeAddress.Data != nil {
			if addrMap, ok := completeAddress.Data.(map[string]interface{}); ok {
				r.CompleteAddress = addrMap
			}
		}

		results = append(results, r)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("row iteration error: %w", err)
	}

	return results, totalCount, nil
}
