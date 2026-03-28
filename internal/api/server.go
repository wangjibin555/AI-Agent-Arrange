package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/orchestrator"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/workflow"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/midware"
	"go.uber.org/zap"
)

// Server represents the HTTP API server
type Server struct {
	engine           *orchestrator.Engine
	executionService *orchestrator.ExecutionService
	router           *gin.Engine
	server           *http.Server
	addr             string
	eventManager     *EventStreamManager
	metrics          *monitor.Metrics
	errors           *ginErrorAdapter
}

// NewServer creates a new API server
func NewServer(engine *orchestrator.Engine, host string, port int, mode string) *Server {
	return NewServerWithWorkflow(engine, nil, host, port, mode)
}

// NewServerWithWorkflow creates a new API server with unified execution support.
func NewServerWithWorkflow(engine *orchestrator.Engine, workflowEngine workflowRunner, host string, port int, mode string) *Server {
	// Set Gin mode
	if mode == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.New()
	metrics := monitor.New(monitor.Config{Namespace: "ai_agent_arrange"})

	addr := fmt.Sprintf("%s:%d", host, port)

	s := &Server{
		engine:           engine,
		executionService: orchestrator.NewExecutionService(engine, workflowEngine),
		router:           router,
		addr:             addr,
		metrics:          metrics,
		errors:           newGinErrorAdapter(),
		eventManager:     NewEventStreamManager(),
		server: &http.Server{
			Addr:    addr,
			Handler: router,
			// Increase timeouts to handle long-running tasks
			// ReadTimeout: time to read the request (including body)
			ReadTimeout: 30 * time.Second,
			// WriteTimeout: time to write the response
			// Set to 5 minutes to allow for long-running tasks
			WriteTimeout: 5 * time.Minute,
			// IdleTimeout: time to keep connection alive
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20, // 1MB
		},
	}
	s.executionService.SetMetrics(metrics.Task)
	if s.engine != nil {
		s.engine.SetMetrics(metrics.Task)
	}
	if workflowMetricsSetter, ok := any(workflowEngine).(interface {
		SetMetrics(*monitor.WorkflowMetrics)
	}); ok {
		workflowMetricsSetter.SetMetrics(metrics.Workflow)
	}
	s.eventManager.SetMetrics(metrics.SSE)

	// Add middleware
	router.Use(s.errors.Recovery())
	router.Use(midware.RequestContextMiddleware())
	router.Use(s.MetricsMiddleware())
	router.Use(midware.AccessLogMiddleware())
	router.Use(midware.CORSMiddleware())

	// Setup routes
	s.setupRoutes()

	return s
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Health check
	s.router.GET("/health", s.handleHealth)
	s.router.GET("/metrics", gin.WrapH(s.metrics.Handler()))
	s.router.GET("/", s.handleRoot)

	// API v1 routes
	v1 := s.router.Group("/api/v1")
	{
		//任务建立
		tasks := v1.Group("/tasks")
		{
			tasks.POST("", s.handleCreateTask)
			tasks.GET("/:id", s.handleGetTask)
			tasks.GET("", s.handleListTasks)
			tasks.DELETE("/:id", s.handleCancelTask)          // Cancel task
			tasks.GET("/:id/stream", s.handleTaskEventStream) // SSE stream
		}

		// 查询
		executions := v1.Group("/executions")
		{
			executions.GET("/:id", s.handleGetExecution)
			executions.DELETE("/:id", s.handleCancelExecution)
			executions.GET("/:id/stream", s.handleExecutionEventStream)
		}

		//工作流建立
		workflows := v1.Group("/workflows")
		{
			workflows.POST("/execute", s.handleExecuteWorkflow)
		}

		agents := v1.Group("/agents")
		{
			agents.GET("", s.handleListAgents)
		}

		// Smart task with auto agent selection
		v1.POST("/smart-task", s.handleCreateSmartTask)

		// Engine status
		v1.GET("/status", s.handleEngineStatus)
	}
}

type workflowRunner interface {
	Execute(ctx context.Context, workflow *workflow.Workflow, variables map[string]interface{}) (*workflow.WorkflowExecution, error)
	GetExecution(ctx context.Context, executionID string) (*workflow.WorkflowExecution, error)
	CancelExecution(ctx context.Context, executionID string) error
}

// Start starts the HTTP server
func (s *Server) Start() error {
	logger.Info("Starting HTTP server", zap.String("addr", s.addr))

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Failed to start HTTP server", zap.Error(err))
		}
	}()

	return nil
}

// Stop gracefully stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	logger.Info("Stopping HTTP server")
	return s.server.Shutdown(ctx)
}

// GetEventPublisher returns the unified event publisher for external integration.
func (s *Server) GetEventPublisher() *UnifiedEventPublisher {
	return NewUnifiedEventPublisher(s.eventManager)
}

func (s *Server) Metrics() *monitor.Metrics {
	return s.metrics
}

func (s *Server) MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.metrics == nil {
			c.Next()
			return
		}

		start := time.Now()
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		s.metrics.HTTP.IncInflight(route, c.Request.Method)
		defer s.metrics.HTTP.DecInflight(route, c.Request.Method)

		c.Next()

		s.metrics.HTTP.Observe(route, c.Request.Method, strconv.Itoa(c.Writer.Status()), time.Since(start))
	}
}
