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
