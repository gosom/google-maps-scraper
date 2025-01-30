package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailExtraction(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected []string
	}{
		{
			name: "extract from mailto links",
			html: `
				<html>
					<body>
						<a href="mailto:test@example.com">Email us</a>
						<a href="mailto:support@example.com">Support</a>
					</body>
				</html>
			`,
			expected: []string{
				"test@example.com",
				"support@example.com",
			},
		},
		{
			name: "extract from text content",
			html: `
				<html>
					<body>
						<p>Contact us at contact@example.com or sales@example.com</p>
						<div>Invalid email: not.an.email</div>
					</body>
				</html>
			`,
			expected: []string{
				"contact@example.com",
				"sales@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test document
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			require.NoError(t, err)

			// Test mailto extraction
			emails := docEmailExtractor(doc)

			// If no mailto links found, test regex extraction
			if len(emails) == 0 {
				emails = regexEmailExtractor([]byte(tt.html))
			}

			// Verify results
			assert.Equal(t, len(tt.expected), len(emails), "should find expected number of emails")
			for _, expectedEmail := range tt.expected {
				assert.Contains(t, emails, expectedEmail, "should find expected email")
			}
		})
	}
}

func TestEmailJob(t *testing.T) {
	tests := []struct {
		name          string
		url           string
		maxDepth      int
		html          string
		expectedLinks []string
	}{
		{
			name:     "process single page",
			url:      "https://example.com",
			maxDepth: 0,
			html: `
				<html>
					<body>
						<a href="mailto:test@example.com">Email us</a>
						<p>Contact: contact@example.com</p>
					</body>
				</html>
			`,
			expectedLinks: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test job
			job := NewEmailJob(tt.url, tt.maxDepth, "test-agent")

			// Create test document
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			require.NoError(t, err)

			// Create test response
			resp := &scrapemate.Response{
				Document: doc,
				Body:     []byte(tt.html),
			}

			// Process the job
			result, newJobs, err := job.Process(context.Background(), resp)
			require.NoError(t, err)

			// Verify result contains emails
			resultMap := result.(map[string]interface{})
			assert.Equal(t, tt.url, resultMap["url"])
			assert.NotEmpty(t, resultMap["emails"])

			// Verify new jobs if depth > 0
			if tt.maxDepth > 0 {
				assert.Equal(t, len(tt.expectedLinks), len(newJobs))
				for i, link := range tt.expectedLinks {
					assert.Equal(t, link, newJobs[i].(*EmailJob).URL)
				}
			} else {
				assert.Empty(t, newJobs)
			}
		})
	}
}

func TestCreateEmailTask(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		maxDepth  int
		userAgent string
	}{
		{
			name:      "basic task",
			url:       "example.com",
			maxDepth:  2,
			userAgent: "test-agent",
		},
		{
			name:      "with https",
			url:       "https://example.com",
			maxDepth:  1,
			userAgent: "Mozilla/5.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := CreateEmailTask(tt.url, tt.maxDepth, tt.userAgent)
			require.NoError(t, err)
			assert.Equal(t, TypeEmailExtract, task.Type())

			// Verify task payload
			var payload struct {
				URL       string `json:"url"`
				MaxDepth  int    `json:"max_depth"`
				UserAgent string `json:"user_agent"`
			}
			err = json.Unmarshal(task.Payload(), &payload)
			require.NoError(t, err)

			assert.Equal(t, tt.url, payload.URL)
			assert.Equal(t, tt.maxDepth, payload.MaxDepth)
			assert.Equal(t, tt.userAgent, payload.UserAgent)
		})
	}
}
