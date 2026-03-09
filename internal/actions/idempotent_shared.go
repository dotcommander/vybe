package actions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

type createWithEventResult[T any] struct {
	Value   T     `json:"value"`
	EventID int64 `json:"event_id"`
}

type eventResult struct {
	EventID int64 `json:"event_id"`
}

func validateAgentRequest(agentName, requestID string) error {
	if agentName == "" {
		return errors.New("agent name is required")
	}
	if requestID == "" {
		return errors.New("request id is required")
	}
	return nil
}

func validateTaskID(taskID string) error {
	if taskID == "" {
		return errors.New("task ID is required")
	}
	return nil
}

func runCreateWithEvent[T any](
	db *sql.DB,
	agentName, requestID, command, action string,
	operation func(tx *sql.Tx) (T, int64, error),
) (*T, int64, error) {
	if err := validateAgentRequest(agentName, requestID); err != nil {
		return nil, 0, err
	}

	r, err := store.RunIdempotent(context.Background(), db, agentName, requestID, command, func(tx *sql.Tx) (createWithEventResult[T], error) {
		value, eventID, err := operation(tx)
		if err != nil {
			return createWithEventResult[T]{}, err
		}
		return createWithEventResult[T]{Value: value, EventID: eventID}, nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to %s: %w", action, err)
	}

	value := r.Value
	return &value, r.EventID, nil
}

func retryOnVersionConflict(err error) bool {
	return errors.Is(err, store.ErrVersionConflict)
}

func retryOnResumeConflict(err error) bool {
	return errors.Is(err, store.ErrIdempotencyInProgress) ||
		errors.Is(err, store.ErrVersionConflict) ||
		store.IsVersionConflict(err)
}

func runTaskMutationWithRetry[T any](
	db *sql.DB,
	agentName, requestID, taskID, command, taskState string,
	operation func(tx *sql.Tx) (T, error),
) (*models.Task, T, error) {
	var zero T

	if err := validateAgentRequest(agentName, requestID); err != nil {
		return nil, zero, err
	}
	if err := validateTaskID(taskID); err != nil {
		return nil, zero, err
	}

	result, _, err := store.RunIdempotentWithRetry(
		context.Background(),
		db,
		agentName,
		requestID,
		command,
		3,
		retryOnVersionConflict,
		operation,
	)
	if err != nil {
		return nil, zero, err
	}

	task, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, zero, fmt.Errorf("failed to fetch %s task: %w", taskState, err)
	}

	return task, result, nil
}
