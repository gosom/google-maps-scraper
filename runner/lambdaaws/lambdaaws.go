package lambdaaws

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
)

var _ runner.Runner = (*lambdaAwsRunner)(nil)

type lambdaAwsRunner struct {
	uploader runner.S3Uploader
}

func New(cfg *runner.Config) (runner.Runner, error) {
	if cfg.RunMode != runner.RunModeAwsLambda {
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}

	ans := lambdaAwsRunner{
		uploader: cfg.S3Uploader,
	}

	return &ans, nil
}

func (l *lambdaAwsRunner) Run(context.Context) error {
	lambda.Start(l.handler)

	return nil
}

func (l *lambdaAwsRunner) Close(context.Context) error {
	return nil
}

//nolint:gocritic // we pass a value to the handler
func (l *lambdaAwsRunner) handler(ctx context.Context, input lInput) error {
	tmpDir := "/tmp"
	browsersDst := filepath.Join(tmpDir, "browsers")
	driverDst := filepath.Join(tmpDir, "ms-playwright-go")

	if err := l.setupBrowsersAndDriver(browsersDst, driverDst); err != nil {
		return err
	}

	out, err := os.Create(filepath.Join(tmpDir, "output.csv"))
	if err != nil {
		return err
	}

	// Track whether file was closed to avoid double-close in defer
	outClosed := false
	defer func() {
		if !outClosed {
			if err := out.Close(); err != nil {
				log.Printf("ERROR: Lambda runner - Failed to close CSV file in defer: %v", err)
			}
		}
	}()

	app, err := l.getApp(ctx, input, out)
	if err != nil {
		return err
	}

	in := strings.NewReader(strings.Join(input.Keywords, "\n"))

	var seedJobs []scrapemate.IJob

	exitMonitor := exiter.New()

	seedJobs, err = runner.CreateSeedJobs(
		false, // TODO support fast mode
		input.Language,
		in,
		input.Depth,
		false, // email
		false, // images
		false, // debug
		func() int {
			if input.ExtraReviews {
				return 1 // Default to 1 review if enabled
			}
			return 0 // No reviews
		}(), // reviewsMax
		"",    // geoCoordinates
		15,    // zoom
		10000, // radius
		nil,   // deduper
		exitMonitor,
		input.ExtraReviews,
		0, // No max results limit for lambda runner (unlimited)
	)
	if err != nil {
		return err
	}

	exitMonitor.SetSeedCount(len(seedJobs))

	bCtx, cancel := context.WithTimeout(ctx, time.Minute*10)
	defer cancel()

	exitMonitor.SetCancelFunc(cancel)

	go exitMonitor.Run(bCtx)

	err = app.Start(bCtx, seedJobs...)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return err
	}

	// CRITICAL: Close the CSV file and check for errors before upload
	// For writable files, Close() can return I/O errors indicating data loss
	if err := out.Close(); err != nil {
		log.Printf("ERROR: Lambda runner - Failed to close CSV file: %v", err)
		return fmt.Errorf("failed to close CSV file: %w", err)
	}
	outClosed = true
	log.Printf("Lambda runner - CSV file closed successfully")

	if l.uploader != nil {
		key := fmt.Sprintf("%s-%d.csv", input.JobID, input.Part)

		fd, err := os.Open(out.Name())
		if err != nil {
			return err
		}
		defer fd.Close()

		result, err := l.uploader.Upload(ctx, input.BucketName, key, fd, "text/csv; charset=utf-8")
		if err != nil {
			return err
		}

		log.Printf("Lambda job %s part %d: S3 upload successful (ETag: %s)", input.JobID, input.Part, result.ETag)
	} else {
		log.Println("no uploader set results are at ", out.Name())
	}

	return nil
}

//nolint:gocritic // we pass a value to the handler
func (l *lambdaAwsRunner) getApp(_ context.Context, input lInput, out io.Writer) (*scrapemateapp.ScrapemateApp, error) {
	csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(out))

	writers := []scrapemate.ResultWriter{csvWriter}

	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(max(1, input.Concurrency)),
		scrapemateapp.WithExitOnInactivity(time.Minute),
		scrapemateapp.WithJS(
			scrapemateapp.DisableImages(),
		),
	}

	if !input.DisablePageReuse {
		opts = append(opts, scrapemateapp.WithPageReuseLimit(2))
		opts = append(opts, scrapemateapp.WithBrowserReuseLimit(200))
	}

	mateCfg, err := scrapemateapp.NewConfig(writers, opts...)
	if err != nil {
		return nil, err
	}

	app, err := scrapemateapp.NewScrapeMateApp(mateCfg)
	if err != nil {
		return nil, err
	}

	return app, nil
}

func (l *lambdaAwsRunner) setupBrowsersAndDriver(browsersDst, driverDst string) error {
	if err := copyDir("/opt/browsers", browsersDst); err != nil {
		return fmt.Errorf("failed to copy browsers: %w", err)
	}

	if err := copyDir("/opt/ms-playwright-go", driverDst); err != nil {
		return fmt.Errorf("failed to copy driver: %w", err)
	}

	return nil
}

func copyDir(src, dst string) error {
	cmd := exec.Command("cp", "-rf", src, dst)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy failed: %v, output: %s", err, string(output))
	}

	return nil
}
