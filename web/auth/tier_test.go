package auth

import (
	"context"
	"testing"

	"github.com/gosom/google-maps-scraper/models"
)

// TestGetUserTier locks in the safe-default contract: a context with no
// UserTierKey set must report free tier, NOT empty. Granting paid limits to
// an unauthenticated or improperly-wired request is the only failure mode
// we want to rule out at the call site of the rate limiter.
func TestGetUserTier(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "no value set defaults to free",
			ctx:  context.Background(),
			want: models.UserTierFree,
		},
		{
			name: "explicit free is free",
			ctx:  context.WithValue(context.Background(), UserTierKey, models.UserTierFree),
			want: models.UserTierFree,
		},
		{
			name: "explicit paid is paid",
			ctx:  context.WithValue(context.Background(), UserTierKey, models.UserTierPaid),
			want: models.UserTierPaid,
		},
		{
			name: "empty string defaults to free (not propagated as paid)",
			ctx:  context.WithValue(context.Background(), UserTierKey, ""),
			want: models.UserTierFree,
		},
		{
			name: "wrong type defaults to free",
			ctx:  context.WithValue(context.Background(), UserTierKey, 123),
			want: models.UserTierFree,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetUserTier(tt.ctx); got != tt.want {
				t.Errorf("GetUserTier() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWithUserContext_SetsAllThreeIdentityKeys locks in the contract that
// withUserContext (called by both auth branches in authenticateRequest)
// populates UserIDKey, UserRoleKey, and UserTierKey atomically. Both the
// Clerk JWT and API key paths funnel through this helper, so a regression
// like "someone removed the UserTierKey line in the API key branch" is
// caught here without needing a full middleware integration harness.
func TestWithUserContext_SetsAllThreeIdentityKeys(t *testing.T) {
	tests := []struct {
		name string
		uid  string
		role string
		tier string
		// What the public accessors should report (mirrors the
		// safe-default behaviour on the cold path).
		wantUID  string
		wantRole string
		wantTier string
	}{
		{
			name:     "paid admin",
			uid:      "user_paid_admin",
			role:     models.RoleAdmin,
			tier:     models.UserTierPaid,
			wantUID:  "user_paid_admin",
			wantRole: models.RoleAdmin,
			wantTier: models.UserTierPaid,
		},
		{
			name:     "free regular user",
			uid:      "user_free",
			role:     models.RoleUser,
			tier:     models.UserTierFree,
			wantUID:  "user_free",
			wantRole: models.RoleUser,
			wantTier: models.UserTierFree,
		},
		{
			name:     "empty role + tier (DB lookup failure cold path)",
			uid:      "user_lookup_failed",
			role:     "",
			tier:     "",
			wantUID:  "user_lookup_failed",
			wantRole: models.RoleUser,     // safe default
			wantTier: models.UserTierFree, // safe default
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := withUserContext(context.Background(), tt.uid, tt.role, tt.tier)

			if got, _ := GetUserID(ctx); got != tt.wantUID {
				t.Errorf("GetUserID() = %q, want %q", got, tt.wantUID)
			}
			if got := GetUserRole(ctx); got != tt.wantRole {
				t.Errorf("GetUserRole() = %q, want %q", got, tt.wantRole)
			}
			if got := GetUserTier(ctx); got != tt.wantTier {
				t.Errorf("GetUserTier() = %q, want %q", got, tt.wantTier)
			}
		})
	}
}
