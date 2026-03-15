package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/api"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/orchestrator"
	mysqlStorage "github.com/wangjibin555/AI-Agent-Arrange/internal/storage/mysql"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/config"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "Path to configuration file")
	version    = "0.1.0"
)

func main() {
	flag.Parse()

	fmt.Printf("AI Agent Orchestration Server v%s\n", version)
	fmt.Println("========================================")

	// Load .env file (optional, won't fail if not exists)
	if err := godotenv.Load(); err != nil {
		fmt.Println("Note: .env file not found, using environment variables or defaults")
	}

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	if err := initLogger(cfg); err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("Starting AI Agent Orchestration Server",
		zap.String("version", version),
		zap.String("mode", cfg.Server.Mode),
	)

	// Print configuration
	config.Print(cfg)

	// Create context that listens for interrupt signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize MySQL database (optional - for task persistence)
	var db *mysqlStorage.DB
	var taskRepo orchestrator.TaskRepository
	if shouldEnableMySQL() {
		mysqlCfg := mysqlStorage.Config{
			Host:     getEnv("MYSQL_HOST", "localhost"),
			Port:     getEnvInt("MYSQL_PORT", 3306),
			User:     getEnv("MYSQL_USER", "root"),
			Password: getEnv("MYSQL_PASSWORD", ""),
			Database: getEnv("MYSQL_DATABASE", "ai_agent_arrange"),
		}

		var dbErr error
		db, dbErr = mysqlStorage.NewDB(mysqlCfg)
		if dbErr != nil {
			logger.Fatal("Failed to connect to MySQL", zap.Error(dbErr))
		}
		defer db.Close()

		// Run migrations
		if dbErr := db.Migrate(); dbErr != nil {
			logger.Fatal("Failed to run database migrations", zap.Error(dbErr))
		}

		// Create task repository
		taskRepo = mysqlStorage.NewTaskRepository(db)
		logger.Info("✅ MySQL persistence enabled",
			zap.String("host", mysqlCfg.Host),
			zap.String("database", mysqlCfg.Database),
		)
	} else {
		logger.Info("⚠️  MySQL persistence disabled (tasks stored in memory only)")
	}

	// Initialize agent registry
	registry := agent.NewRegistry()
	logger.Info("Agent registry initialized")

	// Register agents
	if err := registerAgents(registry); err != nil {
		logger.Fatal("Failed to register agents", zap.Error(err))
	}

	// Initialize orchestration engine with persistence
	engine := orchestrator.NewEngineWithRepository(registry, cfg.Orchestrator.MaxConcurrentTasks, taskRepo)
	logger.Info("Orchestration engine initialized",
		zap.Int("max_workers", cfg.Orchestrator.MaxConcurrentTasks),
		zap.Bool("persistence", taskRepo != nil),
	)

	// Start the engine
	if err := engine.Start(ctx); err != nil {
		logger.Fatal("Failed to start engine", zap.Error(err))
	}

	// Initialize HTTP server
	httpServer := api.NewServer(engine, cfg.Server.Host, cfg.Server.Port, cfg.Server.Mode)
	if err := httpServer.Start(); err != nil {
		logger.Fatal("Failed to start HTTP server", zap.Error(err))
	}

	logger.Info("Server started successfully",
		zap.String("address", fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
	)
	fmt.Printf("\n✅ Server is running on http://%s:%d\n", cfg.Server.Host, cfg.Server.Port)
	fmt.Println("📡 API endpoints:")
	fmt.Println("   - Health check: GET /health")
	fmt.Println("   - Create task:  POST /api/v1/tasks")
	fmt.Println("   - Engine status: GET /api/v1/status")
	fmt.Println("\nPress Ctrl+C to stop\n")

	// Wait for interrupt signal
	<-sigChan
	logger.Info("Received shutdown signal, initiating graceful shutdown")
	fmt.Println("\n🛑 Shutting down gracefully...")

	// Step 1: Stop accepting new HTTP requests
	// Create shutdown context with timeout for HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	fmt.Println("   ⏳ Stopping HTTP server...")
	if err := httpServer.Stop(shutdownCtx); err != nil {
		logger.Error("Error stopping HTTP server", zap.Error(err))
		fmt.Printf("   ❌ HTTP server stop error: %v\n", err)
	} else {
		fmt.Println("   ✅ HTTP server stopped")
	}

	// Step 2: Stop the orchestration engine
	// This will wait for workers to finish their current tasks
	fmt.Println("   ⏳ Stopping orchestration engine (waiting for tasks to complete)...")
	if err := engine.Stop(); err != nil {
		logger.Error("Error stopping engine", zap.Error(err))
		fmt.Printf("   ❌ Engine stop error: %v\n", err)
	} else {
		fmt.Println("   ✅ Orchestration engine stopped")
	}

	logger.Info("Server shutdown completed")
	fmt.Println("✅ Server stopped gracefully")
}

// loadConfig loads and validates configuration
func loadConfig(configPath string) (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	// Merge with environment variables (env vars take precedence)
	config.MergeWithEnv(cfg)

	return cfg, nil
}

// initLogger initializes the logger
func initLogger(cfg *config.Config) error {
	loggerConfig := logger.Config{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
		Output: cfg.Logging.Output,
		File:   cfg.Logging.FilePath,
	}

	return logger.Init(loggerConfig)
}

// registerAgents registers all agents (Echo, OpenAI, DeepSeek)
func registerAgents(registry *agent.Registry) error {
	// 1. Register EchoAgents for testing
	if err := registerEchoAgents(registry); err != nil {
		return err
	}

	// 2. Register OpenAI Agent (for complex tasks)
	if err := registerOpenAIAgents(registry); err != nil {
		logger.Warn("Failed to register OpenAI agents", zap.Error(err))
		// 不中断启动，允许没有 OpenAI API Key 的情况
	}

	// 3. Register DeepSeek Agent (for lightweight tasks)
	if err := registerDeepSeekAgents(registry); err != nil {
		logger.Warn("Failed to register DeepSeek agents", zap.Error(err))
		// 不中断启动，允许没有 DeepSeek API Key 的情况
	}

	return nil
}

// registerEchoAgents registers test EchoAgent instances
func registerEchoAgents(registry *agent.Registry) error {
	// Create multiple EchoAgent instances for testing load balancing
	agents := []struct {
		name  string
		delay int // milliseconds
	}{
		{"echo-agent-1", 100},
		{"echo-agent-2", 150},
		{"echo-agent-3", 200},
	}

	for _, agentInfo := range agents {
		// Create agent
		echoAgent := agent.NewEchoAgent(agentInfo.name)

		// Configure agent
		config := &agent.Config{
			Name:        agentInfo.name,
			Type:        "echo",
			Description: fmt.Sprintf("Echo agent with %dms delay", agentInfo.delay),
			Capabilities: []string{
				"text-processing",
				"health-check",
			},
			Settings: map[string]interface{}{
				"delay_ms": agentInfo.delay,
			},
		}

		if err := echoAgent.Init(config); err != nil {
			return fmt.Errorf("failed to initialize %s: %w", agentInfo.name, err)
		}

		// Register agent
		if err := registry.Register(echoAgent); err != nil {
			return fmt.Errorf("failed to register %s: %w", agentInfo.name, err)
		}

		logger.Info("Registered agent",
			zap.String("name", agentInfo.name),
			zap.String("type", "echo"),
			zap.Strings("capabilities", echoAgent.GetCapabilities()),
		)
	}

	logger.Info("All EchoAgents registered successfully",
		zap.Int("count", len(agents)),
	)

	return nil
}

// registerOpenAIAgents registers OpenAI agents for complex tasks
func registerOpenAIAgents(registry *agent.Registry) error {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" || apiKey == "your-openai-api-key-here" {
		return fmt.Errorf("OPENAI_API_KEY not set in environment")
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-3.5-turbo" // 默认模型
	}

	// Create OpenAI agent
	openaiAgent := agent.NewOpenAIAgent("openai-gpt-agent", apiKey)

	// Configure agent
	config := &agent.Config{
		Name:        "openai-gpt-agent",
		Type:        "openai",
		Description: "OpenAI GPT agent for complex tasks: coding, reasoning, debugging",
		Capabilities: []string{
			"code-generation",
			"code-review",
			"debugging",
			"complex-reasoning",
			"translation",
			"text-generation",
		},
		Settings: map[string]interface{}{
			"model":       model,
			"temperature": 0.7,
			"max_tokens":  2000,
		},
	}

	if err := openaiAgent.Init(config); err != nil {
		return fmt.Errorf("failed to initialize OpenAI agent: %w", err)
	}

	// Register agent
	if err := registry.Register(openaiAgent); err != nil {
		return fmt.Errorf("failed to register OpenAI agent: %w", err)
	}

	logger.Info("Registered agent",
		zap.String("name", "openai-gpt-agent"),
		zap.String("type", "openai"),
		zap.String("model", model),
		zap.Strings("capabilities", openaiAgent.GetCapabilities()),
	)

	return nil
}

