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
	"github.com/wepie/ai-agent-arrange/internal/agent"
	"github.com/wepie/ai-agent-arrange/internal/api"
	"github.com/wepie/ai-agent-arrange/internal/orchestrator"
	"github.com/wepie/ai-agent-arrange/pkg/config"
	"github.com/wepie/ai-agent-arrange/pkg/logger"
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

	// Initialize agent registry
	registry := agent.NewRegistry()
	logger.Info("Agent registry initialized")

	// TODO: Load and register agents from configuration

	// Initialize orchestration engine
	engine := orchestrator.NewEngine(registry, cfg.Orchestrator.MaxConcurrentTasks)
	logger.Info("Orchestration engine initialized",
		zap.Int("max_workers", cfg.Orchestrator.MaxConcurrentTasks),
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
	logger.Info("Received shutdown signal")
	fmt.Println("\n🛑 Shutting down gracefully...")

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Stop HTTP server
	if err := httpServer.Stop(shutdownCtx); err != nil {
		logger.Error("Error stopping HTTP server", zap.Error(err))
	}

	// Stop the engine
	if err := engine.Stop(); err != nil {
		logger.Error("Error stopping engine", zap.Error(err))
	}

	logger.Info("Server stopped successfully")
	fmt.Println("✅ Server stopped")
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
