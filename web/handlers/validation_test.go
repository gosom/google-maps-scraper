package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIHandlers_Scrape_Validation(t *testing.T) {
	tests := []struct {
		name           string
		body           map[string]interface{}
		expectedStatus int
	}{
		{
			name: "Valid Request",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"depth":       5,
				"reviews_max": 100,
			},
			// Expect 401 because validation passes but auth is missing
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Missing Name",
			body: map[string]interface{}{
				"keywords": []string{"pizza"},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Missing Keywords",
			body: map[string]interface{}{
				"name": "test job",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Empty Keywords",
			body: map[string]interface{}{
				"name":     "test job",
				"keywords": []string{},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid Lang",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "eng", // Too long
				"depth":       5,
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid Zoom",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"zoom":        22, // Max is 21
				"depth":       5,
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Missing Depth",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Missing ReviewsMax",
			body: map[string]interface{}{
				"name":     "test job",
				"keywords": []string{"pizza"},
				"lang":     "en",
				"depth":    5,
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Invalid Depth Low",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"depth":       0, // Min is 1
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid Depth High",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"depth":       21, // Max is 20
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Valid ReviewsMax",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"reviews_max": 11, // Min is 11
				"depth":       5,
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Invalid ReviewsMax Low",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"reviews_max": -1, // Min is 0
				"depth":       5,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid Keywords Max",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"1", "2", "3", "4", "5", "6"}, // Max is 5
				"lang":        "en",
				"depth":       5,
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Missing Lang",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"depth":       5,
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Valid ReviewsMax 0",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"depth":       5,
				"reviews_max": 0, // 0 is now allowed
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Invalid ReviewsMax High",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"depth":       5,
				"reviews_max": 10000, // Max is 9999
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Valid MaxResults 0",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"max_results": 0, // 0 is allowed
				"depth":       5,
				"reviews_max": 100,
				"lang":        "en",
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Valid MaxResults 1000",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"max_results": 1000, // Max is 1000
				"depth":       5,
				"reviews_max": 100,
				"lang":        "en",
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Invalid MaxResults High",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"max_results": 1001, // Max is 1000
				"depth":       5,
				"reviews_max": 100,
				"lang":        "en",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	h := &APIHandlers{Deps: Dependencies{}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/v1/jobs", bytes.NewBuffer(bodyBytes))
			w := httptest.NewRecorder()

			h.Scrape(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, w.Code, w.Body.String())
			}
		})
	}
}

func TestAPIHandlers_EstimateJobCost_Validation(t *testing.T) {
	tests := []struct {
		name           string
		body           map[string]interface{}
		expectedStatus int
	}{
		{
			name: "Valid Request",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"depth":       5,
				"reviews_max": 100,
			},
			// Expect 401 because validation passes but auth is missing
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Missing Name",
			body: map[string]interface{}{
				"keywords":    []string{"pizza"},
				"lang":        "en",
				"depth":       5,
				"reviews_max": 100,
			},
			// Name is required in apiScrapeRequest
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid Keywords",
			body: map[string]interface{}{
				"name":        "test job",
				"keywords":    []string{},
				"lang":        "en",
				"depth":       5,
				"reviews_max": 100,
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	h := &APIHandlers{Deps: Dependencies{}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/v1/jobs/estimate", bytes.NewBuffer(bodyBytes))
			w := httptest.NewRecorder()

			h.EstimateJobCost(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tt.expectedStatus, w.Code, w.Body.String())
			}
		})
	}
}
