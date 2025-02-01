package database

import (
	"context"
	"reflect"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

func TestDb_ValidateAPIKey(t *testing.T) {
	type args struct {
		ctx  context.Context
		hash string
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    *lead_scraper_servicev1.APIKey
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.ValidateAPIKey(tt.args.ctx, tt.args.hash)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.ValidateAPIKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.ValidateAPIKey() = %v, want %v", got, tt.want)
			}
		})
	}
}
