package webrunner

import (
	"context"
	"errors"
	"testing"

	"github.com/gosom/google-maps-scraper/proxypool"
)

// TestClassifyProxyOutcome_NaturalSuccess_DoesNotBlameProxy is the
// regression guard for the master-review CRITICAL finding: mate.Start
// returns context.Canceled on natural success-path termination, so
// jobErr=context.Canceled is the COMMON case for successful unlimited-
// mode scrapes. Without filtering it out, every successful scrape would
// classify as NetworkErr and a healthy proxy would cool out after 3
// successes — defeating the entire health-tracking feature.
func TestClassifyProxyOutcome_NaturalSuccess_DoesNotBlameProxy(t *testing.T) {
	reason, report := classifyProxyOutcome(true, context.Canceled, false)
	if report {
		t.Fatalf("natural success (jobSuccess=true, err=context.Canceled, !reviewCircuit) reported as failure (reason=%s); should be Success", reason)
	}
}

// TestClassifyProxyOutcome_TableDriven covers every documented branch
// of the classifier and locks in the contract.
func TestClassifyProxyOutcome_TableDriven(t *testing.T) {
	cases := []struct {
		name                 string
		jobSuccess           bool
		jobErr               error
		reviewCircuitTripped bool
		wantReport           bool
		wantReason           proxypool.FailureReason
	}{
		{
			name:                 "clean_success",
			jobSuccess:           true,
			jobErr:               nil,
			reviewCircuitTripped: false,
			wantReport:           false,
		},
		{
			name:                 "natural_success_with_context_canceled",
			jobSuccess:           true,
			jobErr:               context.Canceled,
			reviewCircuitTripped: false,
			wantReport:           false,
		},
		{
			name:                 "natural_success_with_wrapped_context_canceled",
			jobSuccess:           true,
			jobErr:               errors.Join(context.Canceled, errors.New("wrap")),
			reviewCircuitTripped: false,
			wantReport:           false,
		},
		{
			name:                 "job_error_blames_proxy_as_network",
			jobSuccess:           false,
			jobErr:               errors.New("scrapemate exploded"),
			reviewCircuitTripped: false,
			wantReport:           true,
			wantReason:           proxypool.NetworkErr,
		},
		{
			name:                 "review_circuit_tripped_blames_proxy_as_soft_reject",
			jobSuccess:           true,
			jobErr:               nil,
			reviewCircuitTripped: true,
			wantReport:           true,
			wantReason:           proxypool.SoftReject,
		},
		{
			name:                 "review_circuit_tripped_with_context_canceled",
			jobSuccess:           true,
			jobErr:               context.Canceled,
			reviewCircuitTripped: true,
			wantReport:           true,
			wantReason:           proxypool.SoftReject,
		},
		{
			name:                 "job_failure_takes_precedence_over_review_circuit",
			jobSuccess:           false,
			jobErr:               errors.New("oops"),
			reviewCircuitTripped: true,
			wantReport:           true,
			wantReason:           proxypool.NetworkErr,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, report := classifyProxyOutcome(c.jobSuccess, c.jobErr, c.reviewCircuitTripped)
			if report != c.wantReport {
				t.Errorf("report: got %v, want %v", report, c.wantReport)
			}
			if c.wantReport && reason != c.wantReason {
				t.Errorf("reason: got %s, want %s", reason, c.wantReason)
			}
		})
	}
}
