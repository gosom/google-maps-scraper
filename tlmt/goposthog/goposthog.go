package goposthog

import (
	"context"

	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/posthog/posthog-go"
)

type service struct {
	client posthog.Client
}

func New(publicAPIKEY, endpointURL string) (tlmt.Telemetry, error) {
	client, err := posthog.NewWithConfig(publicAPIKEY, posthog.Config{Endpoint: endpointURL})
	if err != nil {
		return nil, err
	}

	ans := service{
		client: client,
	}

	return &ans, nil
}

func (s *service) Send(_ context.Context, event tlmt.Event) error {
	capture := posthog.Capture{
		DistinctId: event.AnonymousID,
		Event:      event.Name,
		Properties: event.Properties,
	}

	if err := capture.Validate(); err != nil {
		return err
	}

	return s.client.Enqueue(capture)
}

func (s *service) Close() error {
	if s.client != nil {
		return s.client.Close()
	}

	return nil
}
