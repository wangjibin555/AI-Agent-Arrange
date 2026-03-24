package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/orchestrator"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// TaskRepository handles task persistence
type TaskRepository struct {
	db *DB
}

// NewTaskRepository creates a new task repository
func NewTaskRepository(db *DB) *TaskRepository {
	return &TaskRepository{db: db}
}

// Create creates a new task in the database
func (r *TaskRepository) Create(ctx context.Context, task *orchestrator.Task) error {
	parametersJSON, err := json.Marshal(task.Parameters)
	if err != nil {
		return fmt.Errorf("failed to marshal parameters: %w", err)
	}

	timeoutSeconds := int(task.Timeout.Seconds())

	query := `
		INSERT INTO tasks (
			id, agent_name, required_capability, action, parameters,
			priority, timeout_seconds, status, retry_count, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = r.db.ExecWithContext(ctx, query,
		task.ID,
		task.AgentName,
		task.RequiredCapability,
		task.Action,
		parametersJSON,
		task.Priority,
		timeoutSeconds,
		task.Status,
		getRetryCount(task),
		task.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert task: %w", err)
	}

	return nil
}

// Update updates an existing task
func (r *TaskRepository) Update(ctx context.Context, task *orchestrator.Task) error {
	var resultJSON []byte
	var err error

	if task.Result != nil {
		resultJSON, err = json.Marshal(task.Result)
		if err != nil {
			return fmt.Errorf("failed to marshal result: %w", err)
		}
	}

	query := `
		UPDATE tasks SET
			agent_name = ?,
			status = ?,
			result = ?,
			error = ?,
			retry_count = ?,
			started_at = ?,
			completed_at = ?
		WHERE id = ?
	`

	_, err = r.db.ExecWithContext(ctx, query,
		task.AgentName,
		task.Status,
		resultJSON,
		task.Error,
		getRetryCount(task),
		task.StartedAt,
		task.CompletedAt,
		task.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}

	return nil
}

// GetByID retrieves a task by ID
func (r *TaskRepository) GetByID(ctx context.Context, id string) (*orchestrator.Task, error) {
	query := `
		SELECT
			id, agent_name, required_capability, action, parameters,
			priority, timeout_seconds, status, result, error,
			retry_count, created_at, started_at, completed_at
		FROM tasks
		WHERE id = ?
	`

	row := r.db.QueryRowWithContext(ctx, query, id)

	var task orchestrator.Task
	var parametersJSON, resultJSON []byte
	var timeoutSeconds int
	var retryCount int

	err := row.Scan(
		&task.ID,
		&task.AgentName,
		&task.RequiredCapability,
		&task.Action,
		&parametersJSON,
		&task.Priority,
		&timeoutSeconds,
		&task.Status,
		&resultJSON,
		&task.Error,
		&retryCount,
		&task.CreatedAt,
		&task.StartedAt,
		&task.CompletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, apperr.NotFoundf("task not found: %s", id).WithCode("task_not_found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan task: %w", err)
	}

	// Unmarshal JSON fields
	if len(parametersJSON) > 0 {
		if err := json.Unmarshal(parametersJSON, &task.Parameters); err != nil {
			return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
		}
	}

	if len(resultJSON) > 0 {
		if err := json.Unmarshal(resultJSON, &task.Result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal result: %w", err)
		}
	}

	task.Timeout = time.Duration(timeoutSeconds) * time.Second
	task.RetryCount = &retryCount

	return &task, nil
}

// TaskFilter represents filter criteria for listing tasks
type TaskFilter struct {
	Status    orchestrator.TaskStatus
	AgentName string
	Limit     int
	Offset    int
}

// List retrieves tasks with filters
func (r *TaskRepository) List(filter TaskFilter) ([]*orchestrator.Task, int, error) {
	return r.listWithContext(context.Background(), filter)
}

func (r *TaskRepository) listWithContext(ctx context.Context, filter TaskFilter) ([]*orchestrator.Task, int, error) {
	// Build query
	query := `
		SELECT
			id, agent_name, required_capability, action, parameters,
			priority, timeout_seconds, status, result, error,
			retry_count, created_at, started_at, completed_at
		FROM tasks
		WHERE 1=1
	`
	countQuery := "SELECT COUNT(*) FROM tasks WHERE 1=1"
	args := []interface{}{}

	// Add filters
	if filter.Status != "" {
		query += " AND status = ?"
		countQuery += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.AgentName != "" {
		query += " AND agent_name = ?"
		countQuery += " AND agent_name = ?"
		args = append(args, filter.AgentName)
	}

	// Get total count
	var total int
	err := r.db.QueryRowWithContext(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count tasks: %w", err)
	}

	// Add pagination
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)

	// Execute query
	rows, err := r.db.QueryWithContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	tasks := []*orchestrator.Task{}
	for rows.Next() {
		var task orchestrator.Task
		var parametersJSON, resultJSON []byte
		var timeoutSeconds int
		var retryCount int

		err := rows.Scan(
			&task.ID,
			&task.AgentName,
			&task.RequiredCapability,
			&task.Action,
			&parametersJSON,
			&task.Priority,
			&timeoutSeconds,
			&task.Status,
			&resultJSON,
			&task.Error,
			&retryCount,
			&task.CreatedAt,
			&task.StartedAt,
			&task.CompletedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan task: %w", err)
		}

		// Unmarshal JSON fields
		if len(parametersJSON) > 0 {
			if err := json.Unmarshal(parametersJSON, &task.Parameters); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal parameters: %w", err)
			}
		}

		if len(resultJSON) > 0 {
			if err := json.Unmarshal(resultJSON, &task.Result); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal result: %w", err)
			}
		}

		task.Timeout = time.Duration(timeoutSeconds) * time.Second
		task.RetryCount = &retryCount

		tasks = append(tasks, &task)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating tasks: %w", err)
	}

	return tasks, total, nil
}

// GetPendingTasks retrieves all pending tasks
func (r *TaskRepository) GetPendingTasks(ctx context.Context) ([]*orchestrator.Task, error) {
	tasks, _, err := r.listWithContext(ctx, TaskFilter{
		Status: orchestrator.TaskStatusPending,
		Limit:  1000, // Get up to 1000 pending tasks
		Offset: 0,
	})
	return tasks, err
}

// GetRunningTasks retrieves all running tasks
func (r *TaskRepository) GetRunningTasks(ctx context.Context) ([]*orchestrator.Task, error) {
	tasks, _, err := r.listWithContext(ctx, TaskFilter{
		Status: orchestrator.TaskStatusRunning,
		Limit:  1000,
		Offset: 0,
	})
	return tasks, err
}

// Delete deletes a task by ID
func (r *TaskRepository) Delete(id string) error {
	query := "DELETE FROM tasks WHERE id = ?"
	_, err := r.db.ExecWithContext(context.Background(), query, id)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}
	return nil
}

// DeleteOldTasks deletes tasks older than the specified duration
func (r *TaskRepository) DeleteOldTasks(olderThan time.Duration) (int64, error) {
	query := "DELETE FROM tasks WHERE created_at < ?"
	cutoffTime := time.Now().Add(-olderThan)

	result, err := r.db.ExecWithContext(context.Background(), query, cutoffTime)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old tasks: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get affected rows: %w", err)
	}

	return affected, nil
}

// Helper function to safely get retry count
func getRetryCount(task *orchestrator.Task) int {
	if task.RetryCount == nil {
		return 0
	}
	return *task.RetryCount
}
