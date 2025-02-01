package database

import (
	"context"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
)

func TestUpdateScrapingWorkflow(t *testing.T) {
	// Create a test workflow first
	testWorkflow := &lead_scraper_servicev1.ScrapingWorkflow{
		CronExpression:       "0 0 * * *",
		RetryCount:           0,
		MaxRetries:           5,
		AlertEmails:          "test@example.com",
		OrgId:               "test-org",
		TenantId:            "test-tenant",
		GeoFencingRadius:    1000.0,
		GeoFencingLat:       40.7128,
		GeoFencingLon:       -74.0060,
		GeoFencingZoomMin:   10,
		GeoFencingZoomMax:   20,
		IncludeReviews:      true,
		IncludePhotos:       true,
		IncludeBusinessHours: true,
		MaxReviewsPerBusiness: 100,
		RespectRobotsTxt:     true,
		AcceptTermsOfService:  true,
		UserAgent:            "TestBot/1.0",
	}
	created, err := conn.CreateScrapingWorkflow(context.Background(), testWorkflow)
	assert.NoError(t, err)
	assert.NotNil(t, created)

	tests := []struct {
		name      string
		workflow  *lead_scraper_servicev1.ScrapingWorkflow
		wantError bool
	}{
		{
			name: "valid update",
			workflow: &lead_scraper_servicev1.ScrapingWorkflow{
				Id:                   created.Id,
				CronExpression:       "0 0 0 * * *",
				RetryCount:           1,
				MaxRetries:           3,
				AlertEmails:          "updated@example.com",
				OrgId:               "updated-org",
				TenantId:            "updated-tenant",
				GeoFencingRadius:    2000.0,
				GeoFencingLat:       41.8781,
				GeoFencingLon:       -87.6298,
				GeoFencingZoomMin:   12,
				GeoFencingZoomMax:   18,
				IncludeReviews:      false,
				IncludePhotos:       false,
				IncludeBusinessHours: false,
				MaxReviewsPerBusiness: 50,
				RespectRobotsTxt:     true,
				AcceptTermsOfService:  true,
				UserAgent:            "UpdatedTestBot/2.0",
			},
			wantError: false,
		},
		{
			name:      "nil workflow",
			workflow:  nil,
			wantError: true,
		},
		{
			name: "zero id",
			workflow: &lead_scraper_servicev1.ScrapingWorkflow{
				Id:                   0,
				CronExpression:       "0 0 0 * * *",
				RetryCount:           1,
				MaxRetries:           3,
				AlertEmails:          "updated@example.com",
			},
			wantError: true,
		},
		{
			name: "non-existent id",
			workflow: &lead_scraper_servicev1.ScrapingWorkflow{
				Id:                   999999,
				CronExpression:       "0 0 0 * * *",
				RetryCount:           1,
				MaxRetries:           3,
				AlertEmails:          "updated@example.com",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			result, err := conn.UpdateScrapingWorkflow(ctx, tt.workflow)

			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.workflow.Id, result.Id)
				assert.Equal(t, tt.workflow.CronExpression, result.CronExpression)
				assert.Equal(t, tt.workflow.RetryCount, result.RetryCount)
				assert.Equal(t, tt.workflow.MaxRetries, result.MaxRetries)
				assert.Equal(t, tt.workflow.AlertEmails, result.AlertEmails)
				assert.Equal(t, tt.workflow.OrgId, result.OrgId)
				assert.Equal(t, tt.workflow.TenantId, result.TenantId)
			}
		})
	}
}
