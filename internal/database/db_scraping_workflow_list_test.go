package database

import (
	"context"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
)

func TestListScrapingWorkflows(t *testing.T) {
	// Create multiple test workflows
	workflows := []*lead_scraper_servicev1.ScrapingWorkflow{
		{
			CronExpression:       "0 0 * * *",
			RetryCount:           0,
			MaxRetries:           5,
			AlertEmails:          "test1@example.com",
			OrgId:               "test-org-1",
			TenantId:            "test-tenant-1",
			GeoFencingRadius:    1000.0,
			GeoFencingLat:       40.7128,
			GeoFencingLon:       -74.0060,
		},
		{
			CronExpression:       "0 12 * * *",
			RetryCount:           1,
			MaxRetries:           3,
			AlertEmails:          "test2@example.com",
			OrgId:               "test-org-2",
			TenantId:            "test-tenant-2",
			GeoFencingRadius:    2000.0,
			GeoFencingLat:       41.8781,
			GeoFencingLon:       -87.6298,
		},
		{
			CronExpression:       "0 0 1 * *",
			RetryCount:           2,
			MaxRetries:           4,
			AlertEmails:          "test3@example.com",
			OrgId:               "test-org-3",
			TenantId:            "test-tenant-3",
			GeoFencingRadius:    3000.0,
			GeoFencingLat:       34.0522,
			GeoFencingLon:       -118.2437,
		},
	}

	for _, w := range workflows {
		created, err := conn.CreateScrapingWorkflow(context.Background(), w)
		assert.NoError(t, err)
		assert.NotNil(t, created)
	}

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantCount int
		wantError bool
	}{
		{
			name:      "valid limit and offset",
			limit:     2,
			offset:    0,
			wantCount: 2,
			wantError: false,
		},
		{
			name:      "zero limit",
			limit:     0,
			offset:    0,
			wantCount: 10, // default limit
			wantError: false,
		},
		{
			name:      "offset exceeds total",
			limit:     10,
			offset:    100,
			wantCount: 0,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			results, err := conn.ListScrapingWorkflows(ctx, tt.limit, tt.offset)

			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, results)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, results)
				assert.Len(t, results, tt.wantCount)

				if len(results) > 0 {
					// Verify the first result has all required fields
					first := results[0]
					assert.NotZero(t, first.Id)
					assert.NotEmpty(t, first.CronExpression)
					assert.NotEmpty(t, first.AlertEmails)
					assert.NotEmpty(t, first.OrgId)
					assert.NotEmpty(t, first.TenantId)
				}
			}
		})
	}
}
