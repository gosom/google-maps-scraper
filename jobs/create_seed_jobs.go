package jobs

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/utils"
	"github.com/gosom/scrapemate"
)

func CreateSeedJobs(langCode string, r io.Reader, maxDepth int, email bool, useLatLong bool) (jobs []scrapemate.IJob, err error) {
	//if json
	isJson, _ := utils.IsJson(r)
	if isJson {
		return CreateSeedJobsByJson(langCode, r, maxDepth, email, useLatLong)
	}

	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		query := strings.TrimSpace(scanner.Text())
		if query == "" {
			continue
		}

		var id string //= uuid.NewString()

		if before, after, ok := strings.Cut(query, "#!#"); ok {
			query = strings.TrimSpace(before)
			id = strings.TrimSpace(after)
		}

		jobs = append(jobs, gmaps.NewGmapJob(id, langCode, query, maxDepth, email, useLatLong))
	}

	return jobs, scanner.Err()
}

func CreateSeedJobsByJson(langCode string, r io.Reader, maxDepth int, email bool, useLatLong bool) (jobs []scrapemate.IJob, err error) {
	jsonInput := models.JsonInput{}
	utils.ResetReaderPosition(r)
	err = utils.UnmarshalJSON[models.JsonInput](r, &jsonInput)
	if err != nil {
		return jobs, err
	}
	for _, p := range jsonInput.Polygons {
		for _, k := range jsonInput.Keyword {
			locations, err := utils.GenerateH3Listing(p, jsonInput.Resolution)
			if err != nil {
				fmt.Printf("ERROR generating h3 locations: %v", err)
				continue
			}
			for _, location := range locations {
				//Phở/@10.7773285,106.6864011,18z
				query := fmt.Sprintf("%s/@%f,%f,%dz", url.QueryEscape(k), location[0], location[1], jsonInput.ZoomLevel)
				jobs = append(jobs, gmaps.NewGmapJob(uuid.NewString(), langCode, query, maxDepth, email, true))
			}
		}
	}

	return jobs, err
}
