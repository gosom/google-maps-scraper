package runner

import (
	"log/slog"
	"testing"

	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeAWSDefaults(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		appCfg        *pkgconfig.Config
		wantAccessKey string
		wantSecretKey string
		wantRegion    string
		wantPanic     bool
	}{
		{
			name: "flag set env empty flag wins",
			cfg: &Config{AWS: AWSConfig{
				AccessKey: "flag-key",
				SecretKey: "flag-secret",
				Region:    "flag-region",
			}},
			appCfg: &pkgconfig.Config{AWS: pkgconfig.AWSConfig{
				AccessKeyID:     "",
				SecretAccessKey: "",
				Region:          "",
			}},
			wantAccessKey: "flag-key",
			wantSecretKey: "flag-secret",
			wantRegion:    "flag-region",
		},
		{
			name: "flag empty env set env fills",
			cfg: &Config{AWS: AWSConfig{
				AccessKey: "",
				SecretKey: "",
				Region:    "",
			}},
			appCfg: &pkgconfig.Config{AWS: pkgconfig.AWSConfig{
				AccessKeyID:     "env-key",
				SecretAccessKey: "env-secret",
				Region:          "env-region",
			}},
			wantAccessKey: "env-key",
			wantSecretKey: "env-secret",
			wantRegion:    "env-region",
		},
		{
			name: "both set flag wins",
			cfg: &Config{AWS: AWSConfig{
				AccessKey: "flag-key",
				SecretKey: "flag-secret",
				Region:    "flag-region",
			}},
			appCfg: &pkgconfig.Config{AWS: pkgconfig.AWSConfig{
				AccessKeyID:     "env-key",
				SecretAccessKey: "env-secret",
				Region:          "env-region",
			}},
			wantAccessKey: "flag-key",
			wantSecretKey: "flag-secret",
			wantRegion:    "flag-region",
		},
		{
			name: "both empty both remain empty",
			cfg: &Config{AWS: AWSConfig{
				AccessKey: "",
				SecretKey: "",
				Region:    "",
			}},
			appCfg: &pkgconfig.Config{AWS: pkgconfig.AWSConfig{
				AccessKeyID:     "",
				SecretAccessKey: "",
				Region:          "",
			}},
			wantAccessKey: "",
			wantSecretKey: "",
			wantRegion:    "",
		},
		{
			name:   "nil cfg no panic",
			cfg:    nil,
			appCfg: &pkgconfig.Config{},
		},
		{
			name:   "nil appCfg no panic",
			cfg:    &Config{},
			appCfg: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				MergeAWSDefaults(tt.cfg, tt.appCfg)
			})

			if tt.cfg != nil && tt.appCfg != nil {
				assert.Equal(t, tt.wantAccessKey, tt.cfg.AWS.AccessKey)
				assert.Equal(t, tt.wantSecretKey, tt.cfg.AWS.SecretKey)
				assert.Equal(t, tt.wantRegion, tt.cfg.AWS.Region)
			}
		})
	}
}

func TestBuildS3Uploader_SkipsWhenCredsIncomplete(t *testing.T) {
	cfg := &Config{AWS: AWSConfig{
		AccessKey: "",
		SecretKey: "",
		Region:    "",
	}}
	err := BuildS3Uploader(cfg, slog.Default())
	require.NoError(t, err)
	assert.Nil(t, cfg.S3Uploader, "S3Uploader should be nil when creds are incomplete")
}

func TestBuildS3Uploader_SkipsWhenOnlySomeCredsSet(t *testing.T) {
	cfg := &Config{AWS: AWSConfig{
		AccessKey: "some-key",
		SecretKey: "",
		Region:    "us-east-1",
	}}
	err := BuildS3Uploader(cfg, slog.Default())
	require.NoError(t, err)
	assert.Nil(t, cfg.S3Uploader, "S3Uploader should be nil when not all creds are provided")
}

// TestBuildS3Uploader_PassesEndpointToUploader verifies that the new
// endpoint/SSE/checksum fields flow into the constructed uploader.
// We can't easily inspect the resulting client's BaseEndpoint without
// unsafe, so the smoke test is: construction succeeds and the uploader
// is non-nil. Functional behaviour is covered by stub-client tests in
// later chunks.
func TestBuildS3Uploader_PassesEndpointToUploader(t *testing.T) {
	cfg := &Config{AWS: AWSConfig{
		AccessKey: "k",
		SecretKey: "s",
		Region:    "nyc3",
		Endpoint:  "https://nyc3.digitaloceanspaces.com",
	}}
	err := BuildS3Uploader(cfg, slog.Default())
	require.NoError(t, err)
	require.NotNil(t, cfg.S3Uploader)
}

// TestBuildS3Uploader_PartialConfigRejected verifies the partial-config
// guard: if AWS_ENDPOINT is set but creds are missing, the function
// returns a clear error rather than silently no-op'ing.
func TestBuildS3Uploader_PartialConfigRejected(t *testing.T) {
	cfg := &Config{AWS: AWSConfig{
		AccessKey: "",
		SecretKey: "",
		Region:    "",
		Endpoint:  "https://nyc3.digitaloceanspaces.com",
	}}
	err := BuildS3Uploader(cfg, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AWS_ENDPOINT")
}

func TestSplitAndTrim(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"http://a:b@host:1", []string{"http://a:b@host:1"}},
		{" http://a:b@h:1 , http://c:d@h2:2 ", []string{"http://a:b@h:1", "http://c:d@h2:2"}},
		{",,, ,", nil},
		{"http://only", []string{"http://only"}},
	}
	for _, c := range cases {
		got := splitAndTrim(c.in)
		assert.Equal(t, c.want, got, "input %q", c.in)
	}
}
