package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/orchestrator"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/workflow"
)

// 整体相当于一个处理工作任务创建的Service层
// Response represents a standard API response
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

// CreateTaskRequest represents the request body for creating a task
type CreateTaskRequest struct {
	AgentName  string                 `json:"agent_name"` // 可选：指定 Agent
	Capability string                 `json:"capability"` // 可选：指定能力（自动选择 Agent）
	Action     string                 `json:"action" binding:"required"`
	Parameters map[string]interface{} `json:"parameters"`
	Priority   int                    `json:"priority"`
	Timeout    int                    `json:"timeout"` // timeout in seconds
}

// TaskResponse represents the response for a task
type TaskResponse struct {
	ID                 string                 `json:"id"`
	AgentName          string                 `json:"agent_name,omitempty"`
	RequiredCapability string                 `json:"required_capability,omitempty"`
	Action             string                 `json:"action"`
	Parameters         map[string]interface{} `json:"parameters"`
	Priority           int                    `json:"priority"`
	Status             string                 `json:"status"`
	Result             map[string]interface{} `json:"result,omitempty"`
	Error              string                 `json:"error,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	StartedAt          *time.Time             `json:"started_at,omitempty"`
	CompletedAt        *time.Time             `json:"completed_at,omitempty"`
}

// ExecuteWorkflowRequest represents the request body for executing a workflow.
type ExecuteWorkflowRequest struct {
	Workflow  *workflow.Workflow     `json:"workflow" binding:"required"`
	Variables map[string]interface{} `json:"variables"`
}

// handleRoot handles the root endpoint
func (s *Server) handleRoot(c *gin.Context) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: "AI Agent Orchestration Server is running",
		Data: gin.H{
			"version": "0.1.0",
			"status":  "operational",
		},
	})
}

// handleHealth handles the health check endpoint
func (s *Server) handleHealth(c *gin.Context) {
	status := s.engine.GetStatus()

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data: gin.H{
			"status":    "healthy",
			"engine":    status,
			"timestamp": time.Now(),
		},
	})
}

// handleEngineStatus handles the engine status endpoint
func (s *Server) handleEngineStatus(c *gin.Context) {
	status := s.engine.GetStatus()
	stats := s.engine.GetLoadStats()
	limits := s.engine.GetTaskLimits()
	usage := s.engine.GetTaskUsage()

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data: gin.H{
			"engine": status,
			"tasks": gin.H{
				"stats":  stats,
				"limits": limits,
				"usage":  usage,
			},
		},
	})
}

// handleCreateTask handles task creation
// 支持三种模式：
// 1. 指定 agent_name - 直接使用指定的 Agent
// 2. 指定 capability - 自动选择支持该能力的 Agent（负载均衡）
// 3. 都不指定 - 从 action 推断能力，自动选择 Agent
func (s *Server) handleCreateTask(c *gin.Context) {
	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// 验证：agent_name 和 capability 不能同时指定
	if req.AgentName != "" && req.Capability != "" {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Error:   "cannot specify both 'agent_name' and 'capability', choose one",
		})
		return
	}

	// Set timeout (default: 60 seconds for normal tasks)
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second // default timeout: 60 seconds
	}
	// Maximum timeout: 5 minutes to prevent hanging tasks
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}

	handle, err := s.executionService.ExecuteTask(context.Background(), &orchestrator.TaskExecutionRequest{
		AgentName:  req.AgentName,
		Capability: req.Capability,
		Action:     req.Action,
		Parameters: req.Parameters,
		Priority:   req.Priority,
		Timeout:    timeout,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	view, err := s.executionService.GetExecution(context.Background(), handle.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// 构建响应消息
	var message string
	if req.AgentName != "" {
		message = fmt.Sprintf("Task created with agent '%s'", req.AgentName)
	} else if req.Capability != "" {
		message = fmt.Sprintf("Task created, will auto-select agent with capability '%s'", req.Capability)
	} else {
		message = "Task created, will auto-select agent based on action"
	}

	c.JSON(http.StatusCreated, Response{
		Success: true,
		Data:    view,
		Message: message,
	})
}

// handleGetTask handles getting a specific task
func (s *Server) handleGetTask(c *gin.Context) {
	taskID := c.Param("id")

	view, err := s.executionService.GetExecution(context.Background(), taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Task not found: %s", taskID),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    view,
	})
}

// handleListTasks handles listing all tasks
func (s *Server) handleListTasks(c *gin.Context) {
	// Get query parameters
	status := c.Query("status")    // 过滤状态：pending, running, completed, failed
	agentName := c.Query("agent")  // 过滤 Agent
	limitStr := c.Query("limit")   // 分页：每页数量
	offsetStr := c.Query("offset") // 分页：偏移量

	// Parse pagination parameters
	limit := 50 // 默认每页 50 条
	offset := 0

	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// Get task manager from engine
	taskManager := s.engine.GetTaskManager()

	var tasks []*orchestrator.Task

	// Filter by status if specified
	if status != "" {
		taskStatus := orchestrator.TaskStatus(status)
		tasks = taskManager.GetTasksByStatus(taskStatus)
	} else {
		// Get all tasks from all statuses
		allTasks := make([]*orchestrator.Task, 0)
		for _, st := range []orchestrator.TaskStatus{
			orchestrator.TaskStatusPending,
			orchestrator.TaskStatusRunning,
			orchestrator.TaskStatusCompleted,
			orchestrator.TaskStatusFailed,
			orchestrator.TaskStatusCancelled,
		} {
			allTasks = append(allTasks, taskManager.GetTasksByStatus(st)...)
		}
		tasks = allTasks
	}

	// Filter by agent if specified
	if agentName != "" {
		filtered := make([]*orchestrator.Task, 0)
		for _, task := range tasks {
			if task.AgentName == agentName {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}

	// Apply pagination
	total := len(tasks)
	start := offset
	end := offset + limit

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	paginatedTasks := tasks[start:end]

	// Convert to response
	responses := make([]*TaskResponse, len(paginatedTasks))
	for i, task := range paginatedTasks {
		responses[i] = convertTaskToResponse(task)
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data: gin.H{
			"tasks":  responses,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		},
	})
}

// handleCreateSmartTask is now deprecated, use POST /api/v1/tasks with "capability" field instead
// 保留此端点以保持向后兼容
func (s *Server) handleCreateSmartTask(c *gin.Context) {
	c.JSON(http.StatusGone, Response{
		Success: false,
		Error:   "This endpoint is deprecated. Use POST /api/v1/tasks with 'capability' field instead.",
		Message: "Example: {\"capability\": \"translation\", \"action\": \"translate\", \"parameters\": {...}}",
	})
}

// handleCancelTask handles task cancellation
func (s *Server) handleCancelTask(c *gin.Context) {
	taskID := c.Param("id")

	// 看任务是否存在，是否可以被取消
	task, err := s.engine.GetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Task not found: %s", taskID),
		})
		return
	}

	// 看任务是否已经完成
	if task.Status == orchestrator.TaskStatusCompleted ||
		task.Status == orchestrator.TaskStatusFailed ||
		task.Status == orchestrator.TaskStatusCancelled {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Error:   fmt.Sprintf("Cannot cancel task in '%s' status", task.Status),
			Message: "Only pending or running tasks can be cancelled",
		})
		return
	}

	// 如果以上校验都通过了，就可以进行取消了
	taskManager := s.engine.GetTaskManager()
	if err := taskManager.CancelTask(taskID); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// 将
	updatedTask, _ := s.engine.GetTask(taskID)
	response := convertTaskToResponse(updatedTask)

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    response,
		Message: "Task cancelled successfully",
	})
}

// handleExecuteWorkflow executes a workflow through the unified execution service.
func (s *Server) handleExecuteWorkflow(c *gin.Context) {
	var req ExecuteWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	handle, err := s.executionService.ExecuteWorkflow(context.Background(), &orchestrator.WorkflowExecutionRequest{
		Workflow:  req.Workflow,
		Variables: req.Variables,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	view, err := s.executionService.GetExecution(context.Background(), handle.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, Response{
		Success: true,
		Data:    view,
		Message: "Workflow execution started",
	})
}

// handleGetExecution returns a unified execution view for task or workflow.
func (s *Server) handleGetExecution(c *gin.Context) {
	executionID := c.Param("id")

	view, err := s.executionService.GetExecution(context.Background(), executionID)
	if err != nil {
		c.JSON(http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Execution not found: %s", executionID),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    view,
	})
}

// handleCancelExecution cancels a unified execution when supported by the underlying engine.
func (s *Server) handleCancelExecution(c *gin.Context) {
	executionID := c.Param("id")

	if err := s.executionService.CancelExecution(context.Background(), executionID); err != nil {
		c.JSON(http.StatusBadRequest, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	view, err := s.executionService.GetExecution(context.Background(), executionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    view,
		Message: "Execution cancelled successfully",
	})
}

// handleTaskEventStream handles SSE streaming for task events.
// The old route is kept for compatibility, but the payload is now the unified execution event model.
func (s *Server) handleTaskEventStream(c *gin.Context) {
	taskID := c.Param("id")

	// Check if task exists
	task, err := s.engine.GetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Task not found: %s", taskID),
		})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // Disable nginx buffering

	// Generate client ID
	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())

	// Subscribe to task events
	eventChannel := s.eventManager.Subscribe(taskID, clientID)
	defer s.eventManager.Unsubscribe(taskID, clientID)

	// Send initial task state
	initialEvent := ExecutionEvent{
		Type:          "connected",
		ExecutionID:   taskID,
		ExecutionType: string(orchestrator.ExecutionTypeTask),
		Status:        string(task.Status),
		Message:       "Connected to task event stream",
		Timestamp:     time.Now(),
	}
	fmt.Fprint(c.Writer, FormatSSE(initialEvent))
	c.Writer.Flush()

	// Set up heartbeat ticker
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	// Set up timeout for completed tasks
	timeoutTimer := time.NewTimer(5 * time.Minute)
	defer timeoutTimer.Stop()

	// Stream events
	for {
		select {
		case event, ok := <-eventChannel.Channel:
			if !ok {
				// Channel closed
				return
			}

			// Send event to client
			fmt.Fprint(c.Writer, FormatSSE(event))
			c.Writer.Flush()

			// Close connection if task is finished
			if event.Type == "completed" || event.Type == "failed" || event.Type == "cancelled" {
				return
			}

		case <-heartbeatTicker.C:
			// Send heartbeat comment to keep connection alive
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			c.Writer.Flush()

		case <-timeoutTimer.C:
			// Timeout - close connection
			return

		case <-c.Request.Context().Done():
			// Client disconnected
			return
		}
	}
}

// handleExecutionEventStream streams unified execution events for task or workflow executions.
func (s *Server) handleExecutionEventStream(c *gin.Context) {
	executionID := c.Param("id")

	view, err := s.executionService.GetExecution(context.Background(), executionID)
	if err != nil {
		c.JSON(http.StatusNotFound, Response{
			Success: false,
			Error:   fmt.Sprintf("Execution not found: %s", executionID),
		})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())
	eventChannel := s.eventManager.Subscribe(executionID, clientID)
	defer s.eventManager.Unsubscribe(executionID, clientID)

	initialEvent := ExecutionEvent{
		Type:                    "connected",
		ExecutionID:             executionID,
		ExecutionType:           string(view.Type),
		Status:                  string(view.Status),
		RecoveryStatus:          view.RecoveryStatus,
		SupersededByExecutionID: view.SupersededByExecutionID,
		Message:                 "Connected to execution event stream",
		Timestamp:               time.Now(),
	}
	fmt.Fprint(c.Writer, FormatSSE(initialEvent))
	c.Writer.Flush()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	timeoutTimer := time.NewTimer(5 * time.Minute)
	defer timeoutTimer.Stop()

	for {
		select {
		case event, ok := <-eventChannel.Channel:
			if !ok {
				return
			}

			fmt.Fprint(c.Writer, FormatSSE(event))
			c.Writer.Flush()

			if event.Status == string(orchestrator.ExecutionStatusCompleted) ||
				event.Status == string(orchestrator.ExecutionStatusFailed) ||
				event.Status == string(orchestrator.ExecutionStatusCancelled) {
				return
			}

		case <-heartbeatTicker.C:
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			c.Writer.Flush()

		case <-timeoutTimer.C:
			return

		case <-c.Request.Context().Done():
			return
		}
	}
}

// convertTaskToResponse converts a Task to TaskResponse
func convertTaskToResponse(task *orchestrator.Task) *TaskResponse {
	return &TaskResponse{
		ID:                 task.ID,
		AgentName:          task.AgentName,
		RequiredCapability: task.RequiredCapability,
		Action:             task.Action,
		Parameters:         task.Parameters,
		Priority:           task.Priority,
		Status:             string(task.Status),
		Result:             task.Result,
		Error:              task.Error,
		CreatedAt:          task.CreatedAt,
		StartedAt:          task.StartedAt,
		CompletedAt:        task.CompletedAt,
	}
}
