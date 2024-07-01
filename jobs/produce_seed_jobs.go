package jobs

import (
	"context"
	"io"
	"os"

	"github.com/gosom/google-maps-scraper/models"

	"github.com/gosom/scrapemate"
)

func ProduceSeedJobs(ctx context.Context, args *models.Arguments, provider scrapemate.JobProvider) error {
	var input io.Reader

	switch args.InputFile {
	case "stdin":
		input = os.Stdin
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
