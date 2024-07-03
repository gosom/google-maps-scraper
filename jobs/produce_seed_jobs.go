package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/gosom/google-maps-scraper/models"

	"github.com/gosom/scrapemate"
)

func ProduceSeedJobs(ctx context.Context, args *models.Arguments, provider scrapemate.JobProvider, jsonInput *models.JsonInput) error {
	var input io.Reader

	switch args.InputFile {
	case "stdin":
		input = os.Stdin
	case "json":
		// Assuming jsonInput is already populated with JSON data
		jsonData, err := json.Marshal(jsonInput)
		if err != nil {
			return err
		}
		input = bytes.NewReader(jsonData)
		break
	default:
		f, err := os.Open(args.InputFile)
		if err != nil {
			return err
		}

		defer f.Close()

		input = f
	}

	jobs, err := CreateSeedJobs(args.LangCode, input, args.MaxDepth, args.Email, args.UseLatLong)
	if err != nil {
		return err
	}

	for i := range jobs {
		if err := provider.Push(ctx, jobs[i]); err != nil {
			return err
		}
	}

	return nil
}
