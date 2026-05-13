//nolint:testpackage // tests unexported cloudinit helpers directly
package cloudinit

import (
	"strings"
	"testing"
)

func TestGenerateUsesReplicaScaling(t *testing.T) {
	cfg := Config{
		DatabaseURL:     "postgres://example",
		Concurrency:     3,
		MaxJobsPerCycle: 200,
		Image:           "repo/image:tag",
	}

	got := Generate(cfg)

	mustContain(t, got, "WORKER_INSTANCES=3")
	mustContain(t, got, "CONCURRENCY=1")
	mustContain(t, got, "worker_replica:")
	mustContain(t, got, "--scale worker_replica=2")
	mustContain(t, got, `cpus: "1.5"`)
	mustContain(t, got, `mem_limit: "2g"`)
	mustContain(t, got, `mem_reservation: "1500m"`)
	mustContain(t, got, `shm_size: "1g"`)
	mustContain(t, got, `tmpfs:`)
	mustContain(t, got, `/tmp:size=512m`)
	mustContain(t, got, `ulimits:`)
	mustContain(t, got, `nofile:`)
	mustContain(t, got, `soft: 65536`)
	mustContain(t, got, `hard: 65536`)
	mustContain(t, got, `driver: json-file`)
	mustContain(t, got, `max-size: "10m"`)
	mustContain(t, got, `max-file: "3"`)
}

func TestGenerateUpdateCommandUsesReplicaScaling(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://example",
		Concurrency: 1,
		Image:       "repo/image:tag",
	}

	got := GenerateUpdateCommand(cfg)

	mustContain(t, got, "WORKER_INSTANCES=1")
	mustContain(t, got, "CONCURRENCY=1")
	mustContain(t, got, "worker_replica:")
	mustContain(t, got, "docker compose pull")
	mustContain(t, got, "--scale worker_replica=0")
	mustContain(t, got, `cpus: "1.5"`)
	mustContain(t, got, `mem_limit: "2g"`)
	mustContain(t, got, `mem_reservation: "1500m"`)
	mustContain(t, got, `shm_size: "1g"`)
	mustContain(t, got, `tmpfs:`)
	mustContain(t, got, `/tmp:size=512m`)
	mustContain(t, got, `ulimits:`)
	mustContain(t, got, `nofile:`)
	mustContain(t, got, `soft: 65536`)
	mustContain(t, got, `hard: 65536`)
	mustContain(t, got, `driver: json-file`)
	mustContain(t, got, `max-size: "10m"`)
	mustContain(t, got, `max-file: "3"`)
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()

	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected to contain %q", needle)
	}
}
