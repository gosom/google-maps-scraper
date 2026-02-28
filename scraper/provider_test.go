//nolint:testpackage // tests unexported provider details directly
package scraper

import (
	"context"
	"testing"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"
)

func TestProviderPushAfterCloseReturnsError(t *testing.T) {
	p := NewProvider(1)
	p.Close()

	err := p.Push(context.Background(), &scrapemate.Job{ID: "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider closed")
}

func TestProviderSubmitAfterCloseReturnsError(t *testing.T) {
	p := NewProvider(1)
	p.Close()

	err := p.Submit(context.Background(), &scrapemate.Job{ID: "root"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider closed")
}

func TestProviderCloseIsIdempotent(t *testing.T) {
	p := NewProvider(1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		p.Close()
		p.Close()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("provider close did not finish")
	}
}

func TestProviderFIFOOrder(t *testing.T) {
	p := NewProvider(8)
	defer p.Close()

	ctx := context.Background()
	jobsCh, _ := p.Jobs(ctx)

	require.NoError(t, p.Submit(ctx, &scrapemate.Job{ID: "root-1"}))
	require.NoError(t, p.Push(ctx, &scrapemate.Job{ID: "child-1", ParentID: "root-1"}))
	require.NoError(t, p.Submit(ctx, &scrapemate.Job{ID: "root-2"}))

	got := make([]string, 0, 3)
	deadline := time.After(2 * time.Second)

	for len(got) < 3 {
		select {
		case job := <-jobsCh:
			got = append(got, job.GetID())
		case <-deadline:
			t.Fatal("timed out waiting for provider dispatch")
		}
	}

	require.Equal(t, []string{"root-1", "child-1", "root-2"}, got)
}
