package httpext

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type Option func(*HTTPServer) error

type HTTPServer struct {
	srv  *http.Server
	addr string
}

func New(router http.Handler, opts ...Option) (*HTTPServer, error) {
	ans := HTTPServer{}

	for _, opt := range opts {
		if err := opt(&ans); err != nil {
			return nil, err
		}
	}

	setupDefaults(&ans)

	srv := &http.Server{
		Addr:              ans.addr,
		Handler:           router,
		ReadTimeout:       180 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	ans.srv = srv

	return &ans, nil
}

func WithAddr(addr string) Option {
	return func(s *HTTPServer) error {
		s.addr = addr

		return nil
	}
}

func (h *HTTPServer) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()

		h.gracefulShutdown(ctx)
	}()

	errc := make(chan error, 1)

	go func() {
		errc <- h.srv.ListenAndServe()
	}()

	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func (h *HTTPServer) gracefulShutdown(ctx context.Context) {
	shutdownCtx, shutdownStop := context.WithTimeout(
		context.WithoutCancel(ctx),
		time.Second*10,
	)
	defer shutdownStop()

	if err := h.srv.Shutdown(shutdownCtx); err != nil {
		_ = h.srv.Close()
	}
}

const defaultAddr = ":8080"

func setupDefaults(s *HTTPServer) {
	if s.addr == "" {
		s.addr = defaultAddr
	}
}
