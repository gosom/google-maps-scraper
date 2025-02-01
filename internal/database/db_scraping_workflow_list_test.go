package database

import (
	"context"
	"reflect"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

func TestDb_ListScrapingWorkflows(t *testing.T) {
	type args struct {
		ctx    context.Context
		limit  int
		offset int
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    []*lead_scraper_servicev1.ScrapingWorkflow
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.ListScrapingWorkflows(tt.args.ctx, tt.args.limit, tt.args.offset)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.ListScrapingWorkflows() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.ListScrapingWorkflows() = %v, want %v", got, tt.want)
			}
		})
	}
}
