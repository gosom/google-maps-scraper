package database

import (
	"context"
	"reflect"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

func TestDb_CreateScrapingJob(t *testing.T) {
	type args struct {
		ctx context.Context
		job *lead_scraper_servicev1.ScrapingJob
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    *lead_scraper_servicev1.ScrapingJob
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.CreateScrapingJob(tt.args.ctx, tt.args.job)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.CreateScrapingJob() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.CreateScrapingJob() = %v, want %v", got, tt.want)
			}
		})
	}
}
