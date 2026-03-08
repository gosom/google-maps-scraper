package rqueue

import (
	"context"

	"riverqueue.com/riverui"

	"github.com/gosom/google-maps-scraper/log"
)

func CreateRiverUIHandler(_ context.Context, client *Client) (*riverui.Handler, error) {
	endpoints := riverui.NewEndpoints(client.RiverClient(), nil)

	logger := log.With("srv", "riverui")

	opts := &riverui.HandlerOpts{
		Endpoints: endpoints,
		Logger:    logger,
		Prefix:    "/riverui",
	}

	handler, err := riverui.NewHandler(opts)
	if err != nil {
		return nil, err
	}

	return handler, nil
}
