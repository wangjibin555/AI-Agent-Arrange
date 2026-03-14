package orchestrator

import (
	"context"
	"fmt"

	"github.com/wepie/ai-agent-arrange/internal/agent"
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
	for {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Wait for task availability or timeout
		if err := w.taskManager.WaitForTask(ctx); err != nil {
			if err == context.Canceled || err == context.DeadlineExceeded {
				return
			}
			// Timeout or other error, continue to next iteration
			continue
		}

		// Pull next task
		task, err := w.taskManager.PullNextTask(ctx)
		if err != nil {
			// No task available, continue
			continue
		}

		// Execute task
		w.executeTask(ctx, task)
	}
}

// executeTask executes a single task
func (w *Worker) executeTask(ctx context.Context, task *Task) {
	// Get agent (task.AgentName 已经在 PullNextTask 中分配好了)
	agentInstance, err := w.agentRegistry.Get(task.AgentName)
	if err != nil {
		w.handleTaskError(task, fmt.Errorf("failed to get agent: %w", err))
		return
	}

	// Create context with timeout
	execCtx := ctx
	if task.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, task.Timeout)
		defer cancel()
	}

	// Execute task
	input := &agent.TaskInput{
		TaskID:     task.ID,
		Action:     task.Action,
		Parameters: task.Parameters,
	}

	output, err := agentInstance.Execute(execCtx, input)
	if err != nil {
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
		w.handleTaskError(task, fmt.Errorf("failed to mark task as completed: %w", err))
		return
	}
}

// handleTaskError handles task execution errors
func (w *Worker) handleTaskError(task *Task, err error) {
	// Mark task as failed in TaskManager (with retry logic)
	if err := w.taskManager.MarkTaskAsFailed(task.ID, task.AgentName, err.Error()); err != nil {
		// Log error but don't fail silently
		fmt.Printf("Failed to mark task as failed: %v\n", err)
	}
}
