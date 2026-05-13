//nolint:testpackage // tests unexported command builder helper directly
package cmdupdate

import (
	"strings"
	"testing"
)

func TestBuildWorkerUpdateCommandPreservesReplicaScaleFromEnv(t *testing.T) {
	cmd := buildWorkerUpdateCommand()

	checks := []string{
		"WORKER_INSTANCES",
		"worker_instances=$(sed -n 's/^WORKER_INSTANCES=//p' .env | tail -n1)",
		"docker compose up -d --remove-orphans --scale worker_replica=$worker_replicas",
	}

	for _, check := range checks {
		if !strings.Contains(cmd, check) {
			t.Fatalf("expected update command to contain %q, got: %s", check, cmd)
		}
	}
}
