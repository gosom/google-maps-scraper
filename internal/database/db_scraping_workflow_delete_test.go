package database

import (
	"context"
	"testing"
)

func TestDb_DeleteScrapingWorkflow(t *testing.T) {
	type args struct {
		ctx context.Context
		id  uint64
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
			if err := tt.db.DeleteScrapingWorkflow(tt.args.ctx, tt.args.id); (err != nil) != tt.wantErr {
				t.Errorf("Db.DeleteScrapingWorkflow() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
