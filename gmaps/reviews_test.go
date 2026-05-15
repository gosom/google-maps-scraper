//nolint:testpackage // we need to test unexported functions in the same package
package gmaps

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_extractPlaceID(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{
			name:    "standard hex format with exclamation prefix",
			url:     "https://www.google.com/maps/place/Joe's+Pizza+Broadway/@40.7546795,-73.9870291,17z/data=!4m7!3m6!1s0x89c259ab3c1ef289:0x3b67a41175949f55!8m2!3d40.7546795!4d-73.9870291!16s%2Fg%2F11bw4ws2mt?hl=en&entry=ttu",
			want:    "0x89c259ab3c1ef289:0x3b67a41175949f55",
			wantErr: false,
		},
		{
			name:    "place_id query parameter format",
			url:     "https://www.google.com/maps/place/Joe's+Pizza/@40.7546795,-73.9870291,17z?place_id=ChIJDdnwdv0y5xQRRytw1ihZQeU&hl=en",
			want:    "ChIJDdnwdv0y5xQRRytw1ihZQeU",
			wantErr: false,
		},
		{
			name:    "full place URL with data and hex ID",
			url:     "https://www.google.com/maps/place/Coffee+Project+New+York/data=!4m7!3m6!1s0x89c2599b5a24d7fd:0x9e354f6cf514b9fc!8m2!3d40.7270884!4d-73.989382!16s%2Fg%2F11c3svpqld!19sChIJ_dckWptZwokR_LkU9WxPNZ4",
			want:    "0x89c2599b5a24d7fd:0x9e354f6cf514b9fc",
			wantErr: false,
		},
		{
			name:    "maps search URL (no place ID)",
			url:     "https://www.google.com/maps/search/pizza+in+Brooklyn+NY",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty URL",
			url:     "",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractPlaceID(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractPlaceID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			assert.Equal(t, tt.want, got, "extractPlaceID() = %v, want %v", got, tt.want)
		})
	}
}

func loadReviewsFixture(t *testing.T, filename string) []Review {
	t.Helper()

	raw, err := os.ReadFile("testdata/" + filename)
	require.NoError(t, err)

	var reviewsI []any

	require.NoError(t, json.Unmarshal(raw, &reviewsI))

	return parseReviews(reviewsI)
}

func Test_parseReviews_NativeWithReply(t *testing.T) {
	reviews := loadReviewsFixture(t, "review_native_with_reply.json")
	require.Len(t, reviews, 1)

	r := reviews[0]
	assert.Equal(t, "Ci9DQUlRQUNvZENodHljRjlvT2xGMmRraFdhSFowWW0xWVNURTBObEptU3pWWVgxRRAB", r.ReviewID)
	assert.Equal(t, "Google", r.Source)
	assert.Equal(t, 5, r.RatingScale)
	assert.Equal(t, 1, r.Rating)
	assert.Equal(t, 1.0, r.RatingFloat)
	assert.Equal(t, int64(1772186522193853), r.PostedAtUnixMicros)
	assert.Equal(t, int64(1772186522193853), r.UpdatedAtUnixMicros)
	assert.Equal(t, "https://www.google.com/maps/contrib/116111130377271376564/reviews?hl=en", r.AuthorURL)
	assert.Equal(t, "de", r.Language)
	assert.Equal(t, "en", r.TranslatedLang)
	assert.NotEmpty(t, r.TextOriginal)
	assert.NotEmpty(t, r.TextTranslated)
	assert.NotEmpty(t, r.ReplyTextOriginal)
	assert.NotEmpty(t, r.ReplyText)
	assert.Equal(t, int64(1772266947000000), r.ReplyPostedAtUnixMicros)
	assert.Equal(t, int64(1772266947000000), r.ReplyUpdatedAtUnixMicros)
	assert.Equal(t, "de", r.ReplyLanguage)
	// Backward-compat: existing fields unchanged
	assert.Equal(t, "E. Ö.", r.Name)
	assert.NotEmpty(t, r.Description)
	assert.Equal(t, r.TextOriginal, r.Description)
}

func Test_parseReviews_Aggregator(t *testing.T) {
	reviews := loadReviewsFixture(t, "review_aggregator.json")
	require.Len(t, reviews, 1)

	r := reviews[0]
	assert.Equal(t, "AGG_REVIEW_ID_001", r.ReviewID)
	assert.Equal(t, "Tripadvisor", r.Source)
	assert.Equal(t, 10, r.RatingScale)
	assert.Equal(t, 0, r.Rating)
	assert.Equal(t, 8.5, r.RatingFloat)
	assert.Equal(t, int64(1700000000000000), r.PostedAtUnixMicros)
	assert.Equal(t, int64(1700001000000000), r.UpdatedAtUnixMicros)
	assert.Equal(t, "https://www.tripadvisor.com/members/testuser", r.AuthorURL)
	assert.Equal(t, "de", r.Language)
	assert.Empty(t, r.TranslatedLang)
	assert.NotEmpty(t, r.TextOriginal)
	assert.Empty(t, r.TextTranslated)
	// No reply
	assert.Equal(t, int64(0), r.ReplyPostedAtUnixMicros)
	assert.Empty(t, r.ReplyTextOriginal)
}

func Test_parseReviews_NoText(t *testing.T) {
	reviews := loadReviewsFixture(t, "review_native_no_text.json")
	require.Len(t, reviews, 1)

	r := reviews[0]
	assert.Equal(t, "Ci9DQUlRQUNvZENodHljRjlvT21jMmJ6UnpkemN6Y0dscE9YRndaUzFuVVhCSVprRRAB", r.ReviewID)
	assert.Equal(t, "Google", r.Source)
	assert.Equal(t, 5, r.Rating)
	assert.Equal(t, 5.0, r.RatingFloat)
	assert.Empty(t, r.TextOriginal)
	assert.Empty(t, r.TextTranslated)
	assert.Empty(t, r.Language)
	assert.Empty(t, r.ReplyTextOriginal)
	assert.Equal(t, "Lysann Lieblang", r.Name)
}

func Test_parseReviews_NativeNoTranslation(t *testing.T) {
	reviews := loadReviewsFixture(t, "review_native_no_translation.json")
	require.Len(t, reviews, 1)

	r := reviews[0]
	assert.Equal(t, "ChZDSUhNMG9nS0VJQ0FnSUNZemVhOFpREAE", r.ReviewID)
	assert.Equal(t, "Google", r.Source)
	assert.Equal(t, 5, r.RatingScale)
	assert.Equal(t, "en", r.Language)
	assert.Empty(t, r.TranslatedLang)
	assert.NotEmpty(t, r.TextOriginal)
	assert.Empty(t, r.TextTranslated)
	assert.Empty(t, r.ReplyTextOriginal)
}
