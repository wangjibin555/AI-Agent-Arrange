package orchestrator

import (
	"context"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/midware"
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
	if w.taskManager != nil && w.taskManager.metrics != nil {
		w.taskManager.metrics.ObserveWorkerActive(w.id, true)
		defer w.taskManager.metrics.ObserveWorkerActive(w.id, false)
	}

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
		w.handleTaskError(task, apperr.NotFoundf("agent not found: %s", task.AgentName).
			WithCode("task_agent_not_found").
			WithCause(err).
			WithDetail("agent_name", task.AgentName))
		return
	}

	// Create context with timeout
	execCtx := ctx
	if len(task.RequestMetadata) > 0 {
		execCtx = midware.WithRequestMetadata(execCtx, midware.RequestMetadataFromMap(task.RequestMetadata))
	}
	var cancel context.CancelFunc
	if task.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(execCtx, task.Timeout)
	} else {
		// Default timeout if not specified (60 seconds)
		execCtx, cancel = context.WithTimeout(execCtx, 60*time.Second)
	}
	defer cancel()

	// Execute task
	input := &agent.TaskInput{
		TaskID:         task.ID,
		Action:         task.Action,
		Parameters:     task.Parameters,
		EventPublisher: w.taskManager.GetEventPublisher(), // 传递事件发布器
	}

	output, err := agentInstance.Execute(execCtx, input)

	// Check for timeout or cancellation
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			if w.taskManager != nil && w.taskManager.metrics != nil {
				w.taskManager.metrics.ObserveTaskTimeout(task.AgentName, task.Action)
				w.taskManager.metrics.ObserveTaskFinished(task.AgentName, task.Action, "timeout", time.Since(startTime))
			}
			w.handleTaskError(task, apperr.Internal("task execution timed out").
				WithCode("task_execution_timeout").
				WithCause(execCtx.Err()).
				WithDetail("timeout", effectiveTimeout(task.Timeout).String()))
			return
		}
		if execCtx.Err() == context.Canceled {
			if w.taskManager != nil && w.taskManager.metrics != nil {
				w.taskManager.metrics.ObserveTaskFinished(task.AgentName, task.Action, "canceled", time.Since(startTime))
			}
			w.handleTaskError(task, apperr.Conflict("task execution canceled").
				WithCode("task_execution_canceled").
				WithCause(execCtx.Err()))
			return
		}
		if w.taskManager != nil && w.taskManager.metrics != nil {
			w.taskManager.metrics.ObserveTaskFinished(task.AgentName, task.Action, "failed", time.Since(startTime))
		}
		w.handleTaskError(task, apperr.Wrap(err, apperr.KindInternal, "task_execution_failed", "task execution failed"))
		return
	}

	// Check output success
	if !output.Success {
		if w.taskManager != nil && w.taskManager.metrics != nil {
			w.taskManager.metrics.ObserveTaskFinished(task.AgentName, task.Action, "failed", time.Since(startTime))
		}
		w.handleTaskError(task, apperr.Internal("task execution failed").
			WithCode("task_execution_failed").
			WithDetail("agent_error", output.Error))
		return
	}

	// Mark task as completed in TaskManager
	if err := w.taskManager.MarkTaskAsCompleted(execCtx, task.ID, task.AgentName, output.Result); err != nil {
		logger.Error("Failed to mark task as completed",
			zap.Int("worker_id", w.id),
			zap.String("task_id", task.ID),
			zap.Error(err),
		)
		return
	}
	if w.taskManager != nil && w.taskManager.metrics != nil {
		w.taskManager.metrics.ObserveTaskFinished(task.AgentName, task.Action, "success", time.Since(startTime))
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
	if markErr := w.taskManager.MarkTaskAsFailed(context.Background(), task.ID, task.AgentName, err.Error()); markErr != nil {
		logger.Error("Failed to mark task as failed",
			zap.Int("worker_id", w.id),
			zap.String("task_id", task.ID),
			zap.Error(markErr),
		)
	}
}

func effectiveTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return 60 * time.Second
}
