package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/wepie/ai-agent-arrange/internal/orchestrator"
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

	// Create task
	taskID := uuid.New().String()
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second // default timeout
	}

	task := &orchestrator.Task{
		ID:                 taskID,
		AgentName:          req.AgentName,  // 如果指定了就用，否则留空自动选择
		RequiredCapability: req.Capability, // 如果指定了能力
		Action:             req.Action,
		Parameters:         req.Parameters,
		Priority:           req.Priority,
		Timeout:            timeout,
		Status:             orchestrator.TaskStatusPending,
		CreatedAt:          time.Now(),
	}

	// Submit task to engine
	// TaskManager 会在 PullNextTask 时自动选择合适的 Agent
	if err := s.engine.SubmitTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, Response{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Convert to response
	response := convertTaskToResponse(task)

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
		Data:    response,
		Message: message,
	})
}

// handleGetTask handles getting a specific task
func (s *Server) handleGetTask(c *gin.Context) {
	_ = c.Param("id") // taskID will be used after implementing storage

	// TODO: Implement task retrieval from storage
	// For now, return a placeholder response
	c.JSON(http.StatusNotImplemented, Response{
		Success: false,
		Error:   "Task retrieval not yet implemented",
		Message: "This endpoint will be available after implementing task storage",
	})
}

// handleListTasks handles listing all tasks
func (s *Server) handleListTasks(c *gin.Context) {
	// TODO: Implement task listing from storage
	// For now, return a placeholder response
	c.JSON(http.StatusNotImplemented, Response{
		Success: false,
		Error:   "Task listing not yet implemented",
		Message: "This endpoint will be available after implementing task storage",
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
