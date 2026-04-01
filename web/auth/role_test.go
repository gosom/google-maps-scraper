package auth_test

import (
	"context"
	"testing"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/web/auth"
)

func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name string
		role string
		want bool
	}{
		{"admin role returns true", models.RoleAdmin, true},
		{"user role returns false", models.RoleUser, false},
		{"empty role returns false", "", false},
		{"unknown role returns false", "superadmin", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), auth.UserRoleKey, tt.role)
			if got := auth.IsAdmin(ctx); got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}
