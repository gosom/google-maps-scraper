package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gosom/google-maps-scraper/models"
)

type ResultsService struct{ db *sql.DB }

func NewResultsService(db *sql.DB) *ResultsService { return &ResultsService{db: db} }

// NullableJSON helps scan JSONB/text fields that may be NULL
type NullableJSON struct {
	Data  interface{}
	Valid bool
}

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
		var any interface{}
		if err := json.Unmarshal(v, &any); err != nil {
			// store as string if not valid JSON
			nj.Data = string(v)
		} else {
			nj.Data = any
		}
		return nil
	case string:
		nj.Valid = true
		if v == "" {
			nj.Data = nil
			return nil
		}
		var any interface{}
		if err := json.Unmarshal([]byte(v), &any); err != nil {
			nj.Data = v
		} else {
			nj.Data = any
		}
		return nil
	default:
		return fmt.Errorf("cannot scan %T into NullableJSON", value)
	}
}

func convertToInt(s string) (int, error) {
	if i, err := strconv.Atoi(s); err == nil {
		return i, nil
	}
	return 0, fmt.Errorf("invalid int string: %s", s)
}

func (s *ResultsService) GetJobResults(ctx context.Context, jobID string) ([]models.Result, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	const q = `SELECT 
            id, user_id, job_id, input_id, link, cid, title,
            categories, category, address, website, phone, pluscode,
            review_count, rating, latitude, longitude, status_info,
            description, reviews_link, thumbnail, timezone, price_range,
            data_id, emails, created_at
        FROM results
        WHERE job_id = $1
        ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, q, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to query results: %w", err)
	}
	defer rows.Close()
	var results []models.Result
	for rows.Next() {
		var r models.Result
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.JobID, &r.InputID, &r.Link, &r.Cid, &r.Title,
			&r.Categories, &r.Category, &r.Address, &r.Website, &r.Phone, &r.PlusCode,
			&r.ReviewCount, &r.Rating, &r.Latitude, &r.Longitude, &r.Status,
			&r.Description, &r.ReviewsLink, &r.Thumbnail, &r.Timezone, &r.PriceRange,
			&r.DataID, &r.Emails, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return results, nil
}

func (s *ResultsService) GetUserResults(ctx context.Context, userID string, limit, offset int) ([]models.Result, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	const q = `SELECT 
            id, user_id, job_id, input_id, link, cid, title,
            categories, category, address, website, phone, pluscode,
            review_count, rating, latitude, longitude, status_info,
            description, reviews_link, thumbnail, timezone, price_range,
            data_id, emails, created_at
        FROM results
        WHERE user_id = $1
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	rows, err := s.db.QueryContext(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query results: %w", err)
	}
	defer rows.Close()
	var results []models.Result
	for rows.Next() {
		var r models.Result
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.JobID, &r.InputID, &r.Link, &r.Cid, &r.Title,
			&r.Categories, &r.Category, &r.Address, &r.Website, &r.Phone, &r.PlusCode,
			&r.ReviewCount, &r.Rating, &r.Latitude, &r.Longitude, &r.Status,
			&r.Description, &r.ReviewsLink, &r.Thumbnail, &r.Timezone, &r.PriceRange,
			&r.DataID, &r.Emails, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return results, nil
}

func (s *ResultsService) GetEnhancedJobResultsPaginated(ctx context.Context, jobID string, limit, offset int) ([]models.EnhancedResult, int, error) {
	if s.db == nil {
		return nil, 0, fmt.Errorf("database not available")
	}
	const countQ = `SELECT COUNT(1) FROM results WHERE job_id = $1`
	var total int
	if err := s.db.QueryRowContext(ctx, countQ, jobID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count results: %w", err)
	}

	const q = `SELECT 
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
        LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, q, jobID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query enhanced results: %w", err)
	}
	defer rows.Close()

	var results []models.EnhancedResult
	for rows.Next() {
		var r models.EnhancedResult
		var openHours, popularTimes, reviewsPerRating, menu, owner, completeAddress NullableJSON
		var images, reservations, orderOnline, about, userReviews, userReviewsExtended NullableJSON
		if err := rows.Scan(
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
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan enhanced result: %w", err)
		}

		if openHours.Valid && openHours.Data != nil {
			switch data := openHours.Data.(type) {
			case string:
				if data != "" && data != "{}" {
					r.OpenHours = map[string][]string{"default": {data}}
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

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("row iteration error: %w", err)
	}
	return results, total, nil
}