// registerDeepSeekAgents registers DeepSeek agents for lightweight tasks
func registerDeepSeekAgents(registry *agent.Registry) error {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" || apiKey == "your-deepseek-api-key-here" {
		return fmt.Errorf("DEEPSEEK_API_KEY not set in environment")
	}

	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-chat" // 默认模型
	}

	// Create DeepSeek agent
	deepseekAgent := agent.NewDeepSeekAgent("deepseek-chat-agent", apiKey)

	// Configure agent
	config := &agent.Config{
		Name:        "deepseek-chat-agent",
		Type:        "deepseek",
		Description: "DeepSeek agent for lightweight tasks: Q&A, summarization, knowledge",
		Capabilities: []string{
			"question-answering",
			"summarization",
			"knowledge-extraction",
			"text-generation",
			"conversation",
		},
		Settings: map[string]interface{}{
			"model":       model,
			"temperature": 0.7,
			"max_tokens":  2000,
		},
	}

	if err := deepseekAgent.Init(config); err != nil {
		return fmt.Errorf("failed to initialize DeepSeek agent: %w", err)
	}

	// Register agent
	if err := registry.Register(deepseekAgent); err != nil {
		return fmt.Errorf("failed to register DeepSeek agent: %w", err)
	}

	logger.Info("Registered agent",
		zap.String("name", "deepseek-chat-agent"),
		zap.String("type", "deepseek"),
		zap.String("model", model),
		zap.Strings("capabilities", deepseekAgent.GetCapabilities()),
	)

	return nil
}
