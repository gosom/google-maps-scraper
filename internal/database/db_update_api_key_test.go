package database

import (
	"context"
	"reflect"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

func TestDb_UpdateAPIKey(t *testing.T) {
	type args struct {
		ctx    context.Context
		apiKey *lead_scraper_servicev1.APIKey
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
			got, err := tt.db.UpdateAPIKey(tt.args.ctx, tt.args.apiKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.UpdateAPIKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.UpdateAPIKey() = %v, want %v", got, tt.want)
			}
		})
	}
}
