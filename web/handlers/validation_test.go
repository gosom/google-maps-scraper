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
		"language":    "en",
		"depth":       5,
		"max_reviews": 100,
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
			// After Task 2.4: only Name + Keywords + Lang are required.
			// Everything else is optional and filled by ApplyJobDataDefaults.
			// This is the "minimal valid request" — locks in the REST
			// best-practice posture from the audit plan §2.
			name: "Minimal Valid Request With Only Required Fields",
			body: map[string]interface{}{
				"name":     "minimal",
				"keywords": []string{"pizza"},
				"language": "en",
			},
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
			body:           cloneBody(map[string]interface{}{"language": "eng"}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Zoom",
			body:           cloneBody(map[string]interface{}{"zoom": 22}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			// After Task 2.4: depth is optional with default 5. Missing
			// depth no longer 400s — ApplyJobDataDefaults fills it in.
			name:           "Missing Depth Defaults To 5",
			body:           dropKey("depth"),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			// After Task 2.4: depth=0 is treated as "unset" and the default
			// (5) fills in. To assert that the lower bound is enforced,
			// send a NEGATIVE value — that bypasses the default-fill (only
			// fires on zero) and trips the min=1 struct tag.
			name:           "Invalid Depth Low",
			body:           cloneBody(map[string]interface{}{"depth": -1}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Depth High",
			body:           cloneBody(map[string]interface{}{"depth": 21}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid ReviewsMax",
			body:           cloneBody(map[string]interface{}{"max_reviews": 11}),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid ReviewsMax Negative",
			body:           cloneBody(map[string]interface{}{"max_reviews": -1}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid Keywords Max",
			body:           cloneBody(map[string]interface{}{"keywords": []string{"1", "2", "3", "4", "5", "6"}}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing Lang",
			body:           dropKey("language"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid ReviewsMax 0",
			body:           cloneBody(map[string]interface{}{"max_reviews": 0}),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid ReviewsMax Above Cap",
			body:           cloneBody(map[string]interface{}{"max_reviews": 501}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid ReviewsMax Legacy 9999",
			body:           cloneBody(map[string]interface{}{"max_reviews": 9999}),
			expectedStatus: http.StatusBadRequest,
		},
		{
			// After Task 2.4: max_results is optional with default 50. An
			// explicit zero is treated as "unset" and the default fills in.
			name:           "MaxResults Zero Coerced To Default",
			body:           cloneBody(map[string]interface{}{"max_results": 0}),
			expectedStatus: http.StatusUnauthorized,
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
			// After Task 2.4: max_time is optional with default 30 min.
			// Missing max_time no longer 400s.
			name:           "Missing MaxTime Defaults To 30m",
			body:           dropKey("max_time"),
			expectedStatus: http.StatusUnauthorized,
		},
		// Note: max_time cap (1h ceiling) is enforced in the service-layer
		// ValidateJob, which runs AFTER auth. An unauth test request with
		// bad max_time would get 401, not a useful cap-related 4xx, so the
		// cap is asserted directly in web/utils/validation_test.go via
		// TestValidateJobData_RejectsMaxTimeAboveCap instead.
		{
			name:           "Valid ImagesMax",
			body:           cloneBody(map[string]interface{}{"max_images": 100}),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			// May 2026 — Cafe Schöneberg fix: max_images is now PER PLACE
			// (ceiling 500), not per-job total (was 40k). 501 must reject.
			name:           "Invalid ImagesMax Above Cap",
			body:           cloneBody(map[string]interface{}{"max_images": 501}),
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

// validEstimateBody returns a baseline estimate request body.
// estimateRequest has a different shape than apiScrapeRequest (no "name",
// no "max_time"), so it needs its own helper.
func validEstimateBody() map[string]interface{} {
	return map[string]interface{}{
		"keywords":    []string{"pizza"},
		"depth":       5,
		"max_reviews": 100,
		"max_results": 10,
	}
}

func TestAPIHandlers_EstimateJobCost_Validation(t *testing.T) {
	tests := []struct {
		name           string
		body           map[string]interface{}
		expectedStatus int
	}{
		{
			name:           "Valid Request",
			body:           validEstimateBody(),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Missing Keywords",
			body: func() map[string]interface{} {
				b := validEstimateBody()
				delete(b, "keywords")
				return b
			}(),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid Keywords",
			body: func() map[string]interface{} {
				b := validEstimateBody()
				b["keywords"] = []string{}
				return b
			}(),
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
