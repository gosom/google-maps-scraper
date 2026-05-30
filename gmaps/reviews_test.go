//nolint:testpackage // we need to test unexported functions in the same package
package gmaps

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
