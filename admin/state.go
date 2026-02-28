package admin

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/gosom/google-maps-scraper/ratelimit"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

const DefaultCookieName = "gms_session"

// NewAppState creates a new AppState with all dependencies initialized.
func NewAppState(store IStore, rateLimiter ratelimit.Store, encryptionKey []byte) (*AppState, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	return &AppState{
		Store:         store,
		RateLimiter:   rateLimiter,
		Templates:     tmpl,
		EncryptionKey: encryptionKey,
		CookieName:    DefaultCookieName,
	}, nil
}

// StaticFileHandler returns an http.Handler for serving static files.
func StaticFileHandler() http.Handler {
	staticContent, _ := fs.Sub(staticFS, "static")
	return http.StripPrefix("/admin/static/", http.FileServer(http.FS(staticContent)))
}
