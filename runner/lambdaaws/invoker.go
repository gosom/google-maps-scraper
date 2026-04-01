package lambdaaws

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/runner"
)

var _ runner.Runner = (*invoker)(nil)

type invoker struct {
	logger   *slog.Logger
	lclient  *lambda.Client
	payloads []lInput
}

func NewInvoker(cfg *runner.Config, logger *slog.Logger) (runner.Runner, error) {
	if cfg.RunMode != runner.RunModeAwsLambdaInvoker {
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}

	creds := credentials.NewStaticCredentialsProvider(
		cfg.AWS.AccessKey,
		cfg.AWS.SecretKey,
		"",
	)

	awscfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithCredentialsProvider(creds),
		config.WithRegion(cfg.AWS.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %v", err)
	}

	ans := invoker{
		logger:  logger,
		lclient: lambda.NewFromConfig(awscfg),
	}

	if err := ans.setPayloads(cfg); err != nil {
		return nil, err
	}

	return &ans, nil
}

func (i *invoker) Run(ctx context.Context) error {
	for j := range i.payloads {
		if err := i.invoke(ctx, i.payloads[j]); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocritic // let's pass the input as is
func (i *invoker) invoke(ctx context.Context, input lInput) error {
	payloadBytes, err := json.Marshal(input)
	if err != nil {
		return err
	}

	finput := &lambda.InvokeInput{
		FunctionName:   &input.FunctionName,
		Payload:        payloadBytes,
		InvocationType: types.InvocationTypeEvent,
	}

	result, err := i.lclient.Invoke(ctx, finput)
	if err != nil {
		return err
	}

	i.logger.Info("lambda_function_invoked", slog.String("function_name", input.FunctionName), slog.String("job_id", input.JobID), slog.Int("part", input.Part), slog.Int("status_code", int(result.StatusCode)))

	return nil
}

func (i *invoker) Close(context.Context) error {
	return nil
}

func (i *invoker) setPayloads(cfg *runner.Config) error {
	f, err := os.Open(cfg.InputFile)
	if err != nil {
		return err
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	chunkSize := cfg.AWS.LambdaChunkSize

	var currentChunk []string

	chunkNumber := 0
	jobID := uuid.New().String()

	for scanner.Scan() {
		keyword := strings.TrimSpace(scanner.Text())
		if keyword == "" {
			continue
		}

		currentChunk = append(currentChunk, keyword)

		// When we reach chunkSize or EOF, create a new payload
		if len(currentChunk) >= chunkSize {
			payload := lInput{
				JobID:        jobID,
				Part:         chunkNumber,
				BucketName:   cfg.AWS.S3Bucket,
				Keywords:     currentChunk,
				Depth:        cfg.Scraping.MaxDepth,
				Concurrency:  cfg.Concurrency,
				Language:     cfg.Scraping.LangCode,
				FunctionName: cfg.AWS.FunctionName,
				ExtraReviews: cfg.Scraping.ExtraReviews,
			}
			i.payloads = append(i.payloads, payload)

			currentChunk = []string{}
			chunkNumber++
		}
	}

	if len(currentChunk) > 0 {
		payload := lInput{
			JobID:        jobID,
			Part:         chunkNumber,
			BucketName:   cfg.AWS.S3Bucket,
			Keywords:     currentChunk,
			Depth:        cfg.Scraping.MaxDepth,
			Concurrency:  cfg.Concurrency,
			Language:     cfg.Scraping.LangCode,
			FunctionName: cfg.AWS.FunctionName,
			ExtraReviews: cfg.Scraping.ExtraReviews,
		}
		i.payloads = append(i.payloads, payload)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if len(i.payloads) == 0 {
		return fmt.Errorf("no keywords found in input file")
	}

	return nil
}
