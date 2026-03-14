package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load loads configuration from a YAML file
func Load(configPath string) (*Config, error) {
	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found: %s", configPath)
	}

	// Read file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Expand environment variables
	content := expandEnvVars(string(data))

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate configuration
	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// LoadDefault loads configuration from default path
func LoadDefault() (*Config, error) {
	// Try multiple default paths
	defaultPaths := []string{
		"configs/config.yaml",
		"./config.yaml",
		"/etc/ai-agent-arrange/config.yaml",
	}

	for _, path := range defaultPaths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			continue
		}

		if _, err := os.Stat(absPath); err == nil {
			return Load(absPath)
		}
	}

	return nil, fmt.Errorf("no config file found in default paths")
}

// expandEnvVars expands environment variables in the format ${VAR_NAME} or ${VAR_NAME:-default}
func expandEnvVars(content string) string {
	return os.Expand(content, func(key string) string {
		// Support ${VAR:-default} syntax
		if idx := strings.Index(key, ":-"); idx > 0 {
			envKey := key[:idx]
			defaultValue := key[idx+2:]
			if value := os.Getenv(envKey); value != "" {
				return value
			}
			return defaultValue
		}
		return os.Getenv(key)
	})
}

// validate validates the configuration
func validate(config *Config) error {
	// Server validation
	if config.Server.Port <= 0 || config.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", config.Server.Port)
	}

	if config.Server.Mode != "development" && config.Server.Mode != "production" {
		return fmt.Errorf("invalid server mode: %s (must be 'development' or 'production')", config.Server.Mode)
	}

	// Redis validation
	if config.Redis.Host == "" {
		return fmt.Errorf("redis host is required")
	}

	// MySQL validation
	if config.MySQL.Host == "" {
		return fmt.Errorf("mysql host is required")
	}
	if config.MySQL.Database == "" {
		return fmt.Errorf("mysql database is required")
	}

	// RabbitMQ validation
	if config.RabbitMQ.Host == "" {
		return fmt.Errorf("rabbitmq host is required")
	}

	// LLM validation
	if config.LLM.DefaultProvider == "" {
		return fmt.Errorf("default LLM provider is required")
	}

	// Logging validation
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[config.Logging.Level] {
		return fmt.Errorf("invalid logging level: %s", config.Logging.Level)
	}

	return nil
}

// MergeWithEnv merges config with environment variables
// Environment variables take precedence over config file
func MergeWithEnv(config *Config) {
	if host := os.Getenv("SERVER_HOST"); host != "" {
		config.Server.Host = host
	}
	if port := os.Getenv("SERVER_PORT"); port != "" {
		fmt.Sscanf(port, "%d", &config.Server.Port)
	}
	if mode := os.Getenv("SERVER_MODE"); mode != "" {
		config.Server.Mode = mode
	}

	if redisHost := os.Getenv("REDIS_HOST"); redisHost != "" {
		config.Redis.Host = redisHost
	}
	if redisPort := os.Getenv("REDIS_PORT"); redisPort != "" {
		fmt.Sscanf(redisPort, "%d", &config.Redis.Port)
	}

	if mysqlHost := os.Getenv("MYSQL_HOST"); mysqlHost != "" {
		config.MySQL.Host = mysqlHost
	}
	if mysqlPort := os.Getenv("MYSQL_PORT"); mysqlPort != "" {
		fmt.Sscanf(mysqlPort, "%d", &config.MySQL.Port)
	}

	if rabbitmqHost := os.Getenv("RABBITMQ_HOST"); rabbitmqHost != "" {
		config.RabbitMQ.Host = rabbitmqHost
	}
}

// Print prints configuration in a readable format
func Print(config *Config) {
	fmt.Println("=== AI Agent Arrange Configuration ===")
	fmt.Printf("Server: %s:%d (mode: %s)\n", config.Server.Host, config.Server.Port, config.Server.Mode)
	fmt.Printf("Redis: %s:%d\n", config.Redis.Host, config.Redis.Port)
	fmt.Printf("MySQL: %s:%d/%s\n", config.MySQL.Host, config.MySQL.Port, config.MySQL.Database)
	fmt.Printf("RabbitMQ: %s:%d\n", config.RabbitMQ.Host, config.RabbitMQ.Port)
	fmt.Printf("LLM Provider: %s\n", config.LLM.DefaultProvider)
	fmt.Printf("Logging: level=%s format=%s\n", config.Logging.Level, config.Logging.Format)

	// Mask sensitive information
	providers := make([]string, 0)
	for name := range config.LLM.Providers {
		providers = append(providers, name)
	}
	if len(providers) > 0 {
		fmt.Printf("Available LLM Providers: %s\n", strings.Join(providers, ", "))
	}
	fmt.Println("=====================================")
}
