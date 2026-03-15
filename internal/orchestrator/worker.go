package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// Worker executes tasks from the queue
type Worker struct {
	id            int
	agentRegistry *agent.Registry
	taskManager   *TaskManager
}

// NewWorker creates a new worker
func NewWorker(id int, registry *agent.Registry, taskManager *TaskManager) *Worker {
	return &Worker{
		id:            id,
		agentRegistry: registry,
		taskManager:   taskManager,
	}
}

// Start starts the worker
func (w *Worker) Start(ctx context.Context) {
	logger.Info("Worker started", zap.Int("worker_id", w.id))
	defer logger.Info("Worker stopped", zap.Int("worker_id", w.id))

	for {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			logger.Info("Worker received shutdown signal", zap.Int("worker_id", w.id))
			return
		default:
		}

		// Wait for task availability or timeout
		if err := w.taskManager.WaitForTask(ctx); err != nil {
			if err == context.Canceled || err == context.DeadlineExceeded {
				logger.Info("Worker context cancelled during wait", zap.Int("worker_id", w.id))
				return
			}
			// Timeout or other error, continue to next iteration
			continue
		}

		// Check context again before pulling task
		select {
		case <-ctx.Done():
			logger.Info("Worker shutdown before pulling task", zap.Int("worker_id", w.id))
			return
		default:
		}

		// Pull next task
		task, err := w.taskManager.PullNextTask(ctx)
		if err != nil {
			// No task available, continue
			continue
		}

		// Execute task (blocks until completion)
		// This ensures we finish the current task before shutting down
		w.executeTask(ctx, task)
	}
}

// executeTask executes a single task
func (w *Worker) executeTask(ctx context.Context, task *Task) {
	logger.Info("Worker executing task",
		zap.Int("worker_id", w.id),
		zap.String("task_id", task.ID),
		zap.String("agent", task.AgentName),
		zap.String("action", task.Action),
	)

	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		logger.Info("Worker finished task",
			zap.Int("worker_id", w.id),
			zap.String("task_id", task.ID),
			zap.Duration("duration", duration),
			zap.String("status", string(task.Status)),
		)
	}()

	// Get agent (task.AgentName 已经在 PullNextTask 中分配好了)
	agentInstance, err := w.agentRegistry.Get(task.AgentName)
	if err != nil {
		w.handleTaskError(task, fmt.Errorf("failed to get agent: %w", err))
		return
	}

	// Create context with timeout
	execCtx := ctx
	var cancel context.CancelFunc
	if task.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, task.Timeout)
	} else {
		// Default timeout if not specified (60 seconds)
		execCtx, cancel = context.WithTimeout(ctx, 60*time.Second)
	}
	defer cancel()

	// Execute task
	input := &agent.TaskInput{
		TaskID:     task.ID,
		Action:     task.Action,
		Parameters: task.Parameters,
	}

	output, err := agentInstance.Execute(execCtx, input)

	// Check for timeout or cancellation
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			w.handleTaskError(task, fmt.Errorf("task execution timeout after %v", task.Timeout))
			return
		}
		if execCtx.Err() == context.Canceled {
			w.handleTaskError(task, fmt.Errorf("task execution cancelled"))
			return
		}
		w.handleTaskError(task, fmt.Errorf("task execution failed: %w", err))
		return
	}

	// Check output success
	if !output.Success {
		w.handleTaskError(task, fmt.Errorf("task failed: %s", output.Error))
		return
	}

	// Mark task as completed in TaskManager
	if err := w.taskManager.MarkTaskAsCompleted(task.ID, task.AgentName, output.Result); err != nil {
		logger.Error("Failed to mark task as completed",
			zap.Int("worker_id", w.id),
			zap.String("task_id", task.ID),
			zap.Error(err),
		)
		return
	}

	logger.Info("Task completed successfully",
		zap.Int("worker_id", w.id),
		zap.String("task_id", task.ID),
	)
}

// handleTaskError handles task execution errors
func (w *Worker) handleTaskError(task *Task, err error) {
	logger.Error("Task execution error",
		zap.Int("worker_id", w.id),
		zap.String("task_id", task.ID),
		zap.String("agent", task.AgentName),
		zap.Error(err),
	)

	// Mark task as failed in TaskManager (with retry logic)
	if markErr := w.taskManager.MarkTaskAsFailed(task.ID, task.AgentName, err.Error()); markErr != nil {
		logger.Error("Failed to mark task as failed",
			zap.Int("worker_id", w.id),
			zap.String("task_id", task.ID),
			zap.Error(markErr),
		)
	}
}
