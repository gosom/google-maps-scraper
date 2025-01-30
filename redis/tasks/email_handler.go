package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// CreateEmailTask creates a new email extraction task with the given payload
func CreateEmailTask(payload *EmailPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal email payload: %w", err)
	}
	return asynq.NewTask(TypeEmailExtract, data), nil
}

func (h *Handler) processEmailTask(ctx context.Context, task *asynq.Task) error {
	var payload EmailPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal email payload: %w", err)
	}

	// Set default values if not provided
	if payload.MaxDepth == 0 {
		payload.MaxDepth = 2
	}
	if payload.UserAgent == "" {
		payload.UserAgent = "Mozilla/5.0"
	}

	// TODO: Implement actual email extraction logic
	return nil
} 