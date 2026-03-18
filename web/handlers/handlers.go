package handlers

import (
	"context"
	"database/sql"
	"html/template"
	"io"
	"log/slog"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/googlesheets"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/web/auth"
	webservices "github.com/gosom/google-maps-scraper/web/services"
)

// Dependencies aggregates shared services used by handlers.
type Dependencies struct {
	Logger             *slog.Logger
	DB                 *sql.DB
	BillingSvc         *billing.Service
	Templates          map[string]*template.Template
	Auth               *auth.AuthMiddleware
	App                JobService
	UserRepo           postgres.UserRepository
	APIKeyRepo         models.APIKeyRepository     // nil if API key feature not configured
	PricingRuleRepo    models.PricingRuleRepository // nil-safe; estimation falls back to defaults
	ServerSecret       []byte                       // HMAC secret for GenerateAPIKey
	ResultsSvc         ResultsService
	IntegrationRepo    models.IntegrationRepository
	GoogleSheetsSvc    *googlesheets.Service
	ConcurrentLimitSvc *webservices.ConcurrentLimitService
	// Version is the Git SHA injected at build time, used by the /health endpoint.
	Version string
}

// HandlerGroup groups all handler categories for routing setup.
type HandlerGroup struct {
	Web         *WebHandlers
	API         *APIHandlers
	APIKey      *APIKeyHandlers
	Billing     *BillingHandlers
	Integration *IntegrationHandler
	Version     *VersionHandler
}

// NewHandlerGroup constructs a HandlerGroup with initialized handlers.
func NewHandlerGroup(deps Dependencies) *HandlerGroup {
	return &HandlerGroup{
		Web:         &WebHandlers{Deps: deps},
		API:         &APIHandlers{Deps: deps},
		APIKey:      &APIKeyHandlers{Deps: deps},
		Billing:     &BillingHandlers{Deps: deps},
		Integration: NewIntegrationHandler(deps.IntegrationRepo, deps.App, deps.GoogleSheetsSvc),
		Version:     NewVersionHandler(),
	}
}

// WebHandlers contains routes for HTML UI and public pages.
type WebHandlers struct{ Deps Dependencies }

// APIHandlers contains routes for authenticated JSON API.
type APIHandlers struct{ Deps Dependencies }

// BillingHandlers contains routes for billing and webhooks.
type BillingHandlers struct{ Deps Dependencies }

// JobService is the minimal interface needed by handlers to interact with jobs.
type JobService interface {
	Create(ctx context.Context, job *models.Job) error
	All(ctx context.Context, userID string) ([]models.Job, error)
	Get(ctx context.Context, id string, userID string) (models.Job, error)
	Delete(ctx context.Context, id string, userID string) error
	Cancel(ctx context.Context, id string, userID string) error
	GetCSV(ctx context.Context, id string) (string, error)
	GetCSVReader(ctx context.Context, id string) (io.ReadCloser, string, error)
}

// ResultsService exposes read operations for results data.
type ResultsService interface {
	GetJobResults(ctx context.Context, jobID string) ([]models.Result, error)
	GetUserResults(ctx context.Context, userID string, limit, offset int) ([]models.Result, error)
	GetEnhancedJobResultsPaginated(ctx context.Context, jobID string, limit, offset int) ([]models.EnhancedResult, int, error)
}
