package auth

import (
	"context"

	"github.com/gosom/google-maps-scraper/models"
)

// IsAdmin returns true if the authenticated user has the admin role.
func IsAdmin(ctx context.Context) bool {
	return GetUserRole(ctx) == models.RoleAdmin
}
