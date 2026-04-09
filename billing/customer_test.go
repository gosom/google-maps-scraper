package billing

import (
	"context"
	"testing"
)

// fakeCustomerRepo records calls so tests can assert that the fast path
// never reaches the persistence layer (and that errors propagate cleanly).
type fakeCustomerRepo struct {
	setCalls     int
	lastUserID   string
	lastCustomer string
	errOnSet     error
}

func (f *fakeCustomerRepo) SetStripeCustomerID(_ context.Context, userID, stripeCustomerID string) error {
	f.setCalls++
	f.lastUserID = userID
	f.lastCustomer = stripeCustomerID
	return f.errOnSet
}

// newFastPathService builds a Service with only the fields the fast-path
// branch of EnsureStripeCustomer touches (logger). No DB, no Stripe key.
func newFastPathService() *Service {
	return &Service{
		logger: newTestLogger(),
	}
}

// TestEnsureStripeCustomer_FastPath asserts the documented contract: when
// existingCustomerID is non-empty, the function returns it unchanged
// without touching Stripe OR the repo.
func TestEnsureStripeCustomer_FastPath(t *testing.T) {
	svc := newFastPathService()
	repo := &fakeCustomerRepo{}

	got, err := svc.EnsureStripeCustomer(context.Background(), "u1", "a@b.com", "cus_existing", repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cus_existing" {
		t.Errorf("expected cus_existing, got %q", got)
	}
	if repo.setCalls != 0 {
		t.Errorf("fast path must not call repo.SetStripeCustomerID, got %d calls", repo.setCalls)
	}
}

// TestEnsureStripeCustomer_EmptyUserIDRejected guards the validation
// invariant — without a user ID we cannot scope an idempotency key or
// metadata, so the function refuses to call Stripe.
func TestEnsureStripeCustomer_EmptyUserIDRejected(t *testing.T) {
	svc := newFastPathService()
	repo := &fakeCustomerRepo{}

	_, err := svc.EnsureStripeCustomer(context.Background(), "", "a@b.com", "", repo)
	if err == nil {
		t.Fatal("expected error for empty userID, got nil")
	}
	if repo.setCalls != 0 {
		t.Errorf("validation failure must not call repo.SetStripeCustomerID, got %d calls", repo.setCalls)
	}
}

// Note: the slow path (real customer.New call against Stripe) is intentionally
// not unit-tested. There is no stripe-mock harness in this repo and we will
// not introduce one as part of S-C3. The slow path is verified manually in
// staging against Stripe test mode. See the plan Task 1.3 Step 5 for the
// rationale (TDD where it pays — fast-path coverage catches the regression
// surface that matters).
