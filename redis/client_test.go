package redis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/redis/config"
	"github.com/Vector/vector-leads-scraper/testcontainers"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient(t *testing.T) {
	testcontainers.WithTestContext(t, func(ctx *testcontainers.TestContext) {
		t.Run("creates client with valid configuration", func(t *testing.T) {
			cfg := &config.RedisConfig{
				Host:     ctx.RedisConfig.Host,
				Port:     ctx.RedisConfig.Port,
				Password: ctx.RedisConfig.Password,
			}

			client, err := NewClient(cfg)
			require.NoError(t, err)
			defer client.Close()

			// Verify client is working by enqueueing a task
			err = client.EnqueueTask(context.Background(), "test_task", []byte(`{"key": "value"}`))
			assert.NoError(t, err)
		})

		t.Run("handles task enqueueing with options", func(t *testing.T) {
			cfg := &config.RedisConfig{
				Host:     ctx.RedisConfig.Host,
				Port:     ctx.RedisConfig.Port,
				Password: ctx.RedisConfig.Password,
			}

			client, err := NewClient(cfg)
			require.NoError(t, err)
			defer client.Close()

			// Test task enqueueing with options
			err = client.EnqueueTask(
				context.Background(),
				"test_task",
				[]byte(`{"key": "value"}`),
				asynq.Queue("default"),
				asynq.ProcessIn(time.Minute),
				asynq.MaxRetry(5),
				asynq.Timeout(time.Hour),
				asynq.Unique(time.Hour),
			)
			assert.NoError(t, err)

			// Verify task was enqueued by checking queue info
			inspector := asynq.NewInspector(asynq.RedisClientOpt{
				Addr:     fmt.Sprintf("%s:%d", ctx.RedisConfig.Host, ctx.RedisConfig.Port),
				Password: ctx.RedisConfig.Password,
			})
			info, err := inspector.GetQueueInfo("default")
			require.NoError(t, err)
			assert.True(t, info.Size > 0)
		})

		t.Run("handles task retries", func(t *testing.T) {
			cfg := &config.RedisConfig{
				Host:            ctx.RedisConfig.Host,
				Port:            ctx.RedisConfig.Port,
				Password:        ctx.RedisConfig.Password,
				RetryInterval:   time.Second,
				MaxRetries:      3,
				RetentionPeriod: time.Hour,
			}

			client, err := NewClient(cfg)
			require.NoError(t, err)
			defer client.Close()

			// Enqueue a task with retry configuration
			err = client.EnqueueTask(
				context.Background(),
				"retry_task",
				[]byte(`{"key": "retry"}`),
				asynq.Queue("retry_queue"),
				asynq.MaxRetry(3),
				asynq.Timeout(time.Second),
			)
			assert.NoError(t, err)

			// Verify task is in the queue
			inspector := asynq.NewInspector(asynq.RedisClientOpt{
				Addr:     fmt.Sprintf("%s:%d", ctx.RedisConfig.Host, ctx.RedisConfig.Port),
				Password: ctx.RedisConfig.Password,
			})
			info, err := inspector.GetQueueInfo("retry_queue")
			require.NoError(t, err)
			assert.True(t, info.Size > 0)
		})

		t.Run("handles scheduled tasks", func(t *testing.T) {
			cfg := &config.RedisConfig{
				Host:     ctx.RedisConfig.Host,
				Port:     ctx.RedisConfig.Port,
				Password: ctx.RedisConfig.Password,
			}

			client, err := NewClient(cfg)
			require.NoError(t, err)
			defer client.Close()

			// Schedule a task for future processing
			processTime := time.Now().Add(time.Hour)
			err = client.EnqueueTask(
				context.Background(),
				"scheduled_task",
				[]byte(`{"key": "scheduled"}`),
				asynq.ProcessAt(processTime),
				asynq.Queue("scheduled_queue"),
			)
			assert.NoError(t, err)

			// Verify task is in the scheduled queue
			inspector := asynq.NewInspector(asynq.RedisClientOpt{
				Addr:     fmt.Sprintf("%s:%d", ctx.RedisConfig.Host, ctx.RedisConfig.Port),
				Password: ctx.RedisConfig.Password,
			})
			info, err := inspector.GetQueueInfo("scheduled_queue")
			require.NoError(t, err)
			assert.True(t, info.Scheduled > 0)
		})

		t.Run("handles connection failures", func(t *testing.T) {
			cfg := &config.RedisConfig{
				Host:     "nonexistent",
				Port:     6379,
				Password: "",
			}

			client, err := NewClient(cfg)
			assert.Error(t, err)
			assert.Nil(t, client)
		})
	})
}
