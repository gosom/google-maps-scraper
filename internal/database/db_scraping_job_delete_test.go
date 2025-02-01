package database

import (
	"context"
	"testing"
)

func TestDb_DeleteScrapingJob(t *testing.T) {
	type args struct {
		ctx context.Context
		id  string
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
			if err := tt.db.DeleteScrapingJob(tt.args.ctx, tt.args.id); (err != nil) != tt.wantErr {
				t.Errorf("Db.DeleteScrapingJob() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
