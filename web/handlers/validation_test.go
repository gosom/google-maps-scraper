package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// validBody returns a baseline request body that satisfies every cap. Each
// table-driven case clones this map and mutates one field to assert the
// corresponding rejection branch.
func validBody() map[string]interface{} {
	return map[string]interface{}{
		"name":        "test job",
		"keywords":    []string{"pizza"},
		"lang":        "en",
		"depth":       5,
		"reviews_max": 100,
		"max_results": 10,
		"max_time":    60,
	}
}

func cloneBody(overrides map[string]interface{}) map[string]interface{} {
	out := validBody()
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// dropKey returns a clone of the valid body with one key removed. Used to
// assert that required fields are enforced.
func dropKey(key string) map[string]interface{} {
	out := validBody()
	delete(out, key)
	return out
}

func TestAPIHandlers_Scrape_Validation(t *testing.T) {
	tests := []struct {
		name           string
		body           map[string]interface{}
		expectedStatus int
	}{
		{
			name: "Valid Request",
			body: validBody(),
			// Expect 401 because validation passes but auth is missing
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Missing Name",
			body:           dropKey("name"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing Keywords",
			body:           dropKey("keywords"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Empty Keywords",
			body:           cloneBody(map[string]interface{}{"keywords": []string{}}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Lang Length",
			body:           cloneBody(map[string]interface{}{"lang": "eng"}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Zoom",
			body:           cloneBody(map[string]interface{}{"zoom": 22}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing Depth",
			body:           dropKey("depth"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Depth Low",
			body:           cloneBody(map[string]interface{}{"depth": 0}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Depth High",
			body:           cloneBody(map[string]interface{}{"depth": 21}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid ReviewsMax",
			body:           cloneBody(map[string]interface{}{"reviews_max": 11}),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid ReviewsMax Negative",
			body:           cloneBody(map[string]interface{}{"reviews_max": -1}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Keywords Max",
			body:           cloneBody(map[string]interface{}{"keywords": []string{"1", "2", "3", "4", "5", "6"}}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing Lang",
			body:           dropKey("lang"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid ReviewsMax 0",
			body:           cloneBody(map[string]interface{}{"reviews_max": 0}),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid ReviewsMax Above Cap",
			body:           cloneBody(map[string]interface{}{"reviews_max": 501}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid ReviewsMax Legacy 9999",
			body:           cloneBody(map[string]interface{}{"reviews_max": 9999}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid MaxResults Zero",
			body:           cloneBody(map[string]interface{}{"max_results": 0}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid MaxResults Above Cap",
			body:           cloneBody(map[string]interface{}{"max_results": 501}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid MaxResults At Cap",
			body:           cloneBody(map[string]interface{}{"max_results": 500}),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid MaxResults Legacy 1000",
			body:           cloneBody(map[string]interface{}{"max_results": 1000}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing MaxTime",
			body:           dropKey("max_time"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid ImagesMax",
			body:           cloneBody(map[string]interface{}{"images_max": 5000}),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid ImagesMax Above Cap",
			body:           cloneBody(map[string]interface{}{"images_max": 20001}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Radius Above Cap",
			body:           cloneBody(map[string]interface{}{"radius": 50001}),
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
			body: validBody(),
			// Expect 401 because validation passes but auth is missing
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Missing Name",
			body:           dropKey("name"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Keywords",
			body:           cloneBody(map[string]interface{}{"keywords": []string{}}),
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
