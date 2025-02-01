package database

import (
	"context"
	"reflect"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

func TestDb_CreateLead(t *testing.T) {
	type args struct {
		ctx           context.Context
		scrapingJobID uint64
		lead          *lead_scraper_servicev1.Lead
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    *lead_scraper_servicev1.Lead
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.CreateLead(tt.args.ctx, tt.args.scrapingJobID, tt.args.lead)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.CreateLead() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.CreateLead() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_GetLead(t *testing.T) {
	type args struct {
		ctx context.Context
		id  uint64
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    *lead_scraper_servicev1.Lead
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.GetLead(tt.args.ctx, tt.args.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.GetLead() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.GetLead() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_UpdateLead(t *testing.T) {
	type args struct {
		ctx  context.Context
		lead *lead_scraper_servicev1.Lead
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    *lead_scraper_servicev1.Lead
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.UpdateLead(tt.args.ctx, tt.args.lead)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.UpdateLead() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.UpdateLead() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_DeleteLead(t *testing.T) {
	type args struct {
		ctx          context.Context
		id           uint64
		deletionType DeletionType
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.db.DeleteLead(tt.args.ctx, tt.args.id, tt.args.deletionType); (err != nil) != tt.wantErr {
				t.Errorf("Db.DeleteLead() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDb_ListLeads(t *testing.T) {
	type args struct {
		ctx    context.Context
		limit  int
		offset int
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    []*lead_scraper_servicev1.Lead
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.ListLeads(tt.args.ctx, tt.args.limit, tt.args.offset)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.ListLeads() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.ListLeads() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_BatchUpdateLeads(t *testing.T) {
	type args struct {
		ctx   context.Context
		leads []*lead_scraper_servicev1.Lead
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    []*lead_scraper_servicev1.Lead
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.BatchUpdateLeads(tt.args.ctx, tt.args.leads)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.BatchUpdateLeads() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.BatchUpdateLeads() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDb_BatchDeleteLeads(t *testing.T) {
	type args struct {
		ctx     context.Context
		leadIDs []uint64
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.db.BatchDeleteLeads(tt.args.ctx, tt.args.leadIDs); (err != nil) != tt.wantErr {
				t.Errorf("Db.BatchDeleteLeads() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBreakIntoBatches(t *testing.T) {
	type args struct {
		items     []T
		batchSize int
	}
	tests := []struct {
		name string
		args args
		want [][]T
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BreakIntoBatches(tt.args.items, tt.args.batchSize); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BreakIntoBatches() = %v, want %v", got, tt.want)
			}
		})
	}
}
