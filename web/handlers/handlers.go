package handlers

import (
	"context"
	"database/sql"
	"html/template"
	"io"
	"log"

	"github.com/gosom/google-maps-scraper/billing"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/googlesheets"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// Dependencies aggregates shared services used by handlers.
type Dependencies struct {
	Logger          *log.Logger
	DB              *sql.DB
	BillingSvc      *billing.Service
	Templates       map[string]*template.Template
	Auth            *auth.AuthMiddleware
	App             JobService
	UserRepo        postgres.UserRepository
	ResultsSvc      ResultsService
	IntegrationRepo models.IntegrationRepository
	GoogleSheetsSvc *googlesheets.Service
}

// HandlerGroup groups all handler categories for routing setup.
type HandlerGroup struct {
	Web         *WebHandlers
	API         *APIHandlers
	Billing     *BillingHandlers
	Integration *IntegrationHandler
}

// NewHandlerGroup constructs a HandlerGroup with initialized handlers.
func NewHandlerGroup(deps Dependencies) *HandlerGroup {
	return &HandlerGroup{
		Web:         &WebHandlers{Deps: deps},
		API:         &APIHandlers{Deps: deps},
		Billing:     &BillingHandlers{Deps: deps},
		Integration: NewIntegrationHandler(deps.IntegrationRepo, deps.App, deps.GoogleSheetsSvc),
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
	Get(ctx context.Context, id string) (models.Job, error)
	Delete(ctx context.Context, id string) error
	Cancel(ctx context.Context, id string) error
	GetCSV(ctx context.Context, id string) (string, error)
	GetCSVReader(ctx context.Context, id string) (io.ReadCloser, string, error)
}

// ResultsService exposes read operations for results data.
type ResultsService interface {
	GetJobResults(ctx context.Context, jobID string) ([]models.Result, error)
	GetUserResults(ctx context.Context, userID string, limit, offset int) ([]models.Result, error)
	GetEnhancedJobResultsPaginated(ctx context.Context, jobID string, limit, offset int) ([]models.EnhancedResult, int, error)
}
