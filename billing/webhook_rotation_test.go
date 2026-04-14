package billing

import (
	"testing"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

func TestConstructWebhookEvent_AcceptsPreviousSecret(t *testing.T) {
	svc := &Service{
		webhookSigningKeys: []string{"whsec_current", "whsec_previous"},
	}

	payload := []byte(`{
		"id":"evt_testrotation",
		"api_version":"` + stripe.APIVersion + `",
		"object":"event",
		"type":"checkout.session.completed",
		"data":{
			"object":{
				"id":"cs_testrotation",
				"object":"checkout.session",
				"payment_status":"paid"
			}
		}
	}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  "whsec_previous",
	})

	event, err := svc.constructWebhookEvent(payload, signed.Header)
	if err != nil {
		t.Fatalf("constructWebhookEvent returned error: %v", err)
	}
	if event.ID != "evt_testrotation" {
		t.Fatalf("expected event ID evt_testrotation, got %q", event.ID)
	}
	if string(event.Type) != "checkout.session.completed" {
		t.Fatalf("expected checkout.session.completed, got %q", event.Type)
	}
}

func TestCompactWebhookSecrets_RemovesBlanksAndDuplicates(t *testing.T) {
	got := compactWebhookSecrets([]string{" whsec_current ", "", "whsec_previous", "whsec_current"})

	if len(got) != 2 {
		t.Fatalf("expected 2 secrets, got %d (%v)", len(got), got)
	}
	if got[0] != "whsec_current" {
		t.Fatalf("expected first secret to be whsec_current, got %q", got[0])
	}
	if got[1] != "whsec_previous" {
		t.Fatalf("expected second secret to be whsec_previous, got %q", got[1])
	}
}
