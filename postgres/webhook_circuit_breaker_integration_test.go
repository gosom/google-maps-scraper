//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/gosom/google-maps-scraper/models"
)

// TestRecordDeliverySuccess_ResetsCounterAndState locks in the contract that
// a single success call clears BOTH the counter and any non-healthy state —
// the only path back to "healthy" without admin intervention.
func TestRecordDeliverySuccess_ResetsCounterAndState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWebhookConfigRepository(db)
	ctx := context.Background()

	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)

	// Drive the row into degraded by recording 5 failures (DegradedThreshold).
	for i := 0; i < models.DegradedThreshold; i++ {
		if _, _, err := repo.RecordDeliveryFailure(ctx, cfgID, "5xx"); err != nil {
			t.Fatalf("seed failure %d: %v", i, err)
		}
	}

	got, err := repo.GetByID(ctx, cfgID, userID)
	if err != nil {
		t.Fatalf("GetByID after failures: %v", err)
	}
	if got.HealthState != models.WebhookHealthDegraded {
		t.Fatalf("precondition: want degraded after %d failures, got %s", models.DegradedThreshold, got.HealthState)
	}
	if got.ConsecutiveFailures != models.DegradedThreshold {
		t.Fatalf("precondition: counter want %d, got %d", models.DegradedThreshold, got.ConsecutiveFailures)
	}

	if err := repo.RecordDeliverySuccess(ctx, cfgID); err != nil {
		t.Fatalf("RecordDeliverySuccess: %v", err)
	}

	got, err = repo.GetByID(ctx, cfgID, userID)
	if err != nil {
		t.Fatalf("GetByID after success: %v", err)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("counter: want 0 after success, got %d", got.ConsecutiveFailures)
	}
	if got.HealthState != models.WebhookHealthHealthy {
		t.Errorf("health_state: want healthy after success, got %s", got.HealthState)
	}
	if got.DisabledAt != nil {
		t.Errorf("disabled_at: want nil, got %v", got.DisabledAt)
	}
	if got.DisabledReason != nil {
		t.Errorf("disabled_reason: want nil, got %v", got.DisabledReason)
	}
}

// TestRecordDeliveryFailure_TripsBreakerAtThreshold is the headline test for
// the whole feature: the Nth (N=AutoDisableThreshold) consecutive failure
// MUST disable the row, and ONLY that call MUST report justDisabled=true.
// Subsequent failures keep the row disabled but must NOT re-fire the
// one-shot notification.
func TestRecordDeliveryFailure_TripsBreakerAtThreshold(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWebhookConfigRepository(db)
	ctx := context.Background()

	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)

	// Failures 1..N-1: state escalates but breaker has not tripped.
	for i := 1; i < models.AutoDisableThreshold; i++ {
		state, justDisabled, err := repo.RecordDeliveryFailure(ctx, cfgID, "5xx")
		if err != nil {
			t.Fatalf("failure %d: %v", i, err)
		}
		if justDisabled {
			t.Fatalf("failure %d: justDisabled must be false before threshold, got true (state=%s)", i, state)
		}
		if state == models.WebhookHealthDisabled {
			t.Fatalf("failure %d: state must not be disabled before threshold, got %s", i, state)
		}
	}

	// The threshold-tripping call.
	state, justDisabled, err := repo.RecordDeliveryFailure(ctx, cfgID, "10_consecutive_failures")
	if err != nil {
		t.Fatalf("threshold failure: %v", err)
	}
	if state != models.WebhookHealthDisabled {
		t.Errorf("state at threshold: want disabled, got %s", state)
	}
	if !justDisabled {
		t.Errorf("justDisabled at threshold: want true, got false")
	}

	got, err := repo.GetByID(ctx, cfgID, userID)
	if err != nil {
		t.Fatalf("GetByID after trip: %v", err)
	}
	if got.DisabledAt == nil {
		t.Error("disabled_at: want non-nil after trip")
	}
	if got.DisabledReason == nil || *got.DisabledReason != "10_consecutive_failures" {
		t.Errorf("disabled_reason: want \"10_consecutive_failures\", got %v", got.DisabledReason)
	}

	// Subsequent failure on already-disabled row: state stays disabled,
	// justDisabled is false (don't re-spam the notification), and
	// disabled_reason is preserved (don't clobber the root-cause tag).
	state, justDisabled, err = repo.RecordDeliveryFailure(ctx, cfgID, "later_5xx")
	if err != nil {
		t.Fatalf("post-trip failure: %v", err)
	}
	if state != models.WebhookHealthDisabled {
		t.Errorf("state after post-trip failure: want disabled, got %s", state)
	}
	if justDisabled {
		t.Errorf("justDisabled after post-trip failure: want false, got true")
	}
	got, err = repo.GetByID(ctx, cfgID, userID)
	if err != nil {
		t.Fatalf("GetByID after post-trip failure: %v", err)
	}
	if got.DisabledReason == nil || *got.DisabledReason != "10_consecutive_failures" {
		t.Errorf("disabled_reason should be preserved, got %v", got.DisabledReason)
	}
}

