package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/orchestrator"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// Server represents the HTTP API server
type Server struct {
	engine *orchestrator.Engine
	router *gin.Engine
	server *http.Server
	addr   string
}

// NewServer creates a new API server
func NewServer(engine *orchestrator.Engine, host string, port int, mode string) *Server {
	// Set Gin mode
	if mode == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.New()

	// Add middleware
	router.Use(gin.Recovery())
	router.Use(LoggerMiddleware())
	router.Use(CORSMiddleware())

	addr := fmt.Sprintf("%s:%d", host, port)

	s := &Server{
		engine: engine,
		router: router,
		addr:   addr,
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

	// Setup routes
	s.setupRoutes()

	return s
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Health check
	s.router.GET("/health", s.handleHealth)
	s.router.GET("/", s.handleRoot)

	// API v1 routes
	v1 := s.router.Group("/api/v1")
	{
		// Task management
		tasks := v1.Group("/tasks")
		{
			tasks.POST("", s.handleCreateTask)
			tasks.GET("/:id", s.handleGetTask)
			tasks.GET("", s.handleListTasks)
		}

		// Smart task with auto agent selection
		v1.POST("/smart-task", s.handleCreateSmartTask)

		// Engine status
		v1.GET("/status", s.handleEngineStatus)
	}
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

// LoggerMiddleware creates a Gin middleware for logging
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)

		logger.Info("HTTP request",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", latency),
			zap.String("user-agent", c.Request.UserAgent()),
		)
	}
}

// CORSMiddleware handles Cross-Origin Resource Sharing
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
