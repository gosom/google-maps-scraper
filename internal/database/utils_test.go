package database

import (
	"reflect"
	"testing"

	"github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/dal"
	user "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

func TestDb_PreloadAccount(t *testing.T) {
	type args struct {
		queryRef dal.IAccountORMDo
	}
	tests := []struct {
		name    string
		db      *Db
		args    args
		want    *user.AccountORM
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.db.PreloadAccount(tt.args.queryRef)
			if (err != nil) != tt.wantErr {
				t.Errorf("Db.PreloadAccount() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Db.PreloadAccount() = %v, want %v", got, tt.want)
			}
		})
	}
}
