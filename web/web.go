package web

import (
	"context"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	templates "github.com/wolfeidau/echo-go-templates"

	"github.com/gosom/google-maps-scraper/entities"
	"github.com/gosom/google-maps-scraper/web/internal/server"
	"github.com/gosom/google-maps-scraper/web/internal/views"
	"github.com/gosom/google-maps-scraper/worker"
)

type Config struct {
	Addr  string
	Debug bool
	Store entities.Store
}

func Start(ctx context.Context, cfg Config) error {
	e := echo.New()
	e.Debug = cfg.Debug

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.Logger.SetOutput(os.Stderr)
	e.Logger.SetOutput(os.Stderr)

	render := templates.New()

	err := render.AddWithLayoutAndIncludes(views.Content, "layouts/base.html", "includes/*.html", "templates/*.html")
	if err != nil {
		return err
	}

	e.Renderer = render

	workhorse := worker.NewWorker(cfg.Store)

	go func() {
		if err := workhorse.Start(ctx); err != nil {
			panic(err)
		}
	}()

	srv := server.NewServer(workhorse, cfg.Store)

	server.RegisterHandlers(e, srv)

	go func() {
		select {
		case <-ctx.Done():
			e.Shutdown(ctx)
		}
	}()

	return e.Start(cfg.Addr)
}