// TestReenable_ClearsDisabledState exercises the user recovery path: after
// the breaker trips, an authenticated re-enable from the owning user must
// fully clear the disabled state so the next delivery is attempted.
func TestReenable_ClearsDisabledState(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWebhookConfigRepository(db)
	ctx := context.Background()

	userID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, userID, nil)

	for i := 0; i < models.AutoDisableThreshold; i++ {
		if _, _, err := repo.RecordDeliveryFailure(ctx, cfgID, "5xx"); err != nil {
			t.Fatalf("seed failure %d: %v", i, err)
		}
	}
	got, _ := repo.GetByID(ctx, cfgID, userID)
	if got.HealthState != models.WebhookHealthDisabled {
		t.Fatalf("precondition: want disabled, got %s", got.HealthState)
	}

	if err := repo.Reenable(ctx, cfgID, userID); err != nil {
		t.Fatalf("Reenable: %v", err)
	}

	got, err := repo.GetByID(ctx, cfgID, userID)
	if err != nil {
		t.Fatalf("GetByID after Reenable: %v", err)
	}
	if got.HealthState != models.WebhookHealthHealthy {
		t.Errorf("state: want healthy after Reenable, got %s", got.HealthState)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("counter: want 0 after Reenable, got %d", got.ConsecutiveFailures)
	}
	if got.DisabledAt != nil {
		t.Errorf("disabled_at: want nil after Reenable, got %v", got.DisabledAt)
	}
	if !got.IsDeliverable() {
		t.Error("IsDeliverable: want true after Reenable")
	}
}

// TestReenable_OwnershipScoping confirms the IDOR guardrail: another user
// cannot re-enable a config they don't own, even with a valid config ID.
func TestReenable_OwnershipScoping(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWebhookConfigRepository(db)
	ctx := context.Background()

	ownerID := seedUser(t, db)
	attackerID := seedUser(t, db)
	cfgID := seedWebhookConfig(t, db, ownerID, nil)

	for i := 0; i < models.AutoDisableThreshold; i++ {
		if _, _, err := repo.RecordDeliveryFailure(ctx, cfgID, "5xx"); err != nil {
			t.Fatalf("seed failure %d: %v", i, err)
		}
	}

	err := repo.Reenable(ctx, cfgID, attackerID)
	if err != models.ErrWebhookConfigNotFound {
		t.Errorf("cross-user Reenable: want ErrWebhookConfigNotFound, got %v", err)
	}

	// Verify owner's row is still disabled — the unauthorized call must
	// not have produced a partial side-effect.
	got, err := repo.GetByID(ctx, cfgID, ownerID)
	if err != nil {
		t.Fatalf("GetByID after attacker call: %v", err)
	}
	if got.HealthState != models.WebhookHealthDisabled {
		t.Errorf("owner row should remain disabled, got %s", got.HealthState)
	}
}

// TestListActive_ExcludesDisabled is the integration counterpart to the
// new WHERE-clause filter: a disabled config must not appear in the
// "Active" list helpers (both list variants share this contract).
func TestListActive_ExcludesDisabled(t *testing.T) {
	db := setupTestDB(t)
	repo := NewWebhookConfigRepository(db)
	ctx := context.Background()

	userID := seedUser(t, db)
	healthyID := seedWebhookConfig(t, db, userID, nil)
	disabledID := seedWebhookConfig(t, db, userID, nil)

	for i := 0; i < models.AutoDisableThreshold; i++ {
		if _, _, err := repo.RecordDeliveryFailure(ctx, disabledID, "5xx"); err != nil {
			t.Fatalf("seed failure %d: %v", i, err)
		}
	}

	active, err := repo.ListActiveByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("ListActiveByUserID: %v", err)
	}
	if !containsConfigID(active, healthyID) {
		t.Error("ListActiveByUserID: missing healthy config")
	}
	if containsConfigID(active, disabledID) {
		t.Error("ListActiveByUserID: disabled config must be excluded")
	}

	withSecret, err := repo.ListActiveWithSecretByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("ListActiveWithSecretByUserID: %v", err)
	}
	if !containsConfigID(withSecret, healthyID) {
		t.Error("ListActiveWithSecretByUserID: missing healthy config")
	}
	if containsConfigID(withSecret, disabledID) {
		t.Error("ListActiveWithSecretByUserID: disabled config must be excluded")
	}

	// ListByUserID (unscoped variant) MUST still return the disabled row —
	// that's the channel the UI uses to render the "re-enable" CTA.
	all, err := repo.ListByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("ListByUserID: %v", err)
	}
	if !containsConfigID(all, disabledID) {
		t.Error("ListByUserID: disabled config should still be visible to owner")
	}
}

func containsConfigID(cfgs []*models.WebhookConfig, id string) bool {
	for _, c := range cfgs {
		if c.ID == id {
			return true
		}
	}
	return false
}
